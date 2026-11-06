package imagepricing

import "testing"

func TestOptionalImagePricingRequiresEveryCategory(t *testing.T) {
	for _, raw := range []string{"", "{}", " { } "} {
		config, err := Parse(raw)
		if err != nil || config != nil {
			t.Fatalf("raw=%q config=%+v error=%v", raw, config, err)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"textInputUSDPerMTokens":5}`, `{"textInputUSDPerMTokens":5,"imageInputUSDPerMTokens":8,"cachedTextInputUSDPerMTokens":null,"cachedImageInputUSDPerMTokens":2,"imageOutputUSDPerMTokens":30}`, `{"textInputUSDPerMTokens":-1,"imageInputUSDPerMTokens":8,"cachedTextInputUSDPerMTokens":1.25,"cachedImageInputUSDPerMTokens":2,"imageOutputUSDPerMTokens":30}`} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("invalid raw accepted: %s", raw)
		}
	}
	config, err := Parse(`{"textInputUSDPerMTokens":0,"imageInputUSDPerMTokens":8,"cachedTextInputUSDPerMTokens":1.25,"cachedImageInputUSDPerMTokens":2,"imageOutputUSDPerMTokens":30,"textOutputUSDPerMTokens":10}`)
	if err != nil || config.Rates().TextInput != 0 || config.Rates().TextOutput != 10_000_000_000 {
		t.Fatalf("config=%+v err=%v", config, err)
	}
}
