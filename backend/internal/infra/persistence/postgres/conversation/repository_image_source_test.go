package conversation

import (
	"context"
	"fmt"
	"sync"
	"testing"

	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func imageSourceTestRepo(t *testing.T) (*Repo, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&models.FileObject{}, &models.ImageSourceAlias{}, &models.UserStorageQuota{}); err != nil {
		t.Fatal(err)
	}
	return NewRepo(db), db
}
func imageSourceTestFile(id string, userID uint) *domainconversation.FileObject {
	return &domainconversation.FileObject{FileID: id, UserID: userID, FileName: "a.png", FileCategory: "image", StoragePath: "objects/" + id, Status: "active", SizeBytes: 80, SHA256: id, ImageWidth: 2, ImageHeight: 3, ImageAnimationKnown: true}
}

func TestImageSourceRepositoryConcurrentUploadConsumesQuotaOnce(t *testing.T) {
	repo, db := imageSourceTestRepo(t)
	source := repository.ImageSourceFingerprint{SourceSHA256: "source", SourceSizeBytes: 120, ImagePolicyVersion: "v1:1024"}
	const n = 12
	var wg sync.WaitGroup
	results := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			file := imageSourceTestFile(fmt.Sprintf("file_%d", i), 1)
			quota, _, err := repo.CreateFileObjectWithImageSourceAndConsumeQuota(context.Background(), file, source, 80)
			if err != nil {
				errs <- err
				return
			}
			if quota.UsedBytes != 80 {
				errs <- fmt.Errorf("quota %d", quota.UsedBytes)
			}
			results <- file.FileID
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	close(results)
	canonical := ""
	for id := range results {
		if canonical == "" {
			canonical = id
		}
		if id != canonical {
			t.Fatal("concurrent source aliases returned different files")
		}
	}
	var count int64
	if err := db.Model(&models.FileObject{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("%d files", count)
	}
	var quota models.UserStorageQuota
	if err := db.First(&quota).Error; err != nil {
		t.Fatal(err)
	}
	if quota.UsedBytes != 80 {
		t.Fatal("duplicate quota charge")
	}
}

func TestDifferentImageSourcesWithSameOutputConsumeQuotaOnce(t *testing.T) {
	_, db := imageSourceTestRepo(t)
	start := make(chan struct{})
	var wg sync.WaitGroup
	type outcome struct {
		fileID string
		reused bool
		used   int64
		err    error
	}
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			// Distinct repository instances model independent upload-service
			// processes; only the database transaction coordinates these calls.
			repo := NewRepo(db)
			file := imageSourceTestFile(fmt.Sprintf("same_output_%d", i), 1)
			file.SHA256 = "identical_canonical_sha"
			source := repository.ImageSourceFingerprint{SourceSHA256: fmt.Sprintf("different_source_%d", i), SourceSizeBytes: int64(120 + i), ImagePolicyVersion: "v1:1024"}
			quota, reused, err := repo.CreateFileObjectWithImageSourceAndConsumeQuota(context.Background(), file, source, 80)
			result := outcome{fileID: file.FileID, reused: reused, err: err}
			if quota != nil {
				result.used = quota.UsedBytes
			}
			results <- result
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	canonical := ""
	reusedCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if canonical == "" {
			canonical = result.fileID
		}
		if result.fileID != canonical {
			t.Fatal("same canonical bytes created multiple files")
		}
		if result.used != 80 {
			t.Fatalf("used quota %d", result.used)
		}
		if result.reused {
			reusedCount++
		}
	}
	if reusedCount != 1 {
		t.Fatalf("expected one reuse, got %d", reusedCount)
	}
	var fileCount int64
	if err := db.Model(&models.FileObject{}).Count(&fileCount).Error; err != nil {
		t.Fatal(err)
	}
	if fileCount != 1 {
		t.Fatalf("stored %d copies", fileCount)
	}
	var aliases []models.ImageSourceAlias
	if err := db.Find(&aliases).Error; err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 2 {
		t.Fatalf("expected both private source aliases: %d", len(aliases))
	}
	for _, alias := range aliases {
		if alias.FileID != canonical {
			t.Fatal("source alias points to another canonical")
		}
	}
}

func TestImageSourceRepositoryScopesAliasesAndReplacesDeletedTargets(t *testing.T) {
	repo, db := imageSourceTestRepo(t)
	ctx := context.Background()
	source := repository.ImageSourceFingerprint{SourceSHA256: "source", SourceSizeBytes: 120, ImagePolicyVersion: "v1:1024"}
	first := imageSourceTestFile("first", 1)
	if _, _, err := repo.CreateFileObjectWithImageSourceAndConsumeQuota(ctx, first, source, 1000); err != nil {
		t.Fatal(err)
	}
	hits, err := repo.GetActiveImageSourceFiles(ctx, 2, []repository.ImageSourceFingerprint{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatal("cross-user hit")
	}
	other := source
	other.ImagePolicyVersion = "v1:2048"
	hits, err = repo.GetActiveImageSourceFiles(ctx, 1, []repository.ImageSourceFingerprint{other})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatal("different profile hit")
	}
	if err = db.Model(&models.FileObject{}).Where("file_id = ?", first.FileID).Update("status", "deleted").Error; err != nil {
		t.Fatal(err)
	}
	hits, err = repo.GetActiveImageSourceFiles(ctx, 1, []repository.ImageSourceFingerprint{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatal("deleted file hit")
	}
	next := imageSourceTestFile("second", 1)
	if _, reused, err := repo.CreateFileObjectWithImageSourceAndConsumeQuota(ctx, next, source, 1000); err != nil || reused {
		t.Fatalf("replace deleted: %v %v", reused, err)
	}
	hits, err = repo.GetActiveImageSourceFiles(ctx, 1, []repository.ImageSourceFingerprint{source})
	if err != nil {
		t.Fatal(err)
	}
	if hits[source].FileID != "second" {
		t.Fatal("stale alias was not replaced")
	}
	if err = repo.InvalidateImageSource(ctx, 1, source, "first"); err != nil {
		t.Fatal(err)
	}
	hits, err = repo.GetActiveImageSourceFiles(ctx, 1, []repository.ImageSourceFingerprint{source})
	if err != nil {
		t.Fatal(err)
	}
	if hits[source].FileID != "second" {
		t.Fatal("invalidating an old candidate must preserve its concurrent replacement")
	}
}
