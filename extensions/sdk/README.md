# Go Bot plugin SDK

The SDK defines `ctx.host`, the asynchronous interface to the Go Host. Plugins
run as trusted JavaScript modules in the Host's shared Cordis process. Go owns
AgentLoop, Sessions, tool execution, permissions, sandboxing and persistence;
Cordis owns dependency injection and plugin lifetimes.

```ts
import type { Context } from '@go-bot/plugin-sdk'
export const inject = ['host']
export async function apply(ctx: Context) {
  await ctx.host.tools.register({
    name: 'ping', description: 'Contact an external integration.',
    inputSchema: { type: 'object', additionalProperties: false },
    async execute(args, call) {
      call.signal.throwIfAborted()
      return { content: [{ type: 'text', text: 'Hello' }] }
    },
  })
}
```

Declare `"goBot": { "api": "1" }` in package.json. Compile TypeScript to JavaScript
before installing the package. For TypeScript development, use the repository-local SDK dependency shown in the
example project. The Host ships this package and its installer pins
`@go-bot/plugin-sdk` to that local copy, including when a business package declares
it as a peer dependency. No published npm SDK is required. Type-only imports are
removed during compilation; runtime exports such as `HostError` also resolve to
the Host-supplied SDK. The SDK itself is not a market plugin. Declare Cordis as an optional peer (and a
development dependency when compiling types), rather than a production dependency.
The Host supplies the shared Cordis module; user profiles use `autoInstallPeers: false`.

Normal users install reviewed releases from the project's reviewed remote plugin
market. See [catalog maintenance](../catalog/README.md) for publishing a real
business plugin to that list. Development examples are not market products;
arbitrary source inspection and full settings application are Host-private.

All Go methods return Promises, including registration and queries. Await required
registrations during `apply`. Registrations are accepted individually; Cordis
withdraws their resources on disposal or initialization failure. This is not an
atomic batch activation or a rollback of external side effects.

Registration cancellation waits for Go to settle so a late accepted resource can
still be removed. Non-JSON values such as `Date`, `BigInt` or functions are
rejected instead of silently converted. Return explicit `null` for missing
records. Among callbacks invoked by Go, only authorization `run` may omit a
return value; ordinary Cordis `apply` functions retain their native semantics.

`ctx.host` exposes tools, Skills, appended prompts, credentials, authorization,
MCP, tool-bound approval requests and questions. See `index.d.ts` for signatures.
Tools return ordered text blocks and optional JSON data. Image attachments and
file presentation remain internal Go tools. There are no actions/storage APIs,
Agent/Session proxies, global guards, tool-stage middleware or cross-process next.

Use native Cordis `ctx.on` for the typed `host/*` state notifications. Their return
values do not affect execution. Native local business services and events retain
Cordis semantics. Plugin code can use its own libraries; the Go shell sandbox does
not isolate Node plugins from the container operating system.

## New external information

An enabled plugin can `await ctx.host.proactive.changed()` when it has new
external information. This records only a pending check and its time; it does
not force a turn, steer an Agent, forward the event body, or create a Schedule.
Keep the actual information in its normal source and make it available through
the plugin's tools or context contribution. Go's proactive service collects
current information when its quiet-window and minimum-interval conditions are
satisfied; an earlier AI timer remains independent. Installation alone does not
subscribe to third-party events automatically.

## Plugin display metadata

Ship plugin.json with the package. It declares name (original product name),
bilingual description (`en`, `zh-CN`), icon, version, maintainer and license.
The independent plugin release workflow reads this file and checks version/license against
package.json. Clients render those fields rather than maintaining capability
lists or product copy in components. Host package inspection supplies actual
installed versions; Controller reports availableVersion only for a newer reviewed
SemVer release. Updating reuses the package installation pipeline and preserves
the enabled flag.

## Package installation and activation

Users select one version and enabled flag per reviewed catalog package. Redis
stores that selection only. Go Host serializes operations and waits for pnpm to
install each package and its dependencies. Custom dependency setup belongs in
standard package.json lifecycle scripts, for example:

```json
{"scripts":{"postinstall":"node ./scripts/install.mjs"},"goBot":{"api":"1"}}
```

The reviewed catalog's private builds list grants exact pnpm allowBuilds entries
for that package or explicitly versioned dependency scripts. Public clients
cannot grant script permissions. Unapproved required builds fail installation;
inspection always disables scripts. pnpmfile hooks remain disabled. Side-effect
caching is disabled because first-party CLI installers change global locations.

A package's postinstall must be idempotent and verify its dependencies before
exiting successfully. Go waits for the same pnpm operation; there is no custom
goBot.install API or second installer callback. Native scripts run as trusted
container processes and are cancelled with the package operation. They must not
wait for credential prompts; service authorization happens after activation.

Cordis apply only checks local prerequisites and registers capabilities. Disposal
withdraws registrations without uninstalling software. Uninstall withdraws the
Cordis entry and removes the wrapper with pnpm; credentials, workspaces and
shared/global CLI dependencies are retained. See the first-party packages in the [plugin repository](https://github.com/xyc999-ai/got-bot-plugins).

## Authorization interactions

A plugin registers only the authorization methods its integration implements.
The built-in clients show those methods in that plugin's detail page; they do
not infer OAuth support or display a fixed API-key form for every plugin.
After the user chooses a method, the plugin's `run(session)` requests any inputs
with `await session.prompt(...)` (`text`, `secret` or `select`). The answer
resumes that Promise. Validate it with the service before `session.commit(...)`;
ordinary flows complete only after a committed credential. Native CLI integrations
can instead register `inspect({signal})` and `disconnect({signal})`: after `run`,
Go requires `inspect` to report `{configured:true,accountLabel?}`. Do not create a
fake grant to satisfy completion. `disconnect` performs the CLI logout; Go checks
that `configured` is false. Both callbacks return Promises and stay in the plugin. These inputs are not chat
messages and are not added to model context. Closing the client panel preserves
an active attempt; cancelling explicitly withdraws it. Attempts are transient
Host state and do not survive a Host restart.

Business OAuth implementations can request a controlled browser callback:

```ts
const callback = await session.callback({ state })
// Use callback.url as the provider redirect_uri and session.notify({ message,
// url }) to show the provider's authorization URL.
const query = await callback.wait()
```

Generate `state` and the PKCE verifier with the provider's library. The Host
matches state and accepts a callback once; the plugin remains responsible for
issuer and provider-error validation, PKCE/token exchange, scope checks, refresh
and credential format. Receiving a callback does not save a token or mark the
attempt authorized. Do not put tokens or secrets in notices. Browser callbacks
require HTTPS or localhost HTTP. MCP wrappers use Go's existing MCP OAuth client
rather than implementing this provider flow again.

X and Google Workspace collect each user's own developer application configuration
during authorization. Google requires a downloaded Desktop OAuth client JSON on
first connection and reuses it from the native gws directory thereafter. A text/secret
prompt may set `webInput: "json-file"`: Web offers a JSON file chooser, parses the
file locally and sends the JSON as the same prompt answer; mobile retains the
normal text/secret input. The file is not stored as a chat attachment. The
[connection guide](../../docs/plugin-account-setup.md) explains both. Account tokens
remain in the native CLI store. `ctx.host.info().instanceId` identifies the caller's
existing Cordis instance when filtering Host state notifications; it does not add
another plugin lifecycle manager.

Plugin display metadata can include `tutorials`, an array of
`{language: "en" | "zh-CN", title, video, poster?}`. Media paths refer to backend
plugin assets, for example `/assets/plugins/x/account-setup.zh-CN.mp4`. Only
publish a tutorial entry once its video exists and has been reviewed for exposed
credentials. Web and mobile choose the local interface language, then English,
then the first supplied tutorial. The clients do not hardcode provider media.
