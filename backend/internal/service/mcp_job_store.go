package service

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"ai-localbase/internal/model"

	_ "modernc.org/sqlite"
)

const (
	mcpJobStoreSchemaVersion = 1
	mcpJobStoreDefaultKeep   = 50
	mcpJobLeaseDuration      = 30 * time.Second
	mcpJobHeartbeatInterval  = 10 * time.Second
	mcpJobDescriptorVersion  = 1
)

var (
	mcpJobStorePathPattern       = regexp.MustCompile(`(?i)(?:/Users/|/app/|/var/|/tmp/|[A-Za-z]:[\\/])[^\s"'<>]+`)
	mcpJobStoreURLPattern        = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
	mcpJobStoreSecretPattern     = regexp.MustCompile(`(?i)(?:ailb_sk_|mcp_confirm_)[A-Za-z0-9_-]+`)
	mcpJobStoreCredentialPattern = regexp.MustCompile(`(?i)((?:authorization|cookie|token|api[_-]?key|password|secret))\s*[:=]\s*(?:bearer\s+)?[^\s,;]+`)
)

// mcpJobDescriptor is the durable, non-sensitive description needed to rebuild
// a worker after a process restart. It deliberately contains references to
// staged files rather than their contents.
type mcpJobDescriptor struct {
	Version         int      `json:"version"`
	Type            string   `json:"type"`
	KnowledgeBaseID string   `json:"knowledgeBaseId,omitempty"`
	DocumentID      string   `json:"documentId,omitempty"`
	FileName        string   `json:"fileName,omitempty"`
	UploadID        string   `json:"uploadId,omitempty"`
	Checksum        string   `json:"checksum,omitempty"`
	UploadIDs       []string `json:"uploadIds,omitempty"`
	Concurrency     int      `json:"concurrency,omitempty"`
	MaxPerDocument  int      `json:"maxPerDocument,omitempty"`
}

type mcpJobStoreRecord struct {
	Job            model.MCPJob
	Descriptor     mcpJobDescriptor
	LeaseOwner     string
	LeaseExpiresAt string
}

type MCPJobStore struct {
	db   *sql.DB
	path string
}

func NewMCPJobStore(path string) (*MCPJobStore, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return nil, fmt.Errorf("mcp job store path is required")
	}
	if err := os.MkdirAll(filepath.Dir(trimmedPath), 0o755); err != nil {
		return nil, fmt.Errorf("create mcp job store directory: %w", err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(trimmedPath))
	if err != nil {
		return nil, fmt.Errorf("open mcp job store: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &MCPJobStore{db: db, path: trimmedPath}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *MCPJobStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *MCPJobStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *MCPJobStore) init() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("mcp job store is nil")
	}

	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read mcp job store schema version: %w", err)
	}
	if version > mcpJobStoreSchemaVersion {
		return fmt.Errorf("unsupported mcp job store schema version %d", version)
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS mcp_jobs (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			progress INTEGER NOT NULL DEFAULT 0,
			summary TEXT NOT NULL DEFAULT '',
			result_json TEXT NOT NULL DEFAULT '{}',
			error TEXT NOT NULL DEFAULT '',
			error_code TEXT NOT NULL DEFAULT '',
			warnings_json TEXT NOT NULL DEFAULT '[]',
			retryable INTEGER NOT NULL DEFAULT 0,
			retry_count INTEGER NOT NULL DEFAULT 0,
			parent_job_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			completed_at TEXT NOT NULL DEFAULT '',
			owner_user_id TEXT NOT NULL DEFAULT '',
			owner_api_key_id TEXT NOT NULL DEFAULT '',
			descriptor_json TEXT NOT NULL DEFAULT '{}',
			resumable INTEGER NOT NULL DEFAULT 1,
			recovery_state TEXT NOT NULL DEFAULT '',
			attempt INTEGER NOT NULL DEFAULT 0,
			lease_owner TEXT NOT NULL DEFAULT '',
			lease_expires_at TEXT NOT NULL DEFAULT '',
			last_heartbeat_at TEXT NOT NULL DEFAULT '',
			next_action TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_jobs_updated_at ON mcp_jobs(updated_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_jobs_recovery ON mcp_jobs(status, lease_expires_at);`,
		`CREATE TABLE IF NOT EXISTS mcp_job_items (
			job_id TEXT NOT NULL,
			upload_id TEXT NOT NULL,
			file_name TEXT NOT NULL DEFAULT '',
			checksum TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			document_id TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			error_code TEXT NOT NULL DEFAULT '',
			retryable INTEGER NOT NULL DEFAULT 1,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (job_id, upload_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_job_items_job ON mcp_job_items(job_id, updated_at);`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("initialize mcp job store schema: %w", err)
		}
	}
	if err := s.ensureColumns(); err != nil {
		return err
	}
	if version < mcpJobStoreSchemaVersion {
		if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, mcpJobStoreSchemaVersion)); err != nil {
			return fmt.Errorf("write mcp job store schema version: %w", err)
		}
	}
	return nil
}

func (s *MCPJobStore) ensureColumns() error {
	rows, err := s.db.Query(`PRAGMA table_info(mcp_jobs)`)
	if err != nil {
		return fmt.Errorf("inspect mcp job store schema: %w", err)
	}
	found := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan mcp job store schema: %w", err)
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate mcp job store schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close mcp job store schema rows: %w", err)
	}

	legacyColumns := []struct {
		name       string
		definition string
	}{
		{"error_code", "TEXT NOT NULL DEFAULT ''"},
		{"descriptor_json", "TEXT NOT NULL DEFAULT '{}'"},
		{"resumable", "INTEGER NOT NULL DEFAULT 1"},
		{"recovery_state", "TEXT NOT NULL DEFAULT ''"},
		{"attempt", "INTEGER NOT NULL DEFAULT 0"},
		{"lease_owner", "TEXT NOT NULL DEFAULT ''"},
		{"lease_expires_at", "TEXT NOT NULL DEFAULT ''"},
		{"last_heartbeat_at", "TEXT NOT NULL DEFAULT ''"},
		{"next_action", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, column := range legacyColumns {
		if found[column.name] {
			continue
		}
		if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE mcp_jobs ADD COLUMN %s %s`, column.name, column.definition)); err != nil {
			return fmt.Errorf("add mcp job store column %s: %w", column.name, err)
		}
	}
	return nil
}

func (s *MCPJobStore) Create(record mcpJobStoreRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("mcp job store is nil")
	}
	if strings.TrimSpace(record.Job.ID) == "" {
		return fmt.Errorf("mcp job id is required")
	}
	record.Descriptor = normalizeMCPJobDescriptor(record.Descriptor, record.Job.Type)
	_, err := s.db.Exec(`INSERT INTO mcp_jobs (
		id, type, status, progress, summary, result_json, error, error_code,
		warnings_json, retryable, retry_count, parent_job_id, created_at, updated_at,
		completed_at, owner_user_id, owner_api_key_id, descriptor_json, resumable,
		recovery_state, attempt, lease_owner, lease_expires_at, last_heartbeat_at, next_action
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mcpJobStoreValues(record)...,
	)
	if err != nil {
		return fmt.Errorf("create mcp job: %w", err)
	}
	return nil
}

// Update persists a state transition only while the caller owns the current
// lease. A false result means another worker has already taken over.
func (s *MCPJobStore) Update(record mcpJobStoreRecord, expectedLeaseOwner string, expectedAttempt int) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("mcp job store is nil")
	}
	if strings.TrimSpace(record.Job.ID) == "" {
		return false, fmt.Errorf("mcp job id is required")
	}
	record.Descriptor = normalizeMCPJobDescriptor(record.Descriptor, record.Job.Type)
	values := mcpJobStoreValues(record)
	values = append(values[1:], record.Job.ID, strings.TrimSpace(expectedLeaseOwner), expectedAttempt)
	result, err := s.db.Exec(`UPDATE mcp_jobs SET
		type = ?, status = ?, progress = ?, summary = ?, result_json = ?, error = ?, error_code = ?,
		warnings_json = ?, retryable = ?, retry_count = ?, parent_job_id = ?, created_at = ?, updated_at = ?,
		completed_at = ?, owner_user_id = ?, owner_api_key_id = ?, descriptor_json = ?, resumable = ?,
		recovery_state = ?, attempt = ?, lease_owner = ?, lease_expires_at = ?, last_heartbeat_at = ?, next_action = ?
		WHERE id = ? AND lease_owner = ? AND attempt = ?`, values...)
	if err != nil {
		return false, fmt.Errorf("update mcp job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect mcp job update: %w", err)
	}
	return affected == 1, nil
}

func (s *MCPJobStore) Claim(jobID, workerID string, leaseDuration time.Duration, now time.Time) (mcpJobStoreRecord, bool, error) {
	if s == nil || s.db == nil {
		return mcpJobStoreRecord{}, false, fmt.Errorf("mcp job store is nil")
	}
	return s.claimWhere(
		`id = ? AND resumable = 1 AND (
			(status = 'queued' AND (lease_owner = '' OR lease_expires_at = '' OR lease_expires_at <= ?))
			OR (status = 'running' AND (lease_owner = '' OR lease_expires_at = '' OR lease_expires_at <= ?))
		)`,
		[]any{strings.TrimSpace(jobID), now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano)},
		workerID,
		leaseDuration,
		now,
		false,
	)
}

func (s *MCPJobStore) ClaimRecoverable(workerID string, leaseDuration time.Duration, now time.Time) ([]mcpJobStoreRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("mcp job store is nil")
	}
	nowValue := now.UTC().Format(time.RFC3339Nano)
	rows, err := s.db.Query(`SELECT id FROM mcp_jobs
		WHERE resumable = 1 AND (
			(status = 'queued' AND (lease_owner = '' OR lease_expires_at = '' OR lease_expires_at <= ?))
			OR (status = 'running' AND (lease_owner = '' OR lease_expires_at = '' OR lease_expires_at <= ?))
		)
		ORDER BY updated_at ASC`, nowValue, nowValue)
	if err != nil {
		return nil, fmt.Errorf("list recoverable mcp jobs: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan recoverable mcp job: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate recoverable mcp jobs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close recoverable mcp jobs: %w", err)
	}

	recovered := make([]mcpJobStoreRecord, 0, len(ids))
	for _, id := range ids {
		record, claimed, err := s.claimWhere(
			`id = ? AND resumable = 1 AND (
				(status = 'queued' AND (lease_owner = '' OR lease_expires_at = '' OR lease_expires_at <= ?))
				OR (status = 'running' AND (lease_owner = '' OR lease_expires_at = '' OR lease_expires_at <= ?))
			)`,
			[]any{id, nowValue, nowValue},
			workerID,
			leaseDuration,
			now,
			true,
		)
		if err != nil {
			return nil, err
		}
		if claimed {
			recovered = append(recovered, record)
		}
	}
	return recovered, nil
}

func (s *MCPJobStore) Delete(jobID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("mcp job store is nil")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete mcp job: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM mcp_job_items WHERE job_id = ?`, strings.TrimSpace(jobID)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete mcp job items: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM mcp_jobs WHERE id = ?`, strings.TrimSpace(jobID)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete mcp job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete mcp job: %w", err)
	}
	return nil
}

func (s *MCPJobStore) RenewLease(jobID, workerID string, attempt int, leaseDuration time.Duration, now time.Time) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("mcp job store is nil")
	}
	if leaseDuration <= 0 {
		leaseDuration = mcpJobLeaseDuration
	}
	result, err := s.db.Exec(`UPDATE mcp_jobs SET
		lease_expires_at = ?, last_heartbeat_at = ?, updated_at = ?
		WHERE id = ? AND lease_owner = ? AND attempt = ? AND status IN ('queued', 'running')`,
		now.Add(leaseDuration).UTC().Format(time.RFC3339Nano),
		now.UTC().Format(time.RFC3339Nano),
		now.UTC().Format(time.RFC3339Nano),
		strings.TrimSpace(jobID), strings.TrimSpace(workerID), attempt,
	)
	if err != nil {
		return false, fmt.Errorf("renew mcp job lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect mcp job lease renewal: %w", err)
	}
	return affected == 1, nil
}

func (s *MCPJobStore) List(limit int) ([]mcpJobStoreRecord, error) {
	return s.ListForPrincipal(limit, AuthPrincipal{}, "")
}

// ListForPrincipal applies ownership filtering in SQLite before pagination.
// This prevents a large admin history from being loaded into the process and
// avoids relying on the in-memory recovery window for authorization.
func (s *MCPJobStore) ListForPrincipal(limit int, owner AuthPrincipal, cursor string) ([]mcpJobStoreRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("mcp job store is nil")
	}
	if limit <= 0 || limit > 500 {
		limit = mcpJobStoreDefaultKeep
	}
	where, args := mcpJobOwnerFilter(owner)
	if strings.TrimSpace(cursor) != "" {
		parts := strings.SplitN(cursor, "\x00", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("invalid mcp job cursor")
		}
		where += ` AND (updated_at < ? OR (updated_at = ? AND id < ?))`
		args = append(args, parts[0], parts[0], parts[1])
	}
	args = append(args, limit)
	rows, err := s.db.Query(mcpJobSelectSQL+` WHERE `+where+` ORDER BY updated_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list mcp jobs: %w", err)
	}
	defer rows.Close()
	return scanMCPJobRows(rows)
}

func mcpJobOwnerFilter(owner AuthPrincipal) (string, []any) {
	if strings.TrimSpace(owner.AuthType) == "" || hasScope(owner.Scopes, "mcp:admin") {
		return "1 = 1", nil
	}
	if owner.AuthType == "api_key" {
		apiKeyID := strings.TrimSpace(owner.APIKeyID)
		if apiKeyID == "" {
			return "1 = 0", nil
		}
		return "owner_api_key_id = ?", []any{apiKeyID}
	}
	userID := strings.TrimSpace(owner.UserID)
	if userID == "" {
		return "1 = 0", nil
	}
	return "owner_api_key_id = '' AND owner_user_id = ?", []any{userID}
}

func (s *MCPJobStore) Get(jobID string) (mcpJobStoreRecord, bool, error) {
	if s == nil || s.db == nil {
		return mcpJobStoreRecord{}, false, fmt.Errorf("mcp job store is nil")
	}
	row := s.db.QueryRow(mcpJobSelectSQL+` WHERE id = ?`, strings.TrimSpace(jobID))
	record, err := scanMCPJobRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return mcpJobStoreRecord{}, false, nil
		}
		return mcpJobStoreRecord{}, false, fmt.Errorf("get mcp job: %w", err)
	}
	return record, true, nil
}

func (s *MCPJobStore) Prune(keep int) error {
	_, err := s.PruneWithCount(keep)
	return err
}

func (s *MCPJobStore) PruneWithCount(keep int) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("mcp job store is nil")
	}
	if keep <= 0 {
		keep = mcpJobStoreDefaultKeep
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin prune mcp jobs: %w", err)
	}
	// Keep the newest terminal jobs, but always preserve ancestors that are
	// still referenced by a retry chain. The recursive CTE also protects older
	// grandparents when a chain contains more than one retry.
	pruneQuery := `WITH RECURSIVE protected_parents(id) AS (
		SELECT DISTINCT parent_job_id FROM mcp_jobs WHERE TRIM(parent_job_id) <> ''
		UNION
		SELECT parent.parent_job_id
		FROM mcp_jobs AS parent
		JOIN protected_parents AS child ON parent.id = child.id
		WHERE TRIM(parent.parent_job_id) <> ''
	)
	SELECT id FROM mcp_jobs
		WHERE status IN ('succeeded', 'failed', 'cancelled')
		AND id NOT IN (SELECT id FROM protected_parents)
		ORDER BY updated_at DESC, id DESC
		LIMIT -1 OFFSET ?`
	if _, err := tx.Exec(`DELETE FROM mcp_job_items WHERE job_id IN (`+pruneQuery+`)`, keep); err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("prune mcp job items: %w", err)
	}
	result, err := tx.Exec(`DELETE FROM mcp_jobs WHERE id IN (`+pruneQuery+`)`, keep)
	if err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("prune mcp jobs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit prune mcp jobs: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("inspect pruned mcp jobs: %w", err)
	}
	return int(deleted), nil
}

// CancelItems marks unfinished children terminal before the parent lease is
// cleared. This keeps an explicit user cancellation from being mistaken for
// a crash recovery on the next process start.
func (s *MCPJobStore) CancelItems(jobID, expectedLeaseOwner string, expectedAttempt int) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("mcp job store is nil")
	}
	query := `UPDATE mcp_job_items SET status = 'cancelled', error = ?, error_code = 'cancelled', retryable = 0, updated_at = ?
		WHERE job_id = ? AND status IN ('pending', 'running')`
	args := []any{"任务已取消。", time.Now().UTC().Format(time.RFC3339Nano), strings.TrimSpace(jobID)}
	if owner := strings.TrimSpace(expectedLeaseOwner); owner != "" {
		query += ` AND EXISTS (
			SELECT 1 FROM mcp_jobs WHERE mcp_jobs.id = mcp_job_items.job_id
			AND mcp_jobs.lease_owner = ? AND mcp_jobs.attempt = ?
			AND mcp_jobs.status IN ('queued', 'running')
		)`
		args = append(args, owner, expectedAttempt)
	}
	if _, err := s.db.Exec(query, args...); err != nil {
		return fmt.Errorf("cancel mcp job items: %w", err)
	}
	return nil
}

type mcpJobStoreStats struct {
	Writable         bool
	ActiveJobs       int
	RecoveringJobs   int
	LeasedJobs       int
	ExpiredLeaseJobs int
	TerminalJobs     int
}

func (s *MCPJobStore) Stats() (mcpJobStoreStats, error) {
	if s == nil || s.db == nil {
		return mcpJobStoreStats{}, fmt.Errorf("mcp job store is nil")
	}
	var stats mcpJobStoreStats
	nowValue := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN status IN ('queued', 'running') THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN recovery_state = 'recovering' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN lease_owner <> '' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status IN ('queued', 'running')
			AND lease_expires_at <> '' AND lease_expires_at <= ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status IN ('succeeded', 'failed', 'cancelled') THEN 1 ELSE 0 END), 0)
		FROM mcp_jobs`, nowValue).Scan(
		&stats.ActiveJobs,
		&stats.RecoveringJobs,
		&stats.LeasedJobs,
		&stats.ExpiredLeaseJobs,
		&stats.TerminalJobs,
	); err != nil {
		return stats, fmt.Errorf("read mcp job store stats: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return stats, fmt.Errorf("begin mcp job store write probe: %w", err)
	}
	// A no-op UPDATE can succeed on some read-only SQLite connections because
	// no row is touched. Creating and rolling back a probe table forces SQLite
	// to acquire a write transaction without leaving data behind.
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS mcp_job_store_write_probe (id INTEGER NOT NULL)`)
	rollbackErr := tx.Rollback()
	if err != nil {
		return stats, fmt.Errorf("mcp job store is not writable: %w", err)
	}
	if rollbackErr != nil && rollbackErr != sql.ErrTxDone {
		return stats, fmt.Errorf("rollback mcp job store write probe: %w", rollbackErr)
	}
	stats.Writable = true
	return stats, nil
}

func (s *MCPJobStore) claimWhere(where string, args []any, workerID string, leaseDuration time.Duration, now time.Time, recovering bool) (mcpJobStoreRecord, bool, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return mcpJobStoreRecord{}, false, fmt.Errorf("mcp worker id is required")
	}
	if leaseDuration <= 0 {
		leaseDuration = mcpJobLeaseDuration
	}
	now = now.UTC()
	recoveryState := ""
	nextAction := "worker scheduled"
	if recovering {
		recoveryState = "recovering"
		nextAction = "worker scheduled after restart"
	}
	setValues := []any{
		recoveryState,
		workerID,
		now.Add(leaseDuration).Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		nextAction,
	}
	setValues = append(setValues, args...)
	result, err := s.db.Exec(`UPDATE mcp_jobs SET
		status = 'queued', recovery_state = ?, attempt = attempt + 1,
		lease_owner = ?, lease_expires_at = ?, last_heartbeat_at = ?,
		updated_at = ?, next_action = ?
		WHERE `+where,
		setValues...,
	)
	if err != nil {
		return mcpJobStoreRecord{}, false, fmt.Errorf("claim mcp job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return mcpJobStoreRecord{}, false, fmt.Errorf("inspect mcp job claim: %w", err)
	}
	if affected != 1 {
		return mcpJobStoreRecord{}, false, nil
	}
	jobID := ""
	for _, arg := range args {
		if value, ok := arg.(string); ok {
			jobID = value
			break
		}
	}
	if jobID == "" {
		return mcpJobStoreRecord{}, false, fmt.Errorf("claimed mcp job id is missing")
	}
	record, found, err := s.Get(jobID)
	if err != nil {
		return mcpJobStoreRecord{}, false, err
	}
	return record, found, nil
}

const mcpJobSelectSQL = `SELECT id, type, status, progress, summary, result_json, error, error_code,
	warnings_json, retryable, retry_count, parent_job_id, created_at, updated_at, completed_at,
	owner_user_id, owner_api_key_id, descriptor_json, resumable, recovery_state, attempt,
	lease_owner, lease_expires_at, last_heartbeat_at, next_action FROM mcp_jobs`

type mcpJobRowScanner interface {
	Scan(dest ...any) error
}

func scanMCPJobRows(rows *sql.Rows) ([]mcpJobStoreRecord, error) {
	items := make([]mcpJobStoreRecord, 0)
	for rows.Next() {
		record, err := scanMCPJobRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mcp job: %w", err)
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mcp jobs: %w", err)
	}
	return items, nil
}

func scanMCPJobRow(row mcpJobRowScanner) (mcpJobStoreRecord, error) {
	var (
		job                         model.MCPJob
		resultJSON, errorMessage    string
		errorCode, warningsJSON     string
		parentJobID, completedAt    string
		ownerUserID, ownerAPIKeyID  string
		descriptorJSON              string
		retryable, resumable        int
		recoveryState, leaseOwner   string
		attempt                     int
		leaseExpiresAt              string
		lastHeartbeatAt, nextAction string
	)
	if err := row.Scan(
		&job.ID, &job.Type, &job.Status, &job.Progress, &job.Summary, &resultJSON, &errorMessage, &errorCode,
		&warningsJSON, &retryable, &job.RetryCount, &parentJobID, &job.CreatedAt, &job.UpdatedAt, &completedAt,
		&ownerUserID, &ownerAPIKeyID, &descriptorJSON, &resumable, &recoveryState, &attempt,
		&leaseOwner, &leaseExpiresAt, &lastHeartbeatAt, &nextAction,
	); err != nil {
		return mcpJobStoreRecord{}, err
	}
	job.Error = errorMessage
	job.ErrorCode = errorCode
	job.Retryable = retryable != 0
	job.ParentJobID = strings.TrimSpace(parentJobID)
	job.CompletedAt = strings.TrimSpace(completedAt)
	job.OwnerUserID = strings.TrimSpace(ownerUserID)
	job.OwnerAPIKeyID = strings.TrimSpace(ownerAPIKeyID)
	job.Resumable = resumable != 0
	job.RecoveryState = strings.TrimSpace(recoveryState)
	job.Attempt = attempt
	job.LastHeartbeatAt = strings.TrimSpace(lastHeartbeatAt)
	job.NextAction = strings.TrimSpace(nextAction)
	if strings.TrimSpace(resultJSON) != "" && resultJSON != "{}" {
		if err := json.Unmarshal([]byte(resultJSON), &job.Result); err != nil {
			return mcpJobStoreRecord{}, fmt.Errorf("decode mcp job result: %w", err)
		}
	}
	if strings.TrimSpace(warningsJSON) != "" && warningsJSON != "[]" {
		if err := json.Unmarshal([]byte(warningsJSON), &job.Warnings); err != nil {
			return mcpJobStoreRecord{}, fmt.Errorf("decode mcp job warnings: %w", err)
		}
	}
	var descriptor mcpJobDescriptor
	if strings.TrimSpace(descriptorJSON) != "" && descriptorJSON != "{}" {
		if err := json.Unmarshal([]byte(descriptorJSON), &descriptor); err != nil {
			return mcpJobStoreRecord{}, fmt.Errorf("decode mcp job descriptor: %w", err)
		}
	}
	return mcpJobStoreRecord{
		Job:            job,
		Descriptor:     normalizeMCPJobDescriptor(descriptor, job.Type),
		LeaseOwner:     strings.TrimSpace(leaseOwner),
		LeaseExpiresAt: strings.TrimSpace(leaseExpiresAt),
	}, nil
}

func mcpJobStoreValues(record mcpJobStoreRecord) []any {
	job := prepareMCPJobForPersistence(record.Job)
	resultJSON := "{}"
	if len(job.Result) > 0 {
		if encoded, err := json.Marshal(job.Result); err == nil && len(encoded) <= 128*1024 {
			resultJSON = string(encoded)
		}
	}
	warningsJSON := "[]"
	if len(job.Warnings) > 0 {
		if encoded, err := json.Marshal(job.Warnings); err == nil {
			warningsJSON = string(encoded)
		}
	}
	descriptorJSON := "{}"
	if encoded, err := json.Marshal(normalizeMCPJobDescriptor(record.Descriptor, job.Type)); err == nil {
		descriptorJSON = string(encoded)
	}
	return []any{
		job.ID, job.Type, job.Status, job.Progress, job.Summary, resultJSON, redactMCPJobMessage(job.Error), job.ErrorCode,
		warningsJSON, boolToInt(job.Retryable), job.RetryCount, job.ParentJobID, job.CreatedAt, job.UpdatedAt, job.CompletedAt,
		job.OwnerUserID, job.OwnerAPIKeyID, descriptorJSON, boolToInt(job.Resumable), job.RecoveryState, job.Attempt,
		record.LeaseOwner, record.LeaseExpiresAt, job.LastHeartbeatAt, job.NextAction,
	}
}

func normalizeMCPJobDescriptor(descriptor mcpJobDescriptor, fallbackType string) mcpJobDescriptor {
	if descriptor.Version <= 0 {
		descriptor.Version = mcpJobDescriptorVersion
	}
	if strings.TrimSpace(descriptor.Type) == "" {
		descriptor.Type = strings.TrimSpace(fallbackType)
	}
	descriptor.Type = normalizeMCPJobType(descriptor.Type)
	descriptor.KnowledgeBaseID = strings.TrimSpace(descriptor.KnowledgeBaseID)
	descriptor.DocumentID = strings.TrimSpace(descriptor.DocumentID)
	descriptor.FileName = strings.TrimSpace(descriptor.FileName)
	descriptor.UploadID = strings.TrimSpace(descriptor.UploadID)
	descriptor.Checksum = strings.ToLower(strings.TrimSpace(descriptor.Checksum))
	descriptor.UploadIDs = cloneStrings(descriptor.UploadIDs)
	return descriptor
}

func normalizeMCPJobType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "eval_dataset":
		return "eval-dataset"
	case "batch_index":
		return "batch-index"
	default:
		return value
	}
}

func prepareMCPJobForPersistence(job model.MCPJob) model.MCPJob {
	job.Summary = redactMCPJobMessage(job.Summary)
	job.Error = redactMCPJobMessage(job.Error)
	job.Warnings = append([]string(nil), job.Warnings...)
	for index := range job.Warnings {
		job.Warnings[index] = redactMCPJobMessage(job.Warnings[index])
	}
	job.Result = sanitizeMCPJobResult(job.Result)
	return job
}

func sanitizeMCPJobResult(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil
	}
	var generic map[string]any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return nil
	}
	return sanitizeMCPJobResultMap(generic)
}

func sanitizeMCPJobResultMap(values map[string]any) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		if isSensitiveMCPJobResultKey(key) {
			continue
		}
		result[key] = sanitizeMCPJobResultValue(value)
	}
	return result
}

func sanitizeMCPJobResultValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return sanitizeMCPJobResultMap(typed)
	case []any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, sanitizeMCPJobResultValue(item))
		}
		return items
	case string:
		return redactMCPJobMessage(typed)
	default:
		return value
	}
}

func isSensitiveMCPJobResultKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "content", "rawcontent", "rawtext", "rawdata", "contentpreview", "originalcontent", "fulltext",
		"path", "filepath", "token", "apikey", "api_key", "cookie", "authorization", "password", "secret",
		"credentials", "headers", "requestbody", "responsebody", "rawresponse":
		return true
	default:
		return false
	}
}

func redactMCPJobMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = mcpJobStoreSecretPattern.ReplaceAllString(value, "[redacted secret]")
	value = mcpJobStoreCredentialPattern.ReplaceAllString(value, "$1=[redacted credential]")
	value = mcpJobStoreURLPattern.ReplaceAllString(value, "[redacted url]")
	value = mcpJobStorePathPattern.ReplaceAllString(value, "[redacted path]")
	return truncateRunes(value, 1000)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := append([]string(nil), values...)
	sort.Strings(cloned)
	return cloned
}
