// Command buildeci prepares a Ready Alibaba ECI image cache.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-bot/internal/config"
	"go-bot/thirdparty/aliyun"
)

func run(args []string) error {
	if len(args) == 0 || (args[0] != "cache" && args[0] != "check") {
		return errors.New("usage: buildeci cache|check [--env-file file] [--output json|cache-id]")
	}
	command := args[0]
	f := flag.NewFlagSet("buildeci "+command, flag.ContinueOnError)
	profile := f.String("env-file", "", "shared deployment profile (default .env)")
	tuning := f.String("config", "", "optional buildeci tuning YAML")
	output := f.String("output", "json", "output format: json or cache-id")
	state := f.String("state-dir", ".cache/buildeci", "local cache operation state")
	operation := f.String("operation", "default", "operation name; use a new name for a deliberate new cache")
	writeProfile := f.Bool("write-profile", false, "save the Ready cache ID in the deployment profile")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 || (*output != "json" && *output != "cache-id") || *operation == "" ||
		(*writeProfile && (*profile == "" || command == "check")) {
		return errors.New("invalid buildeci arguments")
	}
	o, err := readOptions(*profile, *tuning)
	if err != nil {
		return err
	}
	if command == "check" {
		fmt.Println("ECI image cache configuration: OK")
		return nil
	}
	client, err := aliyun.NewECI(o.Region, o.Cloud)
	if err != nil {
		return err
	}
	ctx, done := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer done()
	out, err := execute(ctx, o, *state, *operation, client, 3*time.Second)
	if err != nil {
		return err
	}
	if *writeProfile {
		if err := config.SaveECIImageCacheID(*profile, out.CacheID); err != nil {
			return fmt.Errorf("cache %s is Ready but could not be saved: %w", out.CacheID, err)
		}
	}
	if *output == "cache-id" {
		fmt.Println(out.CacheID)
		return nil
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
