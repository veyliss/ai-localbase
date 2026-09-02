package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

func durableMCPTestConfig(root string) model.ServerConfig {
	return model.ServerConfig{
		UploadDir:         filepath.Join(root, "uploads"),
		StagingDir:        filepath.Join(root, "staging"),
		IndexedContentDir: filepath.Join(root, "indexed-content"),
		StateFile:         filepath.Join(root, "app-state.json"),
		QdrantVectorSize:  4,
	}
}

func TestMCPRetryRejectsChangedStagedMetadataAfterRestart(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	jobStore, err := NewMCPJobStore(filepath.Join(root, "mcp-jobs.db"))
	if err != nil {
		t.Fatalf("create job store: %v", err)
	}
	service := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, jobStore)
	defer func() {
		if err := service.ShutdownJobs(context.Background()); err != nil {
			t.Errorf("shutdown service: %v", err)
		}
		if err := jobStore.Close(); err != nil {
			t.Errorf("close job store: %v", err)
		}
	}()
	owner := AuthPrincipal{AuthType: "session", UserID: "checksum-owner"}
	kbID, err := service.ResolveKnowledgeBaseID("")
	if err != nil {
		t.Fatalf("resolve knowledge base: %v", err)
	}
	staged, err := service.StageInlineUploadAs("checksum.txt", []byte("original"), "test", owner)
	if err != nil {
		t.Fatalf("stage checksum fixture: %v", err)
	}
	if err := os.WriteFile(staged.Path, []byte("changed"), 0o600); err != nil {
		t.Fatalf("modify staged fixture: %v", err)
	}
	changedChecksum, err := checksumFile(staged.Path)
	if err != nil {
		t.Fatalf("checksum changed staged fixture: %v", err)
	}
	service.staging.mu.Lock()
	changedMetadata := service.staging.items[staged.ID]
	changedMetadata.SHA256 = changedChecksum
	service.staging.items[staged.ID] = changedMetadata
	service.staging.mu.Unlock()

	_, err = service.startMCPJobFromDescriptor(mcpJobDescriptor{
		Version:         mcpJobDescriptorVersion,
		Type:            "import",
		KnowledgeBaseID: kbID,
		FileName:        staged.FileName,
		UploadID:        staged.ID,
		Checksum:        staged.SHA256,
	}, owner)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected changed single-file retry to be rejected, got %v", err)
	}

	second, err := service.StageInlineUploadAs("checksum-2.txt", []byte("second"), "test", owner)
	if err != nil {
		t.Fatalf("stage batch checksum fixture: %v", err)
	}
	if err := os.WriteFile(second.Path, []byte("changed-second"), 0o600); err != nil {
		t.Fatalf("modify batch staged fixture: %v", err)
	}
	changedSecondChecksum, err := checksumFile(second.Path)
	if err != nil {
		t.Fatalf("checksum changed batch fixture: %v", err)
	}
	service.staging.mu.Lock()
	changedSecondMetadata := service.staging.items[second.ID]
	changedSecondMetadata.SHA256 = changedSecondChecksum
	service.staging.items[second.ID] = changedSecondMetadata
	service.staging.mu.Unlock()
	failed := testMCPJobRecord("job-batch-checksum-retry", time.Now().UTC())
	failed.Job.Type = "batch-index"
	failed.Job.Status = "failed"
	failed.Job.Retryable = true
	failed.Job.Resumable = true
	failed.Job.OwnerUserID = owner.UserID
	failed.Descriptor = mcpJobDescriptor{
		Version:         mcpJobDescriptorVersion,
		Type:            "batch-index",
		KnowledgeBaseID: kbID,
		UploadIDs:       []string{second.ID},
		Concurrency:     1,
	}
	if err := jobStore.Create(failed); err != nil {
		t.Fatalf("create failed batch job: %v", err)
	}
	if err := jobStore.CreateItems(failed.Job.ID, []mcpJobItem{{
		UploadID:  second.ID,
		FileName:  second.FileName,
		Checksum:  second.SHA256,
		Status:    mcpJobItemFailed,
		Retryable: true,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}}); err != nil {
		t.Fatalf("create failed batch checkpoint: %v", err)
	}
	_, err = service.RetryMCPJobAs(failed.Job.ID, owner)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected changed batch retry to be rejected, got %v", err)
	}
}

func waitForMCPJobStatus(t *testing.T, service *AppService, jobID string, owner AuthPrincipal, expected string) model.MCPJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := service.GetMCPJobStatusAs(jobID, owner)
		if err == nil && job.Status == expected {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, err := service.GetMCPJobStatusAs(jobID, owner)
	t.Fatalf("wait for MCP job %s to become %s: job=%+v err=%v", jobID, expected, job, err)
	return model.MCPJob{}
}

func TestMCPJobServiceRecoversPersistedImportAfterRestart(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	jobStorePath := filepath.Join(root, "mcp-jobs.db")
	owner := AuthPrincipal{AuthType: "session", UserID: "user-restart"}

	store, err := NewMCPJobStore(jobStorePath)
	if err != nil {
		t.Fatalf("create initial job store: %v", err)
	}
	first := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, store)
	kbID, err := first.ResolveKnowledgeBaseID("")
	if err != nil {
		t.Fatalf("resolve default knowledge base: %v", err)
	}
	staged, err := first.StageInlineUploadAs("restart.txt", []byte("\n"), "test", owner)
	if err != nil {
		t.Fatalf("stage restart fixture: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	jobID := "job-restart-import"
	if err := store.Create(mcpJobStoreRecord{
		Job: model.MCPJob{
			ID:            jobID,
			Type:          "import",
			Status:        "queued",
			Summary:       "等待重启后恢复",
			Retryable:     true,
			Resumable:     true,
			CreatedAt:     now,
			UpdatedAt:     now,
			OwnerUserID:   owner.UserID,
			OwnerAPIKeyID: owner.APIKeyID,
		},
		Descriptor: mcpJobDescriptor{
			Version:         mcpJobDescriptorVersion,
			Type:            "import",
			KnowledgeBaseID: kbID,
			FileName:        staged.FileName,
			UploadID:        staged.ID,
		},
	}); err != nil {
		t.Fatalf("persist restart job: %v", err)
	}

	if err := first.ShutdownJobs(context.Background()); err != nil {
		t.Fatalf("shutdown first service: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close first job store: %v", err)
	}

	reopenedStore, err := NewMCPJobStore(jobStorePath)
	if err != nil {
		t.Fatalf("reopen job store: %v", err)
	}
	second := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, reopenedStore)
	defer func() {
		if err := second.ShutdownJobs(context.Background()); err != nil {
			t.Errorf("shutdown recovered service: %v", err)
		}
		if err := reopenedStore.Close(); err != nil {
			t.Errorf("close recovered job store: %v", err)
		}
	}()

	recovered := waitForMCPJobStatus(t, second, jobID, owner, "succeeded")
	if recovered.Attempt < 1 || recovered.RecoveryState != "recovered" {
		t.Fatalf("expected restart metadata to be retained, got %+v", recovered)
	}
	if len(recovered.Result) == 0 || recovered.Result["knowledgeBaseId"] != kbID {
		t.Fatalf("expected recovered import result, got %#v", recovered.Result)
	}

	if _, err := second.GetMCPJobStatusAs(jobID, AuthPrincipal{AuthType: "session", UserID: "other-user"}); err == nil {
		t.Fatal("expected recovered job ownership to remain isolated")
	}
}

func TestMCPJobServiceRecoversAnonymousImportAfterRestart(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	jobStorePath := filepath.Join(root, "mcp-jobs.db")

	store, err := NewMCPJobStore(jobStorePath)
	if err != nil {
		t.Fatalf("create initial anonymous job store: %v", err)
	}
	first := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, store)
	kbID, err := first.ResolveKnowledgeBaseID("")
	if err != nil {
		t.Fatalf("resolve anonymous knowledge base: %v", err)
	}
	staged, err := first.StageInlineUploadAs("anonymous-restart.txt", []byte("\n"), "test", AuthPrincipal{})
	if err != nil {
		t.Fatalf("stage anonymous restart fixture: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	jobID := "job-anonymous-restart-import"
	if err := store.Create(mcpJobStoreRecord{
		Job: model.MCPJob{
			ID:        jobID,
			Type:      "import",
			Status:    "queued",
			Summary:   "等待匿名任务恢复",
			Retryable: true,
			Resumable: true,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Descriptor: mcpJobDescriptor{
			Version:         mcpJobDescriptorVersion,
			Type:            "import",
			KnowledgeBaseID: kbID,
			FileName:        staged.FileName,
			UploadID:        staged.ID,
		},
	}); err != nil {
		t.Fatalf("persist anonymous restart job: %v", err)
	}
	if err := first.ShutdownJobs(context.Background()); err != nil {
		t.Fatalf("shutdown initial anonymous service: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial anonymous job store: %v", err)
	}

	reopenedStore, err := NewMCPJobStore(jobStorePath)
	if err != nil {
		t.Fatalf("reopen anonymous job store: %v", err)
	}
	second := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, reopenedStore)
	defer func() {
		if err := second.ShutdownJobs(context.Background()); err != nil {
			t.Errorf("shutdown recovered anonymous service: %v", err)
		}
		if err := reopenedStore.Close(); err != nil {
			t.Errorf("close recovered anonymous job store: %v", err)
		}
	}()

	recovered := waitForMCPJobStatus(t, second, jobID, AuthPrincipal{}, "succeeded")
	if recovered.OwnerUserID != "" || recovered.OwnerAPIKeyID != "" {
		t.Fatalf("expected anonymous owner to remain empty, got %+v", recovered)
	}
	if _, err := second.staging.Get(staged.ID); err == nil {
		t.Fatal("expected recovered anonymous import to consume staged upload")
	}
}

func TestRegisterStagedUploadIsIdempotentAfterIndexedDocumentExists(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	service := NewAppService(nil, NewAppStateStore(config.StateFile), nil, config)
	owner := AuthPrincipal{AuthType: "session", UserID: "idempotent-owner"}
	kbID, err := service.ResolveKnowledgeBaseID("")
	if err != nil {
		t.Fatalf("resolve idempotent knowledge base: %v", err)
	}
	staged, err := service.StageInlineUploadAs("replayed.txt", []byte("replayed content"), "test", owner)
	if err != nil {
		t.Fatalf("stage idempotent fixture: %v", err)
	}
	existing := model.Document{
		ID:              "already-indexed-document",
		KnowledgeBaseID: kbID,
		Name:            "original.txt",
		Status:          "indexed",
		Version:         1,
		Checksum:        staged.SHA256,
		Source:          "upload",
	}
	if _, err := service.AddDocument(kbID, existing); err != nil {
		t.Fatalf("add existing indexed document: %v", err)
	}

	registered, err := service.RegisterStagedUploadAs(context.Background(), staged.ID, kbID, "replayed.txt", owner)
	if err != nil {
		t.Fatalf("register replayed staged upload: %v", err)
	}
	if registered.ID != existing.ID {
		t.Fatalf("expected existing document to be returned, got %+v", registered)
	}
	if _, err := service.staging.Get(staged.ID); err == nil {
		t.Fatal("expected replayed staged upload to be consumed")
	}
}

func TestMCPImportRecoveryConvergesAfterConsumedUpload(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	service := NewAppService(nil, NewAppStateStore(config.StateFile), nil, config)
	owner := AuthPrincipal{AuthType: "session", UserID: "recovery-owner"}
	kbID, err := service.ResolveKnowledgeBaseID("")
	if err != nil {
		t.Fatalf("resolve recovery knowledge base: %v", err)
	}
	staged, err := service.StageInlineUploadAs("already-consumed.txt", []byte("already indexed"), "test", owner)
	if err != nil {
		t.Fatalf("stage consumed recovery fixture: %v", err)
	}
	existing := model.Document{
		ID:              "recovered-indexed-document",
		KnowledgeBaseID: kbID,
		Name:            staged.FileName,
		Size:            staged.Size,
		SizeLabel:       staged.SizeLabel,
		Status:          "indexed",
		Checksum:        staged.SHA256,
		Source:          "upload",
		Version:         1,
	}
	if _, err := service.AddDocument(kbID, existing); err != nil {
		t.Fatalf("add recovered indexed document: %v", err)
	}
	if err := service.staging.MarkConsumed(staged.ID); err != nil {
		t.Fatalf("mark staged recovery fixture consumed: %v", err)
	}
	if err := service.staging.Delete(staged.ID); err != nil {
		t.Fatalf("delete consumed recovery fixture: %v", err)
	}

	jobID := "job-consumed-upload-recovery"
	job := model.MCPJob{
		ID:          jobID,
		Type:        "import",
		Status:      "running",
		Retryable:   true,
		Resumable:   true,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		OwnerUserID: owner.UserID,
	}
	descriptor := mcpJobDescriptor{
		Version:         mcpJobDescriptorVersion,
		Type:            "import",
		KnowledgeBaseID: kbID,
		FileName:        staged.FileName,
		UploadID:        staged.ID,
		Checksum:        staged.SHA256,
	}
	service.mcpJobMu.Lock()
	service.mcpJobs[jobID] = job
	service.mcpJobDescriptors[jobID] = descriptor
	service.mcpJobMu.Unlock()

	service.runMCPImportJob(context.Background(), jobID, descriptor, owner)
	recovered, err := service.GetMCPJobStatusAs(jobID, owner)
	if err != nil {
		t.Fatalf("get recovered consumed-upload job: %v", err)
	}
	if recovered.Status != "succeeded" || recovered.Result["knowledgeBaseId"] != kbID {
		t.Fatalf("expected recovery to converge on existing document, got %+v", recovered)
	}
	if recovered.Error != "" {
		t.Fatalf("expected no recovery error, got %q", recovered.Error)
	}
}

func TestMCPJobServiceRecoversExpiredRunningImportAfterCrash(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	owner := AuthPrincipal{AuthType: "session", UserID: "user-crash"}

	// Stage the input without starting a job store monitor. Closing the store
	// below then simulates a process crash that never released its lease.
	stagingService := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, nil)
	kbID, err := stagingService.ResolveKnowledgeBaseID("")
	if err != nil {
		t.Fatalf("resolve crash fixture knowledge base: %v", err)
	}
	staged, err := stagingService.StageInlineUploadAs("crash.txt", []byte("\n"), "test", owner)
	if err != nil {
		t.Fatalf("stage crash fixture: %v", err)
	}

	jobStorePath := filepath.Join(root, "mcp-jobs.db")
	store, err := NewMCPJobStore(jobStorePath)
	if err != nil {
		t.Fatalf("create crash job store: %v", err)
	}
	now := time.Now().UTC()
	jobID := "job-crash-import"
	if err := store.Create(mcpJobStoreRecord{
		Job: model.MCPJob{
			ID:          jobID,
			Type:        "import",
			Status:      "queued",
			Summary:     "模拟进程崩溃",
			Retryable:   true,
			Resumable:   true,
			CreatedAt:   now.Format(time.RFC3339Nano),
			UpdatedAt:   now.Format(time.RFC3339Nano),
			OwnerUserID: owner.UserID,
		},
		Descriptor: mcpJobDescriptor{
			Version:         mcpJobDescriptorVersion,
			Type:            "import",
			KnowledgeBaseID: kbID,
			FileName:        staged.FileName,
			UploadID:        staged.ID,
		},
	}); err != nil {
		t.Fatalf("persist crash job: %v", err)
	}
	if _, claimed, err := store.Claim(jobID, "crashed-worker", time.Nanosecond, now); err != nil || !claimed {
		t.Fatalf("claim crash lease: claimed=%t err=%v", claimed, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close crashed job store: %v", err)
	}

	reopenedStore, err := NewMCPJobStore(jobStorePath)
	if err != nil {
		t.Fatalf("reopen crashed job store: %v", err)
	}
	second := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, reopenedStore)
	defer func() {
		if err := second.ShutdownJobs(context.Background()); err != nil {
			t.Errorf("shutdown crash recovery service: %v", err)
		}
		if err := reopenedStore.Close(); err != nil {
			t.Errorf("close crash recovery store: %v", err)
		}
	}()

	recovered := waitForMCPJobStatus(t, second, jobID, owner, "succeeded")
	if recovered.Attempt != 2 || recovered.RecoveryState != "recovered" {
		t.Fatalf("expected expired lease to be recovered exactly once, got %+v", recovered)
	}
}

func TestMCPJobServiceGracefulShutdownReleasesLeaseForNextRestart(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	store, err := NewMCPJobStore(filepath.Join(root, "mcp-jobs.db"))
	if err != nil {
		t.Fatalf("create job store: %v", err)
	}
	service := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, store)
	defer store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	job := model.MCPJob{
		ID:          "job-graceful-shutdown",
		Type:        "import",
		Status:      "queued",
		Summary:     "等待执行",
		Resumable:   true,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		OwnerUserID: "user-shutdown",
	}
	descriptor := mcpJobDescriptor{Version: mcpJobDescriptorVersion, Type: "import", UploadID: "upload-shutdown", FileName: "shutdown.txt"}
	ok, err := service.registerMCPJobWithDescriptor(job, cancel, nil, descriptor)
	if err != nil || !ok {
		t.Fatalf("register shutdown job: ok=%t err=%v", ok, err)
	}
	workerDone := make(chan struct{})
	go func() {
		defer service.mcpJobWG.Done()
		defer close(workerDone)
		<-ctx.Done()
	}()

	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	if err := service.ShutdownJobs(shutdownContext); err != nil {
		t.Fatalf("graceful shutdown: %v", err)
	}
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("expected shutdown worker to exit")
	}

	record, found, err := store.Get(job.ID)
	if err != nil || !found {
		t.Fatalf("read released job: found=%t err=%v", found, err)
	}
	if record.Job.Status != "queued" || record.Job.RecoveryState != "shutdown" || record.LeaseOwner != "" {
		t.Fatalf("expected queued job without lease after shutdown, got %+v", record)
	}
	if record.Descriptor.UploadID != descriptor.UploadID {
		t.Fatalf("expected descriptor to survive shutdown, got %+v", record.Descriptor)
	}
}

func TestMCPJobServiceGracefulShutdownReleasesTrackedStagingLease(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	store, err := NewMCPJobStore(filepath.Join(root, "mcp-jobs.db"))
	if err != nil {
		t.Fatalf("create job store: %v", err)
	}
	service := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, store)
	defer func() {
		if err := service.ShutdownJobs(context.Background()); err != nil {
			t.Errorf("shutdown service: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Errorf("close job store: %v", err)
		}
	}()

	owner := AuthPrincipal{AuthType: "session", UserID: "user-staging-shutdown"}
	staged, err := service.StageInlineUploadAs("shutdown-source.txt", []byte("source"), "test", owner)
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}
	job := model.MCPJob{
		ID:          "job-staging-shutdown",
		Type:        "import",
		Status:      "queued",
		Summary:     "等待执行",
		Resumable:   true,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		OwnerUserID: owner.UserID,
	}
	ok, err := service.registerMCPJobWithDescriptor(job, func() {}, nil, mcpJobDescriptor{
		Version:  mcpJobDescriptorVersion,
		Type:     "import",
		UploadID: staged.ID,
		FileName: staged.FileName,
	})
	if err != nil || !ok {
		t.Fatalf("register job: ok=%t err=%v", ok, err)
	}
	service.mcpJobWG.Done()

	leaseOwner := service.mcpStagingLeaseOwner(job.ID)
	claimed, err := service.staging.ClaimWithLeaseAs(staged.ID, owner, leaseOwner, mcpJobLeaseDuration)
	if err != nil {
		t.Fatalf("claim staged upload: %v", err)
	}
	service.trackMCPStagingLease(job.ID, staged.ID, leaseOwner, claimed.ProcessingAttempt)

	service.releaseMCPJobLease(job.ID)
	released, err := service.staging.Get(staged.ID)
	if err != nil {
		t.Fatalf("read released staged upload: %v", err)
	}
	if released.Status != stagedUploadStatusStaged || released.ProcessingOwner != "" {
		t.Fatalf("expected staged upload lease to be released, got %+v", released)
	}

	if _, err := service.staging.ClaimWithLeaseAs(staged.ID, owner, "next-worker", time.Minute); err != nil {
		t.Fatalf("expected next worker to claim released upload immediately: %v", err)
	}
}

func TestMCPJobServiceExplicitCancellationIsNotRecoverable(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	jobStorePath := filepath.Join(root, "mcp-jobs.db")
	store, err := NewMCPJobStore(jobStorePath)
	if err != nil {
		t.Fatalf("create job store: %v", err)
	}
	service := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, store)
	owner := AuthPrincipal{AuthType: "session", UserID: "user-cancel"}
	ctx, cancel := context.WithCancel(context.Background())
	job := model.MCPJob{
		ID:          "job-explicit-cancel",
		Type:        "import",
		Status:      "queued",
		Summary:     "等待执行",
		Retryable:   true,
		Resumable:   true,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		OwnerUserID: owner.UserID,
	}
	ok, err := service.registerMCPJobWithDescriptor(job, cancel, nil, mcpJobDescriptor{
		Version: mcpJobDescriptorVersion,
		Type:    "import",
	})
	if err != nil || !ok {
		t.Fatalf("register cancellation job: ok=%t err=%v", ok, err)
	}
	workerDone := make(chan struct{})
	go func() {
		defer service.mcpJobWG.Done()
		defer close(workerDone)
		<-ctx.Done()
	}()

	cancelled, err := service.CancelMCPJobAs(job.ID, owner)
	if err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	if cancelled.Status != "cancelled" || cancelled.Resumable || cancelled.Retryable {
		t.Fatalf("expected cancellation to disable recovery and retry, got %+v", cancelled)
	}
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("expected cancelled worker to exit")
	}
	if err := service.ShutdownJobs(context.Background()); err != nil {
		t.Fatalf("shutdown cancelled service: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close first job store: %v", err)
	}

	reopenedStore, err := NewMCPJobStore(jobStorePath)
	if err != nil {
		t.Fatalf("reopen job store: %v", err)
	}
	restarted := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, reopenedStore)
	defer func() {
		if err := restarted.ShutdownJobs(context.Background()); err != nil {
			t.Errorf("shutdown restarted service: %v", err)
		}
		if err := reopenedStore.Close(); err != nil {
			t.Errorf("close restarted job store: %v", err)
		}
	}()

	loaded := waitForMCPJobStatus(t, restarted, job.ID, owner, "cancelled")
	if loaded.Resumable || loaded.Status != "cancelled" {
		t.Fatalf("expected cancelled job to remain terminal after restart, got %+v", loaded)
	}
}

func TestMCPBatchJobPersistsItemCheckpointsAndDoesNotDuplicateCompletedDocuments(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	store, err := NewMCPJobStore(filepath.Join(root, "mcp-jobs.db"))
	if err != nil {
		t.Fatalf("create job store: %v", err)
	}
	service := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, store)
	owner := AuthPrincipal{AuthType: "session", UserID: "user-batch"}
	defer func() {
		if err := service.ShutdownJobs(context.Background()); err != nil {
			t.Errorf("shutdown batch service: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Errorf("close batch store: %v", err)
		}
	}()

	kbID, err := service.ResolveKnowledgeBaseID("")
	if err != nil {
		t.Fatalf("resolve batch knowledge base: %v", err)
	}
	first, err := service.StageInlineUploadAs("first.txt", []byte(" \n"), "test", owner)
	if err != nil {
		t.Fatalf("stage first batch fixture: %v", err)
	}
	second, err := service.StageInlineUploadAs("second.txt", []byte("  \n"), "test", owner)
	if err != nil {
		t.Fatalf("stage second batch fixture: %v", err)
	}
	job, err := service.StartBatchIndexJobAs(kbID, []string{first.ID, second.ID, first.ID}, 2, owner)
	if err != nil {
		t.Fatalf("start batch job: %v", err)
	}
	completed := waitForMCPJobStatus(t, service, job.ID, owner, "succeeded")
	if completed.Result["successful"] != float64(2) && completed.Result["successful"] != 2 {
		t.Fatalf("expected two successful batch items, got %#v", completed.Result)
	}

	items, err := store.ListItems(job.ID)
	if err != nil {
		t.Fatalf("list batch items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected duplicate upload ID to be de-duplicated, got %d items", len(items))
	}
	for _, item := range items {
		if item.Status != mcpJobItemSucceeded || item.DocumentID == "" || item.Checksum == "" {
			t.Fatalf("expected durable successful checkpoint, got %+v", item)
		}
	}

	documents, err := service.GetKnowledgeBaseDocuments(kbID)
	if err != nil {
		t.Fatalf("list indexed documents: %v", err)
	}
	if len(documents) != 2 {
		t.Fatalf("expected two indexed documents, got %d", len(documents))
	}

	record, found, err := store.Get(job.ID)
	if err != nil || !found {
		t.Fatalf("load completed batch job: found=%t err=%v", found, err)
	}
	record.Job.Status = "queued"
	record.Job.Resumable = true
	record.Job.Retryable = true
	record.Job.Progress = 0
	record.Job.CompletedAt = ""
	record.Job.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := resetMCPJobForCheckpointReplay(store, record); err != nil {
		t.Fatalf("reset batch job for checkpoint replay: %v", err)
	}
	service.mcpJobMu.Lock()
	service.mcpJobs[job.ID] = record.Job
	service.mcpJobDescriptors[job.ID] = record.Descriptor
	service.mcpJobMu.Unlock()
	service.runBatchIndexJob(context.Background(), job.ID, kbID, []string{first.ID, second.ID}, 2, owner)

	documents, err = service.GetKnowledgeBaseDocuments(kbID)
	if err != nil {
		t.Fatalf("list documents after checkpoint replay: %v", err)
	}
	if len(documents) != 2 {
		t.Fatalf("completed checkpoint replay duplicated documents, got %d", len(documents))
	}
}

func TestMCPBatchRecoveryConvergesAfterConsumedUpload(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	jobStorePath := filepath.Join(root, "mcp-jobs.db")
	owner := AuthPrincipal{AuthType: "session", UserID: "batch-recovery-owner"}

	store, err := NewMCPJobStore(jobStorePath)
	if err != nil {
		t.Fatalf("create initial batch recovery store: %v", err)
	}
	first := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, store)
	kbID, err := first.ResolveKnowledgeBaseID("")
	if err != nil {
		t.Fatalf("resolve batch recovery knowledge base: %v", err)
	}

	staged, err := first.StageInlineUploadAs("batch-consumed.txt", []byte("\n"), "test", owner)
	if err != nil {
		t.Fatalf("stage batch recovery fixture: %v", err)
	}
	indexed, err := first.registerStagedUploadAs(context.Background(), staged.ID, kbID, staged.FileName, owner, "pre-crash-worker", staged.SHA256)
	if err != nil {
		t.Fatalf("index batch recovery fixture before simulated crash: %v", err)
	}
	if !isIndexedDocumentStatus(indexed.Status) {
		t.Fatalf("expected indexed document before simulated crash, got %+v", indexed)
	}
	if _, err := first.staging.Get(staged.ID); err == nil {
		t.Fatal("expected source staging record to be consumed before simulated crash")
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	jobID := "job-batch-consumed-upload-recovery"
	job := model.MCPJob{
		ID:          jobID,
		Type:        "batch-index",
		Status:      "queued",
		Summary:     "等待批量任务恢复",
		Retryable:   true,
		Resumable:   true,
		CreatedAt:   now,
		UpdatedAt:   now,
		OwnerUserID: owner.UserID,
	}
	descriptor := mcpJobDescriptor{
		Version:         mcpJobDescriptorVersion,
		Type:            "batch-index",
		KnowledgeBaseID: kbID,
		UploadIDs:       []string{staged.ID},
		Concurrency:     1,
	}
	if err := store.Create(mcpJobStoreRecord{Job: job, Descriptor: descriptor}); err != nil {
		t.Fatalf("persist batch recovery job: %v", err)
	}
	if err := store.CreateItems(jobID, []mcpJobItem{{
		JobID:     jobID,
		UploadID:  staged.ID,
		FileName:  staged.FileName,
		Checksum:  staged.SHA256,
		Status:    mcpJobItemPending,
		Retryable: true,
		UpdatedAt: now,
	}}); err != nil {
		t.Fatalf("persist pending batch checkpoint: %v", err)
	}

	if err := first.ShutdownJobs(context.Background()); err != nil {
		t.Fatalf("shutdown initial batch recovery service: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial batch recovery store: %v", err)
	}

	reopenedStore, err := NewMCPJobStore(jobStorePath)
	if err != nil {
		t.Fatalf("reopen batch recovery store: %v", err)
	}
	second := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, reopenedStore)
	defer func() {
		if err := second.ShutdownJobs(context.Background()); err != nil {
			t.Errorf("shutdown recovered batch service: %v", err)
		}
		if err := reopenedStore.Close(); err != nil {
			t.Errorf("close recovered batch store: %v", err)
		}
	}()

	recovered := waitForMCPJobStatus(t, second, jobID, owner, "succeeded")
	if successful := recovered.Result["successful"]; successful != float64(1) && successful != 1 {
		t.Fatalf("expected consumed upload to converge as one success, got %#v", recovered.Result)
	}
	items, err := reopenedStore.ListItems(jobID)
	if err != nil {
		t.Fatalf("list recovered batch checkpoint: %v", err)
	}
	if len(items) != 1 || items[0].Status != mcpJobItemSucceeded || items[0].DocumentID != indexed.ID {
		t.Fatalf("expected pending checkpoint to converge to existing document, got %+v", items)
	}
	documents, err := second.GetKnowledgeBaseDocuments(kbID)
	if err != nil {
		t.Fatalf("list documents after batch recovery: %v", err)
	}
	if len(documents) != 1 || documents[0].ID != indexed.ID {
		t.Fatalf("expected exactly one indexed document after batch recovery, got %+v", documents)
	}
	if _, err := second.staging.Get(staged.ID); err == nil {
		t.Fatal("expected consumed upload to remain unavailable after batch recovery")
	}
}

func TestMCPBatchRecoveryRecreatesMissingCheckpoint(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	store, err := NewMCPJobStore(filepath.Join(root, "mcp-jobs.db"))
	if err != nil {
		t.Fatalf("create job store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close job store: %v", err)
		}
	}()

	service := NewAppService(nil, NewAppStateStore(config.StateFile), nil, config)
	service.mcpJobStore = store
	owner := AuthPrincipal{AuthType: "session", UserID: "missing-checkpoint-owner"}
	kbID, err := service.ResolveKnowledgeBaseID("")
	if err != nil {
		t.Fatalf("resolve knowledge base: %v", err)
	}
	staged, err := service.StageInlineUploadAs("missing-checkpoint.txt", []byte("\n"), "test", owner)
	if err != nil {
		t.Fatalf("stage batch upload: %v", err)
	}

	jobID := "job-missing-checkpoint"
	now := time.Now().UTC()
	job := model.MCPJob{
		ID:          jobID,
		Type:        "batch-index",
		Status:      "queued",
		Summary:     "等待恢复",
		Retryable:   true,
		Resumable:   true,
		CreatedAt:   now.Format(time.RFC3339Nano),
		UpdatedAt:   now.Format(time.RFC3339Nano),
		OwnerUserID: owner.UserID,
	}
	descriptor := mcpJobDescriptor{
		Version:         mcpJobDescriptorVersion,
		Type:            "batch-index",
		KnowledgeBaseID: kbID,
		UploadIDs:       []string{staged.ID},
		Concurrency:     1,
	}
	if err := store.Create(mcpJobStoreRecord{Job: job, Descriptor: descriptor}); err != nil {
		t.Fatalf("create parent job without checkpoint: %v", err)
	}
	claimed, ok, err := store.Claim(jobID, "checkpoint-worker", time.Minute, now)
	if err != nil || !ok {
		t.Fatalf("claim parent job: ok=%t err=%v", ok, err)
	}
	lease := mcpJobLease{Owner: claimed.LeaseOwner, Attempt: claimed.Job.Attempt, ExpiresAt: claimed.LeaseExpiresAt}
	service.mcpJobMu.Lock()
	service.mcpJobs[jobID] = claimed.Job
	service.mcpJobDescriptors[jobID] = claimed.Descriptor
	service.mcpJobLeases[jobID] = lease
	service.mcpJobMu.Unlock()

	service.runBatchIndexJobWithLease(context.Background(), jobID, kbID, []string{staged.ID}, 1, owner, lease)

	record, found, err := store.Get(jobID)
	if err != nil || !found {
		t.Fatalf("load recovered parent job: found=%t err=%v", found, err)
	}
	if record.Job.Status != "succeeded" {
		t.Fatalf("expected missing-checkpoint recovery to complete, got %+v", record.Job)
	}
	items, err := store.ListItems(jobID)
	if err != nil {
		t.Fatalf("list recreated checkpoint: %v", err)
	}
	if len(items) != 1 || items[0].Status != mcpJobItemSucceeded || items[0].DocumentID == "" {
		t.Fatalf("expected recreated checkpoint to be successful, got %+v", items)
	}
}

func TestMCPBatchCheckpointRecoveryIsFencedAfterWorkerTakeover(t *testing.T) {
	store := newTestMCPJobStore(t)
	now := time.Now().UTC()
	jobID := "job-checkpoint-takeover"
	record := testMCPJobRecord(jobID, now)
	record.Job.Type = "batch-index"
	record.Descriptor = mcpJobDescriptor{Version: mcpJobDescriptorVersion, Type: "batch-index", UploadIDs: []string{"upload-checkpoint"}}
	if err := store.Create(record); err != nil {
		t.Fatalf("create parent job: %v", err)
	}
	claimedA, ok, err := store.Claim(jobID, "worker-a", time.Nanosecond, now)
	if err != nil || !ok {
		t.Fatalf("claim worker-a: ok=%t err=%v", ok, err)
	}
	claimedB, ok, err := store.Claim(jobID, "worker-b", time.Minute, now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("claim worker-b after expiry: ok=%t err=%v", ok, err)
	}
	leaseA := mcpJobLease{Owner: claimedA.LeaseOwner, Attempt: claimedA.Job.Attempt, ExpiresAt: claimedA.LeaseExpiresAt}
	leaseB := mcpJobLease{Owner: claimedB.LeaseOwner, Attempt: claimedB.Job.Attempt, ExpiresAt: claimedB.LeaseExpiresAt}
	item := mcpJobItem{JobID: jobID, UploadID: "upload-checkpoint", Status: mcpJobItemPending, Retryable: true, UpdatedAt: now.Format(time.RFC3339Nano)}
	if err := store.CreateItems(jobID, []mcpJobItem{item}); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}

	if ensured, err := store.EnsureItemsForLease(jobID, []mcpJobItem{{UploadID: "upload-a", Status: mcpJobItemPending, Retryable: true}}, leaseA.Owner, leaseA.Attempt); err != nil {
		t.Fatalf("ensure stale checkpoint: %v", err)
	} else if ensured {
		t.Fatal("expected stale worker checkpoint creation to be rejected")
	}
	if ensured, err := store.EnsureItemsForLease(jobID, []mcpJobItem{{UploadID: "upload-b", Status: mcpJobItemPending, Retryable: true}}, leaseB.Owner, leaseB.Attempt); err != nil || !ensured {
		t.Fatalf("expected current worker checkpoint creation, ensured=%t err=%v", ensured, err)
	}
	items, err := store.ListItems(jobID)
	if err != nil {
		t.Fatalf("list fenced checkpoints: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected original and current-worker checkpoints only, got %+v", items)
	}
}

func TestMCPWorkerTakeoverRejectsAllStaleOperations(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	store, err := NewMCPJobStore(filepath.Join(root, "mcp-jobs.db"))
	if err != nil {
		t.Fatalf("create job store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close job store: %v", err)
		}
	}()

	service := NewAppService(nil, NewAppStateStore(config.StateFile), nil, config)
	service.mcpJobStore = store
	service.mcpWorkerID = "worker-b"
	jobID := "job-worker-takeover"
	now := time.Now().UTC()
	record := testMCPJobRecord(jobID, now)
	record.Job.Status = "running"
	record.Descriptor = mcpJobDescriptor{Version: mcpJobDescriptorVersion, Type: "batch-index", UploadIDs: []string{"upload-takeover"}}
	if err := store.Create(record); err != nil {
		t.Fatalf("create takeover job: %v", err)
	}
	claimedA, ok, err := store.Claim(jobID, "worker-a", time.Nanosecond, now)
	if err != nil || !ok {
		t.Fatalf("claim worker-a: ok=%t err=%v", ok, err)
	}
	claimedB, ok, err := store.Claim(jobID, "worker-b", time.Minute, now.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("claim worker-b: ok=%t err=%v", ok, err)
	}
	leaseA := mcpJobLease{Owner: claimedA.LeaseOwner, Attempt: claimedA.Job.Attempt, ExpiresAt: claimedA.LeaseExpiresAt}
	leaseB := mcpJobLease{Owner: claimedB.LeaseOwner, Attempt: claimedB.Job.Attempt, ExpiresAt: claimedB.LeaseExpiresAt}
	item := mcpJobItem{JobID: jobID, UploadID: "upload-takeover", Status: mcpJobItemPending, Retryable: true, UpdatedAt: now.Format(time.RFC3339Nano)}
	if err := store.CreateItems(jobID, []mcpJobItem{item}); err != nil {
		t.Fatalf("create takeover item: %v", err)
	}
	staged, err := service.StageInlineUploadAs("takeover.txt", []byte("takeover"), "test", AuthPrincipal{})
	if err != nil {
		t.Fatalf("stage takeover upload: %v", err)
	}
	stagingOwnerA := service.mcpStagingLeaseOwnerForLease(jobID, leaseA)
	claimedStagedA, err := service.staging.ClaimWithLeaseAs(staged.ID, AuthPrincipal{}, stagingOwnerA, time.Minute)
	if err != nil {
		t.Fatalf("claim staging lease for worker-a: %v", err)
	}
	service.staging.mu.Lock()
	stagingItem := service.staging.items[staged.ID]
	stagingItem.ProcessingLeaseUntil = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	service.staging.items[staged.ID] = stagingItem
	if err := service.staging.saveManifestLocked(); err != nil {
		service.staging.mu.Unlock()
		t.Fatalf("persist expired staging lease: %v", err)
	}
	service.staging.mu.Unlock()
	stagingOwnerB := service.mcpStagingLeaseOwnerForLease(jobID, leaseB)
	claimedStagedB, err := service.staging.ClaimWithLeaseAs(staged.ID, AuthPrincipal{}, stagingOwnerB, time.Minute)
	if err != nil {
		t.Fatalf("claim staging lease for worker-b: %v", err)
	}
	service.trackMCPStagingLeaseForJob(jobID, staged.ID, stagingOwnerA, claimedStagedA.ProcessingAttempt, leaseA.Attempt)
	service.trackMCPStagingLeaseForJob(jobID, staged.ID, stagingOwnerB, claimedStagedB.ProcessingAttempt, leaseB.Attempt)
	service.mcpJobMu.Lock()
	service.mcpJobs[jobID] = claimedB.Job
	service.mcpJobLeases[jobID] = leaseB
	service.mcpJobMu.Unlock()

	service.updateMCPJobWithLease(jobID, leaseA, func(job *model.MCPJob) {
		job.Summary = "旧 Worker 的写入"
	})
	if service.persistMCPBatchItemWithLease(item, leaseA) {
		t.Fatal("expected stale item update to be rejected")
	}
	if service.renewMCPJobLeaseForLease(jobID, leaseA) {
		t.Fatal("expected stale job heartbeat to be rejected")
	}
	if service.renewMCPJobStagingLeasesForLease(jobID, leaseA) {
		t.Fatal("expected stale staging heartbeat to be rejected")
	}
	service.releaseMCPJobStagingLeasesForLease(jobID, leaseA)
	if err := service.staging.ReleaseWithLeaseAttempt(staged.ID, stagingOwnerA, claimedStagedA.ProcessingAttempt); err == nil {
		t.Fatal("expected stale staging release to be rejected")
	}
	if err := service.staging.MarkConsumedWithLeaseAttempt(staged.ID, stagingOwnerA, claimedStagedA.ProcessingAttempt); err == nil {
		t.Fatal("expected stale staging consume to be rejected")
	}

	loaded, found, err := store.Get(jobID)
	if err != nil || !found {
		t.Fatalf("load takeover job: found=%t err=%v", found, err)
	}
	if loaded.Job.Summary == "旧 Worker 的写入" || loaded.LeaseOwner != leaseB.Owner || loaded.Job.Attempt != leaseB.Attempt {
		t.Fatalf("expected current worker job state to remain intact, got %+v", loaded)
	}
	loadedItem, found, err := store.GetItem(jobID, item.UploadID)
	if err != nil || !found {
		t.Fatalf("load takeover item: found=%t err=%v", found, err)
	}
	if loadedItem.Status != mcpJobItemPending {
		t.Fatalf("expected current worker item state to remain pending, got %+v", loadedItem)
	}
	loadedStaged, err := service.staging.get(staged.ID, true)
	if err != nil {
		t.Fatalf("load current staging lease: %v", err)
	}
	if loadedStaged.ProcessingOwner != stagingOwnerB || loadedStaged.ProcessingAttempt != claimedStagedB.ProcessingAttempt || loadedStaged.Status != stagedUploadStatusProcessing {
		t.Fatalf("expected current staging lease to remain intact, got %+v", loadedStaged)
	}
}

func TestMCPJobServiceUsesPersistedHistoryAndOperationsAfterRestart(t *testing.T) {
	root := t.TempDir()
	config := durableMCPTestConfig(root)
	jobStorePath := filepath.Join(root, "mcp-jobs.db")
	owner := AuthPrincipal{AuthType: "session", UserID: "user-persisted"}
	store, err := NewMCPJobStore(jobStorePath)
	if err != nil {
		t.Fatalf("create job store: %v", err)
	}
	now := time.Now().UTC()

	// More than the in-memory recovery window proves list pagination reads from
	// SQLite after restart instead of exposing only restored map entries.
	for index := 0; index < 55; index++ {
		job := testMCPJobRecord(fmt.Sprintf("job-history-%02d", index), now.Add(time.Duration(index)*time.Second))
		job.Job.Status = "succeeded"
		job.Job.OwnerUserID = owner.UserID
		job.Job.UpdatedAt = now.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano)
		if err := store.Create(job); err != nil {
			t.Fatalf("create history job %d: %v", index, err)
		}
	}

	service := NewAppServiceWithJobStore(nil, NewAppStateStore(config.StateFile), nil, config, store)
	defer func() {
		if err := service.ShutdownJobs(context.Background()); err != nil {
			t.Errorf("shutdown persisted service: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Errorf("close persisted store: %v", err)
		}
	}()

	page, err := service.ListMCPJobsPageAs(100, "", owner)
	if err != nil {
		t.Fatalf("list persisted history after restart: %v", err)
	}
	if len(page.Items) != 55 {
		t.Fatalf("expected all persisted history items, got %d", len(page.Items))
	}

	cancelled := testMCPJobRecord("job-cancel-after-restart", now.Add(2*time.Hour))
	cancelled.Job.Status = "queued"
	cancelled.Job.OwnerUserID = owner.UserID
	if err := store.Create(cancelled); err != nil {
		t.Fatalf("create restart cancellation job: %v", err)
	}
	cancelledJob, err := service.CancelMCPJobAs(cancelled.Job.ID, owner)
	if err != nil {
		t.Fatalf("cancel persisted job after restart: %v", err)
	}
	if cancelledJob.Status != "cancelled" || cancelledJob.Resumable {
		t.Fatalf("expected persisted job cancellation to be terminal: %+v", cancelledJob)
	}
	storedCancelled, found, err := store.Get(cancelled.Job.ID)
	if err != nil || !found {
		t.Fatalf("read cancelled persisted job: found=%t err=%v", found, err)
	}
	if storedCancelled.Job.Status != "cancelled" || storedCancelled.Job.Resumable {
		t.Fatalf("expected cancelled state in SQLite: %+v", storedCancelled.Job)
	}

	kbID, err := service.ResolveKnowledgeBaseID("")
	if err != nil {
		t.Fatalf("resolve persisted retry knowledge base: %v", err)
	}
	staged, err := service.StageInlineUploadAs("restart-retry.txt", []byte("\n"), "test", owner)
	if err != nil {
		t.Fatalf("stage persisted retry fixture: %v", err)
	}
	failed := testMCPJobRecord("job-retry-after-restart", now.Add(3*time.Hour))
	failed.Job.Status = "failed"
	failed.Job.OwnerUserID = owner.UserID
	failed.Job.Retryable = true
	failed.Job.Resumable = true
	failed.Descriptor = mcpJobDescriptor{
		Version:         mcpJobDescriptorVersion,
		Type:            "import",
		KnowledgeBaseID: kbID,
		FileName:        staged.FileName,
		UploadID:        staged.ID,
		Checksum:        staged.SHA256,
	}
	if err := store.Create(failed); err != nil {
		t.Fatalf("create restart retry job: %v", err)
	}
	retried, err := service.RetryMCPJobAs(failed.Job.ID, owner)
	if err != nil {
		t.Fatalf("retry persisted job after restart: %v", err)
	}
	if retried.ParentJobID != failed.Job.ID || retried.RetryCount != 1 {
		t.Fatalf("expected persisted retry chain metadata: %+v", retried)
	}
	completed := waitForMCPJobStatus(t, service, retried.ID, owner, "succeeded")
	if completed.ParentJobID != failed.Job.ID || completed.RetryCount != 1 {
		t.Fatalf("expected retry child to retain metadata after completion: %+v", completed)
	}
	updatedSource, found, err := store.Get(failed.Job.ID)
	if err != nil || !found {
		t.Fatalf("read retry source after restart: found=%t err=%v", found, err)
	}
	if updatedSource.Job.Retryable {
		t.Fatal("expected persisted retry source to reject a second retry")
	}
}
