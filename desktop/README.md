# Go Bot Desktop

Independent Electron + React + TypeScript + Vite desktop client for macOS and
Windows. Linux makers are retained for future use. The renderer owns a snapshot of the Web interface and evolves
independently: it has no source imports, symlinks, runtime dependency or build
dependency on `frontend/`. The Controller HTTP/SSE API is the shared contract.

## Run

Use Node.js 22.12+ (Node 24 recommended).

```sh
cd desktop
npm ci
npm run dev
```

Electron downloads its platform runtime on the first launch. The installed app
connects to `http://127.0.0.1:8080` automatically and opens the same account-entry
page as Web. Start the local Controller, then open or create your account.
There is no connection-address setting. Existing saved server addresses and
optional tray preferences are discarded on upgrade.

Model/provider keys continue to be managed by Controller/Host. The desktop app
does not start Redis, Docker, Controller or an Agent automatically.

## Behavior

- Presented MP4/WebM/MOV/M4V files use the Web-style inline video player, with
  seek, mute, fullscreen, download and unsupported-codec fallback. Only one chat
  video plays at a time; leaving the view pauses playback. Actual decoding depends
  on the platform codec support.
- Chat titles and rename dialogs follow Web, including the fixed main chat and
  title changes received from other clients.
- A persistent anonymous desktop installation ID accompanies the Host stream. It
  lets the mobile Devices screen report desktop connection status; this desktop
  app has no device-list page, matching the current Web interface.
- Remote plugin icons/tutorial media pass through the Controller with streaming,
  HEAD and byte-range support. Explicit market refresh revalidates its catalog.
- Chats, text playback, tool cards, questions/approvals, models, permissions,
  attachment drafts, tasks, plugin management, authorization and Host files retain
  the Web presentation and API behavior.
- OAuth links open in the system browser. Callback URLs and MCP authorization
  bodies refer to the selected Controller, never `gobot://desktop`.
- File links use Electron's download flow. The file browser shows remote Host
  files, not the computer's filesystem. Documents must be uploaded to the Host.
- `Cmd/Ctrl+N` creates a chat; normal system Edit/View/Window menus provide copy,
  paste, selection, zoom and window controls.
- Closing a window keeps the app running by default on both macOS and Windows.
  Restore it through the macOS Dock or the Windows tray. Only an explicit Quit
  ends the app. Closing/Quit never calls Stop Host. This behavior is not a user
  preference and has no checkbox on the entry page.
- Notifications for approvals, questions and completed replies are emitted only
  while the application is running and unfocused. They contain no message text.
  System permission and do-not-disturb settings may suppress them. Clicking a
  notification opens its chat. Notifications are throttled by chat and type.
- A single renderer-owned Host SSE connection survives page changes. Network
  failures reconnect using existing cursors; waking the machine restarts that
  connection without resending messages or approval decisions.
- Theme/language persist under desktop-specific keys. Window
  preferences are stored in Electron's per-user `desktop.json`. The Controller address is fixed to local port 8080.

## Verify

```sh
npm test
npm run build
npm run smoke
npm run test:video
```

`smoke` launches real Electron against a local deterministic Controller fixture;
it needs a graphical session (use `xvfb-run` on Linux). It exercises the main
process, sandboxed preload, renderer, network gateway, stream reconnect and disk
download. It requires no Docker, credentials or paid model calls. `test:video` uses a
small checked-in VP8/WebM fixture to verify actual decoding, playback, media
ranges and downloads; FFmpeg is not required to run it. To repeat it
against a packaged application, set `DESKTOP_EXECUTABLE` to that application's
executable and run `node scripts/smoke.cjs`.

To capture the independently built Web UI and Electron with identical fixtures:

```sh
WEB_REFERENCE_DIST=/absolute/path/to/web/dist npm run test:visual
```

Install Playwright Chromium with `npx playwright install chromium`, or set
`CHROME_PATH` to an existing Chrome binary. Outputs go to ignored `test-results/`.
`DESKTOP_QA_OUTPUT` selects another output folder. See
[the design and validation record](design/desktop-plan.zh-CN.md).

## Package

```sh
npm run package
npm run make
```

The active CI build matrix targets macOS and Windows only. Forge produces
macOS ZIP applications and Windows Squirrel installers. Linux makers remain
available for future use but are not built by the current workflow. Run builds
on each target OS. `out/` is ignored. Current release targets are macOS arm64/x64 and Windows x64; target OS/architecture builds must be validated
on those machines before claiming release support.

`npm run make` produces local test packages. Windows installers are intentionally
unsigned; no Windows certificate or secret is configured. Windows may show an
unknown publisher or SmartScreen warning, and managed devices may block them.
A Help menu entry opens releases for manual updates; an automatic update feed
is not currently configured.

### Signed and notarized macOS releases

Install a **Developer ID Application** certificate and its private key in the
signing Mac's keychain. An Apple Distribution certificate is not a substitute.
Use an App Store Connect API key for notarization. Individual keys require
Xcode 26+ and **must omit** `APPLE_API_ISSUER`; team keys require the Issuer ID.
The key itself never goes into this repository or an application bundle.

Set these variables in the shell that runs the build (values below are examples):

```sh
export APPLE_SIGNING_IDENTITY='Developer ID Application: Your Company (YOURTEAMID)'
export APPLE_TEAM_ID='YOURTEAMID'
export APPLE_API_KEY='/absolute/path/outside/repository/ApiKey_KEYID.p8'
export APPLE_API_KEY_ID='YOUR_KEY_ID'
# Team keys only: export APPLE_API_ISSUER='your-issuer-uuid'
npm run check
npm run release:mac -- arm64
```

Use `x64` for an Intel build. Validate each architecture on a compatible Mac
(an Apple Silicon Mac needs Rosetta to test x64). Release builds explicitly enable
`GOBOT_DESKTOP_RELEASE=1`, require all credentials, and fail instead of silently
producing an unsigned release. Ordinary development builds do not use these keys.
`APPLE_SIGNING_KEYCHAIN` optionally selects an isolated CI keychain.

To reuse an existing signed App, create the DMG without rebuilding or re-signing
the App (the final DMG still needs its own notarization submission):

```sh
npm run release:mac -- arm64 --from-app '/absolute/path/to/Go Bot.app'
```

The same signing environment is required. The command checks the bundle ID,
version, architecture and Developer ID signature before using the existing App.
App and DMG Gatekeeper checks are required after the final DMG is accepted.

The release command authenticates before building, packages with Hardened Runtime,
signs the app, verifies its signature/team and runs native/video smoke suites with
isolated test data. It creates a compressed DMG using native `hdiutil` and signs
the DMG. **Only this final DMG is submitted to Apple**; Apple scans its nested App
in the same submission. It does not separately submit the App first.

Upload and processing are separate visible phases. As soon as upload completes,
the command prints and saves the submission ID. It waits in the same invocation
for up to 90 minutes (override with `GOBOT_NOTARIZATION_TIMEOUT_MINUTES`, 1–110).
If Apple still reports In Progress, or rejects the scan, the command fails and
retains the signed DMG, checksum, ID/status and available logs under
`out/notarization/macos-<arch>/attempt-*/`. It never treats pending as accepted,
silently skips notarization, or submits the same file again to accelerate the queue.

After acceptance it staples the DMG ticket, verifies Gatekeeper, and checks the
mounted payload. The read-only payload uses the container's stapled ticket. It
copies the app out and tests that installed copy, and staples the writable App's
ticket from the same submission before creating the optional ZIP. No running user
instance, user profile or existing installation is replaced.

macOS validates nested Mach-O executables, frameworks and Helper apps separately;
they are signed from the inside out before the outer app. Binary data such as
locale `.pak` files, fonts, images and `app.asar` are sealed as resources rather
than individually signed. This avoids unnecessary private-key operations.
On the first local signing operation, Keychain may ask to let `/usr/bin/codesign`
use the private key. A one-time Allow can cause repeated prompts; choosing Always
Allow for this signing tool persists its access to this key. Enter the Mac login
password only in the system dialog. The build never requests that password.

For CI, export only this Developer ID Application identity (certificate **and**
private key) from Keychain Access > My Certificates as a password-protected `.p12`.
Keep it outside the repository along with the `.p8`. The `.p12` protection password
is a separate credential from both the Mac login password and API key. Local builds
use the existing keychain identity and do not need the exported `.p12` password.

Only after all checks pass are artifacts copied to `out/release/macos-<arch>/`:

```text
go-bot-<version>-macos-<arch>.dmg
go-bot-<version>-macos-<arch>.zip
go-bot-<version>-macos-<arch>.sha256
go-bot-<version>-macos-<arch>.verification.json
```

The DMG contains the app, an Applications shortcut and bilingual installation
instructions. It is a desktop **client**; users still need a running local
Controller at port 8080. Closing its window keeps it running; Command+Q quits.

To independently repeat release validation:

```sh
APPLE_TEAM_ID='YOURTEAMID' npm run verify:mac -- '/path/to/Go Bot.app'
APPLE_TEAM_ID='YOURTEAMID' npm run verify:mac -- '/path/to/go-bot-release.dmg'
```

### GitHub Actions releases

The [Client release workflow](../.github/workflows/client-release.yml) is
**manual-only** (`workflow_dispatch`): pushes, pull requests and version tags do
not automatically start a client release build. It tests and packages macOS,
Windows and Android in a **single run**, started with an existing
`desktop-v<version>` tag. The tag must match `desktop/package.json`, and its
commit must already be merged into `main`.

It builds/signs/notarizes macOS arm64 and x64 on separate compatible runners
(Xcode 26+), builds an unsigned Windows x64 installer and a signed Android ARM64
APK in parallel, and verifies the complete artifact set before creating/updating
a **draft** GitHub Release. Linux packages
are not built. A published release is never overwritten. Publishing the verified
draft remains a manual action.

Each macOS job waits for Apple within the same invocation (90-minute processing
timeout, 120-minute job timeout). Only the final DMG is submitted. Slow Apple
processing can still cause a timeout: a pending or rejected result fails that
job and prevents Release assembly. Submission IDs/logs are retained as diagnostic
artifacts; the signed DMG is retained on failure. There is no periodic polling
workflow or separate manual finalize task. Keychains and raw signing credentials
are removed from the runner in an always-run cleanup step.

Configure these **repository Secrets**, never source files:

| Secret | Value |
| --- | --- |
| `MACOS_CERTIFICATE_P12_BASE64` | Base64 of the exported certificate + private key `.p12` |
| `MACOS_CERTIFICATE_PASSWORD` | Protection password chosen when exporting that `.p12` |
| `APPLE_API_PRIVATE_KEY` | Contents of the `.p8` API private key |
| `APPLE_API_KEY_ID` | API key ID |

Configure repository **Variables** `APPLE_TEAM_ID` and `APPLE_SIGNING_IDENTITY`.
For team API keys only, also set `MACOS_NOTARY_API_ISSUER`; omit it for personal
keys. An Apple account password/app-specific password is not used by this workflow.
No Windows signing secrets are needed. Android additionally requires the four
repository Secrets and existing release certificate fingerprint described in
[the mobile release guide](../mobile/README.md#github-actions与桌面端一起发布).
Android keeps its own version and versionCode; the shared release tag continues
to use the desktop version. Missing Android signing configuration or a failed
Android job prevents draft Release assembly.

The helper below configures the settings using GitHub CLI. Run it yourself in a
local terminal after installing `gh` and completing `gh auth login`. It prompts
for the `.p12` protection password with echo disabled, validates it locally, and
pipes private contents directly to `gh`; it does not print them or put the password
in command arguments/history. It does not ask for your Mac login password.

```sh
bash desktop/scripts/configure-macos-ci.sh \
  OWNER/REPO /absolute/path/certificate.p12 /absolute/path/ApiKey_KEYID.p8 \
  KEY_ID TEAM_ID 'Developer ID Application: Your Company (TEAM_ID)'
```

After merging the source/workflow into `main`, create and push the matching tag
(for example `desktop-v0.3.2` for version `0.3.2`). This does not start a build.
In GitHub, open **Actions > Client release > Run workflow**, select `main`,
enter the existing tag in the `tag` input, and run the workflow. Each
platform/architecture remains a separate download in the same draft Release,
accompanied by `SHA256SUMS` and macOS/Android verification reports. Android
filenames and release notes state its independent mobile version.

The renderer includes font, icon and dependency license notices; the source
snapshot retains the project's MIT license. Generated files, dependencies,
connection profiles and tokens must not be committed.

Expanded conversation sidebars can be resized by dragging their right edge.
Chat/composer widths follow in real time; double-click resets the width and
keyboard arrows/Home/End provide fine control. Width persists independently
from Web. The application and tray menus follow the in-app language immediately,
including native role items such as Copy, Quit and Full Screen.
