# Contributing

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

Document behavior changes, data compatibility and checks performed. Open an issue
or describe a design before large protocol/format changes. Contributions to Go Bot
are provided under the [MIT License](LICENSE) unless stated otherwise.
