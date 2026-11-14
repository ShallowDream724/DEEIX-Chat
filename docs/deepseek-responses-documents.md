# DeepSeek Responses 与文档输入

`deepseek_responses` 是独立的渠道适配器，界面名称为 `Responses (DeepSeek)`。它保留明确的 DeepSeek 身份，复用既有 Responses 编码、流式解析和连接管理；没有新增 Chat Completions 或 Messages 变体，也不复制三套文件实现。

## 管理员配置

选择此调用协议后，多模态仍按原顺序显示文字、图片、PDF、音频、视频，末尾增加可选的“文档”。PDF 与文档独立开关；Chat01 仍使用自己的“图片、附件”选项，DeepSeek 不提供通用附件能力。

协议模板名称为 `DeepSeek（Responses）`，仅匹配原始 `deepseek_responses` 协议推荐，不按供应商展示名推荐。模板包含 `text,image,pdf,document`、DeepSeek 搜索与链接读取，以及 `nativeFiles.spreadsheet: "local"`。手动选择模板属于显式管理员配置；没有套模板时仍遵守模型目录和已有配置。选择协议即可看到并配置文档，不要求名称匹配某个型号。

```json
{
  "inputModalities": ["text", "image", "pdf", "document"],
  "nativeFiles": {"provider": "deepseek", "enabled": true, "spreadsheet": "local"}
}
```

实际 Files 调用仍按上游模型能力复核；当前已确认的模型为 `deepseek-flash`。Pro 可以使用该 Responses 协议做文本对话，不因勾选文档就自动获得上游未支持的 Files 能力。`nativeFiles.enabled: false` 明确关闭上游文件处理。普通文本提取/全文/RAG仍遵守既有规则，不由文档开关放宽。

## 格式与发送边界

| 输入 | 原生准入条件 | DeepSeek 上传用途 |
| --- | --- | --- |
| PDF | PDF 勾选 | `rich_layout_recognition` |
| DOC/DOCX、PPT/PPTX | 文档勾选 | `rich_layout_recognition` |
| EPUB、MOBI | 文档勾选 | `user_data` |
| XLS/XLSX | 文档勾选且 spreadsheet 为 native | `user_data` |
| TXT/Markdown/CSV/JSON/源码 | 沿用本站文本链路 | 不因文档勾选而新增上传 |
| ZIP、音视频、未知二进制 | 不属于文档准入 | 不进入此 Files 路径 |

Excel 默认仍在本站解析；只有管理员显式改为 `spreadsheet: "native"` 才使用上游转换，不并行运行两条路径。图文解析保留上游检测到的文档图片，不保证识别每张图；表格及电子书转换不等同于原件视觉理解。

选择 `Responses (DeepSeek)`、使用受支持的 Flash 模型并勾选对应 PDF/文档输入后，普通附件发送会自动使用上述 Files 路径，不需要用户再打开图文解析开关。本站后台仍可能生成提取结果供文件库和其他模型使用；提取状态为 ready 不能证明该文本被发送给当前模型。原生上下文优先引用官方 `file_id`，不会同时附加本站提取全文。

官方图文解析不是逐页截图传送。含有文字与矢量连线的思维导图，即使页面在阅读器里呈现完整图形，也不能据此保证官方裁图保留整页视觉结构。DOCX 是否含图应检查实际上传包内的图片、绘图和外部图片关系，图注文字本身不代表图像存在。模型对“自己看到了什么”的回答只作线索，不能替代上传用途、文件引用和原文件检查。参见 [DeepSeek 文件解析说明](https://api-docs.deepseek.com/zh-cn/guides/document_parsing/)。

EPUB/MOBI 在普通和临时上传共用的 512 字节头部检查中识别。EPUB 检查 OCF 首个 stored `mimetype` 条目；MOBI 检查 PalmDB `BOOKMOBI` 标识。不解压全文、不把改后缀的 ZIP 当成电子书。出厂白名单包含 PPT/PPTX、EPUB/MOBI；已有管理员白名单不迁移、不自动扩大，仍需允许对应 MIME 才能上传。

## 模块职责与兼容

- `ports/llm/adapter.go` 注册独立适配身份；参数策略键映射到 `openai_responses`。该映射仅用于参数规则，不用于猜测供应商、UI推荐或启用原生文件。
- `infra/llm/deepseek_responses.go` 复用 Responses 引擎；不会自动启用 OpenAI 的 `previous_response_id`、store/background、专属图片工具或服务档位语义。DeepSeek 签名历史继续沿用原 Responses 内容形状。
- `shared/deepseekfiles` 是后端格式、解析用途和已支持模型的公共定义；conversation 准入和 infra 上传读取同一份定义，不各自维护后缀表。
- `conversation/native_input.go` 根据实际路由与 PDF/document 开关决定原生读取。预检、正式发送、临时会话和历史附件共用它；同一文件进入 Files 后不会再重复注入本站全文/RAG。
- `infra/llm/deepseek_files.go` 保留原有文件 ID 缓存、用户/凭据隔离、上传租约、结果不明时查询恢复及解析计量。格式扩展不创建另一套上传缓存或重试逻辑。
- 新原生工具声明保留 `deepseek_responses` 配置身份；运行时与既有 Responses 参数规则兼容，并按规范化身份去重，避免两个协议别名重复授权和计费。

旧 `openai_responses`、Chat Completions、Messages 的 DeepSeek 文件配置继续兼容，官方地址或已有明确 `nativeFiles.provider` 声明仍有效；不按厂商展示名批量改写协议。旧协议中 PDF/attachment 曾共同启用 Files 的配置保留旧行为；新适配器的 PDF/document 则独立控制。切换到新适配器时应明确勾选需要的输入项。地址、密钥、上游模型名和已保存文件引用无需重建。

## 验证

离线测试覆盖新协议/个人服务导入、工具授权与别名去重、PDF/document 独立开关、Pro和普通兼容渠道边界、旧配置兼容、Office/电子书上传后携带 file_id、重复请求复用已有文件，以及 Chat01 原件能力隔离。前端覆盖选项顺序、协议绑定、模板推荐及原生工具协议身份；上传测试覆盖真实电子书头部、伪装 ZIP 与白名单拒绝。测试不调用付费上游。

2026-11-14 本地验收通过：相关 Go 包测试、20 项前端专项测试、Web 类型检查和 lint、前后端构建。真实管理页面在 1440px 和 390px 下核对文档菜单与勾选结果；厂商为 Unknown、平台模型名为空时仍可选择文档，生成 `inputModalities: ["text", "document"]`。切换普通 OpenAI、Chat01 和 DeepSeek 协议时选项分别隔离，模板显示 `DeepSeek（Responses）`，无浏览器运行时错误。页面验收使用未保存的新建表单，没有改写既有模型或生产配置。私有证据在 `.cache/local-dev/deepseek-documents-browser-qa.json` 及同目录桌面/手机截图；本地前端 3100、后端 8080 已更新。

同日针对“文件只见文字”的反馈核查了实际测试会话：PDF 与 DOCX 的官方文件缓存均为 ready，用途均为 `rich_layout_recognition`，上下文标记为 `native_document`；Responses 编码使用 `input_file/file_id`。实际 DOCX 包无嵌入或外链图片，PDF 为文字与矢量图形。未发现该路径回退到本站纯文本的证据；官方不提供解析结果下载，无法确认该 PDF 的具体裁图结果。本次只读核查没有新增付费上游请求。
