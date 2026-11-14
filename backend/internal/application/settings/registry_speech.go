package settings

import (
	"math"
	"net/url"
	"strconv"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

func speechSettingSpecs() []settingSpec {
	return []settingSpec{
		{Namespace: "speech", Key: "input_provider", ValueType: "string", Default: "browser", Description: "全站统一语音输入来源", Validate: oneOf("browser", "doubao-ime", "wetype", "deepseek"), Apply: applyField(func(c *config.Config) *string { return &c.Speech.InputProvider }, trimmedText)},
		{Namespace: "speech", Key: "charge_asr", ValueType: "bool", Default: "false", Description: "向用户收取语音输入费用", Validate: boolValue(), Apply: applyField(func(c *config.Config) *bool { return &c.Speech.ChargeASR }, toBool)},
		{Namespace: "speech", Key: "charge_tts", ValueType: "bool", Default: "false", Description: "向用户收取朗读费用", Validate: boolValue(), Apply: applyField(func(c *config.Config) *bool { return &c.Speech.ChargeTTS }, toBool)},
		{Namespace: "speech", Key: "deepseek_tts_enabled", ValueType: "bool", Default: "false", Description: "启用 DeepSeek 朗读回复", Validate: boolValue(), Apply: applyField(func(c *config.Config) *bool { return &c.Speech.DeepSeekTTSEnabled }, toBool)},
		{Namespace: "speech", Key: "deepseek_base_url", ValueType: "string", Default: "https://api.deepseek.com", Description: "DeepSeek 语音服务地址", Validate: validateSpeechBaseURL, Apply: applyField(func(c *config.Config) *string { return &c.Speech.DeepSeekBaseURL }, trimmedText)},
		{Namespace: "speech", Key: "deepseek_api_key", ValueType: "string", Default: "", Description: "DeepSeek API Key", Sensitive: true, Validate: maxLength(4096), Apply: applyField(func(c *config.Config) *string { return &c.Speech.DeepSeekAPIKey }, trimmedText)},
		{Namespace: "speech", Key: "max_recording_seconds", ValueType: "int", Default: "120", Description: "单段录音时长上限（秒）", Validate: intRange(10, 1800), Apply: applyField(func(c *config.Config) *int { return &c.Speech.MaxRecordingSeconds }, toInt)},
		{Namespace: "speech", Key: "max_concurrent_per_user", ValueType: "int", Default: "2", Description: "每用户同时进行的语音请求上限", Validate: intRange(1, 8), Apply: applyField(func(c *config.Config) *int { return &c.Speech.MaxConcurrentPerUser }, toInt)},
		{Namespace: "speech", Key: "cache_max_bytes", ValueType: "int", Default: "268435456", Description: "语音缓存总大小上限（字节）", Validate: int64Min(1), Apply: applyField(func(c *config.Config) *int64 { return &c.Speech.CacheMaxBytes }, toInt64)},
		{Namespace: "speech", Key: "cache_ttl_hours", ValueType: "int", Default: "72", Description: "语音缓存保留时长（小时）", Validate: intRange(1, 8760), Apply: applyField(func(c *config.Config) *int { return &c.Speech.CacheTTLHours }, toInt)},
		{Namespace: "speech", Key: "asr_cny_per_hour", ValueType: "string", Default: "0.39", Description: "语音输入费率（元/小时）", Validate: speechPrice(), Apply: applyField(func(c *config.Config) *float64 { return &c.Speech.ASRCNYPerHour }, toFloat)},
		{Namespace: "speech", Key: "tts_cny_per_10000_characters", ValueType: "string", Default: "1.5", Description: "DeepSeek 朗读费率（元/万计费字符）", Validate: speechPrice(), Apply: applyField(func(c *config.Config) *float64 { return &c.Speech.TTSCNYPer10000Characters }, toFloat)},
	}
}

func speechPrice() settingValidator {
	return func(value, key string) error {
		price, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(price) || math.IsInf(price, 0) || price < 0 || price > 1000000 {
			return settingRule("float_range", "0,1000000")
		}
		return nil
	}
}

func validateSpeechBaseURL(value, key string) error {
	if value == "" {
		return settingRule("required", "")
	}
	if err := maxLength(2048)(value, key); err != nil {
		return err
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return settingRule("trusted_http_url", "")
	}
	return optionalTrustedHTTPURL()(value, key)
}
