# ECI 镜像缓存与启动配置方案

> 历史设计与调研记录。当前行为、配置和命令以[文档索引](README.md)中的指南为准。

2026-11-11 · 实施方案

本文记录当时的存储和迁移方案。当前 Docker 自动挂载账号专属卷，ECI 支持容器层和 NAS。 [当前部署说明](deployment.md#storage-choices)

## 组件分工

**`buildeci` 交付处于 Ready 状态的镜像缓存 ID，ECI driver 用该 ID 启动实例，`start.sh` 管理本地 `.env`。** 构建与运行共用一份私有环境文件，各命令只校验自己使用的字段。

改造包括缓存的制作与交接、ECI 的启动前提，以及环境文件的写入归属。
相关实现见 [ECI driver](../providers/runtime/eci/driver.go)、[启动脚本](../scripts/start.sh)
和 [controller 入口](../cmd/controller/main.go)。

```text
build/Dockerfile.host → 发布的 Host 镜像 → buildeci → Ready 缓存 ID
                                                      │
                                                 显式传入脚本
                                                      ↓
.env.example + 进程环境变量 ────────────────────────→ start.sh
                                                      │ 写入
                                                      ↓
                                                  私有 .env
                                                      │ 只读
                                                      ↓
                                                  controller
                                      ├─ Docker driver → 本机镜像
                                      └─ ECI driver    → 指定缓存 → ECI Host
```

| 入口 | 负责的工作 | 产物 |
|---|---|---|
| `cmd/buildeci` | 构建及发布 Host 镜像，或接受已发布镜像。调用阿里云 API 创建缓存并等待就绪 | `ECI_IMAGE_CACHE_ID` 与不含凭证的构建记录 |
| `scripts/start.sh` | 初始化环境文件，在内存中应用显式覆盖并校验，然后启动 controller | 私有 `.env` |
| `cmd/controller` | 读取配置，装配所选 driver，管理 Host 和 HTTP/SSE 入口 | 运行状态与就绪的 Host |
| `providers/runtime/eci` | 校验指定缓存，从缓存详情解析镜像地址，用该缓存创建或接回 ECI 实例 | 私网 Host 地址 |

## `HOST_IMAGE` 和 `ECI_IMAGE_CACHE_ID`

- `HOST_IMAGE` 是本地镜像标签。 `REGISTRY_PUSH_IMAGE` 是发布与缓存用的仓库引用，例如 `registry.example.com/team/go-harness:v0.1.0`。
- `ECI_IMAGE_CACHE_ID` 是阿里云生成的 `imc-...`。它是 ECI 运行侧选择 Host 版本的唯一配置项。Docker 模式只用 `HOST_IMAGE` 选镜像。
- 阿里云的 `CreateContainerGroup` 请求仍要求提供容器 `Image`。driver 用缓存 ID 查询 `DescribeImageCaches`，从返回的 `Images` 取出该缓存唯一的镜像引用，同时将缓存 ID 作为 `ImageSnapshotId` 传给云 API。ECI 启动时无需另填一份镜像地址。 [DescribeImageCaches](https://www.alibabacloud.com/help/en/eci/developer-reference/api-eci-2018-08-08-describeimagecaches) · [CreateContainerGroup](https://www.alibabacloud.com/help/en/eci/developer-reference/api-eci-2018-08-08-createcontainergroup)

首版每个缓存只包含一个 Host 镜像。缺少 ID、查询不到、状态未就绪、已过期、区域不匹配或存在多个镜像时，driver 在创建实例前给出明确错误。创建请求显式指定缓存，关闭自动匹配，并使用 `ImagePullPolicy=Never`。缓存失效时启动失败，由用户重新制作或选择缓存。真实环境需验证指定缓存与 `Never` 同时使用时的启动结果和事件。 [镜像缓存使用方式](https://www.alibabacloud.com/help/en/eci/user-guide/use-the-image-cache-feature-to-accelerate-the-creation-of-an-elastic-container-instance) · [镜像拉取策略](https://www.alibabacloud.com/help/en/eci/developer-reference/api-eci-2018-08-08-createcontainergroup)

升级用新镜像版本制作新缓存，然后切换 ID。ECI 的运行配置指纹以缓存 ID 为准，编辑打包输入 `REGISTRY_PUSH_IMAGE` 不会更新已选缓存或替换运行中的 Host。需要替换旧 Host 时沿用显式重建流程。已有实例的状态查询、停止和删除可以使用保存的实例引用，即使旧缓存已经过期。

## `buildeci` 怎样制作缓存

1. `buildeci prepare --env-file .env.eci` 读取本地标签 `HOST_IMAGE`、发布地址 `REGISTRY_PUSH_IMAGE`、阿里云与仓库配置，使用现有 Host Dockerfile 构建镜像并发布到指定仓库。首版按 ECI 默认的 Linux AMD64 构建，避免在 Apple Silicon 上沿用本机架构。发布使用显式版本标签并记录 digest。 [Docker Buildx](https://docs.docker.com/reference/cli/docker/buildx/build/) · [ECI 架构配置](https://www.alibabacloud.com/help/en/eci/user-guide/create-an-instance-by-specifying-the-arm-type)
2. `buildeci cache --env-file .env.eci` 使用 `REGISTRY_PUSH_IMAGE` 指定的已发布镜像，直接进入同一套缓存制作流程。公开镜像、CI 发布镜像和有独立发布网络的私有仓库都可以走这条路径。
3. 调用 `CreateImageCache`，传入 ECI 区域、vSwitch、安全组、单个镜像引用和所需的私有仓库拉取凭证。缓存名称、大小、保留时间和等待超时由 `buildeci` 自己的配置与默认值管理。首版建议采用云侧默认容量与保留策略，等待超时默认 30 分钟。创建会产生云资源与存储费用，因此由上述显式命令发起。 [CreateImageCache](https://www.alibabacloud.com/help/en/eci/developer-reference/api-eci-2018-08-08-createimagecache) · [缓存计费](https://www.alibabacloud.com/help/en/eci/product-overview/image-caches)
4. 通过 `DescribeImageCaches` 等待 `Ready`，核对返回的 ID、区域和镜像。创建前保存操作记录与 `ClientToken`，拿到 ID 后保存它。超时或断线后使用原 ID 查询，或以同一令牌对账，避免重试生成重复缓存。失败时提供可用于排查的 ID 或请求信息，成功输出只在缓存就绪后产生。 [DescribeImageCaches](https://www.alibabacloud.com/help/en/eci/developer-reference/api-eci-2018-08-08-describeimagecaches)
5. 输出 `ECI_IMAGE_CACHE_ID`。构建记录另外保存镜像引用、可取得的发布 digest、架构、区域和缓存 ID，便于排查和回退。构建日志与结构化输出分开。用户可以手动填写 ID，也可以把成功输出交给启动脚本保存。

发布端使用已授权的 Docker 仓库登录态。共享 `.env` 里的 `REGISTRY_USERNAME`、`REGISTRY_PASSWORD` 用于阿里云制作缓存时读取私有镜像，两项默认空，私有仓库须成对填写，仓库地址由 `REGISTRY_PUSH_IMAGE` 提取。这样同一份配置可以保存权限较小的拉取凭证，发布权限留在本地 Docker 或 CI。镜像层、构建参数及构建记录排除模型、仓库和阿里云凭证。 [私有镜像缓存配置](https://www.alibabacloud.com/help/en/eci/user-guide/manage-an-image-cache)

## 一份环境文件，按命令校验

| 字段 | 使用者 | 要求 |
|---|---|---|
| `HARNESS_RUNTIME` | controller、启动脚本 | 模板显式填 `docker`，ECI 部署改为 `eci` |
| `HOST_IMAGE` | Docker driver、`buildeci` | 本地镜像标签，Docker 与 ECI 默认分别为 `go-harness/host:local` 和 `go-harness/host:eci` |
| `REGISTRY_PUSH_IMAGE` | `buildeci` | ECI 可达的版本化镜像引用 |
| `ECI_IMAGE_CACHE_ID` | ECI driver | 模板留空。ECI 启动必填，由 `buildeci` 或已就绪的外部缓存提供 |
| `ECI_REGION`、`ECI_VSWITCH_ID`、`ECI_SECURITY_GROUP_ID` | `buildeci`、ECI driver | 共享云网络参数。制作缓存的临时实例也需要访问镜像仓库 |
| `ALIBABA_CLOUD_ACCESS_KEY_ID`、`ALIBABA_CLOUD_ACCESS_KEY_SECRET` | `buildeci`、ECI driver | 共用长期凭证。制作缓存需要创建与查询权限，运行阶段只查询缓存及管理实例 |
| `REGISTRY_USERNAME`、`REGISTRY_PASSWORD` | `buildeci` | 私有镜像制作缓存时使用。真实验收需确认私有缓存配合 `Never` 启动无需再次传递这组凭证 |
| `MODEL_API_KEY`、`MODEL_URL`、`MODEL_NAME` | controller、Host | 传给 Host 的三个模型字段。URL 和模型名留空时采用 DeepSeek 预设 |
| `WEB_SEARCH_PROVIDER`、`WEB_SEARCH_API_KEY` | controller、Host | 两项必填。模板留空，官方 DeepSeek 模型地址可由启动脚本补齐供应商和搜索密钥 |
| `HARNESS_STORAGE` | controller、runtime driver | ECI 选择 `container` 或 `nas`，默认 `container` |
| `NAS_MOUNT_TARGET` | ECI driver | 使用 NAS 时设置一次 NAS 服务地址。私有部署配置可预填 |

打包配置的类型和校验放在 `cmd/buildeci/config.go`。容量、保留时间等低频选项可用独立的 `configs/buildeci.yaml`，云账号和仓库凭证仍放共享 `.env`。打包可以在模型配置及缓存 ID 尚未填写时运行。

## 启动脚本与直接调用

1. 先显式运行 `scripts/start.sh --init-env`，从 `.env.example` 生成 `.env` 和 `.env.eci`，权限为 `0600`。启动时用 `--env-file` 选择已有配置，默认读取 `.env`。
2. 正常启动时，脚本通过 Controller 镜像内的 check 命令校验配置。文件按字面解析，只在内存中合并显式进程部署变量；无论校验成功或失败都不写回 env。Controller 启动入口独立执行相同校验。
3. 脚本启动 `controller serve --env-file <path>`。controller 严格读取该文件，缺文件或缺当前模式所需字段即退出。删除现有的 `--save-env` 和 runtime 的自动写回路径。直接运行 controller 时，应用配置以文件值为准。controller 的实例状态和生成的应用 token 仍通过各自的状态文件持久化。
4. Docker 脚本可先准备默认本地镜像。ECI 分支直接使用配置好的缓存 ID，已经安装本机二进制时只需运行 controller。受管 Host 继续接收 controller 显式注入的 Host 参数，独立 Host 由调用方提供配置或容器环境。

从仓库根目录执行以下命令。

```bash
./scripts/start.sh --init-env
# 在 .env.eci 填写 HARNESS_RUNTIME=eci、要发布的 REGISTRY_PUSH_IMAGE 和相关凭证。
cache_id="$(./bin/buildeci prepare --env-file .env.eci --output cache-id)" &&
  ECI_IMAGE_CACHE_ID="$cache_id" ./scripts/start.sh --env-file .env.eci
```

已有 Ready 缓存时，可直接填入 `ECI_IMAGE_CACHE_ID` 并启动。此时 `HOST_IMAGE` 可以留空，driver 会从缓存详情取镜像引用。

## Host 最小启动参数

controller 从私有 `.env` 读取部署配置，按允许的字段生成 Host 环境变量。Docker 和 ECI driver 传递同一份键值表，Host 用同一套解析与默认值。删除 `HOST_CONFIG_JSON`，模型地址直接使用 `MODEL_URL`。阿里云凭证、私有仓库凭证、缓存 ID 和挂载地址由打包命令或 driver 消费。

**受管 Host 接收三项模型设置、两项搜索设置，以及自动管理的运行 ID 和认证 token。** 默认 DeepSeek 的模型地址和名称由预设补齐。Host 固定走 Responses 协议。

| Host 环境变量 | 来源 | 默认或填写时机 |
|---|---|---|
| `HOST_RUN_ID` | controller 生成 | 标识本次受管实例，新建实例换 ID |
| `HOST_API_TOKEN` | controller 自动生成并保存在私有状态文件中 | Host 接口认证。用户模板省掉此字段，独立 Host 自行生成 |
| `MODEL_API_KEY` | 私有 `.env` | 模型服务的 Bearer key |
| `MODEL_URL`、`MODEL_NAME` | 私有 `.env` | 默认使用 DeepSeek 预设。选择自定义服务时填写 |
| `WEB_SEARCH_PROVIDER`、`WEB_SEARCH_API_KEY` | 私有 `.env` | 搜索 provider 与搜索密钥，两项必填。当前支持 `deepseek` |
| `HOST_DATA_MOUNTED` | controller 根据 `HARNESS_STORAGE` 派生 | 默认容器内存储。使用 volume、bind 或 NAS 时传 `true`，Host 验证数据目录已经挂载 |

默认模型地址和名称仍是 `https://api.deepseek.com` 与 `deepseek-flash`。controller 和 Host 用同一套预设解析模型。模型协议、认证方式和 provider 由代码确定。

上下文窗口、网页搜索和空闲回收统一由代码给出默认行为，用户配置 schema 省去 `MODEL_CONTEXT_WINDOW_TOKENS`、`WEB_SEARCH_ENABLED`、`HOST_IDLE_TIMEOUT` 及对应的 YAML 项。controller 的 Host 环境变量清单也省去这三项。

| 内部设置 | 默认行为 |
|---|---|
| 模型上下文窗口 | 官方 DeepSeek 与自定义 Responses 服务统一使用 1,000,000 tokens |
| 网页搜索 | 始终启用。controller 在创建 Host 前检查 provider 是否受支持、搜索密钥是否已填写，然后将两项逐个传给 Host |
| Host 空闲退出 | 由用户通过 Controller 停止。关闭页面后可继续运行任务 |

Host 内部端口固定为 8080，数据根目录固定为 `/root/harness`。外部地址和实际挂载交给 runtime driver。

### ECI 存储与默认挂载路径

| ECI 模式 | `/root/harness` 的来源 | 重建 ECI 后 |
|---|---|---|
| `HARNESS_STORAGE=container` | 容器内可写层 | 按新的容器数据目录启动 |
| `HARNESS_STORAGE=nas` | 挂载 NAS 子目录 | 重挂同一目录并恢复会话 |

`volume` 是本地 Docker 管理的命名卷，`bind` 是把本机已有目录映射进容器。两者保留为本地 Docker 的可选持久化方式。

LifeClaw 的 ECI driver 将 NAS 的 `/lifeclawapp/serveragents/users/<userID>` 挂到容器内 `/root/harness`。Go Harness 沿用固定容器路径和自动派生远端子目录的结构。新部署的 NAS 路径定为 `/go-harness/<deployment-id>`，用稳定的部署 ID 跨实例重建复用。同一部署的旧路径按迁移记录继续挂载。阿里云 FlexVolume 支持在挂载前创建缺失的子目录。 [阿里云 NAS 挂载参数](https://www.alibabacloud.com/help/en/eci/user-guide/mount-a-nas-volume)

NAS 服务地址取决于阿里云账号与 VPC。建议在确认 ECI 网络和 NAS 权限组匹配后，在本机私有 `.env.eci` 预填 LifeClaw 现有的挂载点域名。开源模板将 `NAS_MOUNT_TARGET` 留空，部署者启用 NAS 时填自己的地址。地址配置完成后，日常只切换 `container` 或 `nas`，两个目录路径都由代码管理。 [阿里云 NAS 网络要求](https://www.alibabacloud.com/help/en/eci/user-guide/mount-a-nas-volume)

### 账号 ID 和运行实例 ID

- 账号 ID 用于派生 NAS 路径和 Docker 卷名。Controller 将它传给 Host，重建时沿用同一账号的存储。
- `RunID` 对应 Redis 中的 `hostId`，重新接入或重启原 Docker 容器时沿用，新建容器或 ECI 时由 Controller 生成。Host 的 `/readyz` 返回 `runId` 和 `apiVersion`。

## 扩展结构

```text
cmd/
  controller/                 # 运行入口，按模式装配 driver
  host/                       # 云电脑进程
  buildeci/                   # ECI 打包入口与自身配置
internal/
  config/                     # 只读解析和按用途校验
  imagebuild/                 # 可复用的镜像构建、发布及产物信息
providers/runtime/eci/        # ECI 实例生命周期与缓存读取
thirdparty/aliyun/            # ImageCache 与 ContainerGroup SDK 封装
build/Dockerfile.host         # Docker 与云端共用的 Host 镜像
```

新增云服务时，在 `providers/runtime/<provider>` 实现该平台的实例生命周期，并在 `cmd/controller` 装配新 driver。平台需要提前制作运行产物时，可以另增 `cmd/build<provider>`，复用镜像发布工具和环境文件解析。平台专用配置由该 driver 校验。阿里云缓存 ID 只出现在 ECI 配置中。

## 实施顺序与验收

1. 先把文件初始化与覆盖写入从 controller 移到启动脚本侧。测试直接调用时缺文件报错、覆盖只由脚本写入、Docker 正常启动。
2. 增加 `buildeci` 和 ImageCache API 封装。测试已发布镜像路径、缓存 Ready、失败、超时及幂等恢复，核对输出与构建上下文均排除凭证。
3. 将 ECI 启动收紧为必填缓存。用 fake API 证明空 ID、未就绪、错误区域和多镜像缓存都不会发起创建请求。用同一个 Ready 缓存在阿里云实际启动，核对缓存命中事件、`Never` 行为、私有缓存和可选 NAS。
4. 验证 Docker 和 ECI 使用同一套独立环境变量启动 Host，覆盖默认 DeepSeek、自定义 Responses 服务、缺搜索凭证时拒绝启动、自动 Host token、密钥更新和持久存储恢复。ECI driver 在发送请求前逐项校验环境变量值不超过 256 字节，超限时只报字段名。在真实 ECI 上核对 Responses 调用和 NAS 默认路径的首次挂载、重建恢复。 [CreateContainerGroup 环境变量参数](https://www.alibabacloud.com/help/en/eci/developer-reference/api-eci-2018-08-08-createcontainergroup)
