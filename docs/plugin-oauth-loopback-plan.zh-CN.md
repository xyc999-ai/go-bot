# 现有插件的客户端 OAuth 回调接收

状态：已实现并完成本地验证；尚未发布或替换个人客户端。

2026-11-29。范围：X、Google Workspace；Desktop、iOS、Android 自动接收，Web 保持手动提交。暂不实现模型 OAuth。

## 流程与分工

插件继续创建登录链接，原生 CLI 负责令牌交换、保存和刷新，X 的 PKCE 继续由 xurl 管理。扩展通用授权 text prompt：可附带 `loopback: {authorizationUrl, redirectUri, state}`。这些值来自本轮 CLI 的真实登录链接，不从开发者后台读取。GWS 上游未提供 state 时由插件补入 32 字节随机 state，前端和 Host 插件共同校验；已有上游 state 原样保留。登录链接与元数据在同一个 prompt 中发布，防止原生监听准备前先显示可点击的裸链接。Web 忽略该提示信息，仍展示文本框；不增加回调上传接口或授权记录表。

Desktop/Mobile 显示“在浏览器中继续”，隐藏同一登录链接对应的手动提示。用户点击后先绑定链接里的端口与回调路径，监听就绪才打开系统浏览器；回调校验通过，立即以原有 answer API 提交完整链接。Host 校验后交给 xurl 标准输入，或转发给 GWS 在 Host 的原生监听器。授权是否成功仍以 Host 结果为准。

X 固定使用 `http://localhost:8082/callback`，应用登记、插件传参、双语说明、视频一致。每次发起授权都显式传入此地址，不受历史 xurl 配置影响；已建 X 应用需修改 Callback URL。GWS 保留动态端口、原有 Desktop OAuth JSON 导入和权限选择，不改教程视频。

## 原生接收与安全

Desktop 使用 Electron 主进程临时 HTTP 服务，受信任 renderer 通过窄 IPC 启动/取消；浏览器只在绑定成功后打开。Mobile 使用本地 Expo 原生模块：iOS Network.framework 监听 + SFSafariViewController，避免正常登录期间因外部 Safari 导致应用挂起；短暂切换到验证器时使用系统有限的后台宽限，系统宽限到期即释放监听并提示重新授权；Android 使用 loopback ServerSocket + 系统浏览器 Custom Tabs（AndroidX Browser 1.10.0），保留同一任务栈以符合后台 Activity 启动限制，收到回调后回到应用。iOS 采用系统浏览器而非 WebView 注入或抓取网页。模块随原生构建自动链接，不支持 Expo Go。

只允许 HTTP loopback 的明确端口；仅接受本轮路径、state 和唯一 code（或 OAuth error）；拒绝外部 Host/Origin、错误 state、重复参数。服务只绑定 loopback，不监听公网；普通错误请求不消耗本轮授权。回包使用固定 HTML、不回显 URL/code，无缓存、无跨域。临时状态仅在内存，不写本地缓存或日志。

## 生命周期与重试

监听最长 5 分钟，成功、取消、超时、组件关闭/卸载、切换用户/插件、进程退出时关闭。CLI 的交互等待也有 5 分钟上限。一个客户端只接收一轮，重复点击不创建第二个监听器。端口被占用不替换第三方进程，也不打开浏览器。

超时、拒绝、浏览器取消和提交失败均给出明确反馈和“重新授权”。重试先取消旧 Host 授权并等待 CLI 清理，再发起新 attempt，生成新的 state/PKCE/登录链接，不重用旧 code。取消本轮不删除已保存账号。关闭面板停止本地接收；Host 的授权按现有规则可继续，可回来后重新授权。Web 保持原有手动流程。

## 验证

1. Go Surface/SDK：结构化字段传递、拒绝不合法提示，答案不持久化。
2. Desktop 真实 HTTP：绑定前不打开浏览器、成功/拒绝/state 错误/路径错误/端口占用/超时/取消/重试/重复请求与资源释放。
3. 插件：真实子进程 fixture，X 每轮传入 8082、GWS 动态回调、元数据与 CLI URL 一致、取消与重试不覆盖凭证。
4. 客户端：类型检查、测试、构建；iOS/Android 原生构建和浏览器回调 fixture，覆盖原生 UI、自动提交及失败重试。无第三方账号登录或真实付费调用；真实供应商授权需用户完成账号确认，单独说明未验证项。
5. X 视频检查实际画面，更新涉及回调地址的双语素材并检查输出；保留 GWS 素材。

Host 和客户端需一同更新以识别结构化回调 prompt；不增加历史迁移。不修改客户端版本或 build number，不覆盖已发布插件资产；本地源码/素材准备完毕后，发布另走现有流程。

## 本次实现与验证记录

- Host：`authorization.Prompt.loopback` 通过现有插件 RPC/HTTP Surface 传递；不新增提交 API、令牌存储或持久化授权任务。SDK 与 OpenAPI 已同步。
- Desktop：Electron 主进程双栈 loopback 监听；预加载桥只暴露启动/取消。监听就绪才打开浏览器，成功/取消/超时/面板卸载/renderer 重载/退出均清理。
- Mobile：本地 Expo 模块随原生构建自动链接。iOS 使用 Network.framework + 系统 Safari 控件，并支持有限后台宽限；Android 使用 loopback ServerSocket + 系统浏览器 Custom Tabs（AndroidX Browser 1.10.0），保留同一任务栈以符合后台 Activity 启动限制，接收后返回应用。空回调路径按 `/` 处理。
- UI：原生客户端不显示最后的手动粘贴框；Web 保留登录链接和输入框。登录链接与回调元数据在单个 prompt 中一起发布。失败和取消后可重新授权，使用新的 attempt/prompt/state。
- 插件：X 0.1.2 固定回调 8082；Google Workspace 0.1.1 在上游未提供时补随机 state。CLI 交互五分钟超时后终止。本地已打包新版本，保留旧 catalog/发布资产。
- 视频：X 双语视频的回调配置片段重新录制并替换，字幕/表单均为 8082；导出 1280×720、31.7 秒，更新封面。GWS 视频未修改。
- 验证已通过：全部 Go 测试、Go vet、依赖/凭证边界检查、授权/插件 race tests；Host Docker 对话恢复/文件集成；Desktop 94、Web 68、Mobile 17 个单元测试，三个客户端类型/构建检查，插件 21 个测试与清单检查。
- 真实客户端 fixture：Desktop 自动接收/取消/重试/提交失败、Web 手动提交、iOS 系统浏览器回调/取消/重新授权/根路径回调均通过。iOS XCTest 使用测试进程内的动画等待适配，不改生产 App 的动画行为。Android 的 Custom Tabs、自动回到 App、取消后立即重新授权与根路径回调均通过。Android listener 使用 SO_REUSEADDR 允许连接进入 TIME_WAIT 后立即重绑，不开启 SO_REUSEPORT，不允许重复活动监听。

可复跑的入口：

```sh
# 在 desktop 目录
node scripts/oauth-loopback-smoke.cjs
node scripts/oauth-web-smoke.cjs
# 在仓库根目录；需要已构建的原生 App 与专用 QA 模拟器
node mobile/scripts/oauth-ios-smoke.cjs
node desktop/scripts/oauth-loopback-fixture.cjs
node mobile/scripts/oauth-android-smoke.cjs
```

所有自动流程均使用合成回调/本地 fixture，不登录真实第三方账号，不请求付费模型。仍需用户实际登录一次才能验证其 X/Google 应用登记、账号权限和供应商侧策略。本地插件包与 QA 构建尚未正式发布；现有运行服务和个人真机未替换。客户端显示版本和移动 build number 保持原值。

最终原生复测：iOS 脚本结果位于 `.local/oauth-ios-qa/results-1795895307357`；Android 的成功和重试截图位于 `test-results/oauth/`。测试回调由本地 fixture 注入，保留实际系统浏览器、原生监听和客户端提交链路；不包含真实供应商的账号登录确认。另验证了 Chromium 表单 POST 后跨来源 302 回到 loopback 的浏览器请求。

原生验证使用 Release 构建。iOS Debug 的完整 App 构建在 RNDateTimePicker/RNSVG 与预编译 React Native 的链接处出现符号错误，本次未调整这部分第三方构建配置；新增 Swift 模块和完整 Release App 均编译并通过原生 UI 回归。

## 独立同步验证

本次 OAuth 修改从最新 `main` 单独整理，不依赖此前 Space 功能或 Redis/account 数据迁移。独立分支再次通过全部 Go 测试、vet、依赖检查、授权与插件 race tests；Desktop 91、Web 55、Mobile 17 个测试；桌面/Web 构建与移动端类型检查；Desktop 自动回调和 Web 手动授权端到端 fixture。插件仓库独立分支通过 21 个测试与清单检查。新增原生模块与上文已验证的 Release 源码保持一致。
