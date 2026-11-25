# 独立插件仓库与远程市场

## 目标

将业务插件与主程序镜像的发布解耦。维护者可以发布新插件或版本，已部署的
Controller 在刷新市场后即可显示，用户仍通过现有 Web / 移动端市场安装。
Go 核心能力、Cordis 生命周期、插件 SDK、凭证、授权与 MCP 的职责不改变。

## 仓库边界

两个本地 Git 仓库并列放置，不嵌套、不使用 submodule。用户已创建远端
`https://github.com/xyc999-ai/got-bot-plugins`，远端拼写与本地目录名分别处理：

```text
project/
  go-harness/              # 现有 go-bot 主仓库，保持当前本地目录名
  go-bot-plugins/          # 新的独立公开插件仓库
```

主仓库保留 `extensions/cordis`、`extensions/sdk`、Go 安装/市场模块和基础镜像。
将 `extensions/plugins` 中的 agent-browser、officecli、x、google-workspace、
gemini-api 业务包移到新仓库 `plugins/`，连同安装脚本、Skill、原始许可、图标、
中英文教程及测试。正式镜像不再复制业务包或嵌入其资源。

## 发布与目录

- 每个插件保留独立 package.json / plugin.json，名称、版本、描述与资源仍以
  包内声明为准，不要求维护者手写两份展示信息。
- 发布脚本直接在 GitHub Actions 临时 Linux 运行器中按官方方式准备 upstream
  Skills，使用 `npm pack` 制作标准 `.tgz`，不依赖 Docker、不生成插件镜像。
  业务依赖的安装仍由 package.json 的安装脚本提供。
- 每个插件使用独立 release tag，例如 `agent-browser-v0.1.0`。GitHub Release
  承载包和展示资源；不使用要求普通用户提供 Token 的 GitHub npm Packages。
- 新仓库根目录的 `catalog.json` 提供经审核的发布记录。自动从已发布包生成，
  保留历史版本，区分每个插件的当前推荐版本与用户已安装版本。
- 先上传完整 release 资源，最后更新目录，避免市场列出尚不能下载的版本。
  发布失败不覆盖既有目录。发布工具支持先在本地生成、检查，再发布。
- 插件与主程序分别维护 CI；主项目 CI 不再引用已迁出的业务测试和资源。

## Controller 远程市场

- 默认读取 `xyc999-ai/got-bot-plugins` 的公开市场清单。地址由部署端入口指定，
  客户端仍只提交 pluginId，不提交任意包地址。不新增普通用户必填配置。
- 获取采用短超时、ETag、短期内存缓存及显式刷新，不新增后台轮询。
  缓存只是派生数据，不把公共市场写进用户 Redis，也不增加 Controller 状态文件。
- 拉取失败时保留本进程最近一次有效目录；没有有效目录时清楚提示重试，不能
  把错误伪装成空市场。已有聊天、会话、模型与授权能力不依赖刷新市场才能运行。
- 安装、更新、恢复分别按 pluginId + 包名 + 所选版本精确解析发布记录；不能
  将已安装旧版本指向市场最新包。新版仅显示可更新，由用户主动操作。
- 默认插件仍为 officecli、agent-browser。新账号取得默认选择，首次准备流程
  从网络安装并启用；后续恢复不重新补回用户已卸载的默认插件。
- 图标与教程由 Controller 按目录中的已发布资源获取，通过后端资源地址提供。
  支持缓存、视频 Range / HEAD，市场展示不需要先在 Host 安装插件。

## Host 与 pnpm

- 用户安装位置继续为 `$DSH_HOME/plugins/profile/node_modules`；不更改 NAS、
  工作目录、凭证位置或授权流程。
- 下载、依赖安装和生命周期脚本统一使用 pnpm。远程归档需要接通准确的
  pnpm allowBuilds 许可；检查包时仍禁止执行安装脚本。
- 安装成功后 Cordis apply 只注册能力，停用不卸载 CLI；更新与卸载沿用现有流程。
- 移除 bundled 业务来源和主镜像中准备业务资源的阶段，清除相应旧测试、导入和文档。
  开发目录与标准远程包来源保留给开发和受审查的配置，不开放客户端任意安装入口。

## 共享框架依赖

- SDK 的 JavaScript 不依赖 Cordis；Cordis 仅供开发时类型定义使用，移到开发依赖
  与可选 peer 声明，生产安装保持 `autoInstallPeers=false`。
- Host 从共享 Cordis 运行环境提供框架模块解析入口，插件使用的是同一真实实例，
  不在每个用户的 `.pnpm` 中再安装 Cordis、cosmokit、standard-schema 这套基础依赖。
- 插件自己的业务依赖仍正常安装。发布检查要求核心框架使用 peer / 开发依赖，
  不将主程序基础模块包装为业务插件。
- 不改 ctx.host 接口、RPC、生命周期与回调执行边界，不引入新的插件执行框架。

## 验证与交付

覆盖市场刷新发现新插件、网络失败与缓存、旧版本恢复、资源代理、远程 tgz 的
安装脚本执行与检查禁用、安装后启停、SDK 共享模块解析和依赖树。
使用本地 HTTP 发布服务和模拟插件验证完整下载链，再在 Docker 中验证实际 Host。
Web 与移动端检查市场刷新、图标、教程和更新状态；不构建 Android。

新仓库需初始化 Git、MIT 许可、贡献/Agent 规范、发布工具与工作流。若当前会话
没有 GitHub 创建/发布凭证，先完成本地两个仓库与完整验收，列明建立公开仓库及
首次发布所需操作，不能将尚未公开的 URL 说成已经可用。主仓库依赖新目录上线后
才能正常联网安装默认插件；这一先后关系需在交付和文档中明确。

## 本次落地与验收（2026-11-26）

- 独立仓库已推送，首批五个插件的 GitHub Releases 和 catalog.json 已公开发布。后续按用户要求移除了打包 Dockerfile；[直接在 Actions 运行器执行的新发布工作流](https://github.com/xyc999-ai/got-bot-plugins/actions/runs/45695476827) 的原始 Skills 准备、npm pack、既有发布核对和目录步骤均成功，没有覆盖既有版本。
- Go 全量测试、vet、依赖边界检查通过；catalog、Controller、plugins 的相关 race 测试通过。Cordis 13 项、SDK 类型与测试、Web 构建及 43 项测试、移动端类型检查及 9 项测试通过；独立插件仓库 19 项测试及发布检查通过。
- Host 镜像完成一次完整构建与基础环境检查。Docker Web 聊天、Redis 插件选择保存、真实网络安装默认插件及会话/文件恢复回归通过，验证了原始 OfficeCLI Skills 和真实 DOCX 创建。
- 真实 HTTP 验收：公开市场返回五个插件，新账号选择两个默认插件；五个图标返回正确 PNG 类型，X、Google Workspace 教程支持 MP4 Range 和 HEAD。媒体类型按声明文件扩展名规范化，兼容 GitHub Release 的 application/octet-stream 响应。
- Node 远程归档测试验证：检查阶段不运行脚本，正式安装经 pnpm 放行执行，Cordis 与 Host 实例相同；持久 profile 中失效的旧框架符号链接可被修复，不重复安装框架依赖。
- 补充 Controller HTTP 契约测试通过：普通读取复用清单缓存，显式刷新发现新插件与新版；刷新后保留原用户选择，并仍解析到旧版本的准确安装来源，展示资源地址绑定相应发布版本。
- 当前媒体缓存依靠客户端及 GitHub/CDN；Controller 不保存媒体磁盘副本，安装插件不改变市场资源的读取位置。
- 本轮不构建 Android。浏览器控制持续超时，因此没有宣称完成额外双端可视化截图验收；前端验证为构建、类型、测试和真实 API/媒体请求。
