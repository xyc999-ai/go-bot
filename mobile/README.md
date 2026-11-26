# go-bot 移动端

Expo SDK 58 / React Native 项目，运行在 iOS 和 Android 上。用同一个账号连接 `go-bot` Controller，即可打开会话、查看 Host 文件和定时任务，并配置模型、网页搜索和图片生成。

## 本地运行

准备 Node.js 22.13 或更新的兼容版本，以及 iOS 的 Xcode 或 Android 的 Android Studio 和 SDK。先按仓库根目录的部署说明启动 Controller，再在仓库根目录执行以下命令。

```sh
cd mobile
npm ci
npm run ios
# 或者
npm run android
```

开发时可运行 `npm start` 重新启动 Metro。Expo 生成的原生工程由 `app.json` 配置。使用实体设备时，连接或启动设备后运行 `npm run ios -- --device` 或 `npm run android -- --device`。iOS 实体设备需要在 Xcode 中配置签名。

### 重新生成工程和打包

在 `mobile/` 运行 `npx expo prebuild --clean`，从 Expo 配置重新生成 Android 和 iOS 工程。两端的应用 ID 都是 `com.gobot.mobile`。原生配置应写入 `app.json`，这样重新生成时会保留 Android Release 的 R8 设置和连接配置。

在 `android/` 运行 `./gradlew :app:assembleRelease -PreactNativeArchitectures=arm64-v8a` 可生成 ARM64 Release APK。发布时用 Android SDK 的 `zipalign -P 16` 对齐、再用 `apksigner` 签名，最后验证签名和对齐。发布密钥保存在项目的本地忽略目录 `.local/signing/android/`，不能提交到 Git。

### GitHub Actions：与桌面端一起发布

在 **Actions → Client release → Run workflow** 手动运行
[发布工作流](../.github/workflows/client-release.yml)，选择需要构建的分支，手动填写
Tag 名称（例如 `v0.0.4` 或 `client-build-2026-11`），无需提前创建 Tag，也不限制
项目专用格式或要求 Tag 与包版本对应。一次运行并行构建
macOS、Windows 和 Android，全部成功后才创建或更新同一份**草稿 Release**。
构建固定使用所选分支在运行时的提交；创建草稿 Release 时，GitHub 会为不存在的
Tag 使用这个提交，已有 Tag 则保留原指向。推送代码或标签不会自动构建，也不会自动公开发布。

所有客户端版本保持一致，CI 从 `desktop/package.json` 的 `version` 读取主版本，
并校验 `mobile/package.json`、`mobile/app.json` 和 `frontend/package.json` 一致。
更新版本时也要同步三个客户端的 `package-lock.json` 根版本。当前统一为 `0.0.4`，
Android 的 `android.versionCode` 为 `4`；以后每次 Android 升级还需递增这个整数。
Expo Prebuild 会将 `expo.version` 同步到 iOS 展示版本和 Android `versionName`，
将 `android.versionCode` 和 `ios.buildNumber` 写入对应工程，但不会自动递增构建号。
当前 iOS `ios.buildNumber` 也显式设置为 `"4"`。两端构建号不要求相同；iOS 同一
展示版本再次上传 TestFlight / App Store 时应使用新的构建号，Android 升级应增加
`versionCode`。这些构建号与统一展示版本 `0.0.4` 分开管理。
项目包名分别是 `go-bot-desktop`、`go-bot-mobile`、`go-bot-web`。
Release 标题自动生成为 `Go Bot v<版本>`，例如 `Go Bot v0.0.4`，与手动输入的 Tag 独立。

CI 使用 Node 24、JDK 17、Android SDK 37 / Build Tools 37.0.0 和
React Native 0.88 对应的 NDK 27.1.12297006。先运行类型检查和测试，执行
`expo prebuild --platform android --clean --no-install` 生成临时工程，再构建 ARM64
Release APK。`with-unsigned-android-release` 插件确保 Release 不使用 Expo 模板的
调试签名，开发用 Debug 签名保持可用。随后用既有发布密钥签名，并验证包名、
版本、架构、非调试模式、证书 SHA256 和 16KB ZIP 对齐。

在仓库 **Settings → Secrets and variables → Actions** 配置：

| 类型 | 名称 | 内容 |
| --- | --- | --- |
| Secret | `ANDROID_KEYSTORE_BASE64` | **既有 Android 发布 keystore** 的 Base64（单行） |
| Secret | `ANDROID_KEYSTORE_PASSWORD` | keystore 密码 |
| Secret | `ANDROID_KEY_ALIAS` | 既有发布密钥 alias |
| Secret | `ANDROID_KEY_PASSWORD` | 该密钥密码，即使与 keystore 密码相同也需配置 |
| Variable | `ANDROID_SIGNING_CERT_SHA256` | 既有发布证书的 SHA256 指纹，64 位十六进制，可带冒号 |

必须沿用之前发布 APK 的密钥，不能生成新密钥替代，否则已安装用户无法覆盖升级。
可以对之前正式分发的 APK 运行 `apksigner verify --print-certs <旧APK>`，从
`certificate SHA-256 digest` 行获取公开证书指纹；不要用 APK 文件自身的
SHA256 替代证书指纹。缺少配置、证书不匹配或签名/对齐失败时，本次 Release 组装失败。
签名文件只在签名步骤写入，临时 keystore 在 always-run 清理步骤删除，不上传为产物。

产物为 `go-bot-<Android版本>-android-arm64.apk` 和对应的 `.verification.json`，
与桌面安装包一起附在草稿 Release 中，统一的 `SHA256SUMS` 包含所有产物。
安装 APK 时允许浏览器或文件管理器安装来自此来源的应用；安装后在 App 中填写
手机可访问的 Controller 地址。当前不生成 AAB、不上传 Google Play，也不构建 iOS。

`with-shared-source-inputs` 配置插件会把共用的 `frontend/src` 纳入 Android JavaScript 打包任务的输入，修改共享文案、会话投影或授权交互后，增量 Release 构建也会更新包。Metro 的 React 映射和 TypeScript 路径使用移动端自己的依赖，无需另外安装 Web 的 node_modules。

TypeScript 中的 React 路径只指向类型声明；`experiments.tsconfigPaths=false` 避免 Metro 把声明文件当成运行时代码。运行时通过 `metro.config.js` 解析实际 React。

在连接页填写 Controller 入口地址和账号名。可以连接已有账号，也可以在同一页面创建账号。新账号在「我的 → 模型」设置端点和默认模型后即可发送第一条消息。iOS 模拟器可直接填同机的 `http://127.0.0.1:8080`。

## 手机连接

通过 Tailscale 连接时，从 [Tailscale 官方下载页](https://tailscale.com/download) 给电脑和手机安装客户端。两端登录同一个 Tailscale 账号，确认设备在线后，在电脑上启动 Serve，再将输出的完整 `http://…ts.net:8081` 地址填入 App。 [图文步骤](../README.zh-CN.md#手机通过-tailscale-连接)

如果已有受访问控制的 HTTPS 入口，在 App 中填入该入口地址即可。 [代理配置](../docs/configuration.md#runtime-storage-and-entry)

## 使用

- **会话**位于导航中间。登录或冷启动直接进入账号固定的主会话，首次显示一条持久化欢迎消息。主会话固定在抽屉顶部且不可归档。首次自动安装并启用 OfficeCLI 和 agent-browser，安装期间仍可配置模型。发送消息或新建普通会话前检查模型配置，失败时保留草稿。左上角可新建、切换与归档会话，右上角可逐层浏览 Host 的 `/root/harness`。输入框可切换端点与模型。附件选择后先加入草稿，点发送后随消息上传。
- **任务**可创建、编辑、暂停、恢复或立即运行 Host 上的定时任务，查看下次运行和最近一次投递时间，并打开对应会话查看执行结果。
- **我的**可配置模型、搜索和生图供应商。点击头像可从图库选择或拍照，头像按账号名保存在设备上。语言可在此切换简体中文和 English，重启后保留。系统原生选择器使用系统语言。点击 `Stop Host` 调用 Controller 停止当前账号的 Host，完成后返回连接页。

从系统导入的图片和文件会保存在 App 的本地库。会话中的交付文件要点下载才会存入本地文件区。聊天框中点「＋」进入「图片」或「文件」，这两个本地库在同一 App 安装内共用，切换会话或账号仍可访问。多选后可添加到草稿、通过系统分享面板发送到其他 App，或从本地库删除。会话图片可点开全屏预览，长按「保存图片」同时存入 App 和手机图库。从 App 本地图片库删除时只删除 App 内的副本。

来自同一账号和同一个 Host 路径的交付文件共用一个本地条目。再次下载会刷新副本。同名文件如果来自不同路径，会各自保存原文件名，并在文件列表里显示保存时间。

## 开发检查

```sh
npm run typecheck
npx expo install --check
npx expo export --platform ios --platform android
```

移动端与 Web 共用会话事件投影，并复用 [Controller HTTP API](../docs/api.md)。定时任务的状态、规则和历史由 [Host Schedule](../docs/schedule-parity-plan.zh-CN.md) 管理。设计参考与原型见 [设计说明](design/README.md)。
