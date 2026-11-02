package personalprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	domainpersonalprovider "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/personalprovider"
)

type previewPlatform struct {
	*fakePlatform
	previews int
}

func (p *previewPlatform) PreviewRoutes(context.Context, channel.ResolveRouteInput) ([]*channel.ResolvedRoute, error) {
	p.previews++
	return []*channel.ResolvedRoute{{PlatformModelName: "platform"}}, nil
}

func TestPreviewRoutesDelegatesPlatformAndAuthorizesPersonalWithoutBilling(t *testing.T) {
	fixture := newFixture(t, nil)
	item, err := fixture.service.Create(t.Context(), 1, validCreate(), RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	platform := &previewPlatform{fakePlatform: &fakePlatform{}}
	resolver := NewRouteResolver(platform, fixture.service)
	if _, err := resolver.PreviewRoutes(t.Context(), channel.ResolveRouteInput{PlatformModelName: "platform", UserID: 1}); err != nil || platform.previews != 1 || platform.resolveCalls != 0 {
		t.Fatalf("platform previews must not resolve billing routes: %v, %#v", err, platform)
	}
	input := channel.ResolveRouteInput{PlatformModelName: domainpersonalprovider.FormatModelRef(item.PublicID, "gpt-4o"), TaskType: channel.TaskTypeChat, UserID: 1}
	routes, err := resolver.PreviewRoutes(t.Context(), input)
	if err != nil || len(routes) != 1 || !routes[0].UntrustedEndpoint || routes[0].APIKey != "" {
		t.Fatalf("personal preview must reuse owned model policy without exposing keys: %#v, %v", routes, err)
	}
	input.UserID = 2
	if _, err := resolver.PreviewRoutes(t.Context(), input); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("other user's personal model must be rejected, got %v", err)
	}
	if platform.resolveCalls != 0 || platform.failures != 0 {
		t.Fatal("previews must not resolve platform billing routes or update circuit failures")
	}
}
