package service

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ai-localbase/internal/model"

	_ "modernc.org/sqlite"
)

type ChatHistoryStore interface {
	SaveConversation(conversation model.Conversation) error
	ListConversations() ([]model.ConversationListItem, error)
	GetConversation(id string) (*model.Conversation, error)
	DeleteConversation(id string) error
}

type SQLiteChatHistoryStore struct {
	db *sql.DB
}

const sqliteBusyTimeoutMilliseconds = 5000

func sqliteDSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return fmt.Sprintf(
		"%s%s_pragma=busy_timeout%%3d%d&_pragma=foreign_keys%%3dON&_pragma=journal_mode%%3dWAL",
		path,
		separator,
		sqliteBusyTimeoutMilliseconds,
	)
}

func NewSQLiteChatHistoryStore(path string) (*SQLiteChatHistoryStore, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return nil, fmt.Errorf("sqlite chat history path is required")
	}

	if err := os.MkdirAll(filepath.Dir(trimmedPath), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(trimmedPath))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &SQLiteChatHistoryStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *SQLiteChatHistoryStore) init() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite chat history store is nil")
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS conversations (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			knowledge_base_id TEXT NOT NULL,
				document_id TEXT NOT NULL,
				knowledge_scope TEXT NOT NULL DEFAULT 'none',
				scope_version INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT '{}',
			seq INTEGER NOT NULL,
			FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation_seq ON messages(conversation_id, seq);`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_updated_at ON conversations(updated_at DESC);`,
	}

	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("initialize sqlite chat history schema: %w", err)
		}
	}
	if err := s.ensureConversationScopeVersionColumn(); err != nil {
		return err
	}
	if err := s.ensureConversationKnowledgeScopeColumn(); err != nil {
		return err
	}

	return nil
}

func (s *SQLiteChatHistoryStore) ensureConversationKnowledgeScopeColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(conversations)`)
	if err != nil {
		return fmt.Errorf("inspect sqlite chat history schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan sqlite chat history schema: %w", err)
		}
		if name == "knowledge_scope" {
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate sqlite chat history schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close sqlite chat history schema rows: %w", err)
	}
	if !found {
		if _, err := s.db.Exec(`ALTER TABLE conversations ADD COLUMN knowledge_scope TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add conversation knowledge scope: %w", err)
		}
	}
	if _, err := s.db.Exec(`UPDATE conversations
		SET knowledge_scope = CASE
			WHEN TRIM(knowledge_base_id) <> '' OR TRIM(document_id) <> '' THEN 'selected'
			ELSE 'none'
		END
		WHERE TRIM(COALESCE(knowledge_scope, '')) = ''`); err != nil {
		return fmt.Errorf("backfill conversation knowledge scope: %w", err)
	}
	return nil
}

func (s *SQLiteChatHistoryStore) ensureConversationScopeVersionColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(conversations)`)
	if err != nil {
		return fmt.Errorf("inspect sqlite chat history schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan sqlite chat history schema: %w", err)
		}
		if name == "scope_version" {
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate sqlite chat history schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close sqlite chat history schema rows: %w", err)
	}
	if found {
		return nil
	}
	if _, err := s.db.Exec(`ALTER TABLE conversations ADD COLUMN scope_version INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add conversation scope version: %w", err)
	}
	return nil
}

func (s *SQLiteChatHistoryStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteChatHistoryStore) SaveConversation(conversation model.Conversation) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite chat history store is nil")
	}
	if strings.TrimSpace(conversation.ID) == "" {
		return fmt.Errorf("conversation id is required")
	}
	if len(conversation.Messages) == 0 {
		return fmt.Errorf("conversation messages cannot be empty")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(
		`INSERT INTO conversations (id, title, knowledge_base_id, document_id, knowledge_scope, scope_version, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET
			   title = excluded.title,
			   knowledge_base_id = excluded.knowledge_base_id,
			   document_id = excluded.document_id,
			   knowledge_scope = excluded.knowledge_scope,
			   scope_version = excluded.scope_version,
		   created_at = excluded.created_at,
		   updated_at = excluded.updated_at`,
		conversation.ID,
		strings.TrimSpace(conversation.Title),
		strings.TrimSpace(conversation.KnowledgeBaseID),
		strings.TrimSpace(conversation.DocumentID),
		normalizeStoredKnowledgeScope(conversation.KnowledgeScope, conversation.KnowledgeBaseID, conversation.DocumentID),
		conversation.ScopeVersion,
		normalizeTimestamp(conversation.CreatedAt),
		normalizeTimestamp(conversation.UpdatedAt),
	); err != nil {
		return fmt.Errorf("upsert conversation: %w", err)
	}

	if _, err = tx.Exec(`DELETE FROM messages WHERE conversation_id = ?`, conversation.ID); err != nil {
		return fmt.Errorf("replace conversation messages: %w", err)
	}

	for index, message := range conversation.Messages {
		metadataJSON := "{}"
		if len(message.Metadata) > 0 {
			encoded, encodeErr := json.Marshal(message.Metadata)
			if encodeErr != nil {
				return fmt.Errorf("encode message metadata: %w", encodeErr)
			}
			metadataJSON = string(encoded)
		}

		if _, err = tx.Exec(
			`INSERT INTO messages (id, conversation_id, role, content, created_at, metadata, seq)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			message.ID,
			conversation.ID,
			strings.TrimSpace(message.Role),
			message.Content,
			normalizeTimestamp(message.CreatedAt),
			metadataJSON,
			index,
		); err != nil {
			return fmt.Errorf("insert conversation message: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite transaction: %w", err)
	}
	return nil
}

func (s *SQLiteChatHistoryStore) ListConversations() ([]model.ConversationListItem, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("sqlite chat history store is nil")
	}

	rows, err := s.db.Query(`
		SELECT c.id, c.title, c.knowledge_base_id, c.document_id, c.knowledge_scope, c.scope_version, c.created_at, c.updated_at, COUNT(m.id)
		FROM conversations c
		LEFT JOIN messages m ON m.conversation_id = c.id
		GROUP BY c.id, c.title, c.knowledge_base_id, c.document_id, c.knowledge_scope, c.scope_version, c.created_at, c.updated_at
		ORDER BY c.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	items := make([]model.ConversationListItem, 0)
	for rows.Next() {
		var item model.ConversationListItem
		if err := rows.Scan(
			&item.ID,
			&item.Title,
			&item.KnowledgeBaseID,
			&item.DocumentID,
			&item.KnowledgeScope,
			&item.ScopeVersion,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.MessageCount,
		); err != nil {
			return nil, fmt.Errorf("scan conversation item: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversations: %w", err)
	}

	return items, nil
}

func (s *SQLiteChatHistoryStore) GetConversation(id string) (*model.Conversation, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("sqlite chat history store is nil")
	}

	conversationID := strings.TrimSpace(id)
	if conversationID == "" {
		return nil, fmt.Errorf("conversation id is required")
	}

	var conversation model.Conversation
	if err := s.db.QueryRow(
		`SELECT id, title, knowledge_base_id, document_id, knowledge_scope, scope_version, created_at, updated_at
		 FROM conversations
		 WHERE id = ?`,
		conversationID,
	).Scan(
		&conversation.ID,
		&conversation.Title,
		&conversation.KnowledgeBaseID,
		&conversation.DocumentID,
		&conversation.KnowledgeScope,
		&conversation.ScopeVersion,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get conversation: %w", err)
	}

	rows, err := s.db.Query(
		`SELECT id, role, content, created_at, metadata
		 FROM messages
		 WHERE conversation_id = ?
		 ORDER BY seq ASC`,
		conversationID,
	)
	if err != nil {
		return nil, fmt.Errorf("query conversation messages: %w", err)
	}
	defer rows.Close()

	conversation.Messages = make([]model.StoredChatMessage, 0)
	for rows.Next() {
		var message model.StoredChatMessage
		var metadataJSON string
		if err := rows.Scan(
			&message.ID,
			&message.Role,
			&message.Content,
			&message.CreatedAt,
			&metadataJSON,
		); err != nil {
			return nil, fmt.Errorf("scan conversation message: %w", err)
		}
		if strings.TrimSpace(metadataJSON) != "" && metadataJSON != "{}" {
			if err := json.Unmarshal([]byte(metadataJSON), &message.Metadata); err != nil {
				return nil, fmt.Errorf("decode conversation message metadata: %w", err)
			}
		}
		conversation.Messages = append(conversation.Messages, message)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversation messages: %w", err)
	}

	return &conversation, nil
}

func (s *SQLiteChatHistoryStore) DeleteConversation(id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite chat history store is nil")
	}

	conversationID := strings.TrimSpace(id)
	if conversationID == "" {
		return fmt.Errorf("conversation id is required")
	}

	if _, err := s.db.Exec(`DELETE FROM conversations WHERE id = ?`, conversationID); err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	return nil
}

func normalizeTimestamp(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		return trimmed
	}
	return time.Now().UTC().Format(time.RFC3339)
}

func buildConversationTitle(messages []model.StoredChatMessage) string {
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		trimmed := strings.TrimSpace(message.Content)
		if trimmed == "" {
			continue
		}
		runes := []rune(trimmed)
		if len(runes) > 18 {
			return string(runes[:18])
		}
		return trimmed
	}
	return "新的对话"
}

func cloneStoredMessages(messages []model.StoredChatMessage) []model.StoredChatMessage {
	if len(messages) == 0 {
		return []model.StoredChatMessage{}
	}
	cloned := make([]model.StoredChatMessage, 0, len(messages))
	for _, message := range messages {
		copied := message
		if len(message.Metadata) > 0 {
			copied.Metadata = make(map[string]any, len(message.Metadata))
			for key, value := range message.Metadata {
				copied.Metadata[key] = value
			}
		}
		cloned = append(cloned, copied)
	}
	return cloned
}

func sortConversationItems(items []model.ConversationListItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt == items[j].UpdatedAt {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
}
