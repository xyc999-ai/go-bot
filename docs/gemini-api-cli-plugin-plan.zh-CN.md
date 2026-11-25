# Gemini API CLI 插件接入方案

> 分发更新：业务源码、资源与发布已迁至独立的 [got-bot-plugins](https://github.com/xyc999-ai/got-bot-plugins)；本文原镜像内 bundled 分发章节由 [远程市场方案](remote-plugin-market-plan.zh-CN.md) 替代，授权与业务行为保持不变。

状态：已实现，2026-11-21 完成本地 Docker、真实 Web 和 Android 模拟器的安装与 API Key 配置验收。生图实调受 Google 项目计费前提限制，详见末尾验收记录。

## 1. 接入目标与边界

将 Google 的 [Gemini API CLI](https://aistudio.google.com/docs/cli) 包装为 Go Bot 插件，使用用户自己的 API Key，为现有 Agent 增加多模态生成、文件理解和远端研究能力。

本方案接入的是命令为 `gemini-api` 的 [google-gemini/gemini-api-cli](https://github.com/google-gemini/gemini-api-cli)。它与交互式编码 Agent `@google/gemini-cli` 是两个项目。

确定如下：

- 展示名称：`Gemini API CLI`；插件 ID：`gemini-api`；包名：`@go-bot/plugin-gemini-api`；首个包装包版本：`0.1.0`。
- 通过现有插件市场安装、更新、启用、停用、卸载，沿用 pnpm 包安装与 Cordis 生命周期。
- 只提供 API Key 连接方式，不提供 OAuth、Access Token 输入或 Google Workspace 授权复用。
- 业务入口为官方 CLI，由现有 Shell 工具调用；不新增一套 Gemini 工具集合或 MCP Server。
- Go 继续管理 AgentLoop、Session、工具执行、权限、审批、文件交付和前端投影。
- 不新增 `.env` 字段，不改变 LLM、Web search 设置，不将本插件设为默认模型服务。
- 按最新产品决定，Host 不再注册内置 `generateimage`，Web/mobile 隐藏原生图设置入口；工具、provider、设置组件、服务端设置处理和已存数据保留在代码及原存储中，供未来重新接入。保留 readimage、图片附件和 present。

## 2. 能力范围

以下为上游 CLI 已公开的命令分组；实际可用模型与能力取决于用户项目、地区、权限、配额和服务状态。插件不承诺填写 Key 后所有模型均可使用。

| 能力 | 官方入口 | 接入 Go Bot 的方式 |
| --- | --- | --- |
| 文本生成、辅助推理 | `generate` | Shell 调用并读取文本结果 |
| 文生图、参考图编辑 | `image` | 输出工作区文件，再按需调用现有 readimage / present |
| 语音合成、音乐、视频 | `tts`、`music`、`video` | 输出工作区文件，通过现有文件交付展示 |
| 文档与多模态理解、转录 | `analyze`、`transcribe` | 传入实际文件，读取分析或转录结果 |
| Google 托管研究任务 | `agent run/status/cancel` | 保存返回的 interaction ID，按官方接口查询或取消 |
| 文件上传、模型发现 | `files`、`models` | Agent 按任务需要调用 |

命令参数、模型名和输出选项以已安装 CLI 的 `--help`、`--usage`、`--schema` 为准。插件不在前端维护另一份模型列表，也不把模型名称固定到系统提示中。

依据：[官方 CLI 使用说明](https://aistudio.google.com/docs/cli)、[上游项目介绍及 Agent 指引](https://github.com/google-gemini/gemini-api-cli#readme)。

## 3. 源码与发布资源

沿用当前仓库结构：

```text
extensions/plugins/gemini-api/
  package.json
  plugin.json
  src/
    index.mjs              # Cordis apply；授权及提示注册
    native-cli.mjs         # 原生配置、私有写入、子进程与错误处理
    upstream-instructions.md # 官方 README 的 Agent 指引原文
  scripts/
    install.mjs            # pnpm postinstall 调用官方安装方式
  assets/
    icon.png
  LICENSE
  UPSTREAM_LICENSE
  THIRD_PARTY_NOTICES.md
```

`plugin.json` 声明原始产品名、英文/简体中文 description、icon 后端资源路径、版本、维护者、包装包许可证。包版本及许可证与 `package.json` 一致；上游许可证另行保留。

图标使用从 Google 官方品牌或项目资源确认可使用的 Gemini 图标，并记录来源；使用 `/assets/plugins/gemini-api/icon.png` 分发，前端不画供应商专属 SVG。

`extensions/catalog/plugins.json` 增加 bundled 条目，允许该包装包的标准安装脚本。Host 镜像复制包装包到 `/usr/share/go-bot/plugins/gemini-api/`，不在镜像构建时安装业务 CLI，不包含任何 Key。

没有已完成的教程视频时不填写 `tutorials` 占位条目；详情中的双语文字指引和官方链接即可完成初次配置。

## 4. 安装、启用与更新

### 4.1 安装

```text
用户点击安装
  → Controller / Host 执行现有插件包安装操作
  → pnpm 安装包装包及依赖
  → 经审核放行的 postinstall
  → 下载并运行 Google 官方安装脚本
  → 验证 gemini-api --version 与 --help
  → 本次安装成功或返回具体错误
```

安装阶段使用官方方式：

```sh
curl -fsSL https://raw.githubusercontent.com/google-gemini/gemini-api-cli/main/scripts/install.sh | bash
```

实际脚本先检查下载成功，再运行 Bash，并将整个子进程纳入现有安装操作的取消处理。安装不读取用户 Key，也不发起收费生成请求。

官方安装脚本会识别 Linux amd64 / arm64；正常 Host 内以 root 安装到默认 `/usr/local/bin/gemini-api`，已有 PATH 即可访问。无需 Host 预装 Go 编译工具链、npm 全局 Gemini 包或额外 launcher。

与当前采用官方默认安装方式的 CLI 插件一致，本方案安装时取官方默认最新 release；包装包自身仍按市场版本管理。上游为 beta，官方建议自动化环境固定 release，因此要明确：同一包装包在不同日期重装可能得到不同 CLI 版本。验收记录实际 `--version`；如后续出现破坏性变更，再在包装包内部选择已验证 release，不增加普通用户版本表单。

依据：[官方安装脚本](https://github.com/google-gemini/gemini-api-cli/blob/main/scripts/install.sh)、[官方安装与 beta 提示](https://aistudio.google.com/docs/cli#install)。

### 4.2 启用与停用

`apply()` 只检查本地命令并注册授权入口及 Agent 指引，不下载软件、不等待用户输入、不请求远端服务。配置直接使用 CLI 的默认目录，不建立目录映射。

未填写 Key 也可以安装并启用，详情显示「未配置」。Host 和其他能力继续正常使用。

停用由 Cordis 撤销本插件注册的提示及授权入口，不卸载 CLI、不删除 Key。与当前插件机制一致，停用不是操作系统层面的命令封禁；已经运行的命令不会因撤销提示而自动取消。

### 4.3 更新与卸载

- 可更新状态沿用 Controller 对市场包装包版本与实际已安装版本的比较，不用上游 CLI 版本冒充插件版本。
- 用户更新时复用安装流程，重新执行官方安装，验证后恢复原启用状态；保留原生配置及用户产物。
- `apply()`、打开详情、Host 重启均不自动升级 CLI。
- 卸载沿用现有语义：移除包装包和注册项，保留全局 CLI、原生凭证及文件；用户希望清除 Key 时先点击「移除 API Key」。
- 安装请求等待成功或失败，安装状态只影响相应插件操作；Web/mobile 仍可离开页面、切换会话和发送消息，不增加全屏操作遮罩。

## 5. API Key 授权与存储

### 5.1 复用现有原生 CLI 授权模式

本插件使用现有 `ctx.host.authorization.register()` 的 `run / inspect / disconnect` 模式，与 X、Google Workspace 的原生存储适配一致。

只注册一个 method：`api-key`。用户在现有授权面板输入 Key，表单通过 `session.prompt({kind: 'secret', ...})` 驱动。该输入不进入聊天消息和模型上下文。

不使用 `session.commit()` 再保存一份 Go CredentialRecord；授权结束由现有框架调用 `inspect()` 确认原生配置是否存在。原因是 Agent 将直接执行官方 CLI，CLI 必须能从自己的配置读取凭证；本方案选择原生配置作为唯一密钥来源，避免两份数据同步。

这不新增授权框架，也不更改其他插件使用 `ctx.host.credentials` 的方式。

### 5.2 数据位置

保持 `HOME=/root`，直接使用官方默认位置：

```text
/root/.config/gemini-api/config.yaml
```

插件授权与 Agent 执行 CLI 都读写这一份默认配置，不创建软链接，不复制到 `.dsh/credentials`，也不增加 Docker/NAS 挂载。当前工作区挂载 `/root/harness` 不包含这个目录：同一容器 stop/start 时文件保留，删除并重新创建容器后不能依靠工作区卷或 NAS 恢复这份 Key，需要重新配置。

拟保存的原生配置结构：

```yaml
version: 1
security:
  api_key: "用户提供的值"
no_keyring: "true"
```

这是上游支持的 YAML 配置，`no_keyring` 在当前源码中为字符串字段。插件通过包管理引入 YAML 库读取、更新配置；不手工拼接用户输入。目录权限 0700、文件权限 0600，通过临时文件替换保存；使用容器文件存储，不依赖桌面系统 Keychain。

读取并保留原生配置中的其他设置，只更新本插件管理的 API Key 及文件存储选项。配置损坏时明确报错，不删除用户数据来制造成功状态。

Redis 仍只保存用户已选插件的 `catalogId/version/enabled`，不新增 Key、整份 CLI 配置或常驻授权任务。

依据：[官方配置结构及读取优先级](https://github.com/google-gemini/gemini-api-cli/blob/main/internal/config/config.go)、[官方 Keychain 与文件存储方式](https://aistudio.google.com/docs/cli#authentication)。

### 5.3 保存与验证流程

```text
点击「配置 API Key」
  → 输入 Key
  → 检查非空、无换行等输入错误
  → 仅为验证子进程设置 GEMINI_API_KEY
  → 用官方 models 命令执行轻量认证检查
  → 检查成功后写入原生配置
  → inspect 确认已配置
  → 授权面板显示完成
```

验证调用只读模型列表，不生成图片或文本；不消耗生成 tokens。实施时确认所安装版本的 JSON 输出和退出码，并用失败响应证明无效 Key 不会误判为成功。`whoami` 和 `--dry-run` 只能用于本地配置或命令检查，不能作为远端验证成功的证据。

验证失败或用户取消时，保留此前可用 Key。401/认证失败、403/权限问题、429/限流、网络错误分别显示可理解的原因，不直接回显含密钥的原生错误对象或完整子进程参数。

验证子进程通过独立 env 接收 Key，不更改 `process.env` 或 Host 主进程环境，不将 Key 放入 Shell 命令、进程参数、通知或日志。排除继承的 Gemini Token 等冲突认证变量，确保本次检查使用用户刚输入的 Key。成功配置 API Key 时清除原生文件中先前的 access_token，明确切换到当前插件支持的认证方式；其他原生偏好保留。

普通命令后续从原生配置直接读取 Key，无需启动时设置环境变量。CLI 本身仍遵循官方 flags/env/config 优先级；本项目不提供第二个全局 Gemini Key 配置入口，避免面板所见与实际使用的 Key 不一致。

`inspect()` 只检查本地已配置状态，不在每次打开详情或恢复 Host 时请求 Google；因此 UI 用「已配置」，不用「已验证所有服务」或「Google 账号已登录」。Key 后续失效由真实调用报错暴露，用户可以重新配置。

「移除 API Key」删除本插件保存的原生 Key，由 `disconnect()` 完成，框架再确认未配置。不删除生成文件；不是撤销 Google 控制台上的 Key。运行中已读取 Key 的子进程不保证立即失效。

## 6. Agent 如何发现与使用

官方项目提供 Agent 使用指引和可查询命令 schema。本次核对未发现仓库内的原生 `SKILL.md`，因此不宣称存在官方 Skill，也不引入另一项目面向 SDK 编程的 Skills 来冒充此 CLI 用法。

启用后通过已有 `ctx.host.prompts.register()`，从包装包内 `src/upstream-instructions.md` 读取官方 README 的 Agent 指引，原样注入，仅注册这一条。不改写原文，也不另加 Go Bot 授权入口或 readimage/present 的自写说明；原有界面、工具定义和核心提示继续承担各自职责。官方原文的来源 commit 和许可记录在 THIRD_PARTY_NOTICES.md，插件运行时不联网获取提示。官方指引包含：

1. 可通过 Shell 使用 `gemini-api` 调用模型、生成或编辑媒体、分析文件或使用托管 Agent。
2. 查阅 `--help` / `--usage` 及请求体 `--schema`。
3. 使用官方 dry-run 方式预览 API 调用。
4. 非流式 JSON、流式命令及 background 返回结果的用法。
5. 保存文件路径和 interaction ID，用于继续多步工作。

指引由包装包注册，停用即撤销。既有会话在下一次组装模型上下文时取得当前插件提示，不要求新建会话或重启 Host。不会向模型注入 Key，也不会复制完整 CLI 手册到 system prompt。

当前生图使用插件提供的 CLI 能力。Host 不再为模型注册 `generateimage`，插件提示也不再建议调用它。原生图实现和 provider 代码保留；既有供应商配置不删除，前端不展示其设置入口。Web 直接访问旧设置路径时显示模型设置。此调整不影响读取图片、图片附件、图片展示和文件交付。

## 7. 文件与长任务链路

### 7.1 图片和其他文件

```text
Agent → Shell → gemini-api → 当前工作目录中的文件
  → 需要图片进入模型上下文时：readimage
  → 需要向用户交付时：present
  → Go 提交现有 image / file 内容与事件
  → Controller 转发 → Web/mobile 现有渲染与下载
```

CLI 返回路径不自动变成 image block 或 file block，不扫描目录、不解析 Markdown 来补交付，不增加插件 assets API。readimage 和 present 分别沿用当前图片附件和工作区文件语义。

音频、视频先作为现有 file 块交付，不承诺新增播放器或流式通话界面。文件分析向 Google 发送指定输入，插件详情应说明数据会交给 Google 处理。

### 7.2 命令执行

Agent 通过现有 Shell 调用官方命令，读取退出码、文本输出和文件路径。执行较久的命令沿用已有 job/joboutput 行为。具体命令需要多步操作时，按官方使用说明继续调用。

## 8. Web 与 mobile 交互

沿用当前插件市场及详情样式，不新增供应商专属页面：

| 位置 | 表现 |
| --- | --- |
| 市场列表 | 官方名称、图标、双语 description、现有安装/启用状态 |
| 插件详情 | 介绍、官方 Key 获取链接、配置前置说明、安装和启用操作 |
| 账号与授权区域 | 单一「API Key」方式；配置、重新配置、移除入口 |
| 输入过程 | 现有 secret 输入与取消交互；不要求输入模型、URL、Client ID 或 Secret |
| 配置结果 | 未配置 / 已配置；失败时保留原因和重试入口 |

用户路径：打开市场 → 安装 → 启用 → 配置 API Key → 返回会话发任务。详情说明从 [Google AI Studio API Keys](https://aistudio.google.com/apikey) 获取 Key，配额和计费由用户自己的 Google 项目承担。

中英文文案由元数据和现有通用授权文案机制提供。客户端不根据 `pluginId === 'gemini-api'` 手写授权表单。输入 Key 后请求仍通过现有 Controller → Host → 插件授权链路处理。

## 9. 实施范围与验收

实施时按以下顺序完成：

1. 新增包装包、元数据、官方图标和许可证说明。
2. 接入 pnpm postinstall，使用官方安装脚本，并验证两种 Host 架构。
3. 接入默认位置的原生配置读写、Key 验证、授权 run/inspect/disconnect 及错误脱敏。
4. 注册官方 CLI 使用指引，接通市场清单、Host 资源复制和静态资源分发。
5. 补充插件使用文档，核对 Web/mobile 通用表单、操作状态及翻译。
   同时验证原生图设置入口隐藏、旧 Web 路径不会打开生图表单、实际 Host 工具列表无 generateimage 而保留 readimage/present。
6. 重新构建 Host/Controller，并进行真实前端验收。

必要验证：

- 新 Host 安装成功；下载失败和取消明确结束；未配置 Key 不阻塞安装或 Host 启动。
- 启用/停用不重复安装；卸载/重装不误删凭证；更新保持用户启用选择。
- 无效 Key、网络错误、取消不覆盖旧 Key；API Key 不进入聊天日志、模型请求、Redis、安装输出或源码。
- 授权写入和普通 CLI 读取同一份默认配置；同容器重启后正常使用；损坏配置不显示成功；不创建软链接或额外挂载。
- 用 mock 验证输入、认证检查、失败和清除逻辑；真实 Key 验收只做小量请求：一次简短文本或文件分析、一次小尺寸生图，确认 readimage/present 与前端展示。
- 从 Web 和 Android 模拟器分别完成配置/重新配置/移除，确认安装期间可正常切换会话与使用其他操作。
- Linux amd64 与 arm64 检查 CLI 安装、启动和配置读取；不要求本次专门创建 ECI/NAS 资源。

真实服务验收使用用户提供的 Key；未实测的媒体类型明确标为未验收，不以命令存在代替成功证据。验收结果如下。

## 10. 官方依据与本地对应实现

上游资料（核对日期：2026-11-21）：

- [Gemini API CLI 官方说明](https://aistudio.google.com/docs/cli)：用途、命令、认证、Agent 指引和已知限制。
- [官方 GitHub 仓库](https://github.com/google-gemini/gemini-api-cli)：源码、发布和许可证。
- [官方安装脚本](https://github.com/google-gemini/gemini-api-cli/blob/main/scripts/install.sh)：默认安装位置及架构识别。
- [配置实现](https://github.com/google-gemini/gemini-api-cli/blob/main/internal/config/config.go)：原生 YAML 字段和认证优先级。
- [认证实现](https://github.com/google-gemini/gemini-api-cli/blob/main/internal/cli/auth.go)：本地认证配置与退出语义。
- [官方 API Key 指引](https://aistudio.google.com/docs/api-key)：项目、Key 获取和使用前提。

本项目现有可复用位置：

- `extensions/sdk/README.md`、`extensions/sdk/index.d.ts`：插件安装、生命周期、授权 run/inspect/disconnect 契约。
- `extensions/plugins/google-workspace/src/native-cli.mjs`：私有文件写入和子进程取消处理的参考；本插件不复用其中的目录映射函数。
- `extensions/plugins/google-workspace/src/index.mjs`：原生存储授权适配与 Agent 授权指引注册的参考。
- `extensions/catalog/plugins.json`：审核清单与安装脚本放行。
- `extensions/plugins/manifest.go`、`extensions/plugins/assets.go`：展示元数据和后端资源。
- `build/Dockerfile.host`：包装包资源分发。

以上为在现有机制上的新增业务插件，不需要重构核心插件框架。

## 11. 实施与验收记录（2026-11-21）

- 新增 `@go-bot/plugin-gemini-api@0.1.0`，市场名称 Gemini API CLI。经真实市场安装，官方安装器取得 `gemini-api 0.5.0`。
- Web 与 Android 模拟器分别使用独立测试账号，从市场安装、启用并用真实 Key 配置成功；安装期间可以离开插件页。
- Web 验证无效 Key 的错误显示、取消、移除后重新配置；同一 Host 容器重启后仍显示已配置。
- Android 最新 QA APK 已重新打包并安装；验证配置、无效 Key 更新失败后保留旧 Key、移除及重新配置。
- 实际文件位于 `/root/.config/gemini-api/config.yaml`，目录/文件无软链接，文件权限 0600；普通 CLI 不传 Key 参数即可成功执行在线模型列表。两账号 Redis 不含该 Key，源码中不含该 Key。
- 官方 README Agent 指引单独保存在 `src/upstream-instructions.md`，仅原文注册一条 system 提示；来源 commit 与许可在插件 notices 中记录。没有 Go Bot 授权入口、readimage/present 的自写补充。
- 默认 Host 停用 `generateimage` 注册，保留实现及 provider；Web/mobile 生图设置入口隐藏，旧 Web 路径显示模型设置。Docker 集成测试检查实际模型请求：无 generateimage，仍有 readimage/present。
- `make test vet check`、Cordis 29 项测试、Web 25 项测试、Web 构建、mobile TypeScript 检查、Host 镜像 smoke、Docker Web 会话集成检查通过。最后提示词调整另跑插件定向测试和相关 Go 包测试通过。
- Linux ARM64 官方安装及启动实测成功；同 release 的 Linux AMD64 官方二进制在 Docker 模拟执行下成功输出版本。未创建 ECI/NAS 资源，也未执行 iOS 验收。
- 额外尝试一次 512 尺寸生图，Google 返回 `403 permission_denied`：当前项目不可使用免费层，需要配置计费。在线模型列表验证成功并不代表项目具备全部生成能力。没有生成成功的图片，不宣称生图验收通过；未进行其他媒体生成或修改 Google 计费设置。

真实截图和本地测试日志位于忽略目录 `.local/gemini-api-qa/`；不纳入发布源码，不保存真实 Key 到文档。
