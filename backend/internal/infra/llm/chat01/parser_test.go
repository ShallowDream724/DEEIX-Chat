package chat01

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func parsed(raw string, width int) []Block {
	p := NewParser()
	if width < 1 {
		width = max(1, len(raw))
	}
	for i := 0; i < len(raw); i += width {
		p.Feed(raw[i:min(i+width, len(raw))])
	}
	p.Finish()
	return p.Blocks()
}
func textFor(blocks []Block, kind Kind) string {
	var s strings.Builder
	for _, b := range blocks {
		if b.Kind == kind {
			s.WriteString(b.Text)
		}
	}
	return s.String()
}
func checkReplay(t *testing.T, raw string) []Block {
	t.Helper()
	baseline := parsed(raw, 0)
	var joined strings.Builder
	var offset int64
	for _, b := range baseline {
		if b.Start != offset || b.End != b.Start+int64(len(b.Text)) {
			t.Fatalf("invalid source range: %+v", b)
		}
		joined.WriteString(b.Text)
		offset = b.End
	}
	if joined.String() != raw {
		t.Fatal("source bytes lost")
	}
	for _, width := range []int{1, 5, 64, 127, 511} {
		if got := parsed(raw, width); !reflect.DeepEqual(got, baseline) {
			t.Fatalf("split %d differs\ngot: %+v\nwant: %+v", width, got, baseline)
		}
	}
	return baseline
}

func TestLiteralProtection(t *testing.T) {
	for name, raw := range map[string]string{
		"tilde":      "## Answer:\n~~~python\n## Thought:\n<think>\n~~~\n正文",
		"nested":     "## Answer:\n````markdown\n```python\n## Thought:\n```\n````\n正文",
		"inline":     "## Answer:\n保留 `<think>`、`</think>`、`` `## Thought:` ``。",
		"escape":     "## Answer:\n\\<think> 是转义后的标签。",
		"indent":     "## Answer:\n    <think>\n    ## Thought:\n正文",
		"tutorial":   "## Answer:\n## Python:\n```python\nprint(\"<think>\")\n## Thought:\n```\n正文",
		"unfinished": "## Answer:\n保留未完整标记 <thi",
	} {
		t.Run(name, func(t *testing.T) {
			b := checkReplay(t, raw)
			if textFor(b, KindReasoning) != "" {
				t.Fatal("literal code became reasoning")
			}
		})
	}
}

func TestProcessBoundaries(t *testing.T) {
	for name, raw := range map[string]string{
		"comment":          "<think>\n## Python:\n# ## Answer:\nprint(1)\n</think>\n## Answer:\n真答案",
		"heredoc":          "<think>\nbash -lc set -eu\ncat > out.txt <<'END'\n## Answer:\n<think>\nEND\ncat out.txt\n</think>\n## Answer:\n真答案",
		"shell-quote":      "<think>\n## Shell:\nprintf '%s\\n' '\n## Answer:\n<think>\n'\n</think>\n## Answer:\n真答案",
		"clipped":          "<think>\n**Completed inspection**record.json').read_text()\nassert count == 3\nprint(count)\n</think>\n## Answer:\n真答案",
		"orphan":           "<think>\n损坏标题的尾部**\n</think>\n## Answer:\n真答案",
		"unclosed-bold":    "<think>\n**Running\n## Answer:\n真答案",
		"unclosed-fence":   "<think>\n## Python:\n```python\nprint(1)\n## Answer:\n真答案",
		"triple":           "<think>\n## Python:\n```python\ns = \"\"\"\n</think>\n## Answer:\ntext\n\"\"\"\nprint(s)\n</think>\n## Answer:\n真答案",
		"bold-markers":     "**Thought:**\n处理\n**Answer:**\n真答案",
		"clipped-shell":    "<think>\n**Used skill**clipped.bin; ls -lh /mnt/data; python - <<'PY'\nimport csv\nprint(1)\nPY**Analyzing received data**\n过程说明\n</think>\n## Answer:\n真答案",
		"clipped-cat":      "<think>\n**Used skill**clipped_dir && cat > sample.py <<'PY'\nimport json\nprint(1)\nPY\ncat > README.md <<'MD'\n~~~text\n## Answer:\n## Thought:\n<think>literal</think>\n[example](sandbox:/mnt/data/example.txt)\n~~~\nMD\n</think>\n## Answer:\n真答案",
		"power-expression": "<think>\n**Completed inspection**record.json').read_text()\nassert count == 3\nPY**2**3\nprint(count)\n</think>\n## Answer:\n真答案",
	} {
		t.Run(name, func(t *testing.T) {
			b := checkReplay(t, raw)
			if got := strings.TrimSpace(textFor(b, KindAnswer)); got != "真答案" {
				t.Fatalf("answer=%q", got)
			}
			if name == "clipped" && textFor(b, KindCodeFragment) == "" {
				t.Fatal("clipped code not recognized")
			}
			if name == "clipped-shell" && (!strings.Contains(textFor(b, KindShell), "clipped.bin; ls") || !strings.Contains(textFor(b, KindReasoning), "过程说明")) {
				t.Fatal("clipped command or joined title misclassified")
			}
			if name == "clipped-cat" {
				if !strings.Contains(textFor(b, KindShell), "~~~text\n## Answer:\n## Thought:") {
					t.Fatal("heredoc tutorial markers left shell literal")
				}
				if len(ResourcesFor(b)) != 0 {
					t.Fatal("heredoc tutorial link became a resource")
				}
			}
			if name == "power-expression" && !strings.Contains(textFor(b, KindCodeFragment), "PY**2**3") {
				t.Fatal("power expression became a process heading")
			}
		})
	}
}

func TestComponentBoundsAndRecovery(t *testing.T) {
	raw := "## Answer:\n" + componentOpen + strings.Repeat("x", 66000) + "\n## Answer:\n正文"
	b := checkReplay(t, raw)
	if !strings.Contains(textFor(b, KindAnswer), "正文") {
		t.Fatal("oversized component swallowed answer boundary")
	}
	for _, v := range b {
		if v.Kind == KindComponent && (!v.Oversized || v.Complete) {
			t.Fatal("component damage state missing")
		}
	}
	complete := checkReplay(t, componentOpen+`{"map_widget_v2":{"title":"地点","ref":"private-ref"}}`+componentClose)
	if len(complete) != 1 || !complete[0].Complete {
		t.Fatal("complete component not identified")
	}
}

func TestDeltasReconstructBlocks(t *testing.T) {
	raw := "## Answer:\n先答复<think>\n**Searched 3 sources**\n## Python:\nprint(1)\n</think>\n## Answer:\n后答复" + componentOpen + `{"title":"地点"}` + componentClose
	p := NewParser()
	emitted := map[string]string{}
	events := 0
	for i := 0; i < len(raw); i += 7 {
		for _, d := range p.Feed(raw[i:min(i+7, len(raw))]) {
			emitted[d.BlockID] += d.Text
			events++
		}
		p.Feed("")
	}
	for _, d := range p.Finish() {
		emitted[d.BlockID] += d.Text
	}
	for _, b := range p.Blocks() {
		if emitted[b.ID] != b.Text {
			t.Fatalf("deltas differ for %s", b.ID)
		}
	}
	if p.Finish() != nil || p.Feed("ignored") != nil {
		t.Fatal("finished parser changed")
	}
	if events >= len(raw) {
		t.Fatal("events emitted per character")
	}
	long := NewParser()
	if n := len(long.Feed(strings.Repeat("正文", 10000))); n != 1 {
		t.Fatalf("ordinary text emitted %d events", n)
	}
}

func TestUnknownContentIsPreserved(t *testing.T) {
	raw := "## Answer:\nI will explain. 我会解释。 salt abc\n```json\n{\"salt\":\"keep\"}\n```\nciteinternal-ref"
	b := checkReplay(t, raw)
	if textFor(b, KindReasoning) != "" || !strings.Contains(textFor(b, KindAnswer), "salt") {
		t.Fatal("unstructured content inferred or filtered")
	}
}

func TestPendingBufferIsBounded(t *testing.T) {
	for name, raw := range map[string]string{
		"delimiters":        strings.Repeat("`", 10000) + "\n## Answer:\n正文",
		"inline-delimiters": "正文 " + strings.Repeat("`", 10000) + "\n## Answer:\n正文",
		"long-code-line":    "<think>\nimport " + strings.Repeat("x", 10000) + "\n## Answer:\n正文",
		"long-heredoc-name": "<think>\n## Shell:\ncat <<" + strings.Repeat("A", 10000) + "\n## Answer:\n正文",
		"fence-space":       "<think>\n## Python:\n```python\nprint(1)\n```" + strings.Repeat(" ", 10000) + "\n## Answer:\n正文",
		"bold":              "<think>\n**" + strings.Repeat("x", 10000) + "\n## Answer:\n正文",
	} {
		t.Run(name, func(t *testing.T) {
			baseline := checkReplay(t, raw)
			p := NewParser()
			for i := range len(raw) {
				p.Feed(raw[i : i+1])
				if len(p.pending) > 4096 {
					t.Fatalf("pending=%d at %d", len(p.pending), i)
				}
			}
			p.Finish()
			if !reflect.DeepEqual(p.Blocks(), baseline) {
				t.Fatal("bounded fallback changed segmentation")
			}
		})
	}
}

func TestResources(t *testing.T) {
	raw := "## Answer:\n[网页](https://example.org/page.pdf) ![参考](https://images.example/a.png) [产物](https://files.chat01.ai/python-generations/redacted/report.txt) [未映射](sandbox:/mnt/data/x.csv)\n`[代码链接](https://example.org/no)`\n```md\n![代码图片](https://example.org/no.png)\n```"
	r := ResourcesFor(checkReplay(t, raw))
	if len(r) != 4 {
		t.Fatalf("resources=%+v", r)
	}
	for i, kind := range []string{"source", "external-image", "file", "unavailable"} {
		if r[i].Kind != kind {
			t.Fatalf("resource %d: %s", i, r[i].Kind)
		}
	}
	for _, v := range r {
		if raw[v.Start:v.End] == "" {
			t.Fatal("missing resource source range")
		}
	}
}

// Existing captures are intentionally read only from the ignored local cache.
// No provider URLs, account IDs, credentials or full captures enter fixtures.
func TestLocalCapturedResponses(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..", ".cache", "chat01-probes")
	cases := []string{"20261104/01_native_image", "20261104/02_search_code_files", "20261104/03_mixed_image_search_code", "20261104/05_search_map_cards", "20261104/06_nonstream_code_files", "20261105/stress-a/17_repeated_code", "20261105/stress-b/18_office_pdf_compare", "20261105/stress-a/19_api_native_components", "20261105/stress-b/20_literal_markers", "20261105/stress-a/21_audio_jsonl", "20261105/implementation-serial/22_family_event", "20261105/implementation-serial-2/23_cafe_plan", "20261105/implementation-serial-3/24_stream_tutorial"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			rawBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name), "content.txt"))
			if os.IsNotExist(err) {
				t.Skip("local capture not available")
			}
			if err != nil {
				t.Fatal(err)
			}
			raw := strings.ReplaceAll(string(rawBytes), "\r\n", "\n")
			b := checkReplay(t, raw)
			answers := textFor(b, KindAnswer)
			if strings.Contains(name, "02_search") {
				if !strings.Contains(answers, "sum(n**2 for n in range(1, 101))") || strings.Contains(answers, "import zipfile") || textFor(b, KindSearch) == "" {
					t.Fatal("search/code/tutorial classification")
				}
			}
			if strings.Contains(name, "01_native_image") {
				r := ResourcesFor(b)
				if len(r) != 1 || r[0].Kind != "image" || r[0].Evidence != "chat01-generated-image-namespace" {
					t.Fatalf("native image not preserved as generated: %+v", r)
				}
			}
			if strings.Contains(name, "03_mixed") {
				sections := map[int]bool{}
				for _, v := range b {
					if v.Kind == KindAnswer && strings.TrimSpace(v.Text) != "" {
						sections[v.Section] = true
					}
				}
				if len(sections) != 4 || strings.Contains(answers, "from pathlib import Path") || !strings.Contains(answers, "测试结果：3/3 项完成") {
					t.Fatal("mixed stage answers")
				}
				generated := 0
				for _, r := range ResourcesFor(b) {
					if r.Kind == "image" {
						generated++
					}
				}
				if generated != 2 {
					t.Fatalf("generated images=%d", generated)
				}
			}
			if strings.Contains(name, "17_repeated") || strings.Contains(name, "18_office") {
				if textFor(b, KindShell) == "" || textFor(b, KindCodeFragment) == "" || strings.Contains(answers, "import zipfile") {
					t.Fatal("clipped process classification")
				}
			}
			if strings.Contains(name, "20_literal") && !strings.Contains(answers, "`<think>`") {
				t.Fatal("tutorial literals lost")
			}
			if strings.Contains(name, "21_audio") && !strings.Contains(answers, "## English Summary") {
				t.Fatal("orphan bold swallowed final answer")
			}
			if strings.Contains(name, "22_family_event") {
				if !strings.Contains(answers, "预算模型和6页PPT已生成") || strings.Contains(answers, "import csv") || strings.Contains(answers, "**Built the event plan**") {
					t.Fatal("interrupted process mixed with stage answer")
				}
				if !strings.Contains(textFor(b, KindReasoning), "I’m comparing cost-saving combinations") {
					t.Error("family process narrative classified as code")
				}
			}
			if strings.Contains(name, "23_cafe_plan") {
				if !strings.Contains(answers, "# 上海周末「咖啡 × 城市散步」联动活动提案") || !strings.Contains(answers, "本次没有进行任何预订、支付或商家联系") || strings.Contains(answers, "import openpyxl") {
					t.Fatal("cafe final answer lost or execution leaked")
				}
				if !strings.Contains(textFor(b, KindReasoning), "I’m treating the negative quantity") {
					t.Error("cafe process narrative classified as code")
				}
				for _, block := range b {
					if block.Kind == KindComponent {
						fallback := RenderComponentFallback(block)
						if !strings.Contains(fallback, "静安雕塑公园") || !strings.Contains(fallback, "锦绣路1001号") {
							t.Fatalf("named map points lost: %s", fallback)
						}
					}
				}
			}
			if strings.Contains(name, "24_stream_tutorial") {
				sections := map[int]bool{}
				for _, block := range b {
					if block.Kind == KindAnswer && strings.TrimSpace(block.Text) != "" {
						sections[block.Section] = true
					}
				}
				if len(sections) != 1 || strings.Contains(answers, "#!/usr/bin/env python3") || !strings.Contains(answers, "```text\n## Answer:\n## Thought:\n<think>example</think>\n[示例链接](sandbox:/mnt/data/example.txt)\n```") || !strings.Contains(answers, "### 5\\. 完整结论") {
					t.Fatal("tutorial literals became control boundaries or source code leaked")
				}
				for _, resource := range ResourcesFor(b) {
					if resource.Kind == "unavailable" {
						t.Fatal("tutorial sandbox literal became an artifact")
					}
				}
			}
			// Also replay actual captured SSE chunk boundaries, including empty events.
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name), "events.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			p := NewParser()
			var joined strings.Builder
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var envelope struct {
					Data string `json:"data"`
				}
				if json.Unmarshal([]byte(line), &envelope) != nil {
					continue
				}
				var event struct {
					Choices []struct {
						Delta struct {
							Content string `json:"content"`
						} `json:"delta"`
					} `json:"choices"`
				}
				if json.Unmarshal([]byte(envelope.Data), &event) != nil {
					continue
				}
				if len(event.Choices) > 0 {
					value := event.Choices[0].Delta.Content
					joined.WriteString(value)
					p.Feed(value)
				}
			}
			p.Finish()
			if joined.Len() > 0 && (joined.String() != raw || !reflect.DeepEqual(p.Blocks(), b)) {
				t.Fatal("captured SSE differs")
			}
			t.Logf("bytes=%d blocks=%d resources=%d", len(raw), len(b), len(ResourcesFor(b)))
		})
	}
}

func BenchmarkPlainResponse(b *testing.B) {
	raw := strings.Repeat("正文 with plain text.\n", 4096)
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		p := NewParser()
		for i := 0; i < len(raw); i += 127 {
			p.Feed(raw[i:min(i+127, len(raw))])
		}
		p.Finish()
	}
}
