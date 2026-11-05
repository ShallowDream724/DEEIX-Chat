package conversation

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestGenericAttachmentContainerBytesDoNotBecomePDFPages(t *testing.T) {
	for _, count := range []int{1, 10} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			input := llm.GenerateInput{Messages: []llm.Message{{Role: "user", Parts: []llm.ContentPart{{Kind: llm.ContentPartText, Text: "Inspect these workspace files."}}}}}
			size := int64(100 * 1024 * 1024 / count)
			for i := 0; i < count; i++ {
				input.Messages[0].Parts = append(input.Messages[0].Parts, llm.ContentPart{Kind: llm.ContentPartAttachment, FileName: fmt.Sprintf("bundle-%d.zip", i), MimeType: "application/zip", Reference: &llm.InputFileReference{FileID: fmt.Sprint(i), SizeBytes: size}})
			}
			estimate := estimateGenerateInputTokens(input)
			if err := (contextBudgetPolicy{limit: 70_000}).evaluate(input, estimate).err(); err != nil {
				t.Fatalf("100MiB binary container charged as full text: tokens=%d error=%v", estimate, err)
			}
			if estimate <= 0 {
				t.Fatal("manifest was not counted")
			}
			input.Messages[0].Parts = append(input.Messages[0].Parts, llm.ContentPart{Kind: llm.ContentPartText, Text: strings.Repeat("实际正文内容", 40_000)})
			if err := (contextBudgetPolicy{limit: 70_000}).evaluate(input, estimateGenerateInputTokens(input)).err(); err == nil {
				t.Fatal("actual injected text bypassed context budget")
			}
		})
	}
}
