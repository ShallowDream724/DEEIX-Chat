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

建议隔离识别已知私有标记：有足够数据才转换成项目组件；缺数据时保留有用名称、普通链接和表格，并明确保留该组件的可见入口/缺失状态及原始数据，不无声删除。不为不存在的坐标或图片补造数据。用户明确 HTML 代码块不等于官网交互组件；未取得完整官网组件协议时不开展通用组件仿制。

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

以上为第一批响应探测；附件与 Responses 随后按用户要求补测，见下一节。历史附件跨轮行为、实际总附件上限、连续断流恢复、自然失效 CDN 链接、长任务、不同渠道账户的格式差异仍待验证。

## 追加附件传输验证

同日追加 3 次 `gpt-6-thinking` 请求，按用户报价再用 6 分；两批共 9 次、按单价计 16 分，仍未调用 Pro。所有请求 HTTP 200。没有自动重试付费调用。第二批文件位于本地忽略目录 `attachments/`，以预先保存的 SHA-256 与随机标识为校验基准，未把预期哈希写进模型提示。

| 编号 | 请求方式 | 附件 | 耗时 | 验证 |
| --- | --- | --- | ---: | --- |
| 07 | Chat Completions，Base64 data URI | DOCX、PPTX、PDF、WAV、ZIP、JPEG | 221.1 秒 | 上游执行读取并打包回传；下载 ZIP 后，六份原件与本地逐字节一致 |
| 08 | Chat Completions，HTTPS URL | DOCX、PPTX、ZIP、PNG | 164.0 秒 | 下载上游计算的 manifest，四份文件的字节数和 SHA-256 与本地全部匹配 |
| 09 | Responses，`input_file.file_url` HTTPS URL | DOCX | 36.0 秒 | 返回大小、SHA-256、文档随机标识与本地全部匹配，收到 `response.completed` |

07 使用图片 `image_url: {url: "data:image/jpeg;base64,…"}`，其他类型使用 `file_url: {url: "data:<实际 MIME>;base64,…"}`，不附加未经确认的 filename 参数。总请求 JSON 为 235,087 字节，六份源文件合计 175,301 字节。DOCX/PPTX/PDF/ZIP 含独立随机标识；WAV 是真实半秒音频文件；JPEG 使用用户此前提供的 Chat01 logo。上游正确描述了橙色背景、白色圆形及“01”。验证的是原件运输与可读取性，不是音频转录或所有二进制格式的语义能力。

08 同样使用图片 `image_url`、其他文件 `file_url`，URL 来自此前实际生成并下载核验过的文件。未向 CDN 传递 API 密钥。

09 使用标准形态：

```json
{
  "model": "gpt-6-thinking",
  "stream": true,
  "input": [{"role": "user", "content": [
    {"type": "input_text", "text": "读取附件并计算实际大小、SHA-256及文档标识"},
    {"type": "input_file", "file_url": "https://files.chat01.ai/python-generations/<opaque>/<file>.docx"}
  ]}]
}
```

Responses 确实返回标准事件封装，包括 created、in_progress、output_text.delta、output_item.done 和 completed；但混装内容仍在 output_text 内，并未因此获得完整结构化工具记录或 usage。同一 output_index=0/content_index=0 的 `response.output_text.done` 连续发了两次，内容 1132 字符且哈希相同，不能将快照重复追加。期间收到 `: heartbeat` 注释事件（约 15、30 秒）。因此 Responses 可用，初版仍可优先选择附件覆盖更完整的 Chat Completions；若保留两入口，必须共用 Chat01 规范化核心。

附件文件名与内容有两项细节：Base64 的非图片原件被挂载成随机 `.bin`；URL 的 DOCX/PPTX 保留扩展名但改为随机 basename。URL 输入的 PNG 被挂载成 `.jpg` 名字，实际 PNG 字节及哈希未变。应保留自己的原名并依真实 MIME/内容处理，不能以挂载名决定内容格式。

结论：该特殊链路已经证明可以传递完整 DOCX/PPTX 等原件，不需要先转为解析文本。默认 Base64 不依赖官网上传接口或站点对外开放文件 URL。视频、大文件、中文/重复文件名、更多任意类型、附件数量边界与 Responses 的 Base64/多附件形态尚未测试，不从小文件成功推导无限制。

## 文件名与 DOCX → PPT 追加验证

用户提出 Base64 是否占满上下文、`.bin` 是否影响自然任务，并明确不希望向上游提供站点主域名。本次不以 OpenAI 官方行为推定 Chat01，追加两次请求、按最多 4 分预留；其中一次在 HTTP 参数校验阶段被拒绝，实际是否扣费 API 未返回，不能宣称已扣满 4 分。累计发出 11 次模型请求，其中 10 次 HTTP 200；按用户单价计算成功请求为 18 分，全部请求按单价保守计入为 20 分。未调用 Pro。

构造一份可正常打开的 DOCX：有三段业务内容、随机项目标识和用户提供的 logo 配图；另添加含 512 KiB 随机数据的有效 custom XML 部件，使文档为 828,898 字节，Base64 请求约 1.1 MB。预期哈希、随机标识及图片哈希未写入提示词。任务为“根据附件制作恰好三页 PPT，保留关键信息与配图，附实际源文件信息清单”，没有提示模型文件会改成 `.bin`。

| 编号 | 请求组合 | 结果 |
| --- | --- | --- |
| 10 | Responses `input_file.filename` + `file_data` Base64 | 0.8 秒返回 HTTP 400，`invalid_input`；仅能判定这次组合被拒绝，不能定位具体字段或宣称所有 Base64 形态不支持 |
| 11 | Chat Completions `file_url.url` Base64 + `file_url.filename` | 约 253.4 秒完成，HTTP 200；额外 filename 未保住名字，上游仍挂载成随机 `.bin` |

样本 11 的实际交付通过本地检查：

- 源文件大小及 SHA-256 与本地完全一致，交付清单确认源文件仍叫 `.bin`。
- 生成 PPTX 可读取，恰好三页，包含目标、预算、验收要求及随机项目标识。
- 预算 27,400 元及其 12,600/8,800/6,000 元构成、6 周、18 组、450 毫秒、1.5% 均保留。
- PPTX 内原配图与 DOCX 原配图的 SHA-256 一致。未进行像素排版验收，本次关注附件读取和任务内容。

这证明 `.bin` 没有损坏该原件，并且这个真实 DOCX → PPT 任务可用；不能推广为所有格式的自动识别/预处理都不受扩展名影响。约 1.1 MB 的 data URI 成功作为文件使用，支持附件解码而非普通提示词直塞的解释；没有 usage 或准确的 Chat01 输入计数，不能据此证明零上下文占用或量化节省多少 token。也未使用其他厂商的 token 规则替代测量。

HTTPS 方案指 URL 放在 `file_url.url` / `input_file.file_url` 附件字段，由 Chat01 服务端取文件，不是放进提示词让模型自行浏览。但该服务端必然能看到 URL 域名。文件名方案尚未确认前不强定 Base64 为默认；也不因 `.bin` 就自动开放站点私有内容地址。项目现有内容路由需要用户认证，未提供短期 presign 接口；独立存储地址、限时授权或 Chat01 直接二进制上传能力均需单独设计/验证。

原始结果在 `.cache/chat01-probes/20261104/filename-probe/`，核验文件为 `pptx-verification.json`。未开放本站文件内容路由、未使用站点域名；生成产物来自 Chat01 返回的 CDN 地址，抓包和二进制文件仅本地留存，未提交 Git。没有实施生产改动。
