package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go-bot/internal/config"
	"go-bot/internal/controller"
	"go-bot/plugin"
	dockerapi "go-bot/thirdparty/docker"
)

func TestDockerClientPresenceUsesOnlyTheUserHash(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("requires Docker and Redis")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	docker := dockerapi.Client{}
	raw, err := docker.Run(ctx, nil, "run", "--rm", "-d", "-p", "127.0.0.1::6379", "redis:8-alpine")
	if err != nil {
		t.Fatal(err)
	}
	container := strings.TrimSpace(string(raw))
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = docker.Run(cleanup, nil, "rm", "-f", container)
	})
	raw, err = docker.Run(ctx, nil, "port", container, "6379/tcp")
	if err != nil {
		t.Fatal(err)
	}
	address := strings.TrimSpace(string(raw))
	var store *controller.RedisStore
	for store == nil {
		store, err = controller.OpenRedis(ctx, address)
		if err != nil {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	defer store.Close()
	for _, user := range []string{"alice", "bob"} {
		if err := store.Create(ctx, user, controller.Account{DesiredState: "stopped"}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UnixMilli()
	var group sync.WaitGroup
	for _, device := range []controller.ClientIdentity{{Type: "desktop", Platform: "darwin", Model: "Work Mac"}, {Type: "mobile", Platform: "ios", Model: "iPhone 13 mini"}} {
		group.Go(func() {
			if err := store.UpdateClientPresence(ctx, "alice", controller.ClientUpdate{Identity: device, ConnectionID: device.Type + "-connection", Now: now, Connected: true}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	// A second Controller/Redis connection sees the same authoritative leases.
	second, err := controller.OpenRedis(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	devices, err := second.ClientDevices(ctx, "alice", now)
	if err != nil || len(devices) != 2 || devices[0].Status != "connected" || devices[1].Status != "connected" {
		t.Fatal(devices, err)
	}
	if other, err := second.ClientDevices(ctx, "bob", now); err != nil || len(other) != 0 {
		t.Fatal("account isolation", other, err)
	}
	if err := store.SavePluginSettings(ctx, "alice", plugin.Settings{}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, "alice", controller.Account{DesiredState: "stopped"}); err != nil {
		t.Fatal(err)
	}
	devices, err = second.ClientDevices(ctx, "alice", now+90000)
	if err != nil || len(devices) != 2 || devices[0].Status != "disconnected" || devices[1].Status != "disconnected" {
		t.Fatal("expired leases or other field writes", devices, err)
	}
	manager := controller.New(config.Default(), nil, second)
	request := httptest.NewRequest(http.MethodGet, "/v1/clients", nil)
	request.Header.Set(controller.UserHeader, "alice")
	response := httptest.NewRecorder()
	manager.Handler().ServeHTTP(response, request)
	var view struct{ Clients []controller.ClientDevice }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &view) != nil || len(view.Clients) != 2 || strings.Contains(response.Body.String(), "connections") || strings.Contains(response.Body.String(), "-connection") {
		t.Fatal(response.Code, response.Body)
	}
	raw, err = docker.Run(ctx, nil, "exec", container, "redis-cli", "--raw", "KEYS", "*")
	if err != nil || len(strings.Fields(string(raw))) != 2 || !strings.Contains(string(raw), "user:alice") || !strings.Contains(string(raw), "user:bob") {
		t.Fatal("extra Redis state", string(raw), err)
	}
	raw, err = docker.Run(ctx, nil, "exec", container, "redis-cli", "--raw", "HGET", "user:alice", "clients")
	if err != nil || !strings.Contains(string(raw), "Work Mac") || !strings.Contains(string(raw), "iPhone 13 mini") {
		t.Fatal("missing clients field", err)
	}
	// Upgrade an existing account without leaking old Web history or losing native leases.
	var records map[string]json.RawMessage
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatal(err)
	}
	records["old-browser"] = json.RawMessage(`{"id":"old-browser","type":"web","lastSeenAt":1000,"connections":{"old-tab":9999999999999}}`)
	records["old-unnamed-phone"] = json.RawMessage(`{"id":"old-unnamed-phone","type":"mobile","platform":"ios","lastSeenAt":1000}`)
	seed, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docker.Run(ctx, nil, "exec", container, "redis-cli", "HSET", "user:alice", "clients", string(seed)); err != nil {
		t.Fatal(err)
	}
	group.Go(func() {
		if _, err := second.ClientDevices(ctx, "alice", now); err != nil {
			t.Error(err)
		}
	})
	group.Go(func() {
		if err := store.UpdateClientPresence(ctx, "alice", controller.ClientUpdate{Identity: controller.ClientIdentity{Type: "mobile", Platform: "ios", Model: "iPhone 13 mini"}, ConnectionID: "mobile-after-migration", Now: now, Connected: true}); err != nil {
			t.Error(err)
		}
	})
	group.Wait()
	devices, err = second.ClientDevices(ctx, "alice", now)
	if err != nil || len(devices) != 3 {
		t.Fatal("upgrade removed native history", devices, err)
	}
	// Even an old Web client's renewal cannot recreate its record.
	if err := store.UpdateClientPresence(ctx, "alice", controller.ClientUpdate{Identity: controller.ClientIdentity{ID: "old-browser", Type: "web"}, ConnectionID: "old-tab", Now: now, Connected: true}); err != nil {
		t.Fatal(err)
	}
	raw, err = docker.Run(ctx, nil, "exec", container, "redis-cli", "--raw", "HGET", "user:alice", "clients")
	if err != nil || strings.Contains(string(raw), "old-browser") || !strings.Contains(string(raw), "old-unnamed-phone") || !strings.Contains(string(raw), "mobile-after-migration") {
		t.Fatal("Web history survived or native history was removed", string(raw), err)
	}
}
