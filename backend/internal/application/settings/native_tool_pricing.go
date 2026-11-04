package settings

import (
	"context"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/nativetool"
)

type nativeToolCatalogProvider interface {
	ListNativeToolDefinitions(context.Context) ([]nativetool.Definition, error)
}

// SetNativeToolCatalogProvider 注入与计费服务同源的原生工具目录。
func (s *Service) SetNativeToolCatalogProvider(provider nativeToolCatalogProvider) {
	s.nativeToolCatalog = provider
}

// validatePatchItem 在所有设置写入入口按当前目录校验原生工具价格，
// 其余配置及 Clear 语义仍由注册表处理。
func (s *Service) validatePatchItem(ctx context.Context, item PatchItem) error {
	if item.Namespace != "billing" || item.Key != "native_tool_pricing_json" || item.Clear || s.nativeToolCatalog == nil {
		return validatePatchItem(item)
	}
	definitions, err := s.nativeToolCatalog.ListNativeToolDefinitions(ctx)
	if err != nil {
		return err
	}
	if err := validateNativeToolPricingJSONWithParser(strings.TrimSpace(item.Value), func(value string) (map[string]nativetool.PricingOverride, error) {
		return nativetool.ParsePricingOverridesJSONForDefinitions(value, definitions)
	}); err != nil {
		return settingValidationForKey(item.Namespace, item.Key, err)
	}
	return nil
}
