# Agent 组合能力：DeepSeek Harness 调研与 Go 还原方案

日期：2026-11-22。状态：本文中的固定 Go 装配选择器和后台组合已实现并验收；完整 Agent Preset 子系统尚未实现。

范围澄清：此前“还原组合能力”仅指实际差异装配，不代表已经具备上游完整的声明、注册、绑定和选择体系。新增能力的完整设计见 [Go 内置 Agent Preset 方案](agent-preset-go-plan.zh-CN.md)。该方案按功能逻辑补齐，不要求复制上游模块或 Cordis 插件树；本文件保留为已有后台装配的调研与实施记录。

本文确定 Agent 组合能力及其 Go 归属；后台上下文、能力和逐轮结果契约以主动服务方案第 10、13 节为准。

执行流边界已经明确：新 Agent 直接使用现有 AgentLoop、turn/step、Inbox、工具管线、checkpoint 和 compaction。组合功能只决定组装给它的内容与能力，不修改这些执行机制，也不为后台 Agent 增加循环分支。后台每 turn 首次模型请求前，由外部运行器补充一次标准 time-context，复用原消息格式与文案；原 time-context 刷新规则继续运行。外部入口需要复用/抽取构造能力，不另写时间提示或改成每 step 采样。

## 1. 上游确实提供 Agent Preset

本轮源码固定到 DeepSeek Harness `2e39e70496aa02986ca6a820c5d497e816b69d7e`。

| 上游模块 | 已核对的职责 |
| --- | --- |
| `agent-preset` | 声明 preset 身份和该组合的 Cordis 子插件列表 |
| `agent-preset-registry` | 为 Agent 挂载组合，绑定作用域，管理运行中组合的保留与释放 |
| `persona` | 为特定 Agent 组合提供 persona 前后缀及相关提示词设置 |
| `system-prompt` | 按 Agent 作用域组织提示词和动态上下文贡献 |
| Skill/工具注册表 | 根据作用域向该 Agent 提供实际能力；组合可以装配不同提供者 |

Preset 的公开声明核心为 `id`、可选名称/描述/排序和 `plugins`，不是一份同时写死 system、tools、skills 三个数组的独立运行器。子插件分别贡献这些内容，Host 中仍共享 AgentLoop。资料：[preset 组](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/preset/README.md)、[定义](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/preset/agent-preset-registry/src/definition.ts)、[注册表](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/preset/agent-preset-registry/README.md)。

### 1.1 组合与会话历史是两个维度

- Preset 决定该 Agent 能看到的能力与提示词来源。
- Session 保存消息历史、当前工作目录和所选组合身份。
- Spawn 子 Agent 可以用独立组合和独立起始上下文。
- Fork 子 Agent 会继承父 Agent 已完成 turn 的历史前缀及其组合；恢复后继续读取自己的日志，不重新拷贝父 Agent 的最新历史。

因此，后台主动服务应是 Host 独立管理、具有自己 Session 的 Agent。它与主会话有明确的投递关系，不通过普通 `fork` / `subagent` 工具创建，也不接入普通子 Agent 的自动结束通知。上游 Spawn/Fork 仅作为组合和上下文继承的调研依据，不定义主动服务的生命周期。

资料：[Spawn](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/subagent/subagent-spawn-in-process/src/index.ts)、[Fork](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/subagent/subagent-fork-in-process/src/index.ts)、[作用域 persona](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/preset/persona/src/index.ts)。

## 2. 当前 Go 实现

| 能力 | 当前 Go 实现 |
| --- | --- |
| 每个 Agent 独立 Session / Inbox / turn / step | 已实现 |
| 创建、恢复、Spawn 与 Fork | 已实现 |
| 独立基础 SystemPrompt 和动态来源 | `NativeFactory` 已提供组装入口 |
| 插件 Prompt、工具、Skill 注册 | 已实现，主要按 Host 当前启用贡献组装 |
| `AgentPreset` 身份保存 | 已有 Header/运行身份字段，Schedule 会保存和传递该值 |
| 项目固定组合的真实工具、提示词、Skill | 已实现 Go 装配选择器、一致 Skill 视图与 Host 后台 preset 选择，并通过实际装配测试；尚无通用 Preset 注册和绑定服务 |
| 持久后台 Agent 的逐轮结构化结果 | 已实现独立后台驱动、逐轮包装、原管线提交与日志恢复；未改变 workflow 工具 |

关键代码：`harness/assembly/native_runtime.go`、`harness/agent/native_prompt_assembly.go`、`harness/skills/registry.go`、`internal/plugins/host_prompt.go`、`internal/hostruntime/native_subagents.go`。

单独设置一个新的 SystemPrompt，不能证明组合能力已经还原；如果随后又把全部工具、插件提示和 Skill 加回去，实际模型请求仍没有形成独立组合。

## 3. Go 内置的 Agent 组合能力

**还原上游“每个 Agent 可以选择自己的能力组合”的行为，由 Go 负责装配。**

| 能力 | Go 侧设计 |
| --- | --- |
| 组合身份 | 复用既有 `AgentPreset`；创建时解析，恢复时从日志身份恢复 |
| System 与动态提示 | 选择基础身份和适用贡献，仍走现有逐 step 组装与 append-only system 替换 |
| 工具 | 从真实内置工具、已启用插件工具、MCP 工具中组装该 Agent 的可用表 |
| Skill | 在已有发现结果上形成该 Agent 可见的目录和可加载集合，不复制或重新安装 Skill |
| 运行上下文来源 | 按组合接入，具体内容分层后续确定 |
| 结果契约 | 普通聊天使用普通助手输出；需要机器消费的后台运行可以要求结构化结束 |

初始需要两个项目维护的组合：普通会话组合与后台主动服务组合。组合定义在 Go 的装配层或项目维护的声明中，不新增每用户模型 profile，不让用户为子 Agent 单独填写模型。

这与上游的功能目标对齐，但实现载体有明确区别：我们的 AgentLoop、Session、工具执行器、权限、记忆等是 Go 内置模块；不把它们重新做成 Cordis 插件树，也不宣称可以直接加载上游完整 preset YAML。

### 3.1 组合必须贯穿真实调用

1. 组装先确定真实工具实现与 schema，再产生模型提示和声明。
2. 动态工具、MCP 或插件贡献变化后，下一 step 继续按该 Agent 的组合选择。
3. Skill 清单、`skill` 加载和显式 Skill 调用使用同一可见范围。
4. Skill 可见性不替代工具权限；实际执行仍由 Go 工具系统和现有沙箱/审批控制。
5. 一个 Agent 的选择不修改 Host 全局注册表，也不删除其他 Agent 需要的贡献。

这是一套 Agent 装配能力，不是让 Go 接管 Cordis 插件启停。包安装、启用、停用和插件资源释放仍由已有 pnpm / Cordis 体系负责。

### 3.2 创建与恢复

- 普通会话继续使用默认组合。
- 普通 Fork 保留父组合和既有继承语义。
- 后台主动服务由 Host 独立创建并记录自己的组合，不继承主会话完整历史，不套用普通子 Agent 的自动回报流程。其内部会话可见性需由明确用途控制，不能仅凭现有子会话过滤就宣称已经支持。
- 恢复时找不到组合，应明确失败或暂停该内部功能，不能悄悄改用普通聊天的完整工具表。
- 组合身份在 Agent 生命周期内稳定；已有项目的插件贡献热更新仍在下一 step 按组合重新选择。此处不照搬上游 Cordis 子树 revision 保留机制。

初始普通/后台组合的能力范围见主动服务方案第 13 节，组合在 Go 装配层实现；后台 nextCheck 必填且不能为 null。

## 4. 子 Agent 投递为何没有用户气泡

当前 Go 有两类内部消息：

| 场景 | 来源 | 路径 |
| --- | --- | --- |
| Agent 调用 `send_message` | `agent-message` / `form: relay` / `senderSessionId` | 收件 Agent Inbox，随后参与模型上下文 |
| 普通子 Agent 结束时的运行时通知 | `subagent-settled` / `form: notice` | 收件 Agent Inbox，说明运行结束及结果 |

二者都是真实投递，底层都是 role 为 `user` 的消息，领取后使用 `user/message` 记录。它们不是最初 `subagent`/`fork` 调用的工具结果，也不是把子 Agent 的整段日志并入父会话。`subagent-settled` 并非只留在子 Agent 内部的状态标记；它也是父 Agent 收到的输入。

“user role”是模型消息结构；`source.kind: user` 才表示我们公开聊天中的真实用户输入。`internal/httpapi/chat_transcript.go` 对内部来源进行过滤，SSE 和历史都不显示它们。父 Agent 之后生成的普通 `assistant/message` 才会公开。

上游也使用 `createUserMessage` 构造 `agent-message` 和 `subagent-settled`，没有为跨 Agent 输入新增一种 LLM role。资料：[内部消息构造](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/subagent/subagent/src/continuation-messages.ts)。

主动服务采用相同消息格式，但选择是否投递由后台 Agent 决定：需要触达时发送 `agent-message`；安静结束时两种消息都不发送。它不产生普通子 Agent 的 `subagent-settled`。当前 `SendChildMessage` 有父子关系检查，因此需要复用底层 Inbox 接纳与消息构造，而不是把独立后台 Agent 硬塞进该父子控制 API。

## 5. 结构化结果不要求额外 LLM

上游的 `structured_output` 给需要结构化结果的子 Agent 注册指定 schema，并在最终工具结果成功后捕获值。当前 Go 已有对应的基础工具。

Go 现有顺序：模型调用工具 → 暂存参数 → 设置 `ConcludeTurn` → 工具结果落盘并成功 settle → 当前工具批次完成 → step 结束 → AgentLoop 按结束条件退出并写 turn/end。它不会销毁 Agent、删除 Session 或退出 Host。

这里提及现有子 Agent 激活，仅说明可复用代码当前的适用范围；主动服务仍是独立后台 Agent。需要明确两项现状：

- 同批其他工具不会因这个标记被强行中断；已有 next-step 输入时，当前循环仍可能继续。
- 现有捕获实例只接纳一次结果，且只在特定子 Agent 激活时提供；不能原样复用成持久 Agent 的无限多轮结果。

主动服务的外部驱动器和标准工具负责“每轮一个结果契约”：开始运行前准备捕获状态，已有 `RunNext` 返回后处理本轮的成功结果。普通外部资料变化只更新上下文来源，不调用 steer 去制造 next-step 输入；新的运行请求由服务在前一轮返回后处理。原 AgentLoop 的结束条件、工具批次处理和 Inbox 语义完整保留，不为结构化结果新增强制结束分支，也不依赖文本结束后再调一个编排模型解析自由文本。

资料：[上游结构化结果实现](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/subagent/subagent/src/structured.ts)、本地 `harness/tools/native_structured_output.go`、`harness/agent/tool_context.go`、`harness/agent/native_loop.go`。

## 6. 与主动服务的实施关系

Agent 组合和逐轮结果契约与主动服务共同实施；每次后台 turn 在结束前提交下一次未来检查时间与原因，不设置“仅等待变化”的正常结果。主动服务的行为方案见[主动服务方案](proactive-discovery-plan.zh-CN.md)。

装配、上下文、逐轮结果及 Host 驱动已接通，并通过 Go / Docker 联调、最小真实模型检查及 Web / Android Release 实际交互验收。详细记录见主动服务方案第 14 节；ECI 暂不验证。
