# 浏览器语音交互

语音输入按用户选择使用浏览器识别、DeepSeek 完整 WAV 转写，或豆包/微信输入法的流式 Opus 通道。服务器能力目录只公布已启用渠道，其时长与大小限制约束本次录音。浏览器识别默认限制为 120 秒；其浏览器 API 不开放采样数据，时长使用单段累计时钟，不为显示波形再次申请麦克风。

自主管理的录音只申请一次麦克风，在 AudioWorklet 中连续重采样为 16 kHz 单声道，按实际样本停止。DeepSeek 使用 PCM16 WAV；流式通道使用 20 ms 裸 Opus 包。优先使用 WebCodecs，缺少该能力或原生初始化失败时才创建按需加载的 WASM 编码 Worker。采集与编码队列各最多缓存约一秒，网络待发送缓冲最多 256 KiB。停止先排空采样与编码尾包，再请求本段定稿；取消和切换会话立即释放资源并隔离迟到响应。

草稿中每次主动录音拥有独立文本范围，开始新段默认换行，内部修订只替换该范围。用户编辑之前的文字或之后的文字会保留；若编辑当前识别范围，编辑后的内容成为权威文本，后续自动修订不覆盖该编辑。取消只撤销仍由当前录音拥有的文字。录音中发送等待定稿，识别失败保留文字供编辑。

朗读默认关闭。普通新请求在流缓冲提交并确认助手消息成功之后发布幂等完成事件；恢复、历史读取和失败不会发布该事件。朗读消费当前会话事件，切会话或开始录音会停止播放。完整正文经过规范化后按句段顺序合成，使用累计字符权重线性分段，单次总预算为 10,000 计费字符和 64 KiB；超过预算明确提示，不截尾。合成按用户、供应商、格式、版本、音色和规范文本去重，重播重新读取相同合成 ID 的音频。未知传输失败保留请求身份，用户手动重试时以同一身份核对原尝试；明确失败、取消或结果不明仅在用户点击“重试朗读”后以新身份及显式重试标志发起新尝试，不自动重新扣费合成。音频过期或 GET 返回 410 时，手动播放至多以一个新身份补建一次，不循环重试。

TTS 使用鉴权 fetch 读取 WAV PCM 流，首次数据即可播放；最多提前排队约半秒音频，暂停停止读取，最终总播放时长不超过 30 分钟。媒体活动边界接收实际播放 Analyser 的音量，录音和播放共享导航反馈协议，不向消息列表逐帧写状态。

## 第三方编码器

`libopus-wasm` 固定为 `0.4.1`，安装包 189,913 bytes，解包 434,641 bytes。包完整性与 npm registry 的 SHA-512 校验一致：`sha512-dt2u6KyBe1IONzZA2Q9TXQbmLsZ6l1vLqgt4n9XC/sE70BegLgwC/hcbZp41aJgURAqalYdnPg0wGSI2IwHyvA==`。包装代码遵循 MIT，内置 libopus 1.6.1 遵循 BSD 风格许可。随 Web 静态文件分发 `public/licenses/libopus-wasm-LICENSE.txt` 和 `public/licenses/opus-COPYING.txt`。

来源：[libopus-wasm 编码接口](https://libopus-wasm.dev/encoding.html)、[项目仓库](https://github.com/openclaw/libopus-wasm)、[libopus 许可](https://github.com/xiph/opus/blob/v1.6.1/COPYING)。

验证：`node scripts/test-speech-input.mjs`、`node scripts/test-media-activity.mjs`，以及 `node node_modules/typescript/bin/tsc --noEmit --pretty false`。真实供应商请求和实机麦克风验收单独记录，不由本地模拟测试代替。

2026-11-14 补充浏览器验收：在独立 Chrome 临时会话挂载真实聊天输入组件，模拟浏览器识别和付费 API，并使用浏览器 fake media device 实际运行 `getUserMedia` 与 AudioWorklet。覆盖取消、partial 修订、停止尾句、多段换行、发送一次、转写等待期间编辑及换会话后迟到响应。生成请求正文直接接收本段定稿快照，避免父层 React 草稿闭包尚未提交而发送旧 partial。原生识别没有音量入口时不申请额外麦克风，显示“录音中”与静态波形。桌面、390px 手机和 820px 平板截图及无密钥结果记录保存在 `.cache/local-dev/speech-browser-qa/`；此验收没有调用付费接口。语音业务回归现为 18 项。
