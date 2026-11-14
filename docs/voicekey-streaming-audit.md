# VoiceKey 流式识别核验

2026-11-14 只读核验 [J3n5en/voicekey](https://github.com/J3n5en/voicekey)，固定提交 `0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c`（2026-11-13）。下文保留源码核验依据。后续开发已采用独立 Go 协议适配，并用 5.56 秒合成音频验证豆包、微信实时收发；实际结果与边界见 [本批验证记录](deepseek-speech-validation-20261114.md)，不能据此宣称任意网络与长时录音均已验证。

## 已确认的两条优先渠道

| 渠道 | 流式上传与返回 | 停录处理 |
| --- | --- | --- |
| 豆包输入法 | WSS/Protobuf，16 kHz 单声道音频按 20 ms 帧编码为 Opus 后持续发送；独立接收任务调用 partial 回调。支持分句定稿和累计全文两种返回结构 | 发送 LAST 与 FinishSession，等待 SessionFinished；中间文字与最终会话成功必须分别判断 |
| 微信输入法 / WeType | WSS 上封装分块请求/响应，约 6 个 20 ms Opus 帧组成一批，串行等待本批响应；每批可返回 text 并调用 partial 回调，因此能边说边出字 | 发送 is_end，并有限轮询 polished 结果；普通 text 和 polished 的修订不能重复追加 |

依据：[公共 Engine](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/lib.rs#L22-L31)、[豆包收发及定稿](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/doubao.rs#L170-L245)、[微信分批上传及中间结果](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/wetype.rs#L56-L132)。20/120 ms 是音频帧/批次长度，不是实测的出字延迟保证。

## 其他云端渠道

同一提交的以下实现均在录音期间上传并回调中间结果，未发现其中某一路必须等完整录音后才开始上传。此表只描述源码行为，不承诺上游长期提供或当前身份能连通；离线模型未在本次核验范围内。

| 渠道 | 实现与登录依赖 | 源码 |
| --- | --- | --- |
| 千问 | WebSocket 分块 PCM，partial 返回；使用本机设备身份，未见用户登录前提 | [qwen.rs](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/qwen.rs#L63-L99) |
| 百度 | /up 持续上传 Opus，/down 独立接收结果；未见用户登录前提 | [baidu.rs](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/baidu.rs#L149-L230) |
| 搜狗 | 录音期间分批 HTTP POST，逐批返回中间文本；未见用户登录前提 | [sogou.rs](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/sogou.rs#L72-L115) |
| 讯飞 | WebSocket 分块 PCM 与 partial；项目通过签名参数连接，未见用户登录前提 | [ifly.rs](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/ifly.rs#L108-L151) |
| Chatterfly | WebSocket 逐帧发送与 partial；可选 token | [chatterfly.rs](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/chatterfly.rs#L134-L199) |
| 千问 App | WebSocket 与 intermediate result；需要千问登录 Cookie 换取临时 token | [qwenapp.rs](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/qwenapp.rs#L95-L150) |
| 叭哥说 | WebSocket 与 transcript.partial；需要有道登录 Cookie | [bageshuo.rs](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/bageshuo.rs#L143-L173) |

## 对本站方案的影响

- 这些是项目分析输入法客户端协议实现的渠道，与火山引擎、腾讯云等公开商业 ASR API 分开建模。豆包、微信核心引擎由代码直接连接上游，初始化并缓存设备身份，不依赖本机安装/启动对应输入法程序；本站开发环境已用短合成音频完成身份与连接验证。见 [项目说明](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/README.md)、[豆包设备初始化](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/doubao.rs#L348-L419)、[微信身份与握手](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/wetype.rs#L144-L239)。
- 不把桌面全局设备文件、无界音频 channel/录音留底直接搬进多用户服务。复用本站用户权限和会话身份，限制每会话音频队列和总并发；慢网、断线、取消及后台切换必须释放资源，并明确保留的未定稿文字状态。
- 统一输出本次录音段的完整临时快照与最终结果。用户此前草稿独立保留，当前段修订只替换当前段；连续口述、最终纠错、停止尾包和迟到结果均不得整框覆盖或重复追加。
- 本次代码未证实两家服务的录音总时长硬上限。豆包接收等待 600 秒、微信单次收包等待 15 秒属于实现超时；不能用于宣称录音限额。本站设置的单段上限和剩余 10 秒提醒仍适用，渠道硬限制需独立核验。
- 追加读取桌面调度器与设置：`settings.rs` 默认静音自动结束为 1.5 秒，可设 1–5 秒；`controller.rs:951-963` 对一直未开口使用 8 秒门槛，`974-1001` 在停录后才开始 15 秒识别完成等待。这些都不是连续讲话的录音总时长上限，当前所查路径未发现总长常量。见 [调度器](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/app/src-tauri/src/controller.rs#L951-L1001)、[设置](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/app/src-tauri/src/settings.rs#L205)。
- 补查其余七个云端引擎的时限：千问/讯飞有停录后定稿等待，百度有上传/接收任务结束等待，千问 App 有预取 token 缓存 TTL，叭哥说有鉴权/握手等待，Chatterfly 有随出字重置的 30 秒空闲等待。所查实现未给出可作为供应商保证的固定录音总长；token 有效期、空闲超时和定稿等待均不能替代该限制。
- 本站已采用独立 Go 协议实现，未引入 Rust 服务或客户端模型依赖；前端 Opus 编码优先 WebCodecs，不支持时按需加载 WASM。TTS 首批接入 DeepSeek。

## 标点与识别纠错参数复核

- 豆包的 StartSession 明确发送 `enable_punctuation:true`，同时设置 `enable_asr_twopass:true`、`enable_asr_threepass:false` 和 `input_mode:"stream"`；这些是参考实现固定发送的参数，不能把二遍开关解释为已验证的“关闭智能整理”。本站按用户选择保留上游自带的识别纠错，不额外调用 AI 改写，也不暴露未经验证的整理强度开关。见 [豆包请求参数](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/doubao.rs#L169-L181)。
- 无索引豆包返回的首候选是整段累计快照，后续项是辅助候选，参考实现也明确按此处理。2026-11-14 的三段合成音频（共 24.68 秒，间隔 4 秒静音）复现了旧过滤规则的问题：第一句纠错后，每批第二、第三句结果都夹带旧句的 `nonstream_result`，整批过滤会让文字冻结。现改为采用首候选累计快照，保留上游纠错，空首项不采用辅助旧句；同样本最终累计结果由修前 25 字恢复至 61 字。分句 `stream_asr_finish` 不结束会话，只有 `SessionFinished` 提交 final。见 [累计快照与分句逻辑](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/doubao.rs#L78-L104)。
- 微信请求固定将 protobuf 字段 22、23、24 设为 1，但所查源码没有为它们提供足以确认“标点”或“禁用整理”的语义说明；不能按编号猜测产品开关。返回普通文本字段 4 与 polished 字段 14 分开，VoiceKey 默认优先 polished。本站保留上游纠错，但只把结束确认或结束后轮询得到的纠错作为 final，避免复用录音中途某句的旧纠错；最多八次轮询仍无纠错则保留最新普通累计文本，真实协议/网络错误不伪装成功。见 [微信请求字段](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/wetype.rs#L225-L251)、[返回文本选择](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/wetype.rs#L113-L124)。
- 桌面设置里的静音、流式和实时文字选项控制录音或显示；Jev 是另一个默认关闭的多候选选择器，均不构成两家输入法可验证的自动标点/整理开关。见 [桌面设置](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/app/src-tauri/src/settings.rs#L97-L135)、[Jev 默认配置](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/jev.rs#L6-L20)。

## 当前作者版本复核与本站边界

2026-11-14 联网核验作者仓库 main，HEAD 为 [`138d675a`](https://github.com/J3n5en/voicekey/commit/138d675a9a7f55fcf029fe4919856aa856687cc7)。该版本豆包、微信两个源文件与此前固定 `0f559e35` 逐字节相同，本轮没有发现新的协议版本差异。

| 行为 | 作者实现 | 本站处理与依据 |
| --- | --- | --- |
| 豆包停顿与候选 | 无索引首项为整段累计，其余为辅助；SessionFinished 结束 | 保留已修累计语义与上游纠错，空首项不回退旧句。60.84 秒、13 次 1.25 倍合成重复且末尾三秒静音，累计首项更新 200 次、最终 325 字；讲话期间相邻更新最长约 1.137 秒。未复现用户连续快说后卡住，不能据此断定更长/真人样本的上游节奏。 |
| 微信停顿与纠错 | 六帧 batch；普通文本供 partial；最终优先 polished，最多八次轮询 | 首次有效 24.68 秒三段/四秒停顿样本，普通文本在第二、第三段继续变化，结束普通/纠错分别 77/58 字；样本包含重复与编辑指令，不能用长度差断定字段丢句。另以新 UUID、本机 SAPI 合成三句不同事实，18.12 秒含两次四秒停顿；154 次请求，END 普通/纠错均为完整 36 字、三个事实全部保留。本站取消旧的普通文本限定，结束纠错优先，并防止旧纠错成为 final。 |
| 总时长与尾包 | 所查两适配没有可保证的供应商总长常量 | 本站有效录音时限保持管理员配置；额外最多 80 ms 仅用于编码 flush，送上游但不增加计量。客户端 finish 或有限尾窗结束后定稿，越窗明确报错。 |
| 会话与错误 | 桌面全局身份/连接复用；豆包有拒绝后换身份重放，已有 partial 时部分错误返回已有字 | 本站用户身份隔离、每会话连接、有界队列，不换身份绕过拒绝，不持久化输入音频，真实失败保留 partial 并报告错误；取消通过 context 主动关闭 WS，打断正在等待的读取。 |

本轮仅使用已有合成录音和本机离线语音合成，未调用 DeepSeek 付费语音、未改变当前用户输入来源。两家请求中的未知参数保持已验证固定值，没有新增本站 AI 整理调用或推测性的整理强度开关。

每次新录音必须使用独立会话 UUID，用户身份可复用而 voice_id 不复用。诊断中重复使用同一测试 voice_id 后换音频，曾收到旧样本识别结果；这类复测已作废，未用于判断纠错字段的整段/分句语义。生产 `CreateSession` 使用新 UUID，并有同用户、同草稿两次录音的供应商及计费身份隔离回归。
