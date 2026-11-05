package chat01

import (
	"strings"
	"testing"
)

func TestUserConcertProcessLeaks(t *testing.T) {
	raw := "<think>\n## Thought:\n**Used pdfs skill**\n</think>\n## Answer:\n我会先核实演出地点。**确认演出地点**\n\n我正在核实场馆。\n\n**Confirmed the performance venue****整理酒店方案**\n\n我会制作酒店清单。\n\n**Created a hotel guide**## Answer:\n\n月份已公布，场馆待定。"
	blocks := checkReplay(t, raw)
	body := textFor(blocks, KindAnswer)
	if strings.Contains(body, "核实场馆") || strings.Contains(body, "Created") || strings.Contains(body, "整理酒店") {
		t.Fatalf("process leaked: %q", body)
	}
	for _, text := range []string{"我会先核实演出地点。", "月份已公布，场馆待定。"} {
		if !strings.Contains(body, text) {
			t.Fatalf("stage answer lost: %q", body)
		}
	}
	if !strings.Contains(textFor(blocks, KindReasoning), "我会制作酒店清单") {
		t.Fatal("recovered process was discarded")
	}
}

func TestImageOnlyTrailingControls(t *testing.T) {
	raw := "<think>\n**Thinking**\n过程\n</think>\n![海报](https://example.org/poster.png)\n\nWorked for 2m 25s\n\n## Answer\n\n"
	body := textFor(checkReplay(t, raw), KindAnswer)
	if strings.Contains(body, "Worked") || strings.Contains(body, "Answer") || !strings.Contains(body, "![海报]") {
		t.Fatalf("image-only body: %q", body)
	}
}

func TestLocalizedAnswerBoundary(t *testing.T) {
	body := textFor(checkReplay(t, "<think>过程</think>\n## Ответ:\n阶段回答。<think>后续核对</think>\n## Ответ:\n最终回答。"), KindAnswer)
	if strings.Contains(body, "Ответ") || !strings.Contains(body, "阶段回答") || !strings.Contains(body, "最终回答") {
		t.Fatalf("localized answer: %q", body)
	}
}

func TestRecoveryKeepsOrdinaryBoldAndLiteralExamples(t *testing.T) {
	for _, body := range []string{
		"说明：**重要结论**\n\n这是正文。\n\n**第二个重点**\n\n这也是正文。",
		"## Answer to the exercise\nWorked for example company\n**结论**\n\n正常正文。",
		"文档中含 **A****B**，两种加粗。",
		"正文里提到## Answer: 这个字符序列，仍然是正文。",
		"对照：**本周****上周**\n\n本周收入 100 万元，上周收入 80 万元。",
		"文档标题：**使用说明**\n\n第一段说明。\n\n**中文****English**\n\n这是完整的双语正文。",
		"看这段 **示例**\n\n```text\n**确认****继续**\n```\n正常正文",
	} {
		raw := "<think>核对</think>\n## Answer:\n" + body
		if got := strings.TrimSpace(textFor(checkReplay(t, raw), KindAnswer)); got != body {
			t.Fatalf("answer changed: %q", got)
		}
	}
}

func TestRecoveryHardLimitAtTruncatedControlPrefix(t *testing.T) {
	for _, tail := range []string{"**B**", "**B***", "**B**## Ans"} {
		p := NewParser()
		suffix := "**A**\n" + strings.Repeat("x", maxProcessRecovery-len("**A**\n")-len(tail)) + tail
		p.Feed("<think>核对</think>\n## Answer:\nIntro " + suffix)
		p.Feed(strings.Repeat("x", 20000))
		if len(p.pending) > maxProcessRecovery {
			t.Fatal("bounded lookahead retained unbounded answer")
		}
		p.Finish()
		if !strings.Contains(textFor(p.Blocks(), KindAnswer), suffix) {
			t.Fatal("fallback discarded original answer")
		}
	}
}

func TestRecoveryEmitsStageAnswerBeforeAmbiguousProcess(t *testing.T) {
	p := NewParser()
	deltas := p.Feed("<think>检查</think>\n## Answer:\n已核实第一项。**确认场馆**\n\n核实中")
	var visible strings.Builder
	for _, d := range deltas {
		if d.Kind == KindAnswer {
			visible.WriteString(d.Text)
		}
	}
	if !strings.Contains(visible.String(), "已核实第一项") {
		t.Fatal("stage answer blocked behind process lookahead")
	}
	p.Feed(strings.Repeat("核实中", 1000))
	if len(p.pending) > maxProcessRecovery {
		t.Fatal("unbounded recovery buffer")
	}
}
