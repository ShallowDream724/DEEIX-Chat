# 模型生成文件

OpenAI Responses 的 Shell / Code Interpreter 生成并明确引用文件后，DEEIX 将文件保存为当前用户的附件。回复正文中的对应链接可直接下载，回复尾部沿用现有附件卡片。部署继续使用已有 API 与文件存储，无需新增 HTTPS 域名或公开 CDN。

## 下载与保存

1. OpenAI 适配器只接收 `container_file_citation`，从中提取 `container_id`、`file_id` 和文件名。不扫描容器目录，不抓取普通网页链接或未引用的临时文件。
2. 使用生成该回复的实际路由、凭据和附加请求头，调用 `GET /v1/containers/{container_id}/files/{file_id}/content` 下载内容。
3. 会话服务通过现有 `UploadService` 将 `generated_file` 保存到当前用户的文件管理，继续执行文件数、大小、MIME 和存储配额限制，并复用重复文件。文件沿用现有保留与删除策略，不依赖上游容器继续存活。
4. 回复正文和附件通过现有完成事务一起保存。只有能明确对应到已保存附件的 `sandbox:/...` 链接，才改为 `/api/v1/files/{fileID}/content`；存在歧义时保留原文，附件卡片仍可下载。

前端只把当前消息已有附件的文件链接交给统一下载动作，复用现有鉴权、文件读取和错误提示。尾部卡片在聊天工作区打开[统一文件预览](file-preview-workspace.md)，卡片下载按钮仍直接下载；已在正文展示的图片不重复展示卡片。

## 失败与范围

下载、保存或配额检查失败时，保留已生成回复和已观测的实际用量，并显示对应错误；未被消息引用的新建文件会清理，复用文件不受影响。已有用量记录和计费链路继续生效。

本轮不修改数据库结构，不新增预览框架，不扩展临时聊天的持久化。后续渠道通过同一生成文件边界接入，预览等后续安排见 [分支排期](FORK_ROADMAP.md)。

协议依据：[Code Interpreter 文件处理](https://developers.openai.com/api/docs/guides/tools-code-interpreter#work-with-files)、[Shell 产物下载](https://developers.openai.com/api/docs/guides/tools-shell#download-artifacts)。
