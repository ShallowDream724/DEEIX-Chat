package llm

import portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"

// deepSeekResponsesAdapter 保留明确的 DeepSeek 协议身份，复用 Responses 的
// 请求编码、流处理和模型目录；原生文件能力另由实际模型和 Files 配置判断。
type deepSeekResponsesAdapter struct {
	openAIResponsesAdapter
}

func (*deepSeekResponsesAdapter) Name() string { return portllm.AdapterDeepSeekResponses }
