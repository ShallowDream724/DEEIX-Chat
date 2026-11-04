package billing

import (
	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	"testing"
)

func TestNativeToolAuthorizationFreezesPricesAndRequiresSelectedIdentity(t *testing.T) {
	repo := &billingRepositoryStub{nativeToolBillingEnabled: true, nativeToolPricingJSON: `{"openai.web_search":{"priceNanousd":123,"unit":"call","billable":true}}`}
	service := NewService(repo)
	authorization := &domainbilling.UsageAuthorization{Mode: "usage"}
	if err := service.freezeNativeToolPricing(t.Context(), authorization); err != nil {
		t.Fatal(err)
	}
	if got := service.AuthorizeNativeTools(authorization, map[string]string{"web_search": "openai.web_search"}); got != 123 {
		t.Fatalf("reserved estimate = %d", got)
	}
	// Administrator changes and a different transport route cannot reprice the run.
	repo.nativeToolBillingEnabled = false
	repo.nativeToolPricingJSON = `{"openai.web_search":{"priceNanousd":999,"unit":"call","billable":true}}`
	items, total := buildNativeToolServiceItems(nativeToolServiceItemsInput{
		Usage:       UsagePricingInput{Authorization: authorization, ProviderProtocol: "xai_responses", ServerSideToolUsage: map[string]int64{"openai.web_search": 2, "xai.web_search": 3, "web_search": 4}},
		BillingMode: "usage", BillingEnabled: authorization.NativeToolBillingEnabled,
	})
	if total != 246 || len(items) != 1 {
		t.Fatalf("items=%#v total=%d", items, total)
	}
	if again := service.AuthorizeNativeTools(authorization, map[string]string{"web_search": "openai.web_search"}); again != 123 {
		t.Fatalf("retry duplicated estimate: %d", again)
	}
}

func TestNativeToolAuthorizationKeepsDisabledBillingFree(t *testing.T) {
	service := NewService(&billingRepositoryStub{})
	authorization := &domainbilling.UsageAuthorization{Mode: "usage"}
	if err := service.freezeNativeToolPricing(t.Context(), authorization); err != nil {
		t.Fatal(err)
	}
	if estimate := service.AuthorizeNativeTools(authorization, map[string]string{"web_search": "openai.web_search"}); estimate != 0 {
		t.Fatalf("estimate=%d", estimate)
	}
}

func TestPaidNativeToolOnFreeModelReservesAndBills(t *testing.T) {
	repo := &billingRepositoryStub{mode: "usage", nativeToolBillingEnabled: true, pricing: &domainbilling.ModelPricing{PlatformModelName: "free", IsFree: true}}
	service := NewService(repo)
	authorization, err := service.AuthorizeUsage(t.Context(), 42, "free", "native-free-run")
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Reservation != nil {
		t.Fatal("free model alone reserved budget")
	}
	estimate := service.AuthorizeNativeTools(authorization, map[string]string{"web_search": "openai.web_search"})
	if err := service.EnsureNativeToolReservation(t.Context(), authorization, estimate); err != nil {
		t.Fatal(err)
	}
	if authorization.Reservation == nil || authorization.Reservation.UserID != 42 || authorization.Reservation.RefNo != "native-free-run" {
		t.Fatalf("reservation=%#v", authorization.Reservation)
	}
	ledger, err := service.BuildUsageLedger(t.Context(), UsagePricingInput{Authorization: authorization, UserID: 42, PlatformModelName: "free", ProviderProtocol: "openai_responses", ServerSideToolUsage: map[string]int64{"openai.web_search": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.BilledNanousd != estimate*2 {
		t.Fatalf("billed=%d estimate=%d", ledger.BilledNanousd, estimate)
	}
}
