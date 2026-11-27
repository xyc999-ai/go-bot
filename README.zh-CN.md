<h1 align="center">go-bot</h1>

<p align="center">
  <a href="README.md">English</a> · <strong>简体中文</strong>
</p>

<p align="center">
  <img src="assets/harness-whale.png" width="184" alt="go-bot 蓝鲸头像">
</p>

go-bot 是用 Go 实现的 Harness 系统，也是一款可自托管的 Personal Agent，
可作为 [Meta Muse](https://about.fb.com/news/2026/09/introducing-muse-personal-ai-agent/)、
[OpenAI dots](https://openai.com/zh-Hans-CN/index/introducing-dots/) 和
[Grok Bot](https://help.x.com/en/using-x/about-grok) 的开源替代方案。个人可以用 Docker 在本地运行、开发和测试。

Go Bot 会记住与你交流的背景，持续关注你关心的事，并主动带来值得了解的新发现和想法。

企业团队可以用阿里云弹性容器实例（ECI）为每个账号按需启动独立 Host，按需持久化数据，
并接入自己的前端和业务服务，构建自己的 Personal Agent 产品。

- **网络搜索和浏览器操作。** 配置搜索供应商后即可检索网页。Agent 能操作 Chromium，打开页面、填写表单和截图。
- **生成和编辑图片。** 可以用文字生图，也可以上传多张参考图，生成结果会直接显示在会话中。
- **处理 Office 文件。** 使用 OfficeCLI 读取、生成和编辑 Word、PPT、Excel 文件，并提供下载。
- **长期记忆和跨会话检索。** 按账号保存用户偏好和 Agent 笔记，任务需要时可以查找同账号的历史会话。
- **主动发现与跟进。** 启用主动服务后，AI 会结合对你的了解收集和梳理相关信息，探索值得关注的新线索，并在它认为合适的时候，主动与你分享新的发现和建议。

可以使用网页端、桌面端或移动端聊天、浏览文件和下载交付物，也可以通过 Controller 的 HTTP 和 SSE 接口接入自己的前端或消息渠道。 [HTTP API](docs/api.md) · [最小前端示例](examples/frontend-minimal/index.html)

[配置](docs/configuration.md) · [部署](docs/deployment.md) · [HTTP API](docs/api.md) · [架构](docs/architecture.md) · [插件仓库](https://github.com/xyc999-ai/go-bot-plugins)

## 客户端与下载

<p align="center">
  <a href="https://github.com/xyc999-ai/go-bot/releases/latest"><img src="assets/downloads/macos-arm64.svg" alt="下载 macOS Apple Silicon（ARM64）版 Go Bot"></a>
  <a href="https://github.com/xyc999-ai/go-bot/releases/latest"><img src="assets/downloads/macos-x64.svg" alt="下载 macOS Intel（x64）版 Go Bot"></a>
  <a href="https://github.com/xyc999-ai/go-bot/releases/latest"><img src="assets/downloads/windows-x64.svg" alt="下载 Windows x64 版 Go Bot"></a>
  <a href="https://github.com/xyc999-ai/go-bot/releases/latest"><img src="assets/downloads/android-arm64.svg" alt="下载 Android ARM64 版 Go Bot"></a>
</p>

点击任一徽章进入最新 Release，在 **Assets** 中选择对应平台的安装包。
桌面客户端连接同一台电脑上运行的 Controller；Android 通过手机可访问的 URL 连接 Controller。
请先按[快速开始](#快速开始)启动服务。[网页端](#网页端与桌面端)随 Controller 提供，在浏览器中打开即可，无需单独下载。

## 演示

### 移动端演示


<table align="center" width="100%">
  <tr>
    <td width="33%" valign="top" align="center">
      <h4>Word 文档</h4>
      <a href="docs/showcase/mobile/word.gif"><img src="docs/showcase/mobile/word.gif" width="260" alt="英文移动端会话制作和修改 Word 简报，并在 WPS Office 中展示成品"></a>
      <p>制作 AI 试点简报，再通过多轮对话明确负责人、指标与评审节点。</p>
    </td>
    <td width="33%" valign="top" align="center">
      <h4>PPT 汇报</h4>
      <a href="docs/showcase/mobile/slides.gif"><img src="docs/showcase/mobile/slides.gif" width="260" alt="英文移动端会话制作和修改 PPT，并在 WPS Office 中展示成品"></a>
      <p>制作三页汇报，并将最后一页改为清晰的试点检查点。</p>
    </td>
    <td width="33%" valign="top" align="center">
      <h4>图片生成与编辑</h4>
      <a href="docs/showcase/mobile/image.gif"><img src="docs/showcase/mobile/image.gif" width="260" alt="英文移动端会话中的两张参考图、工具图片预览和生成的珊瑚色鲸鱼"></a>
      <p>上传鲸鱼和色板，用参考图生成珊瑚色版本，并全屏查看结果。</p>
    </td>
  </tr>
</table>

<table align="center" width="67%">
  <tr>
    <td width="50%" valign="top" align="center">
      <h4>定时任务</h4>
      <a href="docs/showcase/mobile/schedule.gif"><img src="docs/showcase/mobile/schedule.gif" width="260" alt="英文移动端会话中的提醒请求、创建确认和到点送达的检查清单"></a>
      <p>安排一次提醒，到点后在同一会话中收到检查清单。</p>
    </td>
    <td width="50%" valign="top" align="center">
      <h4>丰富的插件和连接器</h4>
      <a href="docs/showcase/mobile/plugins.png"><img src="docs/showcase/mobile/plugins.png" width="260" alt="移动端我的页面选中插件市场分栏，展示五个可用插件"></a>
      <p>通过插件市场扩展浏览器、Office、Google Workspace、X 等工具和服务。</p>
    </td>
  </tr>
</table>

### 网页端 / 桌面端演示

网页端和桌面端有相近的界面与聊天体验。以下录屏使用内置网页和本地 Docker Host，所有会话均用中文提问。Agent 用 Chromium 查阅
[斯坦福 HAI《2026 AI Index》经济章节](https://hai.stanford.edu/ai-index/2026-ai-index-report/economy)，
再用 OfficeCLI 把查到的资料整理成 Word 行业报告和 PPT，并根据后续消息修改两份文件。

<table align="center" width="100%">
  <tr>
    <td width="33%" valign="top" align="center">
      <h3>浏览器调研</h3>
      <img src="docs/showcase/ai-research-demo.gif" width="260" alt="中文调研请求、Agent 交付的资料附件与斯坦福 HAI 原文图表">
      <p>Agent 用 Chromium 核对行业数据，交付原文图表和调研摘要。</p>
      <a href="docs/showcase/ai-research-chat.webp">看会话截图</a> · <a href="docs/showcase/ai-index-source.webp">看原站图表</a>
    </td>
    <td width="33%" valign="top" align="center">
      <h3>Word 行业报告</h3>
      <img src="docs/showcase/ai-word-demo.gif" width="260" alt="制作和校订八页 Word 行业报告的完整会话，以及在 WPS Office 中打开的最终成品">
      <p>制作含封面、数据图表和清晰正文层次的行业报告，再按反馈校订内容与分页，在 WPS Office 中查看最终成品。</p>
      <a href="docs/showcase/ai-word-demo.gif">查看大图</a>
    </td>
    <td width="33%" valign="top" align="center">
      <h3>PPT 汇报</h3>
      <img src="docs/showcase/ai-slides-demo.gif" width="260" alt="制作和修改 PPT 的完整会话，以及 WPS Office 中第二页的行业数据展示">
      <p>核对行业数据、补充调查口径并修改汇报，在 WPS Office 中展示关键数字页。</p>
      <a href="docs/showcase/ai-slides-demo.gif">查看大图</a>
    </td>
  </tr>
</table>

<table align="center" width="67%">
  <tr>
    <td width="50%" valign="top" align="center">
      <h3>两张参考图编辑图片</h3>
      <img src="docs/showcase/ai-image-demo.gif" width="260" alt="上传蓝鲸和色板、编辑图片并在会话中交付结果">
      <p>参考蓝鲸 Logo 和珊瑚杏色色板，重新配色并在会话中交付一张 2048 × 2048 的图片。</p>
      <a href="docs/showcase/ai-image-result.webp">看生成结果</a>
    </td>
    <td width="50%" valign="top" align="center">
      <h3>定时任务</h3>
      <a href="docs/showcase/web-schedule-zh.gif"><img src="docs/showcase/web-schedule-zh.gif" width="260" alt="中文网页会话中的定时任务请求、创建确认和到点送达的提醒"></a>
      <p>创建一次性提醒。任务到点后，检查清单会送达原会话。</p>
    </td>
  </tr>
</table>

## 快速开始

安装 Docker Engine 或 Docker Desktop、Docker Compose v2、curl 和 Bash。无需克隆源码或安装 Go：

```sh
curl -fsSL https://raw.githubusercontent.com/xyc999-ai/go-bot/main/scripts/install.sh | bash
cd go-bot
docker compose up -d
```

安装器下载仓库原始的 `compose.yaml`、`.env.example` 和 `configs/default.yaml`，仅在缺失时
生成 `.env`，并拉取已发布的 Controller、Host 和 Redis 镜像。已有配置保留，不启动服务，
不生成 `.env.eci`。Compose 启动 Controller 和 Redis；每个账号的 Host 由 Controller 动态创建，
是独立容器。应用镜像提供 AMD64 和 ARM64，Docker 自动选择匹配架构。
[源码开发启动](docs/deployment.md#source-checkout)

### 网页端与桌面端

启动 Controller 后，可以选择桌面客户端或网页端，两者共用下面的账号、模型配置和聊天流程。

1. **打开客户端。** 桌面端从[最新 Release](https://github.com/xyc999-ai/go-bot/releases/latest)
   下载并安装 macOS 或 Windows 版本，打开 **Go Bot**，它会自动连接本机 Controller。
   网页端在浏览器访问 [http://127.0.0.1:8080](http://127.0.0.1:8080)，Controller 已经提供网页。
   在任一客户端输入账号名创建账号；打开账号时，Controller 会启动独立的 Docker Host 容器，
   并直接进入带有欢迎消息的主会话。
2. 在 **Model settings** 通过协议下拉框选择 **OpenAI Responses** 或 **Anthropic Messages**，填写端点地址、API Key 和至少一个模型 ID，
   返回主会话即可发消息；**New chat** 用于创建独立主题的会话。
3. 在 **Settings → Web search** 配置 DeepSeek、智谱、豆包、Tavily 或 Brave Search，保存后点击 **Use provider** 选用。需要生图及其他 Gemini 媒体能力时，在插件市场安装并启用 **Gemini API CLI**，然后配置其 API Key。[供应商配置](docs/providers.md) · [插件连接](docs/plugin-account-setup.md#gemini-api-cli)

可在账号页或「设置」选择简体中文或 English。各客户端会记住语言偏好。

新账号会自动安装并启用 **agent-browser** 和 **OfficeCLI**，安装期间可以先配置模型。后续登录复用安装结果，也不会恢复你已停用或卸载的插件；可在侧栏「插件市场」管理。插件包、原始 Skills、图标和教程由独立的[插件仓库](https://github.com/xyc999-ai/go-bot-plugins)发布；刷新市场即可发现新版本，无需重构 Host 镜像。

主动服务默认关闭。配置可用模型后，可在「设置 → 主动服务」开启：结合近期交流和记忆自主研究，在合适时将发现带到主会话，并产生相应的模型与服务用量；设置页也可查看下次检查安排。[工作机制](docs/configuration.md#proactive-assistance)

### 手机通过 Tailscale 连接

Tailscale 会让手机和运行 Controller 的电脑加入同一个私有网络
（tailnet）。手机离开家里的 Wi-Fi 后，仍然可以通过这个网络访问 Controller。

1. **在电脑和手机上下载 Tailscale。** 在运行 Controller 的电脑上打开 [Tailscale 官方下载页](https://tailscale.com/download)，选择对应的操作系统。手机也从该页面进入 iOS App Store 或 Android Google Play 安装。Android 用户可在 [GitHub 最新正式版](https://github.com/xyc999-ai/go-bot/releases/latest)的 **Assets** 中下载 Go Bot APK。
2. **两端登录同一个 Tailscale 账号。** 按提示允许系统添加 VPN 配置，并在电脑和手机上开启连接。登录后，设备会加入同一个 tailnet。在电脑的 **Devices** 页面确认两台设备都在线，点选电脑即可看到它的 MagicDNS 地址。也可以打开 [Tailscale 设备管理页](https://login.tailscale.com/admin/machines) 核对。

   <img src="docs/showcase/tailscale-devices.png" width="800" alt="macOS 上的 Tailscale 设备列表，两台设备均已连接，右侧显示所选电脑的 MagicDNS 地址。账号和设备信息已替换为示例。">

   *两台设备都已在线，右侧是所选电脑的 MagicDNS 地址。图中的名称和地址均为示例。*

3. **在运行 Controller 的电脑上启动 Tailscale Serve。** 等 Controller 启动后再运行下面的命令。Serve 把 tailnet 内的 HTTP 请求转发给本机的 `127.0.0.1:8080`。设备之间的流量由 Tailscale 加密，这个流程无需启用 HTTPS 证书。

   ```sh
   tailscale serve --bg --http=8081 http://127.0.0.1:8080
   ```

   如果终端找不到 `tailscale`，参阅 [Tailscale 命令行配置指南](https://tailscale.com/docs/reference/tailscale-cli?tab=macos)。
4. **在手机上连接 Go Bot。** 复制 Serve 输出的完整地址，例如 `http://my-computer.example-tailnet.ts.net:8081`。先用手机浏览器打开，确认能看到 go-bot 网页，然后在 App 填入同一个地址和 go-bot 账号名，点「连接」。新账号可以在「我的 → 模型」里配置模型。 [Tailscale Serve 指南](https://tailscale.com/docs/features/tailscale-serve)

如果已有手机可以访问的 HTTPS 入口，也可以直接在 App 中填写该地址。

**选择部署方式。** 本地 Docker 适合个人开发和测试。阿里云 ECI 可作为企业部署个人 Agent 的计算基础，为每个账号按需启动独立 Host，并可选用 NAS 持久化。企业部署可通过组织现有的认证网关接入 Controller。

## 各组件如何运行

**Controller** 提供网页和 HTTP API，管理 Host，并转发 HTTP/SSE。**Redis** 保存账号、设置和
Host 状态。**Host** 运行 Go Agent 并直接访问模型服务，管理工具、v4 会话日志、记忆、定时任务、
附件和工作区。每个账号有自己的 Host，打开账号时按需启动。

Host 预装 Chromium、Python、Node.js 与通用文档处理环境。
新账号自动安装并启用 **agent-browser** 和 **OfficeCLI**，无需 API Key。
安装时准备固定版本的官方 CLI 和原始 Skills，启用只加载能力。
OfficeCLI 提供 11 个原始 Skills，agent-browser 提供官方发现 Skill；资源保存在
`/root/harness/.dsh/plugins/profile/node_modules/` 的对应包内，不再复制到用户 Skill 目录。
停用会撤销 Skills，保留已安装的 CLI。模型先看到名称和摘要，再按需加载正文。

插件市场还提供 **Gemini API CLI**。安装并启用后，在「账号与授权 → 配置 API Key」
填写从 [Google AI Studio](https://aistudio.google.com/apikey) 获取的密钥，Agent 即可通过
官方 `gemini-api` 命令生成媒体、分析文件和开展研究。密钥保存在 CLI 默认的
`/root/.config/gemini-api/config.yaml`，不在工作区卷内；更换 Host 容器后需要重新配置。
[连接指引](docs/plugin-account-setup.md#gemini-api-cli)

| 运行模式 | Host 数据存在哪里 | 点击 **Stop Host** 后 |
| --- | --- | --- |
| 本地 Docker | 账号自己的命名卷，挂到 `/root/harness` | 停止容器，保留文件 |
| ECI + `container` | ECI 实例的容器文件系统 | 释放实例及其文件 |
| ECI + `nas` | NAS 的账号目录，挂到 `/root/harness` | 释放实例，保留 NAS 文件 |

关闭网页后 Host 会继续运行。ECI 用完后可点击 **Stop Host** 释放实例，停止该实例的计算计费。

## 移动端

移动端在 iOS 和 Android 上连接同一套 Controller，支持会话、模型切换、附件、任务状态和账号设置。填写 Controller 地址和账号即可连接。 [移动端开发指南](mobile/README.md)

## 阿里云 ECI

在能够访问 ECI 私网的机器上通过 Docker Compose 运行 Controller 和 Redis，例如同一 VPC 内的
一台 ECS。每个账号的 Agent 云电脑运行在独立的 ECI 实例中。准备好云资源和 Ready 镜像缓存后，
由部署者自行启动服务。项目提供准备工具，部署者使用自己的阿里云配置执行；本地 Docker
Quickstart 的 install.sh 不承担 ECI 准备，也不会创建云资源。

### 1. 拉取仓库并准备本地工具

在 Controller 所在机器上安装 Git、带 Compose v2 的 Docker 和 Bash，然后执行：

```sh
git clone https://github.com/xyc999-ai/go-bot.git
cd go-bot
docker pull ghcr.io/xyc999-ai/go-bot-controller:latest
docker pull redis:8-alpine
./scripts/start.sh --init-env
```

显式初始化会生成 `.env` 和 `.env.eci`，保留已有文件；ECI 使用 `.env.eci`。
缓存工具已包含在 Controller 镜像中，无需在本机安装 Go 或下载 Host 镜像。
以上命令不会启动 Controller、Redis 或账号的 Host。

### 2. 准备云资源并填写 `.env.eci`

| 资源 | 需要提前准备 |
| --- | --- |
| RAM 凭证 | 创建 RAM 用户及 AccessKey ID、Secret，授予查询、创建和释放 ECI 实例、查询镜像缓存所需的权限。如需制作缓存，再授予创建缓存权限。 [创建 AccessKey](https://www.alibabacloud.com/help/en/ram/user-guide/create-an-accesskey-pair) · [RAM 用户授权](https://www.alibabacloud.com/help/en/ram/user-guide/grant-permissions-to-the-ram-user) |
| 网络 | 选择地域、交换机和安全组，允许 Controller 通过私网访问 ECI 的 TCP 8080。镜像仓库、模型服务或网页需要公网访问时，可为 ECI 所在交换机配置公网 NAT 网关 SNAT。启动流程不会为每个 Host 分配 EIP。 [VPC 与交换机](https://www.alibabacloud.com/help/en/vpc/vpc-and-vswitch) · [安全组规则](https://www.alibabacloud.com/help/en/ecs/user-guide/security-group-rules) · [ECI 公网访问](https://www.alibabacloud.com/help/en/eci/user-guide/enable-internet-access) |
| NAS（可选） | 准备 ECI 可访问、可写的 NAS 挂载点。账号数据使用 NAS 上的 `/go-bot/<userId>`，挂载至 Host 的 `/root/harness`。 [ECI 挂载 NAS](https://www.alibabacloud.com/help/en/eci/user-guide/mount-a-nas-volume) |
| 镜像缓存 | 在第 3 步使用公共 Host 镜像创建，或者复用所选地域中与 `HOST_IMAGE` 完全对应的 Ready 缓存。 [管理 ECI 镜像缓存](https://www.alibabacloud.com/help/en/eci/user-guide/manage-an-image-cache) |

编辑已预设 `HARNESS_RUNTIME=eci` 的
`.env.eci`，填写 `ECI_REGION`、`ECI_VSWITCH_ID`、`ECI_SECURITY_GROUP_ID`、
`ALIBABA_CLOUD_ACCESS_KEY_ID` 和 `ALIBABA_CLOUD_ACCESS_KEY_SECRET`。

使用实例存储时保留 `HARNESS_STORAGE=container`。需要在实例替换后保留数据时，设置
`HARNESS_STORAGE=nas`，并将 NAS 挂载点域名填入 `NAS_MOUNT_TARGET`。
账号目录会在 Host 启动前挂载。

### 3. 准备 Host 镜像缓存

`HOST_IMAGE` 保持模板中的公共镜像地址即可。已有与该地址完全对应的 Ready 缓存时，
填写 `ECI_IMAGE_CACHE_ID` 后直接进入第 4 步；否则由部署者明确执行：

```sh
./scripts/buildeci.sh --env-file .env.eci
```

脚本使用本地 Controller 镜像中的缓存工具，让阿里云拉取公共 `HOST_IMAGE` 对应的 AMD64 镜像。
无需在本机构建、推送或下载 Host 镜像，也无需安装 Go 或登录镜像仓库。缓存进入 Ready 后，
工具自动将 ID 写回 `.env.eci` 的 `ECI_IMAGE_CACHE_ID`。制作缓存会使用阿里云计费资源。
工具直接使用公共 `HOST_IMAGE`，不需要镜像仓库凭证或额外的推送地址配置。
该命令只准备缓存，不启动 Controller、Redis 或账号 Host；失败或超时不会覆盖已有缓存 ID。
[接口参数、缓存配置与失败恢复](docs/deployment.md#cache-api-request)

### 4. 启动 Controller 并打开账号

在 Controller 所在机器上使用填好的私有配置：

```sh
./scripts/start.sh --env-file .env.eci
```

脚本校验配置，然后用 Compose 启动 Controller 和 Redis。随后在浏览器打开 Controller 地址，
创建或打开账号，Controller 才会直接使用 `HOST_IMAGE` 和 `ECI_IMAGE_CACHE_ID` 创建该账号的
ECI Host。仅制作缓存不会创建用户 Host。更新 Host 镜像时，先重新制作缓存并更新 ID，再替换实例。

## 配置与检查

Controller 启动时读取并校验挂载的 `.env` 和 `configs/default.yaml`，直接用 Compose 启动也会检查。
普通部署修改 `.env` 即可。仓库中的 `start.sh` 会将显式 shell 环境变量传给 Controller，
不会拉取或构建镜像。环境变量只在内存中覆盖本次进程的配置，不写回 `.env`；
显式空值会清空本次进程中的对应值。Compose 只读挂载所选 env 文件和基础 YAML，不挂载整个目录。
模型和网页搜索由用户在设置界面按账号配置；Gemini 生图等能力在插件详情中配置 API Key。

用下面的命令检查 ECI 配置或运行环境：

```sh
docker compose run --rm --no-deps controller check --env-file /config/deployment.env --config /runtime.yaml
docker compose run --rm --no-deps controller doctor --env-file /config/deployment.env --config /runtime.yaml
```

ECI 模式在上述命令前加 `GO_BOT_ENV_FILE=.env.eci` 选择配置。命令使用本地 Controller 镜像中的工具，
无需安装 Go 或先启动 Redis。`check` 离线校验配置及必填项。
`doctor` 还会检查本地 Host 镜像或阿里云 Ready 镜像缓存。实际创建 Host、挂载存储，
以及验证 Controller 到 Host 的连通性，发生在打开账号时。
[配置字段](docs/configuration.md) · [故障排查](docs/troubleshooting.md)

## 依赖

| 用途 | 在执行命令的机器上安装 |
| --- | --- |
| Quickstart | Docker Engine 或 Docker Desktop、Docker Compose v2、curl 和 Bash。 |
| 在源码目录通过 `scripts/start.sh` 启动服务 | Docker、Docker Compose v2 和 Bash。提前准备环境文件和所需本地镜像；脚本不拉取或构建镜像。 |
| 通过 `scripts/buildeci.sh` 制作 ECI 镜像缓存 | Docker、Docker Compose v2 和 Bash、本地 Controller 镜像，以及填写云凭证和网络参数的 ECI 环境文件。 |
| 在 Docker 外开发 | 后端需要 Go 1.26 或更高版本，原生 Controller 需要 Redis，前端开发需要 Node.js 22.12+ 和 npm。 |
| 开发 Cordis 插件运行时 | 按 `extensions/cordis/package.json` 使用 Node.js 24.9+ 和 pnpm 11.25.0。 |

[ECI 完整流程](#阿里云-eci)还需要 Git 获取源码，并提前准备该节列出的云资源。
`buildeci.sh` 会用 Controller 镜像启动临时容器，运行其中打包的 `buildeci` 程序，
调用阿里云拉取 Host 镜像并制作缓存，待缓存就绪后将 ID 回写选定的环境文件。
此步骤不依赖正在运行的 Controller 服务，无需在本机构建 Host 或安装 Go，也不会启动账号的 Host。

Compose 负责启动 Redis 和内嵌前端的已发布 Controller。Agent 使用的 Python、Node.js、Chromium 和通用文档环境已包含在 Host 镜像中。
新账号会自动安装并启用 agent-browser 和 OfficeCLI，包括官方 CLI 依赖及 Skills；首次安装需要联网，后续可以通过插件市场管理。

## 插件

插件维护在独立的 [go-bot-plugins 仓库](https://github.com/xyc999-ai/go-bot-plugins)，
包含插件包、展示资源、上游 Skills 和按版本发布的市场目录。插件发布独立于 Controller 和 Host 镜像，
可在 Go Bot 的**插件市场**中安装并启用 agent-browser、OfficeCLI、Google Workspace、X、Gemini API CLI 等集成。

新增集成或改进现有插件，请向插件仓库提交 PR。
[插件贡献指南](CONTRIBUTING.md#contributing-plugins)说明了代码归属、包结构、版本规则和检查命令。

## 开发与扩展

**添加插件**：在 [go-bot-plugins](https://github.com/xyc999-ai/go-bot-plugins) 中使用原生 Cordis 和异步 `ctx.host` 接口，可贡献工具、Skills、附加提示及接入凭证、授权和 MCP；Harness 核心仍由 Go 内置实现。[插件 SDK](extensions/sdk/README.md) · [示例](examples/plugins/README.md) · [插件贡献](CONTRIBUTING.md#contributing-plugins)

可以用 Host 已有工具组合工作流，也可以接入第三方服务。例如，旅行助手可以查询火车票和机票、
对比酒店价格及取消条款，再整理成行程。若要实现预订，可接入供应商 API，并在下单前单独展示日期、
总价和取消条款，让用户确认。接入方自行提供服务凭证，并将连接权限绑定到已认证的账号。

- **新增工具和部分第三方服务。** 在自定义 Host 中实现 Go Tool 接口，并加入 Host 的工具集合。把外部 SDK 和 HTTP 调用放在 `thirdparty/`，运行环境、模型、搜索和生图的适配放在 `providers/`。 [工具接口](harness/agent/native_contract.go) · [Host 工具组装](harness/assembly/native_runtime.go) · [供应商扩展指南](docs/providers.md)
- **定制产品入口。** 通过 HTTP 和 SSE 接入自己的聊天前端或消息渠道，由产品层处理账号认证、连接权限，以及预订和付款等操作的用户确认。 [API 文档](docs/api.md)

[文档索引](docs/README.md) · [贡献指南](CONTRIBUTING.md) · [安全报告](SECURITY.md) · [MIT 许可证](LICENSE) · [第三方声明](THIRD_PARTY_NOTICES.md)

## 支持与贡献

如果 Go Bot 对你有帮助，欢迎在 [GitHub 上点一个 Star](https://github.com/xyc999-ai/go-bot)，让更多人发现这个项目。

欢迎带着任何想法参与贡献：新功能、问题修复、插件、文档、翻译和使用示例都可以。
可以直接提交 PR，无需先提 Issue。请参考[贡献指南](CONTRIBUTING.md)，
也欢迎到[插件仓库](https://github.com/xyc999-ai/go-bot-plugins)贡献新的集成。

## 社区与联系

欢迎通过 [Issue](https://github.com/xyc999-ai/go-bot/issues/new) 提出问题、分享想法或反馈建议，
也可以直接联系项目作者：

- 邮箱：[xyc00666@gmail.com](mailto:xyc00666@gmail.com)
- 微信：`Xyc--999`
- X：[@xyc_999](https://x.com/xyc_999)

欢迎有意将该项目商业化的团队或个人联系交流。

### QQ 交流群

欢迎加入 QQ 群 **go-bot交流**，交流部署和使用经验，也可以提出问题或反馈建议。
用 QQ 扫描下方二维码，或直接搜索群号 **710767129**。

<a href="assets/qq-community.jpg"><img src="assets/qq-community.jpg" width="260" alt="go-bot交流 QQ 群二维码，群号 710767129"></a>

## 上游项目与致谢

- [Gemini API CLI](https://github.com/google-gemini/gemini-api-cli) 提供调用 Gemini 模型与托管 Agent 的官方命令行接口。

- [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 是本 Go 实现所参考的上游项目。 [兼容性说明](docs/compatibility.md)
- [Hermes Agent](https://github.com/NousResearch/hermes-agent) 启发了本地长期记忆和跨会话历史检索的设计。 [记忆设计](docs/hermes-agent-memory-design.zh-CN.md)
- [OfficeCLI](https://github.com/iOfficeAI/OfficeCLI) 提供 Word、PPT、Excel 处理能力。Host 也包含 [LibreOffice](https://www.libreoffice.org/)。
- [agent-browser](https://github.com/vercel-labs/agent-browser) 提供 CLI 和 Skill，Host 用它们操作 [Chromium](https://www.chromium.org/)。

随项目分发的组件及其许可见[第三方声明](THIRD_PARTY_NOTICES.md)。
