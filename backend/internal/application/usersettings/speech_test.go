package usersettings

import (
	"context"
	"errors"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/usersettings"
	"testing"
)

func TestSpeechPreferencesValidateWithoutEnablingPaidSpeechByDefault(t *testing.T) {
	if _, ok := allowedKeys["chat.speech_input_provider"]; ok {
		t.Fatal("input provider is administrator-controlled")
	}
	if allowedKeys["chat.auto_read_replies"] != "false" {
		t.Fatal("speech defaults changed")
	}
	for _, item := range []struct{ key, value string }{
		{"chat.auto_read_replies", "yes"},
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

type obsoleteSpeechSettingsRepo struct {
	rows    []domain.UserSetting
	deleted bool
}

func (r *obsoleteSpeechSettingsRepo) ListByUserID(context.Context, uint) ([]domain.UserSetting, error) {
	return r.rows, nil
}
func (r *obsoleteSpeechSettingsRepo) Upsert(context.Context, []domain.UserSetting) error { return nil }
func (r *obsoleteSpeechSettingsRepo) Delete(ctx context.Context, user uint, key string) error {
	if user != 1 || key != "chat.speech_input_provider" {
		return errors.New("wrong deletion scope")
	}
	r.deleted = true
	return nil
}
func TestLegacyUserInputProviderIsRemovedAndCannotOverrideSite(t *testing.T) {
	repo := &obsoleteSpeechSettingsRepo{rows: []domain.UserSetting{{UserID: 1, Key: "chat.speech_input_provider", Value: "deepseek"}, {UserID: 1, Key: "chat.speech_voice", Value: "tide"}}}
	service := NewService(repo)
	values, e := service.ListSettings(t.Context(), 1)
	if e != nil || !repo.deleted {
		t.Fatal("obsolete provider not cleaned", e)
	}
	if _, ok := values["chat.speech_input_provider"]; ok {
		t.Fatal("provider preference leaked back to client")
	}
	if values["chat.speech_voice"] != "tide" {
		t.Fatal("independent TTS preference changed")
	}
	if _, e = service.PatchSettings(t.Context(), 1, map[string]string{"chat.speech_input_provider": "wetype"}); !errors.Is(e, ErrUnknownSetting) {
		t.Fatal("user could set input provider", e)
	}
}
