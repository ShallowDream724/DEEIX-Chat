package chat01

import (
	"strings"
	"testing"
)

const syntheticGeneratedImage = "https://files.chat01.ai/user-ABCDEFGHIJKLMNOPQRSTUVWX__11111111-2222-3333-4444-555555555555/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.png"

func TestGeneratedImageNamespace(t *testing.T) {
	if !IsGeneratedImageURL(syntheticGeneratedImage) {
		t.Fatal("known namespace rejected")
	}
	for _, raw := range []string{
		"https://files.chat01.ai/user-uploads/a.png",
		"https://files.chat01.ai/anything/a.png",
		strings.Replace(syntheticGeneratedImage, "files.chat01.ai/", "files.chat01.ai.attacker.invalid/", 1),
		strings.Replace(syntheticGeneratedImage, "https://", "http://", 1),
		strings.Replace(syntheticGeneratedImage, "user-ABCDEFGHIJKLMNOPQRSTUVWX__", "user-uploads__", 1),
		strings.Replace(syntheticGeneratedImage, ".png", ".html", 1),
		strings.Replace(syntheticGeneratedImage, "/aaaaaaaa", "/%61aaaaaaa", 1),
		syntheticGeneratedImage + "/extra",
		syntheticGeneratedImage + "?unverified=1",
	} {
		if IsGeneratedImageURL(raw) {
			t.Errorf("accepted unrelated path: %s", raw)
		}
	}
}

func TestProcessAndComponentResourcesKeepOrigin(t *testing.T) {
	raw := "<think>\n![产物](" + syntheticGeneratedImage + ")\n![检索图片](https://images.example.org/photo.png)\n</think>\n## Answer:\n完成"
	resources := ResourcesFor(checkReplay(t, raw))
	if len(resources) != 2 || resources[0].Kind != "image" || resources[0].OriginKind != KindReasoning || resources[1].Kind != "external-image" {
		t.Fatalf("resources=%+v", resources)
	}
	component := componentOpen + `{"runtime":{"appData":{"opGenui":{"componentResults":{"image1":{"componentName":"AsyncImage","state":{"images":[{"url":"https://example.org/source","content_url":"` + syntheticGeneratedImage + `","title":"结构图片"}]}},"link1":{"componentName":"Link","state":{"url":"https://example.org/page.pdf","title":"来源网页"}},"unknown":{"componentName":"App","code":"https://files.chat01.ai/not-an-artifact.png"}}}}}}` + componentClose
	blocks := checkReplay(t, component)
	structured := ResourcesFor(blocks)
	if len(structured) != 2 || structured[0].Kind != "image" || structured[0].SourceURL != "https://example.org/source" || structured[0].OriginKind != KindComponent || structured[1].Kind != "source" {
		t.Fatalf("structured=%+v", structured)
	}
	if structured[0].Start != blocks[0].Start || structured[0].End != blocks[0].End {
		t.Fatal("component resource source range was guessed")
	}
}
