package inputlimits

// ProviderRules are verified service constraints, separate from administrator
// budgets. Zero means no verified constraint is represented here. Unknown model
// windows use conservative Claude counts until capability data is available.
type ProviderRules struct {
	RequestBytes              int64
	ImageCount                int
	ImageEncodedBytes         int64
	ImageMaxDimension         int
	PDFSingleBytes            int64
	PDFBytes                  int64
	PDFPages                  int
	ConservativeUnknownWindow bool
}

// Provider returns only verified first-party constraints. Protocol-compatible
// third-party endpoints use administrator budgets, not first-party guarantees.
// Claude's >20-image 3000px guidance is a cross-platform safe recommendation,
// not a documented uniform first-party hard limit, so it is not enforced here.
// Sources checked 2026-11-04:
// https://platform.claude.com/docs/en/build-with-claude/vision#request-limits
// https://platform.claude.com/docs/en/build-with-claude/pdf-support
func Provider(protocol, baseURL string, contextWindowTokens int) ProviderRules {
	switch Profile(protocol, baseURL) {
	case "anthropic":
		rules := ProviderRules{RequestBytes: 128_000_000, ImageCount: 600, ImageEncodedBytes: 10_000_000, ImageMaxDimension: 8000, PDFPages: 600}
		if contextWindowTokens <= 0 {
			rules.ImageCount, rules.PDFPages, rules.ConservativeUnknownWindow = 100, 100, true
		} else {
			if contextWindowTokens == 200_000 {
				rules.ImageCount = 100
			}
			if contextWindowTokens < 1_000_000 {
				rules.PDFPages = 100
			}
		}
		return rules
	case "openai":
		return ProviderRules{RequestBytes: 512_000_000, ImageCount: 1500, PDFSingleBytes: 49_999_999, PDFBytes: 50_000_000}
	default:
		return ProviderRules{}
	}
}
