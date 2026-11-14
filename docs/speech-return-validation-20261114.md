# 手机后台朗读恢复验收（2026-11-14）

## 问题与行为

用户报告手机切后台再回来，朗读及文字流可能卡顿；部分操作可正常续读。审查确认原音频等待只由手动继续唤醒，未监听系统恢复，且非运行状态仍有分析定时器及媒体活动。语音和聊天上游 HTTP 客户端独立，没有证据表明 TTS 占用了聊天上游连接池。不能把上述前端缺口直接等同所有手机、网络卡顿的唯一原因。

新的播放生命周期集中管理音频状态、分析、事件监听、音源、reader 和取消信号。系统 interrupted/suspended 使用事件等待，running 自动唤醒；返回可见时仅尝试一次系统恢复，保持音频游标；手动暂停不自动继续。后台可正常播放的音频保持播放，仅停止不可见分析。pagehide/刷新立即清理，刷新后需要用户重新点击，正常有效缓存继续复用。合成/状态请求中断仍复用原 request ID 核对结果，避免重复生成。

文字缓冲在隐藏期间取消渲染计划并保留 delta；返回前台一次补齐已收到正文/思考，之后恢复正常节流。列表保留有序编号，无序列表只在朗读内容中自动编号；桌面和手机的风格恢复按钮同排右对齐。

## 集成证据

- 独立 Chrome 运行真实 WebAudio 和本地鉴权音频接口，对已有 30.56 秒 MiMo 缓存模拟页面隐藏、系统 suspend/interrupted、返回。暂停期间停止排队与分析，返回后原位置继续，累计播放样本与原音频完全相同，无重复合成。
- 同时处理 80 个模拟文字增量，最大间隔约 29.1 ms；这是浏览器内并发回归，不是生产模型网络延迟测量。
- 刷新后没有自动播放；再次点击返回相同 ID `d099afe8-85c0-493e-943b-83f1182b0eec`、`cache_hit=true`。pagehide 后 AudioContext 创建/关闭计数一致。全部使用已有音频，不产生新的上游合成；临时本地配置已恢复。
- 1440px、390px 浏览器验证恢复按钮与风格标签中心高度差小于 2px、按钮右边缘与文本框对齐，无横向溢出；保存和恢复默认仍正常。
- 67 项语音/列表回归和 6 项文字流可见性回归通过；覆盖手动暂停、系统恢复、暂停后立即继续及迟到 Promise、pagehide 资源释放、取消请求身份复用、积压一次补齐、后续增量和旧缓冲隔离。TypeScript、相关 Biome、生产构建通过。

详细本地记录：`.cache/local-dev/mimo-review/return-integration.json`、`settings-alignment.log`。真实手机的操作系统音频回收、网络切换与生产站点代理仍需部署后复测，不以桌面状态模拟宣称覆盖全部移动浏览器。

行为依据：[AudioContext 状态](https://developer.mozilla.org/en-US/docs/Web/API/BaseAudioContext/state)、[Page Visibility](https://developer.mozilla.org/en-US/docs/Web/API/Page_Visibility_API)。

## 发布结果

已发布 `ghcr.io/shallowdream724/deeix-chat:latest`（linux/amd64），源码 `d1826b7dce3d160ccf813a6001f7dea29cbc34eb`。[Image #43598044292](https://github.com/ShallowDream724/DEEIX-Chat/actions/runs/43598044292) 的构建、已发布架构容器检查和 Merge 全部成功。匿名 GHCR 核验 latest 与固定 SHA 标签一致，镜像 revision 正确；manifest digest 为 `sha256:974eb587208d295640f8db135ee1bf3bd5fa343963df3d5f0cd866d89836448f`。未代为更新生产容器。
