# 切页后误报生成失败与标题缺失：只读排查

日期：2026-11-04。基线为已发布 `25c5cdd8`。用户描述：发送多张图片后离开聊天页或切到管理员页面，先看到超时/失败提示，后端稍后仍生成成功；刷新或自动回填能看到结果，部分对话没有正式标题。本次没有改代码、没有复现生产请求，以下区分源码事实与待确认原因。

## 已确认的前端状态问题

- `apps/web/shared/api/authed-client.ts:63` 与 `shared/api/conversation.ts:1342` 直接使用 fetch/reader.read，没有聊天正文流的通用前端 idle watchdog。未找到精确的静态文案“对话超时”；实际文字可能由错误详情带入。
- `use-chat-message-submit.ts:696` 的 catch 对非主动取消、非审核阻断异常统一执行 `failPendingExchange`，并可能恢复草稿和弹出“发送失败”。`message-submit-exchange.ts:186` 将乐观消息标为 error。这包含流连接异常，没有先确认后台任务已经失败。
- 同一提交函数 `:769` 的 finally 在没有权威终态时又调用 `onConversationRunDetached`。这说明“流结果尚不明确”与“任务失败”在界面状态上混在一起，是可以直接从代码确认的问题。
- `use-chat-data.ts:277` 会对后台 pending run 通过 runID/afterSeq 恢复流；恢复完成或异常后 reload，没有可用流时每 1.5 秒轮询 pending 消息。它与用户“后面又回来了”的体验相容。
- `ChatSessionProvider` 位于整个 project layout，覆盖 chat 和 admin；正文提交 promise 属于聊天 runtime，未找到相应 unmount abort 清理，旧 promise 结束时仍可能触发全局 toast。此因果路径待浏览器复现。
- `use-chat-run-state.ts:183` 在后台页停止的是活动 run 列表事件流，恢复可见时重连；不等于取消后台生成。正文渲染缓冲使用 RAF，也不能据此断言后台节流就是超时根因。

最小修复方向：保留 HTTP 拒绝、服务端明确终态、传输中断三类错误；对结果未知的已受理任务走现有状态查询和续流，显示恢复状态，不立即鼓励用户重发。尚未确认请求受理时使用有限查询及现有请求幂等信息判定。站内切页的旧 promise 不应继续弹出误导性失败提示。

## 标题任务是另一条独立链路

- `backend/internal/application/conversation/service_billing.go:272` 在主调用完成计费、metadata hint 为 pending 且 RunID 匹配时安排标题/标签任务；计费失败可能不进入后续安排。
- `service_metadata.go:28,98` 的自动任务拥有独立 90 秒超时，先持久化首消息的简短 fallback，再调用标题模型；失败只记 `conversation_metadata_generation_failed`，未见自动重试。
- `background.WithTimeout` 基于 detached context，已启动任务不随浏览器断开而取消。因此不能直接说“用户切页取消了标题”。
- 自动标题输入 `buildConversationMetadataMessages` 只取用户文本，不包含图片内容；纯图片无文本也可能没有可用于标题生成的输入。
- 正式标题更新仅替换默认占位或允许的 fallback，保护手改标题。手动重新生成是独立入口，不构成自动补偿机制。

可解释的路径：主回复后台成功，但独立标题调用超时/失败，最终留下 fallback；也可能计费或 hint 条件导致未安排任务。需以该会话的 runID、metadata 日志和持久状态区分，当前不能将标题缺失都归结于前端误报。

后续修复应有明确、可恢复的标题任务状态与有限重试，保护用户手改标题并避免重复计费；不要在每次读取列表时无条件重发标题请求。持久任务与读取时补偿的具体实现另行确定。

必要回归：连接断开但后台 pending→success；服务端明确 error；切 admin 后旧请求结束；恢复流后不重复提交；标题超时后可补偿且不覆盖手改标题。暂不扩大为整套运行时重构，Chat01 探测继续作为当前主线。
