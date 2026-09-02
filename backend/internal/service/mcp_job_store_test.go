package service

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

func newTestMCPJobStore(t *testing.T) *MCPJobStore {
	t.Helper()
	store, err := NewMCPJobStore(t.TempDir() + "/mcp-jobs.db")
	if err != nil {
		t.Fatalf("create mcp job store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close mcp job store: %v", err)
		}
	})
	return store
}

func resetMCPJobForCheckpointReplay(store *MCPJobStore, record mcpJobStoreRecord) error {
	if store == nil || store.db == nil {
		return fmt.Errorf("mcp job store is nil")
	}
	_, err := store.db.Exec(`UPDATE mcp_jobs SET
		status = ?, progress = ?, retryable = ?, resumable = ?,
		completed_at = ?, updated_at = ?, lease_owner = ?, lease_expires_at = ?
		WHERE id = ?`,
		record.Job.Status, record.Job.Progress, boolToInt(record.Job.Retryable), boolToInt(record.Job.Resumable),
		record.Job.CompletedAt, record.Job.UpdatedAt, record.LeaseOwner, record.LeaseExpiresAt, record.Job.ID,
	)
	if err != nil {
		return fmt.Errorf("reset mcp job for checkpoint replay: %w", err)
	}
	return nil
}

func testMCPJobRecord(id string, now time.Time) mcpJobStoreRecord {
	stamp := now.UTC().Format(time.RFC3339)
	return mcpJobStoreRecord{
		Job: model.MCPJob{
			ID:            id,
			Type:          "import",
			Status:        "queued",
			Progress:      0,
			Summary:       "等待执行",
			Retryable:     true,
			Resumable:     true,
			CreatedAt:     stamp,
			UpdatedAt:     stamp,
			OwnerUserID:   "user-a",
			OwnerAPIKeyID: "",
		},
		Descriptor: mcpJobDescriptor{
			Version:         mcpJobDescriptorVersion,
			Type:            "import",
			KnowledgeBaseID: "kb-1",
			FileName:        "guide.md",
			UploadID:        "upl-1",
		},
	}
}

func TestMCPJobStorePersistsAndSanitizesJobRecord(t *testing.T) {
	store := newTestMCPJobStore(t)
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	record := testMCPJobRecord("job-persist", now)
	record.Job.Error = "request failed at /Users/private/secret.txt: https://example.test/token"
	record.Job.Warnings = []string{"cookie=secret"}
	record.Job.Result = map[string]any{
		"content": "不应落盘的原文",
		"path":    "/app/data/uploads/private.md",
		"summary": "可保留的摘要",
		"nested":  map[string]any{"authorization": "Bearer secret", "ok": true},
	}

	if err := store.Create(record); err != nil {
		t.Fatalf("create job: %v", err)
	}
	loaded, found, err := store.Get(record.Job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if !found {
		t.Fatal("expected persisted job")
	}
	if loaded.Job.Result["content"] != nil || loaded.Job.Result["path"] != nil {
		t.Fatalf("expected sensitive result fields to be removed, got %#v", loaded.Job.Result)
	}
	if loaded.Job.Result["summary"] != "可保留的摘要" {
		t.Fatalf("expected safe result field to remain, got %#v", loaded.Job.Result)
	}
	if loaded.Job.Error == record.Job.Error || loaded.Job.Error == "" {
		t.Fatalf("expected error message to be redacted, got %q", loaded.Job.Error)
	}
	if loaded.Descriptor.UploadID != "upl-1" || loaded.Descriptor.FileName != "guide.md" {
		t.Fatalf("expected durable descriptor, got %+v", loaded.Descriptor)
	}
}

func TestMCPJobStorePersistsBatchInputMetadataAndProtectsFile(t *testing.T) {
	store := newTestMCPJobStore(t)
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("stat mcp job store: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected mcp job store mode 0600, got %04o", got)
	}

	record := testMCPJobRecord("job-batch-input-metadata", time.Now().UTC())
	record.Job.Type = "batch-index"
	record.Descriptor = mcpJobDescriptor{
		Version:     mcpJobDescriptorVersion,
		Type:        "batch-index",
		UploadIDs:   []string{"upload-sized"},
		Concurrency: 1,
		InputBytes:  42,
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("create batch input record: %v", err)
	}
	if err := store.CreateItems(record.Job.ID, []mcpJobItem{{
		UploadID:  "upload-sized",
		FileName:  "sized.txt",
		Size:      42,
		Status:    mcpJobItemPending,
		Retryable: true,
	}}); err != nil {
		t.Fatalf("create sized batch item: %v", err)
	}
	loaded, found, err := store.Get(record.Job.ID)
	if err != nil || !found {
		t.Fatalf("load batch input record: found=%t err=%v", found, err)
	}
	if loaded.Descriptor.InputBytes != 42 {
		t.Fatalf("expected persisted input bytes 42, got %d", loaded.Descriptor.InputBytes)
	}
	items, err := store.ListItems(record.Job.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("load batch input item: len=%d err=%v", len(items), err)
	}
	if items[0].Size != 42 {
		t.Fatalf("expected persisted item size 42, got %d", items[0].Size)
	}
}

func TestMCPJobStoreProtectsDatabaseAndSQLiteSidecarsAfterWrite(t *testing.T) {
	root := t.TempDir()
	path := root + "/mcp-jobs.db"
	store, err := NewMCPJobStore(path)
	if err != nil {
		t.Fatalf("create mcp job store: %v", err)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("relax mcp job store mode: %v", err)
	}
	if err := store.Create(testMCPJobRecord("job-protected-write", time.Now().UTC())); err != nil {
		t.Fatalf("create job after relaxing mode: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat protected mcp job store: %v", err)
	}
	if got := info.Mode().Perm(); got != mcpJobStorePrivateMode {
		t.Fatalf("expected write to restore database mode %04o, got %04o", mcpJobStorePrivateMode, got)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close mcp job store before sidecar check: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecarPath := path + suffix
		if err := os.WriteFile(sidecarPath, []byte("test sidecar"), 0o644); err != nil {
			t.Fatalf("create sqlite sidecar %s: %v", suffix, err)
		}
	}
	if err := store.protectFilesAfterWrite("sidecar regression test"); err != nil {
		t.Fatalf("protect sqlite sidecars: %v", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatalf("stat protected sqlite file %s: %v", suffix, err)
		}
		if got := info.Mode().Perm(); got != mcpJobStorePrivateMode {
			t.Fatalf("expected sqlite file %s mode %04o, got %04o", suffix, mcpJobStorePrivateMode, got)
		}
	}
}

func TestMCPBatchInputSizeLimit(t *testing.T) {
	if err := ValidateMCPBatchInputBytes(mcpBatchMaxInputBytes); err != nil {
		t.Fatalf("expected maximum batch input size to be accepted: %v", err)
	}
	if err := ValidateMCPBatchInputBytes(mcpBatchMaxInputBytes + 1); err == nil {
		t.Fatal("expected batch input over the limit to be rejected")
	}
	if _, err := mcpBatchInputBytes([]mcpJobItem{
		{UploadID: "first", Size: mcpBatchMaxInputBytes - 1},
		{UploadID: "second", Size: 2},
	}); err == nil {
		t.Fatal("expected aggregate batch input size to be rejected")
	}
}

func TestRedactMCPJobMessageRemovesCommonFilesystemPaths(t *testing.T) {
	paths := []string{
		"/home/alice/uploads/private.csv",
		"/opt/ai-localbase/uploads/private.csv",
		"/workspace/project/uploads/private.csv",
		"/private/var/folders/private.csv",
		`C:\\Users\\alice\\uploads\\private.csv`,
		`\\\\server\\share\\private.csv`,
		"../uploads/private.csv",
		`..\\uploads\\private.csv`,
		"./uploads/private.csv",
	}
	for _, filePath := range paths {
		t.Run(filePath, func(t *testing.T) {
			message := "open " + filePath + ": permission denied"
			redacted := redactMCPJobMessage(message)
			if strings.Contains(redacted, filePath) {
				t.Fatalf("expected path to be redacted: %q", redacted)
			}
			if !strings.Contains(redacted, "[redacted path]") {
				t.Fatalf("expected redacted path marker, got %q", redacted)
			}
		})
	}
}

func TestMCPJobStoreTruncatesOversizedResultWithoutChangingJobState(t *testing.T) {
	store := newTestMCPJobStore(t)
	record := testMCPJobRecord("job-result-too-large", time.Now().UTC())
	record.Job.Result = oversizedMCPJobResult()

	if err := store.Create(record); err != nil {
		t.Fatalf("expected oversized result to be persisted as a summary, got %v", err)
	}
	loaded, found, err := store.Get(record.Job.ID)
	if err != nil || !found {
		t.Fatalf("load truncated result: found=%t err=%v", found, err)
	}
	if loaded.Job.Status != record.Job.Status || loaded.Job.Retryable != record.Job.Retryable {
		t.Fatalf("expected job state to remain unchanged, got %+v", loaded.Job)
	}
	if loaded.Job.Result["truncated"] != true {
		t.Fatalf("expected truncation marker, got %#v", loaded.Job.Result)
	}
	statistics, ok := loaded.Job.Result["statistics"].(map[string]any)
	if !ok || statistics["originalBytes"] == nil || statistics["maxBytes"] == nil {
		t.Fatalf("expected truncation statistics, got %#v", loaded.Job.Result)
	}
}

func TestMCPJobStoreRejectsInvalidBatchConcurrency(t *testing.T) {
	store := newTestMCPJobStore(t)
	for _, value := range []int{-1, mcpBatchMaxConcurrency + 1} {
		record := testMCPJobRecord(fmt.Sprintf("job-invalid-concurrency-%d", value), time.Now().UTC())
		record.Job.Type = "batch-index"
		record.Descriptor = mcpJobDescriptor{
			Version:     mcpJobDescriptorVersion,
			Type:        "batch-index",
			UploadIDs:   []string{"upload-1"},
			Concurrency: value,
		}
		if err := store.Create(record); err == nil || !strings.Contains(err.Error(), "invalid persisted batch concurrency") {
			t.Fatalf("expected invalid concurrency %d to be rejected, got %v", value, err)
		}
	}

	defaultRecord := testMCPJobRecord("job-default-concurrency", time.Now().UTC())
	defaultRecord.Job.Type = "batch-index"
	defaultRecord.Descriptor = mcpJobDescriptor{Version: mcpJobDescriptorVersion, Type: "batch-index", UploadIDs: []string{"upload-1"}}
	if err := store.Create(defaultRecord); err != nil {
		t.Fatalf("create default concurrency record: %v", err)
	}
	loaded, found, err := store.Get(defaultRecord.Job.ID)
	if err != nil || !found {
		t.Fatalf("load default concurrency record: found=%t err=%v", found, err)
	}
	if loaded.Descriptor.Concurrency != mcpBatchDefaultConcurrency {
		t.Fatalf("expected zero concurrency to use default %d, got %d", mcpBatchDefaultConcurrency, loaded.Descriptor.Concurrency)
	}
}

func oversizedMCPJobResult() map[string]any {
	items := make([]string, 0, 200)
	for index := 0; index < 200; index++ {
		items = append(items, fmt.Sprintf("%03d-%s", index, strings.Repeat("x", 1024)))
	}
	return map[string]any{"results": items}
}

func TestMCPJobStoreRejectsUnserializableResult(t *testing.T) {
	store := newTestMCPJobStore(t)
	record := testMCPJobRecord("job-result-invalid", time.Now().UTC())
	record.Job.Result = map[string]any{"invalid": make(chan struct{})}

	if err := store.Create(record); err == nil || !strings.Contains(err.Error(), "sanitize mcp job result") {
		t.Fatalf("expected unserializable result to be rejected explicitly, got %v", err)
	}
}

func TestMCPJobStoreClaimHonorsLeaseAndRejectsStaleUpdate(t *testing.T) {
	store := newTestMCPJobStore(t)
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	currentNow := now
	store.now = func() time.Time { return currentNow }
	record := testMCPJobRecord("job-lease", now)
	if err := store.Create(record); err != nil {
		t.Fatalf("create job: %v", err)
	}

	claimed, ok, err := store.Claim(record.Job.ID, "worker-a", time.Minute, now)
	if err != nil || !ok {
		t.Fatalf("claim job with worker-a: ok=%t err=%v", ok, err)
	}
	if claimed.Job.Attempt != 1 || claimed.LeaseOwner != "worker-a" || claimed.Job.RecoveryState != "" {
		t.Fatalf("unexpected initial claim: %+v", claimed)
	}

	if _, ok, err := store.Claim(record.Job.ID, "worker-b", time.Minute, now.Add(10*time.Second)); err != nil || ok {
		t.Fatalf("expected active lease to block worker-b: ok=%t err=%v", ok, err)
	}

	recovered, ok, err := store.Claim(record.Job.ID, "worker-b", time.Minute, now.Add(2*time.Minute))
	if err != nil || !ok {
		t.Fatalf("claim expired job with worker-b: ok=%t err=%v", ok, err)
	}
	if recovered.Job.Attempt != 2 || recovered.LeaseOwner != "worker-b" {
		t.Fatalf("unexpected recovery claim: %+v", recovered)
	}
	if recovered.Job.RecoveryState != "" {
		t.Fatalf("normal claim should not be marked as restart recovery: %+v", recovered.Job)
	}
	currentNow = now.Add(2 * time.Minute)

	stale := claimed
	stale.Job.Summary = "旧 Worker 的写入"
	updated, err := store.Update(stale, "worker-a", claimed.Job.Attempt)
	if err != nil {
		t.Fatalf("stale update: %v", err)
	}
	if updated {
		t.Fatal("expected stale worker update to be rejected")
	}

	recovered.Job.Summary = "新 Worker 的写入"
	updated, err = store.Update(recovered, "worker-b", recovered.Job.Attempt)
	if err != nil || !updated {
		t.Fatalf("current worker update: updated=%t err=%v", updated, err)
	}
}

func TestMCPJobStoreRecoverableClaimAllowsOnlyOneWorker(t *testing.T) {
	store := newTestMCPJobStore(t)
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	record := testMCPJobRecord("job-recoverable", now)
	if err := store.Create(record); err != nil {
		t.Fatalf("create job: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, workerID := range []string{"worker-a", "worker-b"} {
		workerID := workerID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			jobs, err := store.ClaimRecoverable(workerID, time.Minute, now)
			if err != nil {
				t.Errorf("recover jobs with %s: %v", workerID, err)
				return
			}
			results <- len(jobs)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	claimedCount := 0
	for count := range results {
		claimedCount += count
	}
	if claimedCount != 1 {
		t.Fatalf("expected exactly one recovery claim, got %d", claimedCount)
	}
	loaded, found, err := store.Get(record.Job.ID)
	if err != nil || !found {
		t.Fatalf("get recovered job: found=%t err=%v", found, err)
	}
	if loaded.Job.Attempt != 1 || loaded.Job.RecoveryState != "recovering" {
		t.Fatalf("expected recovery metadata, got %+v", loaded.Job)
	}
}

func TestMCPJobStoreRecoverySlotsEnforceGlobalLimit(t *testing.T) {
	root := t.TempDir()
	path := root + "/mcp-jobs.db"
	storeA, err := NewMCPJobStore(path)
	if err != nil {
		t.Fatalf("create first mcp job store: %v", err)
	}
	defer func() { _ = storeA.Close() }()
	storeB, err := NewMCPJobStore(path)
	if err != nil {
		t.Fatalf("open second mcp job store: %v", err)
	}
	defer func() { _ = storeB.Close() }()

	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	for index := 0; index < mcpJobRecoveryConcurrency+2; index++ {
		record := testMCPJobRecord(fmt.Sprintf("job-recovery-slot-%d", index), now.Add(time.Duration(index)*time.Second))
		if err := storeA.Create(record); err != nil {
			t.Fatalf("create recoverable job %d: %v", index, err)
		}
	}

	first, err := storeA.ClaimRecoverableLimited("worker-a", time.Minute, now, 100)
	if err != nil {
		t.Fatalf("claim first recovery batch: %v", err)
	}
	if len(first) != mcpJobRecoveryConcurrency {
		t.Fatalf("expected first worker to claim %d jobs, got %d", mcpJobRecoveryConcurrency, len(first))
	}
	second, err := storeB.ClaimRecoverableLimited("worker-b", time.Minute, now, 100)
	if err != nil {
		t.Fatalf("claim second recovery batch while full: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("expected global recovery slot limit to block second worker, got %d jobs", len(second))
	}

	if err := storeA.ReleaseRecoverySlot(first[0].Job.ID, "worker-a", first[0].Job.Attempt); err != nil {
		t.Fatalf("release recovery slot: %v", err)
	}
	second, err = storeB.ClaimRecoverableLimited("worker-b", time.Minute, now, 100)
	if err != nil {
		t.Fatalf("claim recovery slot after release: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("expected one slot to become available, got %d jobs", len(second))
	}

	if renewed, err := storeA.RenewLease(first[1].Job.ID, "worker-a", first[1].Job.Attempt, time.Minute, now.Add(10*time.Second)); err != nil || !renewed {
		t.Fatalf("renew recovered job lease: renewed=%t err=%v", renewed, err)
	}
	var slotLease string
	if err := storeA.db.QueryRow(`SELECT lease_expires_at FROM mcp_job_recovery_slots WHERE job_id = ?`, first[1].Job.ID).Scan(&slotLease); err != nil {
		t.Fatalf("read renewed recovery slot: %v", err)
	}
	if slotLease != now.Add(70*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("expected recovery slot lease to follow job heartbeat, got %q", slotLease)
	}
}

func TestMCPJobStoreLinkRetryIsAtomic(t *testing.T) {
	store := newTestMCPJobStore(t)
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	parent := testMCPJobRecord("job-retry-parent", now)
	parent.Job.Status = "failed"
	parent.Job.RetryCount = 0
	parent.Job.Retryable = true
	child := testMCPJobRecord("job-retry-child", now.Add(time.Second))
	child.Job.Status = "queued"
	child.Job.RetryCount = 0
	if err := store.Create(parent); err != nil {
		t.Fatalf("create retry parent: %v", err)
	}
	if err := store.Create(child); err != nil {
		t.Fatalf("create retry child: %v", err)
	}

	linkedParent, linkedChild, err := store.LinkRetry(parent.Job.ID, child.Job.ID, 1, parent.Job.OwnerUserID, parent.Job.OwnerAPIKeyID)
	if err != nil {
		t.Fatalf("link retry jobs: %v", err)
	}
	if linkedParent.Job.Retryable {
		t.Fatal("expected retry parent to be consumed")
	}
	if linkedChild.Job.ParentJobID != parent.Job.ID || linkedChild.Job.RetryCount != 1 || linkedChild.Job.OwnerUserID != parent.Job.OwnerUserID {
		t.Fatalf("expected child relation and owner to be linked atomically, got %+v", linkedChild.Job)
	}

	rollbackParent := testMCPJobRecord("job-retry-rollback-parent", now.Add(2*time.Second))
	rollbackParent.Job.Status = "failed"
	rollbackParent.Job.Retryable = true
	if err := store.Create(rollbackParent); err != nil {
		t.Fatalf("create rollback parent: %v", err)
	}
	if _, _, err := store.LinkRetry(rollbackParent.Job.ID, "missing-child", 1, "user-a", ""); err == nil {
		t.Fatal("expected missing child to roll back retry parent update")
	}
	loaded, found, err := store.Get(rollbackParent.Job.ID)
	if err != nil || !found {
		t.Fatalf("load rollback parent: found=%t err=%v", found, err)
	}
	if !loaded.Job.Retryable {
		t.Fatal("expected failed retry relation to leave parent retryable")
	}
}

func TestMCPJobStorePruneKeepsNewestTerminalJobsAndActiveJobs(t *testing.T) {
	store := newTestMCPJobStore(t)
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		record := testMCPJobRecord("job-terminal-"+string(rune('a'+index)), now.Add(time.Duration(index)*time.Minute))
		record.Job.Status = "succeeded"
		record.Job.UpdatedAt = now.Add(time.Duration(index) * time.Minute).Format(time.RFC3339)
		if err := store.Create(record); err != nil {
			t.Fatalf("create terminal job %d: %v", index, err)
		}
	}
	active := testMCPJobRecord("job-active", now.Add(3*time.Minute))
	active.Job.Status = "running"
	if err := store.Create(active); err != nil {
		t.Fatalf("create active job: %v", err)
	}

	if err := store.Prune(2); err != nil {
		t.Fatalf("prune jobs: %v", err)
	}
	items, err := store.List(20)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Job.ID] = true
	}
	if !seen["job-active"] || !seen["job-terminal-b"] || !seen["job-terminal-c"] {
		t.Fatalf("expected active and newest terminal jobs, got %+v", seen)
	}
	if seen["job-terminal-a"] {
		t.Fatal("expected oldest terminal job to be pruned")
	}
}

func TestMCPJobStorePrunePreservesRetryAncestors(t *testing.T) {
	store := newTestMCPJobStore(t)
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)

	grandparent := testMCPJobRecord("job-grandparent", now)
	grandparent.Job.Status = "failed"
	grandparent.Job.UpdatedAt = now.Format(time.RFC3339)
	if err := store.Create(grandparent); err != nil {
		t.Fatalf("create grandparent job: %v", err)
	}

	parent := testMCPJobRecord("job-parent", now.Add(time.Minute))
	parent.Job.Status = "failed"
	parent.Job.ParentJobID = grandparent.Job.ID
	parent.Job.UpdatedAt = now.Add(time.Minute).Format(time.RFC3339)
	if err := store.Create(parent); err != nil {
		t.Fatalf("create parent job: %v", err)
	}

	child := testMCPJobRecord("job-child", now.Add(2*time.Minute))
	child.Job.Status = "succeeded"
	child.Job.ParentJobID = parent.Job.ID
	child.Job.UpdatedAt = now.Add(2 * time.Minute).Format(time.RFC3339)
	if err := store.Create(child); err != nil {
		t.Fatalf("create child job: %v", err)
	}

	newest := testMCPJobRecord("job-newest", now.Add(3*time.Minute))
	newest.Job.Status = "succeeded"
	newest.Job.UpdatedAt = now.Add(3 * time.Minute).Format(time.RFC3339)
	if err := store.Create(newest); err != nil {
		t.Fatalf("create newest job: %v", err)
	}

	oldUnrelated := testMCPJobRecord("job-old-unrelated", now.Add(-time.Minute))
	oldUnrelated.Job.Status = "succeeded"
	oldUnrelated.Job.UpdatedAt = now.Add(-time.Minute).Format(time.RFC3339)
	if err := store.Create(oldUnrelated); err != nil {
		t.Fatalf("create unrelated job: %v", err)
	}

	if deleted, err := store.PruneWithCount(2); err != nil {
		t.Fatalf("prune retry chain: %v", err)
	} else if deleted != 1 {
		t.Fatalf("expected only unrelated terminal job to be pruned, deleted=%d", deleted)
	}

	for _, id := range []string{grandparent.Job.ID, parent.Job.ID, child.Job.ID, newest.Job.ID} {
		if _, found, err := store.Get(id); err != nil || !found {
			t.Fatalf("expected retry chain job %s to remain: found=%t err=%v", id, found, err)
		}
	}
	if _, found, err := store.Get(oldUnrelated.Job.ID); err != nil {
		t.Fatalf("read unrelated job after prune: %v", err)
	} else if found {
		t.Fatal("expected unrelated old terminal job to be pruned")
	}
}

func TestMCPJobStoreStatsReportsExpiredLeasesAndWritableState(t *testing.T) {
	store := newTestMCPJobStore(t)
	now := time.Now().UTC()

	expired := testMCPJobRecord("job-expired-lease", now.Add(-time.Minute))
	expired.Job.Status = "running"
	expired.LeaseOwner = "worker-expired"
	expired.LeaseExpiresAt = now.Add(-time.Second).Format(time.RFC3339Nano)
	if err := store.Create(expired); err != nil {
		t.Fatalf("create expired job: %v", err)
	}

	active := testMCPJobRecord("job-active-lease", now)
	active.Job.Status = "running"
	active.LeaseOwner = "worker-active"
	active.LeaseExpiresAt = now.Add(time.Minute).Format(time.RFC3339Nano)
	if err := store.Create(active); err != nil {
		t.Fatalf("create active job: %v", err)
	}

	terminal := testMCPJobRecord("job-terminal-stats", now.Add(time.Minute))
	terminal.Job.Status = "succeeded"
	if err := store.Create(terminal); err != nil {
		t.Fatalf("create terminal job: %v", err)
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("read job store stats: %v", err)
	}
	if !stats.Writable {
		t.Fatal("expected job store write probe to succeed")
	}
	if stats.ActiveJobs != 2 || stats.LeasedJobs != 2 || stats.ExpiredLeaseJobs != 1 || stats.TerminalJobs != 1 {
		t.Fatalf("unexpected job store stats: %+v", stats)
	}
}

func TestMCPJobStoreStatsRejectsClosedStore(t *testing.T) {
	store, err := NewMCPJobStore(t.TempDir() + "/mcp-jobs.db")
	if err != nil {
		t.Fatalf("create mcp job store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close mcp job store: %v", err)
	}
	if _, err := store.Stats(); err == nil {
		t.Fatal("expected closed job store stats to fail")
	}
}
