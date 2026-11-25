# 主会话与首次使用流程方案

状态：已实现，并通过本地 Docker、真实 Web 和 Android 模拟器验收。欢迎消息采用 system → assistant 的真实初始化消息，详见 6.1。

## 1. 本次目标与产品依据

每个账号拥有一个稳定的主会话。登录后直接进入主会话，用户可以先看到助手介绍、配置模型和管理插件，而不再面对没有会话的空页面。用户仍然可以手动创建普通会话，专门处理不同主题。

Muse 的官方设计说明明确采用长期交流的 main chat，并提供用于独立主题的 side chats；官方帮助页也区分这两种会话。这里参考其「持续交流、直接介绍能做什么、邀请用户交办事情」的表达方式。公开资料不能确认完整的首次欢迎消息原文，下面文案是 Go Bot 的设计稿，不标作 Muse 原文。

- [Muse 官方设计说明](https://muse.ai/blog/how-we-designed-muse)
- [Muse 官方使用说明](https://www.meta.com/en-gb/help/superintelligence/1331373868832401/)

本次实现主会话、模型检查、欢迎消息和默认插件安装，不引入主动唤醒算法，也不调整已有 Schedule 的投递规则。

## 2. 现状与需要调整的位置

| 当前实现 | 本次调整 |
| --- | --- |
| 账号创建只写 Host 生命周期初始状态 | 同时分配主会话 ID，写入默认插件选择 |
| `/v1/host/start` 启动 Host 并同步账号设置 | 保留核心启动；主会话准备不依赖 LLM；耗时插件准备与核心启动分开 |
| 普通新建会话先检查默认模型 | 保留；主会话的系统初始化使用专门的内部入口 |
| 发送接口已检查会话模型与 Host 配置同步状态 | 复用，补齐主会话未绑定模型时的首次绑定及明确错误 |
| Web、移动端在没有会话时显示空页面 | 默认进入主会话；普通会话仍由用户创建 |
| 插件已支持 pnpm 安装及 Cordis 启停 | 原样复用，不把安装放入插件 `apply()` |

关键代码位置：`internal/controller/store.go`、`models.go`、`http.go`、`plugins.go`，`internal/hostruntime`、`internal/host`、`internal/plugins`，以及 Web 和移动端会话入口。

## 3. 主会话的定义

### 3.1 身份与生命周期

- 一个账号一个主会话，使用正常 UUID，不把字符串 `main` 作为 sessionId。
- UUID 在创建账号时生成并写入 Redis；第一次启动 Host 时才建立磁盘上的会话。
- 主会话 ID 不随容器、HostID、登录、重启、模型切换发生变化。
- fork 子 Agent 的原有关系与前端筛选规则保持不变。
- 主会话固定保留，前端不提供归档/删除入口，服务端也拒绝归档。普通会话继续使用现有归档逻辑。
- 主会话标题显示为 `Go Bot`，旁边显示「主会话 / Main chat」标记；不由第一条用户消息自动覆盖标题。

### 3.2 默认打开规则

- 登录后打开聊天首页，默认选择主会话；移动端冷启动也默认进入主会话。
- Web 显式打开普通会话的 `/chat/<sessionId>` 深链接时，保留该目标，不强制跳走。
- 应用运行过程中切换到设置、任务再返回，保持当前选中的会话。
- 当前普通会话归档或不可访问时，返回主会话。
- 侧边栏/会话抽屉顶部单独固定主会话，下面展示普通会话及「新建会话」。主会话不受普通列表分页、排序影响，也不重复出现。
- 不再通过本地“上次会话 ID”覆盖冷启动主会话；语言、连接信息等现有本地偏好继续保留。

### 3.3 工作目录和 AI 感知

主会话也使用现有手动会话的默认目录约定：

```text
/root/harness/workspaces/<mainSessionId>/
```

工作目录仍通过 Session 的 `cwd` 保存，不引入一套主会话专属工作区规则。会话与工作目录的解耦设计保持不变。

日志继续保存于：

```text
/root/harness/.dsh/sessions/<projectKey>/<mainSessionId>/session.v4.jsonl
```

Docker volume / NAS 承载 `/root/harness` 的机制自然涵盖这两处数据。

Controller 在 Host 启动同步时告知主会话 ID，用于界面标识、归档限制和主动服务投递目标。主会话与普通会话采用相同的 System 装配规则，不额外注入主会话身份说明。

## 4. Redis 与 Host 的数据边界

只给现有 `user:<userId>` Hash 增加一个字段：

| 字段 | 内容 |
| --- | --- |
| `mainSessionId` | 账号固定的主会话 UUID |

继续复用：

| 已有字段 | 用途 |
| --- | --- |
| `llm` | 用户模型端点、协议、API Key、模型列表及默认选择 |
| `sessionModels` | 已绑定模型的会话选择；未配置模型的主会话暂不占条目 |
| `plugins` | 用户对插件包的选择：catalogId、version、enabled |

创建账号时，在同一次写入中初始化 `mainSessionId` 和默认插件选择。不新增 `firstLogin`、`firstHostStarted`、`defaultPluginsInstalled` 等重叠标记；账号是否已经初始化不靠 HostID、日志条数或有没有安装某一个插件推断。

Redis 负责稳定身份及用户选择，Host 负责真实日志、欢迎消息、包安装和运行状态。无需新增 `host.json`、主会话索引文件或新的 Redis key。

Host 的主会话身份是 Controller 每次启动同步下来的账号信息，不写进上游 v4 Header 的自定义字段。公开会话投影增加 `isMain`，用于客户端展示；它是 ID 比较结果，不是第二份可修改的身份数据。

## 5. 主会话可以先于模型配置存在

### 5.1 创建和阅读不要求模型

首次登录时即使 `llm` 为空，也能创建、读取主会话、看欢迎消息和输入框、打开设置。内部准备接口只构造 Session 日志和欢迎消息，不启动 turn、不调用模型。

实现时需要把“持久会话建立”与“可执行 Agent 激活”分开：复用现有 Session preparation / prepared journal 写入能力，不通过虚构模型或临时默认端点绕过模型检查。Agent 在真正需要执行时才解析可用模型。

### 5.2 检查点一：手动新建普通会话

复用当前 `POST /v1/sessions`：

1. 读取账号模型设置。
2. 检查是否有结构完整的可用默认端点及默认模型。
3. 不满足时返回 `model_default_required`，不创建日志、工作区或半成品会话。
4. 满足时创建普通会话，并把默认选择写入 `sessionModels`。

普通会话不预填欢迎消息。

### 5.3 检查点二：发送消息

服务端在保存本条消息/附件、入队及启动 turn 前完成检查，前端提供相同检查以便及时提示，但不能替代服务端：

| 情况 | 行为 |
| --- | --- |
| 主会话尚未选择模型，账号已有可用默认模型 | 保存默认选择到该主会话，同步 Host 后发送 |
| 主会话尚未选择模型，账号也没有默认模型 | 拒绝发送，提示配置模型 |
| 会话已经有可用选择 | 使用该选择，不被新的账号默认模型覆盖 |
| 原先选择的端点被删除、模型被移除或 API Key 被清空 | 拒绝发送，明确提示重新选择；不静默切到其他供应商 |
| 会话选择有效，但账号默认选择失效 | 允许在此已有会话发送；新建普通会话仍需要可用默认模型 |
| Host 尚未应用最新模型配置 | 返回明确的同步错误，不让旧配置处理本条消息 |

“可用”在本地检查层指协议、URL、密钥、模型与选择关系合法，不代表每次发送前额外向服务商做一次探测。真实调用的 401、配额、网络等错误按现有运行错误展示。

网页搜索和 Gemini API CLI 的密钥均不是创建会话或发送一般消息的前置条件。

### 5.4 缺少配置时的交互

- 输入框仍可输入、选择附件，草稿保留。
- 点击发送后，缺少账号模型配置时显示「请先配置模型，再开始聊天」，提供「配置模型」。
- 已有会话模型失效时显示「当前会话的模型不可用，请重新选择」，提供「选择模型」及「模型设置」。
- 弹层可关闭，点击外部或 Esc 收起；不使用无法离开的全屏阻断页面。
- 配置返回后刷新模型状态，用户再次点击发送；不自动发送原草稿。
- 配置与校验失败时不显示“已发送”气泡，不出现虚假的“生成中”。
- Web、移动端使用一致规则，中英文文案跟随本地语言设置。

## 6. 欢迎消息

### 6.1 真实存储，一次写入

只在新建主会话时预置一条普通 `assistant/message`，使用现有文本 content block 和历史/SSE 投影。它不是首次模型生成，也不伪造 token 用量或一次生成完成的 turn。

主会话构造时，把 Header、初始 `system/message`、欢迎 `assistant/message` 和初始化结束标记通过 prepared journal 一起发布。模型上下文顺序为 system → 欢迎 assistant → 后续用户消息。初始 system 使用基础系统提示，首次 step 会按当前工具、插件、记忆等重新组装，并通过原有 append-only replace 事件替换上下文中的 system，磁盘旧记录不变。重复请求发现该日志已存在便直接复用，不通过“消息数为零”补发，不在每次登录时追加。

首次创建的语言由客户端本地语言作为本次请求参数提供，仅接受 `zh-CN` / `en`。服务端未收到时使用英文。它不是账号语言字段。已有欢迎消息与普通历史一样不会因切换界面语言而重写。

这是产品初始化消息，不是模型 step 产物：仅构造前缀允许 `go-bot/initial-system`（seq 0）和 `go-bot/welcome`（seq 1）来源，文本-only、无 turn/step、无工具调用、空 stream、无 usage。普通模型消息仍必须处于真实 turn/step；不扩展上游 v4 Header，不保留专用 welcome 事件或前端特殊消息类型。欢迎内容进入普通助手上下文，也可被普通 compaction 压缩。

### 6.2 中文文案建议

> 你好，我是 Go Bot，你的个人 AI 助手。
>
> 日常事务或工作上的事情，都可以交给我一起处理：查找资料、操作浏览器，或者制作和编辑 Word、PPT、Excel 文件。你可以直接说想完成什么，我们再一起把细节理清楚。
>
> 想生成图片或视频？可以到插件市场安装并启用 **Gemini API CLI**，配置自己的 API Key 后再交给我处理。你也可以连接更多插件，扩展我能帮你做的事情。
>
> 今天有什么想让我帮忙的？

### 6.3 English draft

> Hi, I'm Go Bot, your personal AI assistant.
>
> I can help with everyday tasks and work—researching a topic, using the browser, or creating and editing Word documents, presentations, and spreadsheets. Tell me what you'd like to get done, and we can work through the details together.
>
> Want to create images or videos? Install and enable **Gemini API CLI** from the plugin market, then connect your own API key. You can connect more plugins whenever you want to expand what I can help with.
>
> What would you like a hand with today?

欢迎消息只介绍能力，不承诺服务免费、已经完成授权或尚未实现的主动服务。Gemini 插件名称取真实市场名称，文案中的插件引用采用稳定 catalog ID 关联；未来目录没有该插件时，构造文案时去掉对应推荐句。

### 6.4 界面与历史的关系

欢迎消息按普通助手消息显示 logo、文本和 Markdown，不另造 `welcome` 内容块。输入框附近另有轻量的「配置模型」「查看插件」入口，前端执行导航。这些入口不进入模型上下文，不把业务导航链接伪装成可供 Agent 使用的后端文件链接。

插件准备中的实时状态单独展示在欢迎区附近，不写成反复变化的助手历史。欢迎消息会像其他助手消息一样进入后续模型上下文，也自然遵循原有 compaction 机制。

## 7. 默认插件策略

### 7.1 名单归项目，用户选择归账号

在现有项目维护的 `extensions/catalog/plugins.json` 条目上增加可选 `defaultEnabled: true`，初始仅：

- `officecli` → `@go-bot/plugin-officecli`
- `agent-browser` → `@go-bot/plugin-agent-browser`

这属于项目分发策略，应放在受审核目录中，不让第三方插件通过自己的 `plugin.json` 声明“必须为所有人自动安装”。插件展示信息仍由自己的 `plugin.json` 提供。

Controller 创建新账号时，把该名单转换成现有 `plugins.packages` 的 enabled 选择，版本使用该次创建时的审核目录版本。示意：

```json
{
  "packages": {
    "@go-bot/plugin-officecli": {
      "catalogId": "officecli",
      "version": "0.1.0",
      "enabled": true
    },
    "@go-bot/plugin-agent-browser": {
      "catalogId": "agent-browser",
      "version": "0.1.0",
      "enabled": true
    }
  }
}
```

以后增加默认插件只需更新目录；对新账号生效。已有账号不自动补装新默认项、不强制升级、不重新启用已停用项。用户从市场卸载后，下一次启动也不能重新从“默认名单”加回来。

### 7.2 Controller 和 Host 各自做什么

| 位置 | 职责 |
| --- | --- |
| Controller | 分配主会话身份，保存用户选择，将审核后的安装源与选择发给 Host |
| Host Go 插件管理模块 | 检查真实安装状态、调用现有 pnpm 安装入口、返回失败原因和状态 |
| 插件包自身的安装脚本 | 安装其官方 CLI、依赖、浏览器等运行内容 |
| Cordis | 安装完成后装配插件，注册 Skill / Prompt / 工具等贡献，负责启停生命周期 |

安装仍走 pnpm 的既有生命周期，不在 `apply()` 里下载或安装，也不在 Controller 容器中安装 Host CLI。Skill 仍由启用插件注册现有包内目录，不复制为第二套常驻 Skill。

### 7.3 启动分为两段，界面不被下载卡住

为了同时满足“首次自动安装”和“安装时还能操作其他页面”，使用两段客户端自动编排：

1. `POST /v1/host/start` 完成核心 Host 启动、模型与基础设置同步、主会话准备，返回 `mainSessionId`。这一段不等待 pnpm 下载。
2. 账号级前端连接层自动发起 `POST /v1/host/plugins/prepare`（新增），Controller 同步等待 Host 检查并安装缺失的已选择包，然后启用；用户同时可以阅读主会话、配置模型、切换设置或文件系统。

第二段是启动体验的一部分，用户不需要额外点安装。请求由账号级连接层持有，不绑定插件详情或会话组件；界面路由切换不会取消。Web、移动端及自定义前端文档都说明这两段流程。

准备接口复用当前插件操作串行机制，不新增任务队列、Redis 任务表或后台定时循环。请求同步返回成功或失败；已有包版本匹配时直接跳过安装，只恢复应启用的实例。多个客户端重复请求也复用现有安装检查和操作互斥，不重复执行安装脚本。

后续登录仍可以做快速安装状态核对，但成功安装过且未发生包变更时不能重新执行 pnpm/install 脚本。它不是每次启动都重新安装默认名单。Host 替换或安装失败造成实际文件缺失时，按保存的用户选择恢复缺失项，属于恢复/重试；已经卸载的项没有用户选择，不能被恢复。

要相应移出目前 `syncAccountSettings()` 中会阻塞核心启动的耗时包准备部分，保留其他配置同步。显式安装、升级、卸载接口的同步成功/失败语义不变。

### 7.4 进度、失败和重试

- 展示「正在准备浏览器与 Office 能力…」，列出真实插件名及安装状态，不编造百分比。
- 成功后收起准备提示；市场按已有状态显示已安装、已启用。
- 某项失败，明确显示该项和原因，提供「重试」与「查看插件」。Host、主会话和模型设置继续可用。
- 已成功的项目保留成果；需要检查现有批量 Apply 早退行为，确保后续包失败不会使先前成功的插件一直无法启用。
- 重试只处理未完成项，不重新安装成功项；不增加周期自动重试。
- 未完成安装的插件能力暂不可用，不能通过欢迎文案或模型工具列表伪装为已就绪。已加载 Agent 在下一次构建上下文时读取更新后的 Skill / Prompt 贡献。

## 8. 对外与内部接口

以下接口已实现：

| 接口 | 作用 |
| --- | --- |
| `POST /v1/accounts` | 创建账号时同时初始化主会话 ID、默认插件选择 |
| `POST /v1/host/start` | 核心启动与主会话准备；可接收本次 welcomeLocale，响应包含 mainSessionId |
| `POST /v1/host/plugins/prepare` | 检查并准备账号已选择的插件，复用现有插件状态响应 |
| `POST /internal/host/sessions/main` | Controller 专用：以给定 UUID/locale 准备或复用主会话，不要求 LLM |
| 现有会话列表和详情投影 | 提供 isMain；主会话的定位由 start 响应提供，独立于普通列表分页 |
| `POST /v1/sessions` | 普通新建流程，保留默认模型校验 |
| `POST /v1/sessions/:id/messages` | 所有会话发送校验，主会话无选择时可绑定账号默认模型 |

主会话的服务端归档限制和客户端标识由同一 mainSessionId 推导。普通新建接口不能让调用者随意指定 isMain。现有 Host 级 SSE 共用连接继续使用，不新增专门的主会话连接或欢迎消息推送协议。

## 9. 一次完整的首次使用

```text
创建账号
  → Redis 写主会话 UUID 和默认插件选择
登录
  → Controller 启动 Host，准备主会话及欢迎消息
  → 客户端进入主会话，显示输入框
  → 客户端自动请求插件准备，用户可以同时配置模型
  → Host 通过 pnpm 安装 officecli / agent-browser，再交给 Cordis 启用
用户发送第一条消息
  → 检查账号配置及会话模型；缺少则引导设置并保留草稿
  → 选择可用则正常入队、执行、实时展示
```

再次启动：复用相同主会话及欢迎消息、现有模型选择和用户插件选择；不追加欢迎消息、不重装完整包、不恢复用户已卸载的默认插件。

## 10. 实施与验收

实施顺序：账号字段与默认目录策略 → 无模型主会话持久化 → 两个模型检查点 → 插件准备与启动拆分 → Web/移动端入口和中英文 → API/架构文档。

必须覆盖：

1. 全新账号完全没有 LLM 配置，也能打开主会话且只有一条欢迎消息。
2. Web、移动端同时登录，主会话 ID 一致；反复重启、刷新不重复创建、不重复欢迎。
3. 先输入文字和附件再触发模型配置，草稿保留且消息未入队；配置后再次发送成功。
4. 普通新建缺少默认模型时拒绝；有配置后普通会话为空，不复制主会话欢迎消息。
5. 会话当前模型失效时明确报错；修改默认模型不覆盖已有有效会话选择。
6. 默认两个插件真实安装并启用，Agent 能看到其 Skill；重启不重复执行安装脚本。
7. 停用、卸载默认插件后重新登录不恢复；增加新默认项不强制影响已有用户。
8. 安装中可以导航、配置模型、查看文件；部分失败可重试，不重装已成功项。
9. 主会话不可归档，普通归档返回主会话；普通深链接和任务打开指定会话仍正常。
10. Web 与 Android 模拟器验证中文/英文、欢迎文本、主会话标记、缺模型引导和安装状态；真实模型只需一条简短消息验证主会话完整发送链路。

本次不需要额外验证 ECI 云资源；主会话及日志复用现有 `/root/harness` 存储边界，不变更 NAS 挂载方案。

## 11. 本地验收记录（2026-11-21）

| 范围 | 实际结果 |
| --- | --- |
| Web 首次使用 | 中文新账号未配置模型时可见主会话和欢迎消息；发送、新建均提示配置模型，文字与已选附件保留 |
| Web 真实模型请求 | 在界面配置 Anthropic Messages 兼容端点后发送短消息，正常收到「主会话连接正常」；普通新会话不含欢迎消息 |
| 模型失效 | 移除当前会话所选模型后，发送明确提示重新选择；文字、附件未清除，也未静默改用其他模型 |
| Android 真实交互 | 重新编译并安装独立 QA 包；英文欢迎、缺模型底部提示、界面填写模型配置、真实发送收到 `OK`、普通新会话无欢迎消息均通过 |
| Android 冷启动 | 从普通会话退出进程后重开，自动连接并返回固定主会话；历史与欢迎消息复用 |
| 默认插件 | 两端新账号均真实安装并启用 agent-browser / OfficeCLI；插件市场显示已安装，Agent 的 Skill 清单包含已启用贡献 |
| 停用与卸载 | Web 分别停用、卸载 agent-browser，再停止并启动 Host；不恢复该选择，OfficeCLI 保持就绪 |
| 数据复用 | 停止再启动后主会话 ID 相同，日志逐字节不变，欢迎仅一条；CLI 安装文件时间及大小不变 |
| 部分安装失败 | 自动测试模拟一个包失败、另一个成功；成功包可启用，重试不重装成功包 |
| 上下文与持久化 | 自动测试验证 system → 欢迎 assistant → 用户消息；再次恢复执行、usage 统计、普通 compaction 和压缩后的 JSONL 解码通过 |

检查命令及结果：

```text
make test vet check                                           PASS
go test -race ./harness/agent ./harness/session \
  ./internal/hostruntime ./internal/controller ./internal/plugins PASS
npm --prefix frontend test                                   PASS（25 项）
npm --prefix frontend run build                              PASS
npm --prefix mobile run typecheck                            PASS
scripts/smoke-image.sh go-bot/host:main-chat-qa                 PASS
TestDockerWebChatPreview                                     PASS
TestDockerModelSettingsSwitchBetweenTurns                    PASS
TestDockerHostConversationRecoveryAndFiles                   PASS
scripts/smoke-cli.py（使用本次 QA 镜像的 Compose 覆盖配置）      PASS
api/openapi.yaml YAML 解析、git diff --check                  PASS
```

本地 QA 镜像复用既有依赖镜像层，替换本次编译的 Host / Controller；Android QA 包单独使用应用 ID，未覆盖正式安装。本次真实模型请求用于验证消息链路，没有额外进行长任务生成。截图与私有验收文件保存在被忽略的 `.local/main-chat-qa/`，不发布测试账号、凭证或日志。ECI 未在本次验收范围内。
