package usersettings

import "testing"

func TestSpeechPreferencesValidateWithoutEnablingPaidSpeechByDefault(t *testing.T) {
	if allowedKeys["chat.speech_input_provider"] != "browser" || allowedKeys["chat.auto_read_replies"] != "false" {
		t.Fatal("speech defaults changed")
	}
	for _, provider := range []string{"browser", "deepseek", "doubao-ime", "wetype"} {
		if err := validateValue("chat.speech_input_provider", provider); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ key, value string }{
		{"chat.speech_input_provider", "arbitrary"}, {"chat.auto_read_replies", "yes"},
		{"chat.speech_voice", ""}, {"chat.speech_voice", "tide\x00"},
	} {
		if err := validateValue(item.key, item.value); err == nil {
			t.Fatalf("accepted %q=%q", item.key, item.value)
		}
	}
	for _, voice := range []string{"stella", "tide", "future-voice_2"} {
		if err := validateValue("chat.speech_voice", voice); err != nil {
			t.Fatal(err)
		}
	}
}
