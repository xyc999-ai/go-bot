package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go-bot/internal/config"
	"go-bot/internal/localstate"
	"go-bot/thirdparty/aliyun"
)

type cacheAPI interface {
	CreateImageCache(context.Context, aliyun.CreateImageCache) (string, error)
	DescribeImageCache(context.Context, string) (aliyun.ImageCache, error)
}

type operation struct {
	Token   string `json:"clientToken"`
	Image   string `json:"image"`
	CacheID string `json:"cacheId,omitempty"`
}

type result struct {
	CacheID string `json:"cacheId"`
	Image   string `json:"image"`
	Digest  string `json:"digest,omitempty"`
	Region  string `json:"region"`
}

func execute(ctx context.Context, o options, root, name string, api cacheAPI, interval time.Duration) (result, error) {
	req := o.Request
	identity, _ := json.Marshal([]any{o.Region, o.Cloud.AccessKeyID, req.Image, req.VSwitchID, req.SecurityGroupID, req.SizeGiB, req.RetentionDays, name})
	key := sha256.Sum256(identity)
	store, err := localstate.Open(filepath.Join(root, hex.EncodeToString(key[:16])))
	if err != nil {
		return result{}, err
	}
	defer store.Close()
	const filename = "operation.json"
	var op operation
	err = store.Read(filename, &op)
	if errors.Is(err, os.ErrNotExist) {
		op = operation{Token: uuid.NewString(), Image: req.Image}
		if err := store.Write(filename, op); err != nil {
			return result{}, err
		}
	} else if err != nil {
		return result{}, err
	}
	if _, err := uuid.Parse(op.Token); err != nil || op.Image != req.Image ||
		(op.CacheID != "" && !config.ValidImageCacheID(op.CacheID)) {
		return result{}, errors.New("invalid saved buildeci operation")
	}
	ctx, cancel := context.WithTimeout(ctx, o.Wait)
	defer cancel()
	if op.CacheID == "" {
		req.ClientToken = op.Token
		req.Name = "go-bot-" + op.Token[:8]
		id, err := api.CreateImageCache(ctx, req)
		if err != nil {
			return result{}, err
		}
		if !config.ValidImageCacheID(id) {
			return result{}, errors.New("CreateImageCache returned an invalid cache ID")
		}
		op.CacheID = id
		if err := store.Write(filename, op); err != nil {
			return result{}, fmt.Errorf("save cache %s: %w", id, err)
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		cache, err := api.DescribeImageCache(ctx, op.CacheID)
		if err == nil {
			switch cache.Status {
			case "Ready":
				image, err := cache.ReadyImage(op.CacheID, o.Region, time.Now())
				if err != nil {
					return result{}, err
				}
				if image != op.Image {
					return result{}, fmt.Errorf("image cache %s contains an unexpected image", op.CacheID)
				}
				out := result{CacheID: op.CacheID, Image: image, Region: o.Region}
				if _, digest, ok := strings.Cut(image, "@"); ok {
					out.Digest = digest
				}
				return out, nil
			case "Preparing", "Creating", "Updating":
			default:
				return result{}, fmt.Errorf("image cache %s failed to reach Ready; inspect the cache events in Alibaba Cloud", op.CacheID)
			}
		} else if !errors.Is(err, aliyun.ErrImageCacheNotFound) && ctx.Err() == nil {
			return result{}, fmt.Errorf("inspect image cache %s: %w", op.CacheID, err)
		}
		select {
		case <-ctx.Done():
			return result{}, fmt.Errorf("waiting for image cache %s: %w; rerun the same operation to resume", op.CacheID, ctx.Err())
		case <-ticker.C:
		}
	}
}
