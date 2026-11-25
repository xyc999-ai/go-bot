package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"go-bot/harness/content"
	"go-bot/harness/permission"
	"go-bot/harness/schedule"
	"go-bot/harness/session"
	"go-bot/internal/hostruntime"

	"github.com/google/uuid"
)

// NewNativeHostHTTP exposes one user Host listener. Activation creates or
// resumes a top-level Session inside the Host. Live Agent requests dispatch to
// that runtime; Schedule history reads the Host task table without activating
// a cold Session. Gateway authenticates and routes by user ID.
func NewNativeHostHTTP(host *hostruntime.NativeHost, options NativeWorkerHTTPOptions) http.Handler {
	const publicPrefix = "/v1/sessions/"
	type cached struct {
		runtime *hostruntime.NativeRuntime
		handler http.Handler
	}
	var mu sync.Mutex
	var mutationLocks [64]sync.Mutex
	var historyCache coldHistoryCache
	publicHandlers := make(map[string]cached)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/host/stream" {
			streamHostEvents(w, r, host)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/host/files" {
			serveHostFiles(w, r, options.HostFilesRoot)
			return
		}
		if r.URL.Path == hostSchedulePath || strings.HasPrefix(r.URL.Path, hostSchedulePath+"/") {
			if host != nil && host.Schedule != nil {
				manageHostSchedules(w, r, host.Schedule)
			} else if r.Method == http.MethodGet && r.URL.Path == hostSchedulePath {
				serveHostSchedules(w, r, options)
			} else {
				writeScheduleError(w, schedule.ErrUnavailable)
			}
			return
		}
		if r.Method == http.MethodGet && (r.URL.Path == "/internal/host/sessions" || r.URL.Path == "/v1/sessions") {
			if host.List == nil {
				http.Error(w, "Session listing unavailable", http.StatusServiceUnavailable)
				return
			}
			limit, err := sessionListPageParams(r.URL.Query().Get("limit"), r.URL.Query().Get("cursor"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var page sessionformat.SessionListPage
			if r.URL.Path == "/v1/sessions" && r.URL.Query().Get("latest") == "true" && host.LatestSession != nil {
				page, err = host.LatestSession(r.Context())
			} else {
				page, err = host.List(r.Context(), r.URL.Query().Get("cursor"), limit)
			}
			if err != nil {
				http.Error(w, "Session listing failed", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/internal/host/sessions" {
				_ = json.NewEncoder(w).Encode(page)
				return
			}
			listed, err := listedUserPage(page, host.ReadTitle)
			if err != nil {
				http.Error(w, "Session listing failed", http.StatusServiceUnavailable)
				return
			}
			for i := range listed.Sessions {
				listed.Sessions[i].IsMain = host.IsMainSession(listed.Sessions[i].SessionID)
				if listed.Sessions[i].IsMain {
					listed.Sessions[i].Title = "Go Bot"
				}
			}
			_ = json.NewEncoder(w).Encode(listed)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/internal/host/image" {
			r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
			var ref content.ImageAttachment
			if err := json.NewDecoder(r.Body).Decode(&ref); err != nil {
				http.Error(w, "invalid image reference", http.StatusBadRequest)
				return
			}
			data, err := options.Images.LoadImage(r.Context(), ref)
			if err != nil {
				http.Error(w, "image attachment unavailable", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", ref.MIMEType)
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(data)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/internal/host/activity" {
			activity, err := host.Activity()
			if err != nil {
				http.Error(w, "Host activity unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(activity)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/internal/host/sessions/main" {
			var input struct {
				SessionID string `json:"sessionId"`
				Locale    string `json:"locale"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil || uuid.Validate(input.SessionID) != nil || (input.Locale != "" && input.Locale != "en" && input.Locale != "zh-CN") {
				writeSessionJSON(w, 400, map[string]string{"error": "invalid_main_session"})
				return
			}
			if err := host.EnsureMainSession(r.Context(), input.SessionID, input.Locale); err != nil {
				writeSessionJSON(w, 503, map[string]string{"error": "main_session_unavailable"})
				return
			}
			writeSessionJSON(w, 200, map[string]string{"sessionId": input.SessionID})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/internal/host/sessions" {
			r.Body = http.MaxBytesReader(w, r.Body, 1024)
			var request struct {
				SessionID          string `json:"sessionId"`
				WorkspaceSessionID string `json:"workspaceSessionId,omitempty"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || uuid.Validate(request.SessionID) != nil {
				http.Error(w, "invalid Session ID", http.StatusBadRequest)
				return
			}
			if _, ok := host.Session(request.SessionID); !ok {
				// The same requested ID can be retried after an uncertain create.
				// Resume first so an already persisted journal is never replaced.
				_, err := host.Activate(r.Context(), request.SessionID, true)
				if errors.Is(err, os.ErrNotExist) {
					if request.WorkspaceSessionID == "" {
						_, err = host.Activate(r.Context(), request.SessionID, false)
					} else if uuid.Validate(request.WorkspaceSessionID) == nil && host.ReadSessionHeader != nil {
						header, readErr := host.ReadSessionHeader(r.Context(), request.WorkspaceSessionID)
						if readErr != nil {
							http.Error(w, "Workspace Session unavailable", http.StatusNotFound)
							return
						}
						_, err = host.ActivateInWorkspace(r.Context(), request.SessionID, schedule.TargetCreate{CWD: header.CWD})
					} else {
						http.Error(w, "invalid Workspace Session ID", http.StatusBadRequest)
						return
					}
				}
				if errors.Is(err, hostruntime.ErrHostSessionActive) {
					_, err = host.AwaitSession(r.Context(), request.SessionID)
				}
				if err != nil {
					http.Error(w, "Session creation failed", http.StatusServiceUnavailable)
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"sessionId": request.SessionID})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/internal/host/sessions/") {
			rest := strings.TrimPrefix(r.URL.Path, "/internal/host/sessions/")
			id, action, found := strings.Cut(rest, "/")
			if found && uuid.Validate(id) == nil {
				switch {
				case r.Method == http.MethodGet && action == "exists":
					if err := host.CheckScheduleTarget(r.Context(), id); errors.Is(err, hostruntime.ErrHostSessionArchived) {
						http.NotFound(w, r)
						return
					} else if err != nil {
						http.Error(w, "Session unavailable", http.StatusServiceUnavailable)
						return
					}
					if host.SessionLogInfo != nil {
						_, err := host.SessionLogInfo(id)
						if errors.Is(err, os.ErrNotExist) {
							http.NotFound(w, r)
							return
						}
						if err != nil {
							http.Error(w, "Session unavailable", http.StatusServiceUnavailable)
							return
						}
					} else if _, ok := host.Session(id); !ok {
						http.NotFound(w, r)
						return
					}
					w.WriteHeader(http.StatusNoContent)
					return
				case r.Method == http.MethodGet && action == "activity":
					runtime, ok := host.Session(id)
					if !ok {
						http.NotFound(w, r)
						return
					}
					activity, err := runtime.Activity()
					if err != nil {
						http.Error(w, "Session activity unavailable", http.StatusServiceUnavailable)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(activity)
					return
				case r.Method == http.MethodPost && action == "stop":
					err := host.StopSession(r.Context(), id)
					switch {
					case errors.Is(err, hostruntime.ErrHostSessionAbsent):
						http.NotFound(w, r)
					case err != nil:
						http.Error(w, "Session stop failed", http.StatusServiceUnavailable)
					default:
						w.WriteHeader(http.StatusNoContent)
					}
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sessions" {
			id := uuid.NewString()
			if _, err := host.Activate(r.Context(), id, false); err != nil {
				http.Error(w, "Session creation failed", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"sessionId": id})
			return
		}
		if !strings.HasPrefix(r.URL.Path, publicPrefix) {
			http.NotFound(w, r)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, publicPrefix)
		id, suffix, _ := strings.Cut(rest, "/")
		if host.Archived != nil && (len(id) != 36 || uuid.Validate(id) != nil) {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && id != "" {
			var bucket uint32
			for _, b := range []byte(id) {
				bucket = 31*bucket + uint32(b)
			}
			lock := &mutationLocks[bucket%uint32(len(mutationLocks))]
			lock.Lock()
			defer lock.Unlock()
		}
		if r.Method == http.MethodDelete && suffix == "" && rest == id && uuid.Validate(id) == nil {
			err := host.ArchiveSessionWithActivity(r.Context(), id, r.URL.Query().Get("stopActivity") == "true")
			switch {
			case err == nil:
				historyCache.invalidate(id)
				mu.Lock()
				delete(publicHandlers, id)
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			case errors.Is(err, os.ErrNotExist):
				http.NotFound(w, r)
			case errors.Is(err, hostruntime.ErrMainSessionArchive):
				writeSessionJSON(w, http.StatusConflict, map[string]string{"error": "main_session_cannot_archive"})
			case errors.Is(err, hostruntime.ErrHostSessionBusy):
				writeSessionJSON(w, http.StatusConflict, map[string]string{"error": "session_busy"})
			case errors.Is(err, schedule.ErrSessionHasSchedule):
				writeSessionJSON(w, http.StatusConflict, map[string]string{"error": "session_has_schedule"})
			default:
				writeSessionJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session_archive_failed"})
			}
			return
		}
		if err := host.CheckScheduleTarget(r.Context(), id); err != nil {
			if errors.Is(err, hostruntime.ErrHostSessionArchived) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "Session unavailable", http.StatusServiceUnavailable)
			}
			return
		}

		if attachmentID, ok := strings.CutPrefix(suffix, "attachments/"); ok && r.Method == http.MethodGet {
			downloadSessionAttachment(w, r, host, options, id, attachmentID)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(suffix, "schedules/") {
			parts := strings.Split(suffix, "/")
			if len(parts) != 3 || parts[0] != "schedules" || parts[1] == "" || parts[2] != "history" || uuid.Validate(id) != nil {
				http.NotFound(w, r)
				return
			}
			if host.Schedule == nil {
				http.Error(w, "Schedule unavailable", http.StatusServiceUnavailable)
				return
			}
			limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
			if err != nil || limit < 1 || limit > 100 {
				http.Error(w, "invalid Schedule history limit", http.StatusBadRequest)
				return
			}
			var before *string
			if values, present := r.URL.Query()["before"]; present {
				value := ""
				if len(values) > 0 {
					value = values[0]
				}
				before = &value
			}
			page, err := host.Schedule.HistoryFor(id, parts[1], limit, before)
			if err != nil {
				http.Error(w, "Schedule history unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if page.Code != "" {
				_ = json.NewEncoder(w).Encode(map[string]string{"id": page.ID, "code": page.Code})
			} else {
				_ = json.NewEncoder(w).Encode(page)
			}
			return
		}
		if r.Method == http.MethodGet && suffix == "files/content" {
			if host.WorkspaceForSession == nil || uuid.Validate(id) != nil {
				http.NotFound(w, r)
				return
			}
			workspace := host.WorkspaceForSession(id)
			if workspace == "" {
				http.NotFound(w, r)
				return
			}
			serveWorkspaceFile(w, r, workspace)
			return
		}
		if r.Method == http.MethodGet && suffix == "stream" && r.URL.Query().Get("view") == "chat" && uuid.Validate(id) == nil {
			defer host.AttachChatStream(id)()
		}
		runtime, ok := host.Session(id)

		if r.Method == http.MethodGet && suffix == "transcript" && (ok || host.ReadHistory != nil) {
			serveWindowTranscript(w, r, host, id, runtime)
			return
		}
		if r.Method == http.MethodGet && !ok && host.ReadHistory != nil && (suffix == "permissions" || suffix == "questions") {
			value, err := host.SessionHistoryWindow(r.Context(), id, sessionformat.HistoryRequest{Kind: sessionformat.HistoryState})
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			if err != nil {
				writeSessionJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "transcript_unavailable"})
				return
			}
			if suffix == "permissions" {
				writeSessionJSON(w, http.StatusOK, historyPermission(value))
			} else {
				questions, err := historyQuestions(value, true)
				if err != nil {
					writeSessionJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent_questions_unavailable"})
					return
				}
				writeSessionJSON(w, http.StatusOK, map[string]any{"questions": questions})
			}
			return
		}

		if r.Method == http.MethodGet && !ok && host.ReadHistory != nil && suffix == "stream" && r.URL.Query().Get("view") == "chat" {
			runtime, r = waitForIndexedChatRuntime(w, r, host, id)
			if runtime == nil {
				return
			}
			ok = true
		}
		coldChatRead := suffix == "questions" || suffix == "permissions" ||
			(suffix == "stream" && r.URL.Query().Get("view") == "chat")
		if r.Method == http.MethodGet && (suffix == "transcript" || !ok && coldChatRead) {
			var events []sessionformat.Event
			var err error
			if ok {
				events, err = runtime.DurableSessionEvents()
			} else if host.ReadSession != nil && uuid.Validate(id) == nil {
				var artifact sessionformat.PhysicalArtifact
				artifact, err = historyCache.load(r.Context(), host, id)
				events = artifact.Events
			} else {
				err = os.ErrNotExist
			}
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			if err != nil {
				writeSessionJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "transcript_unavailable"})
				return
			}
			switch suffix {
			case "transcript":
				serveChatTranscript(w, r, events, runtime)
				return
			case "permissions":
				writeSessionJSON(w, http.StatusOK, permission.Read(events))
				return
			case "questions":
				writeSessionJSON(w, http.StatusOK, map[string]any{"questions": coldChatQuestions(events)})
				return
			case "stream":
				runtime, r = waitForChatRuntime(w, r, host, id, events)
				if runtime == nil {
					return
				}
				ok = true
			}
		}
		if r.Method == http.MethodPut && suffix == "title" && uuid.Validate(id) == nil {
			if host.IsMainSession(id) {
				writeSessionJSON(w, 409, map[string]string{"error": "main_session_title_fixed"})
				return
			}
			var input struct {
				Title string `json:"title"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil || sessionformat.NormalizeTitle(input.Title, 80) == "" {
				writeSessionJSON(w, 400, map[string]string{"error": "invalid_session_title"})
				return
			}
			if !ok {
				var err error
				runtime, err = host.Activate(r.Context(), id, true)
				if errors.Is(err, hostruntime.ErrHostSessionActive) {
					runtime, err = host.AwaitSession(r.Context(), id)
				}
				if errors.Is(err, os.ErrNotExist) || errors.Is(err, hostruntime.ErrHostSessionArchived) {
					http.NotFound(w, r)
					return
				}
				if err != nil {
					writeSessionJSON(w, 503, map[string]string{"error": "session_title_unavailable"})
					return
				}
			}
			title, err := runtime.RenameTitle(r.Context(), input.Title)
			if err != nil {
				writeSessionJSON(w, 503, map[string]string{"error": "session_title_unavailable"})
				return
			}
			historyCache.invalidate(id)
			writeSessionJSON(w, 200, map[string]string{"title": title})
			return
		}
		if !ok && r.Method == http.MethodGet && suffix == "events" && uuid.Validate(id) == nil {
			serveColdSessionHistory(w, r, host, &historyCache, id)
			return
		}
		if !ok && r.Method == http.MethodPost && uuid.Validate(id) == nil &&
			(suffix == "messages" || suffix == "permissions" || (strings.HasPrefix(suffix, "questions/") && strings.HasSuffix(suffix, "/answer"))) {
			historyCache.invalidate(id)
			var err error
			runtime, err = host.Activate(r.Context(), id, true)
			if errors.Is(err, hostruntime.ErrHostSessionActive) {
				runtime, err = host.AwaitSession(r.Context(), id)
			}
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, hostruntime.ErrHostSessionArchived) {
				http.NotFound(w, r)
				return
			}
			if err != nil {
				http.Error(w, "Session recovery failed", http.StatusServiceUnavailable)
				return
			}
			ok = true
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		entry, found := publicHandlers[id]
		if !found || entry.runtime != runtime {
			sessionOptions := options

			entry = cached{runtime: runtime, handler: NewNativeWorkerHTTP(runtime, runtime.Identity, sessionOptions)}
			publicHandlers[id] = entry
		}
		mu.Unlock()
		entry.handler.ServeHTTP(w, r)
	})
}
