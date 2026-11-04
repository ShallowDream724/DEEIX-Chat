package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/nativetool"
)

type nativeToolPricingRepo struct {
	testSettingsRepo
	writes int
}

func (r *nativeToolPricingRepo) Upsert(_ context.Context, items []domainsettings.SystemSetting) error {
	r.writes++
	for _, item := range items {
		rows := r.byNamespace[item.Namespace]
		found := false
		for i := range rows {
			if rows[i].Key == item.Key {
				rows[i] = item
				found = true
				break
			}
		}
		if !found {
			rows = append(rows, item)
		}
		r.byNamespace[item.Namespace] = rows
	}
	return nil
}

type nativeToolPricingCatalog struct {
	definitions []nativetool.Definition
	err         error
	calls       int
}

func (c *nativeToolPricingCatalog) ListNativeToolDefinitions(context.Context) ([]nativetool.Definition, error) {
	c.calls++
	return c.definitions, c.err
}

func newNativeToolPricingTestService() (*Service, *nativeToolPricingRepo, *nativeToolPricingCatalog) {
	repo := &nativeToolPricingRepo{testSettingsRepo: testSettingsRepo{
		byNamespace: map[string][]domainsettings.SystemSetting{},
	}}
	catalog := &nativeToolPricingCatalog{definitions: nativetool.MergeDefinitions(
		nativetool.DefinitionsFromCapabilitiesJSON(`{"nativeTools":[{"key":"deepseek.web_search","protocol":"openai_responses","provider":"DeepSeek","type":"web_search","billingUnit":"call"}]}`),
	)}
	service := NewService(repo, testKeyring())
	service.SetNativeToolCatalogProvider(catalog)
	return service, repo, catalog
}

func TestBatchUpdateNativeToolPricingUsesModelCatalog(t *testing.T) {
	service, repo, catalog := newNativeToolPricingTestService()
	// The billing endpoint normalizes prices against this same merged catalog
	// before passing the JSON to the shared settings write path.
	value, err := nativetool.PricingOverridesJSONForDefinitions(map[string]nativetool.PricingOverride{
		"openai.web_search":   {PriceNanousd: 100000000},
		"deepseek.web_search": {PriceNanousd: 0},
	}, catalog.definitions)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.BatchUpdate(t.Context(), []PatchItem{
		{Namespace: "billing", Key: "native_tool_billing_enabled", Value: "true"},
		{Namespace: "billing", Key: "native_tool_pricing_json", Value: value},
	})
	if err != nil {
		t.Fatalf("save prices from the model catalog: %v", err)
	}
	stored, err := service.RuntimeValuesByNamespace(t.Context(), "billing")
	if err != nil {
		t.Fatal(err)
	}
	if stored["native_tool_pricing_json"] != value || stored["native_tool_billing_enabled"] != "true" || repo.writes != 1 {
		t.Fatalf("prices or billing toggle not preserved: %#v (writes=%d)", stored, repo.writes)
	}
	// A direct settings update must support paid dynamic tools as well as free ones.
	paid := `{"deepseek.web_search":{"priceNanousd":1250000,"unit":"call","billable":true}}`
	if _, err = service.BatchUpdate(t.Context(), []PatchItem{{Namespace: "billing", Key: "native_tool_pricing_json", Value: paid}}); err != nil {
		t.Fatalf("save a dynamic price through settings: %v", err)
	}
	stored, err = service.RuntimeValuesByNamespace(t.Context(), "billing")
	if err != nil || stored["native_tool_pricing_json"] != paid {
		t.Fatalf("dynamic price not retained: %#v, %v", stored, err)
	}
}

func TestBatchUpdateNativeToolPricingRejectsInvalidValuesAtomically(t *testing.T) {
	for _, test := range []struct {
		name, value, rule string
		clear             bool
	}{
		{name: "unknown tool", value: `{"unknown.tool":{"priceNanousd":0}}`, rule: "native_tool_pricing"},
		{name: "negative price", value: `{"deepseek.web_search":{"priceNanousd":-1}}`, rule: "native_tool_pricing"},
		{name: "fractional price", value: `{"deepseek.web_search":{"priceNanousd":0.5}}`, rule: "native_tool_pricing"},
		{name: "malformed JSON", value: `{`, rule: "native_tool_pricing"},
		{name: "array", value: `[]`, rule: "native_tool_pricing"},
		{name: "empty", value: " ", rule: "required"},
		{name: "oversized", value: `{"deepseek.web_search":{"priceLabel":"` + strings.Repeat("x", 20000) + `"}}`, rule: "max_length"},
		{name: "clear", clear: true, rule: "clear_not_allowed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, repo, _ := newNativeToolPricingTestService()
			_, err := service.BatchUpdate(t.Context(), []PatchItem{
				{Namespace: "billing", Key: "native_tool_billing_enabled", Value: "false"},
				{Namespace: "billing", Key: "native_tool_pricing_json", Value: test.value, Clear: test.clear},
			})
			var validationErr *SettingValidationError
			if !errors.As(err, &validationErr) || validationErr.Details().Field != "billing:native_tool_pricing_json" || validationErr.Details().Rule != test.rule {
				t.Fatalf("expected %s validation error, got %v", test.rule, err)
			}
			if repo.writes != 0 {
				t.Fatal("invalid batch partially updated settings")
			}
		})
	}
}

func TestBatchUpdateNativeToolPricingCatalogFailureDoesNotWrite(t *testing.T) {
	service, repo, catalog := newNativeToolPricingTestService()
	catalog.err = errors.New("catalog unavailable")
	_, err := service.BatchUpdate(t.Context(), []PatchItem{{Namespace: "billing", Key: "native_tool_pricing_json", Value: `{}`}})
	if !errors.Is(err, catalog.err) || repo.writes != 0 {
		t.Fatalf("catalog failure must prevent writes, got %v (writes=%d)", err, repo.writes)
	}
	// An unavailable tool catalog must not block unrelated settings.
	if _, err = service.BatchUpdate(t.Context(), []PatchItem{{Namespace: "billing", Key: "native_tool_billing_enabled", Value: "false"}}); err != nil {
		t.Fatal(err)
	}
	if catalog.calls != 1 || repo.writes != 1 {
		t.Fatalf("unexpected catalog access or writes: %d, %d", catalog.calls, repo.writes)
	}
}

func TestBatchUpdateNativeToolPricingWithoutCatalogUsesBuiltins(t *testing.T) {
	service, repo, _ := newNativeToolPricingTestService()
	service.SetNativeToolCatalogProvider(nil)
	if _, err := service.BatchUpdate(t.Context(), []PatchItem{{Namespace: "billing", Key: "native_tool_pricing_json", Value: `{"openai.web_search":{"priceNanousd":1}}`}}); err != nil {
		t.Fatal(err)
	}
	_, err := service.BatchUpdate(t.Context(), []PatchItem{{Namespace: "billing", Key: "native_tool_pricing_json", Value: `{"deepseek.web_search":{"priceNanousd":0}}`}})
	if !errors.Is(err, ErrInvalidSetting) || repo.writes != 1 {
		t.Fatalf("undeclared tool should remain invalid without a catalog: %v", err)
	}
}
