package channel

import (
	"errors"
	"testing"

	domainchannel "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

func TestPreviewRoutesAuthorizesAndDoesNotSelectAPIKey(t *testing.T) {
	const encryptionKey = "test-data-encryption-key-32-bytes"
	keys, err := encryptAPIKeys(testKeyring(t, encryptionKey), `{"strategy":"round_robin","keys":[{"key":"sk-one","status":"active"},{"key":"sk-two","status":"active"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	repo := &routeResolutionRepositoryStub{model: domainchannel.PlatformModel{ID: 10, PlatformModelName: "test-model", AccessScope: ModelAccessScopePublic}, routes: []repository.ChannelUpstreamRouteRow{{
		RouteID: 1, UpstreamID: 2, UpstreamModelID: 3, BindingCode: "binding", UpstreamModelName: "model", ModelKindsJSON: `["chat"]`, Protocol: llm.AdapterOpenAIChatCompletions, BaseURL: "https://api.openai.com/v1", APIKeysEnc: keys,
	}}}
	service := newTestService(config.Config{DataEncryptionKey: encryptionKey}, repo, nil, nil, nil)
	input := ResolveRouteInput{PlatformModelName: "test-model", Scope: RouteScopeUser, UserID: 7, TaskType: TaskTypeChat}
	routes, err := service.PreviewRoutes(t.Context(), input)
	if err != nil || len(routes) != 1 || routes[0].APIKey != "" {
		t.Fatalf("preview must not select or expose API keys: %#v, %v", routes, err)
	}
	if index := service.nextLocalAPIKeyIndex(2); index != 0 {
		t.Fatalf("preview advanced key selection: %d", index)
	}
	if repo.breakerLoads != 0 {
		t.Fatal("preview must not load or claim circuit probes")
	}
	repo.model.AccessScope = ModelAccessScopeInternal
	if _, err := service.PreviewRoutes(t.Context(), input); !errors.Is(err, ErrModelAccessDenied) {
		t.Fatalf("internal model should be denied, got %v", err)
	}
}
