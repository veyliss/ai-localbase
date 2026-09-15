package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

func TestRetryPendingKnowledgeBaseCleanupDoesNotReDeletePointsAfterCollectionRemoval(t *testing.T) {
	requests := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodDelete || r.URL.Path != "/collections/kb-kb-cleanup" {
			t.Fatalf("unexpected qdrant request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":true}`))
	}))
	defer server.Close()

	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.txt")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	indexedContentDir := filepath.Join(root, "indexed-content")
	document := model.Document{ID: "doc-cleanup", KnowledgeBaseID: "kb-cleanup", Name: "source.txt"}
	store := NewIndexedContentStore(indexedContentDir)
	if err := store.Put(document, "indexed content", nil); err != nil {
		t.Fatalf("put indexed content: %v", err)
	}

	config := model.ServerConfig{QdrantURL: server.URL, QdrantCollectionPrefix: "kb-", QdrantVectorSize: 2}
	service := &AppService{
		qdrant:              NewQdrantService(config),
		indexedContentStore: store,
		state: &model.AppState{IndexCleanupTasks: []model.IndexCleanupTask{{
			ID:              "cleanup-1",
			Type:            indexCleanupTypeKnowledgeBase,
			KnowledgeBaseID: "kb-cleanup",
			DocumentIDs:     []string{"doc-cleanup"},
			SourcePaths:     []string{sourcePath},
		}}},
	}

	processed, err := service.RetryPendingIndexCleanup()
	if err != nil || processed != 1 {
		t.Fatalf("expected cleanup to complete, processed=%d err=%v", processed, err)
	}
	if len(requests) != 1 || requests[0] != "DELETE /collections/kb-kb-cleanup" {
		t.Fatalf("expected only collection deletion, got %v", requests)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("expected source file to be removed, stat error=%v", err)
	}
	if _, found, err := store.Load(document); err != nil || found {
		t.Fatalf("expected indexed content to be removed, found=%v err=%v", found, err)
	}
	if len(service.state.IndexCleanupTasks) != 0 {
		t.Fatalf("expected cleanup task to be removed, got %#v", service.state.IndexCleanupTasks)
	}
}

func TestRetryPendingIndexCleanupPersistsFailureBackoff(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	config := model.ServerConfig{
		StateFile:              statePath,
		QdrantURL:              "http://127.0.0.1:1",
		QdrantCollectionPrefix: "kb-",
		QdrantTimeoutSeconds:   1,
	}
	store := NewAppStateStore(statePath)
	service := &AppService{
		qdrant: NewQdrantService(config),
		store:  store,
		state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{}, IndexCleanupTasks: []model.IndexCleanupTask{{
			ID:              "cleanup-1",
			Type:            indexCleanupTypeCollection,
			KnowledgeBaseID: "kb-failed",
		}}},
	}
	if err := service.saveState(); err != nil {
		t.Fatalf("persist cleanup task: %v", err)
	}

	processed, err := service.RetryPendingIndexCleanup()
	if err == nil || processed != 0 {
		t.Fatalf("expected cleanup failure, processed=%d err=%v", processed, err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if len(loaded.IndexCleanupTasks) != 1 {
		t.Fatalf("expected one persisted cleanup task, got %#v", loaded.IndexCleanupTasks)
	}
	task := loaded.IndexCleanupTasks[0]
	if task.Attempts != 1 || strings.TrimSpace(task.LastError) == "" || strings.TrimSpace(task.NextAttemptAt) == "" {
		t.Fatalf("expected persisted retry metadata, got %#v", task)
	}
	nextAttempt, err := time.Parse(time.RFC3339Nano, task.NextAttemptAt)
	if err != nil || !nextAttempt.After(time.Now().UTC()) {
		t.Fatalf("expected future retry time, got %q err=%v", task.NextAttemptAt, err)
	}
}

func TestDeleteDocumentRemovesPendingMarkerAfterExternalCleanup(t *testing.T) {
	var deleteBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/collections/kb-kb-1/points/delete" {
			t.Fatalf("unexpected qdrant request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&deleteBody); err != nil {
			t.Fatalf("decode delete request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":true}`))
	}))
	defer server.Close()

	config := model.ServerConfig{QdrantURL: server.URL, QdrantCollectionPrefix: "kb-", QdrantVectorSize: 2}
	service := &AppService{
		qdrant: NewQdrantService(config),
		state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
			"kb-1": {ID: "kb-1", Documents: []model.Document{{
				ID: "doc-1", KnowledgeBaseID: "kb-1", Name: "notes.txt", Status: "indexed",
			}}},
		}},
	}

	if _, err := service.DeleteDocument("kb-1", "doc-1"); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if len(deleteBody) == 0 {
		t.Fatal("expected qdrant delete filter")
	}
	if _, err := service.findDocument("kb-1", "doc-1"); err == nil {
		t.Fatal("expected document metadata to be removed after cleanup")
	}
	if len(service.state.IndexCleanupTasks) != 0 {
		t.Fatalf("expected cleanup task to be removed, got %#v", service.state.IndexCleanupTasks)
	}
}

func TestRetryPendingDocumentCleanupFinalizesMetadata(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.txt")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	document := model.Document{
		ID:              "doc-pending",
		KnowledgeBaseID: "kb-1",
		Name:            "source.txt",
		Status:          "indexed",
		DeletionPending: true,
		Path:            sourcePath,
	}
	indexedContentStore := NewIndexedContentStore(filepath.Join(root, "indexed-content"))
	if err := indexedContentStore.Put(document, "indexed content", nil); err != nil {
		t.Fatalf("put indexed content: %v", err)
	}
	service := &AppService{
		indexedContentStore: indexedContentStore,
		state: &model.AppState{
			KnowledgeBases: map[string]model.KnowledgeBase{
				"kb-1": {ID: "kb-1", Documents: []model.Document{document}},
			},
			IndexCleanupTasks: []model.IndexCleanupTask{{
				ID:              "cleanup-document",
				Type:            indexCleanupTypeDocument,
				KnowledgeBaseID: "kb-1",
				DocumentID:      "doc-pending",
				SourcePaths:     []string{sourcePath},
			}},
		},
	}

	processed, err := service.RetryPendingIndexCleanup()
	if err != nil || processed != 1 {
		t.Fatalf("expected pending document cleanup to complete, processed=%d err=%v", processed, err)
	}
	if _, err := service.findDocument("kb-1", "doc-pending"); err == nil {
		t.Fatal("expected pending document metadata to be finalized")
	}
	if len(service.state.IndexCleanupTasks) != 0 {
		t.Fatalf("expected cleanup task to be removed, got %#v", service.state.IndexCleanupTasks)
	}
}
