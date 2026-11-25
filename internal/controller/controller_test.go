package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-bot/internal/config"
	"go-bot/internal/modelsettings"
	proactive "go-bot/internal/proactivesettings"
	"go-bot/internal/providersettings"
	"go-bot/plugin"
	"go-bot/runtime"
)

type memoryStore struct {
	mu      sync.Mutex
	records map[string]Account
	models  map[string]modelsettings.Snapshot
}

func (s *memoryStore) Create(_ context.Context, id string, account Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[id]; exists {
		return ErrAccountExists
	}
	s.records[id] = account
	return nil
}
func (s *memoryStore) Get(_ context.Context, id string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return Account{}, ErrAccountNotFound
	}
	return record, nil
}
func (s *memoryStore) Save(_ context.Context, id string, account Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved := s.records[id]
	account.WebSearch = saved.WebSearch
	account.ImageGeneration = saved.ImageGeneration
	account.Plugins = saved.Plugins
	account.Proactive = saved.Proactive
	s.records[id] = account
	return nil
}

func (s *memoryStore) SaveProactiveSettings(_ context.Context, id string, settings proactive.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return ErrAccountNotFound
	}
	record.Proactive = &settings
	s.records[id] = record
	return nil
}

func (s *memoryStore) SavePluginSettings(_ context.Context, id string, settings plugin.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return ErrAccountNotFound
	}
	settings = settings.Clone()
	record.Plugins = &settings
	s.records[id] = record
	return nil
}

func (s *memoryStore) SaveProviderSettings(_ context.Context, id, field string, settings providersettings.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[id]
	switch field {
	case webSearchField:
		record.WebSearch = &settings
	case imageGenerationField:
		record.ImageGeneration = &settings
	default:
		return errors.New("invalid provider settings field")
	}
	s.records[id] = record
	return nil
}

type fakeDriver struct {
	mu          sync.Mutex
	provider    string
	endpoint    string
	instances   map[string]runtime.Instance
	specs       []runtime.Spec
	afterCreate error
	afterStop   error
	stopCalls   int
	deleteCalls int
	delayDelete bool
	inspections int
}

func (f *fakeDriver) Name() string { return f.provider }
func (f *fakeDriver) Capabilities() runtime.Capabilities {
	return runtime.Capabilities{NativeStop: f.provider == "docker", RestartStopped: f.provider == "docker"}
}
func (f *fakeDriver) Validate(runtime.Spec) error                   { return nil }
func (f *fakeDriver) Preflight(context.Context, runtime.Spec) error { return nil }
func (f *fakeDriver) Ensure(_ context.Context, spec runtime.Spec) (runtime.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	instance, exists := f.instances[spec.RunID]
	if !exists {
		f.specs = append(f.specs, spec)
		instance = runtime.Instance{
			Ref: runtime.ResourceRef{Provider: f.provider, ID: spec.RunID}, DeploymentID: spec.DeploymentID, RunID: spec.RunID,
		}
	}
	instance.State, instance.Endpoint = running, f.endpoint
	f.instances[spec.RunID] = instance
	if f.afterCreate != nil {
		err := f.afterCreate
		f.afterCreate = nil
		return runtime.Instance{}, err
	}
	return instance, nil
}
func (f *fakeDriver) Inspect(_ context.Context, ref runtime.ResourceRef) (runtime.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspections++
	instance, exists := f.instances[ref.ID]
	if !exists {
		return instance, runtime.ErrNotFound
	}
	return instance, nil
}
func (f *fakeDriver) Find(ctx context.Context, spec runtime.Spec) (runtime.Instance, error) {
	return f.Inspect(ctx, runtime.ResourceRef{ID: spec.RunID})
}
func (f *fakeDriver) Stop(_ context.Context, ref runtime.ResourceRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	if f.afterStop != nil {
		return f.afterStop
	}
	instance := f.instances[ref.ID]
	instance.State = stopped
	f.instances[ref.ID] = instance
	return nil
}
func (f *fakeDriver) Delete(_ context.Context, ref runtime.ResourceRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	if !f.delayDelete {
		delete(f.instances, ref.ID)
	}
	return nil
}

func newTestManager(t *testing.T, provider string, driver *fakeDriver, handler http.HandlerFunc) *Manager {
	t.Helper()
	driver.provider = provider
	driver.instances = make(map[string]runtime.Instance)
	backend := httptest.NewServer(handler)
	t.Cleanup(backend.Close)
	driver.endpoint = backend.URL
	cfg := config.Default()
	cfg.Runtime.Provider = provider
	cfg.Runtime.StartTimeout = 3 * time.Second
	cfg.Web.Provider, cfg.Web.APIKey = "deepseek", "search-key"
	manager := New(cfg, driver, &memoryStore{records: make(map[string]Account)})
	manager.SetPluginCatalog(testPluginCatalog())
	return manager
}

func controlReply(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case proactive.InternalPath:
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"enabled":true,"phase":"waiting"}`))
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	case modelsettings.InternalPath:
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"revision":0}`))
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	case "/readyz":
		w.WriteHeader(http.StatusOK)
	case "/internal/host/shutdown":
		w.WriteHeader(http.StatusAccepted)
	default:
		return false
	}
	return true
}

func controllerCall(m *Manager, method, path, user, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
	if user != "" {
		req.Header.Set(UserHeader, user)
	}
	out := httptest.NewRecorder()
	m.Handler().ServeHTTP(out, req)
	return out
}

func TestAccountHostsStartOnDemandAndUseTheirOwnModel(t *testing.T) {
	driver := &fakeDriver{}
	manager := newTestManager(t, "docker", driver, func(w http.ResponseWriter, r *http.Request) {
		if controlReply(w, r) {
			return
		}
		if r.Header.Get(UserHeader) != "" {
			t.Error("Controller routing header reached the Host")
		}
		_ = json.NewEncoder(w).Encode(r.Header.Get("Authorization"))
	})
	for _, id := range []string{"alice", "bob"} {
		payload := `{"userId":"` + id + `"}`
		out := controllerCall(manager, "POST", "/v1/accounts", "", payload)
		if out.Code != 201 || strings.Contains(out.Body.String(), "key-") {
			t.Fatalf("create %s: %d %s", id, out.Code, out.Body.String())
		}
		if out := controllerCall(manager, "POST", "/v1/accounts", "", payload); out.Code != 409 {
			t.Fatalf("duplicate account status = %d", out.Code)
		}
	}
	if len(driver.instances) != 0 {
		t.Fatal("account registration created compute")
	}
	if out := controllerCall(manager, "POST", "/v1/host/start", "unknown", ""); out.Code != 404 {
		t.Fatalf("unknown account status = %d", out.Code)
	}

	var starts sync.WaitGroup
	for _, id := range []string{"alice", "alice", "bob"} {
		starts.Go(func() {
			if err := manager.Ensure(t.Context(), id); err != nil {
				t.Error(err)
			}
		})
	}
	starts.Wait()
	if len(driver.specs) != 2 {
		t.Fatalf("created %d instances for two accounts", len(driver.specs))
	}
	for _, spec := range driver.specs {
		id := spec.DeploymentID
		if spec.Environment["MODEL_API_KEY"] != "" || spec.Environment["MODEL_NAME"] != "" || spec.Environment["HOST_ID"] != spec.RunID {
			t.Fatalf("wrong configuration for %s", id)
		}
		out := controllerCall(manager, "GET", "/v1/sessions", id, "")
		if out.Code != 200 || !strings.Contains(out.Body.String(), spec.Environment["HOST_API_TOKEN"]) {
			t.Fatalf("session routed to wrong Host for %s", id)
		}
	}
}

func TestRedisEndpointServesAfterRestartAndReopensAfterTransportFailure(t *testing.T) {
	driver := &fakeDriver{}
	manager := newTestManager(t, "docker", driver, func(w http.ResponseWriter, r *http.Request) {
		if !controlReply(w, r) {
			w.WriteHeader(http.StatusOK)
		}
	})
	ctx := t.Context()
	if err := manager.CreateAccount(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	record, err := manager.Store.Get(ctx, "alice")
	if err != nil || record.HostEndpoint != driver.endpoint {
		t.Fatalf("ready endpoint was not persisted: %v", err)
	}
	restarted := New(manager.Config, driver, manager.Store)
	inspections := driver.inspections
	for _, route := range []string{"/v1/host", "/v1/hosts/" + record.HostID, "/v1/sessions"} {
		out := controllerCall(restarted, http.MethodGet, route, "alice", "")
		if out.Code != http.StatusOK {
			t.Fatalf("after restart, %s returned %d", route, out.Code)
		}
	}
	if err := restarted.Ensure(ctx, "alice"); err != nil || driver.inspections != inspections {
		t.Fatalf("reusing a healthy route should not inspect Docker: %v", err)
	}

	unreachable := httptest.NewServer(http.NotFoundHandler())
	unreachable.Close()
	record.HostEndpoint = unreachable.URL
	if err := restarted.Store.Save(ctx, "alice", record); err != nil {
		t.Fatal(err)
	}
	if out := controllerCall(restarted, http.MethodGet, "/v1/sessions", "alice", ""); out.Code != http.StatusBadGateway {
		t.Fatalf("transport failure returned %d", out.Code)
	}
	failed, err := restarted.Store.Get(ctx, "alice")
	if err != nil || failed.HostEndpoint != "" || driver.inspections != inspections {
		t.Fatalf("failed forwarding should invalidate the route without inspecting Docker: %v", err)
	}
	if err := restarted.Ensure(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.Store.Get(ctx, "alice")
	if err != nil || recovered.HostEndpoint != driver.endpoint || recovered.HostID != record.HostID || driver.inspections == inspections {
		t.Fatalf("reopening the account did not rediscover the existing Host: %v", err)
	}
}

func TestManualDockerStopDoesNotResumeFailedStopOnOpen(t *testing.T) {
	driver := &fakeDriver{}
	manager := newTestManager(t, "docker", driver, func(w http.ResponseWriter, r *http.Request) { controlReply(w, r) })
	ctx := t.Context()
	if err := manager.CreateAccount(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	first, _ := manager.Store.Get(ctx, "alice")
	driver.afterStop = errors.New("stop failed")
	if out := controllerCall(manager, "POST", "/v1/host/stop", "alice", ""); out.Code != 503 {
		t.Fatalf("failed stop status = %d", out.Code)
	}
	restarted := New(manager.Config, driver, manager.Store)
	if err := restarted.Ensure(ctx, "alice"); err != nil || driver.stopCalls != 1 {
		t.Fatalf("opening account should reuse the live Host: %v, stop calls = %d", err, driver.stopCalls)
	}
	driver.afterStop = nil
	if err := restarted.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	record, _ := restarted.Store.Get(ctx, "alice")
	if record.HostID != first.HostID || record.HostEndpoint != "" || record.DesiredState != stopped || driver.instances[first.HostID].State != stopped {
		t.Fatal("Docker stop did not preserve its container")
	}
	if err := restarted.Ensure(ctx, "alice"); err != nil || len(driver.specs) != 1 {
		t.Fatalf("could not resume the same Docker container: %v", err)
	}
	if err := restarted.Delete(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	record, _ = restarted.Store.Get(ctx, "alice")
	if record.HostID != "" || record.HostToken != "" || len(driver.instances) != 0 {
		t.Fatal("explicit release did not clear instance state while retaining the account")
	}
}

func TestECICreateRetryAndManualRelease(t *testing.T) {
	driver := &fakeDriver{afterCreate: errors.New("create response timed out")}
	manager := newTestManager(t, "eci", driver, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/host/shutdown":
			_ = driver.Stop(r.Context(), runtime.ResourceRef{ID: driver.specs[0].RunID})
			w.WriteHeader(http.StatusAccepted)
		default:
			controlReply(w, r)
		}
	})
	if err := manager.CreateAccount(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(t.Context(), "alice"); err == nil {
		t.Fatal("expected lost create response")
	}
	first, _ := manager.Store.Get(t.Context(), "alice")
	restarted := New(manager.Config, driver, manager.Store)
	if err := restarted.Ensure(t.Context(), "alice"); err != nil || len(driver.specs) != 1 {
		t.Fatalf("ECI create retry did not reuse its Host ID: %v", err)
	}
	if err := restarted.Stop(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	record, _ := restarted.Store.Get(t.Context(), "alice")
	if record.HostID != "" || record.HostToken != "" || record.HostEndpoint != "" || len(driver.instances) != 0 {
		t.Fatal("ECI manual stop did not preserve the account while clearing compute")
	}
	if out := controllerCall(restarted, "GET", "/v1/hosts/"+first.HostID, "alice", ""); out.Code != 404 {
		t.Fatalf("released Host lookup status = %d", out.Code)
	}
}

func TestECIStopTimeoutKeepsInstanceForExplicitRetry(t *testing.T) {
	driver := &fakeDriver{delayDelete: true}
	manager := newTestManager(t, "eci", driver, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/host/shutdown" {
			driver.mu.Lock()
			for id, instance := range driver.instances {
				instance.State = stopped
				driver.instances[id] = instance
			}
			driver.mu.Unlock()
		}
		controlReply(w, r)
	})
	ctx := t.Context()
	if err := manager.CreateAccount(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	before, _ := manager.Store.Get(ctx, "alice")
	manager.Config.Runtime.StartTimeout = 100 * time.Millisecond
	if out := controllerCall(manager, "POST", "/v1/host/stop", "alice", ""); out.Code != 504 {
		t.Fatalf("unconfirmed release status = %d", out.Code)
	}
	after, _ := manager.Store.Get(ctx, "alice")
	if after.HostID != before.HostID || driver.deleteCalls != 1 {
		t.Fatal("release timeout lost the instance or retried the deletion")
	}
	driver.delayDelete = false
	if err := manager.Stop(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	after, _ = manager.Store.Get(ctx, "alice")
	if after.HostID != "" || after.DesiredState != stopped || driver.deleteCalls != 2 {
		t.Fatal("explicit retry did not confirm release and clear the instance")
	}
}

func TestModelSettingsWaitForHostAndReportSavedChanges(t *testing.T) {
	var revision int64
	var reject bool
	var mu sync.Mutex
	manager := newTestManager(t, "docker", &fakeDriver{}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != modelsettings.InternalPath {
			controlReply(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]int64{"revision": revision})
			return
		}
		if reject {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var snapshot modelsettings.Snapshot
		if err := json.NewDecoder(r.Body).Decode(&snapshot); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		revision = snapshot.Revision
		w.WriteHeader(http.StatusNoContent)
	})
	if err := manager.CreateAccount(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	out := controllerCall(manager, "POST", settingsPath+"/endpoints", "alice", `{"name":"Fixture","baseUrl":"https://models.example/v1","apiKey":"fixture","models":["first"]}`)
	var endpoint struct{ EndpointID string }
	if out.Code != http.StatusCreated || json.Unmarshal(out.Body.Bytes(), &endpoint) != nil || endpoint.EndpointID == "" {
		t.Fatalf("save while stopped: %d %s", out.Code, out.Body)
	}
	if err := manager.Ensure(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if revision != 1 {
		mu.Unlock()
		t.Fatal("opening the account did not apply the saved settings")
	}
	reject = true
	mu.Unlock()
	path := settingsPath + "/endpoints/" + endpoint.EndpointID
	out = controllerCall(manager, "PATCH", path, "alice", `{"models":["second"]}`)
	if out.Code != http.StatusServiceUnavailable || !strings.Contains(out.Body.String(), settingsSavedHostApplyFailed) {
		t.Fatalf("unconfirmed apply: %d %s", out.Code, out.Body)
	}
	state, err := manager.Store.LoadModels(t.Context(), "alice")
	if err != nil || state.Catalog.Default().ModelID != "second" {
		t.Fatalf("settings were not saved: %v", err)
	}
	mu.Lock()
	reject = false
	mu.Unlock()
	out = controllerCall(manager, "POST", "/v1/sessions/3a45e598-70cb-4654-9336-63df17360a20/messages", "alice", `{"content":"hello"}`)
	if out.Code != http.StatusServiceUnavailable || !strings.Contains(out.Body.String(), "host_model_settings_unavailable") {
		t.Fatalf("a message should report stale settings without applying them: %d %s", out.Code, out.Body)
	}
	out = controllerCall(manager, "PATCH", path, "alice", `{"models":["second"]}`)
	if out.Code != http.StatusOK {
		t.Fatalf("explicit settings retry: %d %s", out.Code, out.Body)
	}
	state, _ = manager.Store.LoadModels(t.Context(), "alice")
	mu.Lock()
	defer mu.Unlock()
	if revision != state.Revision {
		t.Fatal("successful settings response preceded Host acknowledgment")
	}
}

func (s *memoryStore) LoadModels(_ context.Context, id string) (modelsettings.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.Marshal(s.models[id])
	var snapshot modelsettings.Snapshot
	_ = json.Unmarshal(raw, &snapshot)
	if snapshot.Sessions == nil {
		snapshot.Sessions = make(map[string]modelsettings.Selection)
	}
	return snapshot, nil
}

func (s *memoryStore) SaveModelCatalog(_ context.Context, id string, catalog modelsettings.Catalog) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.models == nil {
		s.models = make(map[string]modelsettings.Snapshot)
	}
	state := s.models[id]
	state.Catalog = catalog
	state.Revision++
	s.models[id] = state
	return state.Revision, nil
}

func (s *memoryStore) SaveSessionModel(_ context.Context, id, sessionID string, choice modelsettings.Selection) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.models[id]
	if state.Sessions == nil {
		state.Sessions = make(map[string]modelsettings.Selection)
	}
	state.Sessions[sessionID] = choice
	state.Revision++
	s.models[id] = state
	return state.Revision, nil
}
