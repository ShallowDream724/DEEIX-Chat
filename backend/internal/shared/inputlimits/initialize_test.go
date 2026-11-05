package inputlimits

import (
	"encoding/json"
	"testing"
)

func TestInitializeUpstreamDefaultsPreservesExplicitAndInheritedBudgets(t *testing.T) {
	raw := `{"defaults":{"imageCount":2,"pdfPages":125,"requestBytes":12345},"upstreams":{"7":{"imageBytes":99},"8":{"attachmentCount":3,"nativeBytes":0,"pdfBytes":null}}}`
	merged, err := InitializeUpstreamDefaults(raw, 8, `{"attachmentCount":10,"nativeBytes":104857600,"pdfBytes":104857600,"attachmentBytes":104857600}`)
	if err != nil {
		t.Fatal(err)
	}
	limits := Resolve(merged, "", "", 8)
	if limits.AttachmentCount != 3 || limits.NativeBytes != 0 || limits.PDFBytes != 50_000_000 || limits.AttachmentBytes != 104857600 || limits.ImageCount != 2 || limits.PDFPages != 125 || limits.RequestBytes != 12345 {
		t.Fatalf("unexpected effective limits: %+v", limits)
	}
	if other := Resolve(merged, "", "", 7); other.ImageBytes != 99 || other.AttachmentBytes != 0 {
		t.Fatalf("unrelated upstream changed: %+v", other)
	}
	var document map[string]json.RawMessage
	_ = json.Unmarshal([]byte(merged), &document)
	if string(document["defaults"]) != `{"imageCount":2,"pdfPages":125,"requestBytes":12345}` {
		t.Fatalf("system defaults changed: %s", document["defaults"])
	}
	if _, err := InitializeUpstreamDefaults(raw, 9, `{"unknown":10}`); err == nil {
		t.Fatal("invalid preset accepted")
	}
	if _, err := InitializeUpstreamDefaults(`{"defaults":{"unknown":1}}`, 9, `{}`); err == nil {
		t.Fatal("invalid existing document was silently replaced")
	}
}
