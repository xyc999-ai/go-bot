package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-bot/internal/config"
	"go-bot/thirdparty/aliyun"
)

type fakeCache struct {
	tokens       []string
	image        string
	status       string
	loseResponse bool
}

func (f *fakeCache) CreateImageCache(_ context.Context, req aliyun.CreateImageCache) (string, error) {
	f.tokens = append(f.tokens, req.ClientToken)
	f.image = req.Image
	if f.loseResponse {
		f.loseResponse = false
		return "", errors.New("lost create response")
	}
	return "imc-test", nil
}

func (f *fakeCache) DescribeImageCache(_ context.Context, id string) (aliyun.ImageCache, error) {
	return aliyun.ImageCache{ID: id, Region: "cn-hangzhou", Status: f.status, Images: []string{f.image}}, nil
}

func fixture() options {
	return options{
		Cloud:  aliyun.Credentials{AccessKeyID: "fixture-id", AccessKeySecret: "fixture-secret"},
		Region: "cn-hangzhou", Wait: time.Second,
		Request: aliyun.CreateImageCache{Image: "registry.example/host:v1", VSwitchID: "vsw-test", SecurityGroupID: "sg-test"},
	}
}

func TestCacheRetryKeepsClientToken(t *testing.T) {
	o := fixture()
	api := &fakeCache{status: "Ready", loseResponse: true}
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := execute(ctx, o, dir, "default", api, time.Millisecond); err == nil {
		t.Fatal("create uncertainty should be reported")
	}
	out, err := execute(ctx, o, dir, "default", api, time.Millisecond)
	if err != nil || out.CacheID != "imc-test" ||
		len(api.tokens) != 2 || api.tokens[0] != api.tokens[1] {
		t.Fatalf("retry did not reuse the cache operation: %v", err)
	}
	if _, err := execute(ctx, o, dir, "default", api, time.Millisecond); err != nil || len(api.tokens) != 2 {
		t.Fatalf("ready cache was not reused: %v", err)
	}
}

func TestWaitCanResumeAndNeverReturnsUnreadyID(t *testing.T) {
	o := fixture()
	o.Wait = 20 * time.Millisecond
	api := &fakeCache{status: "Creating"}
	dir := t.TempDir()
	ctx := context.Background()
	if out, err := execute(ctx, o, dir, "default", api, time.Millisecond); err == nil || out.CacheID != "" {
		t.Fatal("timed-out operation returned an unready cache")
	}
	api.status = "Failed"
	if out, err := execute(ctx, o, dir, "default", api, time.Millisecond); err == nil || out.CacheID != "" {
		t.Fatal("failed operation returned a cache")
	}
	api.status = "Ready"
	if out, err := execute(ctx, o, dir, "default", api, time.Millisecond); err != nil || out.CacheID != "imc-test" || len(api.tokens) != 1 {
		t.Fatalf("resume did not reuse the saved cache: %v", err)
	}
}

func TestPublicCacheUsesOfficialImageWithoutRegistryCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.eci")
	profile := "ECI_VSWITCH_ID=vsw-test\nECI_SECURITY_GROUP_ID=sg-test\nALIBABA_CLOUD_ACCESS_KEY_ID=fixture\nALIBABA_CLOUD_ACCESS_KEY_SECRET=fixture\n"
	if err := os.WriteFile(path, []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	o, err := readOptions(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if o.Request.Image != config.DefaultHostImage {
		t.Fatal("public cache did not preserve the multi-platform image reference for anonymous pulling")
	}

}
