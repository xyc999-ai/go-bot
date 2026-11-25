# 会话投影与上游还原方案

状态：阶段 A、B、C、E 已实现并通过最终回归；阶段 D 评估后不启用持久化检查点。调研与实施日期：2026-11-25。

本项目已还原统一投影注册与领域增量状态，并为历史查询增加有界窗口、冷文件偏移索引、附件归属查询和 SSE 切点处理。Goal、Plan、权限等运行时查询不再反复重放历史；恢复校验、构造、Fork 和导出保留确实需要的完整前缀。持久化投影检查点不启用，当前状态仍以 Session v4 日志为事实来源。

## 1 对照范围与结论

- Go 代码基线：`52707c2f57f1b4a29715cf2ad5ca4c2716c43dfb`。
- 上游源码基线：[DeepSeek Harness 4b399c3](https://github.com/deepseek-ai/deepseek-harness/tree/4b399c39a6d1479cd84ca591b4d7c88e24d38a96)。除明确标注的旧版链接，以下上游链接均固定到该提交。
- 本项目兼容性基线是 `d743267388641bc76f17c45ce8b4c231aed1d32c`，见 [compatibility.md](compatibility.md)。另行核对了该版本的 Goal 服务和投影注册表：当时已经是缓存状态加序号增量推进，见 [旧版 Goal](https://github.com/deepseek-ai/deepseek-harness/blob/d743267388641bc76f17c45ce8b4c231aed1d32c/packages/goal/goal/src/index.ts#L475) 和 [旧版注册表](https://github.com/deepseek-ai/deepseek-harness/blob/d743267388641bc76f17c45ce8b4c231aed1d32c/packages/session/session-projection/src/index.ts#L615)。其他领域的详细对照以下述当前上游基线为准，不据此宣称它们在旧版完全相同。
- 排查范围：生产 Go 的 `harness`、`internal/hostruntime`、`internal/host`、`internal/httpapi`，包括完整事件复制、直接遍历内部 events、纯 fold 的调用链以及已有增量实现。测试支持旧格式不作为生产修改对象；前端渲染和数据库自身的查询计划不在本轮范围。

判断依据是调用频率、所需数据范围和语义边界，而不是看到 `for` 就改。`Surface` 的当前可见消息遍历不等于遍历全部原始历史；恢复期一次完整重放不等于运行时每次查询完整重放。以下扫描清单记录改造前基线，实际实施状态和基准数据见第 8 节。

## 2 上游解决方案

### 2.1 统一投影注册表

上游把日志派生状态的维护交给 `session-projection`，领域只提供 `init`、同步纯函数 `apply(state, event)`、状态版本、校验规则，以及可选的客户端 `wire.view`。注册返回带类型的 handle；服务读取自己的 handle，避免为了一个字段计算所有客户端视图。

注册表监听 `session/event`，维护每个会话每个 key 的状态及 `observedSeq`。新事件到来时立即推进；读取已追平的状态只做水位检查。首次没有 cell 时从历史建立；已有 cell 落后时只处理未消费后缀。上游的这一实现仍有首次历史读取，不应描述成完全不读日志。[注册表源码](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/session/session-projection/src/index.ts#L838)

领域投影不能把未注册或损坏状态默认为空。状态持有内部校验信息，客户端只收到需要展示的字段；一个事件的多项投影变化按同一序号组成通知。[必需读取边界决策](https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/.agents/notes/implemented/architecture/2026-08-19-session-projection-mandatory-seam.md)

### 2.2 各领域如何读取

| 领域 | 上游实际方案 | 还原判断 |
| --- | --- | --- |
| Goal | `goal` 投影维护目标、轮数、历史 ID 和损坏状态；服务组合投影与进程内 activation | 应还原；不要仅在 GoalService 外面套缓存 |
| Plan | `plan` 投影维护模式事实，服务继续拥有待应用意图 | 应还原事实读取，保留意图与事实的区别 |
| 权限 | `sandboxMode`、`approvalState`、权限预设分别由投影维护 | 应还原，避免工具执行和提示词生成时扫描历史 |
| 轮次与步骤 | `turnBoundary` 保存开放轮起点、最近步骤边界和 lastTurn | 应还原，Goal 工具授权和取消也从该边界取所需状态 |
| 工作目录 | `workingDirectory` 投影，服务读取当前目录 | 应还原，Skill 查找无需反复折叠目录历史 |
| 时间上下文 | `timeContext` 投影保存消息时间、注入时间和当前轮浏览器时区 | 应还原每步增量读取，保留本项目现有时区和刷新语义 |
| 子 Agent | `subagentCatalog`、身份和计时投影 | 应还原目录与身份读取，精确保留继承边界 |
| 问答 | `userQuestions` 投影维护请求 schema、工具调用和回复状态 | 应还原；不能只按日志长度变化使完整缓存失效 |
| 用量 | `tokenUsage`、`sessionStats` 等分别增量累计 | 应还原增量方式；Go 的 UsageSnapshot 字段口径继续保留 |
| 模型输入 | Session 的 `deriveMessages` 在纯尾部增长时复用派生消息，替换/投影变化时重建；`toolHistory` 和 request header/context 也增量维护 | 应还原所需索引和缓存，按可见上下文重建，不按全部历史重建 |
| Skill 目录 | 当前上游仍倒序 `eventAt` 查目录是否发布及是否可见 | 不能宣称上游已解决；可用领域投影改善，属于有明确理由的 Go 优化 |
| Goal 工具授权 | 先读 `turnBoundary` 获取当前轮起点，再检查该轮事件；仍使用完整快照入口 | 应还原轮边界与当前轮窗口，不能照搬历史读取开销 |

对应实现：[Goal][up-goal]、[Plan][up-plan]、[sandbox][up-sandbox]、[审批][up-approval]、[权限预设][up-permission]、[轮边界][up-turn]、[工作目录][up-cwd]、[时间上下文][up-time]、[子 Agent 目录][up-children]、[问答][up-questions]、[Token 用量][up-usage]、[统计][up-stats]、[模型输入][up-session]、[Skill 目录][up-skill]、[Goal 授权][up-authority]、[Agent preset][up-preset]。

### 2.3 可选的持久化投影检查点

上游另有 `session-projection-cache`，把投影状态、版本和序号作为可丢弃缓存保存到存储域。JSON 后端路径为 `<storages>/session_projcache/sessions/<id>.json`。它服务冷会话列表和恢复优化，不替代 Session JSONL 日志。[检查点包说明][up-cache]

其边界是：先确保日志持久化，再保存检查点；检查点可以落后，不能领先日志。恢复检查版本、schema、会话身份和继承 cut，不匹配则回退重放。创建、`turn/end`、退出有写入点，中间按数量和时间节流。该能力是独立层，首批运行时增量化无需同步新增这些文件。

### 2.4 上游也尚未完成的部分

上游已弃用新增的同步任意历史读取，但仍允许遗留调用逐步迁移；当前 Session 仍保留完整内存日志。其 `snapshotEvents` 在没有新 append 时复用冻结数组，事件本身冻结共享，不是每次深拷贝整个 JSON。Go 当前 `Events()` 在锁内深拷贝全部事件 JSON，照搬上游遗留快照调用会有不同成本。[Session 源码][up-session]

因此还原目标应是领域状态通过投影读取、历史通过明确窗口读取，而不是机械对应 TypeScript API。首轮改造也不承诺解决全部历史驻留内存的问题。

## 3 本项目扫描清单与处理判断

P1 表示首批运行时应修改；P2 表示随后还原或优化；保留表示用途合理。下表按职责合并调用点，路径为排查入口。

| 项目 | 当前代码与触发 | 上游对应与结论 |
| --- | --- | --- |
| Goal 查询 | `harness/goal/native.go:getLocked` 每次 `FoldGoal(Events())`；续轮和 claim guard 均调用 | P1，还原 Goal 投影读取 |
| Goal 写入校验 | `harness/session/live.go:appendLocked` 遇到 Goal 变更/轮消息时 `FoldGoal(append(s.events, event))` | P1，严格语义应保留，改成当前 reducer 状态验证单事件，不能仅修查询 |
| Goal 工具授权 | `harness/tools/native_goal.go:nativeGoalExecution` 复制全部事件、找当前轮，再遍历轮内消息 | P1，还原 turnBoundary，读取当前轮来源状态或有界窗口 |
| Plan | `harness/plan/native.go:View/Queue/appendLocked` 多次全量 FoldPlan；视图参与每步提示词 | P1，还原 Plan 投影，pending 意图继续由服务拥有 |
| 权限和父级上限 | `harness/permission/permission.go:Read/Save`、`approval.go`；assembly 权限闭包、子 Agent ceiling、Runtime Get/Set | P1，还原权限投影；父级上限仍实时读取父级投影，不能缓存成启动值 |
| 工作目录 | `harness/assembly/native_runtime.go:skillLookup/nativeSessionCWD`，Skill、交付路径与上下文获取时触发 | P1，还原工作目录投影 |
| turn/step 编号 | `harness/agent/native_loop.go:nextNativeTurn/currentNativeStep`，每轮及异常收尾 | P1，还原轮边界/执行游标；异常关闭不能失去最新 step |
| 模型输入工具名称 | `native_loop.go:nativeModelMessages` 每次请求/重试从所有 tool/call 重建 callID→name | P1，建立所需调用元数据索引；可见消息解析成本与历史索引成本分开处理 |
| 系统提示词已有消息 | `native_loop.go:ensureSystem` 为读取 surface 首节点调用 Events | P1，当前 surface 的带类型节点/消息足够，去掉整份深拷贝 |
| request/header 是否存在 | `native_loop.go:logRequest` 在每次路由重置 requestLogged 后再扫描 | P2，还原 Session 的 header/context 增量状态 |
| 时间上下文 | `native_time_context.go:ensureTimeContext/SampleTimeContext` 每步或外部采样扫描历史 | P1，还原 timeContext 投影，包括当前轮时区集合及进入当前 step 前的时间基线 |
| Skill 目录发布历史 | `native_skill_catalog.go:ensureSkillCatalog` 在当前可见目录缺失时倒序查全历史，先已发生深拷贝 | P2，上游仍有遗留扫描；维护 everPublished 与当前可见目录信息，替换后不能只凭历史摘要去重 |
| 图片卸载 | `native_image_offload.go:offloadNativeImagesFromSources` 实际围绕可见 surface，但为类型/有效性检查获取全 Events | P1 去掉全量复制；继续按可见节点和图片序号卸载 |
| 工具结果裁剪 | `native_prune.go:pruneNativeToolResults` 扫描完整历史判断 openTurn，并按 seq 查工具事件 | P1 去掉全历史复制/轮状态扫描；当前 surface 内容遍历保留 |
| 上下文摘要 | `native_compaction.go:selectNativeCompactRange/nativeSummaryMessages` 将完整 Events 用作选区元数据及首消息查询 | P2，选区需要的是当前 surface 和选中节点元数据，保留摘要区域遍历 |
| Usage | `internal/hostruntime/native_runtime.go:Usage` 每次 `ProjectUsage(DurableEvents())` | P2，还原持久化水位上的用量投影；不把 Go 遥测指标换成上游不同口径 |
| Activity | `harness/session/activity.go:DurableActivity` 锁内扫描全部已持久化事件，没有深拷贝；列表、归档、后台、子 Agent 状态会读取 | P1，增量保存最大事件时间与持久化轮状态；队列/运行任务等忙闲条件继续组合 |
| 待回答问题 | `native_runtime.go:pendingQuestionsLocked` 同水位复用，但任何新持久化事件都会触发全量 FoldTimedQuestions | P2，还原问答 reducer；当前 schema 模式及 continued/回复状态都要保留 |
| 子 Agent 目录/身份 | `harness/subagent/native_catalog.go`，`native_subagents.go` 发送/列出/恢复查目录，assembly 冷恢复读 descriptor | 活跃目录读取 P2 还原投影；冷恢复校验与继承判定保留 |
| Agent preset | `native_agent_preset.go` 查锁定与有效 preset，再构造完整 artifact | P2，读状态还原 agentPreset 投影；确实需要的重建 artifact 单独保留 |
| 取消某轮 | `native_runtime.go:CancelTurn` 扫描所有持久化 start/end | P2，复用轮边界；必要的历史轮存在性由轮索引回答，保留不存在/已结束/运行中的错误区分 |
| SSE 初始状态 | `internal/httpapi/host_stream.go:followHostSession` 全量取历史、FoldInbox、找活跃轮，虽然接下来流是增量 | P2，活跃会话当前状态读投影，初始历史读窗口；连接切点与去重保持一致。上游历史通知按连续 seq 消费事件/后缀，见 [listener][up-history] |
| SSE 指定旧游标 | `native_worker_http.go:nativeWorkerStream` 为游标处 Inbox 读取整个前缀并折叠 | P2，历史切点不能换成最新 Inbox；按切点恢复，或调整握手使当前状态和增量切点一致 |
| 聊天分页 | `native_host_http.go` 全量取事件后 `makeChatTranscript` 截取页，最新页另 FoldInbox；`coldChatQuestions` 全量折叠问答 | P2，区分逻辑分页和物理读取；活跃状态走投影，历史走窗口。上游也仍有对给定前缀切页的函数，不能认为它所有后端都已物理分页 |
| 冷历史大文件 | `session_history_http.go:coldHistoryCache` 只缓存 ≤8 MiB 的单个 artifact，大文件分页会重新读取、校验全文件 | P2 独立存储工作，还原按需历史访问方向；仅加投影无法解决历史内容分页 |
| 附件归属查询 | `internal/httpapi/session_attachment.go:attachmentFromEvents` 每次查附件扫描给定历史 | P2，按会话维护附件归属索引；不可绕过归属校验。本轮未发现必须逐字还原的上游同构接口 |
| 主动服务投递幂等 | `internal/hostruntime/proactive_message.go:acceptProactiveOnce` 查 Inbox 后扫描所有已持久化 user/message | P2，按持久化消息 ID 建幂等索引；这是 Go 产品逻辑，采用投影机制而非宣称存在同名上游实现 |
| 主动服务完成结果 | `internal/host/proactive.go:execute/readResult/recover` 通过 `CommittedResults` 重建全部结果 | 运行中 P2 保存最近完成结果和必要查找索引；恢复期重放保留。成功 tool/result 与 completed turn/end 缺一不可 |
| 搜索索引 | `harness/sessionsearch/index.go` 与 `internal/host/history.go`，活跃会话按 Next 游标取后缀；冷会话文件变化时全读再截尾 | 活跃增量路径保留；冷源读取与磁盘分页一起处理，不能误报为索引每次全量重建 |
| 恢复、检查、构造、Fork | `session/live.go:replaySession`、`inspect.go`、`relationships*.go`、`repair.go`、assembly creation/fork、FileStore 读写 | 保留必要完整前缀读取与严格校验；后续减少同一次恢复中的重复 fold，不能删除语义校验 |
| 纯 fold 工具函数 | `ProjectTodos`、`FoldModelSelection` 等 | 存在全量函数不等于热路径；保留离线重放入口，迁移调用方而不是删除函数 |

已有合理实现应复用：`SurfaceState.Append` 和 Inbox 已逐事件推进；`LiveSession.ModelSelection` 已保存状态与消费游标；`native_pressure_meter.go`、`native_instructions.go` 的文件触碰扫描、`DurableEventPage` 和活跃搜索后缀读取已限定范围。它们不是每次扫描全历史的问题。

## 4 应还原的架构及 Go 边界

### 4.1 采用统一投影机制

还原上游的三个角色：Session 接纳并发布事件；领域定义确定性的 reducer；投影注册表持有每会话状态和水位。Go 不复制 Cordis 生命周期或 TypeScript 类型合并，采用静态内置注册与带类型的读取 handle。当前业务插件不获得修改会话事实或续轮逻辑的入口。

设计契约为定义、带类型的 handle、按 cut 读取和可选 view。最终 Go 接口使用 `ProjectionDefinition{Key, Version, Init, Apply, View}`、`RegisterProjection` 和 `ReadProjection`；由读取 selector 复制所需字段，不为整个领域状态提供任意可变引用。必需 key 在发布 NativeLive 前完成注册/重建，缺失、版本不兼容或折叠失败显式报错。

Session 接纳事件必需的 Goal 转移等核心 reducer 随构造/恢复初始化，不能等到工具服务发布后才注册；其余内置领域在 assembly 发布前完成注册并追平已有 seed。`cut` 首批仅指 live 或 durable 两种前缀，不提供任意历史序号的同步查询承诺。暂不还原上游动态插件注册释放、保留 handle 及远程 wire 协议；这些能力不能作为 Go 内部增量维护的前置依赖。

小状态只复制返回值及其可变字段。日志内部数据仍由 Session 拥有；不能把 `Events()` 改成返回可修改的内部切片来省分配。首批不新增公开同步任意历史读取 API；现有 surface 消费者可取得所需节点的类型和来源信息，由投影内部完成旧事件访问迁移。

### 4.2 事件接纳与磁盘持久化必须分开

上游文档的 committed event 指 Session 接纳事件，不能直接等同 Go 的 `Flush/fsync` 完成。Go 已明确区分 live prefix 和 durable prefix，投影必须显式带 cut：

- live 状态回答当前模式、当前目标、工具校验和当前执行边界，跟随已接纳事件。
- durable 状态回答用量、对外已持久化历史及幂等事实，只消费到 `s.durable`。
- API 同时返回多种状态时，用同一快照切点；不得先读最新投影再搭配旧游标的历史。

统一 reducer 和推进器可服务不同 cut，但两份视图不得互相污染。已有 Activity 对 live Inbox 的忙闲保护需要明确保留，不能仅因为命名 Durable 就删除未落盘输入对 busy 的影响。

### 4.3 保留批量追加的原子性

当前 `AppendBatch` 在 shadow Session 中验证整批，全部成功才替换主状态。投影更新也应先落到候选状态，与 events、surface、inbox 一起提交；失败不得污染真实状态。map/slice/pointer 不能浅拷贝后直接修改共享数据。通知在整批接纳且投影推进完成后发布，读取不得看到半批状态。

保留 Go 现有 Goal 非法状态迁移在入日志前被拒绝的行为。上游 Goal 投影会保存 failure 供读取拒绝，这不构成降低 Go 入站校验的理由。具体使用同一 Goal reducer：候选事件预校验、实际投影更新、离线 FoldGoal 共用状态迁移规则，避免三套逻辑漂移。

### 4.4 Goal 所需状态

内部状态至少包含当前目标、轮数、时间、LastRef、所有见过的 goal ID 和错误状态。只缓存 `GoalSnapshot` 会漏掉 clear 后的历史 ID 重用约束。`activation` 保留在 GoalService 进程态，冷恢复始终 disarmed，检查点也不能把它恢复为 armed。

轮数仅在真正接纳到模型会话的 Goal `user/message` 时递增；Inbox 入队、重复 claim、被用户消息抢占都不能提前计数。创建/更新的 Flush 和失败后解除 activation 的行为保留；不以缓存成功替代持久化成功。

Goal 工具读取当前轮来源投影，或通过已维护的当前轮边界取有限窗口。当前轮结束即失效，直接用户身份、子 Agent 限制、精确 goal ID/revision/round 校验仍适用。不能持久保存一个跨轮有效的 `hasHumanInput` 布尔值来代替授权。

### 4.5 模型上下文缓存按上游的失效规则还原

上游 `deriveMessages` 的重要点不是只记录“最后一个 seq”：纯尾部增长可追加缓存，compaction replace、prune、image/offload 等内容投影变化会使旧派生内容失效，必须按 surface generation 重建。Go 的模型数据转换缓存、调用索引和首系统消息读取也应遵循这一边界。

当前可见消息最终还要遍历并发给模型，这是合理的输出成本。目标是去掉不再可见的历史复制和工具索引重建，而不是承诺每次模型请求全部 O(1)。工具名关联优先来自可见调用/来源元数据；需要保留历史索引时只保留必要字段，避免复制完整工具结果 JSON。

## 5 还原范围与迁移顺序

| 阶段 | 内容 | 是否新增持久化文件 | 完成标准 |
| --- | --- | --- | --- |
| A | 建立统一投影驱动，首先迁移 Goal 与 turnBoundary，替换读取、写入全量校验和工具授权扫描 | 否 | 同水位 Goal 查询不读取 Events；追加只验证新事件；既有日志语义不变 |
| B | 还原 Plan、权限、工作目录、时间上下文与执行/Activity 状态；去掉系统消息、图片卸载、prune 的全量快照 | 否 | 每步小状态读取不随历史增长；父级权限与异常收尾保持实时 |
| C | 问答、子 Agent 目录、Usage、preset、模型派生消息/调用元数据缓存，以及产品特有幂等/后台结果投影 | 否 | 只消费新增事件；替换 surface 正确使缓存失效；已持久化视图严格受 cut 限制 |
| D | 评估并选择还原上游 session-projection-cache，用于冷列表和状态读取 | 是，如选择 JSON 检查点后端 | 有实际冷读需求和基准收益；日志先落盘、缓存可丢弃、版本/身份/cut 校验完善 |
| E | 磁盘历史窗口、轮次/附件等必要索引、SSE 与历史握手 | 可有可重建索引 | 返回一页不会暗中反复完整载入大文件；仍检查会话归属、历史切点及日志完整性 |

A 至 C 已还原上游机制，E 也已完成独立的磁盘历史窗口改造；实现与验证见第 8 节。D 为有条件还原：上游把它设计成可选层，本轮测量后选择不启用，理由与边界见 8.4。每阶段保留纯 fold 作为恢复和差分校验入口。

不采用给 GoalService 单独增加一个缓存字段然后结束的方案：那会遗漏写入前重放和其他高频路径，也无法统一处理事件水位、批量失败和状态通知。

## 6 验证与验收

### 6.1 语义验证

对已有 fixtures 及生成的有效事件序列，逐前缀比较“从头 Fold 的结果”与“注册表增量结果”。覆盖 create/edit/pause/resume/complete/block/clear、重复 ID、过期 revision、轮数上限和非法状态迁移。冷恢复后的 Goal 事实应一致，activation 必须 disarmed。

批量追加中后部失败必须让事件列表及所有投影保持原样；拒绝事件不发布通知。覆盖 map 共享污染、并发读取、取消后写入完成、不确定 fsync、已确认回滚后重试、live 与 durable 水位不同，以及恢复补写的 interrupted-turn closers。

上下文测试覆盖 compaction replace、prune、image/offload、空内容首节点、Skill 目录被压缩移除和 fork inherited cut。授权测试覆盖用户输入抢占 Goal 轮、当前轮结束后旧授权失效、父级权限变化实时生效、子 Agent 不获得根会话直接用户权限。问答保留超时继续和晚到回复，后台结果保留工具成功加轮完成的双条件。

使用项目已锁定的上游严格解码/重放验证确保产出日志未改变 v4 合同；具体环境和方式沿用 [compatibility.md](compatibility.md)。本轮设计不升级协议基线。

### 6.2 性能验证

实施前后采用相同有效长会话，分别测试约 1 千、1 万和 10 万事件，并增加含大工具结果的样本。记录事件 JSON 总字节量、查询/追加/模型准备的 ns/op、B/op、allocs/op，以及恢复和冷历史分页成本。

至少比较：连续无新事件 Goal 查询；单事件/单轮新增后的查询；Goal 事件追加；无关事件追加；权限和 Plan 读取；模型输入构造；问答/Usage 更新；AppendBatch 扩大后的成本。性能检查要验证实际消费事件数，避免“缓存了但每次先复制全日志”的伪增量。

验收要求：小状态同水位读取成本不随历史长度增长；追加的 Goal 转移校验不随旧日志字节量增长；首次恢复仍可线性处理历史；模型准备成本与当前 surface 及必要新增数据相关。暂不设没有测量依据的毫秒阈值。

实现验证按贡献指南运行 `make test vet check`、相关 race 测试与 Docker 集成；实施结果见第 8 节。

## 7 排查复现方式

先定位所有完整快照入口及 fold 调用，再检查消费者是否在循环内或每步重复触发，并检查内部切片的直接遍历：

```sh
rg -n '\.(Events|DurableEvents|DurableEventsFrom|DurableSessionEvents)\(|Fold[A-Za-z]+\(|ProjectUsage\(|ProjectTodos\(' harness internal --glob '!**/*test*' --glob '!internal/testsupport/**'
rg -n 'for .*range .*events|for .*range .*Events|s\.events' harness/session harness/agent internal/hostruntime internal/httpapi internal/host --glob '!**/*test*'
```

纯函数定义、一次恢复调用、后缀扫描、当前 surface 扫描、锁内原始事件扫描和全量深拷贝必须分别归类。上表记录的是职责判断，不以关键词命中数作为问题数量。

[up-goal]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/goal/goal/src/index.ts
[up-plan]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/plan/plan-mode/src/index.ts
[up-sandbox]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/sandbox/sandbox-policy/src/index.ts
[up-approval]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/interaction/user-approval/src/index.ts
[up-permission]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/interaction/permission-presets/src/index.ts
[up-turn]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/core/agent/src/projection.ts
[up-cwd]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/session/working-directory/src/index.ts
[up-time]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/context/time-context/src/index.ts
[up-children]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/subagent/subagent/src/catalog.ts
[up-questions]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/interaction/user-questions/src/projection.ts
[up-usage]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/llm/token-meter/src/usage-projection.ts
[up-stats]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/session/session-stats/src/projection.ts
[up-session]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/core/session/src/index.ts#L696
[up-skill]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/skill/tool-skill/src/index.ts#L390
[up-authority]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/goal/tool-goal/src/authority.ts
[up-cache]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/session/session-projection-cache/README.md
[up-preset]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/preset/agent-preset-registry/src/session.ts
[up-history]: https://github.com/deepseek-ai/deepseek-harness/blob/4b399c39a6d1479cd84ca591b4d7c88e24d38a96/packages/api/session-controller/src/history/listener.ts

## 8 实施记录

### 8.1 已实现的运行时还原

`harness/session/projection.go` 提供带类型定义、注册 handle、消费水位、可选小视图和 live/durable 两种 cut。live 状态在整批接纳后推进；durable 状态按读取需求只推进到 fsync 水位。没有新建 Goal 文件或持久化投影检查点。领域 reducer 可以维护私有可变索引，但只能在批量接纳完成后驱动；读取 selector 返回需要的分离副本，不能暴露私有 map。

Goal 的状态转移在候选 Session 上逐事件校验，失败整批回退；复用同一 reducer 完成离线 FoldGoal 与在线推进，历史 goal ID 集合在 create 时隔离，冷恢复不恢复 activation。轮边界保存当前轮的直接用户来源和精确 Goal 轮身份，Goal 工具不再复制历史或跨轮继承授权。

Plan、执行权限、当前工作目录、时间上下文、轮次状态、Activity、工具调用名称、Agent preset、Skill 目录发布事实、子 Agent 目录、Usage、待回复问题、主动服务投递幂等和已完成后台决策已转为增量状态。保留父级权限上限实时读取、子 Agent 继承 cut、超时问题晚到回复、后台工具成功且轮完成后才能认定结果等行为。

`NativeLoop` 的模型消息缓存只保留当前可见节点，尾部新增复用旧消息；原消息内容改变时重新投影，移出 surface 的缓存项被删除。返回模型的数据包含可变内容的副本；文件路径 resolver 改变时清空缓存。当前 surface 同时提供节点类型、节点消息和工具结果数据，系统消息判断、裁剪、图片卸载和摘要不再为按序号定位而复制完整日志。

额外发现 `SurfaceState.Snapshot` 会复制所有历史 replacement 诊断记录，即使当前上下文已很短。运行时现改用 `ModelSurface/ModelSnapshot`，只读取当前上下文；完整 `Surface/Snapshot` 保留历史诊断用途和既有行为。这避免把完整事件扫描替换成另一种不断增长的历史元数据扫描。

### 8.2 基准

本地 Darwin arm64 Apple M5，合成合法日志，每条填充事件含 512 字节文本。命令为 `go test ./harness/session -run '^$' -bench BenchmarkGoalProjection -benchtime=50ms -count=1`。下表是一次短采样，用于确认复杂度与分配趋势，不代表生产请求端到端延迟。

| 填充事件数 | 增量 Goal 查询 | 查询分配 | 完整复制及重放 | 完整复制及重放分配 | 增量 Goal edit 追加 |
| --- | --- | --- | --- | --- | --- |
| 1 千 | 54.58 ns/op | 104 B/op | 93.76 µs/op | 696,697 B/op | 16.57 µs/op |
| 1 万 | 38.11 ns/op | 104 B/op | 721.86 µs/op | 6,888,299 B/op | 19.19 µs/op |
| 10 万 | 41.68 ns/op | 104 B/op | 13.48 ms/op | 68,812,652 B/op | 16.99 µs/op |

Goal 查询固定 2 次分配。追加仍承担 JSON 编解码、合法性校验和日志数组增长，批量容量扩展可增加分配；消除的是随着旧事件总字节量重复增长的扫描工作。

补充基准覆盖权限、Plan、Usage、空待答问题列表、固定模型上下文、无关事件追加和批量追加。1 千至 10 万事件下，权限/Plan/Usage/空问题列表读取分别约 17–20/11–12/25–31/20–40 ns/op，均为零分配；固定可见消息的模型投影约 0.3–0.6 µs/op、1128 B/op。无关单事件追加约 1.1–1.3 µs/op，32 条批量追加约 27–29 µs/op，成本随新增批量规模变化。

真实 `tool/result` 含 16 MiB 文本时，Goal 缓存查询约 37 ns/op、104 B/op，完整复制重放约 388 µs/op、16.8 MB/op。此样本用于区分事件数量与单条大结果的影响。

`BenchmarkIndexedHistoryWindow` 在首次完整校验和建索引之后读取固定两轮：1 千、1 万、10 万填充事件下分别约 34.7、30.4、31.4 µs/op，约 22 KB/op。首次校验、文件版本失效或索引被淘汰后的重建不包含在这些热读数据中。

补充一次采样的 `BenchmarkColdObservationAndRestore`：1 千、1 万、10 万事件首次冷文件状态读取分别约 22.7、168、1652 ms，累计分配约 16、160、1617 MB；对已经解码的前缀执行 `RestoreLiveSession` 分别约 6.6、57.5、532 ms，累计分配约 6.5、66、673 MB。这些是线性恢复成本，分配量不是常驻内存或峰值；本次未优化首次冷读。相同规模的 `BenchmarkGoalRoundAdmission` 新增 Goal 消息约 25–32 µs/op、15456 B/op，未随旧历史增长。命令：`go test ./harness/session -run '^$' -bench 'Benchmark(ColdObservationAndRestore|GoalRoundAdmission)$' -benchtime=1x -count=1`。

### 8.3 验证记录

- `GOFLAGS=-p=2 make test vet check`：最终源码通过。限制的是同时编译的包数，测试包范围不变。
- 相关 session、Goal、Plan、interaction、Agent、tools、assembly、subagent、Host/runtime、proactive、HTTP race 测试：通过。
- 新增注册冲突、同前缀不重复消费、live/durable 隔离、整批拒绝后私有可变状态不变、Goal 返回值隔离、并发读写、fsync 已确认回滚后重试测试，以及模型缓存副本与图片卸载失效、当前 surface 与完整诊断隔离测试。
- Docker 测试：最终源码的会话恢复与文件、Host 流多会话与审批重连、主动服务静默/定时通知/恢复、Schedule 与附件冷恢复、Web 聊天预览、队列与问答六项全部通过。测试镜像复用本机现有 Host 运行环境，覆盖本次源码编译的 Linux arm64 Host 可执行文件；未发布镜像。
- 锁定上游 `d743267` 的 `scripts/verify-upstream.mjs` 严格解码与 `Session.fromRestore`：最终源码导出的 Goal、超时问答 pending、图片卸载冷恢复、模型重试四类日志通过。
- 历史窗口补充测试覆盖大于旧 8 MiB 缓存上限的文件复用偏移索引、文件替换和损坏失效、可恢复尾部只读、修复后重建、旧轮游标、live/durable 一致性、并发读取、附件的会话与名称归属、SSE 重连不重复应用旧队列 splice。
- 搜索源测试覆盖多页完整消费、每页至多 500 条、稳定文件不重读、分页中断后数据库标记未完成并安全重建。

### 8.4 检查点决策与保留的边界

阶段 D 不启用检查点存储域：当前冷读已经复用内存中的文件观察状态，热读无需反复折叠日志；持久化检查点不能替代首次读取的日志完整性和关系校验。它仍可用于未来降低进程重启后的冷状态读取成本，但本次不增加额外的权威状态、JSON 缓存文件或写入链。

完整日志仍驻留活跃 Session 内存。首次读取一个冷文件版本仍执行完整校验和状态重建；文件的 inode、大小或修改时间变化后重建索引。内存缓存最多保留八个文件观察；被淘汰的文件再次访问也要重建。恢复、Fork、构造、导出与 preset 重建保留其所需完整前缀。未配置窗口后端的旧适配器保留完整 artifact 回退，生产 Host 装配使用窗口后端。

### 8.5 历史窗口实施

`HistoryRequest` 明确区分事件页、轮次页、小状态和附件查询。`LiveSession.HistoryWindow` 在一把 Session 锁内选择 durable 事件范围、轮边界、Inbox、权限及领域小视图，附带同一事件数量切点。旧页面请求不会把当前 Inbox 当作旧游标处的 Inbox。模型尝试流和当前等待交互继续使用运行时自己的 process-local 基线。

`FileHistoryReader` 首次严格读取并重建只读观察，保存行偏移、轮起点、附件元数据和控制状态，释放完整事件正文。后续热读通过 `SectionReader` 仅解码请求范围；读取前后检查文件身份与版本，变化时重新观察。恢复尾部策略仍由原 FileStore 检查器拥有，历史读取不修复文件、不取得 writer claim。

生产 Host 的 transcript、冷 events、permissions、questions、附件、冷流回放和搜索源接入该后端。活跃 Host SSE 初始状态使用当前观察和最近轮窗口；旧游标回放按小页发送到准确的 opening cut，再消费后续事件。旧 per-session SSE 初始化保存当前队列 cut，跳过已反映在该状态里的历史 splice，避免重连重复入队或删除。

搜索源每页至多 500 个事件，索引逐页写入 SQLite 并在消费全部页之后才报告同步完成。中断的冷分页把数据库文件版本标记为未完成，下次安全重建；活跃源继续使用实际 fsync 水位。SQLite 表结构和搜索结果合同不变，不再把所有源页拼成一份大事件切片。
