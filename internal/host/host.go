// Package host composes a single personal computer with many durable v4 sessions.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"go-bot/harness/agent"
	"go-bot/harness/assembly"
	"go-bot/harness/attachments"
	"go-bot/harness/interaction"
	"go-bot/harness/memory"
	"go-bot/harness/schedule"
	"go-bot/harness/session"
	"go-bot/harness/sessionsearch"
	"go-bot/harness/skills"
	"go-bot/harness/tools"
	"go-bot/internal/authorization"
	"go-bot/internal/config"
	"go-bot/internal/credentials"
	"go-bot/internal/durablefs"
	"go-bot/internal/executor"
	"go-bot/internal/hostruntime"
	"go-bot/internal/httpapi"
	"go-bot/internal/mcpclient"
	"go-bot/internal/modelsettings"
	"go-bot/internal/plugins"
	"go-bot/internal/proactive"
	"go-bot/internal/websearchsettings"
	mcpconfig "go-bot/mcp"
	"go-bot/plugin"
	imageproviders "go-bot/providers/image"
	"go-bot/providers/model/mainmodel"
	webproviders "go-bot/providers/web"
	builtin "go-bot/providers/web/builtin"
	"go-bot/web"
)

type Ready struct {
	RunID      string `json:"runId"`
	APIVersion string `json:"apiVersion"`
}

// Run starts a Controller-managed Host after validating its startup configuration.
func Run(ctx context.Context, c config.Config) error {
	if err := c.ValidateHost(); err != nil {
		return err
	}
	return run(ctx, c, func(options web.Options) (web.SearchProvider, error) {
		return webproviders.New(webproviders.Default, options)
	}, builtin.Fetcher{})
}

func sessionLogRoot(dataRoot string) string {
	return filepath.Join(dataRoot, skills.UserDSHHomeDir, "sessions")
}

func run(ctx context.Context, c config.Config, newSearchProvider web.Factory, fetch tools.WebFetcher) error {
	root := config.DataRoot
	if err := durablefs.EnsureDirectory(root); err != nil {
		return err
	}
	mode := "container"
	if c.Host.DataMounted {
		mode = "mounted"
	}
	builder, err := executor.NewVerifiedBuilder(mode)
	if err != nil {
		return err
	}
	search := &hostWebSearch{defaultFactory: newSearchProvider}
	imageGeneration := &hostImageGeneration{}
	dshHome := filepath.Join(root, skills.UserDSHHomeDir)
	if err := seedSkillCreator(dshHome, skills.UserAgentsHome); err != nil {
		return fmt.Errorf("initialize skill-creator: %w", err)
	}
	images := attachments.Store{API: attachments.LocalObjectAPI{Root: dshHome}}
	files := attachments.FileStore{Root: dshHome}
	requestImages := &attachments.RequestImages{Store: images, MaxWidth: 2048, MaxHeight: 2048, MaxBytes: 4 << 20}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// The agent deliberately uses the deployment's configured HTTP(S) proxy.
	defer transport.CloseIdleConnections()
	model := mainmodel.Model{Images: requestImages, ImageAccessPath: images.ImageHostPath, Transport: transport}
	models := &hostModels{base: model, snapshot: modelsettings.Snapshot{Revision: -1}}
	if err := models.loadReservations(filepath.Join(dshHome, "storages", "scheduled-models")); err != nil {
		return err
	}
	builder.WebFetcher = fetch
	builder.ImageAttachments = images
	builder.ImageInputEnabled = true
	builder.WebSearcher = search
	// Image generation is currently supplied by CLI plugins. Keep the native
	// implementation and stored settings available for future composition.
	const builtinImageGenerationEnabled = false
	if builtinImageGenerationEnabled {
		builder.ImageGenerator = imageGeneration
	}
	native := &hostruntime.NativeHost{UserID: c.Host.AccountID, Mode: "host", StartedAt: time.Now().UTC(), ReserveScheduleModel: models.reserve}
	var backgroundRef atomic.Pointer[backgroundService]
	noteBackgroundChange := func() {
		if background := backgroundRef.Load(); background != nil {
			if err := background.changed(); err != nil {
				slog.Warn("record proactive information change", "error", err)
			}
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	credentialStore, err := credentials.Open(filepath.Join(dshHome, credentials.Filename), credentials.Options{Watch: true})
	if err != nil {
		return err
	}
	defer credentialStore.Close()
	commonAuthorization := authorization.New(credentialStore)
	capabilitiesChanged := func() { native.WakeSessions(); noteBackgroundChange() }
	mcpManager := mcpclient.NewManager(runCtx, mcpclient.ManagerOptions{Authorization: commonAuthorization, Credentials: credentialStore, Images: images, CWD: root, Changed: capabilitiesChanged})
	defer mcpManager.Close()
	extensions, err := plugins.NewManager(runCtx, plugins.ManagerConfig{
		Directory: filepath.Join(dshHome, "plugins"),
		Runtime: plugins.RuntimeConfig{UserID: c.Host.AccountID, HostID: c.Host.RunID, Authorization: commonAuthorization, Credentials: credentialStore, Main: "/usr/share/go-bot/cordis/src/main.mjs", Home: "/root", DSHHome: dshHome, MCP: mcpManager, Skills: builder.Skills, Workspace: root,
			ProactiveChanged: func() error {
				if background := backgroundRef.Load(); background != nil {
					return background.changed()
				}
				return errors.New("proactive service is not ready")
			}},
		PackageMain: "/usr/share/go-bot/cordis/src/package-main.mjs", Changed: capabilitiesChanged,
	})
	if err != nil {
		return err
	}
	defer extensions.Close()
	presets := newHostPresetRegistry(extensions.PresetCatalog, mcpManager.PresetCatalog)
	builder.Skills.Providers = extensions.Skills
	native.PluginsChanges = extensions.Changes
	native.MCPChanges = mcpManager.Changes
	scheduleStore := schedule.FileStore{Path: filepath.Join(dshHome, "storages", "schedule.json")}
	native.Schedule, err = schedule.NewHostService(scheduleStore, native.PrepareSchedule, native.DeliverSchedule)
	if err != nil {
		return err
	}
	native.Schedule.SetDestinationCreator(native.EnsureScheduleDestination)
	claims := &sessionformat.ProcessClaims{}
	atWorkspace := func(cwd string) sessionformat.FileStore {
		return sessionformat.FileStore{Root: sessionLogRoot(root), ProjectCWD: cwd, Claim: claims.Acquire}
	}
	resolveStore := func(ctx context.Context, sid string) (sessionformat.FileStore, error) {
		header, err := findHostSession(ctx, root, sid)
		return atWorkspace(header.CWD), err
	}
	native.PrepareMain = func(ctx context.Context, sid, locale string) error {
		if _, err := findHostSession(ctx, root, sid); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		preset, err := presets.Resolve(ctx, "")
		if err != nil {
			return err
		}
		return ensureMainJournal(ctx, root, sid, locale, claims.Acquire, preset.ID())
	}
	native.WorkspaceForSession = func(sid string) string {
		header, _ := findHostSession(context.Background(), root, sid)
		return header.CWD
	}
	native.Archived = func(sid string) (bool, error) {
		store, err := resolveStore(context.Background(), sid)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return archivedChat(store, sid)
	}
	native.SaveArchive = func(ctx context.Context, sid string) error {
		store, err := resolveStore(ctx, sid)
		if err != nil {
			return err
		}
		return archiveChat(ctx, store, sid)
	}
	native.ReadTitle = func(sid string) string {
		if native.IsMainSession(sid) {
			return "Go Bot"
		}
		store, err := resolveStore(context.Background(), sid)
		if err != nil {
			return ""
		}
		return chatTitle(store, sid)
	}
	native.SaveTitle = func(sid, title string) error {
		store, err := resolveStore(context.Background(), sid)
		if err != nil {
			return err
		}
		return saveChatTitle(store, sid, title)
	}
	native.Schedule.SetSessionTargetCheck(native.ValidateScheduleDestination)
	native.Schedule.ConfigureDelivery(schedule.Limits{
		HistoryDays: c.Schedule.DeliveryHistoryDays, HistoryRecords: c.Schedule.DeliveryHistoryRecords,
		NewSessionIntervalSeconds: c.Schedule.NewSessionIntervalSeconds,
	}, native.ObserveScheduleExecution, native.NotifyScheduleChange)
	native.ReadSessionHeader = func(ctx context.Context, sid string) (sessionformat.Header, error) {
		return findHostSession(ctx, root, sid)
	}
	native.SessionLogInfo = func(sid string) (os.FileInfo, error) {
		store, err := resolveStore(context.Background(), sid)
		if err != nil {
			return nil, err
		}
		path, err := store.Path(sid)
		if err != nil {
			return nil, err
		}
		return os.Stat(path)
	}
	native.ReadSession = func(ctx context.Context, sid string) (sessionformat.PhysicalArtifact, error) {
		store, err := resolveStore(ctx, sid)
		if err != nil {
			return sessionformat.PhysicalArtifact{}, err
		}
		return store.ReadArtifact(ctx, sid)
	}
	indexedHistory := &sessionformat.FileHistoryReader{Configure: func(s *sessionformat.LiveSession) error {
		_, err := interaction.RegisterQuestionProjection(s)
		return err
	}}
	native.ReadHistory = func(ctx context.Context, sid string, request sessionformat.HistoryRequest) (sessionformat.HistoryWindow, error) {
		store, err := resolveStore(ctx, sid)
		if err != nil {
			return sessionformat.HistoryWindow{}, err
		}
		return indexedHistory.Read(ctx, store, sid, request)
	}

	native.List = func(ctx context.Context, cursor string, limit int) (sessionformat.SessionListPage, error) {
		return listHostSessions(ctx, root, cursor, limit)
	}
	native.LatestSession = func(ctx context.Context) (sessionformat.SessionListPage, error) {
		return latestHostSession(ctx, root)
	}
	accountMemory := memory.NewStore(filepath.Join(dshHome, "memories"))
	history, err := sessionsearch.Open(ctx, "/var/lib/go-bot/session-search.sqlite", c.Host.AccountID, sessionsearch.Source{
		List: native.List, Snapshot: historySnapshot(native), Title: native.ReadTitle,
	})
	if err != nil {
		return fmt.Errorf("open Host session search index: %w", err)
	}
	defer history.Close()
	if err := history.Sync(ctx); err != nil {
		return fmt.Errorf("sync Host session search index: %w", err)
	}
	buildRuntime := func(sid, workspace, preset string) (*hostruntime.NativeRuntime, error) {
		return &hostruntime.NativeRuntime{
			Factory: assembly.NativeFactory{
				Presets: presets,
				Storage: executor.NativeFileStorage{Store: atWorkspace(workspace), SessionID: sid},
				Builder: builder, Model: model, ResolveTurnModel: func(context.Context) (agent.TurnModel, error) {
					if native.IsMainSession(sid) {
						return models.backgroundRoute(sid)
					}
					return models.route(sid)
				}, ResolveSelectedModel: models.selected,
				ToolSource: extensions.Tools, PromptSource: extensions.AssemblePrompt, ToolInvocationSource: extensions.ToolInvocations,
				ExtensionScope: func() assembly.NativeExtensionScope { return mcpManager.NewScope() },
				Provider:       "unconfigured", ModelID: "unconfigured", SystemPrompt: assembly.DefaultCoreSystemPrompt,
				Limits: agent.Limits{MaxSteps: 128, MaxToolCalls: 256, MaxModelCalls: 160, MaxParallelToolCalls: 4,
					ContextWindowTokens: config.ModelContextWindow, ReservedOutputTokens: 16 * 1024, ContextHeadroomTokens: 8 * 1024},
				Schedule: native.Schedule, FileAttachments: files, Memory: accountMemory, SessionSearch: history,
				MemoryChanged: func(ctx context.Context) {
					if source, ok := agent.TurnSourceFromContext(ctx); !ok || source.Kind != "agent-message" {
						noteBackgroundChange()
					}
				},
			},
			Identity:            assembly.NativeRuntimeIdentity{UserID: c.Host.AccountID, SessionID: sid, Workspace: workspace, AgentPreset: preset},
			HostOwnsSchedule:    true,
			UserMessageAccepted: noteBackgroundChange,
			TurnCommitted: func(events []sessionformat.Event) {
				if background := backgroundRef.Load(); background != nil {
					background.observeTurn(events)
				}
			},
		}, nil
	}
	background, err := newBackgroundService(root, native, models, history, atWorkspace, buildRuntime)
	if err != nil {
		return fmt.Errorf("prepare proactive service: %w", err)
	}
	backgroundRef.Store(background)
	background.capabilityContext = func() ([]agent.RuntimeContextSection, error) {
		records, err := credentialStore.ListRecords()
		if err != nil {
			return nil, err
		}
		flows := commonAuthorization.List()
		data, _ := json.Marshal(map[string]any{"credentialEntries": records[:min(len(records), 64)], "authorizationMethods": flows[:min(len(flows), 64)], "truncated": len(records) > 64 || len(flows) > 64})
		return []agent.RuntimeContextSection{{Name: "proactive:connections", Text: "Known authorization entry points and credential-presence metadata only. These do not prove that an external service is usable; native CLI grants can live outside this store. Actual available tools/skills and their outcomes remain authoritative. No secret values are included: " + string(data)}}, nil
	}
	unsubscribeCredentials := credentialStore.Subscribe(func(credentials.Update) { noteBackgroundChange() })
	unsubscribeAuthorization := commonAuthorization.Subscribe(func(string, string) { noteBackgroundChange() })
	defer unsubscribeCredentials()
	defer unsubscribeAuthorization()
	native.MainReady = background.mainReady
	backgroundDone := make(chan error, 1)
	go func() { backgroundDone <- background.service.Run(runCtx) }()
	go func() {
		for {
			changed, closed := native.Schedule.Changes()
			select {
			case <-runCtx.Done():
				return
			case <-closed:
				return
			case <-changed:
				background.version.Add(1)
			}
		}
	}()
	defer func() {
		cancel()
		if err := <-backgroundDone; err != nil {
			slog.Error("proactive service stopped", "error", err)
		}
		if background.live != nil {
			_ = background.live.Close()
		}
	}()
	native.BuildInWorkspace = func(sid string, create schedule.TargetCreate) (*hostruntime.NativeRuntime, error) {
		if create.AgentPreset == proactive.PresetID {
			return nil, errors.New("internal Agent composition cannot be selected by a public Session")
		}
		workspace := create.CWD
		if workspace == "" {
			workspace = root
		}
		workspace = filepath.Clean(workspace)
		relative, err := filepath.Rel(root, workspace)
		if err != nil || !filepath.IsLocal(relative) {
			return nil, schedule.ErrInvalid
		}
		if err := durablefs.EnsureDirectory(workspace); err != nil {
			return nil, err
		}
		return buildRuntime(sid, workspace, create.AgentPreset)
	}
	native.Build = func(sid string, resume bool) (*hostruntime.NativeRuntime, error) {
		if resume {
			header, err := findHostSession(context.Background(), root, sid)
			if err != nil {
				return nil, err
			}
			if header.AgentPreset == proactive.PresetID {
				return nil, errors.New("internal Agent composition cannot be selected by a public Session")
			}
			return buildRuntime(sid, header.CWD, header.AgentPreset)
		}
		return native.BuildInWorkspace(sid, schedule.TargetCreate{CWD: defaultSessionWorkspace(root, sid)})
	}
	if err = native.Schedule.Start(runCtx); err != nil {
		return err
	}
	base := httpapi.NewNativeHostHTTP(native, httpapi.NativeWorkerHTTPOptions{
		HostFilesRoot: root,
		ReadSchedules: scheduleStore.Load,
		Images:        images, Files: files, ImageInputEnabled: true,
	})
	var draining atomic.Bool
	ready := Ready{RunID: c.Host.RunID, APIVersion: "v1"}
	wakeForModelChange := func() {
		native.WakeSessions()
		native.Schedule.NotifyModelChange()
		if err := background.service.ModelChanged(); err != nil {
			slog.Warn("notify proactive model change", "error", err)
		}
	}
	pluginHTTP := extensions.Handler()
	mcpHTTP := mcpManager.Handler()
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case proactivePath:
			background.handler(w, r)
		case websearchsettings.InternalPath:
			search.handle(w, r)
		case imageproviders.InternalPath:
			imageGeneration.handle(w, r)
		case modelsettings.InternalPath:
			models.handle(w, r, wakeForModelChange)
		case "/readyz":
			if draining.Load() {
				http.Error(w, "draining", 503)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ready)
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(map[string]any{"model": models.defaultModel(), "protocol": models.defaultProtocol(), "protocols": []string{modelsettings.Responses, modelsettings.AnthropicMessages}, "search": search.configured(), "apiVersion": "v1"})
		case "/internal/host/shutdown":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", 405)
				return
			}
			draining.Store(true)
			w.WriteHeader(202)
			cancel()
		default:
			if r.URL.Path == mcpconfig.InternalPath || strings.HasPrefix(r.URL.Path, mcpconfig.InternalPath+"/") {
				mcpHTTP.ServeHTTP(w, r)
				return
			}
			if r.URL.Path == plugin.InternalPath || strings.HasPrefix(r.URL.Path, plugin.InternalPath+"/") {
				pluginHTTP.ServeHTTP(w, r)
				return
			}
			if id, ok := strings.CutPrefix(r.URL.Path, modelsettings.ScheduledSelectionPrefix); ok {
				models.handleScheduledSelection(w, r, id, wakeForModelChange)
				return
			}
			base.ServeHTTP(w, r)
		}
	})
	server := &http.Server{Handler: httpapi.Authenticate(c.Auth.HostToken, mcpManager.ControllerOAuth(mux)), BaseContext: func(net.Listener) context.Context { return runCtx }, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	listener, err := net.Listen("tcp", config.HostListen)
	if err != nil {
		native.Schedule.Close()
		return err
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	fmt.Printf("Host ready: %s (run=%s)\n", listener.Addr(), ready.RunID)
	var runErr error
	select {
	case <-runCtx.Done():
	case runErr = <-serveErr:
	}
	draining.Store(true)
	cancel()
	shutdown, done := context.WithTimeout(context.Background(), 15*time.Second)
	defer done()
	if e := server.Shutdown(shutdown); e != nil {
		_ = server.Close()
		runErr = errors.Join(runErr, e)
	}
	if errors.Is(runErr, http.ErrServerClosed) {
		runErr = nil
	}
	return errors.Join(runErr, native.Close(shutdown))
}
