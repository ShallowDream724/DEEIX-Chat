package channel_test

import (
	"context"
	"errors"
	"testing"

	appchannel "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	domainchannel "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	channelrepo "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/postgres/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func TestChat01CreateAndImportPreservesSharedModelIdentity(t *testing.T) {
	db := openModelProtocolsTestDB(t)
	if err := db.AutoMigrate(&models.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	repo := channelrepo.NewRepo(db)
	if err := db.Create(&models.LLMModelVendor{Key: "chat01", Name: "Chat01", Icon: "/icons/chat01.jpg", BuiltIn: true}).Error; err != nil {
		t.Fatal(err)
	}
	runtime := config.NewRuntime(config.Config{DataEncryptionKey: "test-data-encryption-key-32-bytes"})
	service := appchannel.NewServiceWithRuntime(runtime, repo, repo, nil, nil)
	refreshCalls := 0
	service.SetSettingsRefreshHook(func(context.Context) error {
		refreshCalls++
		return errors.New("cache refresh unavailable")
	})
	created, err := service.CreateUpstream(t.Context(), appchannel.CreateUpstreamInput{
		Name: "Chat01", BaseURL: "https://example.com/v1", Compatible: "chat01",
		APIKeys: `{"strategy":"failover","keys":[{"key":"test-key","status":"active"}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ConnectTimeoutMS != 30_000 || created.ReadTimeoutMS != 9_000_000 || created.StreamIdleTimeoutMS != 9_000_000 {
		t.Fatalf("new upstream defaults: %+v", created)
	}
	limits := inputlimits.Resolve(runtime.Snapshot().InputLimitsJSON, "", "", created.ID)
	if limits.AttachmentCount != 10 || limits.AttachmentBytes != 104857600 || limits.AttachmentSingleBytes != 104857600 || limits.NativeBytes != 104857600 || limits.PDFSingleBytes != 104857600 || limits.NonPDFBytes != 104857600 || limits.PDFBytes != 104857600 || limits.PDFPages != 200 || limits.ImageSingleBytes != 5_000_000 {
		t.Fatalf("Chat01 preset missing or replaced inherited limits: %+v", limits)
	}
	custom, err := service.CreateUpstream(t.Context(), appchannel.CreateUpstreamInput{
		Name: "custom Chat01", BaseURL: "https://example.com/v1", Compatible: "chat01",
		APIKeys:          `{"strategy":"failover","keys":[{"key":"test-key","status":"active"}]}`,
		ConnectTimeoutMS: 42_000, ReadTimeoutMS: 7_200_000, StreamIdleTimeoutMS: 8_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if custom.ConnectTimeoutMS != 42_000 || custom.ReadTimeoutMS != 7_200_000 || custom.StreamIdleTimeoutMS != 8_000_000 {
		t.Fatalf("custom timeouts changed: %+v", custom)
	}
	if refreshCalls != 2 {
		t.Fatalf("committed budget changes did not invoke settings refresh: %d", refreshCalls)
	}
	if inputlimits.Resolve(runtime.Snapshot().InputLimitsJSON, "", "", custom.ID).AttachmentBytes != 104857600 {
		t.Fatal("committed runtime budget was lost after refresh failure")
	}
	shared := models.LLMPlatformModel{Name: "shared-gpt-6", Vendor: "openai", Icon: "custom-icon", KindsJSON: `["chat"]`, CapabilitiesJSON: `{"contextWindow":90000}`, Status: "active"}
	if err := db.Create(&shared).Error; err != nil {
		t.Fatal(err)
	}
	result, err := service.ImportUpstreamModels(t.Context(), created.ID, appchannel.ImportUpstreamModelsInput{Items: []appchannel.ImportUpstreamModelItemInput{
		{PlatformModelName: "gpt-6", UpstreamModelName: "gpt-6"},
		{PlatformModelName: "shared-gpt-6", UpstreamModelName: "gpt-6"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ImportedCount != 2 || result.CreatedPlatform != 1 {
		t.Fatalf("import failed: %+v", result)
	}
	var dedicated models.LLMPlatformModel
	if err := db.Where("name = ?", "gpt-6").First(&dedicated).Error; err != nil {
		t.Fatal(err)
	}
	if dedicated.Vendor != "chat01" || dedicated.Icon != "/icons/chat01.jpg" || dedicated.CapabilitiesJSON != domainchannel.Chat01DefaultCapabilitiesJSON {
		t.Fatalf("new dedicated model: %+v", dedicated)
	}
	var storedShared models.LLMPlatformModel
	if err := db.First(&storedShared, shared.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedShared.Vendor != shared.Vendor || storedShared.Icon != shared.Icon || storedShared.CapabilitiesJSON != shared.CapabilitiesJSON {
		t.Fatalf("route altered global shared model: %+v", storedShared)
	}
	var routes []models.LLMPlatformModelRoute
	if err := db.Find(&routes).Error; err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes=%+v", routes)
	}
	for _, route := range routes {
		if route.Protocol != "chat01_chat_completions" {
			t.Fatalf("wrong imported adapter: %+v", route)
		}
	}
	var upstreamModel models.LLMUpstreamModel
	if err := db.First(&upstreamModel).Error; err != nil {
		t.Fatal(err)
	}
	if upstreamModel.Vendor != "chat01" {
		t.Fatalf("upstream model vendor = %q", upstreamModel.Vendor)
	}
}
