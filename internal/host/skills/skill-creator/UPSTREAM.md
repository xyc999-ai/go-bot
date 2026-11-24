# Upstream source

This bundle derives from OpenAI's Apache-2.0-licensed `skill-creator`:

- Repository: https://github.com/openai/skills
- Commit: `49f948faa9258a0c61caceaf225e179651397431`
- Directory: `skills/.system/skill-creator`
- License: [LICENSE.txt](LICENSE.txt), retained unchanged.

Go Bot modifications:

- `SKILL.md`: name the Go Bot Agent, explain account/project paths, current tool
  names, invocation controls, and the compatibility-only role of `agents/openai.yaml`.
- `scripts/init_skill.py`: default the output directory to `$DSH_HOME/skills`,
  falling back to `/root/harness/.dsh/skills`; explicit `--path` is still supported.
- `scripts/quick_validate.py`: validate Go Bot invocation fields and its 256 KiB
  instruction-file limit; reject empty names/descriptions and unreadable UTF-8.

All other upstream files, including the UI metadata generator, are unchanged.
The full workflow and resource structure are retained. Go Bot seeds this bundle
once into the user's `.dsh/skills/skill-creator`; subsequent edits or deletion are
user-managed and are not overwritten by restarts or upgrades.
