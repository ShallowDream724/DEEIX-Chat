package config

// SpeechConfig is copied with the runtime snapshot so each speech request uses
// one consistent set of service limits, credentials and prices.
type SpeechConfig struct {
	InputProvider                string
	TTSProvider                  string
	DeepSeekASRCredentialsSource string
	DeepSeekTTSCredentialsSource string
	DeepSeekASRBaseURL           string
	DeepSeekASRAPIKey            string
	DeepSeekTTSBaseURL           string
	DeepSeekTTSAPIKey            string
	ChargeASR                    bool
	ChargeTTS                    bool
	DeepSeekTTSEnabled           bool
	DeepSeekBaseURL              string
	DeepSeekAPIKey               string
	MaxRecordingSeconds          int
	MaxConcurrentPerUser         int
	CacheMaxBytes                int64
	CacheTTLHours                int
	ASRCNYPerHour                float64
	TTSCNYPer10000Characters     float64
}

// DeepSeekConnection resolves a role's saved connection without recursively
// following peer references. Validation rejects the only possible cycle.
func (c SpeechConfig) DeepSeekConnection(operation string) (string, string) {
	if c.DeepSeekASRCredentialsSource == "" && c.DeepSeekTTSCredentialsSource == "" && c.DeepSeekASRAPIKey == "" && c.DeepSeekTTSAPIKey == "" {
		return c.DeepSeekBaseURL, c.DeepSeekAPIKey
	}
	if c.DeepSeekASRCredentialsSource == "tts" && c.DeepSeekTTSCredentialsSource == "asr" {
		return "", ""
	}
	if operation == "asr" && c.DeepSeekASRCredentialsSource != "tts" || operation == "tts" && c.DeepSeekTTSCredentialsSource == "asr" {
		return c.DeepSeekASRBaseURL, c.DeepSeekASRAPIKey
	}
	return c.DeepSeekTTSBaseURL, c.DeepSeekTTSAPIKey
}
