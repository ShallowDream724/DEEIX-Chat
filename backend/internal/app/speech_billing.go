package app

import (
	"context"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/billing"
	domainspeech "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

type speechBillingAdapter struct {
	billing *billing.Service
	runtime *config.Runtime
}

func speechRateNanousd(cny, usdToCNY float64) (int64, error) {
	if math.IsNaN(cny) || math.IsInf(cny, 0) || cny < 0 || math.IsNaN(usdToCNY) || math.IsInf(usdToCNY, 0) || usdToCNY <= 0 {
		return 0, domainspeech.ErrUnavailable
	}
	price, ok := new(big.Rat).SetString(strconv.FormatFloat(cny, 'f', -1, 64))
	if !ok {
		return 0, domainspeech.ErrUnavailable
	}
	rate, ok := new(big.Rat).SetString(strconv.FormatFloat(usdToCNY, 'f', -1, 64))
	if !ok {
		return 0, domainspeech.ErrUnavailable
	}
	price.Quo(price, rate).Mul(price, big.NewRat(1_000_000_000, 1))
	amount := new(big.Int).Add(price.Num(), new(big.Int).Sub(price.Denom(), big.NewInt(1)))
	amount.Quo(amount, price.Denom())
	if !amount.IsInt64() {
		return 0, domainspeech.ErrUnavailable
	}
	return amount.Int64(), nil
}

func (a speechBillingAdapter) Authorize(ctx context.Context, access domainspeech.Access) (domainspeech.BillingTicket, error) {
	if access.UserID == 0 || access.Units < 0 || strings.TrimSpace(access.OperationID) == "" || a.billing == nil || a.runtime == nil {
		return domainspeech.BillingTicket{}, domainspeech.ErrForbidden
	}
	cfg := a.runtime.Snapshot()
	quote := billing.MeteredServiceQuote{Provider: access.Provider, EstimatedQuantity: access.Units, Quantum: 1}
	var cny float64
	switch access.Operation {
	case "asr":
		selected := strings.TrimSpace(cfg.Speech.InputProvider)
		if selected == "" {
			selected = "browser"
		}
		if access.Provider != selected || selected == "browser" {
			return domainspeech.BillingTicket{}, domainspeech.ErrProviderChanged
		}
		maxSeconds := cfg.Speech.MaxRecordingSeconds
		if maxSeconds <= 0 {
			maxSeconds = 120
		}
		if access.Provider == "deepseek" && maxSeconds > 120 {
			maxSeconds = 120
		}
		if access.Units > int64(maxSeconds)*1000 {
			return domainspeech.BillingTicket{}, domainspeech.ErrInvalid
		}
		quote.Unit, quote.RateQuantity, quote.Quantum = "millisecond", 3_600_000, 1000
		quote.ServiceName = "语音输入"
		switch access.Provider {
		case "deepseek", "doubao-ime", "wetype":
			if cfg.Speech.ChargeASR {
				cny = cfg.Speech.ASRCNYPerHour
			}
		default:
			return domainspeech.BillingTicket{}, domainspeech.ErrUnavailable
		}
	case "tts":
		if access.Provider != "deepseek" || !cfg.Speech.DeepSeekTTSEnabled || access.Units > 10000 {
			return domainspeech.BillingTicket{}, domainspeech.ErrUnavailable
		}
		quote.Unit, quote.RateQuantity, quote.ServiceName = "character", 10000, "朗读回复"
		if cfg.Speech.ChargeTTS {
			cny = cfg.Speech.TTSCNYPer10000Characters
		}
	default:
		return domainspeech.BillingTicket{}, domainspeech.ErrInvalid
	}
	quote.ServiceCode = access.Provider + "." + access.Operation
	if math.IsNaN(cny) || math.IsInf(cny, 0) || cny < 0 {
		return domainspeech.BillingTicket{}, domainspeech.ErrUnavailable
	}
	// Free input-method routes do not depend on payment conversion configuration.
	if cny > 0 {
		var err error
		quote.RateNanousd, err = speechRateNanousd(cny, cfg.BillingUSDToCNYRate)
		if err != nil {
			return domainspeech.BillingTicket{}, err
		}
	}
	ref := "speech:" + access.OperationID
	ticket, err := a.billing.AuthorizeMeteredService(ctx, access.UserID, ref, quote)
	if err != nil {
		return domainspeech.BillingTicket{}, err
	}
	snapshot, err := json.Marshal(ticket)
	if err != nil {
		_ = a.billing.ReleaseUsageAuthorization(ctx, ticket.Authorization)
		return domainspeech.BillingTicket{}, err
	}
	return domainspeech.BillingTicket{Reference: ref, Snapshot: snapshot}, nil
}

func (a speechBillingAdapter) Record(ctx context.Context, usage domainspeech.Usage) error {
	var ticket billing.MeteredServiceAuthorization
	if a.billing == nil || json.Unmarshal(usage.Ticket.Snapshot, &ticket) != nil || ticket.Authorization == nil ||
		ticket.Authorization.UserID != usage.UserID || ticket.Authorization.RefNo != usage.Ticket.Reference ||
		usage.Ticket.Reference != "speech:"+usage.OperationID || ticket.Quote.Provider != usage.Provider ||
		ticket.Quote.ServiceCode != usage.Provider+"."+usage.Operation {
		return domainspeech.ErrForbidden
	}
	switch usage.Status {
	case "complete":
		if err := a.billing.RecordMeteredServiceUsage(ctx, &ticket, usage.ActualUnits, usage.UpstreamRequestID); err != nil {
			_ = a.billing.MarkUsageAuthorizationForReconciliation(ctx, ticket.Authorization, "speech_settlement_failed")
			return err
		}
		return nil
	case "cancelled":
		return a.billing.ReleaseUsageAuthorization(ctx, ticket.Authorization)
	case "uncertain":
		return a.billing.MarkUsageAuthorizationForReconciliation(ctx, ticket.Authorization, "speech_upstream_uncertain")
	default:
		return domainspeech.ErrInvalid
	}
}
