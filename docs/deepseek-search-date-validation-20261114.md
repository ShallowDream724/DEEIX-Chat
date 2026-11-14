# DeepSeek 搜索兼容、日期与工具计费（2026-11-14）

## 本批修正

旧版将 DeepSeek 的 `web_search` 一律规范化成 `deepseek_web_search`，再借用仅 Flash 支持的 Files 条件过滤工具，导致 Pro 的既有 Responses 搜索丢失。工具和文件能力已分开：按真实上游模型与调用协议选择 wire 类型，保留原有 `deepseek.tool_search`、`deepseek.tool_fetch` 计费身份。

| 模型 | 调用协议 | 搜索工具 | 计量 |
| --- | --- | --- | --- |
| Flash | Responses / Chat Completions / Messages | `deepseek_web_search`、`deepseek_read_url` | 搜索、抓取、图片搜索 |
| Pro | Responses | `web_search` | 搜索、抓取；不授予图片搜索或 Files |

Flash 官方旧别名 `deepseek-v4-flash`、`deepseek-v4-flash-vision-exp` 与 `deepseek-flash` 使用同一能力识别，避免旧配置丢失搜索、官方文件输入或搜索历史。高级配置主模板保留 `DeepSeek（Responses）` 名称；现有配置无需伪装成 OpenAI 工具来恢复搜索。协议、供应商身份与实际模型共同约束能力，不按厂商展示名猜测。

## 日期上下文

DeepSeek 官方 API 不替客户端补当前日期。`latest_reminder` 仅支持 Messages / Chat Completions，不支持 Responses。本项目统一以受支持的 system 消息补等效日期上下文，不发送不合法角色，不改用户保存的系统提示词；没有新增 `{{date}}` 等模板变量。

时间取本次发送开始时的服务器时间，按用户档案的 IANA 时区计算日期、星期及 UTC 偏移。档案缺失、读取失败、无效时区或 `Local` 使用明确的 UTC，避免依赖服务器部署时区。不推断所在地或覆盖用户语言。

普通对话、临时对话、路由重试/切换和工具续轮均保持同一请求日期。档案只在实际命中 DeepSeek 路由时读取一次；日期精确到天，日内不随秒变化，不新增外部 API、音频或文件处理。新的用户请求会重新计算。系统提示词静态部分保持原顺序，日期进入最终请求和预算/追踪。DeepSeek Responses 同时补齐原生 system 能力识别。

日期可减少“今天”被解释成旧日期的问题，但不保证搜索结果新鲜度，不能将当前日期注入等同于证明某次天气结果准确。

## 价格与显示

截至核验日，官方网页搜索（旧/新）、图片搜索、网页抓取均为人民币 5 元/千次；美元刊例为 0.75 美元/千次。本项目管理员工具价格输入单位为 **USD/次**，按美元刊例填写三项各 `0.00075`（750000 nanousd）。如果按人民币实际成本定价，应使用 `0.005 / 站点 USD→CNY 汇率（1 美元对应的人民币数）`，不可把人民币或每千次数字直接填入美元每次输入框。保持既有费率和收费开关，不自动修改生产价格。

- `deepseek.tool_search`：网页搜索，旧 Pro 与新 Flash 共用。
- `deepseek.tool_fetch`：网页抓取，包括搜索内部实际执行的抓取。
- `deepseek.tool_image_search`：Flash 搜索的计量子项，无独立请求工具或开关。图片及模型框选区域分别计次；不是图片生成。

以上按官方 `usage.server_tool_use` 中的 `tool_search`、`tool_fetch`、`tool_image_search` 计数，不按界面事件数或搜索结果块数计费。图片搜索不再计入网页搜索次数。模型 token 费用另计。工具价格为 0、收费关闭或自用模式不会产生相应收费明细，原始用量仍可保留。

用户账单、消息费用与管理员日志均应从已有 `service_items` 展示工具次数、单价和金额；账单总额沿用服务端结算值，不由前端再次加总扣费。

用户确认无需新增更细的单价展示，本站自定收费可取人民币 0.01 元/次，不要求跟随官方 0.005 元/次。保留原有两位单价格式，不修改账本 nanoUSD 精度、既有金额展示规则或已保存的生产费率。管理员定价输入单位与账单显示币种仍须区分。

## PDF 反馈边界

本批不改 PDF/Office 的 `rich_layout_recognition`，不再叠加一次付费 OCR。官方文件上传在解析结束后才返回，解析时限 120 秒且期间不发送进度；未拿到失败原件和该轮时序，不能把用户反馈的扫描页识别失败或耗时全部归咎于上游。旧 Flash 别名导致原生链路未命中是已修正的兼容缺口，但尚不能证明它是用户那份 PDF 的原因。

## 验证

- Go 的 conversation、nativetool、deepseekfiles、deepseekmodel、llm 与 billing 相关包通过。覆盖 Pro 新/旧 JSON、关闭搜索、供应商身份、协议限制、抓取计量与图片计量隔离，以及 Flash 旧别名的工具、文档、URL 与历史回传。
- 日期覆盖东西时区跨日、空/非法时区、档案读取失败、懒加载一次、普通/临时请求、重试路由、非 DeepSeek 隔离、原消息不变；确认 Responses 不发送 `latest_reminder`。
- 前端工具迁移与费用明细专项回归、相关 Biome、全量 TypeScript、Web 生产构建和后端构建通过。390px / 1440px 浏览器检查人民币/美元工具明细，均无内容横向溢出或运行时错误；保留原有单价显示规则。
- 本地后端更新至本批构建并通过 `/healthz`。本批没有新增真实付费上游请求，协议依据来自当日官方文档，不能将模拟用量回归描述为已核对真实供应商扣费。
- 私有证据：`.cache/local-dev/deepseek-date-tests.log`、`deepseek-date-web-build.log`、`.cache/admin-tool-billing/browser.json` 及同目录截图。临时浏览器页面已移除，独立测试浏览器已关闭。

## 官方依据

- [Responses 的 Pro 搜索与工具支持](https://api-docs.deepseek.com/guides/responses_api/)
- [联网工具及图片搜索、用量](https://api-docs.deepseek.com/zh-cn/guides/web_tools/)
- [用户元信息标记](https://api-docs.deepseek.com/zh-cn/guides/latest_reminder/)
- [人民币价格及 Flash 别名](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/)
- [美元价格](https://api-docs.deepseek.com/quick_start/pricing/)
- [文件解析限制](https://api-docs.deepseek.com/zh-cn/guides/document_parsing/)
