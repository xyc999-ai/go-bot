package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"go-bot/internal/config"
	"go-bot/thirdparty/aliyun"
	"gopkg.in/yaml.v3"
)

type options struct {
	Cloud   aliyun.Credentials
	Region  string
	Request aliyun.CreateImageCache
	Wait    time.Duration
}

type tuning struct {
	SizeGiB       int32         `yaml:"size_gib"`
	RetentionDays int32         `yaml:"retention_days"`
	Wait          time.Duration `yaml:"wait_timeout"`
}

var imageDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func readOptions(profile, tuningPath string) (options, error) {
	c, err := config.Load("", profile)
	if err != nil {
		return options{}, err
	}
	t := tuning{Wait: 90 * time.Minute}
	if tuningPath != "" {
		data, err := os.ReadFile(tuningPath)
		if err != nil {
			return options{}, err
		}
		d := yaml.NewDecoder(bytes.NewReader(data))
		d.KnownFields(true)
		if d.Decode(&t) != nil || d.Decode(new(any)) != io.EOF {
			return options{}, errors.New("invalid buildeci tuning YAML")
		}
	}
	if t.Wait <= 0 || (t.SizeGiB != 0 && t.SizeGiB < 20) || t.RetentionDays < 0 {
		return options{}, errors.New("buildeci requires positive wait_timeout, size_gib of at least 20 if set, and nonnegative retention_days")
	}
	image := c.Runtime.Image
	server, name, qualified := strings.Cut(image, "/")
	last := name[strings.LastIndex(name, "/")+1:]
	_, tag, tagged := strings.Cut(last, ":")
	_, digest, pinned := strings.Cut(image, "@")
	u, urlErr := url.Parse("https://" + server)
	if !config.ValidImage(image) || !qualified || last == "" || urlErr != nil || u.User != nil ||
		u.Host != server || u.Hostname() == "" || !strings.ContainsAny(server, ".:") ||
		(pinned && !imageDigest.MatchString(digest)) ||
		(!pinned && (!tagged || tag == "")) {
		return options{}, errors.New("HOST_IMAGE must be a registry-qualified image with an explicit tag or SHA256 digest")
	}
	r := c.Runtime.ECI
	a := c.Cloud.Aliyun.Credentials
	for _, field := range []struct{ name, value string }{
		{"ECI_REGION", r.Region}, {"ECI_VSWITCH_ID", r.VSwitchID}, {"ECI_SECURITY_GROUP_ID", r.SecurityGroupID},
		{"ALIBABA_CLOUD_ACCESS_KEY_ID", a.AccessKeyID}, {"ALIBABA_CLOUD_ACCESS_KEY_SECRET", a.AccessKeySecret},
	} {
		if strings.TrimSpace(field.value) == "" {
			return options{}, fmt.Errorf("missing %s", field.name)
		}
	}
	return options{
		Cloud:  aliyun.Credentials{AccessKeyID: a.AccessKeyID, AccessKeySecret: a.AccessKeySecret},
		Region: r.Region, Wait: t.Wait,
		Request: aliyun.CreateImageCache{
			Image: image, VSwitchID: r.VSwitchID, SecurityGroupID: r.SecurityGroupID,
			SizeGiB: t.SizeGiB, RetentionDays: t.RetentionDays,
		},
	}, nil
}
