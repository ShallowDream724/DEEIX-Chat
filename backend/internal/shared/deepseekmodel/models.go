// Package deepseekmodel identifies documented upstream model families.
package deepseekmodel

import "strings"

// IsFlash includes the legacy names that DeepSeek now serves using Flash.
// https://api-docs.deepseek.com/quick_start/pricing/
func IsFlash(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp":
		return true
	default:
		return false
	}
}

func IsPro(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "deepseek-v4-pro")
}
