package usersettings

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSpeechPreferencesPreserveProviderDefaultsAndUnicodeVoices(t *testing.T) {
	for _, value := range []string{`{}`, `{"mimo":{}}`, `{"mimo":{"voice":"冰糖","speed":"fast","style":""},"deepseek":{"voice":"tide"}}`, `{"mimo":{"style":"自然、亲切。\nEnglish clearly."}}`} {
		if err := validateValue("chat.speech_preferences", value); err != nil {
			t.Fatalf("valid preferences rejected: %s: %v", value, err)
		}
	}
	if allowedKeys["chat.speech_preferences"] != "{}" {
		t.Fatal("new users must inherit provider defaults")
	}
	for _, value := range []string{`null`, `[]`, `{} {}`, `{"mimo":{"voice":""}}`, `{"mimo":{"voice":"冰糖\u0000"}}`, `{"mimo":{"speed":123}}`, `{"mimo":{"key":"must-not-store-credentials"}}`, `{"bad provider":{}}`} {
		if err := validateValue("chat.speech_preferences", value); err == nil {
			t.Fatalf("invalid preferences accepted: %s", value)
		}
	}
	long, _ := json.Marshal(map[string]any{"mimo": map[string]string{"style": strings.Repeat("字", 501)}})
	if validateSpeechPreferences(string(long)) == nil {
		t.Fatal("unbounded style accepted")
	}
}
