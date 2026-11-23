# Extending capabilities

One module, three boundaries:

1. `runtime.Driver`, `web.SearchProvider` and agent model contracts describe the
   capability the application needs.
2. `providers/` implements policies and adapts those contracts.
3. `thirdparty/` is our SDK/API integration code. Actual libraries use Go modules.

## Runtime

Add `providers/runtime/<name>`. Implement Validate, Preflight, Ensure, Inspect,
Stop and Delete, and declare supported storage types. Register its factory in
`internal/controller.Driver`, add strict config fields and credential validation,
and test without provisioning cloud resources.

Ensure must be idempotent for the persisted Host ID (`Spec.RunID`). On uncertain creation,
query platform resources or reuse the same idempotency token; do not blindly
create a second computer. Instances carry RunID and DeploymentID. The Controller
uses the account name as DeploymentID.
Return an endpoint reachable from the controller, then let the controller verify
HTTP readiness and identity. Cloud Running is not application readiness.

Stop and Delete have distinct data semantics. NAS is a storage option, not a
runtime type. SDK-specific DTOs stay behind thirdparty wrappers. Do not add Redis
or cross-Host session execution to a driver.

Declare `RestartStopped` and `NativeStop` capabilities. Docker supports both;
ECI supports neither. For ECI, the Controller attempts authenticated Host
shutdown, then deletes the instance and waits for the runtime to confirm release.

For providers with a separate image preparation step, add a command under
`cmd/build<provider>`. Keep cloud cache preparation separate from project image publication.
The runtime driver consumes the prepared artifact through its own configuration.

## Language models

Model settings supports **OpenAI Responses** (`responses`) and **Anthropic
Messages** (`anthropic_messages`). Choose the API protocol in the endpoint form's
dropdown, then enter its base URL, API key and ordered model IDs. The first model
is the endpoint default. The account default endpoint applies to new chats;
existing chats keep their selection and can switch endpoints/models independently.
A switch during generation applies to the next turn.

For Anthropic, use `https://api.anthropic.com/v1` or your provider's compatible
base URL; do not append `/messages`. The Host appends `/messages`, sends
`x-api-key` and `anthropic-version: 2023-06-01`, and normalizes text, tool use,
images and SSE into the existing Harness contracts. Model IDs are entered as
provided by the service; a Claude model name alone does not determine its API
format. The adapter targets API-key-based Messages endpoints, not Bedrock,
Vertex or Claude subscription login.

Keys stay in the account's Redis `llm` field and are synchronized to the Host.
Public settings return `hasApiKey`, not the key. Saving validates configuration
and applies it to the Host without issuing a billable model request. Missing
protocol on existing endpoints defaults to Responses. Chat Completions remains
an internal adapter and is not offered by this settings form.

Extend `thirdparty/llm` for wire encoding, decoding and streaming, and
`providers/model/mainmodel` for model routing. Keep credentials out of Session
logs. Signed native Anthropic blocks are stored as replay metadata on assistant
messages, separate from visible content, and only replayed to the same endpoint
address and model. Cross-protocol history uses portable content; compaction stays
in the Harness. General model context defaults do not guarantee that a custom
model accepts that context length. [Anthropic Messages reference](https://platform.claude.com/docs/en/api/messages/create?version=2023-06-01)

## Search

Add `providers/web/<engine>` implementing web.SearchProvider and use thirdparty
for external calls. Register the factory in `providers/web/registry.go`. The
Host uses that registry to resolve the provider.

| ID | Service | Key |
| --- | --- | --- |
| `deepseek` (default) | DeepSeek Search | DeepSeek search key |
| `zhipu` | BigModel Web Search API, using `search_std` | BigModel platform key authorized for Web Search. [Official docs](https://docs.bigmodel.cn/cn/guide/tools/web-search) |
| `doubao` | Doubao Search Custom, text-to-web | Dedicated Doubao Search Custom key. [Official docs](https://docs.volcengine.com/docs/networked-search/networked-search?lang=zh) |
| `tavily` | Tavily Search, Basic depth | Tavily API key from [Tavily dashboard](https://app.tavily.com/). [Official API](https://docs.tavily.com/documentation/api-reference/endpoint/search) |
| `brave` | Brave Search, LLM Context endpoint | Search API key with LLM Context access from [Brave dashboard](https://api-dashboard.search.brave.com/). [Official API](https://api-dashboard.search.brave.com/documentation/services/llm-context) |

In **Settings → Web search**, set up a service, save its API key, then choose
**Use provider**. Mobile uses **Me → Web search**, with **Save settings** and
**Save and use**. Saving does not make a search request. The same account catalog
supplies both clients; no extra environment variables or model names are needed.
The default provider remains DeepSeek until another saved provider is selected.

Tavily calls `POST https://api.tavily.com/search` with Bearer authentication,
`search_depth: basic`, `topic: general`, publication dates enabled and at most
20 results. Automatic parameter selection, generated answers and raw page content
are disabled. Relevant `content` passages become source summaries. Publication
dates are provider estimates and may be absent. HTTP 432/433 map to quota errors;
434 maps to temporary rate/capacity limiting.

Brave calls `POST https://api.search.brave.com/res/v1/llm/context` with
`X-Subscription-Token`. It returns source-linked excerpts rather than a generated
answer. The adapter defaults to an 8,192-token context budget and a 1,024-token
per-URL budget, with URL/result counts capped at 50. These are internal defaults,
not extra user settings. Queries must fit 600 characters and 75 words. Source
metadata supplies optional titles, descriptions and absolute dates; relative
ages and content-fetch timestamps are not used as publication dates. The API's
default country/language preferences apply; the adapter does not infer a locale
from UI language or rewrite the Agent's query.

Search and extraction remain distinct:
search-only providers are not forced to fetch pages. Results use URL/title,
snippet, summary and optional publication time. Preserve empty results and unknown
fields honestly. Honor context, bound responses, reject credential redirects and
test 401/429, cancellation, malformed responses and unknown providers.

Each provider receives the account search key. The factory
returns a provider implementing `web.SearchProvider`. `harness/tools` owns
query merging, rendered text and replay metadata for every backend. See the [web tool parity record](web-tools-parity-plan.zh-CN.md).

## Models and tools

Accounts configure endpoints and choose a model per session. A new protocol adapter must
test streaming, tool-call arguments, usage, cancellation, provider errors and
uncertain outcome handling. Read-only model/search clients belong to Host, not
controller. Preserve durable v4 facts and tool-intent checkpoints.

## Image generation

The default Host currently does not register `generateimage`, and Web/mobile
hide its settings entry. Use the **Gemini API CLI** plugin for image generation;
see [connection setup](plugin-account-setup.md#gemini-api-cli). `readimage`, image
attachments and `present` remain active. The adapters, tool, settings code and
saved account settings below are retained for future integration. This section
documents those retained implementations, rather than the current UI.

| ID | Service | Suggested model |
| --- | --- | --- |
| `seedream` (default) | Volcengine Ark Images API | `doubao-seedream-5-0-260128` |
| `openai` | OpenAI Images API with an OpenAI API key. [Official API reference](https://developers.openai.com/api/reference/resources/images/methods/generate) | `gpt-image-2` |
| `nanobanana` | Gemini Developer API with a Google AI Studio API key. [Official API reference](https://ai.google.dev/api/generate-content) | `gemini-3.1-flash-image` |

The model field is editable. `generateimage` accepts a text prompt and an
optional `reference_image_ids` array. Upload reference images in chat, or
ask the agent to reuse image attachments from earlier replies. The agent can
also turn a workspace image into a reference with `readimage`. A request
can include up to 10 normalized attachments totaling 12 MiB and returns one
image through the attachment store. For image-to-image, use a model that
accepts image inputs.

Seedream sends references as data URLs in the Ark request. OpenAI switches
to the Images edits endpoint. Nano Banana sends inline image parts alongside
the text prompt. [Ark image generation API](https://docs.volcengine.com/docs/ark/image-generation-api?lang=zh) · [OpenAI image edits](https://developers.openai.com/api/reference/resources/images/methods/edit) · [Gemini image generation](https://ai.google.dev/gemini-api/docs/generate-content/image-generation)

Add a package under `providers/image/<name>` and register its factory and
descriptor in `providers/image/registry.go`. The descriptor supplies the
settings label and suggested model ID. The adapter receives an account's
selected model and API key, and returns one image plus its MIME type. The Host
uses the account's selection for subsequent `generateimage` calls. The tool
stores the image through the shared attachment store and returns an image block.

Keep request timeouts, response size limits and error mapping inside the
adapter. Test the provider with a local HTTP fixture, including invalid
credentials, throttling, cancellation and malformed image data. Avoid
retrying generation when the upstream outcome is unknown.

The Controller stores search and image settings in the `webSearch` and
`imageGeneration` JSON fields of the Redis Hash `user:<userId>`. Each field
contains the active `provider` ID and an `entries` map with keys and, for
images, models by provider ID. Use
**Save settings** to store or update credentials and **Use provider** to switch
the active service. The Host obtains the current selection at startup and on
changes to the active provider. Public settings return credential presence
only. Saving a setting does not call the upstream API or incur provider charges.
