# Go 内置 Agent Preset：功能还原与接入方案

日期：2026-11-25。状态：Go 内置框架已实施并完成本地验证。本文保留上游功能对照、最终设计契约与实施记录；实际入口和验证范围见第 13 节。

结论：在 Go 内置实现统一的 Preset 定义、注册、解析、绑定与会话选择投影；普通 Agent 与后台研究 Agent 都通过它装配。还原的是上游功能与逻辑，不依赖上游 Cordis 作用域，不实现通用 scope/realm 服务，不将 Go 核心模块重新迁回 Cordis，也不给每个 Preset 或 Agent 安装另一套业务插件。

## 1. 实施前回顾：此前实现了什么，缺少什么

`agentPreset` 在初始提交 `b4373b1` 中已经是 v4 Session Header 的字段。原有创建、恢复、Fork 和 Schedule 会保存或传递该值，但普通 Host 按固定 Go 工厂装配，没有按 ID 解析不同定义的通用服务。Schedule 还原方案也明确记录过“Go 的 Agent 运行组合固定”。

实施前主动服务增加了 `NativeComposition`，支持基础提示词替换、工具过滤和一致的 Skill 可见视图；后台通过专用入口使用固定的 `go-bot-proactive` 组合。它实现了真实的差异装配，但不是完整的 Preset 注册、绑定和选择体系。当时普通 Host 只接受默认/standard，后台组合由内部入口单独处理。本轮已删除这一固定装配入口，统一经 Registry/Binding 解析；以下表格记录实施前差距。

| 能力 | 实施前情况 | 本轮补齐的内容 |
| --- | --- | --- |
| 会话保存组合身份 | Header、运行身份、Fork、Schedule 已有字段 | 统一读取有效选择，消除只读创建 Header 的假设 |
| 不同 Agent 的实际能力 | 已有 `NativeComposition` 选择器及固定后台组合 | 通用定义、注册表、解析和绑定 |
| 工具/Skill 范围 | 已有装配过滤、动态工具过滤和 Skill 视图 | 统一选择规则、局部贡献与实际执行入口的一致解析 |
| 提示词/上下文来源 | 已有工厂和 PromptSource/ContextSource 入口 | 按组合选择贡献；不能只替换基础提示后又无条件加入全部贡献 |
| 首个 turn 前选择 | 尚无通用实现 | 原子选择、日志事实、幂等及首轮后的锁定 |
| 声明更新与现有 Agent | 固定代码组合，无注册表 | 正在运行的 Agent 保留已绑定定义，新 Agent 使用当前定义 |

此前没有引入完整系统，是实施范围被收在固定装配和后台需求上，并非 Go 内置架构无法实现。此前文档的“还原组合能力”应按上述范围理解，不能当作完整 Preset 子系统已完成。

## 2. 还原功能，不复制上游模块组织

上游以 Cordis 插件列表声明组合，通过注册表加载组合插件树，再将 Agent 的作用域绑定到组合。我们的核心执行、会话、工具、记忆等由 Go 内置管理，因此直接实现相应功能语义。

### 2.1 上游实际组成与执行关系

本次核对上游 `master` 的提交 `4b399c39a6d1479cd84ca591b4d7c88e24d38a96`。下述为源码行为，不将我们的 Go 字段当作上游已有 API：

| 上游模块 | 已核对的行为 | 对我们的意义 |
| --- | --- | --- |
| `agent-preset/definition.ts` | 定义只有 `id/name/description/order/plugins`；plugins 是 Cordis 子配置列表 | 上游通过选择插件间接选择能力；Go 定义改用能力规则及提供者引用 |
| `agent-preset-registry.register` | 声明时立即加载独立组合；重复 ID 失败，加载失败的声明仍保留诊断 | 注册不只是存一个 ID；需要能解析真实能力，区分定义无效与可选能力暂不存在 |
| `mount / composeFrom` | Agent 绑定当前组合；继承时可加入父 Agent 正在使用的同一个运行定义 | 多个 Agent 可以共享定义，但必须有独立执行资源；Fork 不能错误解析成后来替换的新定义 |
| `list / resolve` | 列表含不可用原因；解析缺省 ID 使用当前默认，未知 ID 明确报错 | 默认值、显式选择与失败不能混成 standard 静默兜底 |
| `select` | 只允许首轮开始前改选；同一当前定义与已记录身份匹配时不重复提交 | 欢迎消息不是 turn；选择事实与实际绑定必须一致 |
| `session.ts` | Header 初始化投影，再应用 `agent-preset/selected`；冷恢复读投影 | 不能只从 Header 取创建时身份 |
| `persona` | 注册前缀、后缀；`complete` 只保留前缀；`includeRuntimeContext` 独立控制动态快照 | 身份、完整 System 策略、运行时资料开关要分别建模 |
| `tools` | 全局/祖先贡献、局部覆盖、限制相交；工具查询和执行使用同一可见表 | Go 的局部装配视图需要有确定的合并顺序，而不只是发给模型前删几个 schema |
| 声明注销与运行引用 | 旧组合退休后等最后引用释放；进程重启不保留旧实现 | 内存绑定保留定义引用，磁盘只保留 Preset 身份，不新增持久版本管理 |

上游注册表并不扫描某个 preset 目录，也不自己安装插件包。当前上游声明来自普通 Cordis 配置；客户端读取的 YAML 是只读展示，不是向 Registry 提交任意 YAML 的配置编辑接口。

### 2.2 Go 对应关系

| 上游功能 | Go 中的实现方式 |
| --- | --- |
| Preset 声明插件 | Go 的 `Definition` 数据结构和项目维护的声明 |
| Preset 注册表服务 | Host 持有一个 `agentpreset.Registry` |
| 组合/Agent 作用域 | 定义引用、Agent 绑定记录和按绑定生成的能力视图 |
| Persona 插件贡献 | 现有提示词装配器中的身份前后缀与完整提示策略 |
| 插件注册的工具/Skill/上下文 | 使用现有提供者，按来源和组合规则选择 |
| 组合选择的日志投影 | 复用 Header，并增加上游同名的 `agent-preset/selected` 事实及纯投影 |

不新增另一套 AgentLoop，不把 Go 核心重新插件化，不复制 Cordis Context 代理、隔离 realm 和插件加载树。这里的 Go 绑定只管理装配关系；Cordis 继续负责业务插件的安装、启停、回调和资源释放。

这是功能还原，不承诺把上游 `plugins` YAML 原样交给 Go 就能运行。上游的工具、Session、Agent、记忆等核心插件，对应的是我们的 Go 内置模块；第三方业务插件依旧从现有 Cordis Bridge 提供贡献。

## 3. “绑定组合”的准确含义

一个 Agent 绑定一个 Preset 身份，以及本次绑定采用的定义。稳定的是身份、选择规则和提示词组织策略，不是所有运行内容的快照。

| 相对稳定 | 继续动态更新 |
| --- | --- |
| Preset ID、身份文案、能力选择规则、上下文来源规则 | 规则允许范围内的实际插件/MCP 工具和 Skill |
| 本次绑定采用的组合定义 | 当前记忆、runtime-context、time-context |
| 是否采用普通输出或调用方附加的结构化结束契约 | 现有会话模型选择、工具结果、历史和 compaction |

绑定不启动 turn、不自动创建业务 Agent、不复制主会话历史，也不创建新的工作区约定。会话、工作目录、模型配置和执行权限继续由原模块管理。

按上游约束：空白会话可以选择；出现第一个 `turn/start` 后，不能再更换 Preset 身份。已有预置欢迎消息本身不等于已经开始 turn，不用“日志是否非空”替代这个判断。

Go 方案只需表达“这个 Agent 使用哪一份能力组合”，不引入上游作用域对象、Context 父链或 realm。一个 Host 内可同时运行 standard 和后台研究 Agent，它们共用 Go 执行机制、同一个插件运行环境；Go 按定义装配各自的工具表、Skill 视图和提示词规则，不给插件新增归属字段。Session、问答、结构化结果捕获、工作目录等有状态资源仍按原有 Agent 生命周期创建，不能放进共享 Definition。

## 4. Go 模块和调用入口

已新增 `harness/agentpreset/`，只负责定义、注册、解析和绑定规则。它不依赖 Node、Controller、Redis、Docker 或 ECI，也不导入 `harness/assembly`，避免循环依赖。

实际定义与主要调用入口如下，省略字段注释：

```go
type Definition struct {
    ID             string
    Name           string
    Description    string
    Order          int
    Persona        PersonaSpec
    Tools          Selection
    LocalTools     []string // NativeFactory.PresetToolProviders 的构造入口名
    Skills         Selection
    PromptSections Selection
    RuntimeSources Selection
    RequiredSources []SourceRef
}

type PersonaSpec struct {
    Prefix                string
    Suffix                string
    Complete              bool
    IncludeRuntimeContext *bool // nil 与上游默认一致，表示启用
}

type Selection struct {
    AllowNames   *[]string
    DenyNames    []string
    AllowSources *[]SourceRef
    DenySources  []SourceRef
}

// Registry 不创建会话或执行 Agent。
New(defaultID string, check Check) *Registry
Register(Definition) (func(), error)
Replace(Definition) error
Remove(id string) error
SetDefault(id string) error
List(context.Context) []Info
Resolve(context.Context, id string) (*Binding, error)

// 会话身份与首轮协调分别由已有 Session 和 Host 模块负责。
sessionformat.EffectiveAgentPreset(Header, []Event) (string, error)
(*hostruntime.NativeRuntime).SelectAgentPreset(context.Context, id string) error
(*hostruntime.NativeHost).PresetForSession(context.Context, sessionID string) (string, error)
```

这些是 Go 方案接口，不声称与上游同名方法签名逐字一致。`Info` 包含 id、显示信息、排序、是否默认及不可用原因；Binding 是已解析定义的只读引用，不是另一套 Session/Agent 管理器。

`Allow` 未设置表示继承当前可用集合；显式空集合表示不选取该层的贡献，不能把二者混淆。名称和来源是 Go 注册表提供的真实元数据，不从工具描述、展示标题或模型输出推断。

`SourceRef` 表达内置提供者或现有插件/MCP 提供者的稳定身份，不携带凭证。实现时使用已有 package/owner/server 标识，不再制造一套与插件系统重复的业务身份。

`LocalTools` 引用由项目 Go 装配代码提供的工具构造入口，用于组合自身需要的工具或同名变体。NativeFactory 将引用连接到实际构造函数；Registry 不执行它们，也不因此成为插件加载器。有状态实例在绑定/激活阶段创建。普通 Host 工具可直接由选择规则继承，不必为每个工具重复声明 LocalTools。

默认组合 ID 由 Host 装配代码设为 `standard`。本轮不增加 `.env`、Redis 账号配置、Preset 编辑文件或新的目录扫描器。开发者可以通过 Go 注册定义，后续业务需要时再接入自有配置入口。

注册时校验结构、重复 ID、非法规则；List/Resolve 通过 Host 元数据目录诊断工具名称、本地构造入口及必需提供者。实际激活时再校验所构造的工具 schema 与身份模板；不能因某个可选定义不可用就让整个 Host 停机。List/Resolve 要保留明确的不可用原因。

必须区分三类引用：内置必需构造入口不存在属于定义错误；显式要求的插件提供者未启用属于该定义暂不可用；“继承当前可用插件”中某插件不存在则只是没有这项贡献，不能使 standard 或后台整体不可用。静态规则写错名称应诊断；未来插件名称应通过显式可选来源规则表达，不能把拼写错误永久当作正常缺省。

Go 注册阶段准备并校验描述及构造引用，不为了模拟上游 eager mount 提前创建会话、启动 Agent 或执行工具。依赖实际 Session 的资源留到绑定激活阶段构造；提供者可用性按原有插件状态重新检查。上游“组合可诊断、错误不静默”的逻辑保留，其加载插件树的实现形式不复制。

## 5. 与真实能力的连接

### 5.1 工具

Go 工具系统仍拥有完整的 preexecute、execute、postexecute、result 流程。Preset 只决定交给某个 Agent 的实际工具表，不取得调用执行权。

装配分为三个逻辑层：Host 可用贡献 → Preset 选择/局部贡献 → 本次 Agent 激活的局部贡献。后者例如 workflow 或后台运行附加的 `structured_output`。

- 全局和继承工具先按组合规则选择，再合并组合局部工具、最后合并本次 Agent 激活的局部工具。局部定义可在可信 Go 装配入口显式替换上层同名定义；同层重名必须报错。不要用一遍最终过滤误删局部贡献：上游的限制作用于继承贡献，当前局部注册不被同一层限制误删。后台的局部贡献只有专用 structured_output，不借局部覆盖重新放入它已经排除的工具。
- 合并在交给现有 Loop 之前完成；不能把重复名称原样交给 `installTools`，也不能绕开保留名称和 schema 校验。
- 给模型的 schema、真正的查找执行、工具说明及相关上下文，都来自同一份已解析工具表。不能仅隐藏 schema，实际调用却仍能找到被排除工具。
- 每个 step 继续用既有 ToolSource 刷新能力。插件停用、MCP 工具变化后，在同一组合规则内更新；本 step 的请求和执行使用同一份工具集合。
- 组合保存提供者引用/构造规则，不共享所有有状态工具实例。工作目录、Session、问答、结构化捕获等仍按 Agent 或每次激活构造。

新机制不对业务插件增加全局 guard、pre/execute/post 回调链或 `next()`。

实施前 Go 已有真实工具表及动态 ToolSource。本轮已在插件、MCP、Skill 适配处补充来源元数据，并在 NativeFactory 中统一完成分层解析。元数据仅用于选择与诊断，复用原插件/实例/MCP 标识；不新增插件归属、生命周期或凭证管理。工具的 execute 仍是原 Go handler 或原插件 RPC handler。

### 5.2 Skill

继续使用现有发现、清单、加载和更新机制。组合只对当前发现结果形成可见视图，不复制 Skill、不另建 catalog、不替用户安装插件。

Skill 清单、`skill` 工具、显式查找使用同一视图；工具可见范围和 Skill 可见范围分别配置。Skill 文本不是权限授予，执行仍受原沙箱与审批控制。

### 5.3 System prompt

Persona、工具说明、记忆说明和插件提示等必须通过现有装配入口按确定顺序组织，并保留来源。需补齐来源选择，避免基础提示已换成研究身份，随后又无条件拼入全部插件提示。

Persona 支持前缀/后缀，以及上游的 `complete` 语义：开启时只使用该身份声明的完整 System 文本，不能再悄悄附加其他 System section。它不改变模型 tools 字段，也不自动关闭独立的 runtime-context；后者由单独策略控制。

Persona 内容可沿用上游模板变量的功能：只对 Go 已登记变量进行完整占位符替换，未知变量或非法表达式明确失败，不用 JavaScript eval。支持变量不意味着支持 Cordis YAML 的 `!!js` 配置执行；后者不属于 Go 声明格式。

要逐项覆盖当前无条件追加内容：基础身份、composePrompt 的工具说明/工作目录、记忆使用说明、session_search 指南、结构化输出指南、插件 PromptSource、动态 SystemContextSource 及记忆正文。通常按既有顺序组合；complete 模式则只能保留指定身份前缀，不能在后续 step 或 compaction 后由其他入口重新拼回来。结构化工具本身仍能注册，但完整身份文本必须自行包含其使用约定；不在 complete 后悄悄增加“必需”System 段。

现有 PromptSource 可返回完整 System 字符串，而非带来源的段落；不能事后靠切字符串实现来源过滤。方案要求在现有装配器/提供者适配处形成有名称、来源与顺序的贡献，再统一选择、渲染。沿用原有每 step 装配及日志提交入口，不为此给 AgentLoop 增加一套新的执行流程。业务插件原接口的必要适配由 Bridge 完成，不让它取得 Preset 或 Agent 管理权。

现有后台的“替换基础身份、保留记忆与其他适用贡献”不等同于 `complete`，声明时必须明确保留这种行为，不能因为字段相似而改变模型上下文。

### 5.4 Runtime context 和 time context

通过工厂选择现有 ContextSource、Skill catalog 等来源，不改变它们的日志角色、写入顺序或刷新机制。

- runtime-context 仍使用现有 `user/message` / `source.kind: runtime-context`。
- time-context 仍使用原标准文案、采样和刷新入口；可以由组合决定是否装配该来源，不新增另一种时间消息。
- 当前记忆继续按既有机制读取，不冻结为绑定时快照。
- 关闭某个上下文来源时，实际构建模型请求的入口也必须关闭，不能只移除一段提示词。

`Persona.IncludeRuntimeContext` 默认 true，作用于动态 runtime-context 快照，与上游一致。time-context 是独立的既有消息机制，不能被这个布尔值顺手禁用；如 Go 定义显式选择时间来源，这是我们的装配能力，不说成上游 Persona 具有同名时间开关。已经写入日志的旧 runtime/time 消息不回删；关闭来源只阻止后续采集/补入，不伪造历史。

## 6. 创建、选择与持久恢复

创建流程：解析显式 Preset ID 或默认值 → 检查定义可用性 → 形成绑定 → 通过原 NativeFactory 创建 Session/Agent → 沿用原发布与失败回滚路径。不能先发布一个完整能力 Agent，再异步补过滤。

会话创建 Header 记录创建时的 `agentPreset`。创建时先将缺省值解析成实际 ID 再保存，不能只保存空值，让同一会话在将来因默认设置变化而换身份。Header 仍是创建事实，不存完整定义、模型密钥、插件令牌或另一份提示词快照。空白会话显式改选时，追加：

```json
{"type":"agent-preset/selected","data":{"agentPreset":"research"}}
```

上例只示意事件业务字段；完整事件仍由现有日志层生成 seq、time 等字段。不得直接改写旧 Header、插入伪造 system/user 消息或另建 `preset.json` 作为会话事实。

有效 Preset 投影：Header 初始值 → 按顺序应用已提交的 `agent-preset/selected`。选择与首轮接纳通过现有 Host 会话操作协调，确保首轮一旦开始就不能同时切换组合。

同一有效 ID、同一定义的重复选择是幂等操作，不重复写事件。空白会话选择变更失败时，不得留下“日志记录一种组合、实际 Agent 使用另一种组合”的状态；装配和提交要使用现有串行操作及准备/发布边界。

实际顺序为：锁定该会话的选择/首轮启动边界 → 校验未开始 turn → 用独立 Session 视图预检查 Go 资源、静态工具 schema 和身份模板（不发布、不写真实日志）→ 提交并 flush 选择事实 → 关闭旧资源、按已提交事实恢复新绑定并恢复接纳。可选插件/MCP 仍在既有 step 边界刷新，不为预检查执行插件业务回调。提交前失败保留原绑定；选择事实一旦成功落盘，恢复就以它为准，不能只回退内存伪装成没有选择。若最后安装遭遇不可恢复异常，应停止接纳并按已提交事实重建，而不是让旧组合继续运行。

恢复以有效投影为准，不再只读 Header。定义不存在或不能装配时，返回明确的 not-found / unavailable，不默认回退成权限更宽的 standard。通用读取应由统一 helper 提供给创建、恢复和其他消费者。

上游当前 select 的内部顺序为串行化改选 → 检查 turn 边界 → recompose → append selected。我们的“准备 → 提交 → 发布”顺序是适应 Go 已有持久日志与发布边界的实现选择，不声称上游已经采用完全相同的事务。需要保留的是模型实际所见、已提交选择、失败后可恢复三者一致。

纯读历史与恢复执行要分开：读取已存 Session 日志、文件和消息时不必激活 Agent 或启动 Node，也不因定义被移除就隐藏历史；只有再次运行这个 Agent 才必须解析可用定义。上游为插件化历史呈现提供 acquireScope，这里无需复制成“打开历史必加载 Preset 插件树”。

## 7. Fork、Spawn、Schedule 和主会话

| 入口 | Preset 规则 |
| --- | --- |
| 普通手动创建 | 未指定时用当前默认组合；本轮不增加前端选择器 |
| 主会话 | 与普通会话使用同一默认 `standard`，不新增主会话专用 Preset；`mainSessionId`、欢迎消息、不可归档和后台投递目标属于产品层职责 |
| Spawn | 由可信 Go 调用方指定时解析独立组合；未指定时继承父 Agent 当前绑定。若子 Agent 工具配置了 agentPreset 则用该默认，配置 allowedAgentPresets 才向模型暴露 agent_preset 枚举；未配置不让模型任意选身份 |
| Fork | 继承分叉点实际生效的组合；同进程优先沿用已绑定定义，不能仅复制旧 Header 字符串 |
| 子会话冷恢复 | 从自身已提交事实读取，不依赖创建它的父 Agent 当前状态 |
| Schedule 新会话目标 | 创建任务时捕获来源会话实际生效的 Preset；执行时解析；不改原定时规则和投递机制 |
| Schedule 原会话目标 | 使用目标会话自己的有效组合；不从任务字段覆盖一个已经开始的会话 |
| 后台主动服务 | 继续独立生命周期和现有能力；其内部组合可经 Registry 统一解析，不给普通创建接口开放内部后台入口 |

工作目录的创建/继承与 Preset 分离。Preset 不能恢复成“每种角色另建目录”的新约定。模型和权限也继续使用原有会话机制，不增加每个 Preset 的 API Key 或模型配置。

上游 tool-subagent 的独立 Preset 选择仅用于支持该能力的进程内 Spawn；Fork 继承对话和组合，拒绝独立改选。默认 ID 与允许列表需要校验空值、重复值及包含关系；所引用定义在真正创建子 Agent 时解析，不因父 Agent 启动就提前创建子 Agent。本轮通过 SubagentPresetSelection 接通这两个配置；只有配置允许列表才向模型暴露 agent_preset 参数。

Schedule 工具创建“新会话”目标时，上游捕获的是调用 Agent 当前实际组合，不是旧 Header。Go NativeSchedule 已改为从实际 Binding 保存 AgentPreset；目标创建成功后按其已保存身份恢复，不能在重试中重新套用来源会话后来改变的组合。Preset 本身不改变 Schedule 的工作目录继承、任务状态或投递时机。

实施时要一起检查 NativeFactory、NativeHost、Schedule 捕获、子 Agent 描述和恢复、冷读历史、搜索索引等读取身份的位置，不能只改一个 Host 创建入口。

## 8. 定义更新与绑定生命周期

定义注册后以不可变副本保存，调用方改原始切片/对象不能改变正在运行的 Agent。

替换或移除声明后：新绑定使用新定义或明确失败；已绑定 Agent 保留已采用的定义引用，直到其原生命周期结束。使用 Go 对象引用和现有 NativeLive.Close 清理，不增加对外 revision ID、持久版本表或第二个 Agent 管理器。

这仅保留组合声明，不承诺冻结整个 Cordis 插件代码版本。插件贡献仍按现有启停/更新机制变化；被卸载的工具不能因为旧 Agent 持有 Preset 就继续执行。模型 step 在组合规则内解析最新实际能力。

进程重启后，按日志中的有效 ID 解析当前项目提供的定义；不把历史 Go 对象和插件运行树写进磁盘。这个边界与上游“不跨进程保留旧运行组合实现”的约束一致。

## 9. 文件组织与接入位置

```text
harness/agentpreset/
  definition.go       # 组合声明、选择规则和结构校验
  registry.go         # Register/List/Resolve/Replace
  persona.go          # 严格、不可执行的身份模板
  catalog.go          # Host 提供者元数据校验（Binding 在 registry.go）
harness/session/
  agent_preset.go     # Header + selected 事件的有效身份投影
harness/assembly/
  native_preset.go       # 分层工具/Skill/提示/上下文视图和预检查
  native_runtime.go      # 创建/恢复前解析并实际装配
internal/host/
  agent_presets.go    # 项目自带定义、Host Registry 装配
```

这里只是职责安排，不要求为了文件数量拆出更多包。旧 NativeComposition 文件及字段已删除，由统一 Binding 生成装配选择；Host 与后台不再维护独立的能力过滤入口。

实施接入点清单：

| 代码 | 接入内容 |
| --- | --- |
| `harness/assembly/native_creation.go` | 先解析默认/显式身份再构造 Header；保留已有创建准备及失败边界 |
| `harness/assembly/native_runtime.go` | 激活/恢复接收 Binding；工具、PromptSource、SystemContextSource、RuntimeContextSource 从统一定义装配；Fork 不再只复制 parentHeader.AgentPreset |
| `harness/session/artifact.go` 及新身份投影 | 纯投影读取 Header 与 selected 事件，并在创建、恢复及冷读身份中使用 |
| `internal/host/host.go` | 普通/主会话使用 Registry；保留内部定义的路由隔离，去掉能力层只认 standard 的分支 |
| `internal/host/proactive.go`、`internal/proactive/composition.go` | 由同一 Registry 绑定后台定义，删除手工覆盖 factory.Composition 的路径 |
| `harness/tools/subagent.go`、`internal/hostruntime/native_subagents.go` | 配置化的 Spawn 选择、Fork 绑定继承、子会话冷恢复 |
| `harness/tools/native_schedule.go`、Schedule 激活入口 | 从实际有效身份捕获新会话目标；恢复解析目标自己的身份 |
| 现有插件提供者与 Skill 注册表适配入口 | 补来源元数据和每 Agent 只读视图；保持 Cordis 生命周期及原安装流程 |
| 冷读、列表与历史检索入口 | 保留创建 Header 原文，使用纯投影查询有效身份；纯读不启动模型或插件运行实例 |

## 10. 插件、前端与部署边界

业务插件继续提供工具、Skill、提示和外部服务能力，不负责创建 Agent、驱动 Session 或管理 Preset。此次不新增 `ctx.agentPresets`、`ctx.agents`、`ctx.sessions` 给 Node 插件；“Go 核心具备能力”不等于“立即对第三方开放全部管理 API”。

Preset 选择不自动安装/启用插件，不为每个 Agent 复制一套 Cordis 插件实例，不新增认证方式、凭证存储或 MCP 登录机制。所选能力实际不可用时如实诊断。

本轮提供 Go 可调用能力，不增加 Web/移动端角色选择器、Preset 市场、HTTP 管理 API 或后台编辑页。现有普通聊天和主动服务保持已有表现；后续有具体产品需求时再使用此能力。

### 10.1 项目自带的两个声明

| 定义 | 身份与能力规则 | 使用入口 |
| --- | --- | --- |
| `standard` | 普通身份、当前全部可用工具、全部适用 Skill 和原有上下文贡献；每 step 正常更新记忆和插件能力 | 普通创建、主会话及缺省的正常会话；不改变现有消息和欢迎语 |
| `go-bot-proactive` | 后台身份替换基础身份，继续保留普通工具说明、记忆和适用插件提示；排除下列清单，其余当前可用工具/Skill 继承；不采用 complete | 仅 Go 主动服务的可信内部入口；沿用原 Session ID、历史、唤醒与投递 |

后台排除清单按本次最终决定固定为：

```text
askuserquestion / ask_user_question
exitplanmode / exit_plan_mode
get_goal / create_goal / update_goal
subagent / subagent_fork / send_message / interrupt_agent / list_agents / workflow
schedule_create / schedule_edit
present
```

Schedule 的 list/view、文件读写、Shell、readimage、搜索、浏览器、记忆、session_search、现有其他工具及已启用插件能力全部保留。工具未注册或插件未启用时不凭空补入；后台不因 preset 再安装或启用服务。后台依旧按本轮 Output 捕获对象附加 structured_output，其 nextCheck 必填且 notify 可为空；不让多个 Agent/turn 共用结果对象。

不新增后台专用外部操作分类、全局 guard 或审批政策。已有执行权限和工具执行机制保持原样。后台保存研究文件不等于向前端交付，关闭 present 不影响在自己的工作目录写研究笔记；仍由 notify 决定是否向主会话投递发现。

公开/内部入口由 Host 调用边界控制，不能仅相信用户传入的 Preset ID 或 Header。普通接口不能激活内部后台定义；Go 主动服务有明确的内部解析入口。这是 Host 路由边界，不是第二份工具筛选表。

### 10.2 已完成的接入顺序与旧固定装配收口

1. **定义与注册**：完成纯 Go Definition/Registry，登记 standard 与后台声明；做好必需/可选贡献诊断，不复制 Cordis Loader。
2. **有效身份**：实现 Header + selected 事件投影，确定默认解析与首轮边界；先补齐创建、恢复、冷读和选择的调用契约。
3. **统一装配**：NativeFactory 只消费一份已解析 Binding；从同一份能力快照生成工具 schema、执行表、Skill 视图及提示词/上下文来源。接通动态 ToolSource 和每 step 的原有刷新入口。
4. **关联入口**：接通空白改选、主会话、Spawn/Fork、Schedule；工具附加的结构化结果等仍通过本次激活参数交给原工具系统。
5. **后台迁入**：将 Composition() 的身份/排除表移入项目自带声明，后台仅保留唤醒、资料采集和本轮输出契约。Host 不再硬编码“只有 standard 才能装配”，后台也不再绕过 Registry 自行设置另一份 Composition。
6. **删去重复路径**：NativeComposition 已删除；删除重复默认判断、工具名单和只读 Header 的装配路径。已有测试改为走 Registry/Binding 验证，不保留两套可独立工作的系统。
7. **完整验收后标记实施**：没有全部接通前继续标记方案，不用新增 Registry 包或几个测试就宣称完成。

本方案不要求在“读取 Session 原文”时构造运行实例。失败时不损坏原日志；框架实施与此前24小时体验评估属于不同版本，不能将旧评估结果直接记为新框架验收通过。

## 11. 验收与完成标准

| 验收项 | 必须证明的行为 |
| --- | --- |
| 注册与解析 | 多个定义、默认值、重复/非法 ID、不可用原因，不静默回退 |
| 绑定独立性 | 两个 Agent 使用不同组合，共享 Host 和原 Loop，互不修改对方的表 |
| 真实工具 | 模型 schema、工具说明和执行查找一致；动态插件/MCP 更新仍受规则约束 |
| 状态隔离 | 同 Preset 的两个 Agent 不共用问答或 structured_output 捕获状态 |
| 提示与上下文 | Persona、complete、来源过滤、记忆刷新、runtime/time 消息与 compaction 后补齐 |
| Skill | 清单、加载和显式查找一致，磁盘发现/安装机制不变 |
| 日志与恢复 | 空白选择、幂等、首轮锁定、故障回滚、有效投影恢复 |
| 关联入口 | Fork、Spawn、Schedule、主会话和后台组合，不误读创建 Header |
| 生命周期 | 声明替换后已有绑定稳定；插件停用不被旧绑定绕过；重启按当前定义解析 |
| 非回归 | 原 AgentLoop、工具阶段、Inbox、checkpoint、compaction 机制不因 Preset 改写 |

需要实际执行的代表场景：

- 同 Host 同时激活 standard 与研究定义，模型请求确实不同；在一方修改局部表不影响另一方或 Host 注册表。
- 启用/停用插件、MCP 工具变化与 Skill 更新后，两方在自己的定义规则内刷新；不复制插件实例、不因更换 Preset 重装插件。
- 局部工具覆盖继承工具、同层冲突、必需工具缺失、未启用的可选插件，分别得到预期结果；不依赖模型自觉不调用被排除工具。
- 只有欢迎语的主会话可在 Go 调用层改选；首个 turn 已开始就拒绝。改选后冷恢复、Fork 和新会话 Schedule 捕获同一有效身份，而非旧 Header。
- Persona 前后缀、模板变量、complete、runtime 开关分别测试；complete 后工具仍按定义可用，记忆/插件提示不会在后续 step 或 compaction 后意外加回。
- 定义替换后旧 Agent 保持旧定义、新 Agent 使用新定义；Fork 继承仍保留的父定义，重启后按当前定义解析。移除定义后可读原历史，但恢复执行明确失败。
- 两个后台激活不会共享 structured_output 捕获；后台仍不能使用 present、问答、编排及 Schedule 创建/修改，正常研究与原有触达决策继续可用。
- 在选择准备、日志提交与发布边界分别制造失败，确认未提交不改变事实、已提交可按事实重建，不回退成完整 standard 执行。

使用确定性模型和实际装配/日志检查验证框架，不为“证明接口存在”消耗真实模型额度。需做本地 Host/Docker 非回归；没有 UI 变更时不编造新的前端截图验收。

完成标准是上述行为经既有创建、恢复与执行入口真正生效。实际实现、测试证据及本轮范围见第 13 节。

## 12. 上游依据

对照版本：DeepSeek Harness `4b399c39a6d1479cd84ca591b4d7c88e24d38a96`，不是声称这些文件代表未来最新版本。

- [Preset 声明](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/preset/agent-preset-registry/src/definition.ts)
- [注册、绑定、继承和空白会话选择](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/preset/agent-preset-registry/src/index.ts)
- [有效 Preset 会话投影](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/preset/agent-preset-registry/src/session.ts)
- [Persona 策略](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/preset/persona/src/index.ts)
- [工具可见范围及局部覆盖](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/core/tools/src/index.ts)
- [组合生命周期及重启边界](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/preset/agent-preset-registry/README.md)

- [上游独立 Spawn 的选择规则](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/subagent/tool-subagent/src/preset-selection.ts)
- [上游 Fork 提供者](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/subagent/subagent-fork-in-process/src/index.ts)
- [Schedule 捕获实际组合](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/schedule/tool-schedule/src/index.ts)
- [System prompt 的 complete 与 runtime-context 实现](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/core/system-prompt/src/index.ts)

本次源码核对缓存位于被 Git 忽略的 `.local/agent-preset-review-20261125/`，不作为项目运行依赖。上游资料用于确认功能语义；Go 数据结构、准备/提交/发布顺序和提供者引用是本项目方案的实现选择。


## 13. 实施记录与验证范围

本轮已完成纯 Go `agentpreset` 模块及所有既有 Agent 入口的接入。Registry 保留不可变定义引用；各 Agent 的工具实例、问答、MCP 按需加载视图和结构化结果捕获仍独立创建。现有 Agent 保留其已绑定定义，新激活/冷恢复使用 Registry 当前定义；Fork 继承父 Agent 本次绑定。

普通与主会话没有执行机制上的区别：都使用 `standard`。主会话只是账号固定的 `mainSessionId` 和相应产品规则；与普通会话采用相同的 System 装配规则，不注入主会话身份提示，也不新增主会话专用 Preset。后台研究使用同一 Registry 的 `go-bot-proactive`，附加每轮独立的 structured_output。主会话和普通会话不会因后台排除规则失去工具。

### 13.1 实际入口

| 用途 | Go 入口 |
| --- | --- |
| 自定义组合 | `agentpreset.Registry.Register / Replace / Remove / SetDefault / List / Resolve` |
| 注册可信局部工具构造器 | `assembly.NativeFactory.PresetToolProviders`；Definition.LocalTools 引用名称 |
| 绑定声明 | NativeFactory 在 Activate/Resume 前解析；不可变 Binding 保存在 NativeLive.Preset |
| 空白会话改选 | `NativeRuntime.SelectAgentPreset` 或 `NativeHost.SelectAgentPreset`，没有新增 HTTP/UI 选择入口 |
| 不激活 Agent 查询有效身份 | `NativeHost.PresetForSession`；底层为 `sessionformat.EffectiveAgentPreset` |
| 配置 Spawn 的默认/允许选择 | `NativeFactory.SubagentPresets`，工具 schema 按允许列表暴露 `agent_preset` |
| 独立子 Agent 可信 Go 调用 | `ActivateChildWithPresetAt`；普通 ActivateChild/Fork 使用父绑定 |
| System/上下文来源 | PromptSections / RuntimeSources；插件适配器在调用动态回调前检查来源规则 |

身份模板变量为 `account.id`、`session.id`、`cwd`、`workspace`。非空 Prefix 替换默认基础身份，Suffix 在其他 System 贡献后追加；complete 只保留 Prefix。关闭记忆注入不会删除记忆工具或文件；关闭某工具后不再附加要求调用该工具的专用指南。runtime-context 与独立 time-context 保持既有日志和刷新机制。

### 13.2 已执行验证

- 全量 `go test ./...`。
- AgentPreset、Session、Assembly、HostRuntime、Host、插件与 MCP 相关包的 race 检查。
- 空白/欢迎会话改选、重复选择、首轮锁定、冷恢复；预检查失败保留旧事实，提交后发布失败阻止旧 Agent 接纳；不确定提交不重放、重开后按已落盘身份恢复。
- 定义替换后旧绑定稳定；Fork 继承旧定义，显式 Spawn 使用当前定义；移除定义仍可冷读历史，执行恢复明确失败。
- 真实工具覆盖执行、动态工具刷新、Skill 来源过滤、局部视图不修改 Host 注册表；同一 Preset 的两个子 Agent 独立完成结构化结果。
- 记忆每 step 更新、单独禁止记忆注入、完整身份在 compaction 后不被额外 System 段污染；排除插件提示时不调用对应业务回调。
- Schedule 新目标保存已选有效身份，创建 Header 保持原始事实。
- 本地 ARM64 Docker：复用已安装依赖镜像并加载本次 Go 二进制；真实 Container Builder、文件语义检查、空白改选、Shell 文件写入/核对、冷恢复和缺失定义拒绝均通过。
- 最新 Host HTTP：主会话与普通会话各完成正常 turn，后台受限工具表通过真实 Responses HTTP/SSE 路径完成结构化检查。模型为本地确定性服务，不消耗真实模型额度；没有改动真实账号或重启已有服务。

本轮没有前端表现变更、没有新增用户配置，也没有进行 ECI/NAS 云端验收。Docker 检查复用依赖层，不宣称重新构建或发布了公共镜像。此前 24 小时主动体验报告仍只代表此前版本。

原 `harness/agent/native_loop.go` 与 `native_tool_scheduler.go` 保持逐字未改；接入位于现有 Go 装配、身份投影和贡献适配入口。测试脚本、二进制和本地核对记录存放在被 Git 忽略的 `.local/agent-preset-implementation-20261125/`。
