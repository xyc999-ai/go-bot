// Package controller manages account Hosts and proxies the public HTTP API.
package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
	"go-bot/extensions/catalog"
	"go-bot/internal/config"
	proactive "go-bot/internal/proactivesettings"
	"go-bot/plugin"
	dock "go-bot/providers/runtime/docker"
	cloud "go-bot/providers/runtime/eci"
	"go-bot/runtime"
	"go-bot/thirdparty/aliyun"
)

const (
	running = "running"
	stopped = "stopped"
)

var userName = regexp.MustCompile("^[a-z0-9][a-z0-9_-]{0,63}$")
var errInvalidAccount = errors.New("use 1–64 lowercase letters, digits, hyphens or underscores; start with a letter or digit")
var errHostUnavailable = errors.New("Host is unavailable; open the account to start it")

type Manager struct {
	Config config.Config
	Driver runtime.Driver
	Store  AccountStore
	Client *http.Client

	accountLocks  [64]sync.Mutex
	pluginLocks   [64]sync.Mutex
	containerized bool
	pluginCatalog []catalog.Entry
}

func InContainer() bool { return os.Getenv("CONTROLLER_CONTAINER") == "1" }

func Driver(c config.Config) (runtime.Driver, error) {
	factories := runtime.RegistryDrivers{"docker": func() (runtime.Driver, error) {
		driver := dock.New()
		if InContainer() {
			driver.PublishedHost = "host.docker.internal"
		}
		return driver, nil
	}, "eci": func() (runtime.Driver, error) {
		client, err := aliyun.NewECI(c.Runtime.ECI.Region, aliyun.Credentials{AccessKeyID: c.Cloud.Aliyun.Credentials.AccessKeyID, AccessKeySecret: c.Cloud.Aliyun.Credentials.AccessKeySecret})
		return &cloud.Driver{Client: client}, err
	}}
	factory, ok := factories[c.Runtime.Provider]
	if !ok {
		return nil, errors.New("unknown runtime provider")
	}
	return factory()
}

func New(c config.Config, driver runtime.Driver, store AccountStore) *Manager {
	return &Manager{
		Config: c, Driver: driver, Store: store,
		containerized: InContainer(),
		pluginCatalog: catalog.Entries(),
		Client:        &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func (m *Manager) accountLock(id string) *sync.Mutex {
	return &m.accountLocks[crc32.ChecksumIEEE([]byte(id))%uint32(len(m.accountLocks))]
}

// Package downloads serialize only plugin mutations, never ordinary account work.
func (m *Manager) pluginLock(id string) *sync.Mutex {
	return &m.pluginLocks[crc32.ChecksumIEEE([]byte(id))%uint32(len(m.pluginLocks))]
}

func (m *Manager) CreateAccount(ctx context.Context, id string) error {
	if !userName.MatchString(id) {
		return errInvalidAccount
	}
	defaults := plugin.Settings{}.Clone()
	for _, release := range m.pluginCatalog {
		if release.DefaultEnabled {
			defaults.Packages[release.Package] = plugin.PackageSelection{CatalogID: release.ID, Version: release.Version, Enabled: true}
		}
	}
	return m.Store.Create(ctx, id, Account{DesiredState: stopped, MainSessionID: uuid.NewString(), Plugins: &defaults, Proactive: &proactive.Settings{Enabled: false}})
}

func (m *Manager) spec(id string, record Account) runtime.Spec {
	c := m.Config
	spec := runtime.Spec{
		DeploymentID: id, RunID: record.HostID,
		Environment:  c.HostEnvironment(id, record.HostID, record.HostToken),
		Storage:      runtime.Storage{Type: c.Storage.Type},
		StartTimeout: c.Runtime.StartTimeout,
		Image:        c.Runtime.Image,
		Docker: runtime.DockerOptions{
			EndpointHost: c.Runtime.Docker.EndpointHost,
			Platform:     c.Runtime.Docker.Platform,
		},
		ECI: runtime.ECIOptions{
			Region: c.Runtime.ECI.Region, VSwitchID: c.Runtime.ECI.VSwitchID,
			SecurityGroupID: c.Runtime.ECI.SecurityGroupID, ImageCacheID: c.Runtime.ECI.ImageCacheID,
			CPU: c.Runtime.ECI.CPU, MemoryGiB: c.Runtime.ECI.MemoryGiB,
		},
	}
	if m.containerized {
		spec.Docker.EndpointHost = "0.0.0.0"
	}
	if c.Storage.Type == "nas" {
		spec.Storage.MountTarget = c.Storage.NASMountTarget
		spec.Storage.Path = "/go-bot/" + id
	}
	return spec
}

func (m *Manager) hostFileStorage(id string, record Account) (kind, source string) {
	if m.Config.Runtime.Provider == "docker" {
		return "docker-volume", dock.DataVolumeName(id)
	}
	if m.Config.Storage.Type == "nas" {
		return "nas", m.spec(id, record).Storage.Path
	}
	return "instance", ""
}

func (m *Manager) Doctor(ctx context.Context) error {
	spec := m.spec("doctor", Account{HostID: uuid.NewString()})
	return m.Driver.Preflight(ctx, spec)
}

func (m *Manager) find(ctx context.Context, id string, record Account) (runtime.Instance, error) {
	if record.HostID == "" {
		return runtime.Instance{}, runtime.ErrNotFound
	}
	instance, err := m.Driver.Find(ctx, m.spec(id, record))
	if err == nil && (instance.RunID != record.HostID || instance.DeploymentID != id) {
		return runtime.Instance{}, errors.New("runtime resource identity does not match the Host")
	}
	return instance, err
}

func (m *Manager) Ensure(ctx context.Context, id string) error {
	lock := m.accountLock(id)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(ctx, m.Config.Runtime.StartTimeout)
	defer cancel()
	record, err := m.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if record.DesiredState == running && record.HostEndpoint != "" {
		if m.probe(ctx, record.HostEndpoint, record.HostToken) == nil {
			return m.syncAccountSettings(ctx, id, record)
		}
		record.HostEndpoint = ""
		if err := m.Store.Save(ctx, id, record); err != nil {
			return err
		}
	}
	instance, err := m.find(ctx, id, record)
	if err != nil && !errors.Is(err, runtime.ErrNotFound) {
		return err
	}
	if err == nil && instance.State == stopped && !m.Driver.Capabilities().RestartStopped {
		record.HostID, record.HostToken = "", ""
	}
	if record.HostID == "" {
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			return err
		}
		record.HostID = uuid.NewString()
		record.HostToken = hex.EncodeToString(secret[:])
	}
	record.DesiredState = running
	if err := m.Store.Save(ctx, id, record); err != nil {
		return err
	}
	spec := m.spec(id, record)
	instance, err = m.Driver.Ensure(ctx, spec)
	if err != nil {
		return err
	}
	if instance.RunID != record.HostID || instance.DeploymentID != id {
		return errors.New("runtime resource identity does not match the Host")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := m.probe(ctx, instance.Endpoint, record.HostToken); err == nil {
			record.HostEndpoint = instance.Endpoint
			if err := m.syncAccountSettings(ctx, id, record); err != nil {
				return err
			}
			return m.Store.Save(ctx, id, record)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Host did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (m *Manager) control(ctx context.Context, endpoint, token, path string, expected int) error {
	if endpoint == "" {
		return errHostUnavailable
	}
	method := http.MethodPost
	if path == "/readyz" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := m.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != expected {
		return fmt.Errorf("Host control endpoint returned %d", res.StatusCode)
	}
	return nil
}

func (m *Manager) probe(ctx context.Context, endpoint, token string) error {
	return m.control(ctx, endpoint, token, "/readyz", http.StatusOK)
}
