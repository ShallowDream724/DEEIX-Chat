package config

// SpeechConfig is copied with the runtime snapshot so each speech request uses
// one consistent set of service limits, credentials and prices.
type SpeechConfig struct {
	InputProvider            string
	ChargeASR                bool
	ChargeTTS                bool
	DeepSeekTTSEnabled       bool
	DeepSeekBaseURL          string
	DeepSeekAPIKey           string
	MaxRecordingSeconds      int
	MaxConcurrentPerUser     int
	CacheMaxBytes            int64
	CacheTTLHours            int
	ASRCNYPerHour            float64
	TTSCNYPer10000Characters float64
}
