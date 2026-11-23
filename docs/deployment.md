# Deployment

## Default local Docker

Install Docker with Compose v2, curl and Bash. No source checkout is required.

```bash
curl -fsSL https://raw.githubusercontent.com/xyc999-ai/go-bot/main/scripts/install.sh | bash
cd go-bot
docker compose up -d
```

Use `bash -s -- --dir /path/to/go-bot` after the pipe to select another directory.
The installer downloads the original repository Compose, `.env.example` and
`configs/default.yaml`, creates only `.env`, and pulls the Controller, Host and
Redis images. It preserves existing configuration and does not start services.
The installed Compose is identical to the repository file. Both use published
images with `pull_policy: never`; neither startup nor the source launcher
implicitly pulls or builds. Open http://localhost:8080, create an account and
configure its model endpoint in Settings. Opening an account starts or reconnects
its independent Host. Search providers and plugin credentials are configured in
the UI, not in the deployment template.

The published Host is built from `build/Dockerfile.host`; Controller and its
embedded Web UI are built from `build/Dockerfile.controller`. Both have AMD64 and
ARM64 variants selected automatically by Docker. Redis uses `redis:8-alpine`.
[Host Dockerfile](../build/Dockerfile.host) · [Controller Dockerfile](../build/Dockerfile.controller) · [Compose](../compose.yaml)

### Source checkout

The repository launcher is independent of install.sh. First explicitly pull
the images (or deliberately build them with `make image image-controller`), then:

```bash
docker pull ghcr.io/xyc999-ai/go-bot-controller:latest
docker pull ghcr.io/xyc999-ai/go-bot-host:latest
docker pull redis:8-alpine
./scripts/start.sh --init-env
./scripts/start.sh
```

Initialization creates `.env` and `.env.eci` without overwriting either file.
`start.sh` requires an existing selected profile, defaults to `.env`, applies
validated shell overrides in memory and starts the same Compose stack. It reports missing
local images instead of pulling or building. Checks use tools in the Controller
image, so no local Go is required. Compose also validates on Controller startup,
independently of this script.

```bash
./bin/controller check
./bin/controller doctor
./bin/controller host status --user alice
./bin/controller host stop --user alice
./bin/controller host start --user alice
./bin/controller host delete --user alice
```

Build the CLI with `make build`. `doctor` checks the runtime without creating
compute. Account commands use the running Controller. The API accepts the
same account name through the `X-User-ID` header.

`docker compose down` shuts down Controller and Redis and retains the Redis
volume. Stop or release each Host through the Controller before bringing
Compose down if you want to stop its compute. `docker compose down --volumes`
also removes the Redis volume and the accounts stored in it.

## Storage choices

Controller-managed Docker Hosts mount `go-bot-<userId>-data` at `/root/harness`
automatically. After building an updated image, release the old Host with
`controller host delete --user alice` and reopen the account. The replacement
container uses the new image and mounts the same volume. The volume is local to
the Docker daemon. ECI can retain its data across instance replacement with NAS.

The account's long-term memory files live under `/root/harness/.dsh/memories`.
The Host keeps its SQLite FTS5 index at `/var/lib/go-bot/session-search.sqlite`
in both runtimes and rebuilds it from the account's v4 journals after replacing
the container.

For an older Docker Host that stored data in its writable layer, stop it and
copy its `/root/harness` contents into the account volume before releasing it.
Use `docker inspect` to check the mounts on an existing container.

Controller launches each Docker Host as a separate container outside Compose. Docker
maps its port 8080 to a dynamic host port. The containerized Controller
uses `host.docker.internal` and the discovered port. Direct Host requests
require its internal bearer token. A native Controller uses a dynamic
loopback port for each Host.

Hosts remain running after a client disconnects. Use **Stop Host** or the
Controller stop API to stop an account's Host. Docker keeps its stopped
container. ECI releases its instance. Opening the account again resumes
Docker or creates a new ECI instance. Use the stop API again if a request
returns an error and you still want to stop the Host.

## ECI image cache

See the [ECI prerequisites and setup walkthrough](../README.md#alibaba-cloud-eci)
for RAM, VPC, security-group and NAS setup.

Run `./scripts/start.sh --init-env` to create both profiles. Edit the ECI
profile and fill in the shared Alibaba AccessKey pair, region, vSwitch
and security group. Pull the published Controller and Redis images first, as in
the ECI README walkthrough. The public `HOST_IMAGE` is the only image-address setting. The
cache tool preserves that multi-platform reference; Alibaba Cloud pulls it for
its default AMD64 environment.

```bash
./scripts/buildeci.sh
./scripts/start.sh --env-file .env.eci
```

Before starting Controller, set `HARNESS_RUNTIME=eci` in the same profile.
Configure search per account in the Web UI after deployment. The default
`cache` mode uses the tool inside the local Controller image, creates an ECI
cache, waits for `Ready` and writes `ECI_IMAGE_CACHE_ID` into the profile.
It does not build or push, and needs no local Go. The script uses
`.env.eci` by default and accepts `--env-file PATH`. The script reports an error if the selected
profile is missing. Cache creation provisions
Alibaba Cloud resources. See [Alibaba Cloud cache billing](https://www.alibabacloud.com/help/en/eci/product-overview/image-caches).

There is no separate registry credential, push-address or local-publication path
in `buildeci`. The cloud pulls from the configured public reference directly.
Project image publication is handled by the GitHub Actions workflow below.

### Cache API request

The script starts a temporary tool container from the local Controller image;
it does not start the Controller service. The Go command reads the selected
profile and calls the Alibaba Cloud Go SDK's `CreateImageCache` operation
(ECI API version `2018-08-08`, endpoint `eci.<ECI_REGION>.aliyuncs.com`).

| API parameter | Source in this project |
| --- | --- |
| `RegionId` | `ECI_REGION`, default `cn-hangzhou` |
| `Image.1` | `HOST_IMAGE`; one public, multi-platform Host image reference |
| `VSwitchId` | `ECI_VSWITCH_ID` |
| `SecurityGroupId` | `ECI_SECURITY_GROUP_ID` |
| `ImageCacheName` | Generated as `go-bot-` plus the first eight characters of the operation UUID |
| `ClientToken` | Generated UUID, saved locally and reused when retrying the same operation |
| `ImageCacheSize` | Optional `size_gib` in cache tuning YAML; omitted when zero |
| `RetentionDays` | Optional `retention_days` in cache tuning YAML; omitted when zero |

The shared AccessKey ID and Secret authenticate SDK requests; they are not
image registry credentials. The tool sends no registry authentication, EIP,
NAS mount or account Host configuration in this request. With the selected
vSwitch covered by NAT, Alibaba Cloud can pull the public image directly.

Alibaba Cloud creates an intermediate ECI instance and disk, pulls the image,
and prepares the cache snapshot. The client does not run `docker build` or
upload a local Host image. The platform defaults are 20 GiB and no expiry when
size/retention are omitted. [CreateImageCache API](https://help.aliyun.com/zh/eci/developer-reference/api-eci-2018-08-08-createimagecache)

The client calls `DescribeImageCaches` with `RegionId` and `ImageCacheId` every
three seconds until Ready (up to 90 minutes by default). It then atomically
updates only `ECI_IMAGE_CACHE_ID` in the selected profile. Failure or timeout
does not replace that saved value. [DescribeImageCaches API](https://www.alibabacloud.com/help/en/eci/developer-reference/api-eci-2018-08-08-describeimagecaches)

The default wait is 90 minutes. Set `wait_timeout` in the YAML passed with
`--config` to adjust it. Rerun the same operation after a timeout or lost
create response to resume the saved cache operation.
Use `./scripts/buildeci.sh cache --operation <new-name>` to
create a replacement cache for the same published image. Cache output includes
the image reference and a digest when the input is digest-pinned.
This also applies after `latest` has moved: use a new operation name for an
intentional refresh. Repeating the same operation resumes or reuses its saved
cache; it is not an implicit update of the cached image.

You can set an existing Ready `ECI_IMAGE_CACHE_ID` directly and start the
controller without running `buildeci`. Set `HOST_IMAGE` to the exact image used to
make that cache. ECI creation passes the configured image and cache ID directly,
with cache-only startup; it does not query the cache for its image address. The launcher checks
configuration before Compose starts. Run `controller doctor --env-file .env.eci`
to check Alibaba Cloud access and cache readiness before opening an account.
The controller needs
private-network access to the ECI port 8080. The ECI Host needs network
access to the configured model and search services. For a private ECI Host that
needs outbound internet access, cover its vSwitch with a NAT gateway SNAT rule.
[Alibaba Cloud ECI networking](https://www.alibabacloud.com/help/en/eci/user-guide/enable-internet-access)

For persistent NAS set `HARNESS_STORAGE=nas` and `NAS_MOUNT_TARGET` to an
existing mount target hostname. Each account uses writable `/go-bot/<userId>`
storage on NAS. The driver mounts it at
`/root/harness`. Session journals live at
`<nasPath>/.dsh/sessions/<project-key>/<session-id>/session.v4.jsonl`.
Schedules, attachments and user skills share that account's NAS directory.
OfficeCLI and agent-browser are optional plugins installed from the plugin market.
Their original Skills are contributed from the installed plugin packages; the Host
no longer copies a first-start skill seed into user directories.
ECI container storage uses the writable layer for the lifetime of the instance.

See [validation](validation.md) for real Docker coverage and the remaining
cloud checks.

## Publishing project images

[Publish container images](../.github/workflows/publish-images.yml) runs only
for pushed Git tags matching `v*`, or through **Actions → Publish container
images → Run workflow** with a version such as `v0.0.1`. Normal commits and PRs
run CI validation without publishing images. Manual publication builds the
selected ref; choose the intended source commit/branch before running it.

The workflow builds each role on native AMD64 and ARM64 runners and publishes
the Controller and Host artifacts by digest. Only after all builds succeed does
it combine them into each role's multi-platform `<version>` and `latest` tags.
No architecture-specific tag is required for ECI. `latest` is a mutable alias, not an automatic Docker version calculation.
The deployment defaults use `latest` for both Controller and Host. Users can
choose version tags when they want a fixed release. Treat published version tags
as immutable; use a new version for changed source. Updating a registry tag does
not replace running containers or update an existing ECI cache.
Git-tag publication updates `latest`. For manual backfills, disable **update_latest**
to publish the chosen version without moving `latest` backwards; the workflow does
not infer semantic-version ordering.

GitHub Actions authenticates with its automatic `GITHUB_TOKEN` and
`packages: write`; maintainers do not need a Docker Hub account or additional
registry secrets. On first publication, both GHCR packages default to private:
open each package's **Package settings → Change visibility → Public**, then
rerun the manifest job's anonymous-access check. Source repository visibility
does not set package visibility. Confirm anonymous pulls before advertising the
installation command. [GHCR documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)

For the initial release, manually publish `v0.0.1` from the reviewed implementation
before exposing its installer on main. Subsequent releases publish the version
and promote `latest` after all architecture builds succeed. Keep the shared
Compose, `.env.example`, Go defaults and documentation consistent when deployment
behavior changes. The installer downloads these files directly from main;
it is not a Release attachment. Existing installations keep their files on a
repeat install; upgrades require deliberately updating the files and preparing
the new images. Pulling a new image alone does not replace an existing Host.

## Plugin and image dependencies

The base Host supplies Go, Node/npm/pnpm, Python, Chromium, LibreOffice and general
file-processing libraries. OfficeCLI and agent-browser CLIs are installed when the
user installs their plugins, rather than being preinstalled in the runtime image.

A disposable build stage exports the upstream Skills using official installers and
includes their complete resources in each wrapper package. Enabling a plugin
registers its Skills from the package; disabling it withdraws that contribution.
User-created Skills remain in the existing user Skill roots.

OfficeCLI installation runs `npm install -g @officecli/officecli`, without an added
version/checksum lock or an automatic-update override. Plugin package updates are
separate from CLI version selection. Installation policy belongs to the plugin;
the Host only coordinates package installation and reports the result.

Other build dependency versions are recorded in `build/dependencies.lock.yaml`
and the respective package lockfiles. No third-party binary or SDK source is
committed.
