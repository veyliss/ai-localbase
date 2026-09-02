package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

func TestMCPJobTerminalStatusIsNotOverwritten(t *testing.T) {
	service := &AppService{
		mcpJobs: map[string]model.MCPJob{
			"job_cancelled": {
				ID:       "job_cancelled",
				Type:     "import",
				Status:   "cancelled",
				Progress: 10,
				Summary:  "任务已取消。",
			},
		},
	}

	service.updateMCPJob("job_cancelled", func(job *model.MCPJob) {
		job.Status = "running"
		job.Progress = 70
		job.Summary = "正在注册并索引文档。"
	})
	service.completeMCPJob("job_cancelled", "succeeded", 100, "导入完成。", map[string]any{"ok": true}, "")

	job := service.mcpJobs["job_cancelled"]
	if job.Status != "cancelled" {
		t.Fatalf("expected cancelled job to keep terminal status, got %+v", job)
	}
	if job.Progress != 10 || job.Result != nil {
		t.Fatalf("expected cancelled job details to stay unchanged, got %+v", job)
	}
}

func TestCompleteMCPJobPersistsTruncatedResultWithoutChangingOutcome(t *testing.T) {
	store := newTestMCPJobStore(t)
	now := time.Now().UTC()
	record := testMCPJobRecord("job-result-fallback", now)
	if err := store.Create(record); err != nil {
		t.Fatalf("create job: %v", err)
	}
	claimed, ok, err := store.Claim(record.Job.ID, "worker-result", time.Minute, now)
	if err != nil || !ok {
		t.Fatalf("claim job: ok=%t err=%v", ok, err)
	}

	service := &AppService{
		mcpJobs: map[string]model.MCPJob{record.Job.ID: claimed.Job},
		mcpJobDescriptors: map[string]mcpJobDescriptor{
			record.Job.ID: claimed.Descriptor,
		},
		mcpJobLeases: map[string]mcpJobLease{
			record.Job.ID: {Owner: claimed.LeaseOwner, Attempt: claimed.Job.Attempt, ExpiresAt: claimed.LeaseExpiresAt},
		},
		mcpJobCancels:       map[string]context.CancelFunc{},
		mcpJobStagingLeases: map[string]map[string]mcpStagingLease{},
		mcpJobStore:         store,
	}
	service.completeMCPJob(record.Job.ID, "succeeded", 100, "任务完成。", map[string]any{
		"results": oversizedMCPJobResult()["results"],
	}, "")

	loaded, found, err := store.Get(record.Job.ID)
	if err != nil {
		t.Fatalf("load completed job: %v", err)
	}
	if !found {
		t.Fatal("expected completed job to remain persisted")
	}
	if loaded.Job.Status != "succeeded" || loaded.Job.ErrorCode != "" {
		t.Fatalf("expected successful outcome to remain successful, got %+v", loaded.Job)
	}
	if loaded.Job.Result["truncated"] != true {
		t.Fatalf("expected oversized result to be persisted with a truncation marker, got %#v", loaded.Job.Result)
	}
	if service.mcpJobs[record.Job.ID].Result["truncated"] != true {
		t.Fatalf("expected in-memory result to use the bounded representation, got %#v", service.mcpJobs[record.Job.ID].Result)
	}
}

func TestMCPJobRenewsTrackedStagingLease(t *testing.T) {
	service := NewAppService(nil, nil, nil, model.ServerConfig{StagingDir: t.TempDir()})
	staged, err := service.staging.StageBytes("heartbeat.md", []byte("heartbeat content"), "test")
	if err != nil {
		t.Fatalf("stage upload: %v", err)
	}
	claimed, err := service.staging.ClaimWithLeaseAs(staged.ID, AuthPrincipal{}, "job-token", mcpJobLeaseDuration)
	if err != nil {
		t.Fatalf("claim upload: %v", err)
	}
	before, err := time.Parse(time.RFC3339Nano, claimed.ProcessingLeaseUntil)
	if err != nil {
		t.Fatalf("parse initial lease: %v", err)
	}
	service.mcpJobs["job-heartbeat"] = model.MCPJob{ID: "job-heartbeat", Status: "running"}
	service.trackMCPStagingLease("job-heartbeat", claimed.ID, "job-token", claimed.ProcessingAttempt)

	if !service.renewMCPJobStagingLeases("job-heartbeat") {
		t.Fatal("expected tracked staging lease renewal to succeed")
	}
	renewed, err := service.staging.get(claimed.ID, true)
	if err != nil {
		t.Fatalf("get renewed upload: %v", err)
	}
	after, err := time.Parse(time.RFC3339Nano, renewed.ProcessingLeaseUntil)
	if err != nil {
		t.Fatalf("parse renewed lease: %v", err)
	}
	if !after.After(before) {
		t.Fatalf("expected renewed lease to move forward: before=%s after=%s", before, after)
	}
	if after.Before(time.Now().UTC()) {
		t.Fatalf("expected renewed lease to remain active: %s", after)
	}
	service.forgetMCPStagingLease("job-heartbeat", claimed.ID, "job-token", claimed.ProcessingAttempt)
	if !service.renewMCPJobStagingLeases("job-heartbeat") {
		t.Fatal("expected a lease forgotten after completion to be ignored")
	}

	consumed, err := service.staging.StageBytes("consumed.md", []byte("consumed content"), "test")
	if err != nil {
		t.Fatalf("stage consumed upload: %v", err)
	}
	consumedClaim, err := service.staging.ClaimWithLeaseAs(consumed.ID, AuthPrincipal{}, "job-token", mcpJobLeaseDuration)
	if err != nil {
		t.Fatalf("claim consumed upload: %v", err)
	}
	if err := service.staging.MarkConsumedWithLeaseAttempt(consumed.ID, "job-token", consumedClaim.ProcessingAttempt); err != nil {
		t.Fatalf("consume upload: %v", err)
	}
	service.trackMCPStagingLease("job-heartbeat", consumed.ID, "job-token", consumedClaim.ProcessingAttempt)
	if !service.renewMCPJobStagingLeases("job-heartbeat") {
		t.Fatal("expected an already consumed lease to be ignored")
	}
}

func TestCancelMCPJobAddsBestEffortWarning(t *testing.T) {
	service := &AppService{
		mcpJobs: map[string]model.MCPJob{
			"job_running": {
				ID:       "job_running",
				Type:     "import",
				Status:   "running",
				Progress: 70,
				Summary:  "正在注册并索引文档。",
			},
		},
	}

	job, err := service.CancelMCPJob("job_running")
	if err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	if job.Status != "cancelled" {
		t.Fatalf("expected cancelled job, got %+v", job)
	}
	if len(job.Warnings) != 1 || !strings.Contains(job.Warnings[0], "best-effort") {
		t.Fatalf("expected best-effort warning, got %+v", job.Warnings)
	}
}

func TestShutdownJobsCancelsAndWaitsForWorkers(t *testing.T) {
	service := &AppService{
		mcpJobs:       map[string]model.MCPJob{},
		mcpJobCancels: map[string]context.CancelFunc{},
	}
	job := model.MCPJob{ID: "job_shutdown", Status: "running"}
	jobContext, cancelJob := context.WithCancel(context.Background())
	if !service.registerMCPJob(job, cancelJob) {
		t.Fatal("expected job registration to succeed")
	}
	workerDone := make(chan struct{})
	go func() {
		defer service.mcpJobWG.Done()
		defer close(workerDone)
		<-jobContext.Done()
	}()

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	if err := service.ShutdownJobs(shutdownContext); err != nil {
		t.Fatalf("shutdown jobs: %v", err)
	}
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("expected worker to exit during shutdown")
	}

	if service.registerMCPJob(model.MCPJob{ID: "job_after_shutdown"}, func() {}) {
		t.Fatal("expected new jobs to be rejected after shutdown")
	}
}

func TestPruneMCPJobsPreservesActiveJobs(t *testing.T) {
	service := &AppService{
		mcpJobs:       map[string]model.MCPJob{},
		mcpJobCancels: map[string]context.CancelFunc{},
	}
	for index := 0; index < 50; index++ {
		id := "job_terminal_" + string(rune('a'+index))
		service.mcpJobs[id] = model.MCPJob{ID: id, Status: "succeeded", UpdatedAt: "2026-01-01T00:00:00Z"}
	}
	service.mcpJobs["job_active"] = model.MCPJob{ID: "job_active", Status: "running"}
	service.mcpJobCancels["job_active"] = func() {}

	service.mcpJobMu.Lock()
	service.pruneMCPJobsLocked()
	service.mcpJobMu.Unlock()

	if _, ok := service.mcpJobs["job_active"]; !ok {
		t.Fatal("expected active job to remain tracked")
	}
	if _, ok := service.mcpJobCancels["job_active"]; !ok {
		t.Fatal("expected active job cancellation to remain tracked")
	}
}

func TestMCPDangerHashIgnoresConfirmationNonce(t *testing.T) {
	withoutNonce, err := hashMCPDangerArguments(map[string]any{"id": "conversation-1"})
	if err != nil {
		t.Fatalf("hash arguments without nonce: %v", err)
	}
	withNonce, err := hashMCPDangerArguments(map[string]any{
		"id":           "conversation-1",
		"confirmNonce": "mcp_confirm_secret",
	})
	if err != nil {
		t.Fatalf("hash arguments with nonce: %v", err)
	}
	if withoutNonce != withNonce {
		t.Fatalf("expected transport nonce to be excluded from parameter hash: %q != %q", withoutNonce, withNonce)
	}
}

func TestMCPJobOwnerIsolation(t *testing.T) {
	service := &AppService{
		mcpJobs: map[string]model.MCPJob{
			"job-api-key": {
				ID:            "job-api-key",
				Status:        "running",
				OwnerUserID:   "root-user",
				OwnerAPIKeyID: "key-a",
			},
			"job-session": {
				ID:          "job-session",
				Status:      "succeeded",
				OwnerUserID: "root-user",
			},
		},
	}

	if _, err := service.GetMCPJobStatusAs("job-api-key", AuthPrincipal{AuthType: "api_key", UserID: "root-user", APIKeyID: "key-b"}); err == nil {
		t.Fatal("expected another API key to be denied")
	}
	if _, err := service.GetMCPJobStatusAs("job-api-key", AuthPrincipal{AuthType: "api_key", UserID: "root-user", APIKeyID: "key-a"}); err != nil {
		t.Fatalf("expected owning API key to be allowed: %v", err)
	}
	if jobs := service.ListRecentMCPJobsAs(20, AuthPrincipal{AuthType: "session", UserID: "root-user"}); len(jobs) != 1 || jobs[0].ID != "job-session" {
		t.Fatalf("expected session owner to see only session jobs, got %+v", jobs)
	}
}

func TestRetryMCPJobCreatesChildWithRetryMetadata(t *testing.T) {
	called := false
	service := &AppService{
		mcpJobs: map[string]model.MCPJob{
			"job-failed": {
				ID:         "job-failed",
				Status:     "failed",
				Retryable:  true,
				RetryCount: 0,
			},
		},
		mcpJobRetries: map[string]mcpJobRetryAction{
			"job-failed": func() (model.MCPJob, error) {
				called = true
				return model.MCPJob{ID: "job-retry", Status: "queued", Retryable: true}, nil
			},
		},
		mcpJobCancels: map[string]context.CancelFunc{},
	}

	job, err := service.RetryMCPJobAs("job-failed", AuthPrincipal{})
	if err != nil {
		t.Fatalf("retry failed job: %v", err)
	}
	if !called || job.ID != "job-retry" || job.ParentJobID != "job-failed" || job.RetryCount != 1 {
		t.Fatalf("expected retry metadata, called=%t job=%+v", called, job)
	}
	if service.mcpJobs["job-failed"].Retryable {
		t.Fatal("expected the source job to be non-retryable after creating a child")
	}
	calledBeforeDuplicate := called
	if _, err := service.RetryMCPJobAs("job-failed", AuthPrincipal{}); err == nil {
		t.Fatal("expected a source job to reject duplicate retry requests")
	}
	if called != calledBeforeDuplicate {
		t.Fatal("expected duplicate retry request not to invoke the retry action")
	}
}

func TestAdminRetryKeepsChildVisibleToOriginalOwner(t *testing.T) {
	service := &AppService{
		mcpJobs: map[string]model.MCPJob{
			"job-owned-by-user": {
				ID:          "job-owned-by-user",
				Status:      "failed",
				Retryable:   true,
				OwnerUserID: "original-user",
			},
		},
		mcpJobRetries: map[string]mcpJobRetryAction{
			"job-owned-by-user": func() (model.MCPJob, error) {
				return model.MCPJob{
					ID:          "job-owned-by-user-retry",
					Status:      "queued",
					Retryable:   true,
					OwnerUserID: "admin-user",
				}, nil
			},
		},
		mcpJobCancels: map[string]context.CancelFunc{},
	}

	admin := AuthPrincipal{AuthType: "session", UserID: "admin-user", Scopes: []string{"mcp:admin"}}
	child, err := service.RetryMCPJobAs("job-owned-by-user", admin)
	if err != nil {
		t.Fatalf("admin retry: %v", err)
	}
	if child.OwnerUserID != "original-user" || child.OwnerAPIKeyID != "" {
		t.Fatalf("expected retry child to retain original owner, got %+v", child)
	}
}

func TestRetryMCPJobRejectsNonFailedAndExhaustedJobs(t *testing.T) {
	service := &AppService{
		mcpJobs: map[string]model.MCPJob{
			"job-running":   {ID: "job-running", Status: "running", Retryable: true},
			"job-exhausted": {ID: "job-exhausted", Status: "failed", Retryable: false, RetryCount: mcpJobMaxRetries},
		},
		mcpJobRetries: map[string]mcpJobRetryAction{
			"job-running":   func() (model.MCPJob, error) { return model.MCPJob{}, nil },
			"job-exhausted": func() (model.MCPJob, error) { return model.MCPJob{}, nil },
		},
	}

	for _, jobID := range []string{"job-running", "job-exhausted"} {
		if _, err := service.RetryMCPJobAs(jobID, AuthPrincipal{}); err == nil {
			t.Fatalf("expected retry %s to be rejected", jobID)
		}
	}
}
