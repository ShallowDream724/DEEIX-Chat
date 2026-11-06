package billing

import (
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
)

func TestModelImagePricingMigrationPreservesLegacyPriceAndRoundTrips(t *testing.T) {
	db := openBillingSQLiteTestDB(t)
	if err := db.Exec(`CREATE TABLE billing_model_prices (id integer PRIMARY KEY AUTOINCREMENT, platform_model_name text NOT NULL, pricing_mode text NOT NULL DEFAULT 'call', call_nanousd_per_call integer NOT NULL DEFAULT 0)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO billing_model_prices (platform_model_name, call_nanousd_per_call) VALUES ('legacy-image', 25000000)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.ModelPricing{}); err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(db)
	legacy, err := repo.GetModelPricing(t.Context(), "legacy-image")
	if err != nil || legacy.ImagePricingJSON != "{}" || legacy.PricingMode != "call" || legacy.CallNanousdPerCall != 25_000_000 {
		t.Fatalf("legacy=%+v error=%v", legacy, err)
	}
	policy := `{"textInputUSDPerMTokens":5,"imageInputUSDPerMTokens":8,"cachedTextInputUSDPerMTokens":1.25,"cachedImageInputUSDPerMTokens":2,"imageOutputUSDPerMTokens":30}`
	legacy.PricingMode = "token"
	legacy.ImagePricingJSON = policy
	if _, err := repo.UpsertModelPricing(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetModelPricing(t.Context(), "legacy-image")
	if err != nil || loaded.ImagePricingJSON != policy {
		t.Fatalf("loaded=%+v error=%v", loaded, err)
	}
}
