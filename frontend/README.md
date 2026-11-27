# go-bot browser test client

Follow the [local Quickstart](../README.md#quickstart) from the repository root and open
[http://127.0.0.1:8080/](http://127.0.0.1:8080/). The Controller serves the
page and relays API calls and SSE to the selected account's Docker or ECI Host.
Create or open an account by name. Configure OpenAI Responses or Anthropic Messages endpoints using the protocol dropdown in Model settings, and use the model picker in the chat header to change its model. Under **Settings → Web search**, save a key for DeepSeek, Zhipu, Doubao, Tavily, Brave Search, or Firecrawl and choose **Use provider**.

For a custom mobile, desktop or web interface, use the Controller's
[HTTP and SSE API](../docs/api.md). The [minimal example](../examples/frontend-minimal/index.html)
shows how to start a Host, create a session, send a message and read the native stream.

Use **New chat** to create a session. Right-click a row in **Chats** to delete
it from the list. The Host archives the session and retains its data on disk.
On touch screens, the row menu offers the same action.

The narrow left rail switches between **Chats**, **Tasks**, **Plugin market**,
and **Settings**. Click **Chats** to expand its conversation list. Open the
account menu at the bottom for **Stop Host**. Use **File system** to browse the account's Host data at
`/root/harness`. Expand directories to see their contents, select an entry to
copy its absolute path, or refresh the selected directory. The panel keeps
its position when you switch chats.

Choose English, Simplified or Traditional Chinese, Spanish, Japanese, Brazilian
Portuguese, French, German or Korean on the account page or in the workspace header.
The preference is saved per browser. When no preference is saved, the app uses
the first supported browser language, with English as the fallback. Messages
and Agent responses keep their original language.

You can send more messages while a reply is generating. They appear above the
composer. **Steer** adds that message to the current reply's next Agent step.
**Delete** removes just that queued message. Questions from the Agent appear
above the composer and can be answered there. One Host SSE stays connected
while switching chats or opening settings. A pending question or approval in
another chat adds a chat-list indicator; open that chat to respond. Execution
approvals have no fixed timeout and reappear after reconnecting.

New assistant text appears progressively on Web and mobile. Playback follows
the received text continuously across stream chunks and the committed message,
without a typing cursor. History opens immediately; reconnecting, returning from
the background, or enabling the system's reduced-motion preference shows the
current received text directly. Playback affects display only: tools, questions,
approvals and turn completion keep their existing backend behavior.

Use the plus button to choose attachments, or paste files when your browser
provides them in the clipboard. The composer keeps them in the draft until
you send the message. You can remove individual attachments before sending.

## Local development

Use Node.js 22.12+ and the existing Controller configuration. A native
Controller also needs a Redis instance, selected with `REDIS_ADDR` and
defaulting to `127.0.0.1:6379`. Run these commands from the repository root
in separate terminals.

```sh
go run ./cmd/controller serve --env-file .env --web-dev-origin http://127.0.0.1:5173
npm --prefix frontend ci
npm --prefix frontend run dev
```

Open [http://127.0.0.1:5173/](http://127.0.0.1:5173/). Vite proxies `/v1`
to Controller on port 8080 and preserves the browser-facing Host, so local OAuth
callbacks return through the same `/v1` proxy. Set the exact loopback origin in
`--web-dev-origin` when changing the Vite port. The production UI uses the
Controller's own origin.

```sh
npm --prefix frontend test
make build-web
```

`make build-web` embeds the production bundle into `bin/controller`.
A plain Go build keeps the API-only Controller available. The Host image
contains the compiled Host and agent tools. If using ECI, build the image cache from
the same version so the Host supports cold history and archiving.

The interface uses the project's whale logo and [Lucide](https://lucide.dev/) icons.

The upper-right theme button switches between light and dark appearances. Theme
and language preferences stay in this browser, independently of the account.
Tasks reads the Host schedule catalog: paused jobs remain Upcoming; only ended
jobs appear under Completed. The detail view shows the next run, schedule rule,
last delivery and associated conversation. Create or change tasks through your
Agent or the existing API.

Visual architecture and validation: [Lumeo redesign plan](../docs/web-lumeo-redesign-plan.zh-CN.md).

When the conversation sidebar is expanded on a desktop-size window, drag its
right edge to resize it. The chat and composer reflow with the available space.
Double-click the edge to reset, or focus it and use arrow keys / Home / End.
The browser remembers the width, clamped to leave room for chat and Host files;
the narrow-screen drawer keeps its fixed overlay layout.
