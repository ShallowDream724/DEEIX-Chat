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
			roles := &infraspeech.RoleProvider{}
			asrURL, asrKey := cfg.DeepSeekConnection("asr")
			ttsURL, ttsKey := cfg.DeepSeekConnection("tts")
			if strings.TrimSpace(asrKey) != "" {
				roles.ASR = infraspeech.NewDeepSeek(infraspeech.ProviderConfig{BaseURL: asrURL, APIKey: asrKey}, client)
			}
			if cfg.TTSEnabled && (cfg.TTSProvider == "" || cfg.TTSProvider == "deepseek") && strings.TrimSpace(ttsKey) != "" {
				roles.TTS = infraspeech.NewDeepSeek(infraspeech.ProviderConfig{BaseURL: ttsURL, APIKey: ttsKey}, client)
			}
			return roles, roles.ASR != nil, roles.TTS != nil
		}},
		"mimo": &infraspeech.DynamicProvider{ResolveSnapshot: func() (domainspeech.Provider, bool, bool) {
			cfg := runtime.Snapshot().Speech
			if !cfg.TTSEnabled || cfg.TTSProvider != "mimo" || strings.TrimSpace(cfg.MiMoAPIKey) == "" {
				return nil, false, false
			}
			return infraspeech.NewMiMo(infraspeech.ProviderConfig{BaseURL: cfg.MiMoBaseURL, APIKey: cfg.MiMoAPIKey}, client), false, true
		}},
		"doubao-ime": &infraspeech.DynamicProvider{ResolveSnapshot: func() (domainspeech.Provider, bool, bool) {
			return doubao, true, false
		}},
		"wetype": &infraspeech.DynamicProvider{ResolveSnapshot: func() (domainspeech.Provider, bool, bool) {
			return wetype, true, false
		}},
	}
	accounting := speechBillingAdapter{billing: billingService, runtime: runtime}
	service := appspeech.NewService(func() appspeech.Config {
		cfg := runtime.Snapshot().Speech
		store.Configure(cfg.CacheMaxBytes, time.Duration(cfg.CacheTTLHours)*time.Hour)
		return appspeech.Config{SelectedInputProvider: cfg.InputProvider, SelectedTTSProvider: cfg.TTSProvider, MaxRecordingSeconds: cfg.MaxRecordingSeconds, MaxConcurrentPerUser: cfg.MaxConcurrentPerUser, MaxConcurrent: 32, MaxAudioBytes: 8 << 20}
	}, providers, accounting, accounting, store)
	handler := speechhttp.NewHandler(service, func() []string {
		return strings.Split(runtime.Snapshot().CORSAllowOrigin, ",")
	})
	return service, speechhttp.NewModule(handler), client, nil
}
