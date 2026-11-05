package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestChat01UserImageAndProcessPresentation(t *testing.T) {
	raw := `<think>` + "\n\n" + `{"uri":"skills://pdfs","start_line":1}` + "## Thought:\n\n**Used pdfs skill****Thinking****Thinking**he map overview**\n\n过程说明。\n\n**Confirmed location****整理酒店**\n\n后续核对。\n</think>\n![海报](https://example.org/poster.png)\n\nWorked for 2m 25s\n\n## Answer\n"
	var wantText, wantReasoning string
	for _, width := range []int{2048, 1, 7, 64} {
		out := &portllm.GenerateOutput{}
		var visible, reasoning strings.Builder
		err := consumeChat01Stream(strings.NewReader(chat01SSE(raw, width, true)), out, func(e portllm.GenerateStreamEvent) error {
			visible.WriteString(e.Delta)
			if e.Reasoning != nil {
				reasoning.WriteString(e.Reasoning.Text)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if out.Reasoning == nil {
			t.Fatal("reasoning lost")
		}
		if len(out.ServerToolCalls) != 0 {
			t.Fatal("skill annotations became fake tool executions")
		}
		if strings.Contains(out.Text, "Worked") || strings.Contains(out.Text, "Answer") || strings.Contains(out.Text, "酒店") {
			t.Fatalf("body leak: %q", out.Text)
		}
		for _, bad := range []string{"****", "Thinking", "start_line"} {
			if strings.Contains(out.Reasoning.Text, bad) {
				t.Fatalf("raw chrome: %q", out.Reasoning.Text)
			}
		}
		if !strings.Contains(out.Reasoning.Text, "技能说明：pdfs") || !strings.Contains(out.Reasoning.Text, "he map overview") {
			t.Fatalf("reasoning information lost: %q", out.Reasoning.Text)
		}
		if visible.String() != out.Text || reasoning.String() != out.Reasoning.Text {
			t.Fatal("stream differs from stored content")
		}
		if width == 2048 {
			wantText, wantReasoning = out.Text, out.Reasoning.Text
		} else if out.Text != wantText || out.Reasoning.Text != wantReasoning {
			t.Fatal("fragmentation changes presentation")
		}
	}
}

func TestChat01LocalUserPromptCaptures(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", ".cache", "chat01-user-regression-20261105")
	for _, capture := range []string{"probe/25_user_concert_hotels", "components-probe/26_user_components", "acceptance/27_plain_multilingual", "acceptance/28_weekend_event", "acceptance/29_file_iterations"} {
		t.Run(capture, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(capture), "content.txt"))
			if os.IsNotExist(err) {
				t.Skip("private capture unavailable")
			}
			if err != nil {
				t.Fatal(err)
			}
			raw = []byte(strings.ReplaceAll(string(raw), "\r\n", "\n"))
			body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(raw)}}}})
			out, err := parseChat01Output(body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(capture, "25_") {
				if !strings.Contains(out.Text, "李健北京演唱会：最新官宣消息") || !strings.Contains(out.Text, "下载酒店专项 PDF") || strings.Contains(out.Text, "import reportlab") {
					t.Fatal("hotel answer lost or execution leaked")
				}
			} else if strings.Contains(capture, "26_") {
				if strings.Count(out.Text, "```deeix-ui") != 3 || strings.Contains(out.Text, "Worked for") {
					t.Fatal("structured charts lost or timer leaked")
				}
			} else if strings.Contains(capture, "27_") {
				if out.Text != strings.TrimPrefix(string(raw), "## Answer:") {
					t.Fatal("ordinary answer or literal examples changed")
				}
			} else if strings.Contains(capture, "28_") {
				if !strings.Contains(out.Text, "我会先检查附件") || !strings.Contains(out.Text, "## 一、周末销量核算结果") || !strings.Contains(out.Text, "1,940") || !strings.Contains(out.Text, "未进行任何预订、付款或对外联系") || strings.Contains(out.Text, "import reportlab") || len(out.GeneratedImages) == 0 || len(out.GeneratedFiles) != 5 {
					t.Fatalf("mixed deliverables incomplete: images=%d files=%d", len(out.GeneratedImages), len(out.GeneratedFiles))
				}
			} else if strings.Contains(capture, "29_") {
				if !strings.Contains(out.Text, "1000元预算够用") || !strings.Contains(out.Text, "10人结余200元") || len(out.GeneratedFiles) != 2 || strings.Contains(out.Text, "import artifact_tool") {
					t.Fatal("iterative file answer missing or code leaked")
				}
			}
			streamed := &portllm.GenerateOutput{}
			var events strings.Builder
			err = consumeChat01Stream(strings.NewReader(chat01SSE(string(raw), 7, true)), streamed, func(e portllm.GenerateStreamEvent) error { events.WriteString(e.Delta); return nil })
			if err != nil || streamed.Text != out.Text || events.String() != out.Text {
				t.Fatal("real capture stream/persistence mismatch")
			}
			wire, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(capture), "body.raw"))
			if err != nil {
				t.Fatal(err)
			}
			actual := &portllm.GenerateOutput{}
			if err = consumeChat01Stream(strings.NewReader(string(wire)), actual, nil); err != nil || actual.Text != out.Text {
				t.Fatalf("captured wire output differs from complete response: error=%v actual=%d expected=%d", err, len(actual.Text), len(out.Text))
			}
			// Opt-in output for local browser review, never part of repository fixtures.
			if os.Getenv("CHAT01_WRITE_PRIVATE_REVIEW") == "1" {
				data, _ := json.Marshal(out)
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(capture), "normalized.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
