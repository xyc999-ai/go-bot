# Documentation

[English README](../README.md) · [简体中文 README](../README.zh-CN.md)

## User and developer guides

| Guide | Contents |
| --- | --- |
| [Configuration](configuration.md) | Deployment profiles, validation and per-account settings |
| [Deployment](deployment.md) | Docker and ECI, image caches, storage and image publication |
| [Troubleshooting](troubleshooting.md) | Startup, connectivity and account settings |
| [HTTP API](api.md) · [OpenAPI](../api/openapi.yaml) | Accounts, sessions, streaming, files, permissions and plugins |
| [Architecture](architecture.md) | Controller, Redis, Host, Go composition and storage |
| [Extending capabilities](providers.md) | Model, runtime, search and tool adapters |
| [Plugin accounts](plugin-account-setup.md) · [Google application setup](google-workspace-app-setup.md) | Configure plugin services and OAuth applications |
| [Upstream compatibility](compatibility.md) | Session format, behavior references and fixture verification |
| [Contributing](../CONTRIBUTING.md) | Source development and verification |
| [Frontend development](../frontend/README.md) · [Mobile development](../mobile/README.md) | Develop and customize the supplied clients |

## Design documents

Design documents describe responsibilities, data contracts and interactions. Their
status notes distinguish implemented behavior from proposals. For current setup
commands, use the guides above.

| Design | Contents |
| --- | --- |
| [Controller and Redis](redis-multi-host-controller-plan.zh-CN.md) | Account data, multiple Hosts and synchronous lifecycle requests |
| [Container distribution](container-release-plan.zh-CN.md) | Separate GHCR images, installer and Docker/ECI startup boundaries |
| [Main chat and first run](main-chat-and-first-run-plan.zh-CN.md) | Main Session, greeting, model checks and default plugins |
| [Agent Presets](agent-preset-go-plan.zh-CN.md) | Implemented Go registry, immutable bindings and per-Agent capability composition |
| [Models and sessions](model-settings-and-session-routing-plan.zh-CN.md) | Endpoint settings and per-session model selection |
| [Model protocols](anthropic-model-protocol-plan.zh-CN.md) | OpenAI/Anthropic adapters and protocol selection |
| [Long-term memory](hermes-agent-memory-design.zh-CN.md) | Memory files, prompt integration and SQLite FTS5 recall |
| [Schedules and workspaces](schedule-parity-plan.zh-CN.md) | Delivery, working directories and archival |
| [Proactive assistance](proactive-discovery-plan.zh-CN.md) | Background context, wake decisions and main-chat delivery |
| [Proactive experience evaluation](proactive-service-real-world-test-plan.zh-CN.md) | Planned week-long multi-session history and real-model observation from the user's perspective |
| [Permissions and approvals](permission-parity-plan.zh-CN.md) | Per-session execution permissions and human approval |
| [Host event stream](host-event-stream-plan.zh-CN.md) | Shared client stream and in-chat questions/approvals |
| [Business plugin system](host-bridge-plugin-system-plan.zh-CN.md) | Cordis, asynchronous Host Bridge and Go core services |
| [Default capability plugins](agent-browser-officecli-plugin-plan.zh-CN.md) | agent-browser/OfficeCLI installation, activation and Skills |
| [X and Google Workspace](x-google-workspace-plugin-plan.zh-CN.md) | CLI/MCP integration and plugin authorization |
| [Gemini API CLI](gemini-api-cli-plugin-plan.zh-CN.md) | API-key configuration, media tools and Agent integration |
| [Web search and fetch](web-tools-parity-plan.zh-CN.md) | Provider boundaries, formatting and bounded output |
| [Web UI](web-lumeo-redesign-plan.zh-CN.md) | Navigation, themes, typography and interaction |
| [Chat images and tools](chat-media-display-plan.zh-CN.md) | Image groups, scrolling and presented-image previews |
| [Streaming text](stream-text-playback-plan.zh-CN.md) | Progressive display across Web and mobile |
| [Markdown and attachments](markdown-and-demo-refresh-plan.zh-CN.md) | Tables, code, copying, attachment order and demos |

## Showcase

[Showcase assets](showcase/) contains the real demo GIFs and images referenced by
both READMEs. Chinese and English demos are retained alongside their source
screenshots.

## Licensing

[MIT License](../LICENSE) · [Third-party notices](../THIRD_PARTY_NOTICES.md) · [Security policy](../SECURITY.md)

## Development checks

Run from the repository root:

```sh
make test vet check
make race
make build-web
npm --prefix frontend test
make image image-controller
./scripts/smoke-image.sh
RUN_DOCKER_INTEGRATION=1 go test -tags webui ./tests/integration -v
python3 scripts/smoke-cli.py
```

Docker checks require Docker to be running. Real cloud and third-party service
checks additionally need the deployer's own configuration and credentials.
