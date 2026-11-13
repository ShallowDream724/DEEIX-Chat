# 聊天模型、流错误与首响应排查（2026-11-13）

本轮基线为 `6d66de57`。生产反馈包括 Gemini Hydrogen 的 xhigh 不显示、原生附件出现 OCR、长内容/长历史卡顿、模型分组空白、GPT Terra 的内部引用标记以及跨渠道首响应等待 10–60 秒。以下区分代码已证实的问题与需要生产证据的问题；本地修复不等于已更新生产镜像。

## Gemini 推理能力

`domain/channel/model_reasoning.go` 原先把 `gemini_level` 限制为 minimal/low/medium/high：旧 optionControls 推断会丢弃 xhigh，显式 reasoning 声明可能整体无效。现在允许管理员明确声明 xhigh；GenerateContent 和 Interactions 使用原有参数翻译链路，不按模型名猜测能力，不给旧模型自动追加档位。

Hydrogen 可配置：

```json
{"reasoning":{"format":"gemini_level","levels":["low","medium","high","xhigh"],"default":"high"}}
```

也兼容原有 `generationConfig.thinkingConfig.thinkingLevel` select 控件与 defaultOptions 配置。Hydrogen 官方不接受 minimal 或 thinkingBudget；其他 Gemini 模型按各自能力配置。[Google 官方 Hydrogen 指南](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/guides/gemini-4-hydrogen)。

## 原生文件与 OCR

这里有两条独立生命周期：

- 上传后，`processing.InitializeUploadedFile` 按文件类型和配置排队提取文本，用于文件管理、检索和后续文本模型。图片 OCR 由 `extract.image_ocr_enabled` 控制；PDF 可按 `extract.pdf_ocr_fallback_enabled` 在文本不足时回退 OCR。
- 发送时，实际路由满足原生输入要求的文件标记为 native_document；`hydrateAttachmentsForSend` 不等待它的后台提取。`withoutNativeParsedContext` 同时排除该文件的全文、检索片段和未检索提示，避免同一请求重复注入原件和 OCR 文本。

因此，本站文件处理状态中的“OCR”来自本站后台处理，不是 Gemini 官方工具事件的证明。后台工作可能独立存在，但不能据此认定 Gemini 此次发送先 OCR 后再发送，或认定它解释了所有无附件请求的延迟。Gemini 官方本身支持原生 PDF 文本与视觉理解：[文档处理](https://ai.google.dev/gemini-api/docs/document-processing)。本轮不改变全局上传解析策略，以保留其他模型与检索使用这些文件的能力。

## 内部引用标记

`citeturn735019search1turn735019search3` 是内部引用标记，不是一个可直接访问的 URL。普通 Chat Completions 适配器当前原样接收 content；Responses 可收集结构化 URL annotations，但 URL 列表本身没有足够信息把任意 turn/search ID 还原成链接。Chat01 有自己的引用归一化，其无链接占位不适用于任意供应商协议。

这一样例证实展示链路没有处理该私有标记，不能单凭它判定渠道来源、模型真伪或“API 不纯”。缺少实际 Terra 路由协议及返回的 citation/annotation 字段结构，无法可靠恢复引用。未伪造链接，也未悄悄删掉存储的引用。后续需要一条问题请求的协议名称和引用元数据结构，优先复用明确的 source ID → URL 对照；只有裸 ID 时必须明确表示来源链接缺失。

2026-11-13 后续决定：用户明确暂缓此项，当前不继续实现引用适配或调查渠道来源；上述证据要求留待恢复排查时使用。

## HTTP 200 错误

OpenAI、Gemini、Anthropic 共用错误调试包装。HTTP 200 只证明响应头成功，随后 body 仍可能超时、断开、截断或格式错误。原先后端把所有 2xx 错误都称为“格式不兼容”；前端看到成功状态的 SSE 调试内容也会覆盖真正原因。

修复后，保留 cause 并区分取消、读取超时、传输中断、JSON 格式错误和上游业务错误。流空闲超时具有可识别的 timeout 类型；前端优先展示服务端分类结果，SSE 正文本身不决定错误类别。已受理请求的 reader/parser/callback 错误不能因为正文含有 stream unsupported 等文字而触发非流式重发。调试 HTTP 状态和受理/计费标记保持不变。

## 每条请求首响应慢

目前没有生产慢请求的分段记录，不能宣称 10–60 秒的根因已解决，也不能把它直接归因于 API 或网络。前端先创建乐观消息，但思考/工具计时组件要等对应上游事件才出现；它不是请求发出时间。

已检查的共用链路未发现每条固定等待 10–60 秒的显式定时器。附件读取、图片处理、条件性上下文压缩可能耗时，但都不足以单独解释所有渠道每一条消息。发现普通发送原先无条件执行文件能力与文件模式偏好检查，其中包含向量结构探测；若数据库探测持续失败，可能反复等待。

现在先确定活跃上下文附件集合。既无当前/历史附件、又无启用且选中的知识库时，直接跳过文件能力和文件模式偏好读取；有附件或有效知识库选择仍保留原有检查。六个行为场景验证真实 embedding 服务的可用性探测与偏好读取次数，纯文字为零。该改动移除了公共路径中的无用依赖，实际节省多少时间仍需生产记录确认。

新增两个不包含正文、文件 URL、密钥或完整响应的日志事件，通过同一 `request_id` / `run_id` 关联：

| 事件 | 关键字段 | 含义 |
| --- | --- | --- |
| `conversation_send_preparation` | `prep_total_ms`、各阶段 `*_ms`、`last_stage`、`failed` | 应用服务入口到首次实际调用上游；准备失败时在清理前结束 |
| `conversation_upstream_first_event` | `llm_request_attempt`、`wait_ms`、`elapsed_ms`、`first_event`、`protocol` | 每次实际调用到第一个应用层事件；工具后续轮次/降级分别编号 |

准备阶段为 claim、attachments、route、file_capability_history、native_hydrate、retrieval、prompt、transport。native_hydrate 包含原生输入、水合、工具准备和图片处理；prompt 包含提示规划、预算与条件性预压缩；transport 包含最终输入检查及附件传输准备。首次 dispatch 后准备时间冻结，不把后续推理或工具用时算进去。

耗时至少 1 秒或失败记录 Warn，正常记录 Debug；每个阶段/首次事件只记录一次，后续 token 不重复日志。`first_event=metadata` 不等于首正文或首思考，`none` 表示结束前没有应用层事件。HTTP handler 受理前、客户端到服务器的耗时不在 `prep_total_ms` 中，需要与浏览器网络记录和 HTTP 请求日志对照。

生产下一次慢请求可据此判断：准备慢则看具体阶段；准备快而首事件慢则继续区分上游建连、上游计算/缓冲和网络；两者都快而页面迟显示则查反代缓冲与客户端渲染。没有对应证据前保持问题开放。

## 模型菜单与长内容

模型菜单已用“长当前分组 → 短分组 → 关闭 → 重开”复现：默认 focus 同时滚动动画裁剪容器，留下非零 scrollTop，造成列表被裁走。现在自动聚焦不滚动祖先，仅在布局完成后滚动实际列表；动画裁剪层使用 overflow-clip，不能再成为隐藏滚动容器。

长内容采用分层处理：历史消息的视口窗口、大 Markdown 的块级按需渲染、源数据支持的长表分页、所有语言代码的高亮预算，以及归一化算法的线性工作边界。相关行为和性能验证见 [长 Markdown 渲染](long-markdown-rendering.md)。Artifact 按当前会话中的消息内容和身份复用提取结果，流式更新不再重扫未变历史全文；消息离开集合或切换会话后释放缓存。

验证使用本地合成内容、真实已安装解析/渲染依赖和模拟上游，不调用真实付费模型。未据此推断生产错误频率或承诺所有尺寸都在固定毫秒数以内。
