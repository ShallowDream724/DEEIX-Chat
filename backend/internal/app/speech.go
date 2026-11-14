package app

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/billing"
	appspeech "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/speech"
	domainspeech "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	infraspeech "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/speech"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/security"
	speechhttp "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/speech"
)

func newSpeechModule(runtime *config.Runtime, billingService *billing.Service) (*appspeech.Service, *speechhttp.Module, *http.Client, error) {
	initial := runtime.Snapshot()
	store, err := infraspeech.NewFileStore(filepath.Join(initial.StorageRootDir, "speech"), initial.Speech.CacheMaxBytes, time.Duration(initial.Speech.CacheTTLHours)*time.Hour)
	if err != nil {
		return nil, nil, nil, err
	}
	client := security.NewOutboundHTTPClient(initial.TrustedOutboundPolicy(), 35*time.Minute)
	doubao := infraspeech.NewDoubao(infraspeech.IMEConfig{Identities: store, HTTPClient: client})
	wetype := infraspeech.NewWeType(infraspeech.IMEConfig{Identities: store, HTTPClient: client})
	providers := map[string]domainspeech.Provider{
		"deepseek": &infraspeech.DynamicProvider{ResolveSnapshot: func() (domainspeech.Provider, bool, bool) {
			cfg := runtime.Snapshot().Speech
			if !cfg.Enabled || strings.TrimSpace(cfg.DeepSeekAPIKey) == "" {
				return nil, false, false
			}
			return infraspeech.NewDeepSeek(infraspeech.ProviderConfig{BaseURL: cfg.DeepSeekBaseURL, APIKey: cfg.DeepSeekAPIKey}, client), cfg.DeepSeekASREnabled, cfg.DeepSeekTTSEnabled
		}},
		"doubao-ime": &infraspeech.DynamicProvider{ResolveSnapshot: func() (domainspeech.Provider, bool, bool) {
			cfg := runtime.Snapshot().Speech
			return doubao, cfg.Enabled && cfg.DoubaoEnabled, false
		}},
		"wetype": &infraspeech.DynamicProvider{ResolveSnapshot: func() (domainspeech.Provider, bool, bool) {
			cfg := runtime.Snapshot().Speech
			return wetype, cfg.Enabled && cfg.WeTypeEnabled, false
		}},
	}
	accounting := speechBillingAdapter{billing: billingService, runtime: runtime}
	service := appspeech.NewService(func() appspeech.Config {
		cfg := runtime.Snapshot().Speech
		store.Configure(cfg.CacheMaxBytes, time.Duration(cfg.CacheTTLHours)*time.Hour)
		return appspeech.Config{MaxRecordingSeconds: cfg.MaxRecordingSeconds, MaxConcurrentPerUser: cfg.MaxConcurrentPerUser, MaxConcurrent: 32, MaxAudioBytes: 8 << 20}
	}, providers, accounting, accounting, store)
	handler := speechhttp.NewHandler(service, func() []string {
		return strings.Split(runtime.Snapshot().CORSAllowOrigin, ",")
	})
	return service, speechhttp.NewModule(handler), client, nil
}
