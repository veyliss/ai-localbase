package service

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"ai-localbase/internal/util"
)

const (
	mcpJobItemPending   = "pending"
	mcpJobItemRunning   = "running"
	mcpJobItemSucceeded = "succeeded"
	mcpJobItemFailed    = "failed"
	mcpJobItemCancelled = "cancelled"
)

// mcpJobItem is the durable checkpoint for one file in a batch job. Keeping it
// separate from the parent result lets recovery skip completed files before
// the parent job has written its final aggregate response.
type mcpJobItem struct {
	JobID      string
	UploadID   string
	FileName   string
	Checksum   string
	Size       int64
	Status     string
	DocumentID string
	Error      string
	ErrorCode  string
	Retryable  bool
	UpdatedAt  string
}

func (s *MCPJobStore) CreateItems(jobID string, items []mcpJobItem) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("mcp job store is nil")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return fmt.Errorf("mcp job id is required")
	}
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin mcp job item transaction: %w", err)
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	for _, item := range items {
		item.JobID = jobID
		item.UploadID = strings.TrimSpace(item.UploadID)
		if item.UploadID == "" {
			rollback()
			return fmt.Errorf("mcp job item upload id is required")
		}
		if item.Size < 0 {
			rollback()
			return fmt.Errorf("mcp job item size cannot be negative")
		}
		item.Status = normalizeMCPJobItemStatus(item.Status)
		if item.UpdatedAt == "" {
			item.UpdatedAt = s.nowUTC().Format(time.RFC3339Nano)
		}
		if _, err := tx.Exec(`INSERT INTO mcp_job_items (
					job_id, upload_id, file_name, checksum, status, document_id, error,
					error_code, retryable, size, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.JobID, item.UploadID, normalizeMCPJobItemFileName(item.FileName), strings.ToLower(strings.TrimSpace(item.Checksum)), item.Status,
			strings.TrimSpace(item.DocumentID), redactMCPJobMessage(item.Error), strings.TrimSpace(item.ErrorCode), boolToInt(item.Retryable), item.Size, item.UpdatedAt,
		); err != nil {
			rollback()
			return fmt.Errorf("create mcp job item %s: %w", item.UploadID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mcp job items: %w", err)
	}
	return nil
}

// EnsureItems creates missing batch checkpoints idempotently. It is used after
// recovery because the parent job and its child rows are intentionally durable
// in separate transactions.
func (s *MCPJobStore) EnsureItems(jobID string, items []mcpJobItem) error {
	_, err := s.ensureItems(jobID, items, "", 0)
	return err
}

func (s *MCPJobStore) EnsureItemsForLease(jobID string, items []mcpJobItem, expectedLeaseOwner string, expectedAttempt int) (bool, error) {
	return s.ensureItems(jobID, items, expectedLeaseOwner, expectedAttempt)
}

func (s *MCPJobStore) ensureItems(jobID string, items []mcpJobItem, expectedLeaseOwner string, expectedAttempt int) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("mcp job store is nil")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return false, fmt.Errorf("mcp job id is required")
	}
	if len(items) == 0 {
		return true, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin ensure mcp job items transaction: %w", err)
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	if owner := strings.TrimSpace(expectedLeaseOwner); owner != "" {
		var exists int
		if err := tx.QueryRow(`SELECT EXISTS(
			SELECT 1 FROM mcp_jobs
			WHERE id = ? AND lease_owner = ? AND attempt = ? AND status IN ('queued', 'running')
			AND lease_expires_at > ?
		)`, jobID, owner, expectedAttempt, s.nowUTC().Format(time.RFC3339Nano)).Scan(&exists); err != nil {
			rollback()
			return false, fmt.Errorf("check mcp job lease for items: %w", err)
		}
		if exists == 0 {
			rollback()
			return false, nil
		}
	}
	for _, item := range items {
		item.JobID = jobID
		item.UploadID = strings.TrimSpace(item.UploadID)
		if item.UploadID == "" {
			rollback()
			return false, fmt.Errorf("mcp job item upload id is required")
		}
		if item.Size < 0 {
			rollback()
			return false, fmt.Errorf("mcp job item size cannot be negative")
		}
		item.Status = normalizeMCPJobItemStatus(item.Status)
		if item.UpdatedAt == "" {
			item.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if _, err := tx.Exec(`INSERT INTO mcp_job_items (
				job_id, upload_id, file_name, checksum, status, document_id, error,
				error_code, retryable, size, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(job_id, upload_id) DO NOTHING`,
			item.JobID, item.UploadID, normalizeMCPJobItemFileName(item.FileName), strings.ToLower(strings.TrimSpace(item.Checksum)), item.Status,
			strings.TrimSpace(item.DocumentID), redactMCPJobMessage(item.Error), strings.TrimSpace(item.ErrorCode), boolToInt(item.Retryable), item.Size, item.UpdatedAt,
		); err != nil {
			rollback()
			return false, fmt.Errorf("ensure mcp job item %s: %w", item.UploadID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit ensured mcp job items: %w", err)
	}
	return true, nil
}

func (s *MCPJobStore) ListItems(jobID string) ([]mcpJobItem, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("mcp job store is nil")
	}
	rows, err := s.db.Query(`SELECT job_id, upload_id, file_name, checksum, status,
			document_id, error, error_code, retryable, size, updated_at
			FROM mcp_job_items WHERE job_id = ? ORDER BY updated_at ASC, upload_id ASC`, strings.TrimSpace(jobID))
	if err != nil {
		return nil, fmt.Errorf("list mcp job items: %w", err)
	}
	defer rows.Close()
	items := make([]mcpJobItem, 0)
	for rows.Next() {
		item, err := scanMCPJobItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mcp job item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mcp job items: %w", err)
	}
	return items, nil
}

func (s *MCPJobStore) GetItem(jobID, uploadID string) (mcpJobItem, bool, error) {
	if s == nil || s.db == nil {
		return mcpJobItem{}, false, fmt.Errorf("mcp job store is nil")
	}
	row := s.db.QueryRow(`SELECT job_id, upload_id, file_name, checksum, status,
		document_id, error, error_code, retryable, size, updated_at
		FROM mcp_job_items WHERE job_id = ? AND upload_id = ?`, strings.TrimSpace(jobID), strings.TrimSpace(uploadID))
	item, err := scanMCPJobItem(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return mcpJobItem{}, false, nil
		}
		return mcpJobItem{}, false, fmt.Errorf("get mcp job item: %w", err)
	}
	return item, true, nil
}

// UpdateItem is guarded by the parent job lease when a durable worker calls it.
// That prevents a stale worker from writing a successful checkpoint after a
// different process has already recovered the job.
func (s *MCPJobStore) UpdateItem(item mcpJobItem, expectedLeaseOwner string, expectedAttempt int) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("mcp job store is nil")
	}
	item.JobID = strings.TrimSpace(item.JobID)
	item.UploadID = strings.TrimSpace(item.UploadID)
	if item.JobID == "" || item.UploadID == "" {
		return false, fmt.Errorf("mcp job item identifiers are required")
	}
	if item.Size < 0 {
		return false, fmt.Errorf("mcp job item size cannot be negative")
	}
	item.Status = normalizeMCPJobItemStatus(item.Status)
	if item.UpdatedAt == "" {
		item.UpdatedAt = s.nowUTC().Format(time.RFC3339Nano)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin update mcp job item transaction: %w", err)
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	if owner := strings.TrimSpace(expectedLeaseOwner); owner != "" {
		var exists int
		if err := tx.QueryRow(`SELECT EXISTS(
			SELECT 1 FROM mcp_jobs
			WHERE id = ? AND lease_owner = ? AND attempt = ? AND status IN ('queued', 'running')
			AND lease_expires_at > ?
		)`, item.JobID, owner, expectedAttempt, s.nowUTC().Format(time.RFC3339Nano)).Scan(&exists); err != nil {
			rollback()
			return false, fmt.Errorf("check mcp job lease for item: %w", err)
		}
		if exists == 0 {
			rollback()
			return false, nil
		}
	}
	if _, err := tx.Exec(`INSERT INTO mcp_job_items (
		job_id, upload_id, file_name, checksum, status, document_id, error,
		error_code, retryable, size, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(job_id, upload_id) DO UPDATE SET
		file_name = excluded.file_name,
		checksum = excluded.checksum,
		size = excluded.size,
		status = excluded.status,
		document_id = excluded.document_id,
		error = excluded.error,
		error_code = excluded.error_code,
		retryable = excluded.retryable,
		updated_at = excluded.updated_at`,
		item.JobID, item.UploadID, normalizeMCPJobItemFileName(item.FileName), strings.ToLower(strings.TrimSpace(item.Checksum)), item.Status,
		strings.TrimSpace(item.DocumentID), redactMCPJobMessage(item.Error), strings.TrimSpace(item.ErrorCode), boolToInt(item.Retryable), item.Size, item.UpdatedAt,
	); err != nil {
		rollback()
		return false, fmt.Errorf("update mcp job item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit mcp job item update: %w", err)
	}
	return true, nil
}

func (s *MCPJobStore) DeleteItemsForJob(jobID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("mcp job store is nil")
	}
	if _, err := s.db.Exec(`DELETE FROM mcp_job_items WHERE job_id = ?`, strings.TrimSpace(jobID)); err != nil {
		return fmt.Errorf("delete mcp job items: %w", err)
	}
	return nil
}

func scanMCPJobItem(row mcpJobRowScanner) (mcpJobItem, error) {
	var item mcpJobItem
	var retryable int
	if err := row.Scan(
		&item.JobID, &item.UploadID, &item.FileName, &item.Checksum, &item.Status,
		&item.DocumentID, &item.Error, &item.ErrorCode, &retryable, &item.Size, &item.UpdatedAt,
	); err != nil {
		return mcpJobItem{}, err
	}
	item.JobID = strings.TrimSpace(item.JobID)
	item.UploadID = strings.TrimSpace(item.UploadID)
	item.FileName = normalizeMCPJobItemFileName(item.FileName)
	item.Checksum = strings.ToLower(strings.TrimSpace(item.Checksum))
	item.Status = normalizeMCPJobItemStatus(item.Status)
	item.DocumentID = strings.TrimSpace(item.DocumentID)
	item.Error = redactMCPJobMessage(item.Error)
	item.ErrorCode = strings.TrimSpace(item.ErrorCode)
	item.Retryable = retryable != 0
	return item, nil
}

func normalizeMCPJobItemFileName(value string) string {
	if normalized, err := util.NormalizeFilename(value); err == nil {
		return normalized
	}
	if fallback := util.SanitizeFilename(value); fallback != "" {
		return fallback
	}
	return "upload"
}

func normalizeMCPJobItemStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case mcpJobItemRunning:
		return mcpJobItemRunning
	case mcpJobItemSucceeded:
		return mcpJobItemSucceeded
	case mcpJobItemFailed:
		return mcpJobItemFailed
	case mcpJobItemCancelled:
		return mcpJobItemCancelled
	default:
		return mcpJobItemPending
	}
}
