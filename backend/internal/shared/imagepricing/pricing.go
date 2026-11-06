// Package imagepricing validates the optional modality-specific token price
// configuration shared by image models and provider-native image tools.
package imagepricing

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
)

// Config contains USD prices per million tokens. All five prices are required;
// an explicit zero is a free category, while an empty object disables the policy.
type Config struct {
	TextInputUSDPerMTokens        float64 `json:"textInputUSDPerMTokens"`
	ImageInputUSDPerMTokens       float64 `json:"imageInputUSDPerMTokens"`
	CachedTextInputUSDPerMTokens  float64 `json:"cachedTextInputUSDPerMTokens"`
	CachedImageInputUSDPerMTokens float64 `json:"cachedImageInputUSDPerMTokens"`
	ImageOutputUSDPerMTokens      float64 `json:"imageOutputUSDPerMTokens"`
	TextOutputUSDPerMTokens       float64 `json:"textOutputUSDPerMTokens,omitempty"`
}

type Rates struct {
	TextInput, ImageInput, CachedTextInput, CachedImageInput, ImageOutput, TextOutput int64
}

var ErrInvalid = errors.New("image pricing must contain five nonnegative USD token prices")

func Parse(raw string) (*Config, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields == nil {
		return nil, ErrInvalid
	}
	if len(fields) == 0 {
		return nil, nil
	}
	keys := []string{"textInputUSDPerMTokens", "imageInputUSDPerMTokens", "cachedTextInputUSDPerMTokens", "cachedImageInputUSDPerMTokens", "imageOutputUSDPerMTokens"}
	if len(fields) != len(keys) && len(fields) != len(keys)+1 {
		return nil, ErrInvalid
	}
	for _, key := range keys {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrInvalid
		}
	}
	var config Config
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, ErrInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrInvalid
	}
	for _, value := range []float64{config.TextInputUSDPerMTokens, config.ImageInputUSDPerMTokens, config.CachedTextInputUSDPerMTokens, config.CachedImageInputUSDPerMTokens, config.ImageOutputUSDPerMTokens, config.TextOutputUSDPerMTokens} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= float64(math.MaxInt64)/1e9 {
			return nil, ErrInvalid
		}
	}
	return &config, nil
}

func Normalize(raw string) (string, error) {
	config, err := Parse(raw)
	if err != nil {
		return "", err
	}
	if config == nil {
		return "{}", nil
	}
	encoded, err := json.Marshal(config)
	return string(encoded), err
}

func (c Config) Rates() Rates {
	return Rates{TextInput: int64(math.Round(c.TextInputUSDPerMTokens * 1e9)), ImageInput: int64(math.Round(c.ImageInputUSDPerMTokens * 1e9)), CachedTextInput: int64(math.Round(c.CachedTextInputUSDPerMTokens * 1e9)), CachedImageInput: int64(math.Round(c.CachedImageInputUSDPerMTokens * 1e9)), ImageOutput: int64(math.Round(c.ImageOutputUSDPerMTokens * 1e9)), TextOutput: int64(math.Round(c.TextOutputUSDPerMTokens * 1e9))}
}
