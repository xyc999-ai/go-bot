# Documentation

[English README](../README.md) · [简体中文 README](../README.zh-CN.md)

| Guide | Contents |
| --- | --- |
| [Configuration](configuration.md) | Private profiles, validation, precedence, and per-account settings |
| [Deployment](deployment.md) | Docker and ECI operations, image caches, storage, and Host lifecycle |
| [Troubleshooting](troubleshooting.md) | Diagnose startup, connectivity, and account settings |
| [HTTP API](api.md) · [OpenAPI](../api/openapi.yaml) | Accounts, sessions, messages, streaming, and files |
| [Architecture](architecture.md) | Controller, Redis, Host, and data layout |
| [Main chat and first run](main-chat-and-first-run-plan.zh-CN.md) | Stable main conversation, model checks, welcome seed and default plugin preparation |
| [Schedule and workspaces](schedule-parity-plan.zh-CN.md) | Upstream behavior, delivery receipts, chat archiving, and mobile task management |
| [Proactive wake-up research](proactive-service-research-and-plan.zh-CN.md) | Verified Muse, Grok Bot and dots behavior, disclosed triggers, and limits of public implementation details |
| [Agent composition](agent-composition-research-plan.zh-CN.md) | Implemented fixed Go composition selectors and background Agent; distinct from the full Preset subsystem |
| [Go Agent Preset proposal](agent-preset-go-plan.zh-CN.md) | Planned Go registry, definitions, Agent bindings, selection projection and restoration; not yet implemented |
| [Proactive discovery](proactive-discovery-plan.zh-CN.md) | Implemented persistent background Agent, LLM-selected wake times, structured decisions and internal delivery |
| [Extending capabilities](providers.md) · [Contributing](../CONTRIBUTING.md) | Provider contracts and development workflow |
| [X / Google account setup](plugin-account-setup.md) | Obtain your application credentials and connect native accounts |
| [Gemini API CLI plugin plan](gemini-api-cli-plugin-plan.zh-CN.md) | API-key connection, official CLI installation, Agent usage and file delivery |
| [Business plugin SDK](../extensions/sdk/README.md) · [Examples](../examples/plugins/README.md) | Native Cordis plugins, async Host APIs, credentials and MCP |
| [Web visual redesign](web-lumeo-redesign-plan.zh-CN.md) | Lumeo reference, navigation rail, task browser, themes, typography, and visual validation |
| [Frontend development](../frontend/README.md) | Develop the UI with Vite against Controller |
| [Chat images and tools](chat-media-display-plan.zh-CN.md) | Compact image tools, horizontal edge fades and delivered image previews |
| [Streaming text playback](stream-text-playback-plan.zh-CN.md) | Shared Web/mobile progressive text display and validation |
| [Markdown and demo refresh](markdown-and-demo-refresh-plan.zh-CN.md) | Scrollable tables and code, source copying, attachment order, and bilingual demos |
| [Upstream compatibility](compatibility.md) · [Validation](validation.md) | Upstream fixtures and historical verification results |
| [License](../LICENSE) · [Third-party notices](../THIRD_PARTY_NOTICES.md) | License terms and upstream attribution |

Run the development checks from the repository root:

```sh
make test vet check
make race
make build-web
npm --prefix frontend test
make image
./scripts/smoke-image.sh
RUN_DOCKER_INTEGRATION=1 go test -tags webui ./tests/integration -v
python3 scripts/smoke-cli.py
```

The Chinese design and research notes in this directory record earlier implementation
decisions. Use the guides above for current commands and behavior.
