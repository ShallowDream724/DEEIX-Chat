package billing

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	domainbilling "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/billing"
)

func TestMeteredSpeechRoundsDurationOnceWithoutInventingTokens(t *testing.T) {
	repo := &billingRepositoryStub{mode: "usage", prepaidNanousd: 999999999}
	svc := &Service{repo: repo}
	ticket, err := svc.AuthorizeMeteredService(context.Background(), 4, "speech:test", MeteredServiceQuote{
		ServiceCode: "deepseek.asr", Provider: "deepseek", ServiceName: "DeepSeek ASR",
		Unit: "millisecond", EstimatedQuantity: 120000, RateNanousd: 3600000, RateQuantity: 3600000, Quantum: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repo.reservationRequest == nil || repo.reservationRequest.RequestedNanousd != 120000 {
		t.Fatalf("should reserve the service maximum, got %#v", repo.reservationRequest)
	}
	ledger, err := svc.BuildMeteredServiceLedger(ticket, 1001, "upstream-1")
	if err != nil {
		t.Fatal(err)
	}
	if ledger.BilledNanousd != 2000 || ledger.DurationSeconds != 2 || ledger.InputTokens != 0 || ledger.OutputTokens != 0 || ledger.CallCount != 0 {
		t.Fatalf("unexpected service accounting: %#v", ledger)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(ledger.PricingSnapshotJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["usage_quantity"] != float64(1001) || snapshot["billable_quantity"] != float64(2000) {
		t.Fatalf("lost exact duration: %#v", snapshot)
	}
}

func TestMeteredTicketSurvivesSerializationAndModeChange(t *testing.T) {
	repo := &billingRepositoryStub{mode: "usage"}
	svc := &Service{repo: repo}
	ticket, err := svc.AuthorizeMeteredService(context.Background(), 5, "speech:tts", MeteredServiceQuote{
		ServiceCode: "deepseek.tts", Provider: "deepseek", Unit: "character",
		EstimatedQuantity: 300, RateNanousd: 1000, RateQuantity: 10000, Quantum: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(ticket)
	if err != nil {
		t.Fatal(err)
	}
	var restored MeteredServiceAuthorization
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	repo.mode = "self"
	if err := svc.RecordMeteredServiceUsage(context.Background(), &restored, 300, ""); err != nil {
		t.Fatal(err)
	}
	if !repo.usageSettled || repo.usageAdded {
		t.Fatal("recovered operation did not keep original billing mode")
	}
}

func TestMeteredServiceRejectsOverflowAndInvalidUnits(t *testing.T) {
	quote := MeteredServiceQuote{Unit: "character", RateNanousd: math.MaxInt64, RateQuantity: 1, Quantum: 1}
	if _, _, err := meteredServiceCost(quote, 2); err == nil {
		t.Fatal("overflow accepted")
	}
	quote.RateNanousd = 1
	quote.Quantum = 1000
	if _, _, err := meteredServiceCost(quote, math.MaxInt64); err == nil {
		t.Fatal("quantum overflow accepted")
	}
	quote.Quantum = 1
	if _, _, err := meteredServiceCost(quote, -1); err == nil {
		t.Fatal("negative quantity accepted")
	}
}

func TestFreeServiceRetainsUnitEvidenceWithoutReservation(t *testing.T) {
	repo := &billingRepositoryStub{mode: "usage"}
	svc := &Service{repo: repo}
	ticket, err := svc.AuthorizeMeteredService(context.Background(), 6, "speech:free", MeteredServiceQuote{
		ServiceCode: "wetype.asr", Provider: "wetype", Unit: "millisecond", EstimatedQuantity: 120000, RateQuantity: 3600000, Quantum: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Authorization.Reservation != nil || repo.reservationRequest != nil {
		t.Fatal("free service reserved money")
	}
	ledger, err := svc.BuildMeteredServiceLedger(ticket, 15300, "")
	if err != nil {
		t.Fatal(err)
	}
	if !ledger.IsFreeModel || ledger.BilledNanousd != 0 || ledger.DurationSeconds != 16 {
		t.Fatal("free service lost usage")
	}
	if err := svc.RecordUsageWithAuthorization(context.Background(), ledger, &domainbilling.UsageAuthorization{UserID: 6, Mode: "usage", RefNo: "speech:free"}); err != nil {
		t.Fatal(err)
	}
}
