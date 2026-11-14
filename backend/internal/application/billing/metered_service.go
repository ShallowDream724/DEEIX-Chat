package billing

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

// MeteredServiceQuote keeps service units separate from language-model tokens.
// RateNanousd applies to RateQuantity units; Quantum rounds only billable usage.
type MeteredServiceQuote struct {
	ServiceCode       string `json:"service_code"`
	ServiceName       string `json:"service_name"`
	Provider          string `json:"provider"`
	Unit              string `json:"unit"`
	EstimatedQuantity int64  `json:"estimated_quantity"`
	RateNanousd       int64  `json:"rate_nanousd"`
	RateQuantity      int64  `json:"rate_quantity"`
	Quantum           int64  `json:"quantum"`
}

// MeteredServiceAuthorization is persisted with an accepted operation. Finishing
// a recovered operation therefore uses the original price and billing mode.
type MeteredServiceAuthorization struct {
	Authorization *domainbilling.UsageAuthorization `json:"authorization"`
	Quote         MeteredServiceQuote               `json:"quote"`
	BillingAt     time.Time                         `json:"billing_at"`
}

func meteredServiceCost(quote MeteredServiceQuote, quantity int64) (int64, int64, error) {
	if quantity < 0 || quote.RateNanousd < 0 || quote.RateQuantity <= 0 || quote.Quantum <= 0 {
		return 0, 0, repository.ErrInvalidInput
	}
	units := big.NewInt(quantity)
	quantum := big.NewInt(quote.Quantum)
	units.Add(units, new(big.Int).Sub(quantum, big.NewInt(1)))
	units.Quo(units, quantum).Mul(units, quantum)
	if !units.IsInt64() {
		return 0, 0, repository.ErrInvalidInput
	}
	amount := new(big.Int).Mul(units, big.NewInt(quote.RateNanousd))
	divisor := big.NewInt(quote.RateQuantity)
	amount.Add(amount, new(big.Int).Sub(divisor, big.NewInt(1))).Quo(amount, divisor)
	if !amount.IsInt64() {
		return 0, 0, repository.ErrInvalidInput
	}
	return amount.Int64(), units.Int64(), nil
}

// AuthorizeMeteredService reserves the known service maximum, without requiring
// a fictitious chat model or a model-wide minimum prepayment.
func (s *Service) AuthorizeMeteredService(ctx context.Context, userID uint, refNo string, quote MeteredServiceQuote) (*MeteredServiceAuthorization, error) {
	refNo = strings.TrimSpace(refNo)
	if userID == 0 || refNo == "" || len(refNo) > 128 || strings.TrimSpace(quote.ServiceCode) == "" || strings.TrimSpace(quote.Provider) == "" {
		return nil, repository.ErrInvalidInput
	}
	switch quote.Unit {
	case "millisecond", "character", "page":
	default:
		return nil, repository.ErrInvalidInput
	}
	amount, _, err := meteredServiceCost(quote, quote.EstimatedQuantity)
	if err != nil {
		return nil, err
	}
	mode, err := s.repo.GetBillingMode(ctx)
	if err != nil {
		return nil, err
	}
	auth := &domainbilling.UsageAuthorization{UserID: userID, Mode: strings.TrimSpace(mode), RefNo: refNo}
	ticket := &MeteredServiceAuthorization{Authorization: auth, Quote: quote, BillingAt: time.Now().UTC()}
	if amount == 0 || (auth.Mode != "usage" && auth.Mode != "period") {
		return ticket, nil
	}
	request := domainbilling.UsageBalanceReservationRequest{
		UserID: userID, RefNo: refNo, Mode: auth.Mode, RequestedNanousd: amount,
	}
	if auth.Mode == "period" {
		plan, start, end, err := s.currentPeriodPlan(ctx, userID, ticket.BillingAt)
		if err != nil {
			return nil, err
		}
		request.PeriodStartAt, request.PeriodEndAt = &start, &end
		request.PeriodCreditNanousd = plan.PeriodCreditNanousd
	}
	reservation, err := s.repo.ReserveUsageBalance(ctx, request)
	switch {
	case errors.Is(err, repository.ErrUsageReservationLimitExceeded):
		return nil, ErrUsageConcurrencyLimitExceeded
	case errors.Is(err, repository.ErrInsufficientBalance):
		return nil, ErrUsageBalanceInsufficient
	case errors.Is(err, repository.ErrConflict):
		return nil, ErrUsageReservationConflict
	case err != nil:
		return nil, err
	}
	auth.Reservation = reservation
	return ticket, nil
}

func (s *Service) BuildMeteredServiceLedger(ticket *MeteredServiceAuthorization, quantity int64, upstreamRequestID string) (*domainbilling.UsageLedger, error) {
	if ticket == nil || ticket.Authorization == nil || ticket.Authorization.UserID == 0 || ticket.Authorization.RefNo == "" || ticket.BillingAt.IsZero() {
		return nil, repository.ErrInvalidInput
	}
	quote := ticket.Quote
	amount, billableQuantity, err := meteredServiceCost(quote, quantity)
	if err != nil {
		return nil, err
	}
	if ticket.Authorization.Mode != "usage" && ticket.Authorization.Mode != "period" {
		amount = 0
	}
	item := map[string]any{
		"service_code": quote.ServiceCode, "service_name": quote.ServiceName,
		"provider_protocol": quote.Provider, "pricing_mode": "metered",
		"usage_unit": quote.Unit, "usage_quantity": quantity,
		"billable_quantity": billableQuantity, "unit_price_nanousd": quote.RateNanousd,
		"price_unit_quantity": quote.RateQuantity, "billing_quantum": quote.Quantum,
		"billed_nanousd": amount,
	}
	snapshot, err := json.Marshal(map[string]any{
		"version": 1, "pricing_mode": "metered", "service_code": quote.ServiceCode,
		"service_name": quote.ServiceName, "provider_protocol": quote.Provider,
		"usage_unit": quote.Unit, "usage_quantity": quantity,
		"billable_quantity": billableQuantity, "unit_price_nanousd": quote.RateNanousd,
		"price_unit_quantity": quote.RateQuantity, "billing_quantum": quote.Quantum,
		"metered_billed_nanousd": amount, "billed_nanousd": amount,
		"service_items": []any{item}, "billing_mode": ticket.Authorization.Mode,
		"upstream_usage": map[string]any{"request_id": upstreamRequestID, "unit": quote.Unit, "quantity": quantity},
	})
	if err != nil {
		return nil, err
	}
	ledger := &domainbilling.UsageLedger{
		UserID: ticket.Authorization.UserID, RefNo: ticket.Authorization.RefNo,
		ProviderProtocol: quote.Provider, UpstreamName: quote.Provider,
		UpstreamModelName: quote.ServiceCode, IsFreeModel: quote.RateNanousd == 0,
		BillingAt: ticket.BillingAt, UsageDate: ticket.BillingAt,
		BilledCurrency: "USD", BilledNanousd: amount, PricingSnapshotJSON: string(snapshot),
	}
	if quote.Unit == "millisecond" {
		ledger.DurationSeconds = billableQuantity / 1000
	}
	return ledger, nil
}

func (s *Service) RecordMeteredServiceUsage(ctx context.Context, ticket *MeteredServiceAuthorization, quantity int64, upstreamRequestID string) error {
	ledger, err := s.BuildMeteredServiceLedger(ticket, quantity, upstreamRequestID)
	if err != nil {
		return err
	}
	if err := s.EnsureUsageAuthorizationBudget(ctx, ticket.Authorization, ledger.BilledNanousd); err != nil {
		return err
	}
	return s.RecordUsageWithAuthorization(ctx, ledger, ticket.Authorization)
}
