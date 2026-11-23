// Package config loads strict, versioned configuration and startup credentials.
package config

import (
	"bytes"
	"errors"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	webproviders "go-bot/providers/web"
	"gopkg.in/yaml.v3"
)

const DataRoot = "/root/harness"
const DefaultHostImage = "ghcr.io/xyc999-ai/go-bot-host:latest"
const DeepSeekOrigin = "https://api.deepseek.com"
const HostListen = "0.0.0.0:8080"
const ModelContextWindow = 1_000_000

var imageCacheID = regexp.MustCompile(`^imc-[a-zA-Z0-9]+$`)

type MainModel struct {
	Provider      string `json:"provider"`
	Name          string `json:"name"`
	Protocol      string `json:"protocol"`
	BaseURL       string `json:"base_url"`
	Auth          string `json:"auth"`
	APIKey        string `json:"-"`
	ContextWindow int    `json:"context_window_tokens"`
}
type Model struct {
	Main MainModel `json:"main"`
}
type Web struct {
	Provider string
	APIKey   string `json:"-"`
}
type Storage struct {
	Type           string `json:"type"`
	NASMountTarget string `json:"nas_mount_target,omitempty"`
}
type Credentials struct {
	AccessKeyID     string `json:"-"`
	AccessKeySecret string `json:"-"`
}
type Aliyun struct {
	Credentials Credentials `json:"-"`
}
type Cloud struct {
	Aliyun Aliyun `json:"-"`
}
type ECI struct {
	Region          string
	VSwitchID       string
	SecurityGroupID string
	ImageCacheID    string
	CPU             float32
	MemoryGiB       float32
}
type Docker struct {
	EndpointHost string `yaml:"endpoint_host"`
	Platform     string `yaml:"platform"`
}
type Runtime struct {
	Provider     string
	Image        string
	StartTimeout time.Duration
	Docker       Docker
	ECI          ECI
}
type Controller struct {
	Listen string
}
type Schedule struct {
	DeliveryHistoryDays       int `yaml:"delivery_history_days"`
	DeliveryHistoryRecords    int `yaml:"delivery_history_records"`
	NewSessionIntervalSeconds int `yaml:"new_session_interval_seconds"`
}

func defaultSchedule() Schedule {
	return Schedule{DeliveryHistoryDays: 30, DeliveryHistoryRecords: 200, NewSessionIntervalSeconds: 60}
}

func (s Schedule) validate() error {
	if s.DeliveryHistoryDays < 1 || s.DeliveryHistoryDays > 3650 || s.DeliveryHistoryRecords < 1 ||
		s.DeliveryHistoryRecords > 10000 || s.NewSessionIntervalSeconds < 60 || s.NewSessionIntervalSeconds > 604800 {
		return errors.New("schedule tuning requires 1–3650 history days, 1–10000 history records, and 60–604800 seconds between new Sessions")
	}
	return nil
}

func (s *Schedule) hostBindings() map[string]*int {
	return map[string]*int{
		"HOST_SCHEDULE_HISTORY_DAYS":                 &s.DeliveryHistoryDays,
		"HOST_SCHEDULE_HISTORY_RECORDS":              &s.DeliveryHistoryRecords,
		"HOST_SCHEDULE_NEW_SESSION_INTERVAL_SECONDS": &s.NewSessionIntervalSeconds,
	}
}

type Host struct {
	AccountID   string
	RunID       string
	DataMounted bool
}
type Auth struct {
	HostToken string `json:"-"`
}
type Config struct {
	Version    int
	Controller Controller
	Host       Host
	Runtime    Runtime
	Storage    Storage
	Schedule   Schedule
	Model      Model
	Web        Web
	Cloud      Cloud
	Auth       Auth
}

func Default() Config {
	c := Config{
		Version:    1,
		Controller: Controller{Listen: "127.0.0.1:8080"},
		Runtime: Runtime{
			Provider: "docker", Image: DefaultHostImage, StartTimeout: 5 * time.Minute,
			Docker: Docker{EndpointHost: "127.0.0.1"},
			ECI:    ECI{Region: "cn-hangzhou", CPU: 2, MemoryGiB: 4},
		},
		Storage: Storage{Type: "container"}, Schedule: defaultSchedule(),
	}
	return c
}

// tuning is the complete public YAML schema. Deployment fields live in the environment.
type tuning struct {
	Version  int      `yaml:"version"`
	Schedule Schedule `yaml:"schedule"`
	Runtime  struct {
		StartTimeout time.Duration `yaml:"start_timeout"`
		Docker       Docker        `yaml:"docker"`
		ECI          struct {
			CPU       float32 `yaml:"cpu"`
			MemoryGiB float32 `yaml:"memory_gib"`
		} `yaml:"eci"`
	} `yaml:"runtime"`
}

func loadTuning(path string) (Config, error) {
	c := Default()
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	var values tuning
	values.Version = c.Version
	values.Schedule = c.Schedule
	values.Runtime.StartTimeout = c.Runtime.StartTimeout
	values.Runtime.Docker = c.Runtime.Docker
	values.Runtime.ECI.CPU = c.Runtime.ECI.CPU
	values.Runtime.ECI.MemoryGiB = c.Runtime.ECI.MemoryGiB
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err := d.Decode(&values); err != nil {
		return c, errors.New("invalid tuning YAML: see configs/default.yaml for supported fields")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c, errors.New("configuration must contain one YAML document")
	}
	c.Version = values.Version
	c.Schedule = values.Schedule
	c.Runtime.StartTimeout = values.Runtime.StartTimeout
	c.Runtime.Docker = values.Runtime.Docker
	c.Runtime.ECI.CPU = values.Runtime.ECI.CPU
	c.Runtime.ECI.MemoryGiB = values.Runtime.ECI.MemoryGiB
	return c, nil
}

// Load reads an existing deployment profile without taking overrides from the process.
func Load(path, envFile string) (Config, error) {
	c, err := loadTuning(path)
	if err != nil {
		return c, err
	}
	if err := readEnvironment(envFile, controllerBindings(&c), controllerBindings(&c)); err != nil {
		return c, err
	}
	c.resolveModel()
	return c, nil
}

func (c *Config) resolveModel() {
	m := &c.Model.Main
	if m.BaseURL == "" {
		m.BaseURL = DeepSeekOrigin
	}
	m.Provider = "custom"
	if strings.TrimRight(m.BaseURL, "/") == DeepSeekOrigin {
		m.Provider = "deepseek"
		if m.Name == "" {
			m.Name = "deepseek-flash"
		}
	}
	m.Auth = "bearer"
	m.Protocol = "responses"
	m.ContextWindow = ModelContextWindow
}

func finitePositive(value float32) bool {
	return value > 0 && !math.IsInf(float64(value), 0)
}

func validURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func ValidateModel(apiKey, baseURL, name string) error {
	switch {
	case !validURL(baseURL):
		return errors.New("MODEL_URL must be an HTTP(S) base URL")
	case strings.TrimSpace(name) == "":
		return errors.New("MODEL_NAME is required")
	case strings.TrimSpace(apiKey) == "":
		return errors.New("missing MODEL_API_KEY")
	}
	return nil
}

func (c Config) Validate(hostOnly bool) error {
	return c.validate(hostOnly, true)
}

func (c Config) ValidateHost() error {
	if c.Host.AccountID == "" || c.Host.RunID == "" || c.Auth.HostToken == "" {
		return errors.New("Host must be started by Controller with HOST_ACCOUNT_ID, HOST_ID, and HOST_API_TOKEN")
	}
	return nil
}

func (c Config) ValidateController() error {
	return c.validate(false, false)
}

func (c Config) validate(hostOnly, requireModel bool) error {
	var errs []string
	need := func(ok bool, message string) {
		if !ok {
			errs = append(errs, message)
		}
	}
	need(c.Version == 1, "unsupported configuration version")
	if err := c.Schedule.validate(); err != nil {
		need(false, err.Error())
	}
	if requireModel {
		m := c.Model.Main
		if err := ValidateModel(m.APIKey, m.BaseURL, m.Name); err != nil {
			errs = append(errs, err.Error())
		}
	}
	need(c.Storage.Type == "container" || c.Storage.Type == "nas", "HARNESS_STORAGE must be container or nas")
	if requireModel {
		if strings.TrimSpace(c.Web.Provider) == "" {
			need(false, "missing WEB_SEARCH_PROVIDER")
		} else {
			_, supported := webproviders.Lookup(c.Web.Provider)
			need(supported, "unsupported WEB_SEARCH_PROVIDER")
		}
		need(strings.TrimSpace(c.Web.APIKey) != "", "missing WEB_SEARCH_API_KEY")
	}
	if !hostOnly {
		need(c.Runtime.StartTimeout > 0, "runtime.start_timeout must be positive")
		need(ValidImage(c.Runtime.Image), "HOST_IMAGE must be an image reference")
		_, listenErr := ControllerListenAddress(c.Controller.Listen)
		if listenErr != nil {
			need(false, listenErr.Error())
		}
		switch c.Runtime.Provider {
		case "docker":
			need(c.Storage.Type != "nas", "HARNESS_STORAGE=nas requires HARNESS_RUNTIME=eci")
		case "eci":
			need(c.Cloud.Aliyun.Credentials.AccessKeyID != "", "missing ALIBABA_CLOUD_ACCESS_KEY_ID")
			need(c.Cloud.Aliyun.Credentials.AccessKeySecret != "", "missing ALIBABA_CLOUD_ACCESS_KEY_SECRET")
			need(ValidImageCacheID(c.Runtime.ECI.ImageCacheID), "ECI_IMAGE_CACHE_ID must be a valid imc- ID")
			need(c.Runtime.ECI.VSwitchID != "", "ECI_VSWITCH_ID is required")
			need(c.Runtime.ECI.SecurityGroupID != "", "ECI_SECURITY_GROUP_ID is required")
			need(c.Runtime.ECI.Region != "", "ECI_REGION is required")
			need(finitePositive(c.Runtime.ECI.CPU) && finitePositive(c.Runtime.ECI.MemoryGiB), "ECI CPU and memory must be positive")
			if c.Storage.Type == "nas" {
				target := c.Storage.NASMountTarget
				need(target != "" && !strings.ContainsAny(target, "/ \t\r\n"), "NAS_MOUNT_TARGET must be a NAS hostname")
			}

		default:
			need(false, "HARNESS_RUNTIME must be docker or eci")
		}
	}
	if len(errs) > 0 {
		return errors.New("startup configuration is incomplete:\n  " + strings.Join(errs, "\n  "))
	}
	return nil
}

// ValidImage checks reference syntax before it is handed to a runtime.
func ValidImage(image string) bool {
	return image != "" && !strings.HasPrefix(image, "-") &&
		!strings.Contains(image, "://") && !strings.ContainsAny(image, " \r\n\t")
}

func ValidImageCacheID(id string) bool { return imageCacheID.MatchString(id) }

func (c Config) HostEnvironment(accountID, runID, token string) map[string]string {
	environment := map[string]string{
		"HOST_ACCOUNT_ID":   accountID,
		"HOST_ID":           runID,
		"HOST_API_TOKEN":    token,
		"HOST_DATA_MOUNTED": strconv.FormatBool(c.Runtime.Provider == "docker" || c.Storage.Type == "nas"),
	}
	defaults := defaultSchedule()
	fallback := defaults.hostBindings()
	for key, value := range c.Schedule.hostBindings() {
		if *value != *fallback[key] {
			environment[key] = strconv.Itoa(*value)
		}
	}
	return environment
}

func LoadHost() (Config, error) {
	c := Default()
	c.Host.AccountID = os.Getenv("HOST_ACCOUNT_ID")
	c.Host.RunID = os.Getenv("HOST_ID")
	c.Auth.HostToken = os.Getenv("HOST_API_TOKEN")
	switch os.Getenv("HOST_DATA_MOUNTED") {
	case "true":
		c.Host.DataMounted = true
	case "false", "":
	default:
		return c, errors.New("HOST_DATA_MOUNTED must be true or false")
	}
	for key, field := range c.Schedule.hostBindings() {
		if raw := os.Getenv(key); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				return c, errors.New("invalid Host Schedule tuning")
			}
			*field = value
		}
	}
	return c, errors.Join(c.ValidateHost(), c.Schedule.validate())
}

// ControllerListenAddress validates and normalizes the local HTTP listener.
func ControllerListenAddress(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	number, numberErr := strconv.Atoi(port)
	if err != nil || numberErr != nil || number < 1 || number > 65535 || !IsLoopbackHost(host) {
		return "", errors.New("CONTROLLER_LISTEN must use a loopback address with a port between 1 and 65535")
	}
	if strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(number)), nil
}

func IsLoopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}
