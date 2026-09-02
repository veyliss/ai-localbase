package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

func TestIndexDocumentRejectsDuplicateChecksum(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(root, "first.txt")
	secondPath := filepath.Join(root, "second.txt")
	content := []byte("   \n")
	if err := os.WriteFile(firstPath, content, 0o600); err != nil {
		t.Fatalf("write first document: %v", err)
	}
	if err := os.WriteFile(secondPath, content, 0o600); err != nil {
		t.Fatalf("write second document: %v", err)
	}

	service := NewAppService(nil, NewAppStateStore(""), nil, model.ServerConfig{
		IndexedContentDir: filepath.Join(root, "indexed-content"),
	})
	kbID := service.ListKnowledgeBases()[0].ID
	first, err := service.IndexDocument(model.Document{
		ID:              "doc-first",
		KnowledgeBaseID: kbID,
		Name:            "first.txt",
		Path:            firstPath,
		Status:          "processing",
	})
	if err != nil {
		t.Fatalf("index first document: %v", err)
	}

	_, err = service.IndexDocument(model.Document{
		ID:              "doc-second",
		KnowledgeBaseID: kbID,
		Name:            "second.txt",
		Path:            secondPath,
		Status:          "processing",
	})
	var duplicateErr *DuplicateDocumentError
	if !errors.As(err, &duplicateErr) {
		t.Fatalf("expected duplicate document error, got %v", err)
	}
	if duplicateErr.Existing.ID != first.ID {
		t.Fatalf("expected existing document %q, got %+v", first.ID, duplicateErr.Existing)
	}
	if got := len(service.ListKnowledgeBases()[0].Documents); got != 1 {
		t.Fatalf("expected one persisted document after duplicate upload, got %d", got)
	}
}

func TestAddDocumentRejectsDuplicateChecksumAndID(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-1": {ID: "kb-1", Documents: []model.Document{}},
	}}}
	first := model.Document{ID: "doc-1", KnowledgeBaseID: "kb-1", Checksum: "ABC123"}
	if _, err := service.AddDocument("kb-1", first); err != nil {
		t.Fatalf("add first document: %v", err)
	}

	for _, duplicate := range []model.Document{
		{ID: "doc-2", KnowledgeBaseID: "kb-1", Checksum: "abc123"},
		{ID: "doc-1", KnowledgeBaseID: "kb-1", Checksum: "different"},
	} {
		_, err := service.AddDocument("kb-1", duplicate)
		var duplicateErr *DuplicateDocumentError
		if !errors.As(err, &duplicateErr) {
			t.Fatalf("expected duplicate error for %+v, got %v", duplicate, err)
		}
	}
}

func TestReserveDocumentIndexWaitsAndRechecksPersistedState(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-1": {ID: "kb-1", Documents: []model.Document{}},
	}}}
	first := model.Document{ID: "doc-1", KnowledgeBaseID: "kb-1", Checksum: "same-content"}
	second := model.Document{ID: "doc-2", KnowledgeBaseID: "kb-1", Checksum: "same-content"}
	release, err := service.reserveDocumentIndex(t.Context(), first)
	if err != nil {
		t.Fatalf("reserve first document: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := service.reserveDocumentIndex(t.Context(), second)
		result <- err
	}()

	select {
	case err := <-result:
		t.Fatalf("second reservation completed before first release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	service.state.Mu.Lock()
	kb := service.state.KnowledgeBases["kb-1"]
	kb.Documents = append(kb.Documents, first)
	service.state.KnowledgeBases["kb-1"] = kb
	service.state.Mu.Unlock()
	release()

	select {
	case err := <-result:
		var duplicateErr *DuplicateDocumentError
		if !errors.As(err, &duplicateErr) {
			t.Fatalf("expected duplicate after reservation release, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second reservation did not recheck state after release")
	}
}

func TestFindDocumentByChecksumSupportsLegacyDocumentWithoutStoredChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.txt")
	if err := os.WriteFile(path, []byte("legacy content"), 0o600); err != nil {
		t.Fatalf("write legacy document: %v", err)
	}
	checksum, err := checksumFile(path)
	if err != nil {
		t.Fatalf("calculate checksum: %v", err)
	}
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-1": {ID: "kb-1", Documents: []model.Document{{ID: "legacy", Path: path}}},
	}}}

	document, found, err := service.findDocumentByChecksum("kb-1", checksum)
	if err != nil || !found || document.ID != "legacy" {
		t.Fatalf("expected legacy document match, found=%t document=%+v err=%v", found, document, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.reserveDocumentIndex(ctx, model.Document{ID: "new", KnowledgeBaseID: "kb-1", Checksum: checksum}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled reservation, got %v", err)
	}
}

func TestReplaceDocumentChunksDeletesStaleQdrantPoints(t *testing.T) {
	var requests []string
	var deleteBody []byte
	qdrantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/collections/kb-1":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/kb-1/points/scroll":
			_, _ = w.Write([]byte(`{"result":{"points":[{"id":"old-point","payload":{}}],"next_page_offset":null}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/kb-1/points":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/kb-1/points/delete":
			deleteBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "unexpected qdrant request", http.StatusBadRequest)
		}
	}))
	t.Cleanup(qdrantServer.Close)

	service := &AppService{
		qdrant: NewQdrantService(model.ServerConfig{QdrantURL: qdrantServer.URL, QdrantVectorSize: 2}),
		state:  &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{"kb-1": {ID: "kb-1"}}},
	}
	err := service.replaceDocumentChunksWithContext(t.Context(), "kb-1", "doc-1", []DocumentChunk{{
		ID:              "new-point",
		KnowledgeBaseID: "kb-1",
		DocumentID:      "doc-1",
		DocumentName:    "notes.txt",
		Text:            "新版本内容",
	}}, [][]float64{{1, 0}})
	if err != nil {
		t.Fatalf("replace document chunks: %v", err)
	}
	if len(requests) != 4 || requests[0] != "PUT /collections/kb-1" || requests[1] != "POST /collections/kb-1/points/scroll" || requests[2] != "PUT /collections/kb-1/points" || requests[3] != "POST /collections/kb-1/points/delete" {
		t.Fatalf("expected collection, scroll, upsert, delete sequence, got %v", requests)
	}
	var deleteRequest qdrantPointDeleteRequest
	if err := json.Unmarshal(deleteBody, &deleteRequest); err != nil {
		t.Fatalf("decode delete request: %v", err)
	}
	encodedFilter, err := json.Marshal(deleteRequest.Filter)
	if err != nil {
		t.Fatalf("encode delete filter: %v", err)
	}
	if !strings.Contains(string(encodedFilter), "old-point") {
		t.Fatalf("expected stale point id in delete filter, got %s", encodedFilter)
	}
}

func TestIndexGenerationRetirementDeletesOnlyExpectedPreviousPoints(t *testing.T) {
	var deleteBody []byte
	qdrantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/collections/kb-1":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/kb-1/points/scroll":
			_, _ = w.Write([]byte(`{"result":{"points":[
				{"id":101,"payload":{"index_fence":"generation-old"}},
				{"id":202,"payload":{"index_fence":"generation-newer"}},
				{"id":303,"payload":{"index_fence":"generation-superseded"}}
			],"next_page_offset":null}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/kb-1/points":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/kb-1/points/delete":
			deleteBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "unexpected qdrant request", http.StatusBadRequest)
		}
	}))
	t.Cleanup(qdrantServer.Close)

	service := &AppService{
		qdrant: NewQdrantService(model.ServerConfig{QdrantURL: qdrantServer.URL, QdrantVectorSize: 2}),
		state:  &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{"kb-1": {ID: "kb-1"}}},
	}
	receipt, err := service.replaceDocumentChunksWithContextReceipt(t.Context(), indexOperation{
		KnowledgeBaseID: "kb-1",
		DocumentID:      "doc-1",
		Fence:           "generation-current",
		PreviousFence:   "generation-old",
		SupersededFence: "generation-superseded",
	}, []DocumentChunk{{
		ID:              "chunk-1",
		KnowledgeBaseID: "kb-1",
		DocumentID:      "doc-1",
		DocumentName:    "notes.txt",
		Text:            "当前内容",
	}}, [][]float64{{1, 0}})
	if err != nil {
		t.Fatalf("write current generation: %v", err)
	}
	if len(receipt.PreviousPointIDs) != 1 || fmt.Sprint(receipt.PreviousPointIDs[0]) != "101" {
		t.Fatalf("expected only previous generation point, got %#v", receipt.PreviousPointIDs)
	}
	if len(receipt.SupersededPointIDs) != 1 || fmt.Sprint(receipt.SupersededPointIDs[0]) != "303" {
		t.Fatalf("expected only superseded generation point, got %#v", receipt.SupersededPointIDs)
	}
	if err := service.retirePreviousGeneration(t.Context(), receipt); err != nil {
		t.Fatalf("retire previous generation: %v", err)
	}
	var deleteRequest qdrantPointDeleteRequest
	if err := json.Unmarshal(deleteBody, &deleteRequest); err != nil {
		t.Fatalf("decode retirement request: %v", err)
	}
	encoded, err := json.Marshal(deleteRequest.Filter)
	if err != nil {
		t.Fatalf("encode retirement filter: %v", err)
	}
	if !strings.Contains(string(encoded), "101") || !strings.Contains(string(encoded), "303") || strings.Contains(string(encoded), "202") || strings.Contains(string(encoded), "generation-newer") {
		t.Fatalf("expected exact previous point deletion, got %s", encoded)
	}
}

func TestDeleteDocumentKeepsStateWhenQdrantCleanupFails(t *testing.T) {
	service := &AppService{
		qdrant: NewQdrantService(model.ServerConfig{QdrantURL: "http://127.0.0.1:1", QdrantTimeoutSeconds: 1}),
		state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
			"kb-1": {ID: "kb-1", Documents: []model.Document{{
				ID: "doc-1", KnowledgeBaseID: "kb-1", Name: "notes.txt",
			}}},
		}},
	}

	_, err := service.DeleteDocument("kb-1", "doc-1")
	var cleanupErr *IndexCleanupError
	if !errors.As(err, &cleanupErr) {
		t.Fatalf("expected index cleanup error, got %v", err)
	}
	if _, err := service.findDocument("kb-1", "doc-1"); err != nil {
		t.Fatalf("expected document to remain retryable, got %v", err)
	}
}
