# Connect plugin services

X uses each user's own developer application, entered inside the plugin's
Accounts and authorization panel. Google Workspace also uses each user's own
application: create a Desktop OAuth client and provide its downloaded JSON on
first connection. Account
tokens for both services stay in each user's native CLI store, never in Redis or
Go credentials. Do not put user credentials in chat, deployment `.env` or Git.

## Gemini API CLI

1. Create your own Gemini API key in [Google AI Studio](https://aistudio.google.com/apikey).
2. Install and enable **Gemini API CLI** in the plugin market.
3. Open **Accounts and authorization → Configure API Key**. Enter the key in the
   protected field. A live `models list` request checks access without generating content.
4. Return to chat. The Agent uses the official CLI and existing image/file delivery.

Use **Update API Key** to replace a key, or **Remove API Key** to clear it from
this Host. A failed or cancelled replacement preserves the previous key.
Only API-key authentication is offered; no OAuth application or callback is needed.
Google controls available models, regional access, quotas and billing.

The CLI reads `/root/.config/gemini-api/config.yaml` directly. No symlink or
additional mount is created. Configuration remains across a stop/start of the
same container, but is outside `/root/harness` and needs to be entered again
after the Host container is replaced. Redis and chat history do not store it.

### Gemini API CLI：中文配置指引

在 [Google AI Studio](https://aistudio.google.com/apikey) 创建自己的 API Key，
安装并启用插件后，打开「账号与授权 → 配置 API Key」输入密钥。保存前通过在线
`models list` 检查访问，不生成内容。修改用「更新 API Key」，清除用「移除 API Key」；
修改失败或取消会保留原密钥。仅支持 API Key，不需要 OAuth 应用或回跳地址。

配置直接位于 `/root/.config/gemini-api/config.yaml`，不做映射，不写入 Redis 或聊天记录。
同一容器停止再启动后保留；删除并重建 Host 容器后需重新填写。模型、配额、地区和费用由 Google 管理。

## X

Your X Developer App supplies the Client ID, permissions and billing identity.
Go Bot, through xurl, is the software client performing authorization; you do not
need Cursor or Grok. Their callback addresses in the X docs are examples for
those products, not addresses to use with Go Bot.

1. Open the [X Developer Console](https://developer.x.com/), create an app and
   enable OAuth 2.0 user authentication. Select the app permissions you need.
   Use **Native App (public client)** for the default connection flow.
2. Register `http://localhost:8082/callback` exactly as a callback URL. Obtain your
   **Client ID**. A Native App is a public client and does not need a Secret.
   If you use a **Web App / confidential client**, obtain its **Client Secret**
   as well; this application type requires it.
   **Website URL** is the application's homepage, for example
   `https://github.com/xyc999-ai/go-bot`. It introduces the app; it is not the
   Controller address or OAuth callback and does not need another deployed server.
3. Install and enable X in the plugin market. Open **Accounts and authorization** and
   use the authorization-method selector. Keep the default **Native App (no Client
   Secret)** and authorize: first connection asks only for the Client ID. For a
   confidential application, select **Web App / confidential client** instead;
   only that method asks for a Client Secret. Use the method matching your saved
   application when authorizing again.
4. Open the authorization link in your own browser, sign in to X and approve
   access. Desktop and Mobile receive the callback automatically after **Continue in browser**.
   On Web, copy the **complete redirect URL** from the address bar into the panel,
   even if its localhost page cannot open. Submit it immediately: X documents a
   **30-second authorization-code lifetime**. An expired/used code requires a new
   authorization attempt.
5. Wait for the native authorization result. Subsequent connections reuse the
   native application configuration, and xurl refreshes account tokens.

If an attempt fails or is cancelled, start authorization again with the same
application type. No Host restart is needed. Open the new authorization link and
submit that attempt's new redirect URL; do not reuse an earlier URL or
authorization code.

X API usage is billed to the developer application/account. Connecting your own
X account does not make API calls free. Review your developer account's credits,
API access and spending limit. The MCP bridge only exposes the tools supported
by the current X API and your app's scopes; it does not promise every xurl action.

[X OAuth client types](https://docs.x.com/fundamentals/authentication/oauth-2-0/authorization-code)
· [xurl authentication](https://github.com/xdevplatform/xurl#authentication)
· [X MCP](https://docs.x.com/tools/mcp)
· [X API pricing](https://docs.x.com/x-api/getting-started/pricing)

## Google Workspace

Personal Google accounts can connect Gmail, Drive, Calendar, Docs, Sheets and
Slides; a paid Workspace enterprise subscription is not required for these
features. Enterprise directory and administrator features are outside the
plugin's default permission request.

1. Follow [Google application setup](google-workspace-app-setup.md) to create your
   Cloud project, enable the APIs you need, configure the audience and download
   a **Desktop app** OAuth client JSON. While testing, add your Google email as
   a test user. This is not the Go Bot account name.
2. Install and enable Google Workspace. Open **Accounts and authorization → Connect
   Google account**. On Web, select the downloaded JSON file; it is parsed and submitted automatically.
   On mobile, paste the JSON in the protected input. It is saved
   only in your native Host directory; later connections reuse it.
3. Choose read-only or read-and-write permissions. Open the link in your own
   browser, sign in to Google and approve access.
4. Desktop and Mobile automatically receive the callback after **Continue in browser**.
   On Web, copy the complete localhost redirect URL from your browser address bar into
   the panel, even if that local page cannot open.

There is no shared project OAuth application. The plugin runs native
`gws auth login --scopes ...` and relays the redirect only to that command's
current local listener. gws exchanges the code, saves the account tokens and
refreshes them. This is not an upstream `--headless` flag and does not use Google's
deprecated OOB redirect URI. Enable APIs as needed; authorization does not enable
APIs or bypass app audience restrictions.

Standard API usage is generally available at no additional charge within the
published quotas. Google controls app verification, organization access, quotas
and billing changes for high usage. The CLI is not an officially supported Google
product. See [Google application setup](google-workspace-app-setup.md).

[gws authentication](https://github.com/googleworkspace/cli#authentication)
· [Drive quotas/pricing](https://developers.google.com/workspace/drive/api/guides/limits)

## Account data and disconnect

```text
/root/harness/.dsh/credentials/x/                 # ~/.xurl -> this directory
/root/harness/.dsh/credentials/google-workspace/  # ~/.config/gws -> this directory
```

These complete native directories, including the gws encryption key and cache
where used, live with the user's existing Host data. Go credentials and Redis do
not receive another token copy, account flag or file-reference grant.

Disable/uninstall retains account data. **Disconnect account** invokes native
logout/clear and checks that the active account is no longer configured. It is
different from cancelling an unfinished authorization. This is local disconnect;
users may also revoke provider access from their X/Google account settings.

Retrieved service content enters the Agent context and may be processed by the
model provider selected by the user. Application credentials, callback answers and
tokens collected in the authorization panel are not added to chat/model context.

## Native callback retry

Desktop and Mobile listen only on the current login link's loopback port, for up
to five minutes. Closing the panel stops local reception. If authorization times
out, the port is busy, access is denied, or callback submission fails, select
**Restart authorization** to cancel the old CLI and create a fresh login link.
Do not reuse an old code. Existing saved accounts are retained on cancellation.
A native app build with the receiver is required; Web and Expo Go retain manual submission. Update the Host and clients together
with these plugin versions. Existing X apps must register `http://localhost:8082/callback`.
