# VoiceKey 流式识别核验

2026-11-14 只读核验 [J3n5en/voicekey](https://github.com/J3n5en/voicekey)，固定提交 `0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c`（2026-11-13）。未发送真实录音、未执行 ASR、未下载客户端或离线模型；结论证明源码支持，不代表本站服务器网络上的实际可用性或延迟。

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

- 这些是项目分析输入法客户端协议实现的渠道，与火山引擎、腾讯云等公开商业 ASR API 分开建模。豆包、微信核心引擎由代码直接连接上游，初始化并缓存设备身份，不依赖本机安装/启动对应输入法程序；实际签发身份与连接是否成功仍需短录音验证。见 [项目说明](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/README.md)、[豆包设备初始化](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/doubao.rs#L348-L419)、[微信身份与握手](https://github.com/J3n5en/voicekey/blob/0f559e35a2efd8cca9023dfd7d4e1c41cbb4448c/crates/core/src/wetype.rs#L144-L239)。
- 不把桌面全局设备文件、无界音频 channel/录音留底直接搬进多用户服务。复用本站用户权限和会话身份，限制每会话音频队列和总并发；慢网、断线、取消及后台切换必须释放资源，并明确保留的未定稿文字状态。
- 统一输出本次录音段的完整临时快照与最终结果。用户此前草稿独立保留，当前段修订只替换当前段；连续口述、最终纠错、停止尾包和迟到结果均不得整框覆盖或重复追加。
- 本次代码未证实两家服务的录音总时长硬上限。豆包接收等待 600 秒、微信单次收包等待 15 秒属于实现超时；不能用于宣称录音限额。本站设置的单段上限和剩余 10 秒提醒仍适用，渠道硬限制需独立核验。
- 这一来源用于完善接入设计；尚未选定直接依赖、代码移植或独立协议实现方式，未引入 Rust 服务或客户端模型依赖。TTS 继续按已确认的 DeepSeek 首批方案推进。
