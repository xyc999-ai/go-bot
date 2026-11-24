# Third-party notices

Go Bot's own code is distributed under the [MIT License](LICENSE). The project
was extracted from LifeClaw's server-side native Host. The upstream material and bundled components below retain
their respective licenses and copyright notices.

## DeepSeek Harness

Project: https://github.com/deepseek-ai/deepseek-harness

The v4 format, behavior reference and small upstream fixture corpus are based on
the MIT-licensed DeepSeek Harness. Preserve this notice with upstream-derived
material. Compatibility fixtures use commit d743267388641bc76f17c45ce8b4c231aed1d32c;
the web tools and embedded fetch renderer use fcc0ad462172522aaf5c93a7b2e01f6b9f0b0159.

The embedded renderer includes Turndown 7.2.4, Joplin Turndown GFM 1.0.67
and Domino 2.2.0. The original notices are in [build/web-markdown/NOTICES](build/web-markdown/NOTICES).
The build recipe and package integrity hashes are tracked with the source.

The optional plugin process uses DeepSeek's MIT-licensed Cordis 4.1.0 and
Cordis Loader 1.1.0 for native dependency injection and plugin lifetimes. Go
implements the Harness core, tool stages, credentials, authorization and MCP;
the Node process only runs business plugin callbacks through the Host Bridge.
There is no runtime dependency on upstream Harness core service packages or a
second Node Agent/Session implementation. Exact dependencies, integrity hashes
and transitive license notices are retained in `extensions/cordis/pnpm-lock.yaml`
and the installed dependency tree in the Host image.

The local TypeScript SDK defines this project's asynchronous `ctx.host` contract.
Its compiler/type dependencies are locked in `extensions/sdk/pnpm-lock.yaml`.
TypeScript 7.0.2 is Apache-2.0; Node type declarations retain their MIT notice.
Go credential/authorization behavior and the internal tool phase audit reference
upstream commit ca33fd6843b205e279c7ccb6cb73a472c78f5760. Preserve the following
MIT notice with upstream-derived source and fixtures.

MIT License

Copyright (c) 2026 DeepSeek

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Skill Creator

The built-in `skill-creator` is adapted from OpenAI's
https://github.com/openai/skills at commit
`49f948faa9258a0c61caceaf225e179651397431`, directory
`skills/.system/skill-creator`, under Apache-2.0. Its original license is retained
in `internal/host/skills/skill-creator/LICENSE.txt`; `UPSTREAM.md` in the same
directory records the Go Bot modifications. The bundle is embedded in the Host
binary and initialized once at `/root/harness/.dsh/skills/skill-creator/`.
Existing user files, subsequent edits and deletion are preserved; Host upgrades
do not replace an initialized skill. `agents/openai.yaml` and its generator are
retained for Codex compatibility; Go Bot does not consume that configuration.

The Host supplies PyYAML 6.0.3 (MIT) for the bundled Python helpers. Its license
is retained in the installed distribution's metadata.

## OfficeCLI and image components

Official OfficeCLI: https://github.com/iOfficeAI/OfficeCLI (Apache-2.0).
The OfficeCLI plugin uses the official `npm install -g @officecli/officecli`.
It does not add a CLI version/checksum lock or disable upstream updates. Original
Skills and resources are exported by the official installer in a disposable build
stage and supplied unchanged with the wrapper. The upstream license ships at
`/usr/share/go-bot/plugins/officecli/UPSTREAM_LICENSE`.

Debian packages, Chromium, LibreOffice, fonts, Python/Node runtimes and installed
language libraries retain their respective upstream licenses in the image's
package metadata/distribution. Go libraries and versions are listed in go.mod.

`thirdparty/` itself is our integration code; it is unrelated to storing license
texts or vendoring actual third-party SDK source.

## Browser bundle

The Web Controller serves bundled JavaScript dependency notices at
`/assets/licenses.txt`. Vite generates the file from the installed packages,
including the ISC notice for Lucide icons.

The Web interface self-hosts Inter via `@fontsource-variable/inter` (SIL Open
Font License 1.1); its copyright and license are included in the generated
browser notices. File-format icons reuse vscode-icons (MIT), with the original
notice at `frontend/src/assets/file-icons/LICENSE`.

The agent-browser plugin installs the official CLI and contributes original Skill files from
https://github.com/vercel-labs/agent-browser (Apache-2.0). The image includes
the upstream license at `/usr/share/go-bot/plugins/agent-browser/UPSTREAM_LICENSE`.
The npm release and Skill source revision are recorded in `build/dependencies.lock.yaml`.

The plugin market uses the full-color Chrome mark from gilbarbara/logos at
commit d6960edc1b73 (CC0) for agent-browser, as configured in its plugin.json.
OfficeCLI uses the original iOfficeAI repository-owner avatar
(https://avatars.githubusercontent.com/u/145246968?v=4). Product names and
logos belong to their respective owners; these images identify integrations.
The image resources ship in the plugin packages under assets/ and are served by
the Controller at the image paths declared in plugin.json. Clients render those
URLs without bundled per-plugin icon mappings.

## X / xurl

The X plugin installs [xdevplatform/xurl](https://github.com/xdevplatform/xurl)
through its official npm package under MIT; its license is bundled with the
wrapper. The X App icon is from X's official App icon resource at
https://abs.twimg.com/responsive-web/client-web/icon-ios.77d25eba.png.
X trademarks remain owned by X Corp.

## Google Workspace CLI

The Google Workspace plugin installs [googleworkspace/cli](https://github.com/googleworkspace/cli)
through its official npm package. Original Skills/resources and the upstream
Apache-2.0 license are bundled. Its icon is the official `googleworkspace`
GitHub organization avatar, obtained from
<https://avatars.githubusercontent.com/u/3708359?v=4>.
Google names and marks belong to their owners. This CLI is not an officially
supported Google product.

## Gemini API CLI

The Gemini API CLI plugin installs Google's official
https://github.com/google-gemini/gemini-api-cli with its official install script.
The upstream Apache-2.0 license is included at
`/usr/share/go-bot/plugins/gemini-api/UPSTREAM_LICENSE`.
The wrapper uses `yaml` (ISC) for the upstream native configuration format.
Its icon is the official Google AI Studio mark from
https://www.gstatic.com/images/branding/productlogos/ai_studio/v1/web-512dp/logo_ai_studio_color_1x_web_512dp.png.
Google names and marks belong to Google.

## Mobile image masks

The mobile interface uses `@react-native-masked-view/masked-view` (MIT) to fade
scrolling image previews against the existing background. The package includes
its original MIT license: https://github.com/react-native-masked-view/masked-view.

MIT License

Copyright (c) 2015-present, Facebook, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Mobile clipboard

`expo-clipboard` 58.0.4 uses the MIT-licensed Expo modules from
https://github.com/expo/expo. Its original MIT notice is retained in the
installed package and mobile dependency tree. It provides the native copy
operation for Markdown table and code source.
