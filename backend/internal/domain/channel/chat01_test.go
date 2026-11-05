package channel

import (
	"reflect"
	"testing"
)

func TestChat01DefaultsAndExplicitOverrides(t *testing.T) {
	if got := ResolveChat01ModelCaps(`{}`); got.ContextWindow != 70_000 {
		t.Fatalf("default window = %d", got.ContextWindow)
	}
	if got := ResolveChat01ModelCaps(`{"contextWindow":90000,"maxOutputTokens":16000}`); got.ContextWindow != 90_000 || got.MaxOutputTokens != 16_000 {
		t.Fatalf("explicit caps = %+v", got)
	}
	if err := ValidateModelCapsOverrides(Chat01DefaultCapabilitiesJSON); err != nil {
		t.Fatal(err)
	}
	if got := ResolveInputModalities(Chat01DefaultCapabilitiesJSON, nil); !reflect.DeepEqual(got.Values, []string{"text", "image", "attachment"}) {
		t.Fatalf("input modalities = %+v", got)
	}
	for _, vendor := range BuiltInModelVendors() {
		if vendor.Key == "chat01" {
			if vendor.Name != "Chat01" || vendor.Icon != "/icons/chat01.jpg" || !vendor.BuiltIn {
				t.Fatalf("Chat01 vendor = %+v", vendor)
			}
			return
		}
	}
	t.Fatal("Chat01 built-in vendor missing")
}
