package aliyun

import (
	"context"
	"errors"
	"fmt"
	"time"

	eci "github.com/alibabacloud-go/eci-20180808/v3/client"
)

var ErrImageCacheNotFound = errors.New("ECI image cache not found")

type CreateImageCache struct {
	Name, ClientToken, Image, VSwitchID, SecurityGroupID string
	SizeGiB, RetentionDays                               int32
}

type ImageCache struct {
	ID, Region, Status, ExpiresAt string
	Images                        []string
}

// ReadyImage verifies that the cache can identify one Host image.
func (c ImageCache) ReadyImage(id, region string, now time.Time) (string, error) {
	if c.ID != id || c.Region != region {
		return "", errors.New("ECI cache identity or region mismatch")
	}
	if c.Status != "Ready" {
		return "", fmt.Errorf("ECI image cache %s is not Ready", id)
	}
	if c.ExpiresAt != "" {
		expires, err := time.Parse(time.RFC3339, c.ExpiresAt)
		if err != nil || !expires.After(now) {
			return "", fmt.Errorf("ECI image cache %s has expired or has an invalid expiry", id)
		}
	}
	if len(c.Images) != 1 || c.Images[0] == "" {
		return "", errors.New("ECI image cache must contain exactly one Host image")
	}
	return c.Images[0], nil
}

func (c *ECI) imageCacheRequest(s CreateImageCache) *eci.CreateImageCacheRequest {
	r := &eci.CreateImageCacheRequest{
		RegionId: &c.Region, ImageCacheName: &s.Name, ClientToken: &s.ClientToken,
		Image: []*string{&s.Image}, VSwitchId: &s.VSwitchID, SecurityGroupId: &s.SecurityGroupID,
	}
	if s.SizeGiB > 0 {
		r.ImageCacheSize = &s.SizeGiB
	}
	if s.RetentionDays > 0 {
		r.RetentionDays = &s.RetentionDays
	}
	return r
}

func (c *ECI) CreateImageCache(ctx context.Context, s CreateImageCache) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	res, err := c.SDK.CreateImageCache(c.imageCacheRequest(s))
	if err != nil {
		return "", errors.New("CreateImageCache failed; retry the same saved operation to reconcile")
	}
	if res == nil || res.Body == nil || res.Body.ImageCacheId == nil || *res.Body.ImageCacheId == "" {
		return "", errors.New("CreateImageCache returned no cache ID")
	}
	return *res.Body.ImageCacheId, nil
}

func (c *ECI) DescribeImageCache(ctx context.Context, id string) (ImageCache, error) {
	if err := ctx.Err(); err != nil {
		return ImageCache{}, err
	}
	res, err := c.SDK.DescribeImageCaches(&eci.DescribeImageCachesRequest{RegionId: &c.Region, ImageCacheId: &id})
	if err != nil {
		return ImageCache{}, errors.New("DescribeImageCaches failed; check region, network and AccessKey permissions")
	}
	if res == nil || res.Body == nil {
		return ImageCache{}, errors.New("DescribeImageCaches returned no body")
	}
	var cache ImageCache
	for _, row := range res.Body.ImageCaches {
		if row == nil || row.ImageCacheId == nil || *row.ImageCacheId != id {
			continue
		}
		if cache.ID != "" {
			return ImageCache{}, errors.New("DescribeImageCaches returned duplicate cache IDs")
		}
		cache = ImageCache{ID: id, Region: value(row.RegionId), Status: value(row.Status), ExpiresAt: value(row.ExpireDateTime)}
		for _, image := range row.Images {
			cache.Images = append(cache.Images, value(image))
		}
	}
	if cache.ID == "" {
		return ImageCache{}, ErrImageCacheNotFound
	}
	return cache, nil
}

func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
