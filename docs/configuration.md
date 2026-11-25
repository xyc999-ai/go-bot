# Configuration

The [Quickstart installer](../README.md#quickstart) downloads the same Compose,
`.env.example` and `configs/default.yaml` used in the repository, creates only
`.env`, and pulls the images. Start with `docker compose up -d`.

In a source checkout, run `./scripts/start.sh --init-env` to create `.env` for Docker and
`.env.eci` for ECI from [.env.example](../.env.example). Use
`--docker-env-file PATH` and `--eci-env-file PATH` to customize the output
paths. Initialization leaves existing files in place.

Startup defaults to `.env`, and `buildeci.sh` defaults to `.env.eci`. Both
accept `--env-file PATH` and require the selected file to exist. The launcher
applies explicitly set shared deployment variables, validates the runtime
configuration, and forwards the overrides only to that process. Neither loading
nor validating configuration writes overrides back to the profile.
Compose mounts the profile into Controller. Controller connects to Redis over
the internal Compose network at `redis:6379`. Compose publishes Controller on
port 8080 and Redis on port 6379 on the Docker host's network interfaces.
The source launcher runs configuration tools in the existing local Controller
image. It never pulls or builds images and is independent of install.sh.
Controller also validates configuration on `serve`, so starting Compose directly
does not skip checks. `controller check` validates
the profile offline. `controller doctor` also performs a read-only runtime
preflight. In ECI mode, `check` validates the cache ID format and `doctor`
queries Alibaba Cloud for a usable cache. `buildeci` validates its own
image-cache inputs before an ECI cache ID exists.

Use one literal `KEY=value` per line. Blank lines and full-line comments are
allowed. Values can contain spaces, `#`, `$` and `=`. Private profiles use
mode 0600. Git and Docker exclude `.env`, `.env.*`, `*.env` and `*.env.*`
files, except for the root `.env.example` template. For other names, keep
private profiles outside the checkout or under the ignored `.local/` directory.
Create the directory first if passing a new path to `--init-env`.

Compose mounts only the selected env file read-only at `/config/deployment.env`,
and mounts `configs/default.yaml` read-only at `/runtime.yaml`. The repository or
installation directory is not mounted into Controller. Missing files fail startup.
Controller reads the original profile directly; Compose does not pass the entire
profile as container environment variables. `REDIS_ADDR` and `CONTROLLER_CONTAINER`
are fixed container wiring. Compose's automatic `.env` interpolation is not used
to reconstruct application secrets.

Configuration precedence is built-in defaults, tuning/profile files, then explicit
process deployment variables. Overrides affect only the current process. They do
not rewrite the profile, change its permissions or clear saved values. An explicit
empty override clears the effective value in memory. Account model and search
settings remain managed through the UI.

For ordinary Compose deployments, edit `.env` and recreate Controller to apply
changes. Use a Compose override to explicitly supply container environment values.
The repository `start.sh` forwards explicitly set shell values for that launch,
using a temporary Compose override that contains variable names only and is deleted
on exit. The installer prepares files and images; it does not persist shell
configuration. `buildeci` saving a successfully created `ECI_IMAGE_CACHE_ID` remains
an explicit preparation operation, separate from startup.

## Runtime, storage and entry

| Variable | Meaning | Default or requirement |
|---|---|---|
| `HARNESS_RUNTIME` | Runtime provider | `docker` or `eci`, default `docker` |
| `HOST_IMAGE` | Host image reference | `ghcr.io/xyc999-ai/go-bot-host:latest`; Docker requires it locally. ECI cache creation and instance startup pass this same reference |
| `HARNESS_STORAGE` | Host filesystem | `container` by default. Docker automatically mounts an account volume. ECI uses the instance filesystem or can be set to `nas` |
| `NAS_MOUNT_TARGET` | Existing NAS mount target | Required for ECI with `nas` |
| `CONTROLLER_LISTEN` | Loopback address for a native Controller | `127.0.0.1:8080` |
| `ECI_REGION` | Alibaba region | `cn-hangzhou` |
| `ECI_VSWITCH_ID` | Existing vSwitch ID | Required for ECI |
| `ECI_SECURITY_GROUP_ID` | Existing security group ID | Required for ECI |
| `ECI_IMAGE_CACHE_ID` | Ready image cache ID | Required for ECI startup |

NAS uses the account-specific `/go-bot/<userId>`
subdirectory. The mount target is the deployment-specific input.

An HTTPS gateway handles TLS and forwards to the Controller's HTTP listener. Preserve the original `Host` and `X-User-ID` headers, set `X-Forwarded-Proto` to `https` for HTTPS requests, and forward SSE without buffering. Controller uses the request host and scheme to check browser origins. Tailscale Serve supplies the host and scheme automatically. Restrict access at the gateway or through your private network.

Schedule uses the upstream defaults of 30 days and 200 records per task for
delivery history, and 60 seconds between deliveries. The optional tuning YAML
fields `schedule.delivery_history_days`, `schedule.delivery_history_records`
and `schedule.new_session_interval_seconds` change history retention and the
minimum interval for tasks that create a new session on every delivery.
Apply tuning when creating or replacing the Host.

## Model, search and image generation

Add one or more OpenAI Responses or Anthropic Messages compatible endpoints in Model settings.
Each endpoint has its own API key and ordered model IDs. New sessions use the
first model of the default endpoint. An individual session can switch models
during the lifetime of its Host. Child agents inherit the parent turn's route.
The Host uses a 1,000,000-token context budget.

In **Settings → Web search**, save a key for DeepSeek, Zhipu, Doubao, Tavily or Brave,
then choose **Use provider** on your preferred service. Chat and page fetching
work without a search key. For image generation, install and enable **Gemini API CLI**
in the plugin market and configure its API key in **Accounts and authorization**.
The built-in image-generation tool and settings UI are currently disabled; their
implementations and saved settings remain available for future integration.
See the [provider guide](providers.md) for account and key requirements.

### Proactive assistance

Proactive assistance is off by default for new accounts and accounts without a
saved preference. Enable it in **Settings → Proactive assistance** on the Web,
or **Me → Proactive assistance** on mobile. Enabling requires a usable model
configuration; otherwise the switch stays off and the client offers to open
model settings. This configuration check does not call the model service.
The Web also displays the next check and current status;
mobile shows those diagnostic details only in Debug builds. Release builds keep
the switch and actionable configuration/error messages. No new
deployment environment variable or extra model profile is needed. This feature
uses the main chat's valid model selection, or the account default if the main
chat has no selection yet. A broken explicit selection waits for correction.
Model and connected-service calls can consume the user's API credits.

Host manages an independent persistent background Agent using the existing Go
AgentLoop. Each successfully finished turn submits a non-null future check time
and reason through `structured_output`, independently of whether it chooses to
contact the main chat. New dialogue, memory changes and plugin information use a
10-minute quiet window and a 30-minute minimum interval from the latest run;
these gates never delay an AI-selected timer or user Schedule. A successful
collection consumes the earlier change marker, while later changes remain pending.
Turning the service off and back on while its Host is running requests an immediate
check. Repeated configuration synchronization does not trigger another turn; a
normal Host restart restores the existing AI-selected check time.

The Host stores its private recovery projection in
`/root/harness/.dsh/storages/proactive.json`; research lives in its ordinary v4
Session journal. Its internal preset excludes the journal from public chat lists
and session search. The next timer and pending delivery resume with the Host's
normal volume/NAS data. Stopping the Host stops checks; Controller never starts
a Host just because a check is due. Disabling assistance leaves user Schedules
and previously published replies intact. There is no phone push requirement.

## Credentials

| Variable | Scope |
|---|---|
| `ALIBABA_CLOUD_ACCESS_KEY_ID` | Controller and buildeci, required for ECI |
| `ALIBABA_CLOUD_ACCESS_KEY_SECRET` | Controller and buildeci, required for ECI. The same pair is used with optional NAS |

`buildeci` passes the public `HOST_IMAGE` reference to Alibaba Cloud ECI without
registry credentials. It creates a cloud image cache, not a local image build or
registry push. Project image publication is a separate GitHub Actions workflow;
its automatic `GITHUB_TOKEN` is not a deployment credential.
When creating an ECI Host, Controller passes `HOST_IMAGE` and `ECI_IMAGE_CACHE_ID`
directly, without querying the cache to discover its image. Rebuild the cache and
update its ID when changing the image version. The explicit `doctor` command can
still inspect cache readiness as a diagnostic.
The template defaults to `latest`. A ready cache retains the image it was made
from even if that tag later moves. Use a new `buildeci --operation` name for an
intentional cache refresh; repeating the same operation resumes the old request.

### Redis account storage

Each account has one Redis Hash at `user:<userId>` with no expiration.

| Hash field | Format | Contents |
| --- | --- | --- |
| `desiredState` | String | `stopped` or `running` |
| `hostId` | String | Current Host instance ID |
| `mainSessionId` | String | Stable main chat UUID, reserved when the account is created |
| `proactive` | JSON | `enabled` boolean; account preference only, no background history or timer queue |
| `hostToken` | String | Internal credential used by Controller to call the Host |
| `hostEndpoint` | String | Ready Host URL including its port |
| `llm` | JSON | `defaultEndpointId` and `endpoints`. Each endpoint stores its name, URL, API key and ordered model list |
| `webSearch` | JSON | Active `provider` and per-provider `entries` with API keys |
| `imageGeneration` | JSON | Active `provider` and per-provider `entries` with API keys and model names |
| `plugins` | JSON | Per-package user selections: catalogId, fixed version and enabled. No instance configs, sources or secrets; credentials remain in the Host |
| `sessionModels` | JSON | Map from session ID to `{endpointId, modelId}` |
| `modelRevision` | Integer | Version of the model catalog and session selections synchronized with the Host |

Settings fields are added as they are configured. Host instance fields can
be empty. Compose persists Redis in a named volume. Protect that volume and
its backups as credentials. Start with an empty Redis database when switching
from the previous development storage format.

Controller supplies `HOST_ACCOUNT_ID`, `HOST_ID`, `HOST_API_TOKEN` and
`HOST_DATA_MOUNTED` when it starts the Host. It synchronizes the selected search
and image settings, model catalog and session selections over the authenticated
Host control channel. Saved plugin intent is applied by the separate plugin preparation request so first-time downloads do not block the main chat or settings. Open an account to start or reconnect its
Host. Use the stop request to stop it. Host-created scheduled sessions
store their model selections in the Host data directory.

## Optional YAML

[configs/default.yaml](../configs/default.yaml) lists runtime tuning defaults.
[configs/buildeci.yaml](../configs/buildeci.yaml) lists optional cache sizing,
retention and wait settings.
