# Go Bot agent instructions

Before creating a branch, preparing a commit, or opening a pull request, read
the [contribution guide](CONTRIBUTING.md#branches-and-commits). Apply these defaults
when the user has not specified a different workflow:

- Start new work from `main` on a topic branch such as `feat/mobile-attachments`,
  `fix/subagent-test-workspace`, or `docs/android-release`.
- Prefer `type(scope): short summary` for commit and pull request titles. Use
  `feat` for features, `fix` for bug fixes, and `docs` for documentation changes.
  Omit the scope when there is no clear area.
- Keep unrelated changes in separate commits. Review the staged diff and record
  relevant verification in the pull request.

These are project preferences. Follow an explicitly requested branch, commit
message, or push target. The [contribution guide](CONTRIBUTING.md) also covers
project structure and verification.

## Client versions and build numbers

Change client versions or mobile build numbers only when the user explicitly
requests a version or build-number update. Feature work, bug fixes, tests,
packaging, Expo Prebuild, CI runs, tags and Release creation do not implicitly
authorize a version bump. Use the requested version; ask for the target version
if it is not specified or otherwise clear from the user's instructions.

For an authorized client version update, keep these values synchronized:

- `version` in `desktop/package.json`, `mobile/package.json` and
  `frontend/package.json`.
- The top-level `version` and `packages[""].version` in each corresponding
  `package-lock.json`. Do not change dependency versions as part of this step.
- `expo.version` in `mobile/app.json`.

`desktop/package.json` is the CI release-version source. Preserve the package
names `go-bot-desktop`, `go-bot-mobile` and `go-bot-web`, and existing application
identifiers. The manually entered release tag does not determine the version.

For an explicitly requested new mobile release, also advance
`expo.android.versionCode` (integer) and `expo.ios.buildNumber` (string) in
`mobile/app.json`, unless the user specifies a different scope or exact values.
These build numbers are independent; they do not have to match each other or
the display version. A build-number-only request leaves display versions alone.
Expo Prebuild copies these configured values into generated native projects;
it does not increment them. Update the Expo configuration, rather than treating
ignored generated Android/iOS files as the version source of truth.

After a version update, run `node scripts/client-release-config.cjs` and verify
the lockfile root versions agree. Report the resulting display version and both
mobile build numbers.
