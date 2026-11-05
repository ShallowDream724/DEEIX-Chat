package llm

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestChat01RequestKeepsOriginalFilesAndUserInstructions(t *testing.T) {
	input := portllm.GenerateInput{
		Messages: []portllm.Message{
			{Role: "system", Content: "用户选择的指令"},
			{Role: "user", Parts: []portllm.ContentPart{
				{Kind: portllm.ContentPartText, Text: "查看附件"},
				{Kind: portllm.ContentPartAttachment, FileName: "plan.docx", Reference: &portllm.InputFileReference{URL: "https://example.org/api/v1/attachments/fetch/token", SizeBytes: 1024}},
				{Kind: portllm.ContentPartImage, MimeType: "image/png", Data: []byte{1, 2, 3}},
			}},
			{Role: "tool", Content: "本地工具的旧记录"},
		},
		PreviousResponseID: "old-provider-id",
		Options:            map[string]any{"temperature": 0.5, "tools": []any{"forbidden"}, "previous_response_id": "forbidden", "ui_state": "forbidden"},
	}
	body, err := buildChat01RequestBody("gpt-6", input, true)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(body)
	for _, bad := range []string{"forbidden", "old-provider-id", "本地工具的旧记录"} {
		if strings.Contains(string(encoded), bad) {
			t.Fatalf("forwarded local state: %s", encoded)
		}
	}
	if !strings.Contains(string(encoded), "用户选择的指令") {
		t.Fatal("lost user system instructions")
	}
	messages := body["messages"].([]map[string]any)
	parts := messages[1]["content"].([]map[string]any)
	if len(parts) != 3 || parts[1]["type"] != "file_url" || parts[2]["type"] != "image_url" {
		t.Fatalf("unexpected parts: %#v", parts)
	}
	if parts[1]["file_url"].(map[string]any)["url"] != "https://example.org/api/v1/attachments/fetch/token" {
		t.Fatal("lost HTTPS reference")
	}
	if parts[2]["image_url"].(map[string]any)["url"] != "data:image/png;base64,AQID" {
		t.Fatal("invalid fallback data URL")
	}
	input.Messages[1].Parts[1].Reference.URL = ""
	if _, err = buildChat01RequestBody("gpt-6", input, true); err == nil {
		t.Fatal("unprepared attachment silently dropped")
	}
}

func chat01SSE(text string, width int, terminal bool) string {
	var stream strings.Builder
	runes := []rune(text)
	stream.WriteString(": heartbeat\n\n")
	for i := 0; i < len(runes); i += width {
		packet, _ := json.Marshal(map[string]any{"id": "stable", "choices": []any{map[string]any{"delta": map[string]any{"content": string(runes[i:min(i+width, len(runes))])}}}})
		stream.WriteString("data: " + string(packet) + "\n\n")
	}
	if terminal {
		stream.WriteString("data: [DONE]\n\n")
	}
	return stream.String()
}

func TestChat01StreamingAndCompleteResponseAgree(t *testing.T) {
	raw := "## Answer:\n先给结论。\n<think>\n正在核对。\n## Python:\nprint(1 + 1)\n</think>\n## Answer:\n最终答案。 [文件](sandbox:/mnt/data/report.docx)\n"
	body, _ := json.Marshal(map[string]any{"id": "stable", "choices": []any{map[string]any{"message": map[string]any{"content": raw}}}})
	want, err := parseChat01Output(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{1, 7, 64, 1024} {
		var visible strings.Builder
		out := &portllm.GenerateOutput{}
		err := consumeChat01Stream(strings.NewReader(chat01SSE(raw, width, true)), out, func(event portllm.GenerateStreamEvent) error { visible.WriteString(event.Delta); return nil })
		if err != nil {
			t.Fatal(err)
		}
		if out.Text != want.Text || visible.String() != out.Text {
			t.Fatalf("split %d final/stream mismatch: %#v", width, out)
		}
		if !strings.Contains(out.Text, "先给结论") || !strings.Contains(out.Text, "最终答案") || !strings.Contains(out.Text, "上游返回的下载地址无效") || strings.Contains(out.Text, "print(") {
			t.Fatalf("wrong visible result: %q", out.Text)
		}
		if out.Reasoning == nil || !strings.Contains(out.Reasoning.Text, "正在核对") {
			t.Fatal("lost reasoning")
		}
		if !out.ContentNormalized || len(out.Reasoning.Segments) == 0 || len(out.ProcessOrder) != 2 {
			t.Fatal("lost normalized content or process identities")
		}
		if len(out.ToolCalls) != 0 || len(out.ServerToolCalls) != 1 || out.ServerToolCalls[0].Status != "incomplete" {
			t.Fatalf("wrong tool semantics: %+v", out)
		}
		if !reflect.DeepEqual(out.ServerToolCalls, want.ServerToolCalls) {
			t.Fatal("stream tool history differs")
		}
	}
}

func TestChat01ConsumerFailureKeepsDeliveredPartialText(t *testing.T) {
	out := &portllm.GenerateOutput{}
	closed := errors.New("consumer closed")
	err := consumeChat01Stream(strings.NewReader(chat01SSE("## Answer:\n已经收到的正文", 1024, true)), out, func(event portllm.GenerateStreamEvent) error {
		if event.Delta != "" {
			return closed
		}
		return nil
	})
	if !errors.Is(err, closed) || !strings.Contains(out.Text, "已经收到") {
		t.Fatalf("partial text lost: %+v / %v", out, err)
	}
}

func TestChat01UnexpectedEOFReturnsPartialWithoutSuccess(t *testing.T) {
	out := &portllm.GenerateOutput{}
	err := consumeChat01Stream(strings.NewReader(chat01SSE("## Answer:\n已返回的部分答案", 3, false)), out, nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) || !strings.Contains(out.Text, "部分答案") {
		t.Fatalf("partial=%+v err=%v", out, err)
	}
}

func TestChat01FinalSnapshotDoesNotDuplicateAnswer(t *testing.T) {
	stream := chat01SSE("## Answer:\n答案", 3, false)
	final, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "## Answer:\n答案完整"}, "finish_reason": "stop"}}})
	out := &portllm.GenerateOutput{}
	if err := consumeChat01Stream(strings.NewReader(stream+"data: "+string(final)+"\n\n"), out, nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.Text) != "答案完整" {
		t.Fatal(out.Text)
	}
}

func TestChat01ResourcesAreVerifiedAndDeduplicated(t *testing.T) {
	image := "https://files.chat01.ai/user-ABCDEFGHIJKLMNOPQRSTUVWX__11111111-2222-3333-4444-555555555555/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.png"
	file := "https://files.chat01.ai/python-generations/run/report.xlsx"
	raw := "## Answer:\n![生成图](" + image + ")\n[表格](" + file + ") [重复](" + file + ")\n![搜索图](https://example.org/photo.jpg)"
	out := &portllm.GenerateOutput{}
	if err := consumeChat01Stream(strings.NewReader(chat01SSE(raw, 9, true)), out, nil); err != nil {
		t.Fatal(err)
	}
	if len(out.GeneratedImages) != 1 || len(out.GeneratedFiles) != 1 || !strings.HasPrefix(out.GeneratedFiles[0].ID, "chat01-url:") {
		t.Fatalf("artifacts: %+v", out)
	}
	if !strings.Contains(out.Text, "https://example.org/photo.jpg") {
		t.Fatal("lost external preview")
	}
}
