package service

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

type blockingUploadReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	read    bool
}

func (r *blockingUploadReader) Read(buffer []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	if !r.read {
		<-r.release
		r.read = true
		copy(buffer, "content")
		return len("content"), nil
	}
	return 0, io.EOF
}

func TestNewAppServiceDerivesStagingDirectoryFromUploadDirectory(t *testing.T) {
	uploadDir := filepath.Join(t.TempDir(), "uploads")
	appService := NewAppService(nil, nil, nil, model.ServerConfig{UploadDir: uploadDir})

	want := filepath.Join(filepath.Dir(uploadDir), "staging")
	if appService.staging.rootDir != want {
		t.Fatalf("expected staging directory %q, got %q", want, appService.staging.rootDir)
	}
}

func TestUploadStagingCopyToKeepsSourceUntilConsumed(t *testing.T) {
	rootDir := t.TempDir()
	destinationDir := t.TempDir()
	staging := NewUploadStagingService(rootDir, time.Hour)

	staged, err := staging.StageBytes("guide.md", []byte("staged content"), "test")
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}

	destination, err := staging.CopyTo(staged.ID, destinationDir)
	if err != nil {
		t.Fatalf("copy staged upload: %v", err)
	}
	if _, err := os.Stat(staged.Path); err != nil {
		t.Fatalf("expected staged source to remain before consume: %v", err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read permanent upload: %v", err)
	}
	if string(content) != "staged content" {
		t.Fatalf("unexpected permanent upload content: %q", content)
	}

	if err := staging.Delete(staged.ID); err != nil {
		t.Fatalf("delete consumed staging source: %v", err)
	}
	if _, err := os.Stat(staged.Path); !os.IsNotExist(err) {
		t.Fatalf("expected staged source to be deleted, got %v", err)
	}
}

func TestUploadStagingCleanupRemovesExpiredOrphanFiles(t *testing.T) {
	rootDir := t.TempDir()
	staging := NewUploadStagingService(rootDir, time.Minute)
	orphanPath := filepath.Join(rootDir, "orphan-upload.md")
	if err := os.WriteFile(orphanPath, []byte("orphan"), 0o644); err != nil {
		t.Fatalf("write orphan file: %v", err)
	}
	expiredAt := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(orphanPath, expiredAt, expiredAt); err != nil {
		t.Fatalf("age orphan file: %v", err)
	}

	if err := staging.CleanupExpired(); err != nil {
		t.Fatalf("cleanup staging: %v", err)
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("expected expired orphan file to be removed, got %v", err)
	}
}

func TestUploadStagingClaimAllowsOnlyOneConcurrentConsumer(t *testing.T) {
	staging := NewUploadStagingService(t.TempDir(), time.Hour)
	staged, err := staging.StageBytes("guide.md", []byte("staged content"), "test")
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, claimErr := staging.Claim(staged.ID)
			results <- claimErr
		}()
	}
	wg.Wait()
	close(results)

	var successes, failures int
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("expected one claim success and one failure, successes=%d failures=%d", successes, failures)
	}
	if err := staging.Release(staged.ID); err != nil {
		t.Fatalf("release claimed upload: %v", err)
	}
}

func TestUploadStagingEnforcesPrincipalAndGlobalQuotas(t *testing.T) {
	staging := NewUploadStagingServiceWithLimits(t.TempDir(), time.Hour, UploadStagingLimits{
		MaxFilesPerPrincipal: 2,
		MaxBytesPerPrincipal: 10,
		MaxBytes:             15,
	})
	ownerA := AuthPrincipal{AuthType: "api_key", APIKeyID: "key-a"}
	ownerB := AuthPrincipal{AuthType: "api_key", APIKeyID: "key-b"}

	if _, err := staging.StageBytesAs("first.md", []byte("123456"), "test", ownerA); err != nil {
		t.Fatalf("stage first upload: %v", err)
	}
	if _, err := staging.StageBytesAs("second.md", []byte("12345"), "test", ownerA); err == nil {
		t.Fatal("expected principal byte quota to reject the second upload")
	} else if !errors.Is(err, ErrUploadStagingQuotaExceeded) {
		t.Fatalf("expected staging quota error, got %v", err)
	}

	if _, err := staging.StageBytesAs("other.md", []byte("123456"), "test", ownerB); err != nil {
		t.Fatalf("stage upload for another principal: %v", err)
	}
	if _, err := staging.StageBytesAs("global.md", []byte("1234"), "test", ownerB); err == nil {
		t.Fatal("expected global byte quota to reject the upload")
	}
}

func TestUploadStagingEnforcesPrincipalFileLimitAtomically(t *testing.T) {
	staging := NewUploadStagingServiceWithLimits(t.TempDir(), time.Hour, UploadStagingLimits{
		MaxFilesPerPrincipal: 1,
	})
	owner := AuthPrincipal{AuthType: "session", UserID: "user-a"}

	if _, err := staging.StageBytesAs("first.md", []byte("content"), "test", owner); err != nil {
		t.Fatalf("stage first upload: %v", err)
	}
	if _, err := staging.StageBytesAs("second.md", []byte("content"), "test", owner); err == nil {
		t.Fatal("expected principal file quota to reject the second active upload")
	}
}

func TestUploadStagingReservesConcurrentUploadsBeforeReading(t *testing.T) {
	staging := NewUploadStagingServiceWithLimits(t.TempDir(), time.Hour, UploadStagingLimits{
		MaxFilesPerPrincipal: 1,
	})
	owner := AuthPrincipal{AuthType: "api_key", APIKeyID: "key-a"}
	reader := &blockingUploadReader{started: make(chan struct{}), release: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		_, err := staging.stageFromReader("first.md", int64(len("content")), reader, "test", owner)
		result <- err
	}()
	<-reader.started

	if _, err := staging.StageBytesAs("second.md", []byte("content"), "test", owner); err == nil {
		t.Fatal("expected in-flight upload to consume the principal file quota")
	}
	close(reader.release)
	if err := <-result; err != nil {
		t.Fatalf("complete first upload: %v", err)
	}
}

func TestUploadStagingRestoresManifestAfterServiceRestart(t *testing.T) {
	rootDir := t.TempDir()
	staging := NewUploadStagingService(rootDir, time.Hour)
	staged, err := staging.StageBytes("restart.md", []byte("restart content"), "test")
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}

	restarted := NewUploadStagingService(rootDir, time.Hour)
	restored, err := restarted.Get(staged.ID)
	if err != nil {
		t.Fatalf("restore staged upload: %v", err)
	}
	content, err := os.ReadFile(restored.Path)
	if err != nil {
		t.Fatalf("read restored staged upload: %v", err)
	}
	if string(content) != "restart content" {
		t.Fatalf("unexpected restored content: %q", content)
	}
}

func TestUploadStagingExpiredProcessingLeaseCanBeRecoveredAfterRestart(t *testing.T) {
	rootDir := t.TempDir()
	owner := AuthPrincipal{AuthType: "session", UserID: "restart-owner"}
	staging := NewUploadStagingService(rootDir, time.Hour)
	staged, err := staging.StageBytesAs("recover.md", []byte("recoverable staged content"), "test", owner)
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}

	claimed, err := staging.ClaimWithLeaseAs(staged.ID, owner, "worker-a", time.Millisecond)
	if err != nil {
		t.Fatalf("claim upload: %v", err)
	}
	if claimed.Status != stagedUploadStatusProcessing || claimed.ProcessingAttempt != 1 {
		t.Fatalf("expected first processing lease, got %+v", claimed)
	}
	time.Sleep(10 * time.Millisecond)
	if stagedUploadLeaseActive(claimed, time.Now().UTC()) {
		t.Fatal("expected the short processing lease to expire")
	}

	restarted := NewUploadStagingService(rootDir, time.Hour)
	recovered, err := restarted.ClaimWithLeaseAs(staged.ID, owner, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("recover expired processing upload: %v", err)
	}
	if recovered.Status != stagedUploadStatusProcessing || recovered.ProcessingOwner != "worker-b" || recovered.ProcessingAttempt != 2 {
		t.Fatalf("expected expired processing lease to be reclaimed, got %+v", recovered)
	}
}

func TestUploadStagingCopyToRejectsModifiedFileByChecksum(t *testing.T) {
	rootDir := t.TempDir()
	destinationDir := t.TempDir()
	staging := NewUploadStagingService(rootDir, time.Hour)
	staged, err := staging.StageBytes("checksum.md", []byte("original"), "test")
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}
	if err := os.WriteFile(staged.Path, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("tamper staged upload: %v", err)
	}

	if _, err := staging.CopyTo(staged.ID, destinationDir); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	restored, err := staging.Get(staged.ID)
	if err != nil {
		t.Fatalf("get staged upload after checksum rejection: %v", err)
	}
	if restored.Status != stagedUploadStatusStaged {
		t.Fatalf("expected checksum rejection to keep upload staged, got %+v", restored)
	}
	entries, err := os.ReadDir(destinationDir)
	if err != nil {
		t.Fatalf("read destination directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no permanent file after checksum rejection, found %d entries", len(entries))
	}
}

func TestUploadStagingRejectsManifestPathTraversal(t *testing.T) {
	rootDir := t.TempDir()
	manifest := stagedUploadManifest{
		Version: 1,
		Items: []persistedStagedUpload{{
			ID:         "upl-traversal",
			FileName:   "outside.md",
			StoredName: "../outside.md",
			Status:     stagedUploadStatusStaged,
			ExpiresAt:  time.Now().Add(time.Hour).Format(time.RFC3339),
		}},
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "manifest.json"), content, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	staging := NewUploadStagingService(rootDir, time.Hour)
	if _, err := staging.Get("upl-traversal"); err == nil {
		t.Fatal("expected path traversal entry to be ignored")
	}
}

func TestUploadStagingMarkConsumedRollsBackWhenManifestWriteFails(t *testing.T) {
	rootDir := t.TempDir()
	staging := NewUploadStagingService(rootDir, time.Hour)
	staged, err := staging.StageBytes("rollback.md", []byte("rollback content"), "test")
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}

	staging.manifestPath = rootDir
	if err := staging.MarkConsumed(staged.ID); err == nil {
		t.Fatal("expected manifest persistence failure")
	}
	restored, err := staging.Get(staged.ID)
	if err != nil {
		t.Fatalf("get upload after rollback: %v", err)
	}
	if restored.Status != stagedUploadStatusStaged || restored.ConsumedAt != "" {
		t.Fatalf("expected staged state to be restored, got %+v", restored)
	}
}

func TestUploadStagingCleanupDoesNotDeleteManifest(t *testing.T) {
	rootDir := t.TempDir()
	staging := NewUploadStagingService(rootDir, time.Minute)
	manifestPath := filepath.Join(rootDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"version":1,"items":[]}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(manifestPath, old, old); err != nil {
		t.Fatalf("age manifest: %v", err)
	}

	if err := staging.CleanupExpired(); err != nil {
		t.Fatalf("cleanup staging: %v", err)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected manifest to remain after cleanup: %v", err)
	}
}
