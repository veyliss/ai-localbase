package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

func TestIndexOperationCommitRejectsSupersededWorker(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-1": {
			ID: "kb-1",
			Documents: []model.Document{{
				ID:              "doc-1",
				KnowledgeBaseID: "kb-1",
				Name:            "notes.txt",
				Version:         1,
				IndexFence:      "generation-old",
				Status:          "indexed",
			}},
		},
	}}}

	first, err := service.beginIndexOperation(context.Background(), service.state.KnowledgeBases["kb-1"].Documents[0])
	if err != nil {
		t.Fatalf("begin first index operation: %v", err)
	}
	second, err := service.beginIndexOperation(context.Background(), service.state.KnowledgeBases["kb-1"].Documents[0])
	if err != nil {
		t.Fatalf("begin replacement index operation: %v", err)
	}
	if first.Fence == second.Fence {
		t.Fatalf("expected unique operation fences, got %q", first.Fence)
	}

	_, err = service.commitIndexOperation(context.Background(), first, model.Document{
		ID:              "doc-1",
		KnowledgeBaseID: "kb-1",
		Name:            "notes.txt",
		Version:         1,
		Status:          "indexed",
		IndexFence:      first.Fence,
	}, "reindex", nowForIndexOperationTest())
	if !errors.Is(err, ErrIndexOperationSuperseded) {
		t.Fatalf("expected stale operation commit to be rejected, got %v", err)
	}

	current, err := service.findDocument("kb-1", "doc-1")
	if err != nil {
		t.Fatalf("load current document: %v", err)
	}
	if current.IndexFence != "generation-old" || current.IndexOperationFence != second.Fence {
		t.Fatalf("expected current generation and replacement marker to remain intact, got %+v", current)
	}
}

func nowForIndexOperationTest() (now time.Time) {
	return time.Now().UTC()
}

func newIPv4TestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for test server: %v", err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func TestIndexDocumentPersistsFirstIndexLifecycle(t *testing.T) {
	embeddingServer := newIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		var request ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		vectors := make([][]float64, len(request.Input))
		for index := range vectors {
			vectors[index] = make([]float64, 768)
			vectors[index][index%768] = 1
		}
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: vectors})
	}))

	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	sourcePath := filepath.Join(root, "first-index.md")
	content := strings.Repeat("首次索引内容需要在重启后仍然可读。", 80)
	if err := os.WriteFile(sourcePath, []byte(content), 0o600); err != nil {
		t.Fatalf("write source document: %v", err)
	}

	config := model.ServerConfig{IndexedContentDir: filepath.Join(root, "indexed-content")}
	store := NewAppStateStore(statePath)
	service := NewAppService(nil, store, nil, config)
	service.state.Config.Embedding = model.EmbeddingConfig{
		Provider: "ollama",
		BaseURL:  embeddingServer.URL,
		Model:    "test-embedding",
	}
	kbID := service.ListKnowledgeBases()[0].ID
	indexed, err := service.IndexDocument(model.Document{
		ID:              "doc-first-index",
		KnowledgeBaseID: kbID,
		Name:            "first-index.md",
		Path:            sourcePath,
		Status:          "processing",
	})
	if err != nil {
		t.Fatalf("index first document: %v", err)
	}
	if indexed.Status != "indexed" || strings.TrimSpace(indexed.IndexFence) == "" || indexed.IndexRunID == "" {
		t.Fatalf("expected committed first index, got %+v", indexed)
	}

	reloaded := NewAppService(nil, store, nil, config)
	document, err := reloaded.findDocument(kbID, indexed.ID)
	if err != nil {
		t.Fatalf("load indexed document after restart: %v", err)
	}
	if document.Status != "indexed" || document.IndexFence != indexed.IndexFence || document.IndexOperationFence != "" || document.IndexOperationOwner != "" || document.IndexOperationAttempt != 0 {
		t.Fatalf("expected durable committed document without active marker, got %+v", document)
	}
	if len(reloaded.ListKnowledgeBases()[0].IndexHistory) != 1 || reloaded.ListKnowledgeBases()[0].IndexHistory[0].Status != "succeeded" {
		t.Fatalf("expected one persisted successful index run, got %+v", reloaded.ListKnowledgeBases()[0].IndexHistory)
	}
	loadedContent, source, err := reloaded.resolveDocumentContent(document)
	if err != nil || source != "indexed" || loadedContent != content {
		t.Fatalf("expected complete indexed snapshot after restart, source=%q err=%v chars=%d", source, err, len([]rune(loadedContent)))
	}
}

func TestFirstIndexFailurePersistsRetryableDocumentState(t *testing.T) {
	root := t.TempDir()
	store := NewAppStateStore(filepath.Join(root, "state.json"))
	config := model.ServerConfig{IndexedContentDir: filepath.Join(root, "indexed-content")}
	service := NewAppService(nil, store, nil, config)
	kbID := service.ListKnowledgeBases()[0].ID

	_, err := service.IndexDocument(model.Document{
		ID:              "doc-first-index-failure",
		KnowledgeBaseID: kbID,
		Name:            "missing.txt",
		Path:            filepath.Join(root, "missing.txt"),
		Status:          "processing",
	})
	if err == nil {
		t.Fatal("expected first index to fail for a missing source")
	}

	document, err := service.findDocument(kbID, "doc-first-index-failure")
	if err != nil {
		t.Fatalf("load failed first index document: %v", err)
	}
	if document.Status != "failed" || document.IndexErrorCode != indexErrorSourceMissing || document.IndexRunID == "" {
		t.Fatalf("expected retryable persisted failure state, got %+v", document)
	}
	if document.IndexOperationFence != "" || document.IndexOperationOwner != "" || document.IndexOperationAttempt != 0 {
		t.Fatalf("expected failed operation marker to be cleared, got %+v", document)
	}

	reloaded := NewAppService(nil, store, nil, config)
	persisted, err := reloaded.findDocument(kbID, document.ID)
	if err != nil {
		t.Fatalf("reload failed first index document: %v", err)
	}
	if persisted.Status != "failed" || persisted.IndexErrorCode != indexErrorSourceMissing || persisted.IndexRunID != document.IndexRunID {
		t.Fatalf("expected failure state to survive restart, got %+v", persisted)
	}
	if len(reloaded.ListKnowledgeBases()[0].IndexHistory) != 1 || reloaded.ListKnowledgeBases()[0].IndexHistory[0].Status != "failed" {
		t.Fatalf("expected one persisted failed index run, got %+v", reloaded.ListKnowledgeBases()[0].IndexHistory)
	}
}

func TestIndexOperationCommitRollbackClearsMarkerAfterStateSaveFailure(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	store := NewAppStateStore(statePath)
	previous := model.Document{
		ID:              "doc-rollback-commit",
		KnowledgeBaseID: "kb-1",
		Name:            "notes.txt",
		Version:         1,
		Status:          "indexed",
		IndexFence:      "generation-old",
		IndexRunID:      "run-old",
		ContentPreview:  "旧内容",
	}
	service := &AppService{
		store: store,
		state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
			"kb-1": {ID: "kb-1", UpdatedAt: "before", Documents: []model.Document{previous}},
		}},
	}
	op, err := service.beginIndexOperation(context.Background(), previous)
	if err != nil {
		t.Fatalf("begin index operation: %v", err)
	}
	if err := os.Mkdir(statePath+".tmp", 0o700); err != nil {
		t.Fatalf("block state temp path: %v", err)
	}

	_, err = service.commitIndexOperation(context.Background(), op, model.Document{
		ID:              previous.ID,
		KnowledgeBaseID: previous.KnowledgeBaseID,
		Name:            previous.Name,
		Version:         previous.Version,
		Status:          "indexed",
		IndexFence:      op.Fence,
		ContentPreview:  "新内容",
	}, "reindex", time.Now().Add(-time.Second))
	if err == nil {
		t.Fatal("expected commit persistence failure")
	}

	current, err := service.findDocument("kb-1", previous.ID)
	if err != nil {
		t.Fatalf("load rolled back document: %v", err)
	}
	if current.Status != previous.Status || current.IndexFence != previous.IndexFence || current.IndexRunID != previous.IndexRunID || current.ContentPreview != previous.ContentPreview || current.IndexOperationFence != "" || current.IndexOperationOwner != "" || current.IndexOperationAttempt != 0 {
		t.Fatalf("expected exact visible state with cleared marker, got %+v", current)
	}
	if got := len(service.ListKnowledgeBases()[0].IndexHistory); got != 0 {
		t.Fatalf("expected failed commit not to leave index history, got %d entries", got)
	}
}

func TestIndexOperationFailureRollbackClearsMarkerAfterStateSaveFailure(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	store := NewAppStateStore(statePath)
	previous := model.Document{
		ID:              "doc-rollback-failure",
		KnowledgeBaseID: "kb-1",
		Name:            "notes.txt",
		Version:         1,
		Status:          "indexed",
		IndexFence:      "generation-old",
		IndexRunID:      "run-old",
	}
	service := &AppService{
		store: store,
		state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
			"kb-1": {ID: "kb-1", UpdatedAt: "before", Documents: []model.Document{previous}},
		}},
	}
	op, err := service.beginIndexOperation(context.Background(), previous)
	if err != nil {
		t.Fatalf("begin index operation: %v", err)
	}
	if err := os.Mkdir(statePath+".tmp", 0o700); err != nil {
		t.Fatalf("block state temp path: %v", err)
	}

	err = service.failIndexOperation(context.Background(), op, "reindex", time.Now().Add(-time.Second), errors.New("source is unavailable"))
	if err == nil {
		t.Fatal("expected failure persistence error")
	}

	current, err := service.findDocument("kb-1", previous.ID)
	if err != nil {
		t.Fatalf("load rolled back failed document: %v", err)
	}
	if current.Status != previous.Status || current.IndexFence != previous.IndexFence || current.IndexRunID != previous.IndexRunID || current.IndexError != previous.IndexError || current.IndexOperationFence != "" || current.IndexOperationOwner != "" || current.IndexOperationAttempt != 0 {
		t.Fatalf("expected original visible state with cleared marker, got %+v", current)
	}
	if got := len(service.ListKnowledgeBases()[0].IndexHistory); got != 0 {
		t.Fatalf("expected failed transition not to leave index history, got %d entries", got)
	}
}

func TestIndexOperationRejectsExpiredLeaseAfterTakeover(t *testing.T) {
	store := newTestMCPJobStore(t)
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	record := testMCPJobRecord("job-index-fencing", now)
	record.Job.Status = "running"
	record.Job.Attempt = 1
	record.LeaseOwner = "worker-old"
	record.LeaseExpiresAt = now.Add(time.Second).Format(time.RFC3339Nano)
	if err := store.Create(record); err != nil {
		t.Fatalf("create leased job: %v", err)
	}

	previous := model.Document{
		ID:              "doc-fenced",
		KnowledgeBaseID: "kb-1",
		Name:            "notes.txt",
		Version:         1,
		Status:          "indexed",
		IndexFence:      "generation-old",
	}
	service := &AppService{
		mcpJobStore: store,
		state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
			"kb-1": {ID: "kb-1", Documents: []model.Document{previous}},
		}},
	}
	oldContext := context.WithValue(context.Background(), mcpJobLeaseContextKey{}, mcpJobExecution{
		JobID: record.Job.ID,
		Lease: mcpJobLease{Owner: record.LeaseOwner, Attempt: record.Job.Attempt},
	})
	oldOperation, err := service.beginIndexOperation(oldContext, previous)
	if err != nil {
		t.Fatalf("begin old worker index operation: %v", err)
	}

	now = now.Add(2 * time.Second)
	claimed, ok, err := store.Claim(record.Job.ID, "worker-new", time.Minute, now)
	if err != nil || !ok {
		t.Fatalf("take over expired lease: claimed=%t err=%v", ok, err)
	}
	if claimed.Job.Attempt != 2 || claimed.LeaseOwner != "worker-new" {
		t.Fatalf("expected takeover to advance lease attempt, got %+v", claimed)
	}

	current, err := service.findDocument("kb-1", previous.ID)
	if err != nil {
		t.Fatalf("load document for replacement worker: %v", err)
	}
	newContext := context.WithValue(context.Background(), mcpJobLeaseContextKey{}, mcpJobExecution{
		JobID: record.Job.ID,
		Lease: mcpJobLease{Owner: claimed.LeaseOwner, Attempt: claimed.Job.Attempt},
	})
	newOperation, err := service.beginIndexOperation(newContext, current)
	if err != nil {
		t.Fatalf("begin replacement worker index operation: %v", err)
	}
	if err := service.ensureIndexOperationActive(oldContext, oldOperation); !errors.Is(err, ErrMCPJobLeaseLost) {
		t.Fatalf("expected old worker lease to be fenced, got %v", err)
	}
	if _, err := service.commitIndexOperation(oldContext, oldOperation, model.Document{ID: previous.ID, KnowledgeBaseID: previous.KnowledgeBaseID}, "reindex", time.Now()); !errors.Is(err, ErrMCPJobLeaseLost) {
		t.Fatalf("expected old worker commit to be rejected, got %v", err)
	}

	current, err = service.findDocument("kb-1", previous.ID)
	if err != nil {
		t.Fatalf("load current fenced document: %v", err)
	}
	if current.IndexOperationFence != newOperation.Fence || current.IndexOperationOwner != "worker-new" || current.IndexOperationAttempt != 2 {
		t.Fatalf("expected replacement worker marker to remain authoritative, got %+v", current)
	}
}

func TestAbortPartialQdrantGenerationDeletesOnlyWrittenGeneration(t *testing.T) {
	var upsertCalls int
	var deleteBody []byte
	qdrantServer := newIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/collections/kb-1":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/kb-1/points/scroll":
			_, _ = w.Write([]byte(`{"result":{"points":[{"id":9001,"payload":{"index_fence":"generation-old"}}],"next_page_offset":null}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/kb-1/points":
			upsertCalls++
			if upsertCalls == 1 {
				_, _ = w.Write([]byte(`{}`))
				return
			}
			http.Error(w, "injected partial write failure", http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/collections/kb-1/points/delete":
			deleteBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "unexpected qdrant request", http.StatusBadRequest)
		}
	}))

	service := &AppService{
		qdrant: NewQdrantService(model.ServerConfig{QdrantURL: qdrantServer.URL, QdrantVectorSize: 2}),
		state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
			"kb-1": {ID: "kb-1", Documents: []model.Document{{
				ID: "doc-1", KnowledgeBaseID: "kb-1", Version: 1, IndexFence: "generation-old",
				IndexOperationFence: "generation-current",
			}}},
		}},
	}
	operation := indexOperation{
		KnowledgeBaseID: "kb-1",
		DocumentID:      "doc-1",
		Fence:           "generation-current",
		PreviousFence:   "generation-old",
		ExpectedVersion: 1,
		Managed:         true,
	}
	chunks := make([]DocumentChunk, 101)
	vectors := make([][]float64, len(chunks))
	for index := range chunks {
		chunks[index] = DocumentChunk{
			ID:              fmt.Sprintf("chunk-%03d", index),
			KnowledgeBaseID: "kb-1",
			DocumentID:      "doc-1",
			DocumentName:    "notes.txt",
			Text:            fmt.Sprintf("当前代内容 %d", index),
		}
		vectors[index] = []float64{1, 0}
	}

	receipt, err := service.replaceDocumentChunksWithContextReceipt(context.Background(), operation, chunks, vectors)
	if err == nil {
		t.Fatal("expected injected partial qdrant write failure")
	}
	if len(receipt.WrittenPointIDs) != len(chunks) {
		t.Fatalf("expected receipt to retain every intended current point for cleanup, got %d", len(receipt.WrittenPointIDs))
	}
	if err := service.abortIndexGeneration(context.Background(), receipt); err != nil {
		t.Fatalf("abort partially written generation: %v", err)
	}
	if upsertCalls != 3 {
		t.Fatalf("expected one successful batch and two failed attempts including legacy fallback, got %d", upsertCalls)
	}
	expectedCurrentPointID := qdrantPointID(indexedChunkID(DocumentChunk{ID: chunks[0].ID, IndexFence: operation.Fence}))
	if !strings.Contains(string(deleteBody), fmt.Sprintf("%d", expectedCurrentPointID)) {
		t.Fatalf("expected current generation point in cleanup request, got %s", deleteBody)
	}
	if strings.Contains(string(deleteBody), "9001") {
		t.Fatalf("expected previous visible point to survive abort cleanup, got %s", deleteBody)
	}
}
