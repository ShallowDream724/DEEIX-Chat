package billing

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/imageusage"
)

const testImagePricing = `{"textInputUSDPerMTokens":5,"imageInputUSDPerMTokens":8,"cachedTextInputUSDPerMTokens":1.25,"cachedImageInputUSDPerMTokens":2,"imageOutputUSDPerMTokens":30}`

func imageUsageFixture() imageusage.Usage {
	return imageusage.Usage{Present: true, Complete: true, CacheKnown: true, TextInputTokens: 2000, ImageInputTokens: 2000, CachedTextInputTokens: 1000, CachedImageInputTokens: 1000, ImageOutputTokens: 1000}
}

func TestImagePricingSeparatesInputsCacheAndOutputs(t *testing.T) {
	config, err := parseImagePricing(testImagePricing)
	if err != nil {
		t.Fatal(err)
	}
	usage := imageUsageFixture()
	cost, err := imageCost(usage, *config, billingRateMultiplier{Numerator: 1, Denominator: 1})
	if err != nil || cost.Total != 46_250_000 || cost.TextInput != 5_000_000 || cost.ImageInput != 8_000_000 || cost.CachedTextInput != 1_250_000 || cost.CachedImageInput != 2_000_000 || cost.ImageOutput != 30_000_000 {
		t.Fatalf("cost=%+v error=%v", cost, err)
	}
	usage.CacheKnown = false
	usage.CachedTextInputTokens, usage.CachedImageInputTokens = 0, 0
	cost, err = imageCost(usage, *config, billingRateMultiplier{Numerator: 1, Denominator: 1})
	if err != nil || cost.Total != 56_000_000 {
		t.Fatalf("unknown cache cost=%+v error=%v", cost, err)
	}
	config.TextOutputUSDPerMTokens = 10
	usage.TextOutputTokens = 100
	cost, err = imageCost(usage, *config, billingRateMultiplier{Numerator: 1, Denominator: 1})
	if err != nil || cost.TextOutput != 1_000_000 || cost.ImageOutput != 30_000_000 {
		t.Fatalf("text output cost=%+v error=%v", cost, err)
	}
}

func TestImagePricingRejectsIncompleteInvalidAndOverflowUsage(t *testing.T) {
	config, _ := parseImagePricing(testImagePricing)
	for _, mutate := range []func(*imageusage.Usage){
		func(u *imageusage.Usage) { u.Complete = false },
		func(u *imageusage.Usage) { u.Present = false },
		func(u *imageusage.Usage) { u.CachedTextInputTokens = u.TextInputTokens + 1 },
		func(u *imageusage.Usage) { u.ImageInputTokens = -1 },
		func(u *imageusage.Usage) { u.ImageOutputTokens = math.MaxInt64 },
	} {
		u := imageUsageFixture()
		mutate(&u)
		if _, err := imageCost(u, *config, billingRateMultiplier{Numerator: 1, Denominator: 1}); !errors.Is(err, ErrModelPricingRequired) {
			t.Fatalf("usage=%+v err=%v", u, err)
		}
	}
}

func TestImageModelPricingFreezesAndReplacesLegacyTokenRates(t *testing.T) {
	repo := &billingRepositoryStub{mode: "usage", pricing: &domainbilling.ModelPricing{PlatformModelName: "image", PricingMode: "token", ImagePricingJSON: testImagePricing, InputNanousdPerMTokens: 999_000_000_000, OutputNanousdPerMTokens: 999_000_000_000}}
	svc := NewService(repo)
	auth, err := svc.AuthorizeUsage(t.Context(), 42, "image", "image-run")
	if err != nil {
		t.Fatal(err)
	}
	repo.pricing.ImagePricingJSON = `{"textInputUSDPerMTokens":99,"imageInputUSDPerMTokens":99,"cachedTextInputUSDPerMTokens":99,"cachedImageInputUSDPerMTokens":99,"imageOutputUSDPerMTokens":99}`
	repo.pricing.PricingMode = "call"
	repo.pricing.CallNanousdPerCall = 999_000_000
	repo.pricing.IsFree = true
	repo.pricing.SchedulePricingJSON = `{"periods":[{"name":"new-price","weekdays":[0,1,2,3,4,5,6],"start":"00:00","end":"23:59","ratePercent":1000}]}`
	ledger, err := svc.BuildUsageLedger(t.Context(), UsagePricingInput{Authorization: auth, UserID: 42, PlatformModelName: "image", InputTokens: 4000, OutputTokens: 1000, ImageUsage: imageUsageFixture()})
	if err != nil || ledger.BilledNanousd != 46_250_000 {
		t.Fatalf("ledger=%+v err=%v", ledger, err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(ledger.PricingSnapshotJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["image_pricing_json"] != testImagePricing || snapshot["image_token_cost"] == nil {
		t.Fatalf("snapshot=%v", snapshot)
	}
	_, err = svc.BuildUsageLedger(t.Context(), UsagePricingInput{Authorization: auth, UserID: 42, PlatformModelName: "other", ImageUsage: imageUsageFixture()})
	if !errors.Is(err, ErrModelPricingRequired) {
		t.Fatalf("model mismatch err=%v", err)
	}
	repo.pricing.IsFree = false
	if estimate, err := svc.EstimateUsageNanousd(t.Context(), 42, UsageEstimateInput{PlatformModelName: "image"}); err != nil || estimate <= 0 {
		t.Fatalf("estimate=%d err=%v", estimate, err)
	}
}

func TestImageNativeToolPricingAuthorizesModelAndDoesNotDoubleCharge(t *testing.T) {
	encoded, _ := json.Marshal(map[string]any{"openai.image_generation": map[string]any{"priceNanousd": 777_000_000, "imagePricingJSON": testImagePricing}})
	repo := &billingRepositoryStub{mode: "usage", nativeToolBillingEnabled: true, nativeToolPricingJSON: string(encoded), pricing: &domainbilling.ModelPricing{PlatformModelName: "main", PricingMode: "token", InputNanousdPerMTokens: 1_000_000_000}}
	svc := NewService(repo)
	auth, err := svc.AuthorizeUsage(t.Context(), 42, "main", "tool-run")
	if err != nil {
		t.Fatal(err)
	}
	svc.AuthorizeNativeTools(auth, map[string]string{"image_generation": "openai.image_generation"})
	if _, err := svc.AuthorizeImageTool(auth, ""); !errors.Is(err, ErrModelPricingRequired) {
		t.Fatalf("blank model err=%v", err)
	}
	if estimate, err := svc.AuthorizeImageTool(auth, "gpt-image-2.6"); err != nil || estimate <= 0 {
		t.Fatalf("estimate=%d err=%v", estimate, err)
	}
	repo.nativeToolPricingJSON = "{}"
	usage := imageUsageFixture()
	usage.Model = "gpt-image-2.6"
	input := UsagePricingInput{Authorization: auth, UserID: 42, PlatformModelName: "main", InputTokens: 1000, ImageToolUsage: usage, ServerSideToolUsage: map[string]int64{"openai.image_generation": 1}}
	ledger, err := svc.BuildUsageLedger(t.Context(), input)
	if err != nil || ledger.BilledNanousd != 47_250_000 {
		t.Fatalf("ledger=%+v err=%v", ledger, err)
	}
	input.ImageToolUsage.Model = "gpt-image-2.5"
	if _, err := svc.BuildUsageLedger(t.Context(), input); !errors.Is(err, ErrModelPricingRequired) {
		t.Fatalf("mismatch err=%v", err)
	}
	input.ImageToolUsage = imageusage.Usage{}
	if _, err := svc.BuildUsageLedger(t.Context(), input); !errors.Is(err, ErrModelPricingRequired) {
		t.Fatalf("missing usage err=%v", err)
	}
	auth.NativeToolBillingEnabled = false
	ledger, err = svc.BuildUsageLedger(t.Context(), input)
	if err != nil || ledger.BilledNanousd != 1_000_000 {
		t.Fatalf("disabled ledger=%+v err=%v", ledger, err)
	}
}

func TestImageUsageWithoutOptionalPricingKeepsLegacyCost(t *testing.T) {
	repo := &billingRepositoryStub{mode: "usage", pricing: &domainbilling.ModelPricing{PlatformModelName: "old", PricingMode: "call", CallNanousdPerCall: 25}}
	ledger, err := NewService(repo).BuildUsageLedger(t.Context(), UsagePricingInput{UserID: 42, PlatformModelName: "old", CallCount: 2, ImageUsage: imageUsageFixture()})
	if err != nil || ledger.BilledNanousd != 50 {
		t.Fatalf("ledger=%+v err=%v", ledger, err)
	}
}

func TestLegacyRunDoesNotEnableImagePolicyAfterAuthorization(t *testing.T) {
	repo := &billingRepositoryStub{mode: "usage", pricing: &domainbilling.ModelPricing{PlatformModelName: "legacy", PricingMode: "token", InputNanousdPerMTokens: 1_000_000_000}}
	svc := NewService(repo)
	auth, err := svc.AuthorizeUsage(t.Context(), 42, "legacy", "legacy-run")
	if err != nil {
		t.Fatal(err)
	}
	repo.pricing.ImagePricingJSON = testImagePricing
	ledger, err := svc.BuildUsageLedger(t.Context(), UsagePricingInput{Authorization: auth, UserID: 42, PlatformModelName: "legacy", InputTokens: 1000, ImageUsage: imageUsageFixture()})
	if err != nil || ledger.BilledNanousd != 1_000_000 {
		t.Fatalf("ledger=%+v err=%v", ledger, err)
	}
}
