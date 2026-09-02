package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"

	_ "modernc.org/sqlite"
)

const (
	mcpJobStoreSchemaVersion = 3
	mcpJobStoreDefaultKeep   = 50
	mcpJobLeaseDuration      = 30 * time.Second
	mcpJobHeartbeatInterval  = 10 * time.Second
	mcpJobDescriptorVersion  = 1
	mcpJobResultMaxBytes     = 128 * 1024
	mcpJobStorePrivateMode   = 0o600
)

var ErrMCPJobResultTooLarge = errors.New("mcp job result exceeds persistence limit")

var (
	mcpJobStorePathPattern       = regexp.MustCompile(`(?i)(?:/(?:Users|home|app|opt|workspace|private|var|tmp)/|[A-Za-z]:[\\/]|\\\\|(?:\.\.?[\\/]){1,2})[^\s"'<>]+`)
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
	InputBytes      int64    `json:"inputBytes,omitempty"`
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
	now  func() time.Time
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

	store := &MCPJobStore{db: db, path: trimmedPath, now: func() time.Time { return time.Now().UTC() }}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.protectFiles(); err != nil {
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

// protectFiles covers the database and SQLite's WAL/SHM sidecars. SQLite can
// create the sidecars after the database itself has been chmod'ed, so the
// check is repeated after mutating operations as well as during startup.
func (s *MCPJobStore) protectFiles() error {
	if s == nil {
		return fmt.Errorf("mcp job store is nil")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		filePath := s.path + suffix
		info, err := os.Stat(filePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect mcp job store file %s: %w", suffix, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("mcp job store file %s is not a regular file", suffix)
		}
		if info.Mode().Perm() == mcpJobStorePrivateMode {
			continue
		}
		if err := os.Chmod(filePath, mcpJobStorePrivateMode); err != nil {
			return fmt.Errorf("protect mcp job store file %s: %w", suffix, err)
		}
	}
	return nil
}

func (s *MCPJobStore) protectFilesAfterWrite(operation string) error {
	if err := s.protectFiles(); err != nil {
		return fmt.Errorf("protect mcp job store files after %s: %w", operation, err)
	}
	return nil
}

func (s *MCPJobStore) nowUTC() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
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
			size INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (job_id, upload_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_job_items_job ON mcp_job_items(job_id, updated_at);`,
		`CREATE TABLE IF NOT EXISTS mcp_job_recovery_slots (
			slot_id INTEGER PRIMARY KEY,
			worker_id TEXT NOT NULL DEFAULT '',
			job_id TEXT NOT NULL DEFAULT '',
			attempt INTEGER NOT NULL DEFAULT 0,
			lease_expires_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		);`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("initialize mcp job store schema: %w", err)
		}
	}
	if err := s.ensureColumns(); err != nil {
		return err
	}
	if err := s.ensureRecoverySlots(); err != nil {
		return err
	}
	if version < mcpJobStoreSchemaVersion {
		if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, mcpJobStoreSchemaVersion)); err != nil {
			return fmt.Errorf("write mcp job store schema version: %w", err)
		}
	}
	return nil
}

func (s *MCPJobStore) ensureRecoverySlots() error {
	for slotID := 0; slotID < mcpJobRecoveryConcurrency; slotID++ {
		if _, err := s.db.Exec(`INSERT INTO mcp_job_recovery_slots (slot_id)
			VALUES (?) ON CONFLICT(slot_id) DO NOTHING`, slotID); err != nil {
			return fmt.Errorf("initialize mcp job recovery slot %d: %w", slotID, err)
		}
	}
	return nil
}

func (s *MCPJobStore) ensureColumns() error {
	if err := s.ensureColumnsForTable("mcp_jobs", []struct {
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
	}); err != nil {
		return err
	}
	return s.ensureColumnsForTable("mcp_job_items", []struct {
		name       string
		definition string
	}{
		{"size", "INTEGER NOT NULL DEFAULT 0"},
	})
}

func (s *MCPJobStore) ensureColumnsForTable(table string, columns []struct {
	name       string
	definition string
}) error {
	pragma := ""
	switch table {
	case "mcp_jobs", "mcp_job_items":
		pragma = "PRAGMA table_info(" + table + ")"
	default:
		return fmt.Errorf("unsupported mcp job store table %q", table)
	}
	rows, err := s.db.Query(pragma)
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
	for _, column := range columns {
		if found[column.name] {
			continue
		}
		if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column.name, column.definition)); err != nil {
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
	values, err := mcpJobStoreValues(record)
	if err != nil {
		return fmt.Errorf("prepare mcp job: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO mcp_jobs (
		id, type, status, progress, summary, result_json, error, error_code,
		warnings_json, retryable, retry_count, parent_job_id, created_at, updated_at,
		completed_at, owner_user_id, owner_api_key_id, descriptor_json, resumable,
		recovery_state, attempt, lease_owner, lease_expires_at, last_heartbeat_at, next_action
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		values...,
	)
	if err != nil {
		return fmt.Errorf("create mcp job: %w", err)
	}
	if err := s.protectFilesAfterWrite("create mcp job"); err != nil {
		return err
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
	values, err := mcpJobStoreValues(record)
	if err != nil {
		return false, fmt.Errorf("prepare mcp job: %w", err)
	}
	values = append(values[1:], record.Job.ID, strings.TrimSpace(expectedLeaseOwner), expectedAttempt, s.nowUTC().Format(time.RFC3339Nano))
	result, err := s.db.Exec(`UPDATE mcp_jobs SET
		type = ?, status = ?, progress = ?, summary = ?, result_json = ?, error = ?, error_code = ?,
		warnings_json = ?, retryable = ?, retry_count = ?, parent_job_id = ?, created_at = ?, updated_at = ?,
		completed_at = ?, owner_user_id = ?, owner_api_key_id = ?, descriptor_json = ?, resumable = ?,
		recovery_state = ?, attempt = ?, lease_owner = ?, lease_expires_at = ?, last_heartbeat_at = ?, next_action = ?
		WHERE id = ? AND lease_owner = ? AND attempt = ?
		AND (lease_owner = '' OR lease_expires_at > ?)`, values...)
	if err != nil {
		return false, fmt.Errorf("update mcp job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect mcp job update: %w", err)
	}
	if affected == 1 {
		if err := s.protectFilesAfterWrite("update mcp job"); err != nil {
			return false, err
		}
	}
	return affected == 1, nil
}

// UpdateState persists a worker state transition without rewriting the lease
// expiry. Heartbeats own lease timing; state updates must not put an older
// expiry back into SQLite after a heartbeat has extended it.
func (s *MCPJobStore) UpdateState(record mcpJobStoreRecord, expectedLeaseOwner string, expectedAttempt int) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("mcp job store is nil")
	}
	if strings.TrimSpace(record.Job.ID) == "" {
		return false, fmt.Errorf("mcp job id is required")
	}
	record.Descriptor = normalizeMCPJobDescriptor(record.Descriptor, record.Job.Type)
	values, err := mcpJobStoreValues(record)
	if err != nil {
		return false, fmt.Errorf("prepare mcp job: %w", err)
	}
	stateValues := append([]any{}, values[1:21]...)
	stateValues = append(stateValues, values[23], values[24], record.Job.ID, strings.TrimSpace(expectedLeaseOwner), expectedAttempt, s.nowUTC().Format(time.RFC3339Nano))
	result, err := s.db.Exec(`UPDATE mcp_jobs SET
		type = ?, status = ?, progress = ?, summary = ?, result_json = ?, error = ?, error_code = ?,
		warnings_json = ?, retryable = ?, retry_count = ?, parent_job_id = ?, created_at = ?, updated_at = ?,
		completed_at = ?, owner_user_id = ?, owner_api_key_id = ?, descriptor_json = ?, resumable = ?,
		recovery_state = ?, attempt = ?, last_heartbeat_at = ?, next_action = ?
		WHERE id = ? AND lease_owner = ? AND attempt = ?
		AND (lease_owner = '' OR lease_expires_at > ?)`, stateValues...)
	if err != nil {
		return false, fmt.Errorf("update mcp job state: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect mcp job state update: %w", err)
	}
	if affected == 1 {
		if err := s.protectFilesAfterWrite("update mcp job state"); err != nil {
			return false, err
		}
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
	return s.ClaimRecoverableLimited(workerID, leaseDuration, now, mcpJobRecoveryConcurrency)
}

// ClaimRecoverableLimited claims at most limit jobs. Each claim also owns one
// durable recovery slot, so the limit is enforced across processes sharing the
// same SQLite Job Store.
func (s *MCPJobStore) ClaimRecoverableLimited(workerID string, leaseDuration time.Duration, now time.Time, limit int) ([]mcpJobStoreRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("mcp job store is nil")
	}
	if limit <= 0 || limit > mcpJobRecoveryConcurrency {
		limit = mcpJobRecoveryConcurrency
	}
	recovered := make([]mcpJobStoreRecord, 0, limit)
	for len(recovered) < limit {
		record, claimed, err := s.claimRecoverableOne(workerID, leaseDuration, now)
		if err != nil {
			return nil, err
		}
		if !claimed {
			break
		}
		recovered = append(recovered, record)
	}
	return recovered, nil
}

func (s *MCPJobStore) claimRecoverableOne(workerID string, leaseDuration time.Duration, now time.Time) (mcpJobStoreRecord, bool, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return mcpJobStoreRecord{}, false, fmt.Errorf("mcp worker id is required")
	}
	if leaseDuration <= 0 {
		leaseDuration = mcpJobLeaseDuration
	}
	now = now.UTC()
	nowValue := now.Format(time.RFC3339Nano)
	leaseValue := now.Add(leaseDuration).Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return mcpJobStoreRecord{}, false, fmt.Errorf("begin recover mcp job: %w", err)
	}
	rollback := func() {
		_ = tx.Rollback()
	}

	var slotID int
	if err := tx.QueryRow(`SELECT slot_id FROM mcp_job_recovery_slots
		WHERE worker_id = '' OR lease_expires_at = '' OR lease_expires_at <= ?
		ORDER BY slot_id ASC LIMIT 1`, nowValue).Scan(&slotID); err != nil {
		if err == sql.ErrNoRows {
			rollback()
			return mcpJobStoreRecord{}, false, nil
		}
		rollback()
		return mcpJobStoreRecord{}, false, fmt.Errorf("find mcp job recovery slot: %w", err)
	}
	result, err := tx.Exec(`UPDATE mcp_job_recovery_slots SET
		worker_id = ?, job_id = '', attempt = 0, lease_expires_at = ?, updated_at = ?
		WHERE slot_id = ? AND (worker_id = '' OR lease_expires_at = '' OR lease_expires_at <= ?)`,
		workerID, leaseValue, nowValue, slotID, nowValue)
	if err != nil {
		rollback()
		return mcpJobStoreRecord{}, false, fmt.Errorf("claim mcp job recovery slot: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		rollback()
		return mcpJobStoreRecord{}, false, fmt.Errorf("inspect mcp job recovery slot: %w", err)
	} else if affected != 1 {
		rollback()
		return mcpJobStoreRecord{}, false, nil
	}

	var jobID string
	if err := tx.QueryRow(`SELECT id FROM mcp_jobs
		WHERE resumable = 1 AND (
			(status = 'queued' AND (lease_owner = '' OR lease_expires_at = '' OR lease_expires_at <= ?))
			OR (status = 'running' AND (lease_owner = '' OR lease_expires_at = '' OR lease_expires_at <= ?))
		)
		ORDER BY updated_at ASC, id ASC LIMIT 1`, nowValue, nowValue).Scan(&jobID); err != nil {
		if err == sql.ErrNoRows {
			if _, clearErr := tx.Exec(`UPDATE mcp_job_recovery_slots SET worker_id = '', job_id = '', attempt = 0, lease_expires_at = '', updated_at = ? WHERE slot_id = ?`, nowValue, slotID); clearErr != nil {
				rollback()
				return mcpJobStoreRecord{}, false, fmt.Errorf("release empty mcp job recovery slot: %w", clearErr)
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return mcpJobStoreRecord{}, false, fmt.Errorf("commit empty mcp job recovery slot: %w", commitErr)
			}
			return mcpJobStoreRecord{}, false, nil
		}
		rollback()
		return mcpJobStoreRecord{}, false, fmt.Errorf("find recoverable mcp job: %w", err)
	}

	result, err = tx.Exec(`UPDATE mcp_jobs SET
		status = 'queued', recovery_state = 'recovering', attempt = attempt + 1,
		lease_owner = ?, lease_expires_at = ?, last_heartbeat_at = ?,
		updated_at = ?, next_action = 'worker scheduled after restart'
		WHERE id = ? AND resumable = 1 AND (
			(status = 'queued' AND (lease_owner = '' OR lease_expires_at = '' OR lease_expires_at <= ?))
			OR (status = 'running' AND (lease_owner = '' OR lease_expires_at = '' OR lease_expires_at <= ?))
		)`, workerID, leaseValue, nowValue, nowValue, jobID, nowValue, nowValue)
	if err != nil {
		rollback()
		return mcpJobStoreRecord{}, false, fmt.Errorf("claim recoverable mcp job: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		rollback()
		return mcpJobStoreRecord{}, false, fmt.Errorf("inspect recoverable mcp job claim: %w", err)
	} else if affected != 1 {
		rollback()
		return mcpJobStoreRecord{}, false, nil
	}

	record, err := scanMCPJobRow(tx.QueryRow(mcpJobSelectSQL+` WHERE id = ?`, jobID))
	if err != nil {
		rollback()
		return mcpJobStoreRecord{}, false, fmt.Errorf("load recovered mcp job: %w", err)
	}
	if _, err := tx.Exec(`UPDATE mcp_job_recovery_slots SET job_id = ?, attempt = ?, lease_expires_at = ?, updated_at = ? WHERE slot_id = ? AND worker_id = ?`,
		jobID, record.Job.Attempt, leaseValue, nowValue, slotID, workerID); err != nil {
		rollback()
		return mcpJobStoreRecord{}, false, fmt.Errorf("bind mcp job recovery slot: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return mcpJobStoreRecord{}, false, fmt.Errorf("commit recovered mcp job: %w", err)
	}
	if err := s.protectFilesAfterWrite("recover mcp job"); err != nil {
		return mcpJobStoreRecord{}, false, err
	}
	return record, true, nil
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
	if _, err := tx.Exec(`UPDATE mcp_job_recovery_slots SET
		worker_id = '', job_id = '', attempt = 0, lease_expires_at = '', updated_at = ?
		WHERE job_id = ?`, s.nowUTC().Format(time.RFC3339Nano), strings.TrimSpace(jobID)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("release mcp job recovery slots: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM mcp_jobs WHERE id = ?`, strings.TrimSpace(jobID)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete mcp job: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete mcp job: %w", err)
	}
	if err := s.protectFilesAfterWrite("delete mcp job"); err != nil {
		return err
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
	now = now.UTC()
	leaseValue := now.Add(leaseDuration).Format(time.RFC3339Nano)
	nowValue := now.Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin renew mcp job lease: %w", err)
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	result, err := tx.Exec(`UPDATE mcp_jobs SET
		lease_expires_at = ?, last_heartbeat_at = ?, updated_at = ?
		WHERE id = ? AND lease_owner = ? AND attempt = ? AND status IN ('queued', 'running')
		AND lease_expires_at > ?`,
		leaseValue, nowValue, nowValue,
		strings.TrimSpace(jobID), strings.TrimSpace(workerID), attempt, nowValue,
	)
	if err != nil {
		rollback()
		return false, fmt.Errorf("renew mcp job lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		rollback()
		return false, fmt.Errorf("inspect mcp job lease renewal: %w", err)
	}
	if affected != 1 {
		rollback()
		return false, nil
	}
	if _, err := tx.Exec(`UPDATE mcp_job_recovery_slots SET
		lease_expires_at = ?, updated_at = ?
		WHERE job_id = ? AND worker_id = ? AND attempt = ?`,
		leaseValue, nowValue, strings.TrimSpace(jobID), strings.TrimSpace(workerID), attempt); err != nil {
		rollback()
		return false, fmt.Errorf("renew mcp job recovery slot: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit mcp job lease renewal: %w", err)
	}
	if err := s.protectFilesAfterWrite("renew mcp job lease"); err != nil {
		return false, err
	}
	return true, nil
}

// ReleaseRecoverySlot makes a recovered worker's durable concurrency slot
// reusable. The owner and attempt guard prevent an old worker from releasing a
// slot that a later recovery attempt has already claimed.
func (s *MCPJobStore) ReleaseRecoverySlot(jobID, workerID string, attempt int) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("mcp job store is nil")
	}
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(workerID) == "" || attempt <= 0 {
		return nil
	}
	_, err := s.db.Exec(`UPDATE mcp_job_recovery_slots SET
		worker_id = '', job_id = '', attempt = 0, lease_expires_at = '', updated_at = ?
		WHERE job_id = ? AND worker_id = ? AND attempt = ?`,
		s.nowUTC().Format(time.RFC3339Nano), strings.TrimSpace(jobID), strings.TrimSpace(workerID), attempt)
	if err != nil {
		return fmt.Errorf("release mcp job recovery slot: %w", err)
	}
	if err := s.protectFilesAfterWrite("release mcp job recovery slot"); err != nil {
		return err
	}
	return nil
}

// LinkRetry updates both sides of a retry relation in one SQLite transaction.
// The parent CAS is the concurrency guard: only one child can consume a
// retryable failed parent, while the child relation and ownership are updated
// atomically with that decision.
func (s *MCPJobStore) LinkRetry(parentJobID, childJobID string, retryCount int, ownerUserID, ownerAPIKeyID string) (mcpJobStoreRecord, mcpJobStoreRecord, error) {
	if s == nil || s.db == nil {
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("mcp job store is nil")
	}
	parentJobID = strings.TrimSpace(parentJobID)
	childJobID = strings.TrimSpace(childJobID)
	if parentJobID == "" || childJobID == "" || retryCount <= 0 {
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("retry job identifiers and count are required")
	}
	nowValue := s.nowUTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("begin link mcp retry: %w", err)
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	result, err := tx.Exec(`UPDATE mcp_jobs SET
		retryable = 0, updated_at = ?
		WHERE id = ? AND status = 'failed' AND retryable = 1 AND retry_count < ?`,
		nowValue, parentJobID, retryCount)
	if err != nil {
		rollback()
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("claim mcp retry parent: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		rollback()
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("inspect mcp retry parent: %w", err)
	}
	if affected != 1 {
		rollback()
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("mcp retry parent is no longer retryable")
	}
	result, err = tx.Exec(`UPDATE mcp_jobs SET
		retry_count = ?, parent_job_id = ?, owner_user_id = ?, owner_api_key_id = ?,
		retryable = CASE WHEN status = 'failed' AND ? < ? THEN 1 ELSE retryable END,
		updated_at = ?
		WHERE id = ?`,
		retryCount, parentJobID, strings.TrimSpace(ownerUserID), strings.TrimSpace(ownerAPIKeyID), retryCount, mcpJobMaxRetries, nowValue, childJobID)
	if err != nil {
		rollback()
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("link mcp retry child: %w", err)
	}
	affected, err = result.RowsAffected()
	if err != nil {
		rollback()
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("inspect mcp retry child: %w", err)
	}
	if affected != 1 {
		rollback()
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("mcp retry child not found")
	}
	parent, err := scanMCPJobRow(tx.QueryRow(mcpJobSelectSQL+` WHERE id = ?`, parentJobID))
	if err != nil {
		rollback()
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("load linked mcp retry parent: %w", err)
	}
	child, err := scanMCPJobRow(tx.QueryRow(mcpJobSelectSQL+` WHERE id = ?`, childJobID))
	if err != nil {
		rollback()
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("load linked mcp retry child: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, fmt.Errorf("commit linked mcp retry: %w", err)
	}
	if err := s.protectFilesAfterWrite("link mcp retry"); err != nil {
		return mcpJobStoreRecord{}, mcpJobStoreRecord{}, err
	}
	return parent, child, nil
}

func (s *MCPJobStore) LeaseOwned(jobID, workerID string, attempt int) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("mcp job store is nil")
	}
	jobID = strings.TrimSpace(jobID)
	workerID = strings.TrimSpace(workerID)
	if jobID == "" || workerID == "" || attempt <= 0 {
		return false, nil
	}
	var exists int
	err := s.db.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM mcp_jobs
		WHERE id = ? AND lease_owner = ? AND attempt = ? AND status IN ('queued', 'running')
		AND lease_expires_at > ?
	)`, jobID, workerID, attempt, s.nowUTC().Format(time.RFC3339Nano)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check mcp job lease: %w", err)
	}
	return exists != 0, nil
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
	if err := s.protectFilesAfterWrite("prune mcp jobs"); err != nil {
		return 0, err
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
	nowValue := s.nowUTC().Format(time.RFC3339Nano)
	args := []any{"任务已取消。", nowValue, strings.TrimSpace(jobID)}
	if owner := strings.TrimSpace(expectedLeaseOwner); owner != "" {
		query += ` AND EXISTS (
			SELECT 1 FROM mcp_jobs WHERE mcp_jobs.id = mcp_job_items.job_id
			AND mcp_jobs.lease_owner = ? AND mcp_jobs.attempt = ?
			AND mcp_jobs.status IN ('queued', 'running')
			AND mcp_jobs.lease_expires_at > ?
		)`
		args = append(args, owner, expectedAttempt, nowValue)
	}
	if _, err := s.db.Exec(query, args...); err != nil {
		return fmt.Errorf("cancel mcp job items: %w", err)
	}
	if err := s.protectFilesAfterWrite("cancel mcp job items"); err != nil {
		return err
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
	nowValue := s.nowUTC().Format(time.RFC3339Nano)
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
	if err := s.protectFilesAfterWrite("probe mcp job store writeability"); err != nil {
		return stats, err
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
	if err := s.protectFilesAfterWrite("claim mcp job"); err != nil {
		return mcpJobStoreRecord{}, false, err
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
	if !found || record.LeaseOwner != workerID {
		return mcpJobStoreRecord{}, false, nil
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

func mcpJobStoreValues(record mcpJobStoreRecord) ([]any, error) {
	job, err := prepareMCPJobForPersistence(record.Job)
	if err != nil {
		return nil, err
	}
	descriptor, err := validateMCPJobDescriptor(record.Descriptor, job.Type)
	if err != nil {
		return nil, fmt.Errorf("validate mcp job descriptor: %w", err)
	}
	resultJSON, err := encodeMCPJobResult(job.Result)
	if err != nil {
		return nil, err
	}
	warningsJSON := "[]"
	if len(job.Warnings) > 0 {
		if encoded, err := json.Marshal(job.Warnings); err == nil {
			warningsJSON = string(encoded)
		} else {
			return nil, fmt.Errorf("encode mcp job warnings: %w", err)
		}
	}
	descriptorJSON := "{}"
	if encoded, err := json.Marshal(descriptor); err != nil {
		return nil, fmt.Errorf("encode mcp job descriptor: %w", err)
	} else {
		descriptorJSON = string(encoded)
	}
	return []any{
		job.ID, job.Type, job.Status, job.Progress, job.Summary, resultJSON, redactMCPJobMessage(job.Error), job.ErrorCode,
		warningsJSON, boolToInt(job.Retryable), job.RetryCount, job.ParentJobID, job.CreatedAt, job.UpdatedAt, job.CompletedAt,
		job.OwnerUserID, job.OwnerAPIKeyID, descriptorJSON, boolToInt(job.Resumable), job.RecoveryState, job.Attempt,
		record.LeaseOwner, record.LeaseExpiresAt, job.LastHeartbeatAt, job.NextAction,
	}, nil
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
	if strings.TrimSpace(descriptor.FileName) != "" {
		if normalized, err := util.NormalizeFilename(descriptor.FileName); err == nil {
			descriptor.FileName = normalized
		} else {
			descriptor.FileName = util.SanitizeFilename(descriptor.FileName)
		}
	}
	descriptor.UploadID = strings.TrimSpace(descriptor.UploadID)
	descriptor.Checksum = strings.ToLower(strings.TrimSpace(descriptor.Checksum))
	descriptor.UploadIDs = cloneStrings(descriptor.UploadIDs)
	if descriptor.Type == "batch-index" && descriptor.Concurrency == 0 {
		descriptor.Concurrency = mcpBatchDefaultConcurrency
	}
	return descriptor
}

func validateMCPJobDescriptor(descriptor mcpJobDescriptor, fallbackType string) (mcpJobDescriptor, error) {
	descriptor = normalizeMCPJobDescriptor(descriptor, fallbackType)
	if descriptor.Type != "batch-index" {
		return descriptor, nil
	}
	concurrency, err := ValidateMCPBatchConcurrency(descriptor.Concurrency)
	if err != nil {
		return descriptor, fmt.Errorf("invalid persisted batch concurrency: %w", err)
	}
	descriptor.Concurrency = concurrency
	if descriptor.InputBytes < 0 || descriptor.InputBytes > mcpBatchMaxInputBytes {
		return descriptor, fmt.Errorf("invalid persisted batch input size: must be between 0 and %s", util.FormatFileSize(mcpBatchMaxInputBytes))
	}
	return descriptor, nil
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

func prepareMCPJobForPersistence(job model.MCPJob) (model.MCPJob, error) {
	job.Summary = redactMCPJobMessage(job.Summary)
	job.Error = redactMCPJobMessage(job.Error)
	job.Warnings = append([]string(nil), job.Warnings...)
	for index := range job.Warnings {
		job.Warnings[index] = redactMCPJobMessage(job.Warnings[index])
	}
	sanitizedResult, err := prepareMCPJobResult(job.Result)
	if err != nil {
		return model.MCPJob{}, fmt.Errorf("sanitize mcp job result: %w", err)
	}
	job.Result = sanitizedResult
	return job, nil
}

func prepareMCPJobResult(values map[string]any) (map[string]any, error) {
	sanitized, err := sanitizeMCPJobResultStrict(values)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeMCPJobResult(sanitized)
	if err != nil {
		return nil, err
	}
	if encoded == "{}" {
		return map[string]any{}, nil
	}
	var bounded map[string]any
	if err := json.Unmarshal([]byte(encoded), &bounded); err != nil {
		return nil, err
	}
	return bounded, nil
}

func sanitizeMCPJobResult(values map[string]any) map[string]any {
	sanitized, _ := sanitizeMCPJobResultStrict(values)
	return sanitized
}

func sanitizeMCPJobResultStrict(values map[string]any) (map[string]any, error) {
	if values == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	var generic map[string]any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return nil, err
	}
	return sanitizeMCPJobResultMap(generic), nil
}

func encodeMCPJobResult(values map[string]any) (string, error) {
	if len(values) == 0 {
		return "{}", nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode mcp job result: %w", err)
	}
	if len(encoded) <= mcpJobResultMaxBytes {
		return string(encoded), nil
	}
	truncated := truncateMCPJobResult(values, len(encoded))
	encoded, err = json.Marshal(truncated)
	if err != nil {
		return "", fmt.Errorf("encode truncated mcp job result: %w", err)
	}
	if len(encoded) > mcpJobResultMaxBytes {
		return "", fmt.Errorf("%w: truncated result is %d bytes", ErrMCPJobResultTooLarge, len(encoded))
	}
	return string(encoded), nil
}

const (
	mcpJobResultPreviewFields = 128
	mcpJobResultPreviewRunes  = 256
)

// truncateMCPJobResult keeps bounded metadata and scalar counters while
// dropping bulky payloads. The result remains valid JSON and therefore does
// not change the terminal state or retry semantics of the parent job.
func truncateMCPJobResult(values map[string]any, originalBytes int) map[string]any {
	statistics := map[string]any{
		"originalBytes":  originalBytes,
		"maxBytes":       mcpJobResultMaxBytes,
		"topLevelFields": len(values),
		"arrayItems":     countMCPJobResultArrayItems(values),
		"stringValues":   countMCPJobResultStrings(values),
	}
	result := map[string]any{
		"truncated":  true,
		"summary":    fmt.Sprintf("任务结果原始大小为 %d 字节，超过 %d 字节持久化上限，已保存摘要和统计信息。", originalBytes, mcpJobResultMaxBytes),
		"statistics": statistics,
		"truncation": map[string]any{
			"originalBytes": originalBytes,
			"maxBytes":      mcpJobResultMaxBytes,
		},
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	omittedFields := 0
	for _, key := range keys {
		if key == "truncated" || key == "summary" || key == "statistics" || key == "truncation" {
			continue
		}
		if len(result) >= mcpJobResultPreviewFields+4 {
			omittedFields++
			continue
		}
		result[truncateRunes(key, 128)] = summarizeMCPJobResultValue(values[key])
	}
	if omittedFields > 0 {
		result["omittedFields"] = omittedFields
	}

	encoded, err := json.Marshal(result)
	if err == nil && len(encoded) <= mcpJobResultMaxBytes {
		return result
	}
	// The metadata-only fallback is intentionally tiny even if a caller supplied
	// an unusually large number of top-level fields or field names.
	return map[string]any{
		"truncated":  true,
		"summary":    "任务结果过大，已保存摘要和统计信息。",
		"statistics": statistics,
		"truncation": map[string]any{
			"originalBytes": originalBytes,
			"maxBytes":      mcpJobResultMaxBytes,
		},
		"omittedFields": len(keys),
	}
}

func summarizeMCPJobResultValue(value any) any {
	switch typed := value.(type) {
	case string:
		return truncateRunes(typed, mcpJobResultPreviewRunes)
	case []any:
		return map[string]any{"itemCount": len(typed)}
	case map[string]any:
		return map[string]any{"fieldCount": len(typed)}
	default:
		return value
	}
}

func countMCPJobResultArrayItems(values map[string]any) int {
	count := 0
	for _, value := range values {
		if items, ok := value.([]any); ok {
			count += len(items)
		}
	}
	return count
}

func countMCPJobResultStrings(values map[string]any) int {
	count := 0
	for _, value := range values {
		if _, ok := value.(string); ok {
			count++
		}
	}
	return count
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
