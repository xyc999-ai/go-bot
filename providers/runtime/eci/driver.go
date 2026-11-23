package eci

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"go-bot/internal/config"
	"go-bot/runtime"
	"go-bot/thirdparty/aliyun"
)

type API interface {
	DescribeImageCache(context.Context, string) (aliyun.ImageCache, error)
	Create(context.Context, aliyun.Create) (string, error)
	List(context.Context, string, map[string]string) ([]aliyun.Group, error)
	Delete(context.Context, string) error
}
type Driver struct{ Client API }

func (*Driver) Name() string { return "eci" }
func (*Driver) Capabilities() runtime.Capabilities {
	return runtime.Capabilities{StorageTypes: []string{"container", "nas"}}
}
func (*Driver) Validate(s runtime.Spec) error {
	if s.ECI.Region == "" || s.ECI.VSwitchID == "" || s.ECI.SecurityGroupID == "" || s.RunID == "" {
		return errors.New("incomplete ECI configuration")
	}
	if !config.ValidImageCacheID(s.ECI.ImageCacheID) {
		return errors.New("ECI_IMAGE_CACHE_ID must be a valid imc- ID")
	}
	if !config.ValidImage(s.Image) {
		return errors.New("HOST_IMAGE must be an image reference")
	}
	for key, value := range s.Environment {
		if len(value) > 256 {
			return fmt.Errorf("ECI environment value for %s exceeds 256 bytes", key)
		}
	}
	if s.Storage.Type != "container" && s.Storage.Type != "nas" {
		return errors.New("unsupported ECI storage")
	}
	if s.Storage.Type == "nas" && (s.Storage.MountTarget == "" || !strings.HasPrefix(s.Storage.Path, "/") || strings.Contains(s.Storage.Path, "..")) {
		return errors.New("invalid NAS mount plan")
	}
	return nil
}
func tags(s runtime.Spec) map[string]string {
	return map[string]string{"component": "go-bot-host", "deployment-id": s.DeploymentID, "run-id": s.RunID}
}

// checkCache is used only by the explicitly requested doctor command.
// Normal instance creation passes the configured image and cache ID directly.
func (d *Driver) checkCache(ctx context.Context, s runtime.Spec) error {
	cache, err := d.Client.DescribeImageCache(ctx, s.ECI.ImageCacheID)
	if err != nil {
		return err
	}
	image, err := cache.ReadyImage(s.ECI.ImageCacheID, s.ECI.Region, time.Now())
	if err != nil {
		return err
	}
	if !config.ValidImage(image) {
		return errors.New("ECI cache contains an invalid Host image reference")
	}
	return nil
}

func (d *Driver) Preflight(ctx context.Context, s runtime.Spec) error {
	if err := d.Validate(s); err != nil {
		return err
	}
	groups, err := d.Client.List(ctx, "", deploymentTags(s))
	if err != nil {
		return err
	}
	for _, g := range groups {
		if !terminal(g.State) && g.Tags["run-id"] == s.RunID {
			return nil // Reconnecting to an existing instance does not consume its old cache.
		}
	}
	return d.checkCache(ctx, s)
}

func deploymentTags(s runtime.Spec) map[string]string {
	return map[string]string{"component": "go-bot-host", "deployment-id": s.DeploymentID}
}
func terminal(state string) bool {
	switch state {
	case "Succeeded", "Failed", "ScheduleFailed", "Expired", "Terminated":
		return true
	}
	return false
}
func fromGroup(g aliyun.Group) runtime.Instance {
	state := "starting"
	if terminal(g.State) {
		state = "stopped"
	} else if g.State == "Running" {
		state = "running"
	}
	endpoint := ""
	ip := net.ParseIP(g.IP)
	if state == "running" && ip != nil && ip.IsPrivate() {
		endpoint = "http://" + net.JoinHostPort(ip.String(), "8080")
	}
	return runtime.Instance{Ref: runtime.ResourceRef{Provider: "eci", ID: g.ID}, State: state, Endpoint: endpoint, RunID: g.Tags["run-id"], DeploymentID: g.Tags["deployment-id"]}
}
func (d *Driver) Inspect(ctx context.Context, r runtime.ResourceRef) (runtime.Instance, error) {
	groups, err := d.Client.List(ctx, r.ID, nil)
	if err != nil {
		return runtime.Instance{}, err
	}
	if len(groups) == 0 {
		return runtime.Instance{}, runtime.ErrNotFound
	}
	if len(groups) != 1 || groups[0].Tags["component"] != "go-bot-host" {
		return runtime.Instance{}, errors.New("ECI resource identity mismatch")
	}
	return fromGroup(groups[0]), nil
}

func (d *Driver) Find(ctx context.Context, s runtime.Spec) (runtime.Instance, error) {
	filter := tags(s)
	groups, err := d.Client.List(ctx, "", filter)
	if err != nil {
		return runtime.Instance{}, err
	}
	var result runtime.Instance
	for _, g := range groups {
		if g.Tags["component"] != filter["component"] || g.Tags["run-id"] != s.RunID || g.Tags["deployment-id"] != s.DeploymentID {
			continue
		}
		if result.Ref.ID != "" {
			return runtime.Instance{}, errors.New("multiple ECI groups match the Host ID")
		}
		result = fromGroup(g)
	}
	if result.Ref.ID == "" {
		return result, runtime.ErrNotFound
	}
	return result, nil
}
func (d *Driver) Ensure(ctx context.Context, s runtime.Spec) (runtime.Instance, error) {
	if err := d.Validate(s); err != nil {
		return runtime.Instance{}, err
	}
	groups, err := d.Client.List(ctx, "", deploymentTags(s))
	if err != nil {
		return runtime.Instance{}, err
	}
	var active *aliyun.Group
	for _, g := range groups {
		if terminal(g.State) {
			continue
		}
		if active != nil {
			return runtime.Instance{}, errors.New("multiple active ECIs match this deployment")
		}
		current := g
		active = &current
	}
	id := ""
	if active != nil {
		if active.Tags["run-id"] != s.RunID {
			return fromGroup(*active), errors.New("existing ECI belongs to a different run")
		}
		id = active.ID
	} else {
		req := aliyun.Create{Name: "go-bot-" + s.RunID, ClientToken: s.RunID, Image: s.Image, ImageCacheID: s.ECI.ImageCacheID, VSwitchID: s.ECI.VSwitchID, SecurityGroupID: s.ECI.SecurityGroupID, CPU: s.ECI.CPU, MemoryGiB: s.ECI.MemoryGiB, Environment: s.Environment, Tags: tags(s)}
		if s.Storage.Type == "nas" {
			req.NASMountTarget = s.Storage.MountTarget
			req.NASPath = s.Storage.Path
		}
		id, err = d.Client.Create(ctx, req)
		if err != nil {
			return runtime.Instance{}, err
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		instance, e := d.Inspect(ctx, runtime.ResourceRef{Provider: "eci", ID: id})
		if e != nil {
			return instance, e
		}
		if instance.State == "stopped" {
			return instance, fmt.Errorf("ECI %s exited before ready", id)
		}
		if instance.Endpoint != "" {
			return instance, nil
		}
		select {
		case <-ctx.Done():
			return instance, ctx.Err()
		case <-ticker.C:
		}
	}
}
func (d *Driver) Stop(context.Context, runtime.ResourceRef) error {
	return fmt.Errorf("%w: ECI uses Host graceful shutdown; delete for forced termination", runtime.ErrUnsupported)
}
func (d *Driver) Delete(ctx context.Context, r runtime.ResourceRef) error {
	_, err := d.Inspect(ctx, r)
	if errors.Is(err, runtime.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return d.Client.Delete(ctx, r.ID)
}
