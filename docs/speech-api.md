# 语音服务契约

后端实现位于 `application/speech`、`domain/speech`、`infra/speech` 与 `transport/http/speech`。普通 HTTP 请求沿用本站 Bearer 身份认证与统一响应 `{errorMsg,errorCode,data}`。供应商凭据只在服务端配置；所有音频、识别会话与合成结果均绑定用户。

## 能力与录音

`GET /api/v1/speech/capabilities` 的 `data` 包含：

- `selected_input_provider`：管理员统一选择的 `browser`、`doubao-ime`、`wetype` 或 `deepseek`，默认 `browser`。
- `max_recording_duration_ms`：当前所选渠道的有效单段录音上限，浏览器识别也使用此值。
- `asr[]`：`id,name,streaming,formats,sample_rate,max_duration_ms,max_bytes`。仅返回当前所选渠道可用的能力；浏览器识别由端侧执行。
- `selected_tts_provider`：管理员选择的朗读渠道，升级默认保持 `deepseek`，支持 `mimo`。
- `tts[]`：仅返回当前已启用且配置有效的朗读渠道，字段包含 `id,name,streaming,formats,voices:[{id,name}],default_voice,max_weighted_characters,max_text_bytes`；支持速度与风格的渠道另提供 `speeds:[{id,name}],default_speed,supports_style,default_style,max_style_characters`。

每个 ASR 渠道的有效录音上限取本站上限与已确认上游硬上限的较小值。DeepSeek 完整文件 ASR 最长 120 秒、8 MiB；不提供原生流式识别。豆包、微信输入法的录音总长硬上限尚未由官方保证；当前仅声明本站上限，握手、收包与定稿超时不作为录音总长。

能力响应使用 `Cache-Control: no-store`。客户端每次开始录音先获取最新能力；服务器受理时再次校验所选渠道，切换竞争返回 `409 / speech.input_provider_changed`。已受理会话使用开始时的供应商、限额和计费快照完成。TTS 开关与输入来源独立。缺少明确的新 `input_provider` 时迁移为 `browser`，不从旧渠道开关推断；旧个人输入来源设置不再读取并会清理。

完整文件录音先 `POST /api/v1/speech/asr/sessions`，JSON `{provider:"deepseek",format:"wav",sample_rate:16000,draft_id}`；响应包含 `{id,provider,session_token,expires_at,max_duration_ms,max_bytes}`，`ws_url` 为空。上传凭据有效期为本段限额加 120 秒上传窗口。`POST /api/v1/speech/asr/transcriptions` 使用 multipart：`file`、`provider`、`request_id`、`session_id`、`session_token`。客户端可带 `draft_id` 供本地草稿定位，服务器不依靠其写入任何对话。接受 PCM16 WAV 或完整 Ogg Opus；时长由 WAV 样本数、Opus 包时间线与末页裁剪共同校验，不能仅由客户端传来的时钟或 Ogg granule 声明。响应为 `{session_id,text,duration_ms,sequence,final:true}`。同用户、供应商、`request_id` 重放复用结果；更换音频而沿用同一请求身份返回冲突。取消或离开时调用 `DELETE /api/v1/speech/asr/sessions/:id`，立即释放未使用预留；上游已受理后的取消保留结果待核对状态。兼容无会话的旧上传时，仅允许受理当时所选渠道，并在授权前占用请求身份，避免并发重放重复预留。

流式渠道使用：

1. `POST /api/v1/speech/asr/sessions`，JSON `{provider,format:"opus",sample_rate:16000,draft_id}`。
2. 响应 `{id,ws_url,max_duration_ms,max_bytes}`。`ws_url` 带 30 秒、单会话、用户绑定的随机票据；长期登录令牌不放入 URL。访问日志须忽略其 query。WebSocket Origin 使用站点配置的 CORS 白名单。
3. WebSocket 每个二进制消息是一个单声道、20 ms 的裸 Opus packet。不能发送 WebM/Ogg 容器碎片或任意长度 PCM。服务端检查 Opus TOC 的真实样本时长，并限制包大小、累计时长、累计字节与队列。
4. JSON `{type:"finish"}` 停止输入并等待定稿；`{type:"cancel"}` 或断线取消会话并回收连接。发送 finish 后连接保持到终态，以保留最后结果。

服务器事件为 `{type,session_id,sequence,text?,final,code?,message?}`。`type` 为 `ready`、`transcript`、`final`、`error`；`sequence` 在本段内单调递增。`transcript.text` 始终表示**本段完整临时快照**，不是要追加的增量；只有成功协议终态产生 `final`。无结果心跳、空文字与内容相同的 partial 不产生草稿更新，最终事件始终保留。豆包分句结果按分句索引修订，累计结果替换当前段。微信普通 `text` 提供录音期间的累计快照；结束确认及其后最多八次、间隔 250 ms 的轮询优先采用上游 `polished` 纠错，没有纠错则保留最新普通文本。录音中途的旧纠错不直接当成整段最终结果。真实上游错误仍然失败，不能因为已有 partial 就伪装成功。

豆包保留输入法上游自带的识别纠错与最小整理，不额外调用本站 AI 改写。无索引协议的 `results[0].text` 是整段累计快照，后面的结果是辅助分句候选；累计文本会包含已纠错的前句与当前新句，按最新累计快照替换本段。空首项、空结果或心跳不回落到某个旧句候选，也不清空文字。不能因为辅助旧句带 `extra.nonstream_result` 就丢弃整批：真实多停顿样本中，第二、第三句正与旧句纠错结果放在同一批。`stream_asr_finish` 是分句状态，接收循环继续处理后续语音；只有成功的 `SessionFinished` 才提交前端 final。带索引协议按索引修订分句，保留活动尾句。保持已实测的 `enable_asr_twopass:true` 和 `enable_asr_threepass:false`，不将这些参数包装成未经验证的整理强度开关。

录音到限时客户端先停止采集，完成编码器 flush 后发送 finish。WebCodecs 对 6000 个 20 ms 输入帧实测产生 6001 个 Opus 包，额外包可能包含编码延迟的末尾语音，不能直接丢弃；服务器允许最多 80 ms、四包的固定编码尾窗，并完整送上游，计量仍不超过所选录音时限。达到有效时限后等待 finish 最多一秒；尾窗用完或等待超时即正常结束上游输入并等待定稿。继续发送超过尾窗返回 `speech.duration_limit`，坏包、字节超限和真实上游失败不被正常到限逻辑吞掉。整会话墙钟兜底为录音上限加 20 秒，独立于正常到限定稿。

WS 输入队列 32 包、服务层转交队列 16 包、微信上传 batch 最多六个 Opus 帧；慢连接队列满时返回 `speech.backpressure`，客户端保留已显示文字。每用户及全站并发有上限，未连接票据到期释放预留；取消、服务关闭、超限和断线均关闭上游连接。流式 ASR 不留完整录音副本，所有 ASR 均不持久化输入音频。DeepSeek 接收完整上传，但 multipart 通过 header、原音频与 footer 的 `MultiReader` 保持 Content-Length，不再复制一份整段音频；3.84 MB 离线基准的附加分配约 38 KB/op，该数字不是进程 RSS 峰值。

## 朗读与复用

`POST /api/v1/speech/tts`，JSON：

```json
{"provider":"deepseek","text":"你好","voice":"stella","format":"wav","request_id":"stable-attempt-id"}
```

`voice` 为海星 `stella` 或暗潮 `tide`，HTTP 格式为 `wav` 或 `pcm`，默认 WAV。输入最多 10,000 个加权字符（汉字 2、其他字符 1），实际供应商 JSON 请求体不超过 64 KiB。输出限制为 24 kHz、16 位、单声道的最长 30 分钟音频。DeepSeek HTTP 接口接收整段正文并流式返回音频。

MiMo 示例：

```json
{"provider":"mimo","text":"你好，Hello。","voice":"冰糖","speed":"fast","format":"wav","request_id":"stable-mimo-attempt-id"}
```

音色与速度严格按渠道能力列表校验，省略时使用渠道默认值。可传 `style` 自定义朗读风格，省略继承默认，显式空字符串取消个人风格指令；最多 500 个 Unicode 字符。MiMo 本站单请求限制为 1,200 加权字符和 16 KiB 文本。上游采用 `/v1/chat/completions`、`model=mimo-v2.5-tts`、`stream=true`、`audio.format=pcm16`，正文放在 assistant 消息、风格和速度放在 user 消息；只发送本段文本和指令。解析 SSE `choices[].delta.audio.data` 为 24 kHz PCM16 单声道，转换为未知长度 WAV 头加逐块 PCM，未完整生成即可播放。适配器不缓存整段 Base64 或整段解码音频，单 SSE 事件最多 1 MiB。

只有成功 `finish_reason=stop` 且收到有效音频、正常结束的响应才能完成；截断、`length`、`content_filter`、错误事件与坏音频均不写成功缓存。上游响应 ID 与可选 `usage`（输入、输出、总 tokens）记录在语音操作元数据 `upstream_request_id` / `upstream_usage` 中。`usage=null` 保持缺失，不虚构为零；这些字段不代替站内加权字符计费。

响应 `{id,status,media_url,cache_hit}`。`GET /api/v1/speech/tts/:id` 查询同样结构；`GET /api/v1/speech/tts/:id/audio` 通过普通身份认证后，以固定 32 KiB 缓冲读取正在增长的音频文件，首块无需等待整段生成结束。PCM 返回 `audio/L16;rate=24000;channels=1`。WAV 允许 RIFF/data 长度为 `FFFFFFFF` 的流式头，播放端按 HTTP 音频流处理。音频流 EOF 后再查询状态，只有 `complete` 可以视为完整可复用结果。

状态为 `pending`、`complete`、`uncertain`、`expired`、`cancelled`：

- 缓存键包含用户、规范化文本、供应商、实际连接身份摘要、音色、速度、风格、格式与版本；有效完整缓存与并发进行中的结果合并。连接摘要由实际地址、Key、模型及指令版本得到，不在缓存键保存明文密钥；切换连接或朗读参数不误复用其他结果。
- `request_id` 表示生成尝试身份，必须非空且最多 128 字节。同一身份重放返回相同结果；同身份更换正文、音色、速度、风格或格式返回冲突。暂停、继续、重播只读取返回的结果 ID。
- 内容缓存键与收费操作 ID 独立。已确认成功的音频过期后，新 `request_id` 可以产生新的操作；旧请求重放仍返回 `expired`，不会静默重新收费。
- `uncertain` 表示上游已受理后失败、连接结果不明或结算需核对；不自动重试。明确用户重试需要新的 `request_id` 和 `retry:true`。旧请求仍指向原结果。
- 部分失败音频删除，不进入成功缓存。持久 pending 在进程重启后恢复为 uncertain，防止重启触发重复合成。

## 权限、计量与生命周期

`Authorizer.Authorize(Access)` 在发起上游前进行权限与预算预留，返回可序列化的 `BillingTicket`。Ticket 随持久记录保存，`Meter.Record(Usage)` 使用同一价格/权限快照结算：ASR 单位为毫秒，TTS 为加权字符；均不冒充模型 token。明确尚未执行可记为 cancelled；受理后结果不明使用 uncertain 保留核对状态。供应商秒粒度向上取整与币种转换由计费适配层处理。

`charge_asr`、`charge_tts` 默认关闭：仍记录实际语音用量，冻结的有效收费价格为零。开启后，所有服务器 ASR 渠道按 `asr_cny_per_hour` 折算秒粒度费用，TTS 按 `tts_cny_per_10000_characters` 计费；后续调价或开关变化不重算已有票据。DeepSeek 语音输入和朗读各自解析所选连接，允许复用另一项或使用独立地址与密钥。保存时校验有效连接和引用关系，缺少密钥定位到对应字段；空密钥保存保留旧值，显式清除正在使用的密钥需同时处理其消费方，不回退聊天供应商凭据。请求开始时解析为固定快照，配置变化不改变已受理操作。

持久音频库默认保留 6 小时、全站共享 256 MiB 配额；并发 writer 在每次写入时共同预留实际字节。容量不足先回收过期及未占用的旧完整音频，正在写入或读取的文件通过引用计数保护。目录启动时建立轻量索引，后续定期清理空闲缓存，音频块写入不扫描全部 JSON 文件。元数据与索引最多 10,000 条，已完成/已取消记录和索引按独立 30 天规则回收；待核对记录不静默删除，达到上限时拒绝新受理。仍有受保护音频时保留其记录，避免孤立文件。删除音频不会把旧请求 ID 当成新请求，缺少文件的完成记录表现为 expired。目录由操作系统独占锁保护，不能同时由两个进程管理相同文件库；关闭时停止清理任务并释放句柄和目录锁。

`Service.Close()` 停止接纳新调用，取消活动任务和流式会话，释放未使用会话预留，等待活动资源回收后关闭持久目录锁。录音身份按用户存储，音频连接按会话创建；设备身份被上游拒绝时保留拒绝，不更换身份绕过限制重试。

## 来源与验证

协议研究固定参考 [VoiceKey 0f559e35](https://github.com/J3n5en/voicekey/tree/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c)。输入法接入为独立 Go 协议适配，没有捆绑原 Rust 程序、其全局状态或无界重放队列。DeepSeek 格式与限制依据 [ASR](https://api-docs.deepseek.com/zh-cn/guides/asr/) 与 [TTS](https://api-docs.deepseek.com/zh-cn/guides/tts/)。

自动验证使用本地 HTTP/WS 模拟上游，覆盖 DeepSeek 首块流读与鉴权、豆包临时/分句/累计及会话成功、微信加密握手/批量 Opus/定稿纠错、票据单用与 Origin、finish 尾包、取消关闭、幂等与跨用户隔离、部分失败、缓存过期、共享配额、Windows 原子替换与重启恢复。模拟不能证明部署网络的实际可用性；本机另用合成音频完成 DeepSeek、豆包、微信真实通路验证，结果和限制见 [本批验证记录](deepseek-speech-validation-20261114.md)。
