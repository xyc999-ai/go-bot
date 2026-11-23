# Hermes Agent 记忆机制与 Go Harness 还原方案

> 历史设计与调研记录。当前行为、配置和命令以[文档索引](README.md)中的指南为准。

调研固定到 NousResearch/hermes-agent 提交 `415fa335b4d98ecf00e29e88625bb478dc63fb67`。Hermes 内建两份有容量上限的 Markdown 记忆，自动随新会话加载，并用 `session_search` 按需查旧对话。 [固定提交的记忆指南](https://github.com/NousResearch/hermes-agent/blob/415fa335b4d98ecf00e29e88625bb478dc63fb67/website/docs/user-guide/features/memory.md)

## Hermes 如何保存、读取和查询

| 能力 | 实现 | 使用时机 |
| --- | --- | --- |
| 用户资料 | 当前 profile 的 `memories/USER.md`，默认 1,375 字符 | 沟通偏好、常用语言、长期约定 |
| Agent 笔记 | 同目录的 `memories/MEMORY.md`，默认 2,200 字符 | 环境事实、项目约定、已验证的经验 |
| 历史查询 | 当前 profile 的 `state.db`、SQLite FTS5、`session_search` | 查上次决定、原话及前后文 |
| 外部 provider | 独立的 provider 扩展接口，可提供工具、检索、轮次同步等 | 需要更深的检索或远端记忆时选用 |

两份 Markdown 默认启用，以独占一行的 `§` 分隔条目，单条允许跨行。Hermes 按 Unicode 字符计预算，分隔符也计入总长。默认 profile 的目录为 `~/.hermes/memories/`，命名 profile 使用各自的 home。 [记忆文件实现](https://github.com/NousResearch/hermes-agent/blob/415fa335b4d98ecf00e29e88625bb478dc63fb67/tools/memory_tool_store.py#L177) · [Provider 接口](https://github.com/NousResearch/hermes-agent/blob/415fa335b4d98ecf00e29e88625bb478dc63fb67/agent/memory_provider.py#L84)

### 写入

模型依据系统提示主动识别长期偏好、用户纠正和可复用经验。用户说「记住」也会触发同一个 `memory` 工具。以当前源码中对模型公开的批量参数为准，一次只操作一个目标。

```json
{
  "target": "user",
  "operations": [
    { "action": "add", "content": "用户希望日报用中文。" }
  ]
}
```

- **add** 新增条目。相同文本再次添加返回成功且只保留一条。
- **patch** 根据 `old_text` 定位一条记忆，再替换该条目中的唯一匹配片段。其他内容保留。匹配歧义或片段重复时返回错误。
- **remove** 根据 `old_text` 定位并删除整条。整理容量时，可以把缩短、删除和新增放在同一批，按最终结果检查字符上限。任一操作校验失败则整批不提交。

写入在文件锁内重新读盘，校验后用临时文件加原子重命名提交，避免覆盖并发修改。成功返回目标、用量和条目数等状态。超限会反馈可用于整理的条目和剩余容量，模型可以缩短旧条目后重试。工具成功或文件确实落盘才表示已保存。Hermes 也会在写入和加载提示词时检查已知的危险文本模式。 [工具 schema](https://github.com/NousResearch/hermes-agent/blob/415fa335b4d98ecf00e29e88625bb478dc63fb67/tools/memory_tool.py#L373) · [文件事务与批量语义](https://github.com/NousResearch/hermes-agent/blob/415fa335b4d98ecf00e29e88625bb478dc63fb67/tools/memory_tool_store.py#L353)

### 自动读取

新会话加载两份文件，将非空内容、类别和用量拼进系统提示词。该会话使用固定快照，保持提示词前缀稳定。写操作立即修改磁盘，新开的会话读取更新后的内容。已经开始的会话仍使用自己的快照，同时会在当前对话里看到写入工具的结果。模型公开的 `memory` 工具没有独立的 `read` 或 `search` 操作。 [冻结快照的实现](https://github.com/NousResearch/hermes-agent/blob/415fa335b4d98ecf00e29e88625bb478dc63fb67/tools/memory_tool_store.py#L223) · [提示词快照与写入回执](https://github.com/NousResearch/hermes-agent/blob/415fa335b4d98ecf00e29e88625bb478dc63fb67/tools/memory_tool_store.py#L655)

### 查询过去的对话

当用户提到上次的讨论或需要找回旧决策，模型可以调用 `session_search`。Hermes 用 FTS5 在历史消息上做关键词、短语、布尔和前缀匹配。调用有四种形态，由参数决定。

| 参数 | 返回 |
| --- | --- |
| `query` | 按相关度发现历史会话，返回命中片段和消息锚点。默认取 3 个会话，最多 10 个，可按角色、时间过滤。 |
| `session_id` + `around_message_id` | 读取命中消息前后的一小段原文。 |
| `session_id` | 读指定会话。较长会话返回有边界的首尾内容。 |
| 无参数 | 浏览最近会话。 |

查找和读取直接用已保存的消息，无需调用模型生成检索摘要。源码会限制窗口和返回文本量，默认搜索也会规避已在当前上下文中的会话分支。宿主可在工具入口绑定自己的访问范围。 [查询分派和权限范围](https://github.com/NousResearch/hermes-agent/blob/415fa335b4d98ecf00e29e88625bb478dc63fb67/tools/session_search_tool.py#L577) · [查询语法与结果](https://hermes-agent.nousresearch.com/docs/user-guide/sessions/#session-search-tool)

## Go Harness 的还原方案

**把 Hermes 的本地记忆机制嵌入 Go Host。** 一个账号的多个会话共用一份长期记忆，Host 管理文件和工具。Controller 提供现有账号路由和 Host 生命周期，前端通过现有工具卡展示保存结果。

### 账号存储与写入

| 内容 | 位置 | 职责 |
| --- | --- | --- |
| 用户偏好 | `/root/harness/.dsh/memories/USER.md` | 同账号共享，默认上限 1,375 字符 |
| Agent 经验 | `/root/harness/.dsh/memories/MEMORY.md` | 同账号共享，默认上限 2,200 字符 |
| 会话日志 | 原有 `session.v4.jsonl` | 记录实际使用的系统提示词和对话，供会话恢复、索引重建和上下文展开 |
| 历史全文索引 | Host 容器本地的 `/var/lib/go-harness/session-search.sqlite` | 用 SQLite FTS5 查询账号内的历史会话 |

沿用 Hermes 的 `§` 分隔和 `memory(target, operations)` 契约。每次调用在 Host 内持有共享写入互斥，重新读目标文件，按顺序试跑批次，对最终内容按 Unicode 字符计数，再复用原子文件写入。相同文本新增视作成功，歧义匹配和超过容量返回可处理的错误。不可读或无效 UTF-8 文件保留原样。两份文件缺失时视为空。Agent 根据前台指引记录用户明确表达的偏好、纠正和可复用经验，成功回执以工具返回为准。加载与写入都做基本内容校验，检索结果作为历史数据传给模型。 [工具组装](../harness/assembly/native_runtime.go) · [原子文件写入](../internal/localstate/store.go)

### 每个模型步骤加载当前记忆

系统提示词参考 Hermes 的记忆引导。跨任务都适用的用户资料、稳定环境事实和长期约定适合保存为记忆，任务相关的经验放入技能，短期信息留在会话历史。用事实陈述写记忆，容量不足时整理旧条目。新增、修改和删除的参数由 `memory` 工具说明提供。涉及先前对话时，先用 `session_search` 查找，再请用户补充。Go Harness 的提示词说明当前记忆每个模型步骤都会加载。 [Hermes 提示词源码](https://github.com/NousResearch/hermes-agent/blob/415fa335b4d98ecf00e29e88625bb478dc63fb67/agent/prompt_builder.py#L194-L243) · [Go Harness 记忆提示词](../harness/memory/store.go)

Host 在每个模型步骤组装系统提示词时，从本账号的 `USER.md` 和 `MEMORY.md` 读取当前内容，将非空记忆与基础提示词、Plan Mode 等段落一起渲染。`memory` 工具提交新增、修改或删除后，下一模型步骤就能看到结果，同一轮内的后续步骤也适用。同账号其他会话、恢复的会话、Fork 和子 Agent 都在各自的下一模型步骤读取这两份文件。

复用 `ensureSystem` 比较渲染后的完整文本。文本变化时追加现有 `system/message` 事件，在模型上下文视图中替换开头的系统消息，并沿用现有 checkpoint 在调用模型前落盘。文本相同时复用当前系统消息。v4 中的 `data.message` 已经包含该次实际使用的记忆段落。长期记忆的当前值始终来自两份 Markdown。 [系统提示词组装与请求顺序](../harness/agent/native_loop.go) · [Host 提示词组装](../harness/assembly/native_runtime.go)

现有 compaction 保留开头的系统消息，可以把较早的工具调用和结果压缩成摘要。即使摘要略去了某次写入，最新记忆也会随系统消息继续发送给模型。删除一条长期记忆后，下一步骤的系统消息会移除该条目。已写入的历史事件和压缩摘要仍按会话日志的生命周期保留。 [Compaction 保留范围](../harness/agent/native_compaction.go#L72)

### 历史召回

`session_search` 沿用 Hermes 的四种调用形态，按关键词查找、按锚点展开、读指定会话、浏览最近会话。Host 从本账号已提交的 v4 记录投影用户和助手可见文本，用 `session_id + seq` 定位，复用现有归档筛选。排除当前会话、隐藏子会话、系统消息、压缩摘要和流式增量，避免重复命中与暴露内部内容。找到历史原文后按需提供前后文，查询无需模型调用。 [账号内会话枚举](../internal/host/host_list.go) · [会话转录投影](../internal/httpapi/chat_transcript.go) · [v4 日志读取](../harness/session/file_store.go)

关键词、短语、布尔和前缀查询从首次交付起使用 SQLite FTS5 `MATCH`。索引使用 `unicode61` 分词，写入索引和构造查询时都将连续汉字拆成独立词元。中文多字词按短语匹配相邻词元，使「卷」和「挂载」都能命中「挂载卷」。按相关度排序，分数相同时优先返回较新的消息，结果中的原文和前后文按锚点读取。 [Hermes 搜索行为](https://hermes-agent.nousresearch.com/docs/user-guide/sessions/#session-search-tool) · [SQLite FTS5 分词与查询语法](https://www.sqlite.org/fts5.html)

Host 启动时根据 v4 日志补齐索引，索引缺失时重建。每次检索前按各会话的已索引事件序号补齐新事件，索引内容与游标在同一 SQLite 事务中提交。归档状态沿用现有会话可见性规则，查询时过滤。SQLite 文件放在 Host 容器本地可写层，Docker 卷和 NAS 上的 v4 日志足以重建它。Host 使用 `modernc.org/sqlite`，支持 `CGO_ENABLED=0` 构建。 [索引实现](../harness/sessionsearch/index.go) · [Host 镜像构建](../build/Dockerfile.host)

### 存储与交付范围

Docker 命名卷和 ECI + NAS 保留两份记忆和 v4 日志。Host 重建后从卷或 NAS 读取当前记忆。ECI 使用容器层时，新实例从空记忆开始。Host 通过同一账号下的共享写入互斥处理并发会话。 [部署存储说明](deployment.md#storage-choices)

本次交付包含两份记忆文件、`memory` 写入工具、逐步骤加载记忆并更新系统消息、FTS5 索引和 `session_search` 的四种调用方式。记忆的新增、修改和删除统一由当前会话调用 `memory` 工具处理，用户可以在对话中提出这些操作。

### 验收顺序

1. 会话 A 在一轮中用工具写入「日报用中文」，验证同一轮的下一次模型请求已经带上这条记忆。让 compaction 压缩掉原工具回执，检查系统消息仍包含该条目。修改和删除时验证下一步骤同步更新，切换 Plan Mode 时验证两类内容能共同更新。完整提示词相同时验证复用原系统消息。
2. 同账号两个会话并发写入不同条目，磁盘保留两条。重复新增、歧义修改、超限批次和无效 UTF-8 文件分别验证。用批量操作缩短旧条目并新增，确认只有完整批次落盘。
3. 会话 B 用 FTS5 查询 A 的历史原文，覆盖中文单字、双字、英文关键词、短语、布尔和前缀查询，并按消息锚点展开前后文。另一个账号查不到 A。归档会话和系统提示词不出现在检索结果中。
4. 验证日志提交后能够增量入索引，并模拟日志已提交但索引未更新的重启。用 Docker 新容器复用同一个账号卷，验证从 v4 重建索引。账号记忆在其他会话中修改后，验证恢复的会话和 Fork 在下一模型步骤使用当前版本。

存储与实例替换方式见[部署说明](deployment.md#storage-choices)，本地验证入口见[贡献指南](../CONTRIBUTING.md#development-and-verification)。
