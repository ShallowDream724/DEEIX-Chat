package chat01

import (
	"strings"
	"testing"
)

func TestComponentReadableFallback(t *testing.T) {
	raw := componentOpen + `{"map_widget_v2":{"points":[{"ref":"b"},{"ref":"a"}],"carousel":{"cards":{"a":{"title":"地点甲","label":"说明甲","url":"https://example.org/a"},"b":{"title":"地点乙","href":"javascript:bad()"}}},"title":"地点地图"}}` + componentClose
	b := parsed(raw, 1)[0]
	info := ComponentInfoFor(b)
	if info.State != "complete" || info.Title != "地点地图" || len(info.Cards) != 2 || info.Cards[0].Ref != "b" || info.Cards[0].URL != "" || info.Cards[1].URL != "https://example.org/a" {
		t.Fatalf("info=%+v", info)
	}
	fallback := RenderComponentFallback(b)
	if !strings.Contains(fallback, "地点乙") || !strings.Contains(fallback, "说明甲") || strings.Contains(fallback, "map_widget") || strings.Contains(fallback, "javascript") || strings.Contains(fallback, componentOpen) {
		t.Fatalf("fallback=%q", fallback)
	}
	truncated := parsed(componentOpen+`{"map_widget_v2":{"title":"已经收到的标题","points":[{"ref":"safe"}],"carousel":{"cards":{"safe":{"title":"完整卡片","label":"截断`, 1)[0]
	partial := ComponentInfoFor(truncated)
	if partial.State != "incomplete" || partial.Title != "已经收到的标题" || len(partial.Cards) != 1 || partial.Cards[0].Title != "完整卡片" || partial.Cards[0].Label != "" {
		t.Fatalf("partial=%+v", partial)
	}
	oversized := Block{Kind: KindComponent, Text: componentOpen + `{"map_widget_v2":{"title":"保留标题","unknown":"` + strings.Repeat("x", maxComponent) + `"}}`, Oversized: true}
	if got := ComponentInfoFor(oversized); got.State != "oversized" || got.Title != "保留标题" {
		t.Fatalf("oversized=%+v", got)
	}
	unknown := parsed(componentOpen+`{"dynamic_app":{"code":"alert(1)"}}`+componentClose, 0)[0]
	if got := RenderComponentFallback(unknown); strings.Contains(got, "alert") || !strings.Contains(got, "暂不支持") {
		t.Fatal(got)
	}
}

func TestReferencePresentation(t *testing.T) {
	ref := "citeref1ref2"
	text := "正文" + ref + " 未映射citeprivate-id\n`" + ref + "`\n```text\n" + ref + "\n```\n"
	got := NormalizeReferences(text, map[string]string{"ref1": "https://example.org/source", "ref2": "javascript:bad()"})
	if !strings.Contains(got, "正文[来源](https://example.org/source)") || !strings.Contains(got, "未映射[来源未提供链接]") || !strings.Contains(got, "`"+ref+"`") || !strings.Contains(got, "```text\n"+ref+"\n```") || strings.Contains(got, "javascript") {
		t.Fatal(got)
	}
}

func TestNamedMapPointsAndDeclaredMediaFallback(t *testing.T) {
	mapBlock := parsed(componentOpen+`{"map_widget_v2":{"title":"自选地点","points":[{"name":"地点甲","address":"示例路1号","id":"a"},{"name":"地点乙","address":"示例路2号","id":"b"}],"narrative":"二选一，不是一条连续路线。"}}`+componentClose, 1)[0]
	info := ComponentInfoFor(mapBlock)
	fallback := RenderComponentFallback(mapBlock)
	if len(info.Points) != 2 || !strings.Contains(fallback, "地点甲：示例路1号") || !strings.Contains(fallback, "地点乙：示例路2号") || !strings.Contains(fallback, "二选一") {
		t.Fatal(fallback)
	}
	media := parsed(componentOpen+`{"runtime":{"code":"doNotExecute()","appData":{"opGenui":{"componentResults":{"image":{"componentName":"AsyncImage","state":{"images":[{"title":"图[片]","content_url":"https://images.example.org/photo(1).png","url":"https://example.org/source"}]}},"link":{"componentName":"Link","state":{"url":"https://example.org/details","title":"更多信息"}}}}}}}`+componentClose, 0)[0]
	shown := RenderComponentFallback(media)
	if !strings.Contains(shown, `![图\[片\]](https://images.example.org/photo%281%29.png)`) || !strings.Contains(shown, "[图片来源](https://example.org/source)") || !strings.Contains(shown, "[更多信息](https://example.org/details)") || strings.Contains(shown, "doNotExecute") || strings.Contains(shown, "暂不支持") {
		t.Fatal(shown)
	}
}
