package speech

import (
	"context"
	"io"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
)

// RoleProvider binds the ASR and TTS connections from one runtime snapshot.
// The provider ID remains stable while credentials can differ by operation.
type RoleProvider struct {
	ASR domain.Provider
	TTS domain.Provider
}

func (p *RoleProvider) ASRCapability() *domain.Capability {
	if p.ASR == nil {
		return nil
	}
	return p.ASR.ASRCapability()
}
func (p *RoleProvider) TTSCapability() *domain.Capability {
	if p.TTS == nil {
		return nil
	}
	return p.TTS.TTSCapability()
}
func (p *RoleProvider) Transcribe(ctx context.Context, audio domain.Audio) (domain.Transcript, error) {
	if p.ASR == nil {
		return domain.Transcript{}, domain.ErrUnavailable
	}
	return p.ASR.Transcribe(ctx, audio)
}
func (p *RoleProvider) Stream(ctx context.Context, request domain.StreamRequest, frames <-chan []byte, emit func(domain.Event) error) (domain.Transcript, error) {
	if p.ASR == nil {
		return domain.Transcript{}, domain.ErrUnavailable
	}
	return p.ASR.Stream(ctx, request, frames, emit)
}
func (p *RoleProvider) Synthesize(ctx context.Context, request domain.Synthesis) (io.ReadCloser, string, error) {
	if p.TTS == nil {
		return nil, "", domain.ErrUnavailable
	}
	return p.TTS.Synthesize(ctx, request)
}
func (p *RoleProvider) CacheIdentity(operation string) string {
	provider := p.ASR
	if operation == "tts" {
		provider = p.TTS
	}
	return providerCacheIdentity(provider, operation)
}

func providerCacheIdentity(provider domain.Provider, operation string) string {
	if scoped, ok := provider.(interface{ CacheIdentity(string) string }); ok {
		return scoped.CacheIdentity(operation)
	}
	return ""
}
