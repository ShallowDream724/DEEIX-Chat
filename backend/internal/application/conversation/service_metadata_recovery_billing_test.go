package conversation

import (
	"context"
	"errors"
	"testing"
	"time"

	appbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/billing"
	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type metadataRecoveryBillingRepo struct {
	repository.BillingRepository
	reserved, released, reconciled, settled int
	refs                                    map[string]bool
	settleErr                               error
}

func (*metadataRecoveryBillingRepo) GetBillingMode(context.Context) (string, error) {
	return "usage", nil
}
func (*metadataRecoveryBillingRepo) GetBillingPrepaidAmountNanousd(context.Context) (int64, error) {
	return 10, nil
}
func (*metadataRecoveryBillingRepo) GetNativeToolBillingEnabled(context.Context) (bool, error) {
	return false, nil
}
func (*metadataRecoveryBillingRepo) GetNativeToolPricingJSON(context.Context) (string, error) {
	return "{}", nil
}
func (*metadataRecoveryBillingRepo) GetModelPricing(context.Context, string) (*domainbilling.ModelPricing, error) {
	return &domainbilling.ModelPricing{Currency: "USD", InputNanousdPerMTokens: 1000000, OutputNanousdPerMTokens: 1000000}, nil
}
func (*metadataRecoveryBillingRepo) ListSubscriptionEntitlementsByUserIDs(context.Context, []uint, time.Time) ([]domainbilling.Subscription, error) {
	return nil, nil
}
func (*metadataRecoveryBillingRepo) ListPlansByIDs(context.Context, []uint) ([]domainbilling.Plan, error) {
	return nil, nil
}
func (r *metadataRecoveryBillingRepo) ReserveUsageBalance(_ context.Context, input domainbilling.UsageBalanceReservationRequest) (*domainbilling.UsageBalanceReservation, error) {
	r.reserved++
	return &domainbilling.UsageBalanceReservation{UserID: input.UserID, RefNo: input.RefNo, Mode: input.Mode}, nil
}
func (r *metadataRecoveryBillingRepo) ReleaseUsageBalanceReservation(ctx context.Context, _ uint, _ string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.released++
	return nil
}
func (r *metadataRecoveryBillingRepo) MarkUsageReservationReconciliationRequired(ctx context.Context, _ uint, _ string, _ string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.reconciled++
	return nil
}
func (r *metadataRecoveryBillingRepo) AddUsageAndSettleBalance(_ context.Context, usage *domainbilling.UsageLedger, reservation *domainbilling.UsageBalanceReservation) error {
	if r.settleErr != nil {
		return r.settleErr
	}
	if usage.RefNo != reservation.RefNo {
		return errors.New("authorization does not match settlement")
	}
	if r.refs == nil {
		r.refs = map[string]bool{}
	}
	if r.refs[usage.RefNo] {
		return errors.New("duplicate settlement")
	}
	r.refs[usage.RefNo] = true
	r.settled++
	return nil
}

func TestConversationMetadataRecoveryBillingAccountsForEachSuccessfulFieldOnce(t *testing.T) {
	s, r, g := newMetadataRecoveryService()
	billingRepo := &metadataRecoveryBillingRepo{}
	s.billingSvc = appbilling.NewService(billingRepo)
	g.generate = func(_ context.Context, field string, attempt int) (*llm.GenerateOutput, error) {
		if field == "title" && attempt == 1 {
			return nil, &llm.UpstreamError{StatusCode: 503}
		}
		return metadataRecoveryOutput(field), nil
	}
	runMetadataRecoveryForTest(s, r.current, nil)
	if g.titles != 2 || g.labels != 1 || billingRepo.reserved != 3 || billingRepo.released != 1 || billingRepo.settled != 2 || billingRepo.reconciled != 0 {
		t.Fatalf("calls title=%d labels=%d billing=%+v", g.titles, g.labels, billingRepo)
	}
}

func TestConversationMetadataRecoveryUnknownPaidResultIsReconciled(t *testing.T) {
	s, r, g := newMetadataRecoveryService()
	billingRepo := &metadataRecoveryBillingRepo{}
	s.billingSvc = appbilling.NewService(billingRepo)
	g.generate = func(_ context.Context, field string, _ int) (*llm.GenerateOutput, error) {
		if field == "title" {
			return nil, llm.MarkRequestAccepted(context.DeadlineExceeded)
		}
		return metadataRecoveryOutput(field), nil
	}
	runMetadataRecoveryForTest(s, r.current, nil)
	if g.titles != 1 || billingRepo.reconciled != 1 || billingRepo.released != 0 || billingRepo.settled != 1 {
		t.Fatalf("calls=%d billing=%+v", g.titles, billingRepo)
	}
}

func TestConversationMetadataRecoverySettlementFailureStopsFurtherGeneration(t *testing.T) {
	s, r, g := newMetadataRecoveryService()
	billingRepo := &metadataRecoveryBillingRepo{settleErr: repository.ErrConflict}
	s.billingSvc = appbilling.NewService(billingRepo)
	runMetadataRecoveryForTest(s, r.current, nil)
	if g.titles != 1 || g.labels != 0 || billingRepo.reserved != 1 || billingRepo.reconciled != 1 || billingRepo.released != 0 {
		t.Fatalf("calls title=%d labels=%d billing=%+v", g.titles, g.labels, billingRepo)
	}
}
