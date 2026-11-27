# Contributing

All ideas and contributions are welcome, including features, fixes, experiments,
plugins, documentation, translations and examples. You can open a pull request
directly; an issue or prior proposal is not required. Issues are also welcome
when you want to ask a question or discuss an idea.

## Where to contribute

| Contribution | Repository |
| --- | --- |
| Harness, Controller, Host, clients, plugin SDK/runtime and project documentation | [go-bot](https://github.com/xyc999-ai/go-bot) |
| New plugins, connectors, plugin-specific Skills, resources and fixes | [go-bot-plugins](https://github.com/xyc999-ai/go-bot-plugins) |

Fork the relevant repository, create a topic branch from `main`, and open a pull
request against its `main` branch. Describe the resulting behavior and the checks
you ran. For a change spanning both repositories, link the related pull requests.

## Contributing plugins

Plugin packages and their releases belong in
[go-bot-plugins](https://github.com/xyc999-ai/go-bot-plugins). Follow that repository's
[contribution rules](https://github.com/xyc999-ai/go-bot-plugins/blob/main/CONTRIBUTING.md)
and [development and publishing guide](https://github.com/xyc999-ai/go-bot-plugins#develop).
The main repository keeps the shared [Host SDK](extensions/sdk/README.md),
[catalog integration](extensions/catalog/README.md) and [development examples](examples/plugins/README.md).

1. Add or update `plugins/<id>/` in the plugin repository. Include a standard
   `package.json`, a `plugin.json` display manifest, business code, and any needed
   installation scripts or resources. Keep English and Chinese display copy,
   icons, licenses and upstream notices with the plugin.
2. Use the Host-provided Cordis framework and asynchronous `ctx.host` SDK.
   Keep framework modules out of production dependencies; use development
   dependencies or optional peers for types. Compile TypeScript before packaging.
3. Put dependency installation in standard package lifecycle scripts. Keep it
   repeatable and noninteractive. Activation registers capabilities and checks
   prerequisites; service credentials and authorization are configured afterward
   through the plugin's declared authorization flow. Never commit secrets.
4. Each plugin has its own version. For a plugin release, keep `package.json`
   and `plugin.json` versions consistent. Plugin versions are independent of the
   Go Bot client versions.
5. With Node.js 24.9+ and the plugin's development dependencies installed, run
   `npm test` and `npm run check` from the plugin repository root. Include relevant
   tests and describe any manual checks of installation, activation and authorization.
6. Submit the PR to the plugin repository. Maintainers publish through its
   **Publish plugins** workflow: prepare upstream Skills, pack and upload release
   assets, then update the catalog. Ordinary commits do not publish plugins.
   Preserve published releases and historical catalog entries; release a new
   version instead of replacing an existing package or asset.

## Branches and commits

Use `main` as the base for new work. Prefer a short topic branch for each change
and open a pull request against `main`. Maintainers may push directly to `main`
when appropriate.

| Change | Example branch | Example commit or PR title |
| --- | --- | --- |
| New feature | `feat/mobile-attachments` | `feat(mobile): add chat attachments` |
| Bug fix | `fix/subagent-test-workspace` | `fix(host): use writable subagent test workspaces` |
| Documentation | `docs/android-release` | `docs(mobile): explain Android release signing` |
| Build | `build/android-signing` | `build(mobile): configure release signing` |
| CI | `ci/docker-smoke` | `ci(docker): smoke test the Compose stack` |

Use lowercase words separated by hyphens in branch names. For commits and PR
titles, prefer `type(scope): short summary`. Pick a scope that names the main
area, such as `host`, `controller`, `web`, `mobile`, or `docker`. Prefer an
action verb in the summary. If there is no clear scope, use `type: short summary`.
Use `test`, `refactor`, `build`, or `chore` when they describe the change better.
For a breaking change, explain the impact and any migration steps in the PR.
`type(scope)!:` or a `BREAKING CHANGE:` footer can also mark it in commit history.

Keep each commit focused on one logical change. Before committing, review the
staged changes with `git diff --cached` and check them with
`git diff --cached --check`. In the PR description, explain what changed and how
you verified it. If squashing a PR, use its title as the final commit subject
when it accurately describes the change.

These conventions are preferences. Use judgment when another name or message
is clearer. They draw on [Git's commit guidelines](https://git-scm.com/book/en/v2/Distributed-Git-Contributing-to-a-Project#_commit_guidelines)
and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).

## Coding agents

This guide defines the contribution conventions. [AGENTS.md](AGENTS.md) gives
coding agents a short summary and points back here for details. Open the
repository root as the workspace so the tool can discover its project files.

| Tool | How it reads this project's instructions |
| --- | --- |
| [Codex](https://learn.chatgpt.com/docs/agent-configuration/agents-md), [Cursor](https://cursor.com/docs/rules), [OpenCode](https://opencode.ai/docs/instructions/), [Cline](https://docs.cline.bot/customization/cline-rules), [Devin Local](https://docs.devin.ai/desktop/devin-local/customizations) | Read the root `AGENTS.md` directly. |
| [Claude Code](https://code.claude.com/docs/en/memory) | [`.claude/CLAUDE.md`](.claude/CLAUDE.md) imports `AGENTS.md`, including for versions and settings that rely on `CLAUDE.md`. |
| [Gemini CLI](https://geminicli.com/docs/cli/gemini-md/) | [`GEMINI.md`](GEMINI.md) imports `AGENTS.md` using the default project context filename. |
| [GitHub Copilot](https://docs.github.com/en/copilot/reference/custom-instructions-support) | Supported agent modes read `AGENTS.md`. [`.github/copilot-instructions.md`](.github/copilot-instructions.md) also points Chat and review surfaces to the shared files. |
| [Aider](https://aider.chat/docs/usage/conventions.html) | From the repository root, run `aider --read AGENTS.md --read CONTRIBUTING.md` to load both files as context. |

## Development and verification

Use Go 1.26. Run `make test vet check`; run race tests for concurrency changes and
Docker integration for runtime changes. Normal tests must not require credentials,
Redis or cloud infrastructure. Keep upstream fixtures and source notices intact.

For bundled Skill Creator helper changes, also run
`python3 -m unittest discover -s tests/skills -v` with PyYAML installed (the Host
image includes it). The Docker conversation integration covers initialization,
loading the skill and its generated skills, and deletion across Host replacement.

Keep runtime orchestration in providers, external SDK/API calls in thirdparty,
and Agent execution in harness. New providers need strict configuration,
credential validation, cancellation and error tests. Do not expose SDK types in
the public runtime contract. Never commit private environment profiles or generated tokens.

Document behavior changes, data compatibility and checks performed. For large
protocol or format changes, explain the design and migration in the pull request;
a separate issue or proposal is optional. Contributions to Go Bot
are provided under the [MIT License](LICENSE) unless stated otherwise.
