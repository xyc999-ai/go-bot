# HTTP API

Controller serves the API at `127.0.0.1:8080` by default. Account-scoped
requests use the account name in the `X-User-ID` header. Opening an account
by name starts or reconnects its Host. Direct Host requests use the internal
Host bearer token. See [OpenAPI](../api/openapi.yaml).

The browser test client uses this public Controller API. Custom web, mobile,
and desktop clients can use the same HTTP endpoints and SSE streams. Keep
access to Controller within a trusted network because account names identify
users without password authentication.

Serve a custom browser UI from the same origin as the Controller API, or use the loopback development origin described in the [frontend guide](../frontend/README.md). Remote clients can use an HTTPS gateway such as Tailscale Serve. [Controller configuration](configuration.md#runtime-storage-and-entry)

| Method/path | Purpose |
|---|---|
| GET /healthz | Process liveness |
| GET /readyz | Application readiness |
| GET /v1/capabilities | Current model protocol/search capabilities |
| POST /v1/sessions | Create, returning sessionId |
| GET /v1/sessions?limit=&cursor= | Page through unarchived top-level sessions with an optional derived title |
| GET /v1/sessions?latest=true | Return only the newest unarchived top-level session by creation time, or an empty list |
| GET /v1/sessions/{id} | Active session state |
| PUT /v1/sessions/{id}/title | Rename an ordinary chat with `{"title":"…"}`; returns the normalized title |
| DELETE /v1/sessions/{id} | Archive a top-level session and retain its data on disk |
| POST /v1/sessions/{id}/messages | JSON content or multipart content/files/images |
| GET /v1/sessions/{id}/events?after=&limit= | Durable events and nextCursor for active or cold sessions |
| GET /v1/sessions/{id}/transcript?before=&limit= | Web transcript, paged by whole turns for active and cold sessions |
| GET /v1/host/stream?cursors= | One account Host SSE per client, multiplexing all session chat events and task catalog changes |
| GET /v1/sessions/{id}/stream?view=chat | Optional single-session chat projection for specialized clients; bundled clients use the Host stream |
| GET /v1/sessions/{id}/stream | Native SSE, with `Last-Event-ID` or `after` for both views |
| POST /v1/sessions/{id}/queue/{messageId}/steer | Move one queued user message into the current turn's next step |
| DELETE /v1/sessions/{id}/queue/{messageId} | Remove one queued user message |
| POST /v1/sessions/{id}/turns/{turnId}/cancel | Cancel queued/running turn |
| POST /v1/sessions/{id}/commands/compact | Request compaction |
| GET /v1/sessions/{id}/files?path=&offset= | Workspace directory listing |
| GET /v1/sessions/{id}/files/content?path= | Download a regular file using a workspace-relative path or an absolute Host path within the session workspace |
| GET /v1/sessions/{id}/attachments/{attachmentId}?name= | Download a verified attachment referenced by that Session. The optional name disambiguates display aliases |
| GET /v1/sessions/{id}/usage | Model usage |
| GET /v1/sessions/{id}/goal | Current goal |
| POST /v1/sessions/{id}/goal/resume | Resume by goalId/revision |
| GET/POST /v1/sessions/{id}/plan | Read or set active plan mode |
| GET/POST /v1/sessions/{id}/permissions | Read or set the current chat permission with `{"mode":"workspace-write"}` |
| GET /v1/sessions/{id}/questions | Pending/continued questions |
| POST /v1/sessions/{id}/questions/{callId}/answer | Answer (answers array) |
| POST /v1/sessions/{id}/questions/{callId}/cancel | Cancel pending question |
| GET /v1/sessions/{id}/schedules/{scheduleId}/history?limit=&before= | Delivery receipts |
| POST /v1/accounts | Create an account by name. A duplicate name returns 409 |
| GET /v1/host | Runtime state for the selected account |
| GET /v1/host/files?path=&offset= | Read-only directory listing under the selected account's Host data root |
| GET /v1/host/schedules | Host task catalog with timing, destination, status, revision and execution activity |
| POST /v1/host/schedules | Create a task with a typed time rule and optional destination |
| GET /v1/host/schedules/list | Page and filter by activity |
| GET, PATCH /v1/host/schedules/{scheduleId} | Read one task or edit it with a PATCH action (`update`, `pause`, `resume` or `delete`) |
| POST /v1/host/schedules/{scheduleId}/run | Deliver now without advancing the planned occurrence |
| GET /v1/host/schedules/{scheduleId}/history | Cursor page of delivered instructions and target sessions |
| DELETE /v1/host/schedules/ended | Remove ended tasks and their histories |
| GET /v1/host/schedules/stream | Stream task catalog invalidation events |
| GET /v1/host/schedules/limits | Read the configured minimum interval for a fresh session on each run |
| GET /v1/hosts/{hostId} | Runtime state for a matching instance of the selected account |
| POST /v1/host/start | Start/reconnect Host and prepare its stable main chat; returns `{mainSessionId}` |
| POST /v1/host/plugins/prepare | Install missing selected plugins and apply enabled state; synchronous, independent of core startup |
| POST /v1/host/stop | Stop Docker or release ECI for the selected account |
| DELETE /v1/host/instance | Delete compute and retain the account and external storage |

For multipart messages, send the `content` form field first, followed by mixed
`images` / `files` parts in upload order. The admitted user message contains the
image/file blocks in that same order, followed by the text block. This is also
the order stored in the session log and passed to the model. Both clients show
user attachments above the text; assistant block order is preserved.

For `POST /v1/accounts`, send `{"userId":"alice"}`. Account creation returns
the name with HTTP 201. Model settings are available before the Host starts.

Account creation reserves one `mainSessionId` and selects OfficeCLI and agent-browser
for automatic installation. `POST /v1/host/start` accepts an optional
`{"welcomeLocale":"zh-CN"}` (`en` is the default), and returns HTTP 200 with
`{"mainSessionId":"<uuid>"}`. The main chat is created without a model call;
its initial system and assistant greeting are persisted exactly once.
Clients can open it immediately, then automatically call
`POST /v1/host/plugins/prepare` while keeping navigation and settings available.
Preparation reuses matching installed releases and never re-adds a removed default.

Bundled clients pin the main chat and open it at login. Explicit Web chat links
and in-app navigation still open the requested conversation. Session list rows
include `isMain: true` for the main chat; archiving it returns
`409 main_session_cannot_archive`. Ordinary chats require a valid default model
at creation. Sending checks the session's selection before accepting the message
or storing attachments. A main chat without a selection binds the account's
valid default on its first send; a previously selected model that becomes
unavailable is never silently replaced. Search and media credentials do not gate chat.


New chats start with `danger-full-access`. The other modes are
`workspace-write` and `read-only`. A selection persists in the Host's session
journal. A running turn, child, or managed Job can make a change return 409.
A Host that cannot establish the restricted Linux policy returns an error.

The chat SSE stream emits `permissions.state` with `mode` and `approval`.
Pending approvals appear in `questions.state` with an `approval` object showing
the tool, original tool call ID, reason, operation detail and optional target
mode. The pending item's top-level `callId` identifies the decision.
POST an answer like `{"answers":[{"id":"approval","selected":["Allow once"]}]}`
to allow the pending operation. Use `"Deny"` to reject it. An approval never
changes the saved mode. A Host stream provides an interactive audience for all
its sessions, including scheduled work in newly created or unopened chats.
Without any interactive answerer, an approval returns unavailable and the
action does not run. Pending approvals have no fixed timeout. Disconnecting
after a request was created does not cancel it; reconnecting restores the
pending state. Turn cancellation or Host shutdown ends the wait. The bundled
clients show approvals inside their owning chat, with pending indicators in
the chat list; they never present a global approval dialog.

The chat stream and transcript expose successful `present` calls as
`assistant/message` events with `delivery: true` and `file` blocks. A block
with `file.source: "presented"` carries `file.path`, the absolute Host path
of the file. URL-encode it as the `path` query parameter to that session's
`/files/content` endpoint. The Host serves the current file and checks that
the path stays within the session workspace. The chat renders the card as
soon as the event arrives. Attachments use the separate attachment endpoint.

| Model settings endpoint | Behavior |
| --- | --- |
| GET /v1/settings/models | Returns `defaultEndpointId` and endpoints keyed by ID, with `hasApiKey` |
| POST /v1/settings/models/endpoints | Accepts `name`, `baseUrl`, `apiKey`, and ordered `models` |
| PATCH /v1/settings/models/endpoints/{endpointId} | Updates supplied fields. Omitted Key is retained |
| DELETE /v1/settings/models/endpoints/{endpointId} | Removes the endpoint and clears it if it was the default |
| PUT /v1/settings/models/default | Accepts `{"endpointId":"…"}`. An empty ID clears the default |
| GET /v1/sessions/{id}/model | Returns `selection` and `status` |
| PUT /v1/sessions/{id}/model | Accepts `{"endpointId":"…","modelId":"…"}` |

The first configured endpoint becomes the account default. Its first model
is used when creating a session. A missing or invalid default produces
`409 model_default_required` before the Host creates a session. A session
selection applies at the next turn boundary. Settings requests wait for a running Host to apply saved changes. A response
of `503 settings_saved_host_apply_failed` means Redis saved the change and the
Host could not confirm it. Reopen the account to apply the saved settings or
submit the update again. Public model settings responses omit API keys.

The account endpoints `/v1/settings/web-search` and
`/v1/settings/image-generation` share a settings contract.

| Request | Behavior |
| --- | --- |
| `GET` | Returns the active selection in `provider`, the available providers, and per-provider `entries` with `hasApiKey` and an optional image model. |
| `PUT {"provider":"zhipu","apiKey":"…","activate":false}` | Saves the key without changing the selection. Provide `model` when first saving an image provider. Omit the key to retain it. |
| `PUT {"provider":"zhipu","activate":true}` | Selects a provider using its saved settings. An unconfigured provider cannot be selected explicitly. |
| `DELETE ?provider=zhipu` | Removes that key. If it was in use, the tool is unavailable until a key is added or another provider is selected. |

For existing API clients, omitting `activate` keeps the original save-and-select
behavior. The Controller stores the selected ID and credentials separately for
each capability and account. Only the selected credentials are sent to the
running Host. Public responses omit keys. A response of
`503 settings_saved_host_apply_failed` means the setting was saved but the
running Host did not confirm it. Retry or reopen the account.

## Plugins

The account Redis `plugins` field contains only per-package selections:

```json
{"packages":{"@go-bot/plugin-officecli":{"catalogId":"officecli","version":"0.1.0","enabled":false}}}
```

The Controller resolves reviewed sources while sending Host-private commands.
Go owns installation and the core Harness; Cordis owns plugin loading and
resource lifetimes. No Host Redis client, local plugins.json, public multi-instance
configuration or background retry is used.

| Method/path | Purpose |
| --- | --- |
| GET /assets/plugins/{pluginId}/{file} | Public static image declared by plugin.json; available before Host startup or plugin installation |
| GET /v1/settings/plugins | Reviewed catalog, user package selections, actual Host state |
| POST /v1/settings/plugins/packages | Install `{pluginId}`, including declared CLI dependencies; disabled initially |
| PUT /v1/settings/plugins/packages/{name} | Set `{enabled}` for one installed plugin |
| POST /v1/settings/plugins/packages/{name}/update | Explicit update or reinstall from the reviewed `{pluginId}` |
| DELETE /v1/settings/plugins/packages/{name} | Withdraw contributions and remove package; retain credentials, files and globally installed CLI dependencies |
| POST /v1/host/plugins/runtime/restart | Restart Cordis and apply saved selections |

The catalog includes the first-party agent-browser and OfficeCLI packages shipped
with the Host. Other reviewed sources may be fixed npm versions, pinned Git commits
or fixed HTTPS archives. Clients cannot submit source URLs, directories or arbitrary
business config. Catalog maintainers must retain selected versions for supported
releases; users explicitly update to a new version.

Installation checks metadata without executing business setup, saves user intent,
installs the wrapper and dependencies through pnpm, including native lifecycle
scripts allowed by the reviewed build policy. pnpmfile hooks remain disabled. Only success of all steps
completes installation. Activation only checks local readiness and registers
capabilities. A failed setup requires explicit reinstall; no automatic background
retry is scheduled. Enabled plugins are reloaded after a successful update.

The response has catalog, settings and status. status.packages describes real
package files; status.instances is diagnostic information from native Cordis
entries, not user-editable instances. A package file being present alone does
not certify successful dependency setup. A failure after saving returns
502 settings_saved_host_apply_failed with actual status. Plugin install methods
are synchronous requests and may take several minutes.

Package mutations serialize separately from account operations. Installation does
not block chat, model settings or read-only plugin views, and adding a new package
keeps existing Cordis contributions available during dependency preparation. Web
and mobile retain the pending request when navigating away from the plugin screen;
returning shows its progress or result without submitting it again. Other package
mutations remain disabled while that client's request is pending. This does not
turn installation into a detached server job: closing/reloading the browser or
terminating the app can still cancel its request.

### Developing a plugin

Use the [local SDK](../extensions/sdk/README.md) and [examples](../examples/plugins/README.md).
A compiled JavaScript package registers business capabilities through `ctx.host`:

```ts
export const inject = ['host']
export async function apply(ctx) {
  await ctx.host.tools.register({
    name: 'lookup', description: 'Look up information in an external service.',
    inputSchema: { type: 'object', properties: { query: { type: 'string' } }, required: ['query'] },
    async execute(args, call) {
      call.signal.throwIfAborted()
      return { content: [{ type: 'text', text: 'Service result' }], data: { found: true } }
    },
  })
}
```

All Go-facing SDK methods return Promises. Cordis owns dependencies, fibers and
cleanup. Registration becomes visible after Go confirms it, and native disposal
retracts it. Tools are named `<instanceId>__<toolName>` and execute through the same
Go stages as built-ins. Only their business callback crosses RPC; no tool-stage
middleware, `next`, global guard or Agent/Session proxy is exposed. Plugin results
contain ordered text blocks and optional JSON data. Existing Go tools handle
images and `present` file delivery; plugins cannot invent attachment/log facts.

The SDK also exposes Skills, appended prompts, credentials, authorization, MCP,
and active-tool approval/question requests. See [the full type contract](../extensions/sdk/index.d.ts).
Native `ctx.on` subscribes to the documented `host/*` state notifications; their
return values do not change execution. Local Cordis services/events keep their
native semantics. Plugins are trusted container code, not sandboxed subprocesses.

### Credentials and authorization

Plugin records are stored in `$DSH_HOME/.credentials.yaml`, keyed by instance
and the plugin's local credential key. API keys and OAuth grants share the Go
Store. A plugin can register an acquisition flow that prompts for a key, opens
an OAuth/device flow, exchanges tokens and commits a validated record. The Go
authorization service controls admission, cancellation, prompts and persistence.
The plugin validates the supplier protocol; successful persistence is not proof
that an arbitrary supplier token is valid.

The self-owned Web/Mobile plugin pages expose this management surface:

| Request | Purpose |
| --- | --- |
| GET /v1/host/extensions/authorization | Flow labels/methods, configured/in-flight state and the latest resumable attempt handle; no tokens or answers |
| POST /v1/host/extensions/authorization | Start `{key,method?}`; return the attempt and pending interaction |
| GET /v1/host/extensions/authorization/{id} | Current attempt state, notices and prompts |
| POST /v1/host/extensions/authorization/{id}/answer | Answer `{promptId,answer?,decline?}` |
| DELETE /v1/host/extensions/authorization/{id} | Cancel the attempt |
| DELETE /v1/host/extensions/authorization | Cancel `{key}` |
| POST /v1/host/extensions/authorization/disconnect | Disconnect the native account for `{key}`; available when `canDisconnect` is true |

Open the plugin market from the Web sidebar or the mobile Chat workspace menu.
Each installed plugin shows only its registered authorization methods. A single
method has a direct entry; multiple methods offer a choice. A plugin without
authentication has no authorization entry. Ordinary flows and declared MCP
OAuth connections appear within the owning plugin, not in a separate MCP page.

The list includes optional `accountLabel` and `canDisconnect`. For native CLI
flows, `configured` is queried through the plugin's `inspect` callback, not a
placeholder Go credential record. A successful `run` is confirmed with `inspect`.
Ordinary flows keep their credential commit requirement. Explicit disconnect is
separate from cancelling a pending attempt: it calls the integration's native
logout and confirms that it no longer reports an authorized account.

Closing a panel or disconnecting a client does not cancel an authorization.
The flow listing exposes transient `attemptId`/`attemptStatus`; another view can
retrieve that attempt and its still-pending prompts. The flow ends on completion,
failure, explicit cancellation, plugin disposal or Host shutdown. Attempts stay in Host memory, not
browser storage or Redis. `configured` means a credential record exists, not
that a third-party service is currently reachable.

For an OAuth authorization-code flow, `session.callback({state})` returns
`{url,wait()}`. The provider library supplies its generated state; Go allocates a
single-use callback for the current attempt. `await callback.wait()` returns the
query as `Record<string,string[]>` to the plugin only. The plugin must validate
the provider response/issuer, exchange the code using its PKCE/client settings,
and then call `session.commit(record)`. A callback receipt is not authorization.

The browser callback is
`GET /v1/plugins/authorization/callback/{userId}/{token}`. Controller forwards it
to that account's authenticated Host, which checks the active attempt, token
and expected state and rejects replay. The callback does not start a Host or
expose arbitrary plugin HTTP routes. Its public base comes from the verified
Controller origin (HTTPS or localhost HTTP). API-key and device-code flows use
normal prompts/notices without this callback. Callback query/code data never
enter frontend attempt views or Redis configuration.

Credential authorization is separate from tool execution approval. A tool can
await `ctx.host.approvals.request(call, request)` before its business side effect;
only `allowed` means it should proceed. `questions.ask` waits for a real answer
within the active call. Neither adds a short RPC timeout to the human wait.

## MCP

MCP Client is a built-in Go service. Plugins register connections with
`await ctx.host.mcp.register({name,connection,toolLoading?})`. `stdio` accepts a
command/args/env/cwd and `streamable-http` accepts a URL/headers. Transport fields
are mutually exclusive. No independent end-user MCP configuration page is needed.

The Go client resolves explicit `${credential:NAME}` references, connects,
initializes, discovers and executes tools directly. It also owns MCP protocol
OAuth: discovery, PKCE, token exchange/refresh and grant persistence in the same
credential Store. Its grant addresses are separate from business OAuth records.
Missing credentials/authorization and connection failures are distinct states.
Disposing the owning Cordis instance releases the Go connection and tools.

`register` returns a Registration after initial handling; it does not promise
`connected`. Plugins can await `list`/`inspect`/`restart`, start protocol OAuth with
`authorize`, cancel a specific attempt, or `signOut`. These are SDK operations,
not a second Node MCP client. A declared `toolLoading` defaults to `eager`; the
existing Go on-demand discovery mode remains available.

The project's low-level `/v1/host/mcp` diagnostics and explicit restart/probe/OAuth
operations remain for specialized clients. They do not create servers. The
browser OAuth callback is `GET /v1/mcp/oauth/callback/{userId}`; the running Host
validates state, PKCE and issuer and is never started by a callback. The callback
uses a verified HTTPS or loopback HTTP Controller origin. Ordinary HTTP Host/chat
access keeps its existing behavior. Full request shapes are in [OpenAPI](../api/openapi.yaml).

## Files and streams

The Host file listing starts at `/root/harness`. Pass a relative directory
path and use `nextOffset` to read the rest of a directory. The response includes
`rootPath`, `path`, `files` and `truncated`. Entries contain `name`, `path`,
`isDir` and `size` in bytes. The Controller provides the storage kind in
`X-Harness-Storage` and the Docker volume name or NAS subdirectory in
`X-Harness-Storage-Source` when applicable. The endpoint lists metadata and
works even when none of the account's sessions are active.

History and workspace file reads work for cold sessions. The chat stream can
stay open while a session is cold and follows it when the Host activates it.
Unknown and archived IDs return 404 for session operations. The events cursor
advances over filtered rows, so continue until `nextCursor` is null, even
when an intermediate page contains no public events. Durable inbox admission
returns messageId before a turn is assigned. Persisted event sequence numbers are
the reconnect cursor; live attempt chunks have a separate transient sequence.

Bundled Web and Mobile clients open one `GET /v1/host/stream` for the signed-in
workspace, independently of the selected chat or tab. Each SSE record uses
`event: host` and JSON `{sessionId?, type, data}`. Session records carry their
own `sessionId`; `sessions.changed`, `schedule/changed` and `extensions.changed`
invalidate their Host-level lists or settings. Session switches do not reconnect.
No separate task or plugin stream is needed.

The first transcript page includes `events`, `throughSeq`, `before`, `queue`,
`questions`, optional live `baseline`, and `permission`. Pass `before` for older
pages. On connection/reconnection, URL-encode `cursors` as a JSON map from each
cached session ID to its last processed durable sequence (or null for an empty
journal). At most 20 entries are accepted. Omitted cursors overlap the same
bounded recent 20-turn page used for initial history, covering messages that
arrive while a session is being discovered. Clients deduplicate by session ID
and sequence, and buffer events while fetching history. Unopened chats keep
only in-memory activity/pending summaries, not whole transcripts.

Titles are derived from `session/title` journal records, never separate title
files. Ordinary chats get a deterministic first-human-prompt fallback and one
asynchronous title request using their selected model. Failure keeps the fallback.
`PUT /v1/sessions/{id}/title` pins a user title and cancels automatic naming;
empty titles return `400 invalid_session_title`. Titles are terminal-safe,
normalized single lines limited to 80 UTF-8 bytes. The main chat retains
`Go Bot` and rejects renaming with `409 main_session_title_fixed`. Title changes
are projected as `{sessionId, type:"session", data:{seq,type:"session/title",data:{title,messageSeqs,source}}}`
on the same Host stream, including for unopened chats. They do not become chat
messages or model-context content. Auxiliary request details stay private.

The projected `session` events carry ordered user and assistant blocks, tool
outcomes and turn boundaries. `assistant-stream-baseline` and
`assistant-stream` cover provisional model output. `session.state` carries the
current `activeTurn`; `queue.state`, `permissions.state` and `questions.state`
synchronize controls. `session.cursor` advances over filtered private records
without revealing them. `session.unavailable` invalidates an unavailable cached
chat. Per-session durable sequences remain the recovery authority; no global
journal, Redis event queue or client receipt acknowledgement is introduced.

A successful message POST returns 202 with a `messageId`. Queue operations
use that ID and return 204 on success. A queue entry that is no longer
pending returns 404. An attempt to steer while there is no active turn
returns 409. The stream stays open after a turn ends.

Session execution is one turn at a time with a durable inbox for later inputs.
Question timeout can let the current turn continue; late answers are new user
events. Cancellation does not undo filesystem side effects. No message POST is
automatically replayed by controller after a connection failure.

Archiving returns 204, including repeated deletion of the same archived ID.
It hides the session after restart while retaining the log, workspace, and
shared attachments. A busy session or a pending Schedule target returns 409.

The bundled Web UI uses the Controller's own origin. Browser requests from
other origins are denied by default. For local Vite development, pass
`--web-dev-origin http://127.0.0.1:5173` to the Controller. CLI requests
without a browser origin still work. See the [frontend README](../frontend/README.md).

## Proactive assistance

`GET /v1/settings/proactive` reads the account preference and, when the Host is
running, its current status. It does not start a Host. `PUT` accepts exactly
`{"enabled": true}` or `{"enabled": false}`, saves the account's Redis Hash
field and synchronously applies it to a running Host. `503
settings_saved_host_apply_failed` means the preference was saved but not applied;
the same PUT can be retried. No run-now endpoint is provided.

The default is off. Enabling validates the main chat's explicit model selection,
or the account default when no selection exists, before saving the preference.
`409 model_not_configured` or `409 model_selection_unavailable` rejects enabling
without changing the saved preference or starting a Host. Disabling does not
require a model. Validation checks configuration, without a model API request.

The JSON response contains `enabled`, `phase`, optional `nextCheckAt`,
`nextCheckReason`, `lastRunStartedAt`, `lastCompletedAt`, and a safe `error` code.
All timestamps are Unix milliseconds. Phases are `waiting`, `running`,
`waiting-for-model`, `waiting-for-main`, `disabled`, `host-stopped`, `unavailable`,
or `error`. Missing next-check fields mean the first successful decision has not
been made, not an AI choice to stop checking. An explicit invalid main-chat
selection is not replaced silently. Internal background Session IDs and
credential values are not returned.

There is still one Host-wide SSE connection per client. Internal research does
not appear as a separate public chat. When a background decision calls for
contact, a durable `user/message` with source `agent-message` is admitted to the
main chat's next-turn Inbox and hidden by the public projection. The main
Agent's ordinary assistant reply then streams through the existing protocol.

## Model endpoint protocols

`POST /v1/settings/models/endpoints` and `PATCH /v1/settings/models/endpoints/{endpointId}`
accept `protocol: "responses" | "anthropic_messages"` alongside `name`, `baseUrl`,
`apiKey` and `models`. Omitted protocol defaults to Responses for a new endpoint
and stays unchanged on an existing endpoint. GET settings returns protocol and
`hasApiKey`, never the stored key. A session continues to select only
`{ "endpointId": "...", "modelId": "..." }` through its `/model` endpoint.
Base URLs include the API prefix, such as `/v1`, but exclude `/messages` or
`/responses`. See [model provider configuration](providers.md#language-models).
