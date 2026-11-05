# 生产 Go 分类回放

在仓库根目录运行：

```sh
node scripts/experiments/chat01-response-replay/build-production.mjs
```

默认输出 `.cache/chat01-production-review.html`，也可以在命令后传入输出路径。需要已经安装的 Go；`GO_BINARY` 可指定其可执行文件。脚本只读取既有抓包，不请求 Chat01，不下载图片或文件。

`backend/cmd/chat01-replay` 直接调用生产 `chat01.NewParser().Feed/Finish`、`ResourcesFor` 与 `RenderComponentFallback`，导出各网络分片的分类 delta、最终块、资源证据和组件降级文字。页面仅回放导出的 delta，不载入 `normalizer.mjs`，也不在浏览器重新判定正文、思考或工具分类。生成时检查原始内容保全、抓包 `content.txt` 一致性以及 delta 重放与最终块的一致性。

样本默认显示完整的 23 咖啡策划，其次可切换 22 截断家庭活动、24 完整教程、旧 03 多 Answer。支持进度拖动、单步、暂停和加速回放。界面标示实际解析器版本、Go 版本和生产解析包源码 SHA256；修改解析器后重新生成，避免把旧预览当作当前结果。

网页不内嵌产物字节。HTTP(S) 图片由浏览器按需外部预览，工具过程中的图片仍留在折叠过程；文件保留真实链接。未映射的 sandbox 地址保持不可点击。私有组件仅显示 Go 生成的降级内容，并提供原始块对照，不执行上游 HTML、JavaScript 或回调。所有上游文本通过 `textContent`/文本节点输出，链接只允许无用户名密码的 HTTP(S)。
