package nativefile

import (
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNativeFileLeaseRecoveryRetainsBillingAndRejectsStaleOwner(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&models.NativeFile{}); err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(db)
	record := llm.NativeFileRecord{Key: "cache-key", UserID: 1, Scope: "scope", SourceID: "source", Purpose: "rich_layout_recognition", FileName: "private-name.pdf", SizeBytes: 100, State: "pending", ClaimToken: "first", LeaseUntil: time.Now().Add(time.Minute), ExpiresAt: time.Now().Add(24 * time.Hour), BillingTicketJSON: `{"rate":7}`, Pages: 2, QuantityKnown: true}
	if claimed, err := repo.Claim(t.Context(), record); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	other := record
	other.ClaimToken = "second"
	if claimed, err := repo.Claim(t.Context(), other); err != nil || claimed {
		t.Fatalf("live lease stolen: %v %v", claimed, err)
	}
	record.LeaseUntil = time.Now().Add(-time.Second)
	record.State = "uncertain"
	if err = repo.Save(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	other.LeaseUntil = time.Now().Add(time.Minute)
	if claimed, err := repo.Claim(t.Context(), other); err != nil || !claimed {
		t.Fatalf("recovery claim: %v %v", claimed, err)
	}
	recovered, found, err := repo.Load(t.Context(), record.Key)
	if err != nil || !found {
		t.Fatalf("load: %v %v", found, err)
	}
	if recovered.State != "uncertain" || recovered.BillingTicketJSON != record.BillingTicketJSON || !recovered.QuantityKnown || recovered.ClaimToken != "second" {
		t.Fatalf("recovery reset immutable/billing state: %#v", recovered)
	}
	record.State = "ready"
	record.ProviderFileID = "old-owner-id"
	if err = repo.Save(t.Context(), record); err == nil {
		t.Fatal("stale owner overwrote recovered claim")
	}
	recovered.ProviderFileID = "recovered-id"
	recovered.State = "ready"
	recovered.BillingSettled = true
	if err = repo.Save(t.Context(), recovered); err != nil {
		t.Fatal(err)
	}
	foreign := recovered
	foreign.UserID = 2
	if err = repo.Save(t.Context(), foreign); err == nil {
		t.Fatal("foreign user updated cache record")
	}
	final, _, _ := repo.Load(t.Context(), record.Key)
	if final.ProviderFileID != "recovered-id" || !final.BillingSettled {
		t.Fatalf("completion did not persist: %#v", final)
	}
}
