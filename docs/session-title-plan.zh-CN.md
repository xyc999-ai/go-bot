# 会话标题：Go 内置实现

## 目标与对齐依据

对齐 DeepSeek Harness 的 `session-title`、默认启用的
`session-title-first-prompt-llm` 和 `session-title-llm` 的功能行为。
参考上游提交 `4b399c39a6d1479cd84ca591b4d7c88e24d38a96`。
这些能力由 Go 内置实现，不新增 Cordis 插件、Redis 标题表或模型配置项。

## 标题来源与日志

- 只采集 `source.kind=user` 的 `user/message`，按原顺序拼接文本块。
  图片或文件本身、助手消息、定时投递、后台服务和子 Agent 消息均不作为标题输入。
- 首条有效文本产生确定性兜底标题：清理终端控制符、不可见方向控制符，
  合并空白，最多 5 个空格分词、40 个 UTF-8 字节，不截断 Unicode 字符。
- 接受的标题统一追加 `session/title`，包括 `title`、`messageSeqs`、`source`。
  `source.kind` 使用 `fallback`、`provider`、`user`。最新事件为当前标题。
- 标题是日志元数据，不进入模型上下文、聊天消息或 compaction 摘要。
  列表及 session search 读取日志投影；删除 `title.txt` 的读写路径。
  不对旧项目数据作迁移。

## 自动生成与生命周期

- 新的、非 seeded、非 subagent 会话，第一条有效用户文本到达且尚无标题时，
  安排一次辅助模型请求。沿用现有 Inbox；消息领取并记录为 `user/message` 后、实际模型请求前保存兜底。
  多条消息同批领取仍只用首条有效用户文本。
- 等正常模型请求选定实际端点和模型后再启动辅助请求。使用同一协议和认证，
  只发送上游标题 system prompt 及首条用户文本 JSON，不提供工具，也不启动额外 turn。
  输入上限 4096 字节、输出上限 4096 tokens、超时 60 秒。
- 发出前记录 `session/title-llm-request`，记录输入、路由及限制，不记录密钥。
  返回值取首个非空行并移除整行强调符，规范化为最多 80 个 UTF-8 字节。
  错误、空响应、工具调用或输出截断时保留兜底，不循环重试。
- 手动改名追加 `source.kind=user` 的标题，取消待生成请求，迟到结果不能覆盖。
  后续消息不改动手动标题。会话关闭时取消并等待辅助调用退出。
- 恢复已有日志时可补充缺失的兜底，不重新触发首次消息自动生成。
  定时任务显式指定的标题沿用已有命名语义，写入用户来源标题并保护。
- 产品既有主会话始终显示 `Go Bot`，不自动生成或开放改名；保留右侧图标标识。
  普通会话可改名。不会给主会话增加 system 身份提示。

## HTTP、SSE 与客户端

- `PUT /v1/sessions/{id}/title` 接收 `{title}`，返回已规范化标题。
  冷会话通过现有恢复流程取得唯一日志写者；不创建工作目录或新 turn。
- `session/title` 通过现有 Host SSE 以 sessionId 分发；双方列表收到后刷新标题，
  未打开会话也能更新。历史读取和重新连接仍以持久投影为准。
- Web 在会话行菜单增加“重命名”，弹出轻量输入对话框；Enter 保存、Escape 或取消关闭。
  Mobile 会话行操作菜单增加同名入口，使用现有底部 Sheet 和输入框。
  点击空白收起，保存中避免重复提交，失败保留输入并显示错误。
- Web 和移动端均提供简体中文、英文文案；不修改助手消息渲染、导航及执行流程。

## 验证

覆盖中文/英文及终端字符清理、真实用户来源过滤、fallback/provider/user 覆盖顺序、
日志恢复、独立辅助调用、失败兜底、手动改名与迟到结果、关闭取消、HTTP 与 SSE。
运行相关 Go 测试、Web 构建、移动端 TypeScript 检查；不构建 Android。

## 实现与验收记录

已完成 Go 标题投影、辅助命名服务、唯一写者恢复后的改名接口，以及双端菜单和
实时列表刷新。辅助调用复用已有模型适配器，已用模拟端点覆盖 Responses 和
Anthropic Messages；未消耗真实模型凭证或 tokens。

- `go test -race ./harness/session ./harness/sessiontitle ./internal/hostruntime ./internal/host ./internal/httpapi ./internal/controller` 通过。
- 端到端测试覆盖 Inbox 领取后触发命名、冷会话改名、日志恢复及 Host SSE 中的标题投递。
- 覆盖手动改名取消、迟到结果保护、关闭取消、生成失败兜底，以及定时任务恢复不覆盖已有标题。
- Web 43 项测试及生产构建通过；移动端 TypeScript 检查通过。
- 提交前使用最新 Host 二进制完成 Docker Web 聊天回归，覆盖生成标题持久化、Host 重启与冷会话改名；模拟模型的辅助标题请求与 Agent 请求分别处理。
- 双端界面代码已接入，本轮未重新安装手机 App，未构建 Android；不将静态检查描述为真机交互验收。
