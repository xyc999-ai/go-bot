# X 与 Google Workspace 插件接入方案

> 分发更新：业务源码、资源与发布已迁至独立的 [go-bot-plugins](https://github.com/xyc999-ai/go-bot-plugins)；本文原镜像内 bundled 分发章节由 [远程市场方案](remote-plugin-market-plan.zh-CN.md) 替代，授权与业务行为保持不变。

日期：2026-11-21。状态：两个插件已实现；X 已完成真实账号授权、官方 MCP 发现及一次只读请求。Google Workspace 已完成真实浏览器授权与一次 Drive 账号信息只读请求。按最新决定，两者都使用用户自己的应用配置，不再分发 Google 公共应用；首次配置（Web 选择 JSON 文件，移动端保留受保护输入）、原生配置复用与中英文前置视频已交付。

## 1. 结论

分别提供两个 Cordis 包，复用当前市场、pnpm 安装、Go 内置 MCP/授权交互和原生 Skills 发现机制。

| 项目 | X | Google Workspace |
| --- | --- | --- |
| pluginId | `x` | `google-workspace` |
| 包名 | `@go-bot/plugin-x` | `@go-bot/plugin-google-workspace` |
| 展示名 | X | Google Workspace |
| 上游 | `xdevplatform/xurl` | `googleworkspace/cli` |
| 官方安装命令 | `npm install -g @xdevplatform/xurl` | `npm install -g @googleworkspace/cli` |
| Agent 接入 | Go MCP Client → stdio `xurl mcp` → X 官方远端 MCP | 原生 `gws` CLI + 上游 Skills |
| 应用配置来源 | 用户首次授权填写自己的 X 应用 | 用户首次授权提交自己的 Desktop OAuth 客户端 JSON |
| 认证执行者 | xurl | gws |
| Token 保存与刷新 | xurl 原生存储 | gws 原生存储及 Token 缓存 |

不为这两个 CLI 的 Token 再写一份 Go credentials 记录，也不创建指向原生凭证文件的占位 grant。Go authorization 管交互和流程状态；原生 CLI 管自己的认证数据。

“安装了 CLI”与“Agent 通过 CLI 使用业务”是两件事。Google Workspace 的业务入口是 Shell/CLI + Skills；X 的业务入口是 MCP，xurl 是其本地认证及协议桥。X 插件只向 Go 注册 MCP 声明，不在 Node 里再实现 MCP Client 或逐次代理业务工具调用。现有 Go MCP Client 已通过官方 Go SDK CommandTransport 原生支持 stdio JSON-RPC，业务链路为 Go Agent → Go MCP Client → stdio xurl mcp → HTTPS X MCP。插件直接启动 xurl 子进程只用于安装检查、headless 授权和原生授权状态/退出操作。

Google Workspace 在此指 [googleworkspace/cli](https://github.com/googleworkspace/cli)。仓库明确声明它并非 Google 正式支持的产品。其 `gws mcp` 已在 0.8.0 移除，不能依赖旧版本教程中的这个入口。[当前说明](https://github.com/googleworkspace/cli/blob/main/README.md)、[变更记录](https://github.com/googleworkspace/cli/blob/main/CHANGELOG.md#080)

## 2. OAuth 在哪里打开浏览器

按用户最新确认，保留当前手动粘贴完整回跳 URL 的流程。Web 不要求安装浏览器扩展，移动端不新增本地回跳监听器；不实施之前讨论的自动回跳草案。校验与交接边界见第 15 节。

运行 CLI 的位置与用户打开授权网页的位置可以不同。

```text
Host 插件运行原生授权命令
  → 获得 OAuth URL
  → Go authorization / Controller 将 URL 提供给前端
  → 用户点击，在自己的电脑或手机浏览器打开
  → 用户在服务商网页登录、选择账号并授权
  → 用户把完整回跳地址粘贴回插件授权面板
  → Host 插件交给原生 CLI 完成认证
```

关键区别是：浏览器里的 `localhost` 指用户设备，不是远端 Host。单纯把授权链接复制到本机浏览器并不能让远端 CLI 的 localhost 回调自动收到请求。需要原生 headless 输入，或本项目明确实现的回调转投。

本方案不在 Host 启动可视浏览器替用户登录，不要求用户安装 SSH 隧道，也不发布 CLI 的随机回调端口。HTTP/Tailscale 部署使用相同的复制回跳地址流程，不要求为了认证改变 Controller 端口。

## 3. X：采用官方 xurl MCP 桥

### 3.1 已确认的上游行为

- 名称是 `xurl`，npm 包为 `@xdevplatform/xurl`。
- X API 的远端 MCP 是 `https://api.x.com/mcp`；它不提供可让通用 MCP Client 直接完成认证的 OAuth 发现及动态客户端注册。
- 官方推荐以 `xurl mcp` 承担用户 OAuth 和 Bearer 注入。`https://docs.x.com/mcp` 是文档检索服务，不等于 X 账号业务服务。
- 官方支持 `xurl auth oauth2 --headless`。授权 URL 输出到 stderr，输入由 stdin 读取，认证交换及保存继续由 xurl 完成。

依据：[X 官方 MCP 文档](https://docs.x.com/tools/mcp)、[xurl 用法](https://github.com/xdevplatform/xurl/blob/main/README.md)、[headless 命令](https://github.com/xdevplatform/xurl/blob/main/cli/auth.go)、[认证实现](https://github.com/xdevplatform/xurl/blob/main/auth/auth.go)。

### 3.2 安装与启用

包的 postinstall 执行上表中的官方 npm 安装，不追加 CLI 版本号、自定义安装目录或额外 SHA256 锁。启用只检查 CLI 可执行，并注册授权流程。

已有可用授权时，插件通过现有 `ctx.host.mcp.register()` 注册：

```js
{
  name: 'api',
  connection: {
    transport: 'stdio',
    command: 'xurl',
    args: ['mcp', 'https://api.x.com/mcp']
  },
  toolLoading: 'on-demand'
}
```

这里不添加 `oauth` 配置：OAuth 已经在 xurl 内，不再让 Go MCP OAuth 跑第二次认证。内部 MCP 名称为 `x__api`，前端只展示 X 的账号授权情况，不展示桥接器、stdio 或 MCP 连接状态。

Go MCP 系统负责 initialize、分页 tools/list、工具定义校验、转换为 Go 工具、工具列表变化后的重新发现，以及通过现有工具来源接入 Agent。on-demand 控制何时向模型提供具体工具，不改变发现和调用由 Go 管理的边界。X 插件不逐个调用 ctx.host.tools.register，也不硬编码一份 X 工具清单。

未授权时先保留可用的授权入口，不启动会自动尝试登录并阻塞初始化的 MCP 桥。授权成功后注册桥；停用时由 Cordis 撤销注册，Go 关闭相应连接。

### 3.3 用户连接流程

1. 用户在 X 开发者平台创建自己的应用，启用 OAuth 2.0，登记 `http://localhost:8080/callback`，获取 Client ID 和按应用类型所需的 Client Secret。Native App 是公共客户端，Secret 留空，即使控制台显示了 Secret；Web App 等机密客户端才填写。
2. 插件首次授权通过表单收集上述值，不放到部署 `.env`、命令行参数、会话消息、Go credentials 或 Redis；不提供项目共享应用。
3. 插件通过专用 child env 启动 `xurl auth oauth2 --headless`，捕获完整授权 URL，并在用户本机浏览器中打开。
4. 用户登录自己的 X 账号并同意授权，将完整回跳地址粘贴回授权面板。插件核对回调目标及 state 后，通过 stdin 交给本次 xurl 进程。
5. xurl 成功并原生保存授权后，Go 用 inspect 确认完成；已有授权事件触发 MCP 注册。首次原生登录后再通过 xurl status 的原生 backfill 保存应用凭证，使后续刷新无需环境副本。
6. 已有应用配置时跳过开发者凭证输入，直接账号授权。退出授权只清理当前账号令牌，保留应用配置。

X 授权码在用户同意后只有 30 秒有效，需立即提交完整回跳地址。失败或取消后释放进行中状态，用户可从连接入口重新开始，无需重启 Host；新一轮使用新的授权 URL、state 和 PKCE，不复用旧回跳地址。本次真实失败只得到 `invalid_request`，后续成功同时调整了 Secret 与提交时机，因此不将历史失败唯一归因于某一项。[官方客户端类型与有效期](https://docs.x.com/fundamentals/authentication/oauth-2-0/authorization-code)

X MCP 不是免费 API 替代入口，底层调用按用户自己开发者应用对应的访问权限、额度与计费规则执行。插件详情明确介绍获取凭证和连接步骤。

xurl 负责 PKCE、授权码交换、Token 保存、过期刷新及桥接请求。我们不重写这套认证算法，也不从普通 MCP 初始化中偷偷发起登录。

授权完成与 MCP 连通性分开：原生登录成功并经 inspect 确认后即为已授权；随后注册桥失败、API 配额不足或业务请求失败，不应改写成“OAuth 授权失败”，也不删除有效原生凭证。可使用已有授权状态事件触发连接注册，不引入后台定时扫描或 Token 同步任务。

### 3.4 能力范围

按官方 MCP 的实际 `tools/list` 暴露搜索、用户及帖子读取、书签、趋势和 Articles 等能力。不因为 xurl CLI 支持更多 API，就声称官方 MCP 一定提供普通发帖、私信等所有动作。验收时记录实际工具清单；需要扩大范围时再明确引入原生 Skill/CLI 能力，不伪造 MCP 工具。

## 4. Google Workspace：原生 CLI 与 Skills

### 4.1 安装与能力

使用 `npm install -g @googleworkspace/cli`，启用时检查 `gws`。通过官方 Skills 安装方式取得上游内容及引用资源，原样随包装包分发，并交给现有 Go Skills 发现、按需加载机制。不重写 Gmail、Drive、Calendar、Docs、Sheets、Slides 等操作，也不另造一套同名 MCP 工具。

Google API 的可用范围取决于用户实际授权的 scopes、已启用的 API 和组织策略；包里带某个 Skill 不代表账号自动拥有该服务权限。授权页面展示本次要连接的服务和权限，不默认申请 Admin/组织级能力。

依据：[上游安装与认证](https://github.com/googleworkspace/cli/blob/main/README.md)、[原始 Skills](https://github.com/googleworkspace/cli/blob/main/docs/skills.md)。

### 4.2 云端授权适配

已核对的当前实现有 `login/setup/status/export/logout`，没有可直接照搬的 `gws auth login --headless`。其登录流程会输出 URL，并等待 Host 内 localhost 上的临时 HTTP 回调。普通路径的 URL 由原生 flow delegate 输出；配置代理时另有原生回环监听路径。[源码](https://github.com/googleworkspace/cli/blob/main/crates/google-workspace-cli/src/auth_commands.rs)

已实现“浏览器授权 + 回跳转投”适配，沿用原生 gws 认证：

1. 用户创建自己的 Google Cloud 项目，按需启用 API、配置 consent/受众与测试用户，注册 Desktop app OAuth 客户端并下载 JSON。首次连接时 Web 选择本地 JSON 文件并自动解析提交，移动端通过受保护输入框提交 JSON，插件校验标准端点和 installed 结构，私有权限写入原生 gws/client_secret.json，后续连接复用。不在插件包、镜像或部署 env 中提供共享应用。
2. 插件将客户端配置交给原生 gws，运行带明确 scopes 的 `gws auth login`，避免在无 TTY 环境里停在终端权限选择器。
3. 插件从进程输出捕获本次授权 URL 和其中的 `redirect_uri`，通过既有授权面板在用户设备打开 URL。
4. 用户授权后，复制浏览器最终的完整 localhost 回跳地址并粘贴回面板；页面无法打开不等于授权码没有生成。
5. 插件只向本次 gws 进程已公布的 loopback 地址、端口和路径转投回调。不请求用户随意填写的 URL，不跟随重定向；核对原生 URL 中存在的 state。原生进程继续完成代码交换、校验和保存。
6. 命令成功且原生授权检查通过，流程才完成。取消会终止本次子进程和回调监听；CLI 自行失败也结束当前流程，不让用户继续回答已经失效的问题。

这是本项目需要实现、验证的 CLI 适配，不是声称上游已提供通用远程授权接口。需先验证无代理和有代理两条原生登录路径，以及桌面、iOS、Android 浏览器回跳粘贴的可行性，再将它标为“浏览器连接”可用。

Google Workspace 只提供浏览器连接账号的授权入口，不提供已有 gws 授权令牌的导入入口。首次上传/填写的 JSON 是应用客户端配置，不是账号令牌。

服务账号或组织域级代理不是默认登录方式，不将它们包装成个人 OAuth 的等价替代。

## 5. 凭证：以原生 CLI 为唯一保存者

### 5.1 不重复持久化

明确采纳最新决定：这两个插件的 OAuth Token 不再写入 `.credentials.yaml`，也不在其中保存 `native-file`、文件路径指针、connected 布尔值或占位 grant。不添加 Token 双向同步、文件监听回写或后台定时复制。

- X：原生 `~/.xurl/auth.yml` 保存应用和用户 Token，刷新仍由 xurl 负责。
- Google：原生 `~/.config/gws/` 保存客户端配置、认证文件、密钥文件及缓存，沿用其加密和刷新逻辑。
- Go credentials 继续为直接使用它的普通插件及 Go MCP 标准 OAuth 提供原有能力。这两类原生 CLI 插件不为“格式统一”再造第二份记录。
- Redis `plugins` 仍只有 catalogId/version/enabled，不加第三方授权秘密或连接状态。

依据：[xurl 原生存储](https://github.com/xdevplatform/xurl/blob/main/store/tokens.go)、[gws 凭证加载](https://github.com/googleworkspace/cli/blob/main/crates/google-workspace-cli/src/auth.rs)、[gws 加密存储](https://github.com/googleworkspace/cli/blob/main/crates/google-workspace-cli/src/credential_store.rs)。

### 5.2 Host 路径

原生认证数据要属于用户数据。建议由各插件在启用时准备：

```text
/root/harness/.dsh/credentials/x/                 # xurl 原生目录内容
/root/harness/.dsh/credentials/google-workspace/  # gws 原生目录内容

/root/.xurl       -> /root/harness/.dsh/credentials/x
/root/.config/gws -> /root/harness/.dsh/credentials/google-workspace
```

保持 Agent 的 HOME=/root 和官方命令名，普通 Shell 直接执行 CLI 时也读到同一份数据，不另造命令 launcher，不全局改 HOME。gws 的官方配置目录选项可以用于授权子进程，但不能让授权进程和 Agent CLI 最终读到不同目录。

这是认证数据目录，与 CLI 软件安装目录分开。它由账号现有 Docker 卷/NAS 的数据根承载；不要求新增用户路径配置。目录设为私有权限，gws 的文件型加密密钥必须与认证目录一起保留。目录或链接已有不属于插件的内容时明确报错，不覆盖或删除未知文件。

## 6. 授权框架的必要调整

### 6.1 完成条件

原生 CLI 自己保存账号凭证，authorization 通过插件提供的 inspect 确认是否已连接。未提供 inspect 的普通流程仍以 Go credentials 中成功提交记录作为完成条件。两种完成条件分开，不要求原生 CLI 额外调用 `session.commit()`，也不保存假 grant。

### 6.2 最小扩展

`AuthorizationFlow` 增加以下可选回调，通过 Go RPC 调用：

```ts
interface AuthorizationFlow {
  key: string
  title: string
  methods: AuthorizationMethod[]
  run(session: AuthorizationSession): Promise<void>
  inspect?(context: {signal: AbortSignal}): Promise<{ configured: boolean; accountLabel?: string }>
  disconnect?(context: {signal: AbortSignal}): Promise<void>
}
```

- 原有写 Go credentials 的流程继续使用 `session.commit(record)`。
- 提供 inspect 的原生流程：run 成功返回后再次调用 inspect；只有 configured=true 才确认成功，不要求写 Go 凭证。
- 打开授权页面和流程结束时调用 inspect 获取状态，不定时轮询、不持久化一份副本。不以“访问 Token 已到期但可以刷新”误判为账号未连接；配置存在也不等于服务商永远不会撤销授权。
- 读取失败与明确未配置应区分，错误不能被吞成假成功。
- disconnect 先由插件关闭自己正在使用的连接，再调用原生 CLI 的退出/清理逻辑，最后复查状态。它是授权生命周期，不恢复已删除的通用 actions 框架。
- 回调仍由 Cordis 插件注册，Go 通过现有异步 RPC 调用；Go 不引入 X/Google 业务分支。停用时按 Cordis 生命周期撤销回调。

表层 API/UI 补齐这两个可选能力与账号显示；保留现有 run/cancel/notice/prompt。不提供任意命令执行的前端 RPC，也不把授权错误、密钥和验证码塞进 Agent 会话。

## 7. 前端表现

- 继续使用侧栏/移动端工作区菜单中的插件市场；不新增 Settings 页面或独立 MCP 配置入口。
- 包内 plugin.json 提供名称、description、version 和 icon。图标采用后端 `/assets/plugins/x/icon.png`、`/assets/plugins/google-workspace/icon.png` 路径，两端仅渲染图片 URL。Google Workspace 使用仓库名称旁的官方 googleworkspace 组织彩色 G 头像，不使用 README 横幅裁剪图。 Controller 在展示 URL 附加图片内容版本，避免移动端继续命中旧图缓存；源路径仍由 plugin.json 指定。
- 插件详情中的账号卡显示“未连接 / 已连接”、账号标签及实际可用的授权方法。X 显示连接 X；Google 显示浏览器连接和导入授权数据；未通过适配验收的方法不得伪装可用。
- 授权面板包含服务商链接、在本机浏览器打开、完整回跳地址输入、继续和取消。移动端使用已有底部 Sheet，点击遮罩按现有规则收起，不把登录页嵌进 Host Chromium。
- X 开发者平台链接保留在插件详情的连接步骤中；账号授权流程只提供实际登录链接，避免两个相同的“在浏览器中继续”入口。回跳输入提示明确要求先在浏览器授权，再复制完整 callback 地址，提供地址格式占位，不让用户误以为这里需要填写登录账号。
- Google 的客户端 JSON 和导入授权 JSON：Web 通过本地文件选择器读取、校验 JSON 对象并自动作为现有授权答案发送，不存入聊天附件区，也不新增文件上传存储 API；移动端继续使用 kind=secret 的受保护输入。SDK 的可选 webInput=json-file 仅为 Web 表现提示，插件仍负责结构及服务商校验，Go 保留并转发该字段。
- X/Google 的回跳地址输入框明文显示，Client Secret 和移动端授权 JSON 仍隐藏。显示方式不代表可记录：authorization 对所有自由输入答案进行错误回显脱敏，客户端历史不保存答案。原生 CLI 失败只显示经审核的错误分类，不返回可能含密钥或授权码的 stdout/stderr。
- 关闭面板与取消授权不同：保留当前已有的流程恢复规则；显式取消终止等待与子进程。只有原生认证确认后才显示已连接。
- 安装不等于授权，插件启用成功不等于账号已连接。未授权时仍保留连接入口，不让整个插件 apply 因“缺少账号”失败后连授权按钮都消失。
- Google 原生 Skills 保持原文；插件可通过现有 prompts 服务补充一条认证使用说明，避免 Agent 在普通 Shell 中自行发起交互登录或输出凭证。

## 8. 生命周期与目录

源码仍在现有结构内：

```text
extensions/plugins/x/
  package.json, plugin.json, assets/, scripts/install.mjs
  src/index.mjs, src/native-cli.mjs
extensions/plugins/google-workspace/
  package.json, plugin.json, assets/, scripts/install.mjs
  src/index.mjs, src/native-cli.mjs
  skills/  # 镜像构建阶段由官方 Skills 安装命令生成，不提交生成副本
```

统一 pnpm 管包装包与依赖；postinstall 管各自官方 CLI 安装，apply 只检查并注册。两套 CLI 不放入基础 Host 的强制预装步骤。包装包及 Google 原始 Skills 可沿用已有 bundled 分发，保留上游许可证与出处，不虚构尚未发布的 npm 包。

停用关闭本插件的连接和贡献，保留认证文件；卸载移除包装包，沿用当前保留全局 CLI、凭证及用户文件的规则。用户明确“断开账号”才清理该账号的原生授权。

本次界面管理各插件当前使用的账号，不新增多账号切换产品界面。原生工具中已有的其他账号或应用记录不属于本次断开操作的删除范围，不能直接清空整个原生认证目录。

## 9. 实施与验收

1. 完成 authorization 的 inspect/disconnect 适配及契约测试：原生成功无需 commit、原有 commit 路径仍正常，不将失败/取消的尝试标为授权成功；已有有效账号不因重新授权失败而被清除。断开不伪称只是取消流程。
2. 验证 xurl headless 的 URL/stderr/stdin 桥接、state 错误及退出行为，再接 Go MCP；验收未授权不启动交互 MCP、授权后 tools/list 和一次只读请求。
3. 验证 gws 两种登录路径的本地回调转投，确认桌面和移动浏览器回跳粘贴可用；验证官方导出数据导入。不能以模拟请求代替真实授权验收结论。
4. 验证原生 Token 刷新、Host 重启后的原目录恢复、停用重新启用及原生目录丢失时的明确提示；确认 `.credentials.yaml` 和 Redis 没有这两个 CLI 的 Token 副本。
5. 验证 Google 原始 Skills 发现、按需加载及一次实际只读业务请求；缺 scopes、未启用 API、账号撤销或配额不足时显示原始业务原因。
6. Web 与 Android 完整检查：安装、详情、连接、外部浏览器、返回输入、成功、取消、重新连接、断开、详情返回市场。CLI 安装验证 Linux amd64/arm64，权限模式沿用现有 Go 执行器。

真实验收需要用户自己的 X 测试应用与测试账号，以及用户自己的 Google Desktop OAuth 应用。X 获取和填写步骤见 [账号连接指引](plugin-account-setup.md)，Google 前置配置见 [应用配置指引](google-workspace-app-setup.md)。X 私有应用凭证与两者的账号令牌不保存在源码、部署环境文件或 Redis；Google 客户端 JSON 也属于用户自己的原生配置，不随项目分发。默认不发帖、不发邮件，不把远端有副作用的动作当成读连接测试。各写操作可先用模拟服务覆盖，真实发布另按用户明确要求执行。

## 10. 调研基线

调研时 xurl 默认分支提交为 `707f076ab7df7f264803b42a2d3f9fc0e802f25c`；googleworkspace/cli 为 `cd3697e07eb520056dcf142d0c7439bbb9ba32e9`。这些是源码核对基线，不是新增 CLI 安装版本锁。实际实现前复核所安装版本的命令、输出和认证文件布局，避免把旧教程的参数当作现成功能。

授权 URL 适配依据服务商地址及 OAuth 参数识别，不只匹配某句终端英文提示。若所装上游版本的命令或输出不兼容，应明确提示并停止该次授权，不把未知输出当成成功，也不静默改用另一套 OAuth 实现。

## 11. 最新配置收口

- X 不提供共享应用或计费账户；用户首次连接填写自己的 Client ID 与可选 Client Secret。Google 用户首次连接提交自己的 Desktop OAuth JSON，后续复用原生应用配置。
- 前端详情描述由 plugin.json 给出简短介绍、获取凭证步骤、官方指引与连接方法；列表只展示第一段简介。
- 连接前告知用户：检索内容会进入 Agent 上下文，并可能交给其选择的模型服务处理。
- 分开报告每个插件的模拟、原生 CLI 和真实账号验收结果；Google 应用配置已接入，但尚未因此宣称 Google 真实账号也已通过。

## 12. 凭证获取视频

- 详情页按 plugin.json 的 `tutorials` 显示预制视频，字段为 language、title、video、可选 poster；视频位于同一插件后端 assets 路径，不放 Redis，也不由前端写死。
- 使用真实开发者控制台画面，保留全景定位，围绕具体点击/输入区域做平滑放大、缩小和焦点提示，并提供中英文步骤字幕。发布前遮挡账号私密信息与密钥。
- Web 使用带控制条的内嵌播放器，移动端使用 Expo Video 原生播放器与全屏入口；不自动播放，也不加载尚不存在的教程。
- X 使用已捕获的真实控制台画面，用户明确要求录到保存前，不提交设置或生成凭证。Google 教程展示用户自建应用前置步骤：按需启用 API、配置受众/测试用户、选择 Desktop 类型、创建时下载 JSON 的说明；不录制密钥值，不擅自提交控制台变更。视频使用真实控制台画面，停在创建提交前，最后展示旧客户端不允许重新下载原始密钥的现行限制；没有伪造创建成功或下载按钮画面。不能用假控制台或文字动画冒充实录。

## 13. 实施证据与待验收范围

截至 2026-11-21，以下结果已经验证：

- 两个正式包装包经统一 pnpm 安装、Cordis 启用，在 Linux arm64 和 amd64 上安装了实际官方 CLI：xurl 1.3.4、gws 0.22.5。Host 打包包含 95 个原始 Google Workspace Skills，基础镜像不提前安装这两个 CLI。
- authorization 原生 inspect/disconnect 通过 Go、Cordis RPC 和 Controller HTTP 契约测试；没有为了连接状态写入 Go 假凭证或 Redis 账号令牌。
- 实际 xurl 二进制连接本地模拟 OAuth/API，完成 PKCE 授权码交换、应用配置回填、清除临时环境变量后自动刷新、读取账号及单账号退出。退出使用官方 `--oauth2-username`，保留应用配置。这证明原生文件适配和刷新调用，不证明真实 X 账号或 X 云端 MCP 已可用。
- Go MCP Manager 启动真实 xurl stdio 桥，对接本地 Go SDK 模拟服务，完成初始化、tools/list、按需加载、一次工具调用、Go 参数校验及远端会话释放。原生桥在 7 次本地请求中携带 Bearer；没有访问 X 云端。此验证使用隔离临时 HOME，未改动用户账号凭证。
- 使用用户自己的 Native App，用户在自己的浏览器完成 X 同意授权，xurl 原生保存账号。Go authorization inspect 返回已连接，Web 与 Android 显示真实账号标签。实际连接 `https://api.x.com/mcp`，发现 101 个工具，Go MCP Tool 调用一次 `get_users_me` 成功返回当前账号；未发帖、发私信或调用其他写操作。
- 重启本次 QA Host 后，原生授权仍可读取，Go MCP 自动恢复连接并再次发现 101 个工具，无需重新登录；Controller 重启后仍通过 Redis 中的 Host 记录访问它。原生 X 文件确认 Secret 为空；Redis 用户 Hash 仍只有 Host 运行记录及插件选择，Go `.credentials.yaml` 未生成，不存在两份 X Token。 正式 pnpm 重新安装包装包后授权不丢失；停用撤销授权入口并关闭 MCP，再启用从原生目录恢复 101 个工具。Web 实测错误回跳失败后可立即新建授权尝试，取消该尝试不会清除已连接账号。Android 最新 APK 也实测回跳地址明文输入与错误回跳的失败提示，原有账号授权保持连接。
- 实际 Google 包经 Cordis 启用后，Go Registry 发现 95 个 Skill；抽查 gws-shared、gws-gmail、gws-drive、gws-docs、gws-slides 的正文加载及资源目录，停用后撤销、再次启用后全部恢复。
- gws 浏览器回调转投、权限选择及退出通过模拟 CLI 测试；真实 gws 的无账号状态检查和目录链接通过运行验证。原生状态明确 token_valid=false 时不会显示已连接；没有在线校验结果时不会仅凭离线误判授权失效。
- 新版 Web 已实测本地文件选择及自动解析提交：格式错误留在当前流程提示重选，有效的用户原始 JSON 自动进入权限选择并交给原生 CLI；没有复制文本或点击第二次提交。Android 保留受保护输入；Android 通过受保护输入框提交用户自己的有效配置后进入权限选择及原生回跳等待，配置保存与原文件 ID/Secret 一致，权限为 0600。重新授权直接进入权限选择，不重复索取 JSON；取消保留原有真实授权。无效 JSON 失败后能重新开始。Android QA APK 已重新构建并安装。
- X 中英文教程来自真实控制台截图，带焦点缩放和字幕；完整 MP4 解码、后端 Range/HEAD、Web 内嵌播放、Android 内嵌及全屏播放已验证。遵照要求，没有保存 X OAuth 配置或生成新凭证。
- 用户在 Google 控制台添加测试邮箱后，已亲自在真实浏览器完成同意授权并将回跳提交到插件面板。现有 gws 经 HTTP 转投完成代码交换，authorization 状态为 authorized，原生 inspect 返回已连接邮箱。实际 gws drive about get 只请求 user(displayName,emailAddress)，成功返回账号基本信息；未读取文件正文、邮件或执行写操作。先前 403 已解除。此证据证明原生认证与业务链路；新版首次配置入口由独立的真实两端 UI 验收覆盖。
- 项目源码中的公共 oauth-client.json 已删除，插件不再加载或打包共享客户端；首次提交 JSON 与后续原生配置复用通过模拟 CLI 测试。真实账号测试所用应用留在用户的原生目录，不能重新写回源码。
- 用户配置保存后重启 QA Host，X 与 Google 原生账号均恢复；X MCP 再次连接且工具数为 101。Google 停用撤销授权入口，重新启用后从原生目录恢复账号。实际 gws auth status 返回 token_valid=true、encryption_valid=true 和账号邮箱；已对照上游实现确认该命令通过 refresh_token 向 Google Token 端点换取 access token，而不是仅检查离线文件。
- Google 原生退出在真实授权目录的隔离临时副本上验证：插件 inspect 识别真实授权，disconnect 调用实际 gws logout，随后 inspect=false，客户端应用配置仍在。临时副本测试结束删除，原账号仍连接；没有调用服务商撤销接口，不将该项写成原账号云端撤销验收。
- Google 中英文教程均为 48 秒，末尾说明已对齐 Web 选择文件、移动端粘贴，来源为真实控制台截图序列，带字幕、全景与焦点缩放，账号信息和密钥已遮挡。Web 语言切换选择对应视频，Android 实际解码播放；两份 MP4 全量解码与后端 HEAD/Range 读取通过。没有新建额外客户端，没有虚构创建成功后的下载屏幕。
- `make test vet check`、Cordis/SDK/Web 测试、Web 构建、移动端类型检查及最新 Host 镜像基础环境检查通过。Host/Controller QA 镜像及 Android arm64 QA APK 已构建；最新 JSON 类型校验补充测试通过。源码检查未发现用户提供的真实 Client ID/Secret；公共配置文件已从源码、打包清单与实际安装包移除。

### 13.1 真实验收边界

- 真实云端业务调用仅包括 X get_users_me 和 Google Drive about 的账号基本信息读取；没有读取邮件/文件正文或发起写操作，不将 Skills 清单等同于所有 Workspace 业务已逐项验收。
- Google 已验证真实授权、刷新与重启恢复；退出在真实凭证隔离副本上验证。X 的刷新和单账号退出采用真实 xurl 对接隔离模拟服务，不宣称实际撤销了原账号的云端授权。
- 新版首次 JSON 配置与复用、错误/取消在 UI 和针对性测试中验证；用户此次真实同意授权在桌面 Chrome 完成，不宣称又在 Android 重新完成了一次 Google 云端同意。
- 教程没有提交新的 Cloud 配置或创建额外客户端；创建后的下载动作以明确字幕说明，非伪造实录。若后续希望补录完整创建后下载过程，需先授权创建教程用的额外 Desktop 客户端。该补录不是插件运行的依赖。

## 14. 完成核对

| 要求 | 当前证据与结论 |
| --- | --- |
| 两个正式插件、官方安装与图标 | 两个包的 package.json/postinstall/plugin.json 已提供；实际 Linux amd64/arm64 CLI 安装通过，后端图标在两端详情及列表显示。 |
| X 通过 Go 内置 MCP 接入 | 真实官方 MCP 的 101 个工具发现及 get_users_me 已通过；业务请求不经 Node 自造 MCP Client。 |
| gws 原始 Skills 与按需加载 | 官方安装命令生成 95 个 Skill；Go Registry 实测发现、正文与资源加载、停用撤销及再次启用恢复。 |
| 原生凭证与 inspect/disconnect | Go native_test、Cordis 适配测试及真实 CLI 隔离测试通过；X 实际重启恢复，Redis/Go 无 Token 副本。Google 已完成真实授权、原生刷新与重启恢复；退出使用真实凭证隔离副本，原账号保留。 |
| Web/Android 交互与教程 | 已验证列表、详情、外部链接、输入、失败、取消、恢复与真实 X 已连接展示；X 中英文焦点视频播放通过。新版 Web 文件选择、自动解析与提交，以及 Android 受保护输入和账号恢复已验证；中英文 48 秒前置教程已接入并验证播放，未额外创建客户端。 |
| 构建与检查 | 最新 Host/Controller QA 镜像及 Android arm64 QA 包构建通过；make test vet check、Cordis 24 项、SDK、Web 22 项、移动端类型检查与相关 race 检查通过。 |
| Google 真实授权及只读业务 | 已通过真实浏览器登录、代码交换及 gws Drive about 的账号基本信息只读请求。该次应用属于用户自己，不再由项目分发。 |

该核对不把原生 CLI 对模拟服务的成功等同于服务商真实账号通过，也不把已有 X 授权的保留等同于真实撤销/重授权验收。本次实现与必要验收已完成；上述未覆盖范围为真实账号验证边界，不宣称全部业务 API 或服务商云端撤销均已验收。


## 15. 最终回跳流程与校验边界

按用户最新确认，保留手动粘贴回跳 URL 的交互。删除此前浏览器扩展、手机本地 HTTP 接收器、session.browser 和 browser-result 接口草案；这些不属于本次实施范围，也不要求用户额外安装授权辅助程序。已有 session.callback 继续服务原本支持服务端回调的插件，不用于截获 X/gws 的客户端 localhost。

### 15.1 每次授权如何确定回跳地址

插件启动原生 CLI，读取它输出的授权 URL，从该 URL 的 redirect_uri 提取本次预期回跳地址；如果原生链接包含 state，也保留本次 state。随后通过 session.notify 将授权链接交给前端，前端只负责打开用户浏览器和展示输入表单。

服务商让浏览器跳转的地址由原生 CLI 的授权请求指定，不由前端随意生成。不同插件或不同授权尝试可以使用不同的端口和路径，不维护全局固定回跳地址：

| 插件 | 预期地址来源 | 用户输入之后的交接 |
| --- | --- | --- |
| X | 本次 xurl 授权 URL 的 redirect_uri；当前应用登记为 http://localhost:8080/callback | 插件校验后，将完整 URL 写入本次 xurl --headless 的 stdin |
| Google Workspace | 本次 gws 授权 URL 的 redirect_uri，端口由 gws 动态选择 | 插件校验后，在 Host 内向本次 gws 回环监听器发送 HTTP GET |

浏览器所在设备的 localhost 与 Host 的 localhost 不同。用户本机回跳页无法打开时，仍可复制地址栏中的完整 URL，回到插件面板提交。插件在 Host 内完成交接，不需要把 Host 回调端口对外发布。

### 15.2 插件校验与服务商验证

插件的 validateRedirect 使用 URL 解析器比较用户输入与原生 CLI 提供的预期地址：origin（协议、主机、端口）和 pathname 必须相同；不接受用户名、密码或 fragment；必须恰有一个非空 code。本次原生授权 URL 包含 state 时，输入必须恰有一个相同的 state；原生 URL 未包含 state 时，不宣称已有额外 state 保护。

Google 的 relayRedirect 还限制目标只能是本次 CLI 给出的 HTTP loopback 地址和明确端口，只向该目标发送 GET，不跟随 HTTP 重定向，不把用户输入变成任意 URL 请求。X 校验后仅写入本次进程 stdin。

这一步证明输入的格式和回跳目标符合本次流程，不能证明 code 是真实、未过期或未使用的。原生 CLI 继续调用服务商 Token 端点；X 使用原生 PKCE，Google 使用原生 gws 的交换逻辑。授权码是否有效由服务商最终决定。CLI 退出成功后，authorization 通过插件 inspect 再确认原生授权状态，才向前端报告已连接。

### 15.3 交互与失败

回跳 URL 用明文输入，Client Secret 和导入授权 JSON 用密钥输入；授权输入不进入聊天、Redis、客户端历史或错误原文。X 短期授权码需要用户同意后尽快提交，失败后提供重新开始入口，新一轮使用新的原生授权链接。

插件通过 session.prompt 等待输入；Go authorization 和 Controller 只负责通用传输、等待、取消及流程恢复，不实现 X/Google 专属提交逻辑。取消终止本次原生子进程，失败或取消不清除已有有效账号授权。Google 的实际应用访问限制、scopes、API 开关等由服务商决定，输入框或 HTTP 转投不能绕过这些限制。
