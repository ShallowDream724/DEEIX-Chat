package llm

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChat01AnswerPresentationSemantics(t *testing.T) {
	const cite = "citeprivate-id"
	const link = "[报告](sandbox:/mnt/data/report.pdf)"
	const unavailable = "报告（上游返回的下载地址无效）"
	cases := []struct{ name, input, want string }{
		{"prose", "请下载" + link + "。来源" + cite + "。", "请下载" + unavailable + "。来源[来源未提供链接]。"},
		{"balanced link", "[图[一]](sandbox:/plot(a).png)", "图[一]（上游返回的下载地址无效）"},
		{"image", "![预览](sandbox:/plot.png)", "预览（上游返回的下载地址无效）"},
		{"angle destination", "[报告](<sandbox:/report one.pdf>)", unavailable},
		{"link title", "[报告](sandbox:/report.pdf \"下载报告\")", unavailable},
		{"angle destination title", "[报告](<sandbox:/report one.pdf> '下载报告')", unavailable},
		{"ambiguous destination", "[报告](sandbox:/report.pdf invalid trailing words)", "[报告](sandbox:/report.pdf invalid trailing words)"},
		{"http", "[真实下载](https://example.org/report.pdf)", "[真实下载](https://example.org/report.pdf)"},
		{"plain sandbox", "路径 sandbox:/mnt/data/report.pdf", "路径 sandbox:/mnt/data/report.pdf"},
		{"escaped link", `\` + link + ` 和 [报告]\(sandbox:/file)`, `\` + link + ` 和 [报告]\(sandbox:/file)`},
		{"escaped citation", `\` + cite, `\` + cite},
		{"inline code", "`" + link + cite + "` 后" + link, "`" + link + cite + "` 后" + unavailable},
		{"long inline delimiter", "`` `" + link + cite + "` `` 后" + cite, "`` `" + link + cite + "` `` 后[来源未提供链接]"},
		{"backtick fence", "```markdown\n" + link + cite + "\n```\n" + link, "```markdown\n" + link + cite + "\n```\n" + unavailable},
		{"tilde fence", "  ~~~~markdown\n" + link + cite + "\n  ~~~~~ \r\n" + cite, "  ~~~~markdown\n" + link + cite + "\n  ~~~~~ \r\n[来源未提供链接]"},
		{"nonclosing fence line", "~~~\n~~~ text\n" + link + "\n~~~\n" + link, "~~~\n~~~ text\n" + link + "\n~~~\n" + unavailable},
		{"different fence", "~~~~\n```\n" + link + "\n~~~~\n" + link, "~~~~\n```\n" + link + "\n~~~~\n" + unavailable},
		{"indented code", "    " + link + cite + "\n\t" + link + "\n" + link, "    " + link + cite + "\n\t" + link + "\n" + unavailable},
		{"unknown marker", "unknown" + link + "", "unknown" + link + ""},
		{"unfinished link", "[example " + cite, "[example " + cite},
		{"unfinished destination", "[example](sandbox:/ " + cite, "[example](sandbox:/ " + cite},
		{"unfinished marker", "unknown " + link, "unknown " + link},
		{"newline recovery", "[unfinished\n" + link, "[unfinished\n" + unavailable},
		{"code after unfinished bracket", "[unfinished `code\n" + link + "` 后" + link, "[unfinished `code\n" + link + "` 后" + unavailable},
		{"unicode", "中文🙂" + link + "é" + cite, "中文🙂" + unavailable + "é[来源未提供链接]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Every byte boundary includes splits inside UTF-8 characters and markers.
			for cut := 0; cut <= len(tc.input); cut++ {
				var p chat01AnswerPresentation
				got := p.feed(tc.input[:cut], false) + p.feed(tc.input[cut:], false) + p.feed("", true)
				if got != tc.want {
					t.Fatalf("split %d: got %q; want %q", cut, got, tc.want)
				}
			}
			var p chat01AnswerPresentation
			var got strings.Builder
			for i := range len(tc.input) {
				chunk := p.feed(tc.input[i:i+1], false)
				if !utf8.ValidString(chunk) {
					t.Fatalf("invalid UTF-8 output at byte %d: %q", i, chunk)
				}
				got.WriteString(chunk)
			}
			got.WriteString(p.feed("", true))
			if got.String() != tc.want {
				t.Fatalf("byte chunks: got %q; want %q", got.String(), tc.want)
			}
		})
	}
}

func TestChat01AnswerPresentationStreamsAndBoundsPending(t *testing.T) {
	var p chat01AnswerPresentation
	for _, paragraph := range []string{"普通文字立即输出。", " More ordinary prose without a newline.", "\n"} {
		if got := p.feed(paragraph, false); got != paragraph {
			t.Fatalf("ordinary prose was delayed: %q", got)
		}
	}
	for _, input := range []string{
		"[" + strings.Repeat("x", chat01PresentationLimit*2) + "citeid\n",
		"cite" + strings.Repeat("x", chat01PresentationLimit*2) + "\n",
		strings.Repeat("`", chat01PresentationLimit*2) + "\n[code](sandbox:/file)\n",
	} {
		var filter chat01AnswerPresentation
		var got strings.Builder
		for i := 0; i < len(input); i += 97 {
			got.WriteString(filter.feed(input[i:min(i+97, len(input))], false))
			if len(filter.pending) > chat01PresentationLimit {
				t.Fatalf("pending grew to %d", len(filter.pending))
			}
		}
		got.WriteString(filter.feed("", true))
		if got.String() != input {
			t.Fatal("oversized literal token was altered")
		}
		var whole chat01AnswerPresentation
		if result := whole.feed(input, true); result != input {
			t.Fatal("whole-input interpretation differs")
		}
	}
}
