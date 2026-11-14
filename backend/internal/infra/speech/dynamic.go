package speech

import (
	"context"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"io"
)

// DynamicProvider resolves exactly once per operation. Runtime configuration
// changes apply to new sessions while an active session keeps its own snapshot.
type DynamicProvider struct {
	Resolve         func() domain.Provider
	ResolveSnapshot func() (domain.Provider, bool, bool)
	ASREnabled      func() bool
	TTSEnabled      func() bool
}

func (d *DynamicProvider) get() domain.Provider {
	if d.ResolveSnapshot != nil {
		p, asr, tts := d.ResolveSnapshot()
		if p == nil {
			return nil
		}
		return &enabledProvider{Provider: p, asr: asr, tts: tts}
	}
	if d.Resolve == nil {
		return nil
	}
	return d.Resolve()
}
func (d *DynamicProvider) Snapshot() domain.Provider { return d.get() }

type enabledProvider struct {
	domain.Provider
	asr, tts bool
}

func (p *enabledProvider) CacheIdentity(operation string) string {
	return providerCacheIdentity(p.Provider, operation)
}

func (p *enabledProvider) ASRCapability() *domain.Capability {
	if !p.asr {
		return nil
	}
	return p.Provider.ASRCapability()
}
func (p *enabledProvider) TTSCapability() *domain.Capability {
	if !p.tts {
		return nil
	}
	return p.Provider.TTSCapability()
}
func (d *DynamicProvider) ASRCapability() *domain.Capability {
	if d.ASREnabled != nil && !d.ASREnabled() {
		return nil
	}
	p := d.get()
	if p == nil {
		return nil
	}
	return p.ASRCapability()
}
func (d *DynamicProvider) TTSCapability() *domain.Capability {
	if d.TTSEnabled != nil && !d.TTSEnabled() {
		return nil
	}
	p := d.get()
	if p == nil {
		return nil
	}
	return p.TTSCapability()
}
func (d *DynamicProvider) Transcribe(c context.Context, a domain.Audio) (domain.Transcript, error) {
	p := d.get()
	if p == nil {
		return domain.Transcript{}, domain.ErrUnavailable
	}
	return p.Transcribe(c, a)
}
func (d *DynamicProvider) Stream(c context.Context, r domain.StreamRequest, f <-chan []byte, e func(domain.Event) error) (domain.Transcript, error) {
	p := d.get()
	if p == nil {
		return domain.Transcript{}, domain.ErrUnavailable
	}
	return p.Stream(c, r, f, e)
}
func (d *DynamicProvider) Synthesize(c context.Context, r domain.Synthesis) (io.ReadCloser, string, error) {
	p := d.get()
	if p == nil {
		return nil, "", domain.ErrUnavailable
	}
	return p.Synthesize(c, r)
}
