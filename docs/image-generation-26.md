# OpenAI 图像与动画适配

核对日期：2026-11-06。本批仅本地验证，未执行收费 OpenAI 请求，也未发布生产镜像。

## 协议和产物

- 支持 `gpt-image-2.6-sunburst`、`gpt-image-2.6-flare` 的生成和参考图编辑。`output_format: "gif"` 明确选择动画；自然语言描述本身不保证改变输出格式。
- GIF 参数为 `frame_count`（2–16，供应商默认 9）和 `frame_duration_ms`（20–2000，供应商默认 200，按 10ms 取整）。构思强度 `complexity` 与画质 `quality` 独立。
- GIF 上游使用普通请求，不能携带流式预览、压缩率或蒙版。只有在适配器尚未发出任何事件、明确拒绝流式且请求未取消时，应用层才回退普通调用，不重试已经产生费用的生成。
- 本站继续采用同一后台任务、心跳、刷新恢复、权限、文件归属及配额规则。原始 GIF 字节直接进入已有生成图片保存/下载通路，不逐帧解码或转成 PNG；仍遵守管理员设置的生成图片保存上限。
- JPEG/WebP 压缩率允许 0–100；编辑路径也发送 `moderation`。新参数进入后端默认白名单；只迁移与已知历史默认值完全相同的旧白名单，管理员自定义名单保持不变。
- 图像模型默认固定输出格式、画质两个控件，尊重用户已有固定/取消固定状态。GIF 帧参数按有效格式显示，管理员明确限定的可选项不自动扩大。
- SVG 涉及额外作者模型计费和文件展示；markup 需要输入编辑交互。本批不新增这两项能力。

## 分项计费

`billing_model_prices.image_pricing_json` 为增量新增列，默认 `{}`。HTTP DTO 使用可选 `imagePricingJSON` 字符串；模型定价 JSON 编辑和导入/导出支持直观的 `imagePricing` 对象。空配置保留旧计费方式。Token 模式下可显式启用以下配置，按实际文本/图像用量替代普通合并 Token 单价：

```json
{
  "textInputUSDPerMTokens": 5,
  "imageInputUSDPerMTokens": 8,
  "cachedTextInputUSDPerMTokens": 1.25,
  "cachedImageInputUSDPerMTokens": 2,
  "imageOutputUSDPerMTokens": 30,
  "textOutputUSDPerMTokens": 0
}
```

前五项必须明确填写，零表示该类免费；最后一项可省略，默认零。以上仅为核对当日 GPT Image 2.5/2.6 官方标准价格的 JSON 示例，单位 USD / 百万 Token，并非所有渠道的默认收费。快速配置继续采用已有 OpenRouter 和 JSON 来源，不新增自行维护的供应商价格库；来源未给出的分项由管理员填写。应用通用价格时清除旧图像策略，避免两套配置混用。

原生工具价格中的 `openai.image_generation` 可使用相同 `imagePricingJSON`，启用后替代该工具按次价格，不同时收两份。模型权限、协议和获准工具身份继续沿用现有策略。工具内部 `model` 只接受管理员能力配置指定的值，用户的 `options.tools` 不得覆盖；其他工具及敏感字段过滤保持有效。不自动开启 Image Generation 工具或修改现有价格。图像工具继续行内编辑，分项开关与价格区域右对齐；展开字段以标签在上、输入框在下的两列布局展示，保存复用现有工具计费接口。

适配器把 direct Images `usage` 和 Responses 顶层 `tool_usage.image_gen` 归一化为独立用量结构。后者不会混入语言模型上下文占用；多次模型调用累计，单次流累计事件使用差值去重。官方返回的图像输出用量已经是计量结果，不再乘 GIF 帧数。

价格和工具模型在请求开始时固定，包括尚未启用图像策略的状态；运行中改价或改计费方式不会改写已授权的图像策略。估算预留和最终实际结算分别处理。失败、取消以及存储失败保留已经观察到的付费用量。

缓存未报告不冒称已观察到零缓存，只对明确报告的缓存量打折，剩余输入按普通输入价；原始上游数据与观察状态保留在账单快照。缺少必要分项、只有无法分配的正数缓存总量或工具模型不匹配时进入既有待核对流程，不伪造分项。供应商说明 hosted aggregate 可能跳过不完整调用记录，因此不能把 API 返回的汇总宣称为最终供应商账单。

## 展示边界与验证

历史 assistant 产物未参与当前文件上下文时，不再显示无关的“未纳入”处理提示；用户重新附上的同一文件仍按用户输入处理。管理员日志与聊天账单明细共用分项快照映射，显示已记录的文本、图像及缓存用量和金额，不用通用零单价拼接错误公式，也不在前端重新结算。

离线测试覆盖：生成/编辑 GIF 请求、默认流式回退、Base64/URL 产物字节与动画元信息保持、参数和旧默认配置迁移、用量与上下文分离、重复事件、错误/取消后用量保留、改价快照、旧数据库迁移、分项算价和工具模型权限。生产账号的模型权限、预览资格和供应商实际行为仍需使用方上线后验证。

官方依据：

- [生成与编辑指南 / GIF](https://developers.openai.com/api/docs/guides/image-generation#generate-animated-gif-output)
- [Images Generations](https://developers.openai.com/api/reference/resources/images/methods/generate)
- [Images Edits](https://developers.openai.com/api/reference/resources/images/methods/edit)
- [Responses：image_generation 与 tool_usage](https://developers.openai.com/api/reference/resources/responses/methods/create)
- [图像价格](https://developers.openai.com/api/docs/pricing#image-generation)
