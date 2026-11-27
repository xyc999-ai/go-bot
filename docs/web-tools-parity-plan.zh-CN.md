# Web Search 与 Web Fetch 设计

本文保留搜索与抓取的实现边界。用户配置见[配置说明](configuration.md)，供应商接口和当前目录见[能力扩展](providers.md#search)。

## 工具与供应商

- `web_search` 由 Go 工具系统调度，调用供应商无关的搜索接口，输出来源、链接和摘要。搜索供应商作为 adapter 接入，不由前端实现搜索。
- 当前供应商为 DeepSeek、智普、豆包、Tavily、Brave 和 Firecrawl。用户在 Web 或移动端保存配置并选择使用的供应商；Controller 保存账号配置并同步到 Host。
- 搜索凭证使用该账号保存的搜索设置，不复用模型 API Key，不在启动脚本中自动初始化搜索凭证。没有可用配置时，搜索调用应明确报错；不影响普通会话和不需要搜索的工具。
- DeepSeek adapter 复用当前用户问题作为 `ori_question` 等供应商请求信息；供应商专有字段和 HTTP 逻辑放在对应的 provider / thirdparty 代码中。

## 网页抓取

- `web_fetch` 使用 Host 内的 HTTP provider，请求、解码并限制响应正文；保留公开网络地址检查和受控拨号。
- HTML 转 Markdown 在 Go Host 内使用 Goja 执行固定的上游渲染代码，包含 Turndown、GFM 和 Domino。此转换不依赖额外 Node 子进程。
- 浏览器交互是 agent-browser 插件提供的另一项能力，不把页面抓取与 Chromium 操作混为同一执行路径。

## 输出与日志

- 默认搜索超时为 60 秒，抓取超时为 30 秒。
- 模型文本与工具元数据分别保存，工具调用和结果走统一的 Go 工具执行及会话提交机制。
- 超过公共输出预算时保留有界预览，并将完整格式化输出保存到当前工作目录的 `.agent/web-spill/`；预算与截断方式由公共输出模块管理。
- 前端通过现有会话投影展示工具结果，不需要独立的网页工具执行协议。

## 代码入口

| 内容 | 位置 |
| --- | --- |
| 搜索和抓取工具 | `harness/tools/websearch.go`、`harness/tools/web.go` |
| 输出保存 | `harness/tools/web_spill.go`、`harness/output/` |
| 供应商接口与工厂 | `providers/web/` |
| 第三方 HTTP 调用 | `thirdparty/` |
| 配置与账号同步 | `internal/controller/`、`internal/host/` |

兼容性行为基线和上游固定版本见[兼容性说明](compatibility.md)，渲染器版权与依赖声明见[第三方声明](../THIRD_PARTY_NOTICES.md)。

## 验证入口

```sh
go test ./harness/tools ./providers/web/... ./thirdparty/deepseek
```

供应商真实调用需要用户自行提供可用凭证；不能把本地 HTTP 测试当成供应商配额、区域或云网络验证。
