# Go 内置 Agent Preset：功能还原与接入方案

日期：2026-11-22。状态：设计方案，尚未实施。本文替代此前组合方案中“固定 Go 组合已经等同完整 Agent Preset”的理解；不撤销已有主动服务实现。

## 1. 回顾：此前实现了什么，缺少什么

`agentPreset` 在初始提交 `b4373b1` 中已经是 v4 Session Header 的字段。原有创建、恢复、Fork 和 Schedule 会保存或传递该值，但普通 Host 按固定 Go 工厂装配，没有按 ID 解析不同定义的通用服务。Schedule 还原方案也明确记录过“Go 的 Agent 运行组合固定”。

主动服务这一轮增加了 `NativeComposition`，支持基础提示词替换、工具过滤和一致的 Skill 可见视图；后台通过专用入口使用固定的 `go-bot-proactive` 组合。它实现了真实的差异装配，但不是完整的 Preset 注册、绑定和选择体系。普通 Host 目前仍只接受默认/standard，后台组合由内部入口单独处理。

| 能力 | 当前实际情况 | 本方案补齐的内容 |
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

| 上游功能 | Go 中的实现方式 |
| --- | --- |
| Preset 声明插件 | Go 的 `Definition` 数据结构和项目维护的声明 |
| Preset 注册表服务 | Host 持有一个 `agentpreset.Registry` |
| 组合/Agent 作用域 | 定义引用、Agent 绑定记录和按绑定生成的能力视图 |
| Persona 插件贡献 | 现有提示词装配器中的身份前后缀与完整提示策略 |
| 插件注册的工具/Skill/上下文 | 使用现有提供者，按来源和组合规则选择 |
| 组合选择的日志投影 | 复用 Header，并增加上游同名的 `agent-preset/selected` 事实及纯投影 |

不新增另一套 AgentLoop，不把 Go 核心重新插件化，不复制 Cordis Context 代理、隔离 realm 和插件加载树。这里的 Go 绑定只管理装配关系；Cordis 继续负责业务插件的安装、启停、回调和资源释放。

## 3. “绑定组合”的准确含义

一个 Agent 绑定一个 Preset 身份，以及本次绑定采用的定义。稳定的是身份、选择规则和提示词组织策略，不是所有运行内容的快照。

| 相对稳定 | 继续动态更新 |
| --- | --- |
| Preset ID、身份文案、能力选择规则、上下文来源规则 | 规则允许范围内的实际插件/MCP 工具和 Skill |
| 本次绑定采用的组合定义 | 当前记忆、runtime-context、time-context |
| 是否采用普通输出或调用方附加的结构化结束契约 | 现有会话模型选择、工具结果、历史和 compaction |

绑定不启动 turn、不自动创建业务 Agent、不复制主会话历史，也不创建新的工作区约定。会话、工作目录、模型配置和执行权限继续由原模块管理。

按上游约束：空白会话可以选择；出现第一个 `turn/start` 后，不能再更换 Preset 身份。已有预置欢迎消息本身不等于已经开始 turn，不用“日志是否非空”替代这个判断。

## 4. Go 模块和调用入口

建议新增 `harness/agentpreset/`，只负责定义、注册、解析和绑定规则。它不依赖 Node、Controller、Redis、Docker 或 ECI，也不导入 `harness/assembly`，避免循环依赖。

概念接口如下，具体签名在实施时结合已有 NativeFactory 整理：

```go
type Definition struct {
    ID             string
    Name           string
    Description    string
    Order          int
    Persona        PersonaSpec
    Tools          Selection
    LocalTools     []ToolProviderRef
    Skills         Selection
    PromptSections Selection
    RuntimeSources Selection
}

type Selection struct {
    AllowNames   *[]string
    DenyNames    []string
    AllowSources *[]SourceRef
    DenySources  []SourceRef
}

// 伪签名：Registry 不创建会话或执行 Agent。
Register(definition) (unregister, error)
Replace(definition) error
List() []PresetInfo
Resolve(id) (DefinitionSnapshot, error)
Bind(snapshot, agentIdentity) (Binding, error)
```

`Allow` 未设置表示继承当前可用集合；显式空集合表示不选取该层的贡献，不能把二者混淆。名称和来源是 Go 注册表提供的真实元数据，不从工具描述、展示标题或模型输出推断。

`SourceRef` 表达内置提供者或现有插件/MCP 提供者的稳定身份，不携带凭证。实现时使用已有 package/owner/server 标识，不再制造一套与插件系统重复的业务身份。

`LocalTools` 引用由项目 Go 装配代码提供的工具构造入口，用于组合自身需要的工具或同名变体。NativeFactory 将引用连接到实际构造函数；Registry 不执行它们，也不因此成为插件加载器。有状态实例在绑定/激活阶段创建。普通 Host 工具可直接由选择规则继承，不必为每个工具重复声明 LocalTools。

默认组合 ID 由 Host 装配代码设为 `standard`。本轮不增加 `.env`、Redis 账号配置、Preset 编辑文件或新的目录扫描器。开发者可以通过 Go 注册定义，后续业务需要时再接入自有配置入口。

注册时校验结构、重复 ID、非法规则及定义中静态引用的有效性。实际绑定时校验当前提供者能否形成所需组合；不能因某个可选定义不可用就让整个 Host 停机。List/Resolve 要保留明确的不可用原因。

## 5. 与真实能力的连接

### 5.1 工具

Go 工具系统仍拥有完整的 preexecute、execute、postexecute、result 流程。Preset 只决定交给某个 Agent 的实际工具表，不取得调用执行权。

装配分为三个逻辑层：Host 可用贡献 → Preset 选择/局部贡献 → 本次 Agent 激活的局部贡献。后者例如 workflow 或后台运行附加的 `structured_output`。

- 全局和继承工具按组合规则选择。组合或激活附加的同名局部定义，可以在可信的 Go 装配入口显式替换上层定义；同层重名必须报错。
- 合并在交给现有 Loop 之前完成；不能把重复名称原样交给 `installTools`，也不能绕开保留名称和 schema 校验。
- 给模型的 schema、真正的查找执行、工具说明及相关上下文，都来自同一份已解析工具表。不能仅隐藏 schema，实际调用却仍能找到被排除工具。
- 每个 step 继续用既有 ToolSource 刷新能力。插件停用、MCP 工具变化后，在同一组合规则内更新；本 step 的请求和执行使用同一份工具集合。
- 组合保存提供者引用/构造规则，不共享所有有状态工具实例。工作目录、Session、问答、结构化捕获等仍按 Agent 或每次激活构造。

新机制不对业务插件增加全局 guard、pre/execute/post 回调链或 `next()`。

### 5.2 Skill

继续使用现有发现、清单、加载和更新机制。组合只对当前发现结果形成可见视图，不复制 Skill、不另建 catalog、不替用户安装插件。

Skill 清单、`skill` 工具、显式查找使用同一视图；工具可见范围和 Skill 可见范围分别配置。Skill 文本不是权限授予，执行仍受原沙箱与审批控制。

### 5.3 System prompt

Persona、工具说明、记忆说明和插件提示等必须通过现有装配入口按确定顺序组织，并保留来源。需补齐来源选择，避免基础提示已换成研究身份，随后又无条件拼入全部插件提示。

Persona 支持前缀/后缀，以及上游的 `complete` 语义：开启时只使用该身份声明的完整 System 文本，不能再悄悄附加其他 System section。它不改变模型 tools 字段，也不自动关闭独立的 runtime-context；后者由单独策略控制。

现有后台的“替换基础身份、保留记忆与其他适用贡献”不等同于 `complete`，声明时必须明确保留这种行为，不能因为字段相似而改变模型上下文。

### 5.4 Runtime context 和 time context

通过工厂选择现有 ContextSource、Skill catalog 等来源，不改变它们的日志角色、写入顺序或刷新机制。

- runtime-context 仍使用现有 `user/message` / `source.kind: runtime-context`。
- time-context 仍使用原标准文案、采样和刷新入口；可以由组合决定是否装配该来源，不新增另一种时间消息。
- 当前记忆继续按既有机制读取，不冻结为绑定时快照。
- 关闭某个上下文来源时，实际构建模型请求的入口也必须关闭，不能只移除一段提示词。

## 6. 创建、选择与持久恢复

创建流程：解析显式 Preset ID 或默认值 → 检查定义可用性 → 形成绑定 → 通过原 NativeFactory 创建 Session/Agent → 沿用原发布与失败回滚路径。不能先发布一个完整能力 Agent，再异步补过滤。

会话创建 Header 记录创建时的 `agentPreset`。空白会话显式改选时，追加：

```json
{"type":"agent-preset/selected","data":{"agentPreset":"research"}}
```

上例只示意事件业务字段；完整事件仍由现有日志层生成 seq、time 等字段。不得直接改写旧 Header、插入伪造 system/user 消息或另建 `preset.json` 作为会话事实。

有效 Preset 投影：Header 初始值 → 按顺序应用已提交的 `agent-preset/selected`。选择与首轮接纳通过现有 Host 会话操作协调，确保首轮一旦开始就不能同时切换组合。

同一有效 ID、同一定义的重复选择是幂等操作，不重复写事件。空白会话选择变更失败时，不得留下“日志记录一种组合、实际 Agent 使用另一种组合”的状态；装配和提交要使用现有串行操作及准备/发布边界。

具体顺序应为：锁定该会话的选择/首轮启动边界 → 校验未开始 turn → 准备新组合资源和完整视图（不发布、不写模型消息）→ 提交选择事实 → 安装新绑定并恢复接纳。提交前失败保留原绑定；选择事实一旦成功落盘，恢复就以它为准，不能只回退内存伪装成没有选择。若最后安装遭遇不可恢复异常，应停止接纳并按已提交事实重建，而不是让旧组合继续运行。

恢复以有效投影为准，不再只读 Header。定义不存在或不能装配时，返回明确的 not-found / unavailable，不默认回退成权限更宽的 standard。通用读取应由统一 helper 提供给创建、恢复和其他消费者。

## 7. Fork、Spawn、Schedule 和主会话

| 入口 | Preset 规则 |
| --- | --- |
| 普通手动创建 | 未指定时用当前默认组合；本轮不增加前端选择器 |
| 主会话 | 继续当前普通行为，不自动改成新角色，也不因增加 Registry 重写欢迎消息 |
| Spawn | 可以由 Go 调用方明确指定组合；缺省行为沿用现有子 Agent 约定，必须在实现前核对该入口，不暗中改默认 |
| Fork | 继承分叉点实际生效的组合；同进程优先沿用已绑定定义，不能仅复制旧 Header 字符串 |
| 子会话冷恢复 | 从自身已提交事实读取，不依赖创建它的父 Agent 当前状态 |
| Schedule 新会话目标 | 创建任务时捕获来源会话实际生效的 Preset；执行时解析；不改原定时规则和投递机制 |
| Schedule 原会话目标 | 使用目标会话自己的有效组合；不从任务字段覆盖一个已经开始的会话 |
| 后台主动服务 | 继续独立生命周期和现有能力；其内部组合可经 Registry 统一解析，不给普通创建接口开放内部后台入口 |

工作目录的创建/继承与 Preset 分离。Preset 不能恢复成“每种角色另建目录”的新约定。模型和权限也继续使用原有会话机制，不增加每个 Preset 的 API Key 或模型配置。

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
  binding.go          # 不可变定义引用和 Agent 绑定契约
harness/session/
  agent_preset.go     # Header + selected 事件的有效身份投影
harness/assembly/
  native_composition.go  # 现有选择器逐步由统一装配规则承接
  native_runtime.go      # 创建/恢复前解析并实际装配
internal/host/
  agent_presets.go    # 项目自带定义、Host Registry 装配
```

这里只是职责安排，不要求为了文件数量拆出更多包。现有 NativeComposition 不能长期与 Registry 变成两套相互独立的决定来源：接入后由解析结果生成装配选择，不再在 Host 和后台分别维护 ID 判断与重复过滤表。

## 10. 插件、前端与部署边界

业务插件继续提供工具、Skill、提示和外部服务能力，不负责创建 Agent、驱动 Session 或管理 Preset。此次不新增 `ctx.agentPresets`、`ctx.agents`、`ctx.sessions` 给 Node 插件；“Go 核心具备能力”不等于“立即对第三方开放全部管理 API”。

Preset 选择不自动安装/启用插件，不为每个 Agent 复制一套 Cordis 插件实例，不新增认证方式、凭证存储或 MCP 登录机制。所选能力实际不可用时如实诊断。

本轮先提供 Go 可调用能力，不增加 Web/移动端角色选择器、Preset 市场、HTTP 管理 API 或后台编辑页。现有普通聊天和主动服务保持已有表现；后续有具体产品需求时再使用此能力。

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

使用确定性模型和实际装配/日志检查验证框架，不为“证明接口存在”消耗真实模型额度。需做本地 Host/Docker 非回归；没有 UI 变更时不编造新的前端截图验收。

完成标准不是增加一个 Registry 空壳或多一个身份字段，而是上述行为通过既有创建和恢复入口真正生效。实施前仍需按实际接口细化错误返回和原子发布步骤；这份文档本身不代表已经实现。

## 12. 上游依据

对照版本：DeepSeek Harness `2e39e70496aa02986ca6a820c5d497e816b69d7e`，不是声称这些文件代表未来最新版本。

- [Preset 声明](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/preset/agent-preset-registry/src/definition.ts)
- [注册、绑定、继承和空白会话选择](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/preset/agent-preset-registry/src/index.ts)
- [有效 Preset 会话投影](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/preset/agent-preset-registry/src/session.ts)
- [Persona 策略](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/preset/persona/src/index.ts)
- [工具可见范围及局部覆盖](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/core/tools/src/index.ts)
- [组合生命周期及重启边界](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/preset/agent-preset-registry/README.md)
