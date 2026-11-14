package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/billing"
	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	domainspeech "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"math"
	"testing"
)

func TestSpeechRateNanousdPreservesSmallPrices(t *testing.T) {
	for _, tc := range []struct {
		name        string
		price, rate float64
		want        int64
	}{
		{"asr hourly", 0.39, 7.2, 54166667},
		{"tts characters", 1.5, 7.2, 208333334},
		{"free", 0, 7.2, 0},
		{"sub nano rounds up", 0.0000000001, 7.2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := speechRateNanousd(tc.price, tc.rate)
			if err != nil || got != tc.want {
				t.Fatalf("got %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

type speechPolicyBillingRepo struct {
	repository.BillingRepository
	reservations int
}

func (*speechPolicyBillingRepo) GetBillingMode(context.Context) (string, error) { return "usage", nil }
func (r *speechPolicyBillingRepo) ReserveUsageBalance(ctx context.Context, in domainbilling.UsageBalanceReservationRequest) (*domainbilling.UsageBalanceReservation, error) {
	r.reservations++
	return &domainbilling.UsageBalanceReservation{UserID: in.UserID, RefNo: in.RefNo, Mode: in.Mode, BalanceNanousd: in.RequestedNanousd, Status: "reserved"}, nil
}
func TestSpeechUserChargesDefaultOffAndHistoricalQuoteIsFrozen(t *testing.T) {
	repo := &speechPolicyBillingRepo{}
	service := billing.NewService(repo)
	cfg := config.Config{BillingUSDToCNYRate: 7.2, Speech: config.SpeechConfig{InputProvider: "deepseek", TTSEnabled: true, ASRCNYPerHour: .39, TTSCNYPer10000Characters: 1.5}}
	runtime := config.NewRuntime(cfg)
	adapter := speechBillingAdapter{billing: service, runtime: runtime}
	for _, operation := range []string{"asr", "tts"} {
		units := int64(10000)
		if operation == "asr" {
			units = 120000
		}
		raw, e := adapter.Authorize(t.Context(), domainspeech.Access{UserID: 1, OperationID: "free-" + operation, Operation: operation, Provider: "deepseek", Units: units})
		if e != nil {
			t.Fatal(e)
		}
		var ticket billing.MeteredServiceAuthorization
		if e = json.Unmarshal(raw.Snapshot, &ticket); e != nil {
			t.Fatal(e)
		}
		if ticket.Quote.RateNanousd != 0 || repo.reservations != 0 {
			t.Fatal("default speech charged user", ticket.Quote)
		}
		ledger, e := service.BuildMeteredServiceLedger(&ticket, 5560, "")
		if e != nil || ledger.BilledNanousd != 0 {
			t.Fatal(ledger, e)
		}
	}
	cfg.Speech.ChargeASR = true
	cfg.Speech.ChargeTTS = true
	runtime.Store(cfg)
	raw, e := adapter.Authorize(t.Context(), domainspeech.Access{UserID: 1, OperationID: "charged", Operation: "asr", Provider: "deepseek", Units: 120000})
	if e != nil {
		t.Fatal(e)
	}
	var ticket billing.MeteredServiceAuthorization
	_ = json.Unmarshal(raw.Snapshot, &ticket)
	if ticket.Quote.Unit != "millisecond" || ticket.Quote.Quantum != 1000 || ticket.Quote.RateNanousd != 54166667 || repo.reservations != 1 {
		t.Fatal("ASR price/units not captured", ticket.Quote)
	}
	cfg.Speech.ChargeASR = false
	cfg.Speech.ASRCNYPerHour = 999
	cfg.Speech.InputProvider = "browser"
	runtime.Store(cfg)
	ledger, e := service.BuildMeteredServiceLedger(&ticket, 5560, "upstream")
	if e != nil || ledger.BilledNanousd != 90278 || ledger.DurationSeconds != 6 {
		t.Fatal("old operation repriced after settings change", ledger, e)
	}
	if _, e = adapter.Authorize(t.Context(), domainspeech.Access{UserID: 1, OperationID: "bypass", Operation: "asr", Provider: "deepseek", Units: 1000}); !errors.Is(e, domainspeech.ErrProviderChanged) {
		t.Fatal("client bypassed administrator source", e)
	}
}
func TestSpeechChargesApplyToSelectedIMEAndTTSStaysIndependent(t *testing.T) {
	for _, provider := range []string{"doubao-ime", "wetype"} {
		repo := &speechPolicyBillingRepo{}
		service := billing.NewService(repo)
		cfg := config.Config{BillingUSDToCNYRate: 7.2, Speech: config.SpeechConfig{InputProvider: provider, ChargeASR: true, TTSEnabled: true, ChargeTTS: true, ASRCNYPerHour: .39, TTSCNYPer10000Characters: 1.5}}
		adapter := speechBillingAdapter{billing: service, runtime: config.NewRuntime(cfg)}
		for _, operation := range []string{"asr", "tts"} {
			route := provider
			units := int64(1000)
			if operation == "tts" {
				route = "deepseek"
				units = 10
			}
			raw, e := adapter.Authorize(t.Context(), domainspeech.Access{UserID: 1, OperationID: provider + "-" + operation, Operation: operation, Provider: route, Units: units})
			if e != nil {
				t.Fatal(e)
			}
			var ticket billing.MeteredServiceAuthorization
			_ = json.Unmarshal(raw.Snapshot, &ticket)
			if ticket.Quote.RateNanousd <= 0 {
				t.Fatal("enabled charge ignored", ticket.Quote)
			}
			if operation == "tts" && (ticket.Quote.Unit != "character" || ticket.Quote.RateQuantity != 10000 || ticket.Quote.Quantum != 1) {
				t.Fatal("TTS units changed", ticket.Quote)
			}
		}
	}
}

func TestSpeechRateNanousdRejectsInvalidRates(t *testing.T) {
	for _, pair := range [][2]float64{{-1, 7.2}, {math.NaN(), 7.2}, {math.Inf(1), 7.2}, {1, 0}, {1, -1}, {1, math.NaN()}, {1, math.Inf(1)}, {math.MaxFloat64, 1}} {
		if _, err := speechRateNanousd(pair[0], pair[1]); err == nil {
			t.Fatalf("accepted invalid rate %v", pair)
		}
	}
}

func TestMiMoSpeechSiteChargeIsOptInAndSelectedProviderOnly(t *testing.T) {
	repo := &speechPolicyBillingRepo{}
	cfg := config.Config{BillingUSDToCNYRate: 7.2, Speech: config.SpeechConfig{TTSProvider: "mimo", TTSEnabled: true, TTSCNYPer10000Characters: 1.5}}
	runtime := config.NewRuntime(cfg)
	adapter := speechBillingAdapter{billing: billing.NewService(repo), runtime: runtime}
	access := domainspeech.Access{UserID: 1, OperationID: "mimo-free", Provider: "mimo", Operation: "tts", Units: 200}
	raw, err := adapter.Authorize(t.Context(), access)
	if err != nil {
		t.Fatal(err)
	}
	var ticket billing.MeteredServiceAuthorization
	if err := json.Unmarshal(raw.Snapshot, &ticket); err != nil || ticket.Quote.RateNanousd != 0 || repo.reservations != 0 {
		t.Fatal("MiMo unexpectedly charged user", err)
	}
	cfg.Speech.ChargeTTS = true
	runtime.Store(cfg)
	access.OperationID = "mimo-charged"
	raw, err = adapter.Authorize(t.Context(), access)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw.Snapshot, &ticket); err != nil || ticket.Quote.Provider != "mimo" || ticket.Quote.Unit != "character" || ticket.Quote.RateNanousd <= 0 {
		t.Fatal("site rate did not apply to selected provider", err)
	}
	access.Provider = "deepseek"
	if _, err := adapter.Authorize(t.Context(), access); err == nil {
		t.Fatal("client bypassed selected TTS provider")
	}
}
