package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"go-bot/harness/agent"
	"go-bot/harness/skills"
	"go-bot/internal/authorization"
	"go-bot/internal/credentials"
	"go-bot/plugin"
)

type ManagerConfig struct {
	Directory   string
	Runtime     RuntimeConfig
	PackageMain string
	PNPM        string
	Changed     func()
}
type Status = plugin.Status

// Manager installs packages and applies desired configuration to native Cordis.
// It has no plugin-instance lifecycle state machine or Session ownership.
type Manager struct {
	ctx             context.Context
	cancel          context.CancelFunc
	config          ManagerConfig
	gate            chan struct{}
	mu              sync.RWMutex
	runtime         *hostBridge
	desired         plugin.RuntimeSettings
	lastError       string
	applying        bool
	preparation     map[string]plugin.Instance
	changes         chan struct{}
	surface         *authorization.Surface
	ownsCredentials bool
	closeOnce       sync.Once
	closeError      error
}

func NewManager(ctx context.Context, c ManagerConfig) (*Manager, error) {
	if !filepath.IsAbs(c.Directory) || !filepath.IsAbs(c.Runtime.Main) || !filepath.IsAbs(c.PackageMain) {
		return nil, errors.New("plugin manager paths must be absolute")
	}
	if err := os.MkdirAll(c.Directory, 0700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{ctx: ctx, cancel: cancel, config: c, gate: make(chan struct{}, 1), changes: make(chan struct{}), desired: plugin.RuntimeSettings{}.Clone()}
	if m.config.Runtime.Credentials == nil {
		home := c.Runtime.DSHHome
		if home == "" {
			home = filepath.Dir(c.Directory)
		}
		store, err := credentials.Open(filepath.Join(home, credentials.Filename), credentials.Options{Watch: true})
		if err != nil {
			cancel()
			return nil, err
		}
		m.config.Runtime.Credentials = store
		m.ownsCredentials = true
	}
	if m.config.Runtime.Authorization == nil {
		m.config.Runtime.Authorization = authorization.New(m.config.Runtime.Credentials)
	}
	m.surface = authorization.NewSurface(ctx, m.config.Runtime.Authorization, m.changed)
	return m, nil
}
func (m *Manager) profile() string { return filepath.Join(m.config.Directory, "profile") }
func (m *Manager) packages() Packages {
	return Packages{Directory: m.profile(), Main: m.config.PackageMain, Node: m.config.Runtime.Node, PNPM: m.config.PNPM}
}
func (m *Manager) enter(ctx context.Context) (context.Context, func(), error) {
	select {
	case m.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-m.ctx.Done():
		return nil, nil, m.ctx.Err()
	}
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	return call, func() { stop(); cancel(); <-m.gate }, nil
}
func (m *Manager) changed() {
	m.mu.Lock()
	close(m.changes)
	m.changes = make(chan struct{})
	m.mu.Unlock()
	if m.config.Changed != nil {
		m.config.Changed()
	}
}
func (m *Manager) Changes() (<-chan struct{}, <-chan struct{}) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.changes, m.ctx.Done()
}
func (m *Manager) redact(message string) string { return m.config.Runtime.Credentials.Redact(message) }
func (m *Manager) report(err error) {
	message := ""
	if err != nil {
		message = m.redact(err.Error())
	}
	m.mu.Lock()
	m.lastError = message
	m.mu.Unlock()
	m.changed()
}
func (m *Manager) packageCredentials(source plugin.Source) (map[string]string, error) {
	out := map[string]string{}
	if source.AuthRef == "" {
		return out, nil
	}
	value, err := m.config.Runtime.Credentials.Resolve(source.AuthRef)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, failure("CREDENTIAL_REQUIRED", "Missing installation credential: "+source.AuthRef)
	}
	out[source.AuthRef] = value.Value
	return out, nil
}
func (m *Manager) InspectPackage(ctx context.Context, name string, source plugin.Source) (plugin.PackageInfo, error) {
	ctx, leave, err := m.enter(ctx)
	if err != nil {
		return plugin.PackageInfo{}, err
	}
	defer leave()
	values, err := m.packageCredentials(source)
	if err != nil {
		return plugin.PackageInfo{}, err
	}
	return m.packages().Inspect(ctx, name, source, values)
}

func (m *Manager) stopRuntime() error {
	m.mu.Lock()
	runtime := m.runtime
	m.runtime = nil
	m.mu.Unlock()
	if runtime != nil {
		return runtime.Close()
	}
	return nil
}
func (m *Manager) Apply(ctx context.Context, settings plugin.RuntimeSettings) (Status, error) {
	if err := settings.Validate(); err != nil {
		return Status{}, err
	}
	ctx, leave, err := m.enter(ctx)
	if err != nil {
		return Status{}, err
	}
	defer leave()
	return m.apply(ctx, settings.Clone(), false, "")
}
func (m *Manager) apply(ctx context.Context, s plugin.RuntimeSettings, restart bool, reinstall string) (status Status, err error) {
	m.mu.Lock()
	m.desired = s.Clone()
	m.applying = true
	m.lastError = ""
	m.preparation = make(map[string]plugin.Instance)
	m.mu.Unlock()
	m.changed()
	defer func() {
		m.mu.Lock()
		m.applying = false
		for name, row := range m.preparation {
			if row.State != "installation-failed" {
				delete(m.preparation, name)
			}
		}
		m.mu.Unlock()
		m.report(err)
		read, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		status, _ = m.Status(read)
	}()
	if err = os.MkdirAll(m.profile(), 0700); err != nil {
		return
	}
	installed, err := m.packages().List(ctx)
	if err != nil {
		return status, err
	}
	actual := map[string]plugin.PackageInfo{}
	for _, pkg := range installed {
		actual[pkg.Name] = pkg
	}
	// Restore an installed selected release without silently upgrading it just
	// because the market now offers a newer release/source. The source snapshot
	// belongs to the package manager's installed manifest, not Redis user config.
	for name, wanted := range s.Packages {
		if name != reinstall && wanted.Version != "" && actual[name].Present && actual[name].Version == wanted.Version {
			wanted.Source = actual[name].Source
			s.Packages[name] = wanted
		}
	}
	names := make([]string, 0, len(s.Packages))
	for name := range s.Packages {
		names = append(names, name)
	}
	sort.Strings(names)
	missing := []string{}
	for _, name := range names {
		if name == reinstall || !actual[name].Present || actual[name].Source != s.Packages[name].Source || (s.Packages[name].Version != "" && actual[name].Version != s.Packages[name].Version) {
			missing = append(missing, name)
		}
	}
	// Defaults install sequentially. Publish the entire queue so a package
	// waiting for its turn is not mistaken for an unavailable plugin.
	m.mu.Lock()
	for _, name := range missing {
		m.preparation[name] = plugin.Instance{Package: name, State: "installing"}
	}
	m.mu.Unlock()
	m.changed()

	reloadRuntime := restart
	var installErrors []error
	failed := map[string]bool{}
	defer func() { err = errors.Join(append(installErrors, err)...) }()
	for _, name := range missing {
		installErr := func() error {
			values, e := m.packageCredentials(s.Packages[name].Source)
			if e != nil {
				return e
			}
			if expected := s.Packages[name].Version; expected != "" {
				available, e := m.packages().Inspect(ctx, name, s.Packages[name].Source, values)
				if e != nil {
					return e
				}
				if available.Version != expected {
					return failure("SELECTED_RELEASE_UNAVAILABLE", name+": selected release is not present; update to the reviewed market release")
				}
			}
			_, e = m.packages().Install(ctx, name, s.Packages[name].Source, values, s.Packages[name].Builds...)
			return e
		}()
		row := plugin.Instance{Package: name, State: "loading"}
		if installErr != nil {
			row.State, row.Error = "installation-failed", m.redact(installErr.Error())
		}
		m.mu.Lock()
		m.preparation[name] = row
		m.mu.Unlock()
		m.changed()
		if installErr != nil {
			failed[name] = true
			installErrors = append(installErrors, fmt.Errorf("%s: %w", name, installErr))
			continue
		}
		reloadRuntime = reloadRuntime || actual[name].Present
	}
	if reloadRuntime {
		if err = m.stopRuntime(); err != nil {
			return
		}
	}
	actualPackages, metadataErr := m.packages().List(ctx)
	if metadataErr != nil {
		return status, metadataErr
	}
	metadata := map[string]plugin.PackageInfo{}
	for _, info := range actualPackages {
		metadata[info.Name] = info
	}
	// Only ready packages contribute instances. Keep the full desired selection
	// for status/retry, while activating successful siblings after partial failure.
	availableInstances := s.Instances[:0:0]
	for _, row := range s.Instances {
		if !failed[row.Package] && metadata[row.Package].Present {
			availableInstances = append(availableInstances, row)
		}
	}
	s.Instances = availableInstances
	for _, row := range s.Instances {
		if !row.Enabled {
			continue
		}
		schema := metadata[row.Package].ConfigSchema
		if len(schema) == 0 {
			continue
		}
		compiled, schemaErr := compileSchema(schema)
		if schemaErr != nil {
			return status, schemaErr
		}
		config := row.Config
		if len(config) == 0 {
			config = json.RawMessage(`{}`)
		}
		if schemaErr = validateSchema(compiled, config); schemaErr != nil {
			return status, failure("INVALID_CONFIG", row.ID+": "+schemaErr.Error())
		}
	}
	enabled := false
	for _, row := range s.Instances {
		enabled = enabled || row.Enabled
	}
	if !enabled {
		err = m.stopRuntime()
		return
	}
	m.mu.RLock()
	runtime := m.runtime
	m.mu.RUnlock()
	if runtime != nil && !runtime.Alive() {
		if err = m.stopRuntime(); err != nil {
			return
		}
		runtime = nil
	}
	if runtime == nil {
		c := m.config.Runtime
		c.ProfileDir = m.profile()
		c.Changed = m.changed
		runtime, err = startHostBridge(m.ctx, c)
		if err != nil {
			return
		}
		m.mu.Lock()
		m.runtime = runtime
		m.mu.Unlock()
	}
	var instances []map[string]any
	instances, err = runtime.Apply(ctx, s.Instances)
	if err == nil {
		for _, row := range instances {
			if row["state"] == "failed" {
				err = errors.Join(err, failure("PLUGIN_LOAD_FAILED", m.redact(row["id"].(string)+": "+stringField(row, "error"))))
			}
		}
	}
	return
}
func stringField(row map[string]any, key string) string { value, _ := row[key].(string); return value }
func (m *Manager) Status(ctx context.Context) (Status, error) {
	m.mu.RLock()
	runtime, last, desired := m.runtime, m.lastError, m.desired.Clone()
	applying := m.applying
	preparation := make(map[string]plugin.Instance, len(m.preparation))
	for name, row := range m.preparation {
		preparation[name] = row
	}
	m.mu.RUnlock()
	status := Status{Runtime: "stopped", Packages: []plugin.PackageInfo{}, Instances: []plugin.Instance{}, Error: last}
	if runtime == nil && applying {
		status.Runtime = "loading"
	}
	if runtime != nil {
		status.Runtime = "running"
		if !runtime.Alive() {
			status.Runtime = "failed"
			status.Error = "Plugin process is unavailable"
		} else {
			var rows []plugin.Instance
			if err := runtime.peer.call(ctx, "runtime.inspect", nil, &rows); err != nil {
				status.Runtime = "failed"
				status.Error = m.redact(err.Error())
			} else {
				status.Instances = rows
				for i := range rows {
					rows[i].Error = m.redact(rows[i].Error)
				}
			}
		}
	}
	// Cordis has no instance row until a package has been installed and applied.
	// Merge installation progress with its actual rows, including while sibling
	// plugins remain active in the existing runtime.
	for _, wanted := range desired.Instances {
		index := -1
		for i, row := range status.Instances {
			if row.ID == wanted.ID {
				index = i
				break
			}
		}
		row, preparing := preparation[wanted.Package]
		if preparing && (row.State != "loading" || index < 0) {
			row.ID = wanted.ID
		} else if index < 0 {
			row = plugin.Instance{ID: wanted.ID, Package: wanted.Package, State: "disabled"}
			if wanted.Enabled {
				row.State = "unavailable"
				if applying {
					row.State = "loading"
				}
			}
		} else {
			continue
		}
		if index < 0 {
			status.Instances = append(status.Instances, row)
		} else {
			status.Instances[index] = row
		}
	}
	installed, err := m.packages().List(ctx)
	if err != nil {
		return status, err
	}
	status.Packages = installed
	found := map[string]bool{}
	for _, row := range installed {
		found[row.Name] = true
	}
	for name, pkg := range desired.Packages {
		if !found[name] {
			status.Packages = append(status.Packages, plugin.PackageInfo{Name: name, Source: pkg.Source})
		}
	}
	sort.Slice(status.Packages, func(i, j int) bool { return status.Packages[i].Name < status.Packages[j].Name })
	return status, nil
}
func (m *Manager) Restart(ctx context.Context) (Status, error) {
	return m.restart(ctx, nil)
}

func (m *Manager) restart(ctx context.Context, settings *plugin.RuntimeSettings) (Status, error) {
	if settings != nil {
		if err := settings.Validate(); err != nil {
			return Status{}, err
		}
	}
	ctx, leave, err := m.enter(ctx)
	if err != nil {
		return Status{}, err
	}
	defer leave()
	m.mu.RLock()
	s := m.desired.Clone()
	m.mu.RUnlock()
	if settings != nil {
		s = settings.Clone()
	}
	return m.apply(ctx, s, true, "")
}
func (m *Manager) Reinstall(ctx context.Context, name string, s plugin.RuntimeSettings) (Status, error) {
	if _, ok := s.Packages[name]; !ok {
		return Status{}, errors.New("package is not configured")
	}
	if err := s.Validate(); err != nil {
		return Status{}, err
	}
	ctx, leave, err := m.enter(ctx)
	if err != nil {
		return Status{}, err
	}
	defer leave()
	return m.apply(ctx, s.Clone(), false, name)
}
func (m *Manager) Remove(ctx context.Context, name string, s plugin.RuntimeSettings) (Status, error) {
	if !plugin.ValidPackageName(name) {
		return Status{}, errors.New("invalid package name")
	}
	if _, ok := s.Packages[name]; ok {
		return Status{}, errors.New("remove package from settings first")
	}
	if err := s.Validate(); err != nil {
		return Status{}, err
	}
	ctx, leave, err := m.enter(ctx)
	if err != nil {
		return Status{}, err
	}
	defer leave()
	// Apply native removal before deleting code. Other instances stay under Cordis.
	if _, err = m.apply(ctx, s.Clone(), false, ""); err != nil {
		return Status{}, err
	}
	if err = m.packages().Remove(ctx, name); err != nil {
		return Status{}, err
	}
	return m.Status(ctx)
}
func (m *Manager) executionRuntime() (*hostBridge, error) {
	m.mu.RLock()
	runtime := m.runtime
	configured := false
	for _, row := range m.desired.Instances {
		configured = configured || row.Enabled
	}
	m.mu.RUnlock()
	if !configured {
		return nil, nil
	}
	if runtime == nil || !runtime.Alive() {
		return nil, nil // No Node dependency for core execution; failed plugins are shown by Status.
	}
	return runtime, nil
}
func (m *Manager) Tools(ctx context.Context, id string) ([]agent.Tool, error) {
	r, err := m.executionRuntime()
	if err != nil || r == nil {
		return nil, err
	}
	return r.Tools(ctx, id)
}
func (m *Manager) Skills(ctx context.Context, lookup skills.Lookup) ([]skills.Provider, error) {
	r, err := m.executionRuntime()
	if err != nil || r == nil {
		return nil, err
	}
	return r.Skills(ctx, lookup)
}
func (m *Manager) AssemblePrompt(ctx context.Context, input agent.PromptAssemblyInput) (agent.PromptAssembly, error) {
	r, err := m.executionRuntime()
	if err != nil {
		return agent.PromptAssembly{}, err
	}
	if r == nil {
		return agent.PromptAssembly{SystemPrompt: input.SystemPrompt, Tools: input.Tools}, nil
	}
	return r.AssemblePrompt(ctx, input)
}
func (m *Manager) ToolInvocations(context.Context, string) (agent.ToolInvocationFactory, error) {
	// A Go-only adapter supplies active-call data. Node is contacted only from a
	// registered plugin's business callback, never for core stage preparation.
	return hostToolInvocation, nil
}

func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.cancel()
		m.gate <- struct{}{}
		defer func() { <-m.gate }()
		m.surface.Close()
		m.closeError = m.stopRuntime()
		if m.ownsCredentials {
			m.closeError = errors.Join(m.closeError, m.config.Runtime.Credentials.Close())
		}
	})
	return m.closeError
}
func writePrivate(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
