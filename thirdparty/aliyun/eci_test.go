package aliyun

import (
	"context"
	"encoding/json"
	"testing"

	eci "github.com/alibabacloud-go/eci-20180808/v3/client"
)

type describeGroupsStub struct {
	SDK
	requests []*eci.DescribeContainerGroupsRequest
}

func (s *describeGroupsStub) DescribeContainerGroups(r *eci.DescribeContainerGroupsRequest) (*eci.DescribeContainerGroupsResponse, error) {
	s.requests = append(s.requests, r)
	body := &eci.DescribeContainerGroupsResponseBody{}
	if len(s.requests) == 1 {
		body.NextToken = str("next")
	}
	return &eci.DescribeContainerGroupsResponse{Body: body}, nil
}

func TestListGroupsUsesSupportedPageSize(t *testing.T) {
	sdk := &describeGroupsStub{}
	client := ECI{SDK: sdk, Region: "cn-hangzhou"}
	if _, err := client.List(context.Background(), "", map[string]string{"component": "go-bot-host"}); err != nil {
		t.Fatal(err)
	}
	if len(sdk.requests) != 2 || value(sdk.requests[1].NextToken) != "next" {
		t.Fatal("list did not continue with the next page")
	}
	for _, request := range sdk.requests {
		if request.Limit == nil || *request.Limit != 20 {
			t.Fatal("DescribeContainerGroups supports at most 20 results per page")
		}
	}
}

func TestECICacheLaunchAndOptionalNAS(t *testing.T) {
	c := ECI{Region: "cn-hangzhou"}
	s := Create{Image: "public/host:1", ImageCacheID: "imc-test", Environment: map[string]string{"MODEL_API_KEY": "test-key"}, Tags: map[string]string{"component": "go-bot-host"}}
	r := c.request(s)
	if len(r.Volume) != 0 || len(r.Container[0].VolumeMount) != 0 {
		t.Fatal("container-only ECI gained mandatory volumes")
	}
	if value(r.ImageSnapshotId) != "imc-test" || r.AutoMatchImageCache == nil || *r.AutoMatchImageCache ||
		value(r.Container[0].ImagePullPolicy) != "Never" || len(r.ImageRegistryCredential) != 0 {
		t.Fatal("ECI launch must exclusively use the selected cache")
	}
	s.NASMountTarget = "test.cn-hangzhou.nas.aliyuncs.com"
	s.NASPath = "/go-bot/deploy"
	s.ImageCacheID = "imc-test"
	r = c.request(s)
	if len(r.Volume) != 1 || len(r.Container[0].VolumeMount) != 1 || r.ImageSnapshotId == nil {
		t.Fatal("NAS/cache plan missing")
	}
	mount := r.Container[0].VolumeMount[0]
	if value(mount.MountPath) != "/root/harness" || mount.ReadOnly == nil || *mount.ReadOnly {
		t.Fatalf("NAS must be mounted read-write at the Host data root: %+v", mount)
	}
	var options map[string]string
	if err := json.Unmarshal([]byte(value(r.Volume[0].FlexVolume.Options)), &options); err != nil {
		t.Fatal(err)
	}
	if options["path"] != s.NASPath || options["server"] != s.NASMountTarget {
		t.Fatalf("wrong NAS mount options: %+v", options)
	}
}

func TestImageCacheUsesPublicImage(t *testing.T) {
	c := ECI{Region: "cn-hangzhou"}
	s := CreateImageCache{Image: "registry.example/host:v1", ClientToken: "token", Name: "cache", VSwitchID: "vsw-test", SecurityGroupID: "sg-test"}
	r := c.imageCacheRequest(s)
	if len(r.ImageRegistryCredential) != 0 || len(r.Image) != 1 || value(r.Image[0]) != s.Image ||
		value(r.ClientToken) != s.ClientToken || value(r.VSwitchId) != s.VSwitchID || value(r.SecurityGroupId) != s.SecurityGroupID {
		t.Fatal("incorrect image cache request")
	}

}

func TestECISearchSettingsArePassedToHost(t *testing.T) {
	c := ECI{Region: "cn-hangzhou"}
	want := map[string]string{
		"WEB_SEARCH_PROVIDER": "deepseek",
		"WEB_SEARCH_API_KEY":  "fixture-search-key",
	}
	r := c.request(Create{Image: "public/host:1", Environment: want})
	host := r.Container[0]
	if host.EnvironmentVarHide == nil || !*host.EnvironmentVarHide {
		t.Fatal("ECI must hide Host environment values")
	}
	got := make(map[string]string)
	for _, entry := range host.EnvironmentVar {
		got[*entry.Key] = *entry.Value
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("ECI did not pass %s to Host", key)
		}
	}
}
