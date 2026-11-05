package channel

import (
	"context"
	"reflect"
	"testing"

	domainchannel "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestChat01RegistrationOnlyOffersChatEndpoint(t *testing.T) {
	if normalizeCompatible(" Chat01 ") != compatibleChat01 {
		t.Fatal("Chat01 compatible identity was not normalized")
	}
	protocols, err := resolveRouteProtocols(nil, compatibleChat01, "", `["chat"]`)
	if err != nil || !reflect.DeepEqual(protocols, []string{llm.AdapterChat01ChatCompletions}) {
		t.Fatalf("chat protocols=%v err=%v", protocols, err)
	}
	if _, err := normalizeProtocolDefaultsJSON(`{"image_gen":"chat01_chat_completions"}`); err == nil {
		t.Fatal("chat returned images must not enable a dedicated image endpoint")
	}
	if _, ok := NormalizeExternalModelProtocols(protocols); !ok {
		t.Fatal("Chat01 external protocol rejected")
	}
	if got := SuggestExternalModelProtocols(llm.AdapterChat01ChatCompletions, "gpt-image-2"); !reflect.DeepEqual(got, protocols) {
		t.Fatalf("model name changed explicit adapter: %v", got)
	}
}

func TestChat01PublicCapabilitiesDoNotInheritSameNameCatalog(t *testing.T) {
	entry := domainchannel.ModelCatalogEntry{Provider: "openai", ModelID: "gpt-6", InputModalities: []string{"text", "image", "pdf", "audio"}, OutputModalities: []string{"text", "image"}, ContextWindow: 1_000_000, Reasoning: true}
	resolver := ModelCapabilityResolver{catalog: domainchannel.NewModelCatalog([]domainchannel.ModelCatalogEntry{entry}), modalities: domainchannel.NewModelCatalog([]domainchannel.ModelCatalogEntry{entry})}
	view := ModelView{PlatformModelName: "gpt-6", Vendor: "openai", ProtocolsJSON: `["chat01_chat_completions"]`, CapabilitiesJSON: `{}`}
	for _, info := range []ModelCapabilityInfo{resolver.Resolve(view), resolver.ResolveExternal(view)} {
		if info.ContextWindow != 70_000 || info.View != nil || !reflect.DeepEqual(info.InputModalities.Values, domainchannel.Chat01InputModalities()) || !reflect.DeepEqual(info.OutputModalities, []string{"text"}) {
			t.Fatalf("Chat01 inherited upstream catalog: %+v", info)
		}
	}
	view.CapabilitiesJSON = `{"contextWindow":90000,"inputModalities":["text"]}`
	info := resolver.Resolve(view)
	if info.ContextWindow != 90_000 || !reflect.DeepEqual(info.InputModalities.Values, []string{"text"}) {
		t.Fatalf("explicit capability override lost: %+v", info)
	}
	service := newTestService(config.Config{}, nil, nil, nil, nil)
	result := service.ResolveModelCatalog(context.Background(), ModelCatalogResolveInput{Name: "gpt-6", Vendor: "openai", Protocols: []string{llm.AdapterChat01ChatCompletions}})
	if result.ContextWindow != 70_000 || result.Provider != compatibleChat01 || result.AutoReasoning != nil {
		t.Fatalf("edit form catalog inherited upstream specification: %+v", result)
	}
}

func TestChat01ExternalRouteUsesExplicitIdentity(t *testing.T) {
	service := newTestService(config.Config{}, nil, nil, nil, nil)
	model := ExternalModel{Ref: "personal:1/gpt-6", Model: "gpt-6", ProviderName: "arbitrary", Protocols: []string{llm.AdapterChat01ChatCompletions}}
	route, err := service.BuildExternalRoute(ExternalRouteInput{ExternalModel: model, BaseURL: "https://example.com/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if route.ModelVendor != "chat01" || route.ModelIcon != "/icons/chat01.jpg" || route.ReadTimeoutMS != 9_000_000 || route.CatalogReasoning != nil {
		t.Fatalf("unexpected external route: %+v", route)
	}
	view := service.ExternalModelView(model)
	if view.Vendor != "chat01" || view.CapabilitiesJSON != domainchannel.Chat01DefaultCapabilitiesJSON {
		t.Fatalf("unexpected external model view: %+v", view)
	}
}
