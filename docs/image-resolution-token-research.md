# 图片最长边与视觉 Token 调研

日期：2026-11-04。由用户指定的 gpt-6.1-sol（high）完成官方文档核对和离线算术复核，主线程核对 DEEIX 请求构造与关键算例。未进行付费 API 实测；以下数值不能当作任意第三方渠道的实际账单。

## 对当前开发的约束

- 优先讨论用户常用的新代际：OpenAI GPT-5.6及以上、Gemini2.5及以上，尤其3/3.1与Argon。没有已核实文档的GPT-6.1/6.2、Carbon或渠道别名，不凭名称推定细节参数与计费规则。
- DEEIX当前OpenAI Responses/Chat图像内容块不传`detail`，上游采用默认`auto`；收益评估以该默认行为为准。本批不新增或主动传入图像分辨率参数。
- Gemini GenerateContent已有全局`generationConfig.mediaResolution`透传，已有用户配置保持；新增图像分辨率控制及per-part能力后置，须先核对渠道是否接受参数。
- 本批只暴露已有本站最大边长设置：默认仍1024，管理员可改1568/2048/3072，0表示本站不缩放；不增加按MB触发优化，不改现有缩放/编码算法。

## 1024“够用”的出处

找到几乎逐字吻合的表述来自 [mittwald AI Hosting](https://developer.mittwald.de/docs/v2/platform/aihosting/examples/javascript/#encoding-and-resizing-images)：

> Sending large images significantly increases time to first token (TTFT). Keeping the longest edge at 1024 px or below is a safe default that preserves quality for most tasks.

示例使用`Ministral-3-14B-Instruct-2512`，同页列出Qwen等模型。这是该托管平台的默认实践建议，不是OpenAI/Gemini共同的硬上限。本次未查证OpenAI或Google存在相同原话。

## 本站实际输出尺寸

按照现有等比例缩小、不放大和整数四舍五入规则：

| 同一原图 | 1024档 | 1568档 | 2048档 | 3072档 |
| --- | --- | --- | --- | --- |
| 4000×3000照片 | 1024×768 | 1568×1176 | 2048×1536 | 3072×2304 |
| 3840×2160全屏截图 | 1024×576 | 1568×882 | 2048×1152 | 3072×1728 |
| 1080×12000长截图 | 92×1024 | 141×1568 | 184×2048 | 276×3072 |

文件是0.5MB还是5MB不改变这个尺寸条件。长截图即使3072档仍只有276px宽，不能因此保证小字可读；分段是另一个已后置的功能。

## 新OpenAI默认行为与图像输入估算

[当前模型尺寸规则](https://developers.openai.com/api/docs/guides/images-vision#model-sizing-behavior)中，GPT-5.6-sol/terra/luna及GPT-6-astra默认`auto`等于`original`。在上述样例范围内保留本站传入的尺寸；这些型号有65,535px边长条件，处理后超过30,000个patch会拒绝，而非自动缩到该patch上限。

每32×32px为一个patch，patch数乘1.2后向上取整；以下仅图像输入，不含文字、工具、输出，也不是现金费用，官方提示计费浮点可能差1 token。

| 同一原图，默认auto/original | 1024档 | 1568档 | 2048档 | 3072档 |
| --- | ---: | ---: | ---: | ---: |
| 4000×3000照片 | 922 | 2,176 | 3,687 | 8,295 |
| 3840×2160截图 | 692 | 1,647 | 2,765 | 6,221 |
| 1080×12000长截图 | 116 | 294 | 461 | 1,037 |

例如3072×2304需要96×72=6912个patch，ceil(6912×1.2)=8295。提高本站上限可以增加进入模型的细节，同时增加视觉token；不能认定1024、1568或2048在所有任务上都是最佳值。

### 旧规则为何可能没有3072收益

| 模型默认auto | 原图 | 1024档：服务端内容尺寸 / tokens | 2048档 | 3072档 |
| --- | --- | --- | --- | --- |
| GPT-4o/4.1（auto=high） | 4:3照片 | 1024×768 / 765 | 同前 | 同前 |
| 同上 | 16:9截图 | 1024×576 / 765 | 1365×768 / 1105 | 同2048档 |
| 同上 | 长截图 | 92×1024 / 425 | 184×2048 / 765 | 同2048档 |
| GPT-5.4（auto=high） | 4:3照片 | 1024×768 / 922 | 1824×1368 / 2942 | 同2048档 |
| 同上 | 16:9截图 | 1024×576 / 692 | 2048×1152 / 2765 | 同2048档 |
| 同上 | 长截图 | 92×1024 / 116 | 184×2048 / 461 | 同2048档 |

这些仅用于解释代际差异，不据旧模型规则限制用户的新模型。也不能把所有`low`都当作512px或85 tokens：当前GPT-5.4的low预算可能比high更高。

## Gemini默认处理

以 [GenerateContent媒体分辨率文档](https://ai.google.dev/gemini-api/docs/generate-content/media-resolution)为当前实现依据；[Interactions文档](https://ai.google.dev/gemini-api/docs/media-resolution)使用另一组内容项字段，不能混用。

| 模型 / 读取参数 | 图像token分配的文档近似预算 |
| --- | --- |
| Gemini3/4 默认或high | 约1120 / 图 |
| Gemini3/4 medium | 约560 |
| Gemini3/4 low | 约280 |
| Gemini3/4 ultra_high | 约2240，仅per-part/item，当前全局配置不能据此启用 |
| Gemini2.5默认 | 256 + Pan & Scan，表中约2048 |
| Gemini2.5 high | 256 + Pan & Scan，没有完整的按像素精确推算算法 |
| Gemini2.5 medium / low | 约256 / 64 |

官方使用“maximum number of tokens allocated”，表格标为approximate。故本站1024/2048/3072在保持同一上游参数时适用同一个预算档，但不能把这些值写成任意图片的固定实际usage，也不能推断Gemini统一缩至1024。更清楚的源图在同预算下是否仍改善识别，需要实际渠道测试。

旧图像理解页的258 tokens/tile不应套用于Gemini3/4的新预算规则。新增Gemini分辨率控制已记录后置，当前不主动更改发送参数。

## Claude补充对照

依据[Vision](https://platform.claude.com/docs/en/build-with-claude/vision#evaluate-image-size)及[参考尺寸算法](https://platform.claude.com/docs/en/build-with-claude/vision-coordinates#resize-your-image-before-uploading)：standard为1568最长边/1568视觉token；Claude4.7及以后高分辨率档为2576/4784。以下按参考算法估算，未实测：

| 档位 / 原图 | 1024：内容尺寸 / tokens | 2048 | 3072 |
| --- | --- | --- | --- |
| standard / 4:3照片 | 1024×768 / 1036 | 1270×952 / 1564 | 同2048 |
| standard / 16:9截图 | 1024×576 / 777 | 1456×819 / 1560 | 同2048 |
| high / 4:3照片 | 1024×768 / 1036 | 2048×1536 / 4070 | 2212×1659 / 4740 |
| high / 16:9截图 | 1024×576 / 777 | 2048×1152 / 3108 | 2576×1449 / 4784 |

文档示例与参考helper存在1px舍入差异，内容随后还会补齐至28px倍数，所以不要把该表称为真实服务端观测。官方页面直接fetch受地域页影响，研究代理从官方URL的搜索全文取得正文。

## 结论与待验证项

- 新OpenAI默认original可以实际利用更高尺寸，不能以“API反正只看1024”否定3072。
- 部分旧模型/档位在2048到3072之间被同一上游尺寸预算收敛；更多传输不一定带来更多有效分辨率。
- Gemini的预算档与本站像素上限是不同控制。文档无法精确回答同一图片三档实际token和识别效果，须以该实际渠道usage和读图结果验证。
- 当前默认1024保留，用户可自行调大。没有实测证据前，不宣称某个最长边是所有照片、截图和模型的最佳折中。
