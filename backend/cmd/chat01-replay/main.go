// chat01-replay exports existing captures through the production parser. It does no network I/O.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/llm/chat01"
)

type frame struct {
	Time   float64        `json:"t"`
	Text   string         `json:"text"`
	Deltas []chat01.Delta `json:"deltas"`
}
type sample struct {
	Name         string            `json:"name"`
	Source       string            `json:"source"`
	Frames       []frame           `json:"frames"`
	Finish       []chat01.Delta    `json:"finish"`
	Blocks       []chat01.Block    `json:"blocks"`
	Resources    []chat01.Resource `json:"resources"`
	Components   map[string]string `json:"componentFallback"`
	Done         bool              `json:"done"`
	FinishReason string            `json:"finishReason,omitempty"`
	Bytes        int               `json:"bytes"`
	Conserved    bool              `json:"conserved"`
}

func main() {
	root := flag.String("root", "..", "repository root")
	output := flag.String("out", "", "output JSON path, otherwise stdout")
	flag.Parse()
	paths := []struct{ path, name string }{
		{".cache/chat01-probes/20261105/implementation-serial-2/23_cafe_plan", "23 完整｜咖啡活动策划与多模态产物"},
		{".cache/chat01-probes/20261105/implementation-serial/22_family_event", "22 截断｜家庭活动与生成文件"},
		{".cache/chat01-probes/20261105/implementation-serial-3/24_stream_tutorial", "24 完整｜流式教程与协议字面量"},
		{".cache/chat01-probes/20261104/03_mixed_image_search_code", "03 多段 Answer｜生图、搜索与代码"},
	}
	items := make([]sample, 0, len(paths))
	for _, entry := range paths {
		item, err := replay(*root, entry.path, entry.name)
		if err != nil {
			fail(err)
		}
		items = append(items, item)
	}
	hash := sha256.New()
	for _, name := range []string{"types.go", "parser.go", "resources.go", "references.go", "components.go"} {
		data, err := os.ReadFile(filepath.Join(*root, "backend/internal/infra/llm/chat01", name))
		if err != nil {
			fail(err)
		}
		hash.Write([]byte(name))
		hash.Write(data)
	}
	payload := struct {
		ParserVersion string   `json:"parserVersion"`
		GoVersion     string   `json:"goVersion"`
		SourceSHA256  string   `json:"sourceSHA256"`
		Cases         []sample `json:"cases"`
	}{chat01.Version, runtime.Version(), hex.EncodeToString(hash.Sum(nil)), items}
	writer := os.Stdout
	if *output != "" {
		var err error
		writer, err = os.Create(*output)
		if err != nil {
			fail(err)
		}
		defer writer.Close()
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(payload); err != nil {
		fail(err)
	}
}

func replay(root, relative, name string) (sample, error) {
	item := sample{Name: name, Source: relative, Components: map[string]string{}}
	file, err := os.Open(filepath.Join(root, relative, "events.jsonl"))
	if err != nil {
		return item, err
	}
	defer file.Close()
	parser := chat01.NewParser()
	var raw strings.Builder
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event struct {
			Time float64 `json:"t"`
			Data string  `json:"data"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return item, err
		}
		if strings.TrimSpace(event.Data) == "[DONE]" {
			item.Done = true
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		text := chunk.Choices[0].Delta.Content
		if reason := chunk.Choices[0].FinishReason; reason != nil {
			item.FinishReason = *reason
		}
		raw.WriteString(text)
		item.Frames = append(item.Frames, frame{event.Time, text, parser.Feed(text)})
	}
	if err := scanner.Err(); err != nil {
		return item, err
	}
	item.Finish = parser.Finish()
	item.Blocks = parser.Blocks()
	item.Resources = chat01.ResourcesFor(item.Blocks)
	item.Bytes = raw.Len()
	var restored strings.Builder
	for _, block := range item.Blocks {
		restored.WriteString(block.Text)
		if block.Kind == chat01.KindComponent {
			item.Components[block.ID] = chat01.RenderComponentFallback(block)
		}
	}
	item.Conserved = raw.String() == restored.String()
	if !item.Conserved {
		return item, fmt.Errorf("source preservation failed: %s", relative)
	}
	if captured, err := os.ReadFile(filepath.Join(root, relative, "content.txt")); err == nil {
		normalize := func(value string) string { return strings.ReplaceAll(value, "\r\n", "\n") }
		if normalize(string(captured)) != normalize(raw.String()) {
			return item, fmt.Errorf("events/content mismatch: %s", relative)
		}
	}
	return item, nil
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
