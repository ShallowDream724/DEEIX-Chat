# 分支排期

以下为已确认范围，按阶段推进，未约定发布日期。界面沿用 DEEIX 组件与样式，尊重已确认截图的布局和交互；改动集中在现有边界，减少上游同步冲突。

## 2026-11-04 排查与实施顺序

本轮先核对现状并制定计划。基线是已发布的 `56b0e8db`，加上 `f2adbb05` 的排期记录；不是旧主目录中的 MinerU 版本，也没有混入未发布的 Chat01 初版或计费补丁。唯一活动工作树已统一到 `C:/Users/dell/Documents/DEEIX`，分支 `shallowdream/native-files`。

### 1. 原生附件、压缩与分支（优先）

- 覆盖 Gemini PDF、音频、视频，OpenAI PDF，并核对当前已接入的图片及其他原生路径。统一检查原件发送、估算、缓存、重试、切模型、故障转移和历史分支，不把调查范围缩成一个 PDF 常量。
- 已确认：`service_message_context.go` 对所有原生 PDF 统一按每页 2,000 tokens 估算；未知页数按原件每 100 KiB 推算一页。音频按 32 tokens/秒、视频按 300 tokens/秒；时长未知时又按固定码率从文件大小推算。估算没有区分实际模型与媒体处理设置。
- 已确认：压缩软阈值是 `(contextWindow - min(maxOutputTokens, 20000) - 13000) × triggerPercent`，不是直接对完整窗口取百分比。响应后的压缩判断使用 `plan.fullContextPromptTokens + effectiveOutputTokens`，仍采用请求前估算。截图中 216,073/216,173 的估算与约 69,254/69,370 的上游用量存在明显差异；缺少实际配置和原始响应，不能据截图独断确切页数、模型 ID 或全部上下文占用。
- 压缩 trace 中 `SourceTokens` 又来自历史消息的 `TokenUsage` 合计，用户消息存的是当轮整个输入用量。它与请求前估算、摘要文本 token 数不是同一口径，多轮还可能重复累计此前输入。需一起修正统计含义，不能将“69,355 → 241”描述为整个请求或 PDF 已减少到 241 tokens。
- 已确认：`loadMessageBranchContext` 遇到摘要覆盖边界会停止读取更早历史，`collectConversationFileIDs` 随后只收集压缩后 `promptMessages` 中的附件。摘要是文本，未携带被覆盖消息的原生文件引用。该路径可以导致压缩后原件消失；“第三轮压缩、回到第二轮创建分支丢文件”的具体快照边界仍需回归复现。
- 实施方向：将当前分支可用的附件引用与保留的聊天文字分开管理；按祖先关系、覆盖范围验证摘要，防止混入兄弟分支/后续消息。保留必要的文件身份及归属，再按当前路由能力和预算规划原生输入，不能用全会话文件列表替代分支隔离，也不能仅改为扫描已经截短的 `FullBranchMessages`。
- 计量按实际供应商、模型、内容类型与媒体参数处理。区分本地估算、上游上下文计数和计费用量；供应商 PDF 文本/图像计量口径可能不同，不能将每页 2,000 简单统一改成 258，也不能从上下文占用扣掉缓存命中 tokens。评估在临近阈值且路由支持时调用官方 token counting，并复用稳定内容的计数，避免每轮额外传输整个大文件。
- 回归覆盖：同一文件连续问答；摘要覆盖边界早于/等于/晚于分支起点；回到第二轮编辑或重试；压缩前后分支独立；切换原生能力不同的模型；附件读取失败、被删及预算不足。验证实际出站请求中存在所需原件，不能只断言 UI 标签或摘要包含文件名。

### 2. 原生文件缓存核对

- 当前 `nativeInputCache` 只避免同一次发送及路由重试中反复读取本站存储，不是跨轮或上游缓存。Gemini PDF、音频、视频与图片仍通过 `inlineData` 编码发送；没有自动管理 Files API 文件资源或显式缓存对象。
- “可缓存稳定块”来自 prompt 规划的 `Cacheable` 标记，不是上游命中证据。Gemini `cachedContentTokenCount` 已读入缓存用量；显式 `cachedContent` 目前仅支持选项传入。需核对两种 Gemini 协议及实际模型的支持范围，不能因为内部标了 `CacheControl` 就声称创建了 Google 缓存。
- OpenAI 的 `previous_response_id`、prompt cache 和本站文件读取缓存分别检查。目前有默认分支、官方地址、prompt 指纹等续接限制；分支/压缩后重新发送原件是正确性兜底，不能依赖缓存来保存文件可见性。
- 对照 [Gemini 缓存](https://ai.google.dev/gemini-api/docs/caching)、[Gemini tokens](https://ai.google.dev/gemini-api/docs/tokens)、[OpenAI prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching) 和 [会话状态](https://developers.openai.com/api/docs/guides/conversation-state)，核对稳定前缀、模型/路由边界、命中统计、缓存写入/读取费率与上下文容量。当前官方文档对 Argon/Interactions 与旧版 GenerateContent 的显式缓存支持不同，实施时按实际路由选择。

### 3. OpenAI 分组全部原生工具详情

- 覆盖现有目录的 Web Search / Preview、Shell、Code Interpreter、Image Generation；兼顾已由模型合法声明的其他原生工具，不局限于两个代码工具。
- 复用现有 `ServerToolCall`、持久化记录、trace 与工具详情接口。按工具官方结构保留代码/命令/查询、状态、输出、错误、来源和生成产物；大图片不以 base64 塞入详情日志，产物继续走已完成的文件边界。
- 已确认：默认 include 只补 Web Search 来源，没有自动补 Code Interpreter 输出；通用解析器没有读取 Code Interpreter 顶层 `code`；完整结果弹窗只展示输出/错误。Shell 的独立 call/output 关联与流式合并也需用官方形状验证，现有同一 item 内同时放 input/output 的测试不够。
- 对启用的工具补齐适用返回参数，覆盖流式/非流式、重复事件、失败、中断及刷新读取。明确区分执行成功但无输出、上游未返回、旧记录未保存和加载失败。查看详情只读取保存的数据，不重跑模型或工具。

### 4. Google 搜索与 DeepSeek 原生搜索、计费

- Google 已有 `google_search` 发送、grounding 查询/来源解析与工具记录。不能仅凭代码存在宣称线上正常；需验证 GenerateContent / Interactions 的实际返回、引用与流式去重。
- Google 当前目录默认零价，GenerateContent 只要出现 grounding metadata 就计 `google_search = 1`。官方目前对 Gemini 3 及更新模型按查询计费、旧模型按 prompt 计费；计量和价目需按模型区分，不能一概一次响应算一次搜索。参见 [Google Search](https://ai.google.dev/gemini-api/docs/google-search)。
- DeepSeek 复用 Responses 传输与工具展示，在适配边界处理供应商差异，接入原生工具目录、授权、调用详情、引用及多轮回传；与此前独立 Search/Fetch MCP 项目分开。
- [DeepSeek Responses 文档](https://api-docs.deepseek.com/guides/responses_api/) 明确 `usage.server_tool_use.tool_search` / `tool_fetch` 为成功操作次数，流式在终止事件内读取。不能用 `web_search_call` item 数替代成功搜索/读取次数，也不能把这里的 `tool_search` 误当客户端工具发现。
- 已确认：当前版本仍按 `openai_responses + web_search` 映射 `openai.web_search`。此前 `ebf85dda`、`550dee49` 只修自定义工具价格保存/校验，保留于 `shallowdream/fix-native-tool-pricing`，未合入发布基线，完整计费链路并未修完。
- 修复必须传递本轮获准工具的明确身份与价格依据，复用权限、模型访问、预算预留和幂等扣费；覆盖同名工具跨供应商、改价、停用、失败、取消、重复事件、重试与路由变化。费用取证与工具详情共用适配后的结果，不能从 UI 状态猜费用。

实施按上述工作单元提交；文档、针对性回归与实际请求形状一起验收，不重复跑无关测试。本轮调查未进行线上付费 API 测试，也未发布新镜像。

### 工作区收拢与未合入工作

- 旧工作树均已注销，已安装的新版本依赖迁回主目录；旧版依赖/构建残留保留在 `C:/Users/dell/.codex/archives/deeix-cleanup-20261104`，不作为活动源码。旧本地数据与配置未清除。
- Chat01 未提交初版保存为 Git 引用 `refs/archive/chat01-provider-wip-20261104`（包含原未跟踪文件），不是已经验收的功能；旧移动端复现工作保存为 `refs/archive/mobile-nav-repro-wip-20261104`，旧主目录生成文件修改保存为 `refs/archive/old-main-generated-env-20261104`。
- 计费分支与其他历史提交保留在 Git 中，不再为其保留重复活动工作树。后续按范围挑选、重新验证，不能整包合入旧代码。

## 已完成

- 原生附件状态使用紧凑绿色“原生”标签，保持附件布局和已有交互。详见 [原生附件发送与就绪状态](native-file-readiness.md)。

- 接入 OpenAI Responses Shell / Code Interpreter 明确引用文件的下载、用户附件存储、正文下载链接与回复尾部附件卡片。复用现有权限、配额、事务和计费链路，详见 [模型生成文件](generated-files.md)。
- 生成文件能力和已修正的紧凑绿色原生标签已随 `56b0e8db` 镜像发布。

## 后续

### 原生工具执行详情（已纳入本轮计划）

- 排查 Shell / Code Interpreter 的“查看完整结果”为空：核对上游实际返回的执行代码、命令、日志及结果，定位请求参数、响应解析、保存或展示中的缺口。
- 让用户能查看模型执行了什么、工具返回了什么；优先复用现有工具调用详情组件。仅展示实际返回的信息，上游未提供的内容不推测补写。
- 本轮已扩大到 OpenAI 分组的全部原生工具，详见上方当前计划；返回参数及用量影响仍需与工具权限和计费链路保持一致。

### 文件删除提醒与前端联动（待讨论）

- 讨论单个删除、批量删除时，是否识别并提醒其中由模型返回的文件：删除成功后，将无法再从本站下载或重新预览。
- 批量操作需明确提醒覆盖哪些文件，具体提示与确认方式尚未决定。
- 讨论删除后历史回复的文件卡片、正文链接和已打开预览如何联动，包括“已删除”标识、置灰或禁用，以及刷新后的状态一致性；尚未确定最终交互。
- 当前行为：删除后新下载与预览请求失败，但历史卡片、可点击链接仍保留；已经加载的预览可能持续显示至关闭。

### 统一右侧预览

- 宽屏使用可拖拽调整宽度的右侧预览，文件、Artifact、UI block 共用一个活动项，不增加多标签。窄屏全屏展示，保留关闭入口与原有导航。
- 头部显示省略文件名，点击展开全名；提供下载、最大化、关闭，仅支持缩放的格式显示缩放操作。
- 复用现有 PDF、DOCX、文本、代码、Markdown 预览。TXT，包括用户长文本自动转换的附件，使用相同入口。同一文件保留阅读位置与缩放；切换文件取消旧请求，切换会话关闭预览。
- PDF 页进度条及支持 hover、点击、触摸的懒加载缩略图作为独立增强。Word 的非 PDF 精准分页，以及 PPTX / XLSX 新预览暂缓。
- HTML 文件与 Artifact 执行分开处理，文件 HTML 不直接同源运行；后续在隔离边界内评估复用。

### Chat01 独立渠道

- 复用 OpenAI 协议，在渠道内部封装 `file_url`（图片使用 `image_url`），统一 thinking、工具、搜索和 answer；仅适配 API 实际返回的能力。
- 下载文件接入同一生成文件边界；跨模型上下文、数据库兼容、权限和存储限制继续复用。
- 测试使用另行配置的新 Key，不在仓库记录密钥。该渠道改动与上游通用 PR 分开。

### 原生与解析模式切换

- 仅非图片附件且当前模型具有原生能力时显示原生标签。只有解析渠道能够处理该文件时，才允许点击切换；已有解析结果立即切换，需要较长解析时明确确认，避免误触。
- 原生模式绕过本文件的 RAG；解析模式忽略原生能力，按现有 RAG 开关工作。重试遵从所选模式，切换模型重新计算能力，不支持时明确说明原因。
- 不增加多余 tooltip 或附件菜单。

### 原生工具计费

- 作为独立修复，保持“权限 → 模型 → 协议 → 工具 → 价格 → 成功调用 → 扣费”的完整链路，不任意放行自定义 Key。
- `image_generation` 合法参数、编辑场景的 `moderation` / `output_compression`、工具用量及服务档计费另行核实。

### 其他已讨论的附件与容量工作（后续）

- 各协议分别描述原件输入、视觉保真、解析/RAG 和工具容器处理能力。Office 原件接收不等于保留图表和版式；需要时评估明确转换流程，避免所有附件默认丢给 Shell。
- 核对上传分类上限覆盖默认上限的行为、MB/MiB 表达、单文件/请求合计/页数/时长限制，以及本轮与历史图片的总预算；可配置化和更大文件的 Files API 接入按独立工作单元推进。
- 统一原生附件切换、重试、历史文件、跨渠道能力变化与后台解析费用，保留用户主动选择；扩大原生容量不能绕过用户文件权限或存储配额。
- 上游同步以独立通用修复提交为主；Chat01 专属适配与上游 PR 分开。沿用专用标签/手动镜像发布和固定 digest，不因记录排期触发镜像构建。
