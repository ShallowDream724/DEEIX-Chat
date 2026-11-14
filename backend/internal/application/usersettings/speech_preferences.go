package usersettings

import (
	"encoding/json"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Preferences are scoped by provider so switching the site channel does not
// erase a user's voice or apply one provider's controls to another provider.
// Omitted fields inherit the capability defaults; an empty style is intentional.
type speechPreference struct {
	Voice *string `json:"voice,omitempty"`
	Speed *string `json:"speed,omitempty"`
	Style *string `json:"style,omitempty"`
}

func validateSpeechPreferences(value string) error {
	invalid := func() error { return settingValidationError(ErrInvalidSettingValue, "invalid speech preferences") }
	if len(value) > 16<<10 || !utf8.ValidString(value) {
		return invalid()
	}
	var preferences map[string]speechPreference
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&preferences) != nil || preferences == nil || len(preferences) > 8 {
		return invalid()
	}
	if decoder.Decode(new(any)) != io.EOF {
		return invalid()
	}
	identifier := func(v string, max int) bool {
		if len(v) == 0 || len(v) > max {
			return false
		}
		for _, r := range v {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
				return false
			}
		}
		return true
	}
	for provider, preference := range preferences {
		if !identifier(provider, 64) {
			return invalid()
		}
		if preference.Voice != nil {
			voice := *preference.Voice
			if strings.TrimSpace(voice) != voice || voice == "" || utf8.RuneCountInString(voice) > 80 {
				return invalid()
			}
			for _, r := range voice {
				if unicode.IsControl(r) {
					return invalid()
				}
			}
		}
		if preference.Speed != nil && !identifier(*preference.Speed, 32) {
			return invalid()
		}
		if preference.Style != nil {
			if utf8.RuneCountInString(*preference.Style) > 500 {
				return invalid()
			}
			for _, r := range *preference.Style {
				if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
					return invalid()
				}
			}
		}
	}
	return nil
}
