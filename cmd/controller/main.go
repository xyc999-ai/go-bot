package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go-bot/extensions/catalog"
	"go-bot/frontend"
	"go-bot/internal/config"
	"go-bot/internal/controller"
)

func hostCommand(c config.Config, action, user string) error {
	if user == "" {
		return errors.New("specify the account with --user")
	}
	method, route := http.MethodPost, "/v1/host/"+action
	switch action {
	case "status":
		method, route = http.MethodGet, "/v1/host"
	case "delete":
		method, route = http.MethodDelete, "/v1/host/instance"
	case "start", "stop":
	default:
		return errors.New("unknown host action")
	}
	address, err := config.ControllerListenAddress(c.Controller.Listen)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(method, "http://"+address+route, nil)
	if err != nil {
		return err
	}
	request.Header.Set(controller.UserHeader, user)
	res, err := (&http.Client{Timeout: c.Runtime.StartTimeout}).Do(request)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("controller HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	fmt.Print(string(body))
	return nil
}

func run() error {
	args := os.Args[1:]
	command := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	if command == "web-available" {
		if frontend.Files() == nil {
			return errors.New("this Controller was built without the Web UI")
		}
		return nil
	}
	action := ""
	if command == "host" {
		if len(args) == 0 {
			return errors.New("usage: controller host status|start|stop|delete --user name")
		}
		action, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("controller", flag.ContinueOnError)
	path := flags.String("config", "", "optional tuning YAML")
	envFile := flags.String("env-file", "", "deployment environment file")
	user := flags.String("user", "", "account name for a Host command")
	pluginCatalog := flags.String("plugin-catalog", catalog.DefaultURL, "reviewed remote plugin catalog URL")
	webDevOrigin := flags.String("web-dev-origin", "", "optional loopback Vite development origin")
	printRuntime := flags.Bool("print-runtime", false, "print validated runtime and image")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := controller.ValidateWebDevOrigin(*webDevOrigin); err != nil {
		return err
	}
	c, err := config.Load(*path, *envFile)
	if err != nil {
		return err
	}
	if command == "host" {
		return hostCommand(c, action, *user)
	}
	if err = c.ValidateController(); err != nil {
		return err
	}
	if command == "check" {
		if *printRuntime {
			fmt.Printf("%s\n%s\n", c.Runtime.Provider, c.Runtime.Image)
		} else {
			fmt.Println("Configuration and required credentials: OK")
		}
		return nil
	}
	if command != "serve" && command != "doctor" {
		return errors.New("usage: controller serve|check|doctor|host")
	}
	driver, err := controller.Driver(c)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if command == "doctor" {
		check, done := context.WithTimeout(ctx, 30*time.Second)
		defer done()
		if err := controller.New(c, driver, nil).Doctor(check); err != nil {
			return err
		}
		fmt.Println("Runtime preflight: OK (no compute created)")
		return nil
	}
	address, err := config.ControllerListenAddress(c.Controller.Listen)
	if err != nil {
		return err
	}
	if controller.InContainer() {
		address = "0.0.0.0:8080"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	redisAddress := os.Getenv("REDIS_ADDR")
	if redisAddress == "" {
		redisAddress = "127.0.0.1:6379"
	}
	store, err := controller.OpenRedis(ctx, redisAddress)
	if err != nil {
		return fmt.Errorf("connect to Redis: %w", err)
	}
	defer store.Close()
	manager := controller.New(c, driver, store)
	manager.Catalog.URL = *pluginCatalog
	server := &http.Server{
		Handler:           manager.HandlerWithWeb(controller.WebOptions{Files: frontend.Files(), DevOrigin: *webDevOrigin}),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	fmt.Printf("Controller ready: http://%s\n", listener.Addr())
	select {
	case <-ctx.Done():
	case err = <-result:
	}
	shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if e := server.Shutdown(shutdown); e != nil {
		_ = server.Close()
		return e
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
