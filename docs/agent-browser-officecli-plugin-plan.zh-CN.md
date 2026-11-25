# agent-browser 与 OfficeCLI 官方能力插件化方案

> 分发更新：业务源码、资源与发布已迁至独立的 [got-bot-plugins](https://github.com/xyc999-ai/got-bot-plugins)；本文原镜像内 bundled 分发章节由 [远程市场方案](remote-plugin-market-plan.zh-CN.md) 替代，授权与业务行为保持不变。

日期：2026-11-20。状态：已实现并完成本地验收；2026-11-20 按上游核对结果使用 pnpm 标准安装流程，依赖准备不放在 Cordis apply。已确定采用官方默认全局 CLI 安装；按最新决定保留当前 `$DSH_HOME/plugins/profile/node_modules` 的插件包目录。

## 1. 结论与范围

将两者分别包装为本项目的正式 Cordis 插件，接入现有插件市场。采用 **官方 CLI + 官方 Skills**，由 Agent 继续通过 Go 内置 Shell 使用；不自行重写浏览器动作、Office 文档操作或 Skill 正文。

插件负责按官方方式安装软件、可用性检查与 Skill 贡献；Cordis 负责插件生命周期。Go 继续负责 Shell 执行、权限、沙箱、Jobs、会话以及图片读取和文件交付。

用户管理模型收敛为“每个账号对每个插件包一份安装选择和启停状态”。Redis 只保存这类用户状态及所选版本；包自身的配置定义、默认值和准备逻辑随包提供，认证数据使用 Go credentials。不再向用户提供一个包创建多份运行实例的模型。

本方案不处理 ECI/NAS 下安装文件丢失的保存、备份和自动恢复。也不同时接入第二套 MCP 工具集，不引入插件自己的 AgentLoop、模型配置或会话管理。

## 2. 官方已经提供什么

### 2.1 agent-browser

已核对官方 GitHub、安装文档及目前镜像使用的固定提交 `1cc19526f3641c8543d55aa6700e7c55b37ac356`：

| 官方入口 | 作用 | 本项目采用方式 |
| --- | --- | --- |
| `npm install -g agent-browser` | 安装 CLI，由官方包选择平台二进制 | 直接使用官方 npm 全局安装，固定版本，不传自定义 prefix |
| `agent-browser install` | 安装默认浏览器 | 复用 Host 已有 `/usr/bin/chromium`，按官方 custom-browser 方式传入路径，无需再下载一套浏览器 |
| `npx skills add vercel-labs/agent-browser` | 安装发现 Skill | 使用官方安装器取得原始 `skills/agent-browser`，纳入插件发布包 |
| `agent-browser skills get core` | 读取与 CLI 匹配的操作指南 | 保留原流程，由模型按官方 Skill 指示读取 |
| `agent-browser mcp` | 提供 stdio MCP 入口 | 已确认存在，但本次不选用，避免与 CLI 重复提供浏览器操作 |
| `.claude-plugin/marketplace.json` | Claude Code 的 Skill 市场声明 | 参考其原始 Skill 来源；不是可直接加载的 Cordis 模块 |

重要区别：官方发现 Skill 很薄，真正的工作流、命令参考和专项知识由 CLI 随版本提供。我们不把整个命令手册改写进系统提示，也不把所有专项内容一股脑放入 Harness 的 Skill 清单。

来源：[官方 GitHub](https://github.com/vercel-labs/agent-browser)、[安装与已有浏览器](https://agent-browser.dev/installation)、[Skills 机制](https://agent-browser.dev/skills)、[官方 Claude 市场声明](https://github.com/vercel-labs/agent-browser/blob/1cc19526f3641c8543d55aa6700e7c55b37ac356/.claude-plugin/marketplace.json)。

### 2.2 OfficeCLI

已核对官方 GitHub、固定版本 `v1.0.160` 的源码，以及 npm registry 中实际发布的 `@officecli/officecli@1.0.160`：

| 官方入口 | 作用 | 本项目采用方式 |
| --- | --- | --- |
| `npm install -g @officecli/officecli` | 安装官方启动器并取得平台二进制 | 直接使用官方 npm 全局安装，复用官方启动器与下载逻辑，不传自定义 prefix |
| GitHub Release / 官方安装脚本 | 直接安装二进制 | 作为官方来源依据，不维护第二套下载实现 |
| `officecli skills codex` | 导出总入口 Skill | 在插件制作阶段使用隔离 HOME 导出 |
| `officecli skills install <name> codex` | 导出某个专用 Skill 及配套资源 | 保留多个 Skill 目录与完整资源，不合并成一个自写 Skill |
| `officecli load_skill <name>` | 从 CLI 读取内置指南 | 保留官方命令，不新增同义 Harness 工具 |
| 内置 MCP、Node/Python SDK | 其他程序化接入方式 | 已确认存在，本次采用 CLI 路线即可，不同时注册重复工具 |

官方源码树内的 `npm/package.json` 版本与发布版本可能不同：核对安装对象应以实际发布的 npm 包及其二进制版本为准，不能仅从 Git tag 下的 manifest 推断。本次已读取实际 `1.0.160` npm 包，并通过两种 Linux 架构的安装执行核对版本与二进制校验值。

另外，OfficeCLI 仓库 `plugins/plugin-protocol.md` 描述的是 OfficeCLI 自身的文档格式扩展协议，并非我们的 Cordis 插件格式，不能把该目录当成现成的 go-bot 插件。

来源：[官方用法](https://github.com/iOfficeAI/OfficeCLI/tree/v1.0.160)、[官方 npm 说明](https://github.com/iOfficeAI/OfficeCLI/blob/v1.0.160/npm/README.md)、[启动器](https://github.com/iOfficeAI/OfficeCLI/blob/v1.0.160/npm/officecli.js)、[Skill 安装器](https://github.com/iOfficeAI/OfficeCLI/blob/v1.0.160/src/officecli/Core/SkillInstaller.cs)、[OfficeCLI 格式插件协议](https://github.com/iOfficeAI/OfficeCLI/blob/v1.0.160/plugins/plugin-protocol.md)。

### 2.3 为什么仍需要我们包装

官方 npm 包提供可执行程序、安装逻辑或客户端约定，不提供本项目的 `goBot.api` 与 Cordis `apply(ctx)`。我们的插件只做这一层装配：准备官方程序、提供原始 Skills、核实全局命令能在 Host 执行环境中使用，随后按原生 Cordis 生命周期撤销 Skill 等贡献。

## 3. 当前代码的真实缺口

| 当前位置 | 当前行为 | 插件化调整 |
| --- | --- | --- |
| `build/Dockerfile.host` | 构建时安装两套 CLI，导出并复制 Skills 到 seed | 移出两套专用安装和 seed 步骤，交给插件准备与发布流程 |
| `harness/skills/seed.go` | 首次启动将 seed 复制到用户 Skill 根，并使用初始化标记 | 两套官方插件不再复制到用户目录；若已无其他 seed 使用者，删除这套专用启动依赖与旧标记分支 |
| `extensions/sdk/index.d.ts` | 已有 Skill 目录注册 | 直接复用；不增加 commands.register 或命令执行 RPC |
| `internal/plugins/manager.go` 及运行目录参数 | 包管理项目使用 `.dsh/plugins/profile` | 保留当前根目录及标准 node_modules，不因这两个业务插件重做包存储 |
| `plugin.Settings`、Controller/Redis 与两端管理 DTO | packages 与 instances 分开，instances.config 随用户设置写入 Redis | 收敛为按插件包记录用户安装选择、所选版本和 enabled；移除公开多实例管理与 Redis 中的任意业务 config，保留 Cordis 内部必要的加载条目 |
| `internal/executor/browser.go` | 固定 `/usr/local/bin/agent-browser`；管理会话 socket 与清理 | 改为通过 PATH 查找官方命令，保留 Go 资源隔离与清理 |
| `internal/executor/builder.go` | Host 启动时缓存浏览器 CLI 是否存在 | 移除该启动时判断，保证已经打开的会话也能使用后来启用的插件 |
| `extensions/catalog/plugins.json` | 正式清单为空 | 加入两个真正发布的插件；不是加入开发目录或未发布地址 |

## 4. 包与安装方式

建议源代码位置：

```text
extensions/
  plugins/
    agent-browser/
      package.json
      src/index.mjs           # Cordis 装配
      src/prepare.mjs         # 本地就绪检查
      scripts/install.mjs    # npm/pnpm 标准 postinstall：准备官方运行包
      upstream.lock.json     # agent-browser 的来源信息；OfficeCLI 不设额外版本/校验锁
      scripts/prepare-skills.mjs
      skills/                # 发布时由官方安装器生成，正文原样
      THIRD_PARTY_NOTICES.md
    officecli/
      ...                    # 相同职责划分，不要求建立空文件
```

插件包名建议为 `@go-bot/plugin-agent-browser`、`@go-bot/plugin-officecli`，各有自己的插件版本；插件版本与上游 CLI 版本分别记录。

安装包来源继续走现有受审清单。本次两份第一方包装包随 Host 镜像分发，清单使用受控 bundled 来源，从镜像内安装到 profile/node_modules。它们是随产品构建的正式包，不是用户指定的开发目录，也不依赖尚未发布的 npm 包或占位 release URL。后续独立发布可改为固定 HTTPS tarball。

用户操作为：**插件市场 → 安装 → 启用**。安装同时取得包装包、安装官方 CLI 并验证依赖，全部成功才返回安装完成；启用只验证并加载已安装的能力。正常使用无需填写 API Key 或 OAuth，也不显示授权入口。

建议取消这两项能力在基础镜像中的强制预装，改成市场中的推荐插件。首次安装后仍沿用现有“默认停用”的规则，不额外引入启动时自动安装或后台重试。

### 4.1 直接采用官方全局安装

在 Host 容器内，由插件自己的标准安装脚本决定 npm 安装命令。OfficeCLI 直接采用官方无版本号安装：

```sh
npm install -g agent-browser@0.43.0
npm install -g @officecli/officecli
```

不传 `--prefix`，不创建插件私有运行目录，不自行生成命令软链接或 launcher。npm 按自己的全局配置选择安装位置并创建命令入口。原生 Linux 下，全局命令位于 npm `{prefix}/bin`；具体 prefix 由 npm 环境决定，不在插件中写死 `/usr/local` 或 `/root/.local`。

OfficeCLI 官方 shell 安装脚本也是自行决定位置：已有命令则使用其原目录，否则使用 `$HOME/.local/bin`，不要求调用方提供目录。该脚本还会尝试配置 Shell 启动文件；但修改 `.bashrc` 不会改变已经运行的 Go Host 环境。本方案统一选官方 npm 安装，避免两条安装流程叠加。

插件安装和启用：

1. 安装、更新或重装 OfficeCLI 包时，标准 postinstall 直接执行 `npm install -g @officecli/officecli`，等待安装完成。版本由 npm 官方发布及插件自己的安装逻辑决定，Host 不附加 CLI 版本策略。
2. 启用只检查命令可以执行 `--version`、必需资源可用，并注册原始 Skills；不在 apply 内下载软件。不因 OfficeCLI 自身版本变化拒绝启用。
3. npm 沿用默认全局命令目录，Host 无需为 OfficeCLI 设置额外 PATH。基础镜像中的 Python 虚拟环境 PATH 与 OfficeCLI 无关。
4. 不增加 OfficeCLI 二进制 SHA256/版本锁，不设置 OFFICECLI_SKIP_UPDATE，不干预官方自身更新行为。官方 npm 安装器原本提供的下载与完整性校验保持原样。
5. 插件包版本与 CLI 版本相互独立。市场的“可更新”只对比包装插件的受审版本与实际已安装包版本；停用、启用不执行 npm 安装。

agent-browser 目前仍由它自己的安装脚本选择版本与官方 core 指南，不由 Go Host 硬编码 CLI 版本。

官方依据：[agent-browser 全局安装](https://agent-browser.dev/installation)、[OfficeCLI npm 用法](https://github.com/iOfficeAI/OfficeCLI/blob/v1.0.160/npm/README.md)、[OfficeCLI 官方 shell 安装脚本](https://github.com/iOfficeAI/OfficeCLI/blob/main/install.sh)、[npm 全局安装与命令目录](https://docs.npmjs.com/cli/v11/configuring-npm/folders/)。

### 4.2 统一安装框架与插件自有安装逻辑（最新决定）

**Host 统一编排安装，每个包声明自己的可选安装逻辑。安装不是 Cordis apply 的职责。**

职责分为三层：

| 层 | 职责 |
| --- | --- |
| Controller | 只接受受审 pluginId，确定固定版本和来源，保存用户安装选择，等待 Host 返回成功/失败；不执行 npm 或插件安装代码 |
| Go Host 包管理器 | 串行管理安装/更新/重装/卸载；检查包身份，调用统一 Node 包安装器；把最终结果与错误返回给 Controller |
| 插件包 | 声明自己需要的安装入口，完成该插件特有的 CLI/系统依赖准备与校验；普通纯 JS 插件无需额外安装入口 |

#### 4.2.1 直接使用 pnpm 标准安装机制

插件 package.json 声明普通 dependencies，并可提供标准 lifecycle scripts：

```json
{
  "name": "@go-bot/plugin-officecli",
  "version": "0.1.0",
  "type": "module",
  "exports": "./src/index.mjs",
  "scripts": { "postinstall": "node ./scripts/install.mjs" },
  "goBot": { "api": "1" }
}
```

具体准备代码在插件自己的 scripts/install.mjs 中，pnpm 在统一安装步骤执行。删除本轮中间实现的 goBot.install 字段、专用安装模块 RPC 和手动 import 调用；不另造一套安装生命周期。

Host 的统一包管理入口负责调用并等待 pnpm；执行权限使用原生 pnpm-workspace.yaml 的 allowBuilds。受审清单内部声明允许的包/依赖脚本，转换成精确 pnpm resolution；普通用户不能自行提交脚本放行规则。未获准的必需构建脚本使安装失败，不能全局允许所有脚本。纯 JS/API 插件无需额外脚本。

浏览、检查包必须禁用脚本；正式安装按受审规则执行脚本，pnpmfile 仍禁用。禁用 sideEffectsCache，防止包缓存命中跳过修改全局安装目录的 CLI 安装步骤。安装脚本先检查，满足版本即复用；失败抛错，不后台补装，不在安装中等待凭证交互。该机制是安装期包管理，与 Agent 工具审批无关。

#### 4.2.2 完整安装流程

1. 前端选择清单 pluginId；Controller 确认身份、版本和来源，要求 Host 已运行。
2. 仅检查包装包元数据，不运行其 apply 或安装脚本。
3. Redis 保存每个包一份 `{catalogId, version, enabled:false}` 用户选择。它表示用户意图，不等于已安装成功。
4. Host 统一安装包装包及 package.json 依赖到 profile/node_modules，由 pnpm 原生 allowBuilds 限定可执行的安装脚本。
5. pnpm 执行包中获准的 postinstall 等标准安装脚本，并等待完成；Host 核对实际包身份和版本。
6. OfficeCLI postinstall 执行无版本号的官方全局 npm 安装并确认命令可用；agent-browser postinstall 执行自身声明的官方全局 npm 安装及 core 指南检查。完整原始 Skills 随各包装包分发。
7. 包和运行依赖均检查成功才响应安装成功。安装期间前端显示进行中，失败显示错误并提供“重新安装插件”。不把“包文件存在”当作依赖安装成功。

#### 4.2.3 更新、重试和卸载

- **启用**：检查已经安装的 CLI 和资源，Cordis 执行 apply 注册 Skills；不下载、不安装依赖。缺依赖报错，要求重新安装。正常启用只包含本地检查和注册。
- **停用**：Cordis 撤销该包贡献；不卸载 CLI，不运行安装代码。
- **更新**：用户明确选择清单新版本；先撤销旧贡献，安装新包装包，再执行它的 install 并校验；原本启用的插件在成功后重新加载。失败明确返回，不伪称已回滚外部安装副作用。
- **重新安装**：显式重跑当前受审包的安装与依赖检查，修复失败或缺依赖；不新增后台重试任务。
- **卸载**：先撤销 Cordis 贡献，再删除包装包依赖；保留用户工作区、凭证和全局 CLI。软件依赖不等同于插件贡献，避免隐式删除其他流程仍在使用的命令。框架不增加自动删除系统依赖的通用逻辑。
- **Host 重启**：Controller 传入保存的选择；Host 恢复包和加载已启用插件。已有包不会因 apply 或停用而反复安装；本方案不处理 ECI/NAS 下 CLI 安装文件丢失的自动恢复。

普通 JavaScript 依赖由包管理器统一处理；这两个 CLI 插件用自身标准 postinstall；远程 API/MCP 包装插件通常没有系统依赖，不必声明安装脚本。Cordis 原生负责加载、依赖注入和资源生命周期，不接管上述包安装过程。

### 4.3 配置、凭证与 Redis 的明确边界

| 数据 | 负责方与保存位置 |
| --- | --- |
| 支持哪些插件、正式版本和安装来源 | 项目维护的 `extensions/catalog/plugins.json`，随 Controller 分发 |
| 插件代码、配置 schema/默认值、安装与初始化逻辑、原始 Skills | 插件包本身；安装到 Host 后随包保存 |
| 用户选择安装哪些插件、选择的版本、enabled | 账号 Redis Hash 的 plugins 字段；表示用户期望，不冒充实际运行状态 |
| 实际包是否存在、真实版本、加载成功/失败及错误 | Host 包管理器与 Cordis 实际状态，接口返回给前端，不作为 Redis 中的第二套运行状态机 |
| API Key、AccessKey/Secret、密码、OAuth client secret/token 等 | Go credentials 唯一存储；通过 authorization 或插件已有凭证流程取得，不放入 Redis 的插件设置或写回发布包 |
| 将来某具体插件确需的非凭证用户业务数据 | 由该插件按实际业务声明并在 Host 侧保存；不修改发布包源码，不因此预设通用 config 存储、表单或新 storage SDK |

包的配置定义与用户填写值不是同一份数据。当前这两个 CLI 插件无需用户输入业务参数或凭证，采用官方默认行为，不新增空泛的配置表单。账号名、项目名等若是某个授权流程采集的账号资料，也可作为该流程的 credential grant payload，不必另设业务配置。

Controller 的公开管理界面按插件包提供安装、启用、停用、更新和卸载；不再要求用户选择实例引用哪个包或创建第二份实例。Cordis 内部仍可使用其原生 entry/fiber 来装配一次插件，内部加载身份不等于 Redis 必须保存一套用户可管理的 instances。

Redis JSON、Controller/Host 管理合同、Go Client/OpenAPI、Web/Mobile 管理页面与测试均按此包选择模型实现。开发期直接更换该模型，不做旧 instances/config 的兼容或迁移。Cordis 内部装配身份仅用于运行与诊断，不成为用户配置。

### 4.4 不生成 Host 本地 plugins.json

仓库 extensions/catalog/plugins.json 是受审市场清单，继续保留。删除 Host 本地 plugins/profile/plugins.json 及其写入代码；Redis → Controller → Host → runtime.apply 已经完整传递期望配置，无需再落一份副本。Host 的 package.json、锁文件和 node_modules 只负责已安装包与依赖。

GET /v1/settings/plugins 的 settings 字段仅返回用户包选择、版本和启停；status 返回真实安装和 Cordis 运行结果。不存在第二份用户可编辑的 Host 设置文件。

## 5. Agent 最终获得的内容

### 5.1 agent-browser 插件

- 一个原生官方 `agent-browser` 发现 Skill，保留原始名称、摘要和正文。
- 一个可在 Go Shell 中直接使用的 `agent-browser` 命令。
- 命令继续读取官方 `skills get core`，再按需读取 reference、专项指南，不将这些指南预加载成另一套常驻系统提示。
- 使用 Host Chromium，默认无头运行；截图写工作区后仍用内置 readimage，交付仍用 present。
- 浏览器 socket、不同会话间的隔离、受限沙箱的临时环境继续由 Go 处理；不把 Shell 改为通过 Node 执行。

### 5.2 OfficeCLI 插件

一个 `officecli` 命令，加上当前官方完整的 11 个 Skill 目录：

```text
officecli
officecli-docx
officecli-xlsx
officecli-pptx
officecli-academic-paper
officecli-data-dashboard
officecli-financial-model
officecli-pitch-deck
officecli-word-form
morph-ppt
morph-ppt-3d
```

发布阶段使用插件安装的官方 CLI，在隔离 HOME 中运行已有官方 Codex 安装命令，再将生成目录完整放入插件包。包括 references、脚本、模板和二进制素材，不能只复制 SKILL.md，不能把目录合并或改写正文。

运行时使用 `ctx.host.skills.registerDirectory('skills')` 交给 Go 原生发现器。模型经现有 skill 工具加载，工作区文档继续由 Go Shell 调用 OfficeCLI 创建、编辑、验证和保存。没有自写的 Word/PPT/Excel 工具集合。

### 5.3 Skill 管理的变化

插件 Skill 是包贡献，不再作为首次启动 seed 复制到 `.dsh/skills` 或 `.agents/skills`。所以停用/卸载会撤销插件贡献，升级会随包切换。

用户自行安装或修改的 Skill 仍位于现有用户根，遵守原优先级；插件不会覆盖或删除它们。若用户主动复制了一份同名 Skill 到用户根，该副本属于用户，停用插件不会删除副本，CLI 的安装状态按下节的依赖管理规则处理。

本次不设计旧 seed 的自动迁移、目录猜测或覆盖删除；使用干净测试账号验证新流程。已有数据中的人工副本不会被当成插件文件清理。

## 6. 安装依赖与启停的边界

**不增加 `ctx.host.commands.register()`。** 官方全局安装已经完成程序位置选择和命令入口创建。Go Shell 通过标准 PATH 调用即可，SDK 不需要再登记一次命令。

安装插件时准备官方依赖；启用时仅核对就绪状态并注册原始 Skills；停用/卸载包装插件时撤销它贡献的 Skills。CLI 作为已安装的软件依赖保留，不把“停用插件”实现为系统命令权限开关，也不自动卸载可能被其他脚本或用户使用的软件。

如用户需要移除 CLI，使用对应官方卸载方式，例如 `npm uninstall -g agent-browser`，不为此建立 Go 命令所有权注册表或新管理接口。包装插件升级与系统 CLI 升级分别检查和报告，不能将前者成功等同于后者已升级。

### 6.1 Go 仍需保留的环境适配

- 浏览器命令通过 PATH 查找，去掉固定 `/usr/local/bin/agent-browser`。
- 不再于 Host 启动时缓存 CLI 是否存在；已有会话也能发现后来安装的命令。
- 保留不同会话的 socket 环境、受限沙箱的独立临时环境、会话关闭时的浏览器清理。
- Shell/Jobs 的执行、审批、取消与资源收尾仍由 Go 原有机制处理；不改为 Node 直接执行 Agent 的业务命令。
- 复用系统 Chromium时仅配置官方支持的浏览器路径；这与 CLI 的全局安装路径是两件事，不需要自定义 CLI launcher。

## 7. 文件与镜像边界

**按最新决定保留当前目录结构。** `$DSH_HOME/plugins/profile` 作为唯一的插件包管理项目：

```text
/root/harness/.dsh/plugins/profile/
  package.json
  pnpm-lock.yaml
  pnpm-workspace.yaml
  node_modules/
    @go-bot/
      plugin-agent-browser/
        package.json
        skills/agent-browser/SKILL.md
      plugin-officecli/
        package.json
        skills/
          officecli/SKILL.md
          officecli-docx/SKILL.md
          officecli-pptx/SKILL.md
          …其他官方 Skill 及配套资源
```

这是 npm/pnpm 包的逻辑访问路径；内部实际文件布局仍由包管理器负责。插件无需知道 pnpm store 的具体位置。

插件包附带原始 Skills，Go 从包内发现并加载；CLI 及其平台二进制使用官方/npm 全局安装位置，和上面的插件包位置分别管理。不建立 `plugins/runtimes`、managed-bin 或自有 launcher。

Manager、包管理 manifest 写入、Node 进程工作目录及包解析基准继续使用已有 profile 根；无需目录迁移。用户状态合同按第 4.3 节调整；`ctx.host.skills.registerDirectory('skills')` 的调用方式保持不变。

### 7.1 上游插件保存机制与本项目取舍

核对上游并澄清 profile 的 npm 项目含义后，用户决定当前目录没有问题。此前去掉 profile 层的提议已撤销；保留现有实现。

2026-11-20 查询上游默认分支 `master`，固定到 `2e39e70496aa02986ca6a820c5d497e816b69d7e`。上游将外部插件保存为某个 profile 的 pnpm 依赖：

```text
$DSH_HOME/profiles/<name>/
  package.json           # 外部依赖与 dsh.profile.bundles 选择
  pnpm-lock.yaml         # 依赖解析锁
  pnpm-workspace.yaml    # 包管理配置
  cordis.patch.yml       # 用户对插件条目的配置/启停覆盖
  node_modules/
    <package>/           # 插件包及其自带资源
```

`profile` 表示一套具名运行组合，例如 web、headless 或 sdk；不是所有插件都分别拥有一个 profile。`runProfilePnpm` 在 profile 目录执行 pnpm，包代码进入其 `node_modules`。上游还可使用安装发行物随附的包，并非所有内置核心包都重新下载到每个 profile。

需要补充 npm 层面的准确含义：`profiles/web` 本身确实是一个私有 npm 项目。`initProfile()` 默认生成名字为 `dsh-profile-web`、`private: true`、带 `dependencies` 和 `dsh.profile.bundles` 的 manifest；因此其 `node_modules` 存的确实是这个项目的依赖。这里的依赖包括安装进来的插件包，以及包管理器解析的第三方依赖。它在业务上不是“一个外部插件自己的源码根”。例如 web 模板同时选择 `@deepseek-ai/dsh-base` 与 `@deepseek-ai/dsh-web-app`；用户还可往同一 profile 安装其他 bundle。

应分清三个层次：profile 是组合/安装项目；bundle 是被该组合选择的 npm 包，可装配多个插件条目；Cordis 插件条目是实际加载的模块/实例。不能把“一个 profile 是一个 npm 项目”推断成“一个 profile 对应一个业务插件”。插件自身声明的库依赖可以被提升到 profile 的 node_modules，也可以依包管理结果位于更深位置；不保证每个插件都独占一份 node_modules。

本项目只有一个插件包管理项目，不提供上游的多 profile 产品模式。继续使用 `.dsh/plugins/profile`；`node_modules` 由 pnpm 管理，保留标准依赖安装与 Node 包解析。

另需区分：`npm install -g agent-browser` 安装的是 CLI 软件，沿用 npm 全局位置；Cordis 业务插件本身的安装是 profile 内的依赖安装。CLI 全局命令可访问，并不说明 Cordis 插件包也不需要依赖目录。

本次不改包管理目录，也不引入独立归档安装器或自定义模块解析体系。

源码依据：[profile 定义与初始化](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/boot/app-boot/src/profile.ts)、[profile 内 pnpm 操作](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/boot/plugin-manager/src/operations.ts)。

两者的官方安装器负责选择当前 Linux 架构。本文不额外设计安装文件的 ECI/NAS 持久化、备份或自动恢复。

Host 镜像本次保留 Go、Node/npm/pnpm、Python、Chromium、字体、LibreOffice 与现有通用文件处理环境。只移出两套 CLI 和专用 Skills seed 的预装步骤，不把基础 Linux 环境也扩展成插件安装系统。基础 PATH 保证官方全局安装后的命令可由非交互 Go Shell 访问，不要求用户配置路径。

删除专用 scripts/update-officecli.py 与 OfficeCLI 的额外版本/校验锁。安装策略归 OfficeCLI 包自己的 postinstall；build/dependencies.lock.yaml 只保留其他仍需记录的依赖。

## 8. 前端、文档与发布

- 两端沿用插件市场入口，Web 桌面两列条目、窄屏一列，移动端单列竖向。
- 每个条目按包内 plugin.json 显示图标、原始名称和 description 摘要；点击主体进入独立详情。未安装右侧 + 执行安装，已安装 … 打开详情/重装/卸载菜单；点击外部和 Escape 收起，移动端使用既有底部 Sheet。
- 详情显示身份、description、版本/维护者/许可证；安装和启停为主操作，不再在列表内展开长说明。安装中给出进度与禁用重复操作，失败显示诊断、可重新安装。
- 图标由插件包 plugin.json 的 icon 指定后端图片路径，例如 /assets/plugins/agent-browser/icon.png。图片资源保存在各插件的 assets/ 目录，由 Controller 直接提供；两端只渲染返回的图片 URL，不维护品牌枚举、产品映射或 SVG 绘制逻辑。agent-browser 使用 Chrome 图标，OfficeCLI 使用原始 GitHub 仓库所属 iOfficeAI 的头像；原始名称为 agent-browser 和 OfficeCLI，不翻译产品名称。
- 视觉参考用户截图与 Taste Skill 的等边界、真实图标、克制线框、层次和 44px 点击区；保留当前应用整体浅色风格。[Taste Skill](https://github.com/Leonxlnx/taste-skill/blob/main/skills/taste-skill/SKILL.md)
- 按包展示启停与安装结果，移除新增/选择/删除多实例的用户入口；这两个插件不显示业务配置表单。认证入口仍只按插件实际提供的 authorization/MCP OAuth 能力展示。
- 不新增 Settings 页面、API Key 表单或 MCP 传输方式选择。
- 准备期间沿用加载状态，失败展示安装/版本/网络错误；准备成功才显示就绪。安装操作是同步等待结果，不引入后台循环。
- README 的能力介绍仍说明支持浏览器和 Office，但 Quickstart 增加“在插件市场安装并启用这两个插件”的操作；不能继续声称基础 Host 镜像已预装。
- 插件发布包保留上游 LICENSE/NOTICE 和来源版本；上游 Skills 原文随对应插件版本分发。
- 已有镜像冒烟改为基础环境检查；两套 CLI/Skills 检查移到安装插件之后。沿用已有 CI 作业，不增加新的 CI 作业类别。

## 9. 实施顺序与验收

1. 保留包管理目录，先收敛 Redis/管理接口/前端的按包用户状态合同，清理多实例业务配置；核对官方默认全局 CLI 安装、标准 PATH 和动态浏览器发现。
2. 制作两个 Cordis 包，由包自己的 postinstall 声明官方 npm 安装方式，并生成原始 Skill 资源。
3. 先在干净 Host 完成安装和真实调用，再移除镜像的专用预装；避免删掉旧入口后新入口仍不可用。
4. 将真实包装包随 Host 构建分发，内置市场引用受控 bundled 来源；更新双语文档和两端说明。

验收项目：

- Redis 插件字段仅记录用户安装选择、所选版本和 enabled；无用户可管理的 instances 列表、任意业务 config 或明文凭证。实际状态来自 Host；两端按包管理，凭证流程继续使用唯一 Go Store。
- 插件包装包位于现有 `$DSH_HOME/plugins/profile/node_modules`；完整安装/更新/卸载和 Skills 加载正确。
- amd64/arm64 的官方二进制安装、实际版本输出与 CLI 帮助均正常；不额外限制 OfficeCLI 版本或二进制校验值。
- agent-browser discovery Skill 与上游一致，`skills get core` 由相同版本提供；OfficeCLI 11 个目录及引用资源完整。
- 真实浏览器打开、snapshot、交互、截图和下载；不同会话浏览器隔离，归档/会话关闭后的清理仍正确。
- 创建并多轮编辑 Word、PPT、Excel；保存后用现有读取/渲染能力验证成品，readimage 与 present 仍按原协议展示。
- 已打开会话在插件启用后能使用命令；停用后 Skill 贡献消失，已安装 CLI 保留；重新启用、更新与卸载不误删用户数据。
- read-only、workspace-write、danger-full-access 的执行语义不因 CLI 来自插件而改变，受限命令不能连接此前全权限的浏览器 daemon。
- 下载失败、命令不可执行、PATH 不可见、缺资源和插件准备取消均明确报错，不出现假就绪；OfficeCLI 正常的版本变化不作为错误。
- Host、Controller 与 Android 现有构建/回归通过；Web/Mobile 插件安装、启停和失败表现真实可见。

本方案已按后续更新决定实施，验收记录见第 11 节。

## 10. 上游包安装管理核对

固定上游提交 2e39e70496aa02986ca6a820c5d497e816b69d7e。

- app-boot/initProfile 创建私有 npm 项目、package.json、pnpm-workspace.yaml 与用户 patch。node_modules 同时保存插件及其依赖。
- plugin-manager/installBundle 检查源、平台和兼容性，统一调用 runProfilePnpm 执行 pnpm add；不要求插件导出 Harness 专用 install 函数。
- 普通库依赖使用 package.json.dependencies；安装/构建步骤使用 npm/pnpm lifecycle scripts。上游记录被阻止的构建项，用户批准后写入原生 allowBuilds 并重新安装。不是把全部依赖脚本无条件放开。
- 包安装成功后，再选择 bundle、读取 dsh.bundle.patch 并更新 Cordis；installBundle 默认可激活，也支持 enabled:false。用户单独启停时改配置并重载，不执行 pnpm add/remove。
- 卸载先取消选择、撤销运行条目，再 pnpm remove。更新有自己的旧版本撤销、安装新版本和失败恢复流程；包文件回滚不代表任意外部副作用能自动回滚。
- 上游保留安装日志、取消、兼容检查与结果阶段；本项目保留同步等待/明确失败，不复制多 profile、核心插件 bundle/patch、任意市场地址、运行时 HMR 等产品组织形式。

对应本项目：Go Host 持有安装操作和取消；Node 适配仅调用 pnpm，按原生构建规则执行包内脚本；Cordis 负责加载和撤销贡献。两个包装插件仍采用官方全局 npm CLI 安装，作为各自 postinstall 的业务内容，不放在 apply 中。项目受审市场预先提供这两个包装包声明的标准安装脚本的放行，不额外增加用户审批页面。

源码：[统一包操作](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/boot/plugin-manager/src/operations.ts)、[包管理与启停](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/boot/plugin-manager/src/index.ts)、[原生构建批准](https://github.com/deepseek-ai/deepseek-harness/blob/2e39e70496aa02986ca6a820c5d497e816b69d7e/packages/boot/plugin-manager/src/build-approval.ts)。

### 展示元数据与可更新状态（2026-11-20 补充）

每个插件包提供 plugin.json：name（原始名称字符串）、description（中英文描述）、icon（后端图片路径）、version、maintainer、license。不再提供 capabilities、setup、connection 三套重复展示字段。version/license 与 package.json 一致，构建时校验；市场清单只记录包身份、固定来源和构建脚本策略。Controller 构建内置包内元数据，通过管理 API 提供给两端，前端按字段渲染。

Web 详情返回与顶部返回都回插件市场；移动端详情返回同样回市场。市场版本比 Host 实际已安装版本更高时，后端按 SemVer 提供 availableVersion，两端列表标记“可更新”，详情展示当前/可用版本及更新操作。不能用 Redis 中的期望版本代替实际安装版本；相同版本、构建元数据变化、降级均不标记可更新。

这里对比的是市场中受审版本的 plugin.json 元数据与 Host 实际安装包的版本，而不是读取已安装包自身的 plugin.json 与自身比较。只有市场发布了更高版本才显示更新。用户点击后走统一 pnpm 更新流程；更新成功保留原 enabled 状态，并用新包替换旧贡献。失败返回实际错误，实际安装版本不因 Redis 已保存目标版本而被虚报为更新成功。


## 11. 最终实现与本地验收（2026-11-20）

- OfficeCLI 和 agent-browser 均为受审市场中的可选 Cordis 包。安装通过统一 pnpm 流程执行各包的标准 postinstall；启用只检查现有能力并注册原始 Skills，停用撤销贡献，卸载保留全局 CLI 与用户文件。
- OfficeCLI 的安装命令为 `npm install -g @officecli/officecli`。已移除额外二进制 SHA256/版本锁、专用更新脚本、OFFICECLI_SKIP_UPDATE，以及 Host 对 CLI 版本变化的拒绝。两种 Linux 架构实际安装取得 1.0.163，官方安装器均导出完整 11 个 Skills；官方自身安装校验与更新行为保持原样。
- Host 保留 Node、Python、Chromium、LibreOffice 等基础环境，基础镜像没有两套 CLI；包装包和原始 Skills 随镜像分发。已删除 seed 初始化及本地 plugins.json。Redis 仅保存每个包的 catalogId/version/enabled。
- plugin.json 提供原始名称、中英文 description、后端 icon 路径、包装包 version、maintainer、license。图标位于各插件 assets/，Controller 提供图片端点；两端使用返回 URL 渲染，不维护品牌图标映射或 SVG 绘制。未安装插件或未运行 Host 时也能读取市场图标。
- 市场版本与 Host 实际已安装包按 SemVer 对比。更新测试覆盖旧版本仍可启停、明确更新、保留 enabled、新工具回调替换和消除可更新标记；不以 Redis 的目标版本冒充成功安装。
- Go 全量测试与 vet 通过；Node 契约与原生 pnpm 生命周期测试 12 项通过；Web 测试 19 项通过，Web 构建及移动端类型检查通过。
- Host、Controller 镜像与 Android ARM64 QA APK 构建成功；基础镜像冒烟及 Docker 全量回归通过。回归包含插件安装、动态浏览器发现、真实 Chromium 操作/截图/下载及会话隔离、Office 会话恢复、Redis 用户字段、SSE、权限、模型设置、记忆与定时任务。最终 Docker 全量运行约 84 秒。
- 真实 OfficeCLI 创建并再次编辑 Word、PPT、Excel，核对修改后的 OOXML 文本、单元格值、公式与计算值，并分别导出 PDF。无版本限制的 1.0.163 仍可读取已有验收文件。
- Web 实测双列市场、窄屏单列、原始名称和描述、真实安装/重装进度、启停、菜单外部收起、详情返回市场；确认图片 DOM 指向后端路径并成功加载。Android 模拟器已安装最新 QA 包，实测单列市场、后端图片加载、详情、停用/就绪状态切换及返回市场。截图与日志保留在本地忽略目录，不作为项目运行依赖。

本轮未验证 ECI/NAS、iOS 或发布新的商店/Release 安装包；未提交或推送仓库内容。
