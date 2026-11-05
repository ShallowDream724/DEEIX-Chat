package channel

import "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"

const (
	chat01ConnectTimeoutMS = 30_000
	chat01LongTimeoutMS    = 9_000_000
	// Sparse fields retain administrator image/page/request budgets by inheritance.
	chat01InitialInputLimitsJSON = `{"attachmentCount":10,"attachmentBytes":104857600,"attachmentSingleBytes":104857600,"messageFileCount":10,"nativeCount":10,"nativeBytes":104857600,"nonPDFBytes":104857600,"pdfSingleBytes":104857600,"pdfBytes":104857600}`
)

func onlyChat01Protocols(protocols []string) bool {
	if len(protocols) == 0 {
		return false
	}
	for _, protocol := range protocols {
		if llm.NormalizeAdapter(protocol) != llm.AdapterChat01ChatCompletions {
			return false
		}
	}
	return true
}

func inferredKindsForCompatible(name string, compatible string) string {
	if normalizeCompatible(compatible) == compatibleChat01 {
		return `["chat"]`
	}
	return inferKindsJSON(name)
}
