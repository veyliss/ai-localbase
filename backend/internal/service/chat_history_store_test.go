package service

import (
	"database/sql"
	"path/filepath"
	"testing"

	"ai-localbase/internal/model"
)

func TestSQLiteChatHistoryStoreMigratesConversationScopeVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-history.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	legacySchema := []string{
		`CREATE TABLE conversations (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			document_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE messages (
			id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT '{}',
			seq INTEGER NOT NULL
		)`,
		`INSERT INTO conversations VALUES ('legacy', '旧会话', 'kb-school', '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
	}
	for _, statement := range legacySchema {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("initialize legacy database: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	store, err := NewSQLiteChatHistoryStore(path)
	if err != nil {
		t.Fatalf("migrate chat history store: %v", err)
	}
	defer store.Close()

	legacy, err := store.GetConversation("legacy")
	if err != nil {
		t.Fatalf("get legacy conversation: %v", err)
	}
	if legacy == nil || legacy.ScopeVersion != 0 {
		t.Fatalf("expected legacy conversation scope version 0, got %#v", legacy)
	}
	if legacy.KnowledgeScope != KnowledgeScopeSelected {
		t.Fatalf("expected legacy knowledge base conversation to migrate to selected scope, got %q", legacy.KnowledgeScope)
	}

	if err := store.SaveConversation(model.Conversation{
		ID:              "scoped",
		Title:           "新会话",
		KnowledgeBaseID: "kb-school",
		ScopeVersion:    conversationScopeVersion,
		Messages: []model.StoredChatMessage{{
			ID: "message-1", Role: "user", Content: "介绍资料",
		}},
	}); err != nil {
		t.Fatalf("save scoped conversation: %v", err)
	}
	scoped, err := store.GetConversation("scoped")
	if err != nil {
		t.Fatalf("get scoped conversation: %v", err)
	}
	if scoped == nil || scoped.ScopeVersion != conversationScopeVersion {
		t.Fatalf("expected scope version %d, got %#v", conversationScopeVersion, scoped)
	}
	if scoped.KnowledgeScope != KnowledgeScopeSelected {
		t.Fatalf("expected scoped conversation to retain selected scope, got %q", scoped.KnowledgeScope)
	}
}

func TestSQLiteChatHistoryStoreUsesReliablePragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-history.db")
	store, err := NewSQLiteChatHistoryStore(path)
	if err != nil {
		t.Fatalf("create chat history store: %v", err)
	}
	defer store.Close()

	var journalMode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("expected WAL journal mode, got %q", journalMode)
	}

	var busyTimeout int
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy timeout: %v", err)
	}
	if busyTimeout != sqliteBusyTimeoutMilliseconds {
		t.Fatalf("expected busy timeout %d, got %d", sqliteBusyTimeoutMilliseconds, busyTimeout)
	}

	if got := store.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("expected one SQLite connection, got %d", got)
	}
}
