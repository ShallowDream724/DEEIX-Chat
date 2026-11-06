package billing

import (
	"context"
	"errors"
	"math"
	"math/big"
	"strings"

	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/personalprovider"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/imagepricing"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/imageusage"
)

func parseImagePricing(raw string) (*imagepricing.Config, error) { return imagepricing.Parse(raw) }
func normalizeImagePricingJSON(raw string) (string, error)       { return imagepricing.Normalize(raw) }

func (s *Service) freezeModelImagePricing(ctx context.Context, auth *domainbilling.UsageAuthorization, model string) error {
	if personalprovider.IsModelRef(model) {
		return nil
	}
	auth.ModelImagePricing = &domainbilling.ImagePricingSnapshot{PlatformModelName: strings.TrimSpace(model)}
	pricing, err := s.getResolvedModelPricing(ctx, model)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if pricing == nil {
		return nil
	}
	config, err := parseImagePricing(pricing.ImagePricingJSON)
	if err != nil {
		return ErrInvalidModelPricing
	}
	if config != nil {
		if domainbilling.NormalizePricingMode(pricing.PricingMode) != domainbilling.PricingModeToken {
			return ErrInvalidModelPricing
		}
	}
	// Freeze absence too: enabling modality prices during a running legacy
	// request must not change the policy used to settle that request.
	auth.ModelImagePricing = &domainbilling.ImagePricingSnapshot{PlatformModelName: strings.TrimSpace(model), ImagePricingJSON: pricing.ImagePricingJSON, SchedulePricingJSON: pricing.SchedulePricingJSON, IsFree: pricing.IsFree}
	return nil
}

// AuthorizeImageTool fixes the image model selected by administrator policy.
// Token prices were already frozen with the native tool authorization.
func (s *Service) AuthorizeImageTool(auth *domainbilling.UsageAuthorization, selectedModel string) (int64, error) {
	if auth == nil || !auth.NativeToolKeys["openai.image_generation"] {
		return 0, nil
	}
	price, ok := auth.NativeToolPricing["openai.image_generation"]
	if !ok {
		return 0, nil
	}
	selectedModel = strings.TrimSpace(selectedModel)
	if price.ImageModel != "" && price.ImageModel != selectedModel {
		return 0, ErrModelPricingRequired
	}
	price.ImageModel = selectedModel
	auth.NativeToolPricing["openai.image_generation"] = price
	if !auth.NativeToolBillingEnabled || auth.Mode == "self" || price.ImagePricingJSON == "" {
		return 0, nil
	}
	config, err := parseImagePricing(price.ImagePricingJSON)
	if err != nil || config == nil || selectedModel == "" {
		return 0, ErrModelPricingRequired
	}
	return imagePricingEstimate(*config, 0, 0, billingRateMultiplier{Numerator: 1, Denominator: 1}), nil
}

// A conservative shape estimate (not an upstream token limit) prevents empty
// legacy media estimates from bypassing reservation. Real request estimates
// larger than these allowances continue to raise the reservation.
const imageEstimateTokenAllowance int64 = 32768

func imagePricingEstimate(config imagepricing.Config, inputTokens, outputTokens int64, multiplier billingRateMultiplier) int64 {
	rates := config.Rates()
	inputRate := max(rates.TextInput, rates.ImageInput, rates.CachedTextInput, rates.CachedImageInput)
	outputRate := max(rates.ImageOutput, rates.TextOutput)
	inputRate, okInput := imageScaledRate(inputRate, multiplier)
	outputRate, okOutput := imageScaledRate(outputRate, multiplier)
	if !okInput || !okOutput {
		return math.MaxInt64
	}
	total := new(big.Int)
	for _, item := range [][2]int64{{max(inputTokens, imageEstimateTokenAllowance), inputRate}, {max(outputTokens, imageEstimateTokenAllowance), outputRate}} {
		value := new(big.Int).Mul(big.NewInt(item[0]), big.NewInt(item[1]))
		value.Add(value, big.NewInt(500000)).Quo(value, big.NewInt(1000000))
		total.Add(total, value)
	}
	if !total.IsInt64() {
		return math.MaxInt64
	}
	return total.Int64()
}

func imageScaledRate(rate int64, multiplier billingRateMultiplier) (int64, bool) {
	multiplier = normalizeBillingRateMultiplier(multiplier)
	value := new(big.Int).Mul(big.NewInt(rate), big.NewInt(multiplier.Numerator))
	value.Add(value, big.NewInt(multiplier.Denominator/2)).Quo(value, big.NewInt(multiplier.Denominator))
	return value.Int64(), value.IsInt64()
}

type imageTokenCost struct {
	TextInput        int64 `json:"text_input_billed_nanousd"`
	ImageInput       int64 `json:"image_input_billed_nanousd"`
	CachedTextInput  int64 `json:"cached_text_input_billed_nanousd"`
	CachedImageInput int64 `json:"cached_image_input_billed_nanousd"`
	ImageOutput      int64 `json:"image_output_billed_nanousd"`
	TextOutput       int64 `json:"text_output_billed_nanousd"`
	Total            int64 `json:"billed_nanousd"`
}

func imageCost(usage imageusage.Usage, config imagepricing.Config, multiplier billingRateMultiplier) (imageTokenCost, error) {
	var cost imageTokenCost
	if !usage.Present || !usage.Complete {
		return cost, ErrModelPricingRequired
	}
	for _, value := range []int64{usage.TextInputTokens, usage.ImageInputTokens, usage.CachedTextInputTokens, usage.CachedImageInputTokens, usage.ImageOutputTokens, usage.TextOutputTokens} {
		if value < 0 {
			return cost, ErrModelPricingRequired
		}
	}
	if usage.CachedTextInputTokens > usage.TextInputTokens || usage.CachedImageInputTokens > usage.ImageInputTokens {
		return cost, ErrModelPricingRequired
	}
	// Positive cached counts are observations even if another aggregated call
	// omitted its cache breakdown. Only the remaining input uses uncached rates.
	cachedText, cachedImage := usage.CachedTextInputTokens, usage.CachedImageInputTokens
	rates := config.Rates()
	quantities := []int64{usage.TextInputTokens - cachedText, usage.ImageInputTokens - cachedImage, cachedText, cachedImage, usage.ImageOutputTokens, usage.TextOutputTokens}
	prices := []int64{rates.TextInput, rates.ImageInput, rates.CachedTextInput, rates.CachedImageInput, rates.ImageOutput, rates.TextOutput}
	targets := []*int64{&cost.TextInput, &cost.ImageInput, &cost.CachedTextInput, &cost.CachedImageInput, &cost.ImageOutput, &cost.TextOutput}
	for index, quantity := range quantities {
		// Integer arithmetic avoids loss of precision and detects overflow in
		// untrusted upstream counts before a financial amount is recorded.
		rate, ok := imageScaledRate(prices[index], multiplier)
		if !ok {
			return imageTokenCost{}, ErrModelPricingRequired
		}
		amount := new(big.Int).Mul(big.NewInt(quantity), big.NewInt(rate))
		amount.Add(amount, big.NewInt(500000))
		amount.Quo(amount, big.NewInt(1000000))
		if !amount.IsInt64() || amount.Int64() > math.MaxInt64-cost.Total {
			return imageTokenCost{}, ErrModelPricingRequired
		}
		*targets[index] = amount.Int64()
		cost.Total += amount.Int64()
	}
	return cost, nil
}

func (s *Service) buildImageToolServiceItem(input UsagePricingInput, mode string) (*domainbilling.UsageServiceItem, *imageTokenCost, error) {
	auth := input.Authorization
	if auth == nil || mode == "self" || !auth.NativeToolBillingEnabled || !auth.NativeToolKeys["openai.image_generation"] {
		return nil, nil, nil
	}
	price := auth.NativeToolPricing["openai.image_generation"]
	config, err := parseImagePricing(price.ImagePricingJSON)
	if err != nil {
		return nil, nil, ErrModelPricingRequired
	}
	if config == nil || (!input.ImageToolUsage.Present && input.ServerSideToolUsage["openai.image_generation"] <= 0) {
		return nil, nil, nil
	}
	if price.ImageModel == "" || (input.ImageToolUsage.Model != "" && input.ImageToolUsage.Model != price.ImageModel) {
		return nil, nil, ErrModelPricingRequired
	}
	cost, err := imageCost(input.ImageToolUsage, *config, billingRateMultiplier{Numerator: 1, Denominator: 1})
	if err != nil {
		return nil, nil, err
	}
	u := input.ImageToolUsage
	return &domainbilling.UsageServiceItem{ServiceCode: "native_tool.openai.image_generation", ServiceName: price.ServiceName, PlatformModelName: price.ImageModel, ProviderProtocol: input.ProviderProtocol, RateMultiplier: 1, PricingMode: "token", InputTokens: u.TextInputTokens + u.ImageInputTokens - u.CachedTextInputTokens - u.CachedImageInputTokens, CacheReadTokens: u.CachedTextInputTokens + u.CachedImageInputTokens, OutputTokens: u.ImageOutputTokens + u.TextOutputTokens, InputBilledNanousd: cost.TextInput + cost.ImageInput, CacheReadBilledNanousd: cost.CachedTextInput + cost.CachedImageInput, OutputBilledNanousd: cost.ImageOutput + cost.TextOutput, BilledNanousd: cost.Total}, &cost, nil
}
