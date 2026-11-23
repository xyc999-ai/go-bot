// Package aliyun contains first-party wrappers around Alibaba Cloud SDK calls.
// All clients explicitly use the same ordinary AccessKey pair.
package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	eci "github.com/alibabacloud-go/eci-20180808/v3/client"
)

type Credentials struct{ AccessKeyID, AccessKeySecret string }
type SDK interface {
	CreateContainerGroup(*eci.CreateContainerGroupRequest) (*eci.CreateContainerGroupResponse, error)
	DescribeContainerGroups(*eci.DescribeContainerGroupsRequest) (*eci.DescribeContainerGroupsResponse, error)
	DeleteContainerGroup(*eci.DeleteContainerGroupRequest) (*eci.DeleteContainerGroupResponse, error)
	CreateImageCache(*eci.CreateImageCacheRequest) (*eci.CreateImageCacheResponse, error)
	DescribeImageCaches(*eci.DescribeImageCachesRequest) (*eci.DescribeImageCachesResponse, error)
}
type ECI struct {
	SDK    SDK
	Region string
}
type Group struct {
	ID, State, IP string
	Tags          map[string]string
}
type Create struct {
	Name, ClientToken, Image, ImageCacheID, VSwitchID, SecurityGroupID string
	CPU, MemoryGiB                                                     float32
	Environment, Tags                                                  map[string]string
	NASMountTarget, NASPath                                            string
}

func NewECI(region string, c Credentials) (*ECI, error) {
	if c.AccessKeyID == "" || c.AccessKeySecret == "" {
		return nil, errors.New("Alibaba Cloud AccessKey pair is required")
	}
	endpoint := "eci." + region + ".aliyuncs.com"
	connect, read := 10000, 20000
	client, err := eci.NewClient(&openapi.Config{AccessKeyId: &c.AccessKeyID, AccessKeySecret: &c.AccessKeySecret, RegionId: &region, Endpoint: &endpoint, ConnectTimeout: &connect, ReadTimeout: &read})
	if err != nil {
		return nil, errors.New("cannot initialize ECI client")
	}
	return &ECI{SDK: client, Region: region}, nil
}
func str(s string) *string { return &s }
func boolean(b bool) *bool { return &b }
func (c *ECI) request(s Create) *eci.CreateContainerGroupRequest {
	port := int32(8080)
	container := &eci.CreateContainerGroupRequestContainer{Name: str("host"), Image: &s.Image, Command: []*string{str("/usr/local/bin/host")}, WorkingDir: str("/root/harness"), EnvironmentVarHide: boolean(true), Port: []*eci.CreateContainerGroupRequestContainerPort{{Port: &port, Protocol: str("TCP")}}}
	container.ImagePullPolicy = str("Never")
	for k, v := range s.Environment {
		container.EnvironmentVar = append(container.EnvironmentVar, &eci.CreateContainerGroupRequestContainerEnvironmentVar{Key: str(k), Value: str(v)})
	}
	r := &eci.CreateContainerGroupRequest{RegionId: &c.Region, ContainerGroupName: &s.Name, ClientToken: &s.ClientToken, Cpu: &s.CPU, Memory: &s.MemoryGiB, VSwitchId: &s.VSwitchID, SecurityGroupId: &s.SecurityGroupID, RestartPolicy: str("Never"), Container: []*eci.CreateContainerGroupRequestContainer{container}}
	r.AutoMatchImageCache = boolean(false)
	r.ImageSnapshotId = &s.ImageCacheID
	r.ImageAccelerateMode = str("imc")
	for k, v := range s.Tags {
		r.Tag = append(r.Tag, &eci.CreateContainerGroupRequestTag{Key: str(k), Value: str(v)})
	}
	if s.NASMountTarget != "" {
		options, _ := json.Marshal(map[string]string{"server": s.NASMountTarget, "path": s.NASPath, "vers": "3", "options": "nolock,tcp,noresvport"})
		r.Volume = []*eci.CreateContainerGroupRequestVolume{{Name: str("data"), Type: str("FlexVolume"), FlexVolume: &eci.CreateContainerGroupRequestVolumeFlexVolume{Driver: str("alicloud/nas"), Options: str(string(options))}}}
		container.VolumeMount = []*eci.CreateContainerGroupRequestContainerVolumeMount{{Name: str("data"), MountPath: str("/root/harness"), ReadOnly: boolean(false)}}
	}
	return r
}
func (c *ECI) Create(ctx context.Context, s Create) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.Image == "" || s.ImageCacheID == "" {
		return "", errors.New("ECI launch requires HOST_IMAGE and ECI_IMAGE_CACHE_ID")
	}
	res, err := c.SDK.CreateContainerGroup(c.request(s))
	if err != nil {
		return "", errors.New("ECI create failed; query the saved operation before retrying")
	}
	if res == nil || res.Body == nil || res.Body.ContainerGroupId == nil {
		return "", errors.New("ECI create returned no resource ID")
	}
	return *res.Body.ContainerGroupId, nil
}
func (c *ECI) List(ctx context.Context, id string, tags map[string]string) ([]Group, error) {
	var groups []Group
	var cursor *string
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		limit := int32(20)
		r := &eci.DescribeContainerGroupsRequest{RegionId: &c.Region, NextToken: cursor, Limit: &limit}
		if id != "" {
			b, _ := json.Marshal([]string{id})
			r.ContainerGroupIds = str(string(b))
		}
		for k, v := range tags {
			r.Tag = append(r.Tag, &eci.DescribeContainerGroupsRequestTag{Key: str(k), Value: str(v)})
		}
		res, err := c.SDK.DescribeContainerGroups(r)
		if err != nil {
			return nil, errors.New("ECI describe failed; check region, network and AccessKey permissions")
		}
		if res == nil || res.Body == nil {
			return nil, errors.New("ECI describe returned no body")
		}
		for _, g := range res.Body.ContainerGroups {
			if g == nil {
				continue
			}
			item := Group{Tags: map[string]string{}}
			if g.ContainerGroupId != nil {
				item.ID = *g.ContainerGroupId
			}
			if g.Status != nil {
				item.State = *g.Status
			}
			if g.IntranetIp != nil {
				item.IP = *g.IntranetIp
			}
			for _, t := range g.Tags {
				if t != nil && t.Key != nil && t.Value != nil {
					item.Tags[*t.Key] = *t.Value
				}
			}
			groups = append(groups, item)
		}
		cursor = res.Body.NextToken
		if cursor == nil || *cursor == "" {
			return groups, nil
		}
	}
}
func (c *ECI) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := c.SDK.DeleteContainerGroup(&eci.DeleteContainerGroupRequest{RegionId: &c.Region, ContainerGroupId: &id})
	if err != nil {
		return fmt.Errorf("ECI delete failed for %s", strings.TrimSpace(id))
	}
	return nil
}
