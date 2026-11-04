# Chat01 API 实测与适配边界

日期：2026-11-04。当前应用基线：已发布 `25c5cdd8`。本次仅探测与记录，未修改生产适配器、未构建镜像。

## 范围与留存

- 对 `https://chat01.ai/v1/chat/completions` 发起 6 次请求：`gpt-6` 两次、`gpt-6-thinking` 四次；5 次流式、1 次非流式。按用户给出的 1 分/2 分单价合计 10 分，未调用 Pro，没有自动重试付费请求。
- `/v1/models` 实际列出 `gpt-5-6`、`gpt-5-6-thinking`、`gpt-5-6-pro`、`gpt-6`、`gpt-6-thinking`、`gpt-6-pro`。密钥有效，未写入仓库或抓包文件。
- 原始请求、响应体、SSE 事件、客户端读取时间、提取文本及下载验证放在被 Git 忽略的 `.cache/chat01-probes/20261104/`。`probe.py`、`check_assets.py`、`analysis.json`、`assets/index.json` 与 `asset-verification.json` 可供本地复查；原始 CDN 地址不复制进公开文档。
- 响应体总计 73,018 字节；资产读取累计 8,569,269 字节。直接联网，未使用代理。资产检查曾主动停止一个 ZIP 的读取，随后完整取回并验证；另一个重复图片地址只确认 HTTP 200，未完整下载。主动截停不能统计成上游下载失败。
- 所有耗时是客户端观测，包含网络及排队，不是供应商内部执行耗时。此处只核验协议与文件产物，不把回答里的旅游信息或版本信息当成已经独立核实的事实。

## 实测矩阵

| 编号 | 模型 / 模式 | 任务 | 耗时 | 观测结果 |
| --- | --- | --- | ---: | --- |
| 01 | gpt-6 / 流式 | 原生生图 | 35.8 秒 | PNG 的 Markdown 链接先于 `## Answer:`；取回 1254×1254 图片，约 1.62 MB |
| 02 | thinking / 流式 | 搜索 Python 官网、执行计算、CSV/JSON/ZIP | 86.5 秒 | Answer→think→Answer；搜索摘要和命令混在 content；3 个文件有效 |
| 03 | thinking / 流式 | 同轮搜索、生图、Python、TXT/ZIP | 211.7 秒 | 4 次 Answer、3 段 think；图片插在中间；Python 代码围栏未闭合；图片、TXT、ZIP 有效 |
| 04 | gpt-6 / 流式 | 五子棋交互应用和离线 HTML | 47.0 秒 | 普通 gpt-6 也返回 think；正文包含 html 代码块，并有独立 HTML 下载 |
| 05 | thinking / 流式 | 地图、地点卡片、实景图片和来源表格 | 73.7 秒 | 返回私有 genui 地图标记、Markdown 表格和链接；没有可解析的地点坐标载荷 |
| 06 | thinking / 非流式 | Python 生成指定内容 TXT 和 ZIP | 39.4 秒 | 最终 JSON 前有换行心跳；`message.content` 内仍是 think+Answer；2 个文件有效 |

6 次均 HTTP 200、`finish_reason: stop`；5 次流式均收到 `[DONE]`。非流式是普通 `chat.completion` JSON，不应要求 `[DONE]`。

完整下载并检查了 10 个文件：两张 PNG、CSV、JSON、两个 TXT、三个 ZIP、一个 HTML。CSV 有 100 行且每个平方正确，总和为 338350；三个 ZIP 的 CRC 和解压内容均与单独下载文件一致；`hello.txt` 为精确 UTF-8 `123测试`，9 字节。HTML 验证为真实文件并含脚本，未执行其中代码或验收游戏行为。本次未复现自然发生的失效链接，不能据小样本估计失效率或保留时长。

## 真实响应的关键差异

### 内容、思考与工具没有标准分栏

5 个流式样本的有用内容均在 `choices[].delta.content`；非流式在 `choices[].message.content`。均未收到标准 `reasoning_content`、`tool_calls` 或结构化工具结果，也没有 `usage`。非流式带 `message.role`，不能将它误记成 delta。

可见标记包括 `<think>`、`</think>`、`## Thought:`、`## Python:`、`## Answer:`、`Worked for …`、`**Used … skill**`、`**Searched 4 websites**`。这些都是正文字符串，不能当成 OpenAI 标准工具协议。

`## Answer:` 也会标记中间进展，并非唯一最终回答边界。混合样本有 4 次 Answer，生图样本的图片还位于第一次 Answer 前。只取第一次 Answer 后内容会混入过程，只取最后一次又可能丢图片与有用进展。

本次有 think 的 5 个样本标签均闭合，但样本 03 的 ` ```python ` 没有结束围栏，后面直接出现 `</think>` 和 `## Answer:`。样本 02 的命令片段以 `data && python …` 开始，缺乏完整的结构化命令参数和独立执行输出。因此不能保证展示的代码完整，也不能把这些片段直接作为本地可执行工具调用。

### 搜索与引用

样本 02 有 `Searched 4 websites` 摘要，但没有实际查询字符串、4 个搜索结果对象或调用 ID。最终回答主要使用普通 Markdown 来源链接，部分含 `utm_source=chatgpt.com`，同一地址可能又以来源括号重复出现。

可复用现有 Markdown/来源展示，并在渠道层对确定的来源做规范化、去重；不能从“搜索了 4 个网站”推导 4 次可计费搜索，也不能补造查询词。下载链接、图片链接、来源链接必须分别识别。

### 生成图片和文件

观测到两种地址形态：

```text
https://files.chat01.ai/user-<opaque>/<image>.png
https://files.chat01.ai/python-generations/<opaque>/<filename>
```

下载不需要 API Authorization。本次生成图片返回 `image/png`；Python 产物（包括 PNG）多返回 `application/octet-stream`，不能只看 Content-Type 就拒绝可预览图片或信任文件类型，应复用现有文件检测。

混合样本同时给出原生生图 CDN 地址、复制到代码环境后的 PNG 和包含该 PNG 的 ZIP。文件身份不能只按文件名去重；同一内容不同 URL 是否复用，应走已下载内容及用户权限下的现有去重规则。

图像生成没有独立的 image_generation 调用对象或可靠 revised_prompt 字段，不能将用户需求自动冒称为工具实际提示词。

### 交互组件确实可能泄漏私有格式

五子棋返回了可复用现有 Artifact 的 html 围栏，另有独立 HTML 文件。这证明可拿到交互源码，不等于拿到了官网的结构化 UI 协议，也不意味着应用当前会自动打开它。

地图样本则包含以下形态的原始字符串：

```text
genui{"map_widget_v2":{"points":[{"ref":"turn…business1"}],"carousel":{"cards":{"turn…business1":{"title":"…","label":"…"}}},"title":"…"}}
```

完整响应中没有与这些 business ref 配套的经纬度或地点数据表，不能直接还原地图。实景图部分出现了图片说明和照片来源网页链接，但没有 Markdown 图片地址或图片数组，不能认定图像本体也传到了 API。

建议隔离识别已知私有标记：有足够数据才转换成项目组件；缺数据时保留有用名称、普通链接和表格，未支持的原始块留在诊断记录，避免正文直接出现私有控制符。不为不存在的坐标或图片补造数据。

### 心跳、结束与标识

- 5 次流式共见 52 个空字符串 content 事件。混合任务持续约 3 分 32 秒，事件间最大观测间隔约 15 秒。空内容仍是有效连接活动，不应显示为空消息或当成完成。
- 各流式 chunk 的 `id` 会变化；样本 03 有 130 个 JSON chunk 和 130 个不同 ID。应按本地请求/run 归属管理，不把每个 id 当作新对话或新工具调用。
- `created` 为约 13 位的毫秒值，不能按标准秒值直接格式化。
- 非流式响应头是 `application/json`，正文以三个换行开头，之后才是完整 JSON。请求仍可能保持传输活动，不能把“已有响应头/首个字节”当作完成。
- 当前 DEEIX 上游默认首字节等待 120 秒、流式空闲 60 秒，来自 `backend/internal/infra/llm/client.go`；渠道可覆盖。底层读到心跳字节应续活，不能只以可见文字续活。向浏览器的生成流另有 20 秒 keepalive（`handler_message_send.go`）。
- 未调用 Pro、未测试 40–60 分钟场景、未复现最终一次性思考输出。不能据本次短请求宣称 Pro 全链路已通过；总运行时长、上游空闲、浏览器连接、反代超时需分别验证。

## 后续实现边界

1. **独立 Chat01 渠道适配器**：沿用既有 OpenAI 传输，增加有界增量内容解码；按请求生成稳定内部标识。保留原始诊断，处理分片标记、重复 Answer、EOF 未闭合和代码围栏不完整；不在通用 OpenAI parser 上全局增加文本猜测。
2. **正文、思考、执行过程分别规范化**：最终对接现有 `GenerateOutput`、`ReasoningOutput`、只读工具 trace 与图片/文件结构。过程证据不足的内容以执行记录展示，不虚构参数、stdout、exit code、成功状态或工具调用计费次数，更不能把服务端已执行片段发给本地工具再执行。
3. **图片和文件复用统一产物链路**：现有 `OpenGeneratedFile` 只读 OpenAI 容器，`saveAssistantGeneratedFiles` 只改写 sandbox 链接，不能直接用 CDN URL 替换容器 ID。需在来源适配边界增加 Chat01 文件读取，复用用户权限、存储政策、类型检查与卡片。
4. **单文件失败与回答成功分开**：建议成功文件正常保存、失败文件记录可见状态并保留可尝试的原链接，不让一个 CDN 文件失败清空整段正文或其他已成功文件。当前生成文件保存采用整批回滚，实施时需明确新增的逐文件结果边界；不能仅靠前端换链接假装已保存。外链模式与本地保存模式仍需产品确认。
5. **引用、Artifact 与私有块**：标准链接、表格和 html 围栏尽量复用现有组件；私有 genui 只做已验证转换，缺失数据不硬转。HTML 文件仍走隔离预览，不同源执行。
6. **计费**：本批没有 token usage，调用单价依据用户提供。不能将 Chat01 的摘要文字套用 OpenAI 原生工具价格；额度、身份、成功与失败计量需要渠道自己的明确规则。

仍待验证：历史附件 `file_url`/图片 `image_url`、实际总附件上限、跨轮上下文、连续断流恢复、自然失效 CDN 链接、长任务、不同渠道账户的格式差异。此次没有验证 Responses API；能力结论限于上述 Chat Completions 实测样本。
