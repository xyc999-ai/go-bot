package eci

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-bot/runtime"
	"go-bot/thirdparty/aliyun"
)

type fakeAPI struct {
	groups       []aliyun.Group
	creates      int
	last         aliyun.Create
	uncertain    bool
	cache        *aliyun.ImageCache
	cacheQueries int
	listTags     map[string]string
}

func (f *fakeAPI) DescribeImageCache(_ context.Context, id string) (aliyun.ImageCache, error) {
	f.cacheQueries++
	if f.cache != nil {
		return *f.cache, nil
	}
	return aliyun.ImageCache{ID: id, Region: "cn-hangzhou", Status: "Ready", Images: []string{"registry.example/host:v1"}}, nil
}

func (f *fakeAPI) List(_ context.Context, id string, filter map[string]string) ([]aliyun.Group, error) {
	if id == "" {
		f.listTags = filter
		return f.groups, nil
	}
	for _, g := range f.groups {
		if g.ID == id {
			return []aliyun.Group{g}, nil
		}
	}
	return nil, nil
}
func (f *fakeAPI) Create(_ context.Context, s aliyun.Create) (string, error) {
	f.creates++
	f.last = s
	f.groups = []aliyun.Group{{ID: "eci-test", State: "Running", IP: "10.1.2.3", Tags: s.Tags}}
	if f.uncertain {
		f.uncertain = false
		return "", errors.New("response lost after acceptance")
	}
	return "eci-test", nil
}
func (f *fakeAPI) Delete(_ context.Context, id string) error { f.groups = nil; return nil }
func testSpec() runtime.Spec {
	return runtime.Spec{DeploymentID: "deployment", RunID: "run", Image: "public/host:test", StartTimeout: time.Second, Storage: runtime.Storage{Type: "container"}, ECI: runtime.ECIOptions{Region: "cn-hangzhou", VSwitchID: "vsw-test", SecurityGroupID: "sg-test", ImageCacheID: "imc-test", CPU: 2, MemoryGiB: 4}, Environment: map[string]string{"MODEL_API_KEY": "model-key"}}
}
func TestECIContainerUsesCacheWithoutNAS(t *testing.T) {
	api := &fakeAPI{}
	d := &Driver{Client: api}
	s := testSpec()
	out, e := d.Ensure(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	if out.Endpoint != "http://10.1.2.3:8080" || api.last.NASMountTarget != "" || api.last.ImageCacheID != "imc-test" || api.last.Image != s.Image || api.cacheQueries != 0 {
		t.Fatal("unexpected endpoint, NAS or cache image")
	}
	s.Storage = runtime.Storage{Type: "nas"}
	if d.Validate(s) == nil {
		t.Fatal("incomplete explicit NAS plan accepted")
	}
}

func TestECIReplacementRetainsNASDataRoot(t *testing.T) {
	ctx := t.Context()
	api := &fakeAPI{}
	driver := &Driver{Client: api}
	spec := testSpec()
	spec.Storage = runtime.Storage{
		Type: "nas", MountTarget: "example.cn-hangzhou.nas.aliyuncs.com", Path: "/go-bot/deployment",
	}
	first, err := driver.Ensure(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	original := api.last
	if err := driver.Delete(ctx, first.Ref); err != nil {
		t.Fatal(err)
	}
	spec.RunID = "replacement-run"
	if _, err := driver.Ensure(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if api.creates != 2 || api.last.ClientToken == original.ClientToken ||
		api.last.NASPath != spec.Storage.Path || api.last.NASPath != original.NASPath ||
		api.last.NASMountTarget != spec.Storage.MountTarget || api.last.NASMountTarget != original.NASMountTarget {
		t.Fatal("replacement did not reuse the same NAS data root with a new create operation")
	}
}
func TestECIUncertainCreateIsReconciledWithoutSecondInstance(t *testing.T) {
	api := &fakeAPI{uncertain: true}
	d := &Driver{Client: api}
	s := testSpec()
	if _, e := d.Ensure(context.Background(), s); e == nil {
		t.Fatal("uncertain error lost")
	}
	out, e := d.Ensure(context.Background(), s)
	if e != nil || api.creates != 1 || out.Ref.ID != "eci-test" {
		t.Fatalf("uncertain creation duplicated: creates=%d err=%v", api.creates, e)
	}
	if api.last.ClientToken != s.RunID {
		t.Fatal("idempotency token changed")
	}
	api.groups = append(api.groups, aliyun.Group{ID: "eci-other", State: "Running", Tags: map[string]string{
		"component": "go-bot-host", "deployment-id": "other-account", "run-id": s.RunID,
	}})
	found, err := d.Find(t.Context(), s)
	if err != nil || found.Ref.ID != out.Ref.ID || api.listTags["run-id"] != s.RunID || api.listTags["deployment-id"] != s.DeploymentID {
		t.Fatalf("Find did not isolate the current account and instance: %v", err)
	}
}
func TestECIRefusesForeignRunAndMultipleActiveInstances(t *testing.T) {
	api := &fakeAPI{}
	d := &Driver{Client: api}
	s := testSpec()
	if _, e := d.Ensure(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	api.groups[0].Tags["run-id"] = "foreign"
	if _, e := d.Ensure(context.Background(), s); e == nil {
		t.Fatal("foreign run reused")
	}
	api.groups = append(api.groups, api.groups[0])
	if _, e := d.Ensure(context.Background(), s); e == nil {
		t.Fatal("duplicate active instances accepted")
	}
}

func TestECIDoctorValidatesCache(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*runtime.Spec, *aliyun.ImageCache)
	}{
		{"missing ID", func(s *runtime.Spec, _ *aliyun.ImageCache) { s.ECI.ImageCacheID = "" }},
		{"missing image", func(s *runtime.Spec, _ *aliyun.ImageCache) { s.Image = "" }},
		{"not ready", func(_ *runtime.Spec, c *aliyun.ImageCache) { c.Status = "Creating" }},
		{"wrong region", func(_ *runtime.Spec, c *aliyun.ImageCache) { c.Region = "cn-beijing" }},
		{"expired", func(_ *runtime.Spec, c *aliyun.ImageCache) { c.ExpiresAt = "2000-01-01T00:00:00Z" }},
		{"multiple images", func(_ *runtime.Spec, c *aliyun.ImageCache) { c.Images = append(c.Images, "registry.example/extra:v1") }},
		{"long env", func(s *runtime.Spec, _ *aliyun.ImageCache) { s.Environment["MODEL_URL"] = strings.Repeat("x", 257) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := testSpec()
			cache := aliyun.ImageCache{ID: s.ECI.ImageCacheID, Region: s.ECI.Region, Status: "Ready", Images: []string{"registry.example/host:v1"}}
			test.change(&s, &cache)
			api := &fakeAPI{cache: &cache}
			d := &Driver{Client: api}
			if err := d.Preflight(context.Background(), s); err == nil || api.creates != 0 {
				t.Fatalf("invalid cache reached create: %v", err)
			}
		})
	}
}

func TestECICreationDoesNotQueryCache(t *testing.T) {
	api := &fakeAPI{cache: &aliyun.ImageCache{Status: "Failed"}}
	driver := &Driver{Client: api}
	spec := testSpec()
	if _, err := driver.Ensure(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if api.cacheQueries != 0 || api.last.Image != spec.Image || api.last.ImageCacheID != spec.ECI.ImageCacheID {
		t.Fatal("creation must pass the configured image/cache pair directly to the cloud")
	}
}

func TestECIReattachesWithoutRequiringOldCache(t *testing.T) {
	api := &fakeAPI{}
	d := &Driver{Client: api}
	s := testSpec()
	if _, err := d.Ensure(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	api.cache = &aliyun.ImageCache{Status: "Failed"}
	queries := api.cacheQueries
	if err := d.Preflight(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Ensure(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if api.cacheQueries != queries || api.creates != 1 {
		t.Fatal("reattachment consulted a cache or created a new instance")
	}
}
