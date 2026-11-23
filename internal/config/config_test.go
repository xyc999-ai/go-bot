package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gobot "go-bot"
)

func clean(t *testing.T) {
	t.Helper()
	c := Default()
	fields := controllerBindings(&c)
	for key := range fields {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	t.Setenv("HOST_ACCOUNT_ID", "")
	t.Setenv("HOST_ID", "")
	t.Setenv("HOST_API_TOKEN", "")
	t.Setenv("HOST_DATA_MOUNTED", "")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSearchProviderAndKeyAreRequired(t *testing.T) {
	clean(t)
	t.Chdir(t.TempDir())
	writeFile(t, ".env", "MODEL_API_KEY=test-main-key\n")
	c, err := Load("", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Model.Main.Protocol != "responses" || c.Model.Main.BaseURL != DeepSeekOrigin {
		t.Fatal("model preset not resolved")
	}
	for _, test := range []struct {
		name, provider, key, want string
	}{
		{"missing both", "", "", "missing WEB_SEARCH_PROVIDER"},
		{"missing provider", "", "explicit-key", "missing WEB_SEARCH_PROVIDER"},
		{"blank provider", " ", "explicit-key", "missing WEB_SEARCH_PROVIDER"},
		{"missing key", "deepseek", "", "missing WEB_SEARCH_API_KEY"},
		{"blank key", "deepseek", " ", "missing WEB_SEARCH_API_KEY"},
		{"unknown provider", "unsupported", "explicit-key", "unsupported WEB_SEARCH_PROVIDER"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeFile(t, ".env", "MODEL_API_KEY=test-main-key\nWEB_SEARCH_PROVIDER="+test.provider+"\nWEB_SEARCH_API_KEY="+test.key+"\n")
			c, err := Load("", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, hostOnly := range []bool{false, true} {
				if err := c.Validate(hostOnly); err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("validation error = %v, want %s", err, test.want)
				}
			}
		})
	}
	t.Setenv("WEB_SEARCH_PROVIDER", "deepseek")
	t.Setenv("WEB_SEARCH_API_KEY", "explicit-key")
	if err := PrepareControllerProfile("", ""); err != nil {
		t.Fatal(err)
	}
	c, err = Load("", "")
	if err != nil || c.Web.APIKey != "explicit-key" {
		t.Fatalf("explicit search configuration: %v", err)
	}
}
func TestYAMLOnlyAcceptsProcessTuning(t *testing.T) {
	clean(t)
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "tuning.yaml")
	writeFile(t, ".env", "")
	writeFile(t, path, "version: 1\nruntime:\n  start_timeout: 7m\n")
	c, err := Load(path, "")
	if err != nil || c.Runtime.StartTimeout.Minutes() != 7 {
		t.Fatalf("tuning did not load: %v", err)
	}
	for _, body := range []string{
		"runtime:\n  provider: eci\n", "host:\n  id: former-host\n", "controller:\n  listen: localhost:8080\n",
		"host:\n  idle_timeout: 0s\n", "model:\n  main:\n    api_key: key\n", "web:\n  providers: {}\n",
		"cloud:\n  aliyun: {}\n", "auth:\n  host_token: key\n",
		"registry:\n  password: key\n", "storage:\n  type: nas\n", "host:\n  listen: 0.0.0.0:8080\n",
	} {
		writeFile(t, path, body)
		if _, err := Load(path, ""); err == nil {
			t.Fatalf("deployment field was accepted in YAML: %q", body)
		}
	}
}

func TestECICredentialsAndOptionalNAS(t *testing.T) {
	clean(t)
	t.Chdir(t.TempDir())
	writeFile(t, ".env", "MODEL_API_KEY=key\nWEB_SEARCH_PROVIDER=deepseek\nWEB_SEARCH_API_KEY=search-key\n")
	c, err := Load("", "")
	if err != nil {
		t.Fatal(err)
	}
	c.Runtime.Provider = "eci"
	c.Runtime.ECI.ImageCacheID = "imc-fixture"
	c.Runtime.ECI.VSwitchID = "vsw-test"
	c.Runtime.ECI.SecurityGroupID = "sg-test"
	e := c.Validate(false)
	if e == nil || !strings.Contains(e.Error(), "ALIBABA_CLOUD_ACCESS_KEY_SECRET") {
		t.Fatal("ECI missing credentials accepted")
	}
	c.Cloud.Aliyun.Credentials = Credentials{"id", "secret"}
	c.Runtime.Image = ""
	if e = c.Validate(false); e == nil || !strings.Contains(e.Error(), "HOST_IMAGE") {
		t.Fatal("ECI accepted an empty image reference")
	}
	c.Runtime.Image = DefaultHostImage
	if e = c.Validate(false); e != nil {
		t.Fatal(e)
	}
	if c.HostEnvironment("alice", "run", "token")["HOST_DATA_MOUNTED"] != "false" {
		t.Fatal("ECI container storage should use the writable layer")
	}
	c.Storage.Type = "nas"
	if c.Validate(false) == nil {
		t.Fatal("missing NAS target accepted")
	}
	c.Storage.NASMountTarget = "nas.test"
	if e = c.Validate(false); e != nil {
		t.Fatal(e)
	}
	if c.HostEnvironment("alice", "run", "token")["HOST_DATA_MOUNTED"] != "true" {
		t.Fatal("ECI NAS storage should require a mounted data directory")
	}
	c.Runtime.Provider = "docker"
	c.Runtime.Image = Default().Runtime.Image
	if err := c.Validate(false); err == nil || !strings.Contains(err.Error(), "requires HARNESS_RUNTIME=eci") {
		t.Fatalf("Docker accepted NAS storage: %v", err)
	}
	for _, storage := range []string{"volume", "bind"} {
		for _, provider := range []string{"docker", "eci"} {
			c.Runtime.Provider = provider
			c.Storage.Type = storage
			if err := c.Validate(false); err == nil || !strings.Contains(err.Error(), "HARNESS_STORAGE must be container or nas") {
				t.Fatalf("%s accepted %s storage: %v", provider, storage, err)
			}
		}
	}
}
func TestHostEnvironmentHasNoCloudOrPublicToken(t *testing.T) {
	clean(t)
	t.Chdir(t.TempDir())
	writeFile(t, ".env", "MODEL_API_KEY=model-secret\nWEB_SEARCH_PROVIDER=deepseek\nWEB_SEARCH_API_KEY=search-secret\n")
	c, err := Load("", "")
	if err != nil {
		t.Fatal(err)
	}
	c.Cloud.Aliyun.Credentials = Credentials{"cloud-id", "cloud-secret"}
	env := c.HostEnvironment("alice", "run", "internal-token")
	b, _ := json.Marshal(env)
	for _, secret := range []string{"cloud-id", "cloud-secret", "public-token-secret", "model-secret", "search-secret"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("Host environment exposed %s", secret)
		}
	}
	if _, ok := env["HOST_CONFIG_JSON"]; ok {
		t.Fatal("Host received a serialized configuration blob")
	}
	if len(env) != 4 || env["HOST_ACCOUNT_ID"] != "alice" || env["HOST_ID"] != "run" ||
		env["HOST_API_TOKEN"] != "internal-token" || env["HOST_DATA_MOUNTED"] != "true" {
		t.Fatal("Host received an incorrect field set")
	}
}

func TestEnvironmentTemplateAndPrecedence(t *testing.T) {
	clean(t)
	template, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	c := Default()
	fields := controllerBindings(&c)
	if strings.Contains(string(template), "CONTROLLER_STATE_DIR") {
		t.Fatal("public template must use the default Controller state directory")
	}
	for key := range fields {
		if !strings.HasPrefix(key, "MODEL_") && !strings.HasPrefix(key, "WEB_SEARCH_") && !strings.Contains(string(template), key+"=") {
			t.Errorf("template missing %s", key)
		}
	}
	values, err := parseEnvironmentFile(".env.example", string(template), fields)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ALIBABA_CLOUD_ACCESS_KEY_ID", "ALIBABA_CLOUD_ACCESS_KEY_SECRET"} {
		if value, exists := values[key]; !exists || value != "" {
			t.Errorf("%s must be present and blank in the public template", key)
		}
	}
	t.Chdir(t.TempDir())
	writeFile(t, ".env", string(template))
	c, err = Load("", "")
	if err != nil || c.Runtime.Provider != "docker" || c.Runtime.Image != DefaultHostImage || c.Storage.Type != "container" || c.ValidateController() != nil {
		t.Fatalf("template did not produce the default deployment without account credentials: %v", err)
	}
	profile := strings.NewReplacer(
		"HARNESS_RUNTIME=docker", "HARNESS_RUNTIME=eci",
		"HOST_IMAGE="+DefaultHostImage, "HOST_IMAGE=registry.example/host:test",
		"ECI_VSWITCH_ID=", "ECI_VSWITCH_ID=vsw-fixture",
		"ECI_SECURITY_GROUP_ID=", "ECI_SECURITY_GROUP_ID=sg-fixture",
		"ECI_IMAGE_CACHE_ID=", "ECI_IMAGE_CACHE_ID=imc-fixture",
		"ALIBABA_CLOUD_ACCESS_KEY_ID=", "ALIBABA_CLOUD_ACCESS_KEY_ID=fixture-id",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET=", "ALIBABA_CLOUD_ACCESS_KEY_SECRET=fixture-secret",
	).Replace(string(template))
	profile += "\nMODEL_API_KEY=literal#key=$value\nMODEL_URL=https://models.example/v1\nMODEL_NAME=fixture-model\n"
	writeFile(t, ".env.eci", profile)
	c, err = Load("", ".env.eci")
	if err != nil || c.ValidateController() != nil {
		t.Fatalf("ECI profile: %v / %v", err, c.ValidateController())
	}
	if c.Runtime.Provider != "eci" || c.Runtime.ECI.VSwitchID != "vsw-fixture" ||
		c.Cloud.Aliyun.Credentials.AccessKeySecret != "fixture-secret" ||
		c.Model.Main.BaseURL != "https://models.example/v1" || c.Model.Main.APIKey != "literal#key=$value" {
		t.Fatal("environment profile fields were not resolved")
	}
	writeFile(t, ".env.nas", strings.NewReplacer(
		"HARNESS_STORAGE=container", "HARNESS_STORAGE=nas",
		"NAS_MOUNT_TARGET=", "NAS_MOUNT_TARGET=nas.example",
	).Replace(profile))
	c, err = Load("", ".env.nas")
	if err != nil || c.ValidateController() != nil || c.Storage.Type != "nas" {
		t.Fatalf("NAS profile: %v / %v", err, c.ValidateController())
	}
	t.Setenv("MODEL_URL", "https://override.example")
	t.Setenv("MODEL_API_KEY", "")
	c, err = Load("", ".env.eci")
	if err != nil || c.Model.Main.BaseURL != "https://models.example/v1" || c.Model.Main.APIKey != "literal#key=$value" {
		t.Fatal("direct profile loading consumed process overrides")
	}
	if err := PrepareControllerProfile("", ".env.eci"); err != nil {
		t.Fatalf("Controller validation depended on account model environment: %v", err)
	}
	if _, err := Load("", ".env.missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit missing file was not reported: %v", err)
	}
}

func TestEnvironmentFileRejectsInvalidInputWithoutPrintingValues(t *testing.T) {
	clean(t)
	path := filepath.Join(t.TempDir(), ".env")
	for _, value := range []string{
		"export MODEL_API_KEY=secret-marker",
		"MODEL_API_KEY=secret-marker\nMODEL_API_KEY=second",
		"WEB_SEARCH_ENABLED=secret-marker",
		"NAS_PATH=/custom",
		"STORAGE_SOURCE=secret-marker",
		"REGISTRY_PRIVATE=true",
	} {
		writeFile(t, path, value)
		_, err := Load("", path)
		if err == nil || strings.Contains(err.Error(), "secret-marker") {
			t.Fatal("invalid setting was accepted or its value appeared in the error")
		}
	}
}

func TestHostRequiresControllerEnvironment(t *testing.T) {
	clean(t)
	t.Setenv("MODEL_API_KEY", "model-key")
	t.Setenv("MODEL_URL", "http://localhost:9000/v1")
	t.Setenv("WEB_SEARCH_API_KEY", "fixture-search-key")
	if _, err := LoadHost(); err == nil || !strings.Contains(err.Error(), "started by Controller") {
		t.Fatalf("Host accepted an unmanaged launch: %v", err)
	}
	env := Default().HostEnvironment("alice", "run", "test-token")
	for key, value := range env {
		t.Setenv(key, value)
	}

	c, err := LoadHost()
	if err != nil {
		t.Fatal(err)
	}
	if c.Host.AccountID != "alice" || c.Host.RunID != "run" || c.Auth.HostToken != "test-token" || !c.Host.DataMounted {
		t.Fatal("Host did not receive the Controller startup fields")
	}
	if c.Model.Main.APIKey != "" || c.Model.Main.BaseURL != "" || c.Web.APIKey != "" {
		t.Fatal("Host loaded model or search settings from the environment")
	}
}

func TestStartupRequiresProfileWithoutAccountCredentials(t *testing.T) {
	clean(t)
	t.Chdir(t.TempDir())
	if err := PrepareControllerProfile("", ""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing profile: %v", err)
	}
	if _, err := os.Stat(".env"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("startup created a profile")
	}
	writeFile(t, ".env", gobot.EnvironmentTemplate)
	if err := PrepareControllerProfile("", ""); err != nil {
		t.Fatalf("start without account credentials: %v", err)
	}
	t.Setenv("MODEL_API_KEY", "fixture-account-key")
	t.Setenv("MODEL_URL", "https://models.example/v1")
	t.Setenv("MODEL_NAME", "fixture-model")
	t.Setenv("WEB_SEARCH_API_KEY", "fixture#search=$key")
	t.Setenv("WEB_SEARCH_PROVIDER", "deepseek")
	t.Setenv("HOST_IMAGE", "go-bot/host:fixture")
	if err := PrepareControllerProfile("", ""); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(".env")
	if err != nil || strings.Contains(string(saved), "MODEL_") || strings.Contains(string(saved), "WEB_SEARCH_") ||
		!strings.Contains(string(saved), "HOST_IMAGE=go-bot/host:fixture") {
		t.Fatal("profile did not preserve only deployment overrides")
	}
}

func TestStartupRejectsPublicTemplateAsPrivateProfile(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(".env.example", []byte(gobot.EnvironmentTemplate), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".env.example", ".env"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".env.example", ".env"} {
		if err := PrepareControllerProfile("", path); err == nil || !strings.Contains(err.Error(), "private environment profile") {
			t.Fatalf("accepted the public template through %s: %v", path, err)
		}
	}
}

func TestSaveECIImageCacheIDPreservesProfile(t *testing.T) {
	t.Chdir(t.TempDir())
	const path = ".env.eci"
	const initial = "# deployment\nHARNESS_RUNTIME=eci\nECI_IMAGE_CACHE_ID=imc-old\nALIBABA_CLOUD_ACCESS_KEY_SECRET=literal#password=$value\n"
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SaveECIImageCacheID(path, "invalid"); err == nil {
		t.Fatal("accepted an invalid cache ID")
	}
	if err := SaveECIImageCacheID(path, "imc-new"); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil || string(saved) != strings.Replace(initial, "imc-old", "imc-new", 1) {
		t.Fatal("expected only the cache ID to change")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("updated profile is not private")
	}
}

func TestControllerListenRequiresLoopback(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"localhost:8080", "127.0.0.1:8080"},
		{"127.0.0.2:8080", "127.0.0.2:8080"},
		{"[::1]:8080", "[::1]:8080"},
		{"0.0.0.0:8080", ""},
		{"[::]:8080", ""},
		{"192.168.0.3:8080", ""},
		{"localhost.example:8080", ""},
		{"127.0.0.1:0", ""},
	} {
		got, err := ControllerListenAddress(test.input)
		if got != test.want || (err != nil) != (test.want == "") {
			t.Errorf("%s: got %q, %v, want %q", test.input, got, err, test.want)
		}
	}
}

func TestControllerProfileIgnoresRetiredHTTPSOrigin(t *testing.T) {
	profile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(profile, []byte("CONTROLLER_HTTPS_ORIGIN=https://computer.example.ts.net\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load("", profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateController(); err != nil {
		t.Fatal(err)
	}
}
