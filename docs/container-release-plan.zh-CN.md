# GHCR 双镜像与 curl 安装方案

状态：代码已实现并通过本地验证；GHCR 首次发布及本次 ECI/NAS 云端验收尚未执行。保留本次收口后的职责与部署流程，验证结果见末尾。本方案替代此前「Controller 与 Host 合并成一个镜像」的设计。

## 1. 确定的部署形态

Controller、Host 分别构建和发布，保留 `build/Dockerfile.controller`、`build/Dockerfile.host`。Redis 使用官方镜像。

两种应用镜像分别发布为 GitHub Container Registry（GHCR）的两个公共容器包：go-bot-controller、go-bot-host；冒号后的标签表达版本。代码仍在现有 go-bot GitHub 仓库中，不需要另外创建两个源码仓库。[GHCR 官方说明](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)

| 镜像示例 | 包含内容 |
| --- | --- |
| `ghcr.io/xyc999-ai/go-bot-controller:latest` | Controller、内嵌 Web、buildeci、Docker CLI |
| `ghcr.io/xyc999-ai/go-bot-host:latest` | Go Harness Host、Node/Python/Chromium、Office/PDF 基础环境和插件资源 |
| `redis:8-alpine` | 官方 Redis |

使用当前 GitHub 账号 xyc999-ai 作为 namespace。首批镜像采用项目当前 v0.0.1 版本；新工作流需要先从审查后的源码手动发布该版本，再开放安装入口，不重打已有 Git 标签。

按当前 GitHub 官方政策，公共 Packages 免费，GHCR 容器镜像的存储与带宽当前也免费；这不包含阿里云资源费用，也不能把它理解为所有 GitHub Actions 资源都无限免费。[GitHub 计费说明](https://docs.github.com/en/billing/concepts/product-billing/github-packages)

Controller + Redis 由 Compose 启动。每个账号的 Host 仍由 Controller 动态创建，不加入该 Compose，也不嵌在 Controller 容器内部。

Docker 模式下 Controller 通过宿主机 Docker socket 管理平级 Host 容器；ECI 模式下调用阿里云 API。Host 不挂载 Docker socket，也不接收阿里云管理凭证。

## 2. 用户不需要克隆 GitHub 仓库

公开部署只需 Docker Engine / Docker Desktop、Docker Compose v2、curl 和 Bash，不要求 Git、Go、Node.js、Python。

普通 install.sh 只负责本地 Docker 安装，不包含 ECI 参数、云资源检查或镜像缓存制作。Quickstart 从 main 分支的脚本开始；URL 要在脚本合入 main 后才可使用：

```bash
curl -fsSL https://raw.githubusercontent.com/xyc999-ai/go-bot/main/scripts/install.sh | bash
cd go-bot
docker compose up -d
```

脚本默认准备当前目录下的 `go-bot/`，也允许指定 `--dir`。安装结束只完成准备，不自动启动服务、不创建用户 Host。用户直接执行 docker compose up -d，启动 Controller 和 Redis，然后打开 `http://localhost:8080` 使用 Web。该路径不调用仓库中的 start.sh，也不将其放入用户安装目录；配置校验由 Controller 启动入口负责。

脚本不下载整个源码仓库，也不拉取镜像包的全部标签。它直接下载仓库已有的部署文件，并准备所选版本、目标架构需要的镜像，不维护另一份安装专用 Compose。

## 3. 本地会生成什么

```text
go-bot/
├── compose.yaml
├── .env.example
├── .env
├── install.sh
└── configs/
    └── default.yaml
```

- `compose.yaml`：直接下载仓库根目录的同名文件，与源码目录使用完全相同的 Compose；统一调整为只使用 image、不含 build。Controller 镜像版本写明在文件中。
- `.env.example`：直接下载仓库模板，保留中英文注释和空白凭证。
- `.env`：仅在不存在时复制 .env.example 创建，权限 0600；普通安装不生成 .env.eci。
- `configs/default.yaml`：直接下载仓库的同名配置，保留 Compose 的 YAML 挂载及 Controller --config 读取行为。
- `install.sh`：留存安装脚本，供再次准备 Docker 镜像。

安装入口直接来自 main 分支，不通过 GitHub Release 附件分发。安装器从同一仓库 main 下载 compose.yaml、.env.example、configs/default.yaml，保留原始相对目录结构，普通用户不需要自己逐个下载。文件下载和校验完成后才生成缺失的 .env；下载失败时停止，不使用不完整文件继续安装。安装器选择的默认镜像版本必须是已发布并能匿名拉取的版本，不能因为 main 上有新提交就引用尚未发布的镜像。

私有 env 位于 compose.yaml 同级目录，使用现有字面 KEY=value 规则；不另建 config/.env。Compose 自动读取根目录 .env 用于插值，与 Go 直接读取该文件是不同过程：应用凭证不通过 Compose 插值重新组装。环境覆盖实现必须区分调用者显式设置的进程变量和 Compose 自动读入的文件值，不能把后者误判为覆盖值并写坏原文件。

再次安装不覆盖已有 .env、用户修改的 configs/default.yaml 或 Compose，也不删除数据卷；配置有问题时报告具体字段。涉及升级的镜像版本和部署文件需由维护者明确更新，安装器不提供隐式升级或删除操作。

普通公开部署不再要求执行 start.sh 或 start.sh --init-env；Docker 初始化由 install.sh 完成。仓库中的 scripts/start.sh 保留给维护者和源码使用者，但也统一改为消费已有镜像，不拉取、不构建。源码构建另用明确的开发命令，不再藏在启动流程中。ECI 配置初始化和准备流程放在 README 的独立 ECI 章节，不通过普通安装器承载。

### 本次同时修改的三个入口文件

install.sh 与 start.sh 是独立脚本，不互相调用，也不依赖对方执行。本次实施必须同时完成以下内容，不能只新增安装器而保留旧启动行为：

| 文件 | 本次修改职责 |
| --- | --- |
| scripts/install.sh（新增） | 下载仓库原始部署文件；由 .env.example 初始化缺失的 .env；拉取镜像并检查；不启动 Compose、不调用或分发 start.sh、不生成 .env.eci |
| scripts/start.sh（修改） | 保留仓库中的显式初始化、配置文件选择、覆盖校验、Compose 启动与就绪检查；初始化使用更新后的 .env.example；假设所需镜像已在本地，删除所有拉取及构建分支，包括无 Go 时构建 Controller 的回退，不依赖 install.sh |
| .env.example（修改） | 更新 HOST_IMAGE 为实际发布的 GHCR 引用及中英文注释；删除旧本地构建标签的默认描述；注释分别说明安装器和仓库启动脚本各自的使用方式，空白凭证规则保持不变 |

仓库 start.sh --init-env 可以保留此前明确生成 .env 和 .env.eci 的能力；普通 install.sh 只创建 .env。这是两个入口各自的行为，不要求为了安装器改变内部初始化命令的用途。start.sh 中 ECI 初始化的镜像引用也必须同步修正，不能继续硬编码旧的 go-bot/host:eci。

共用的 compose.yaml 同步改成公共 image 引用并去掉 build，继续读取 .env 与 configs/default.yaml。源码目录执行 start.sh 与安装目录直接执行 Compose，最终使用同一份 Compose 和 Controller 配置检查规则。

## 4. Docker 的默认配置

安装器完整复制 .env.example，不另行生成精简版模板。其中 Docker 默认生效的主要配置是：

```dotenv
HARNESS_RUNTIME=docker
HOST_IMAGE=ghcr.io/xyc999-ai/go-bot-host:latest
HARNESS_STORAGE=container
```

落地时同时调整仓库 .env.example 中的 HOST_IMAGE 及中英文注释、Go 默认镜像和相关启动检查，使其引用同一已发布 GHCR Host 版本，不再默认使用 go-bot/host:local。现有 ECI 初始化中硬编码 go-bot/host:eci 的替换也要更新：公共缓存路径使用同版本多架构公共引用；buildeci 不再承担自定义构建和推送。

改为 GHCR 后必须保留 ghcr.io/ 前缀，例如 ghcr.io/xyc999-ai/go-bot-host:latest；省略 registry 会指向 Docker Hub。即使镜像已经拉到本地，配置仍使用该完整引用，安装器不再生成另一套本地别名。此前省略 docker.io/ 的约定只适用于 Docker Hub。

不采用旧方案中的 `GO_BOT_IMAGE`，继续使用已有 `HOST_IMAGE`。Controller 和 Host 的默认引用均使用 latest；安装器分别准备配置所选镜像，不新增必须填写的 Controller 镜像环境变量。

其余基础配置保留在下载的 configs/default.yaml 中，用户无需编辑就能启动。它仍由 Compose 挂载并通过 --config 读取，仓库启动和安装目录启动使用相同逻辑。Go 内置默认值继续保留，但不因此删除现有 YAML 文件依赖。

模型端点、模型密钥、网页搜索和插件授权继续在用户页面配置、按现有机制存储，均不写进默认部署 env，也不编进公共镜像。

Compose 只保留 Controller、Redis 两个服务，沿用现有 8080:8080、6379:6379、Redis 数据卷和网络行为。Controller 使用匹配的角色镜像和 serve 命令，Host 使用现有 Host 入口。

## 5. 安装、检查与 Compose 启动如何配合

Docker 安装流程：

```text
检查 Docker / Compose / Docker Engine 是否可用
  → 创建目标目录，下载仓库原始 Compose、.env.example、configs/default.yaml
  → 仅在 .env 缺失时复制模板初始化，不生成 .env.eci
  → 读取并校验配置
  → 拉取选定版本的 Controller、Host 和 Redis 镜像
  → 使用 Controller 镜像内工具完成配置检查
  → 输出部署目录及 docker compose up -d 启动命令
```

安装脚本不执行源码构建，镜像拉取失败时明确退出，不切换为本地编译。发布 Compose 不配置 build，并对 Controller、Redis 设置 pull_policy: never，因此用户直接运行 docker compose up -d 也只使用已拉取的镜像。Controller、Redis 和 Docker Host 镜像应已由安装器拉取到本地，缺失时报错并提示重新准备。

仓库内部 start.sh 保留 --env-file、可选调优文件及配置检查、本次进程的环境覆盖、启动和就绪检查；不运行 docker pull、docker build、compose build、up --build 或 go run。配置工具统一使用本地 Controller 镜像，不再按本机是否安装 Go 分两条路径，Compose 启动显式禁止构建和拉取。ECI 模式不检查本地 Host 镜像，但要求有效的缓存 ID；普通 start 不制作缓存。这些内部用法不进入 Docker Quickstart。

Host 不在 Compose 服务中，所以安装脚本必须单独准备 Host 镜像。Controller 创建 Host 时继续检查本地镜像并使用 `--pull=never`；缺失时提示重新运行安装器准备，避免每个账号启动都临时联网拉取。

Controller 自身启动时仍执行完整配置检查，因此即使跳过安装器直接运行 Compose，错误配置也不会被忽略。安装检查完成后，真正的服务健康状态由 Compose healthcheck / ready 检查确认，不把「镜像下载成功」称为「服务已经启动」。

Controller 的 Config.Load 按默认值 → 调优与 env 文件 → 显式进程部署环境变量的顺序合并配置，启动入口随后执行完整校验。覆盖值只保存在本次进程内存中，不保存回 .env，不改变其权限。删除旧的 prepare-env 工具及其构建、调用入口。

仓库和安装目录共用的 Compose 只将所选 env 文件只读绑定到 /config/deployment.env，将 configs/default.yaml 只读绑定到 /runtime.yaml。不绑定配置目录或源码目录。GO_BOT_ENV_FILE 可选择其他私有配置，默认 ./.env；GO_BOT_CONFIG_FILE 可选择调优文件。

共用 Compose 固定提供 REDIS_ADDR 和 CONTROLLER_CONTAINER，不将 env 文件整体重新注入进程环境。自定义 Compose override 或 docker run -e 可以提供显式部署覆盖。start.sh 将宿主进程中实际存在的已知部署变量同时用于校验和本次 Compose 启动；临时 override 只包含变量名、不包含凭证值，并在脚本结束时删除。install.sh 只初始化缺失文件并准备镜像，不保存 shell 覆盖。

进程环境变量覆盖 env 文件不等于修改文件。用户希望长期改变配置时自行编辑 .env，再明确重建 Controller；不新增配置监控或热更新。模型和搜索配置继续由账号设置管理，不接受部署环境覆盖。

唯一保留的 env 自动写回是用户明确执行 buildeci 成功制作缓存后保存 ECI_IMAGE_CACHE_ID。这是独立准备操作，不属于 Controller 启动流程，也不要求运行中的 Controller 能写 env。

## 6. 多架构发布

Controller 和 Host 的普通版本标签均提供 `linux/amd64` 与 `linux/arm64`。Docker 会按目标 Docker Engine 的架构自动选择，用户不必手动填写平台。[Docker 多平台说明](https://docs.docker.com/build/building/multi-platform/)

不提供 ECI 专用的 -amd64 tag。各架构构建结果按 digest 暂存，全部成功后汇总成同一版本的多架构清单。ECI 使用与 Docker 相同的镜像引用，由云端的默认 AMD64 环境选择匹配产物；本机架构不会影响云端拉取。

默认安装使用 latest，Controller 与 Host 保持一致；用户可自行指定明确版本。发布流程提供版本 tag 和 latest；Git 标签触发发布时更新 latest，手动补发旧版本可关闭 update_latest。latest 不自动比较语义版本号，不会更新已有 ECI 缓存或运行容器。

main 的安装入口启用前必须完成两种角色、两种架构的首次发布及 latest 公开拉取检查。后续所有架构构建成功后才汇总版本并更新 latest。安装脚本直接从 main 获取，无需上传到 Release；普通 PR 不向 GHCR 推送镜像。

### GHCR 发布权限与公开检查

GitHub Actions 使用自动生成的 GITHUB_TOKEN 发布，并给构建任务配置 contents: read、packages: write；无需再为发布镜像维护 Docker Hub 账号、Docker Hub 推送令牌或长期个人 PAT。安装脚本分发不依赖 Release 附件任务。[GitHub 官方发布流程](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)

两种镜像均关联现有 xyc999-ai/go-bot 代码仓库，并设置源码、版本、revision、MIT License 等 OCI 标签。

首次发布的容器包默认是 private，源码仓库 public 不代表容器包自动 public。首发时必须将两个包的可见性明确设为 public，再从没有登录 GHCR 的环境验证拉取成功；之后才向用户发布安装入口。[GHCR 可见性与关联规则](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)

## 7. ECI 使用公共镜像时需要什么

公共 Host 镜像可以直接用于 ECI 镜像缓存，不需要用户在本地构建或向自己的镜像仓库推送。

| 配置 | 公共镜像方案中的要求 |
| --- | --- |
| 阿里云 AccessKey ID + Secret | 需要，供查询缓存、制作缓存和管理 ECI |
| Region、vSwitch、安全组 | 需要正确的用户云资源配置，不是镜像仓库凭证 |
| ECI_IMAGE_CACHE_ID | 需要；制作缓存成功后自动写回 |
| HARNESS_STORAGE | 默认 container，可选择 nas |
| NAS_MOUNT_TARGET | 仅 nas 模式必填 |
| HOST_IMAGE | 唯一镜像地址参数，模板预填公共 Host 镜像；本地 Docker 和云端缓存共用此引用 |
| GHCR 拉取令牌 / 登录 | 公共镜像不需要 |
| GHCR 发布令牌 | 部署用户不需要，项目 Actions 使用 GITHUB_TOKEN |

不提供镜像仓库账号、密码或第二个推送地址参数。buildeci 只负责让阿里云从公共 HOST_IMAGE 制作缓存，不读取本地 Host 镜像、不构建、不推送。项目镜像的构建和发布完全交给独立 GitHub Actions；维护者的自动 GITHUB_TOKEN 不进入用户部署配置。

## 8. ECI 的用户操作流程

ECI 在中英文 README 中单独说明，不出现在普通 install.sh 的参数或 Quickstart 分支中。沿用明确初始化 ECI 配置 → 填写云资源 → 制作缓存 → 启动 Controller 的独立流程。源码目录操作示例：

```bash
./scripts/start.sh --init-env
# 在 .env.eci 中填入阿里云凭证、地域及已有网络资源，按需选择 NAS。
# 按该版本的说明准备 Controller 和 Redis 公共镜像。
./scripts/buildeci.sh cache --env-file .env.eci
./scripts/start.sh --env-file .env.eci
```

独立 ECI 流程使用现有 .env.eci 和 --env-file 选择机制；普通 Docker 安装器只从 .env.example 生成同级 .env。配置文件不存在时启动和缓存脚本都直接报错，不自动初始化。缺少必要值时明确提示具体字段，不能声称已经可以启动。

ECI 配置中的 HOST_IMAGE 默认是该发行版的公共多架构 Host 镜像。ECI 文档指导在 Controller 机器准备 Controller、Redis 镜像；云侧缓存制作过程拉取 AMD64 Host，不要求本机再下载一份大 Host 镜像。

独立 buildeci.sh cache 是显式请求创建云资源的步骤：

1. 检查缓存制作所需的阿里云凭证与网络字段；此阶段不要求已有缓存 ID。NAS 等 Host 运行配置由 Controller 启动时检查。
2. 通过 Controller 镜像内的 buildeci 工具执行缓存操作，不依赖用户本机 Go。
3. 缓存来源直接为 HOST_IMAGE，保留完整 ghcr.io 地址；将该引用交给云 API 匿名拉取，用于后续缓存核对，不请求仓库凭证。
4. 等待缓存 Ready，自动把 ECI_IMAGE_CACHE_ID 保存回 --env-file 指定文件，默认 .env.eci；沿用现有缓存操作记录目录。
5. 输出写回的缓存 ID；随后由用户启动 Controller，执行完整运行配置检查。

制作缓存时不会启动 Controller、Redis 或用户 Host。已有 Ready 缓存的部署者可以填写其 ID 后直接启动；普通安装、start.sh 和 Compose up 都不自动创建付费镜像缓存。buildeci.sh 使用已有 Controller 镜像中的 buildeci 二进制完成公共缓存流程，不再要求本机安装 Go。

ECI driver 创建实例时直接传入 HOST_IMAGE 与 ECI_IMAGE_CACHE_ID，不再从缓存详情查询镜像地址；用户修改镜像后重新制作缓存并更新 ID。保留 ImagePullPolicy=Never 和明确指定缓存，不自动退回其他镜像来源。显式 doctor 诊断命令仍可检查缓存状态。NAS / 非 NAS 挂载与数据语义不变。

buildeci 仅保留 cache 和离线 check，不提供本地构建、推送或私有镜像认证分支。用户自行更改源码或镜像变量不属于默认部署流程。

ECI 拉取公共镜像仍需要可达 GHCR 的云端网络。已有 NAT 网关时不新增强制 EIP 参数；若 GHCR 在目标网络不可达，可把同版本 AMD64 镜像复制到可达仓库后制作缓存，无需重新编译。[阿里云网络要求](https://www.alibabacloud.com/help/en/eci/user-guide/manage-an-image-cache)

项目镜像换到 GHCR 不代表所有拉取都离开 Docker Hub：Redis 仍用官方 redis:8-alpine，源码构建的基础镜像也仍可能来自 Docker Hub。

## 9. 后续重复运行与升级

重新运行安装器默认保留已有用户配置和数据。升级时明确更新共用 Compose、配置中的版本引用，并准备匹配的 Controller / Host 镜像；安装器本身不改写已有部署文件，不自动删除运行中的 Host 或清理数据卷。

Compose 重建 Controller 不会更新已经存在的独立 Host。Docker 的 stop/start 也不会切换原容器镜像；需要新版本 Host 时按既有显式删除运行实例、保留账号卷、重新创建的流程操作。ECI 则按新 Host 版本准备缓存，再切换 ID 并明确替换实例。

Redis 数据卷、每账号 Docker 卷和 NAS 路径保持现有命名。非 NAS ECI 中的数据依然按原本的临时存储语义处理，不额外承诺保留。

## 10. 实施范围与验收

- 保留两个 Dockerfile，在 Controller 镜像补齐 buildeci 和发布启动入口，不给 Host 加入 Controller 或 Redis。
- 新增从 main 直接下载的 Docker 专用 scripts/install.sh；直接下载仓库同一份 compose.yaml、.env.example、configs/default.yaml，只初始化同级 .env，不生成 .env.eci，不向安装目录分发 start.sh。仓库 Compose 统一去除 build，start.sh 另行去除构建和拉取，不要求本机 Go；保留原始 YAML 配置读取链路。
- 默认配置自动本地生成，带中英文注释；不包含仓库认证和额外推送地址字段。
- 适配缓存工具读取公共 Host 来源、配置写回和预缓存/正式启动两种检查时机。
- 更新现有 CI 的部署 smoke 与角色镜像输入，新增发版推送流程，不扩展无关严格测试，也不调用真实模型或云资源作为普通 CI。
- 更新 .env.example、Go 默认镜像、ECI 初始化引用及相应检查，避免仍指向旧本地标签。
- 独立验证两个入口：安装器从空目录下载、初始化并拉取成功；未运行安装器的源码目录也能在镜像已准备好时通过 start.sh 初始化和启动。用缺失镜像场景确认 start.sh 明确报错，不发生隐式拉取或构建。
- 更新中英文 README、GHCR 包说明；Quickstart 只讲 curl 安装 + docker compose up -d，ECI 独立说明配置、准备镜像、制作缓存和启动步骤。

验收应从没有源码、没有 Go / Node / Python 的目录开始：Docker 默认安装不填密钥即可打开 Web；AMD64/ARM64 均选择正确镜像；Host 可以动态创建；重新执行不会覆盖配置；ECI 不要求仓库凭证，缺阿里云凭证报错，缓存 Ready 后正确写回 ID。整个过程中不依赖当前开发电脑的镜像、缓存或私有文件。

## 11. 本地实施与验证结果

已完成独立安装器、共用 Compose、仓库启动脚本与模板改造；Controller 自行校验配置；环境覆盖只在内存中生效，env 和基础 YAML 均只读挂载，不暴露源码目录。ECI cache 只读取 HOST_IMAGE，不拼接架构后缀；工具已纳入 Controller 镜像，旧的本地构建、推送、仓库认证参数及实现已移除。

本地通过 make test vet check、ARM64 两个完整镜像构建及 Host smoke、安装器和独立 start 的真实容器启动、双账号 Host / Redis 生命周期、Web Docker 集成、AMD64 Go 入口交叉编译，以及 actionlint / Compose 静态检查。安装器测试通过本地 HTTP 文件和本地镜像模拟分发端，不冒充已发布 GHCR 的匿名下载验收。

发布工作流运行后，需将两个 GHCR 包设置为 Public 并完成匿名拉取检查。版本和发布操作以[镜像发布说明](deployment.md#publishing-project-images)为准。
