package channel

import (
	"testing"

	domainchannel "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func TestCreateUpstreamInitializesBudgetAtomicallyAndPreservesSettings(t *testing.T) {
	db := openChannelSQLiteTestDB(t)
	if err := db.AutoMigrate(&models.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	setting := models.SystemSetting{Namespace: "file", Key: "input_limits", ValueType: "json", Value: `{"defaults":{"pdfPages":123,"imageCount":2},"upstreams":{"1":{"attachmentCount":4},"99":{"nativeCount":7}}}`}
	if err := db.Create(&setting).Error; err != nil {
		t.Fatal(err)
	}
	item := &domainchannel.Upstream{Name: "new-upstream", Compatible: "custom"}
	result, err := NewRepo(db).CreateUpstream(t.Context(), repository.CreateChannelUpstreamInput{Upstream: item, InitialInputLimitsJSON: `{"attachmentCount":10,"attachmentBytes":104857600}`})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != 1 {
		t.Fatalf("unexpected created ID %d", item.ID)
	}
	var stored models.SystemSetting
	if err := db.First(&stored, setting.ID).Error; err != nil {
		t.Fatal(err)
	}
	if result.InputLimitsJSON != stored.Value {
		t.Fatal("published settings differ from committed settings")
	}
	limits := inputlimits.Resolve(stored.Value, "", "", item.ID)
	if limits.AttachmentCount != 4 || limits.AttachmentBytes != 104857600 || limits.PDFPages != 123 || limits.ImageCount != 2 || inputlimits.Resolve(stored.Value, "", "", 99).NativeCount != 7 {
		t.Fatalf("preserved settings mismatch: %s", stored.Value)
	}
}

func TestCreateUpstreamRollsBackWhenBudgetInitializationFails(t *testing.T) {
	db := openChannelSQLiteTestDB(t)
	if err := db.AutoMigrate(&models.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	item := &domainchannel.Upstream{Name: "rolled-back"}
	_, err := NewRepo(db).CreateUpstream(t.Context(), repository.CreateChannelUpstreamInput{Upstream: item, InitialInputLimitsJSON: `{"attachmentCount":-1}`})
	if err == nil {
		t.Fatal("invalid preset did not fail")
	}
	var upstreams, settings int64
	if err := db.Model(&models.LLMUpstream{}).Count(&upstreams).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.SystemSetting{}).Count(&settings).Error; err != nil {
		t.Fatal(err)
	}
	if upstreams != 0 || settings != 0 || item.ID != 0 {
		t.Fatalf("partial creation survived rollback: upstreams=%d settings=%d inputID=%d", upstreams, settings, item.ID)
	}
}
