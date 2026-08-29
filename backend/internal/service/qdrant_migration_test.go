package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

func TestQdrantServiceScrollPointPayloadsPreservesLargeNumericIDs(t *testing.T) {
	const largePointID = "18446744073709551614"

	var (
		mu       sync.Mutex
		requests []qdrantScrollRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/collections/source_kb-1/points/scroll" {
			t.Fatalf("unexpected qdrant request: %s %s", r.Method, r.URL.Path)
		}

		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		var request qdrantScrollRequest
		if err := decoder.Decode(&request); err != nil {
			t.Fatalf("decode scroll request: %v", err)
		}
		mu.Lock()
		requests = append(requests, request)
		requestCount := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if requestCount == 1 {
			fmt.Fprintf(w, `{"result":{"points":[{"id":%s,"payload":{"text":"first"}}],"next_page_offset":%s}}`, largePointID, largePointID)
			return
		}
		_, _ = w.Write([]byte(`{"result":{"points":[{"id":"point-2","payload":{"text":"second"}}],"next_page_offset":null}}`))
	}))
	t.Cleanup(server.Close)

	qdrant := NewQdrantService(model.ServerConfig{
		QdrantURL:              server.URL,
		QdrantCollectionPrefix: "source_",
		QdrantVectorSize:       4,
	})
	points, err := qdrant.ScrollPointPayloads(t.Context(), "kb-1")
	if err != nil {
		t.Fatalf("scroll point payloads: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 points, got %d", len(points))
	}
	pointID, ok := points[0].ID.(json.Number)
	if !ok || pointID.String() != largePointID {
		t.Fatalf("expected exact numeric point id %s, got %#v", largePointID, points[0].ID)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected 2 scroll requests, got %d", len(requests))
	}
	offset, ok := requests[1].Offset.(json.Number)
	if !ok || offset.String() != largePointID {
		t.Fatalf("expected exact next-page offset %s, got %#v", largePointID, requests[1].Offset)
	}
}

func TestMigrateQdrantPayloadsReembedsAndPreservesPayload(t *testing.T) {
	const largePointID = "18446744073709551614"

	embeddingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/embed" {
			t.Fatalf("unexpected embedding request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3,0.4]]}`))
	}))
	t.Cleanup(embeddingServer.Close)

	var upserted qdrantPointUpsertRequest
	qdrantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/collections/source_kb-1/points/scroll":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"result":{"points":[{"id":%s,"payload":{"knowledge_base_id":"kb-1","document_id":"doc-1","text":"source text"}}],"next_page_offset":null}}`, largePointID)
		case r.Method == http.MethodPut && r.URL.Path == "/collections/target_kb-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/target_kb-1/points":
			decoder := json.NewDecoder(r.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&upserted); err != nil {
				t.Fatalf("decode upsert request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":true}`))
		default:
			t.Fatalf("unexpected qdrant request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(qdrantServer.Close)

	baseConfig := model.ServerConfig{
		QdrantURL:        qdrantServer.URL,
		QdrantVectorSize: 4,
	}
	sourceConfig := baseConfig
	sourceConfig.QdrantCollectionPrefix = "source_"
	targetConfig := baseConfig
	targetConfig.QdrantCollectionPrefix = "target_"

	count, err := MigrateQdrantPayloads(
		t.Context(),
		NewQdrantService(sourceConfig),
		NewQdrantService(targetConfig),
		NewRagService(),
		model.EmbeddingModelConfig{
			Provider: "ollama",
			BaseURL:  embeddingServer.URL,
			Model:    "test-embedding",
		},
		"kb-1",
	)
	if err != nil {
		t.Fatalf("migrate qdrant payloads: %v", err)
	}
	if count != 1 || len(upserted.Points) != 1 {
		t.Fatalf("expected one migrated point, count=%d points=%d", count, len(upserted.Points))
	}

	point := upserted.Points[0]
	pointID, ok := point.ID.(json.Number)
	if !ok || pointID.String() != largePointID {
		t.Fatalf("expected exact migrated point id %s, got %#v", largePointID, point.ID)
	}
	if point.Payload["document_id"] != "doc-1" || point.Payload["text"] != "source text" {
		t.Fatalf("expected source payload to be preserved, got %#v", point.Payload)
	}
	vectors, ok := point.Vector.(map[string]any)
	if !ok {
		t.Fatalf("expected named vector payload, got %#v", point.Vector)
	}
	dense, ok := vectors[qdrantDenseVectorName].([]any)
	if !ok || len(dense) != 4 {
		t.Fatalf("expected 4-dimensional dense vector, got %#v", vectors[qdrantDenseVectorName])
	}
	if _, ok := vectors[qdrantSparseVectorName].(map[string]any); !ok {
		t.Fatalf("expected sparse vector to be rebuilt, got %#v", vectors[qdrantSparseVectorName])
	}
}

func TestMigrateQdrantPayloadsRejectsCollectionsWithoutText(t *testing.T) {
	qdrantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"points":[{"id":1,"payload":{"document_id":"doc-1"}}],"next_page_offset":null}}`))
	}))
	t.Cleanup(qdrantServer.Close)

	config := model.ServerConfig{
		QdrantURL:              qdrantServer.URL,
		QdrantCollectionPrefix: "source_",
		QdrantVectorSize:       4,
	}
	_, err := MigrateQdrantPayloads(
		t.Context(),
		NewQdrantService(config),
		NewQdrantService(config),
		NewRagService(),
		model.EmbeddingModelConfig{},
		"kb-1",
	)
	if err == nil || !strings.Contains(err.Error(), "contains no text payloads") {
		t.Fatalf("expected missing text payload error, got %v", err)
	}
}

func TestMigrateQdrantPayloadsWithOptionsDryRunScansWithoutWriting(t *testing.T) {
	var embeddingCalls, targetWrites int
	embeddingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embeddingCalls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(embeddingServer.Close)

	qdrantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/collections/target_") {
			targetWrites++
			t.Errorf("dry-run must not write target collection: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"points":[{"id":"row-1","payload":{"text":"姓名：成员甲","chunk_kind":"structured_row","index_version":2}},{"id":"empty","payload":{}}],"next_page_offset":null}}`))
	}))
	t.Cleanup(qdrantServer.Close)

	config := model.ServerConfig{QdrantURL: qdrantServer.URL, QdrantVectorSize: 4, QdrantCollectionPrefix: "source_"}
	options := DefaultQdrantMigrationOptions()
	options.DryRun = true
	options.RetryBackoff = time.Millisecond
	result, err := MigrateQdrantPayloadsWithOptions(
		t.Context(),
		NewQdrantService(config),
		NewQdrantService(func() model.ServerConfig { copy := config; copy.QdrantCollectionPrefix = "target_"; return copy }()),
		NewRagService(),
		model.EmbeddingModelConfig{Provider: "ollama", BaseURL: embeddingServer.URL, Model: "test-embedding"},
		"kb-1",
		options,
	)
	if err != nil {
		t.Fatalf("dry-run migration: %v", err)
	}
	if result.Status != "dry_run" || result.SourcePointCount != 2 || result.TextPointCount != 1 || result.SkippedPointCount != 1 {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}
	if result.StructuredRowCount != 1 || result.IndexVersionCounts["2"] != 1 || result.IssueCounts["missing_text_payload"] != 1 {
		t.Fatalf("expected structured/index diagnostics, got %+v", result)
	}
	if embeddingCalls != 0 || targetWrites != 0 {
		t.Fatalf("dry-run performed writes: embeddings=%d targetWrites=%d", embeddingCalls, targetWrites)
	}

	// A dry-run is useful for auditing a legacy deployment before the target
	// service or embedding endpoint has been configured, so neither dependency
	// should be required during the scan-only path.
	if _, err := MigrateQdrantPayloadsWithOptions(
		t.Context(),
		NewQdrantService(config),
		nil,
		nil,
		model.EmbeddingModelConfig{},
		"kb-1",
		options,
	); err != nil {
		t.Fatalf("dry-run should not require target or embedding service: %v", err)
	}
}

func TestMigrateQdrantPayloadsWithOptionsBatchesRetriesAndValidates(t *testing.T) {
	embeddingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode embedding request: %v", err)
		}
		vectors := make([][]float64, len(request.Input))
		for index := range vectors {
			vectors[index] = []float64{0.1, 0.2, 0.3, 0.4}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
	}))
	t.Cleanup(embeddingServer.Close)

	var mu sync.Mutex
	upsertAttempts := 0
	upserted := make(map[string]string)
	qdrantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/collections/source_kb-1/points/scroll":
			_, _ = w.Write([]byte(`{"result":{"points":[{"id":"p-1","payload":{"text":"第一条","chunk_kind":"text"}},{"id":"p-2","payload":{"text":"第二条","chunk_kind":"structured_summary"}},{"id":"p-3","payload":{"text":"第三条","chunk_kind":"structured_row"}}],"next_page_offset":null}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/target_kb-1":
			_, _ = w.Write([]byte(`{"result":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/target_kb-1/points":
			mu.Lock()
			upsertAttempts++
			attempt := upsertAttempts
			mu.Unlock()
			if attempt == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":{"error":"temporary"}}`))
				return
			}
			var request qdrantPointUpsertRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode upsert request: %v", err)
			}
			mu.Lock()
			for _, point := range request.Points {
				upserted[fmt.Sprint(point.ID)] = payloadString(point.Payload, "text", "")
			}
			mu.Unlock()
			_, _ = w.Write([]byte(`{"result":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/collections/target_kb-1/points/scroll":
			mu.Lock()
			points := make([]map[string]any, 0, len(upserted))
			for id, text := range upserted {
				points = append(points, map[string]any{"id": id, "payload": map[string]any{"text": text}})
			}
			mu.Unlock()
			_, _ = json.Marshal(points)
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"points": points, "next_page_offset": nil}})
		default:
			t.Fatalf("unexpected qdrant request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(qdrantServer.Close)

	base := model.ServerConfig{QdrantURL: qdrantServer.URL, QdrantVectorSize: 4}
	sourceConfig := base
	sourceConfig.QdrantCollectionPrefix = "source_"
	targetConfig := base
	targetConfig.QdrantCollectionPrefix = "target_"
	options := DefaultQdrantMigrationOptions()
	options.BatchSize = 2
	options.MaxAttempts = 2
	options.RetryBackoff = time.Millisecond

	result, err := MigrateQdrantPayloadsWithOptions(
		t.Context(),
		NewQdrantService(sourceConfig),
		NewQdrantService(targetConfig),
		NewRagService(),
		model.EmbeddingModelConfig{Provider: "ollama", BaseURL: embeddingServer.URL, Model: "test-embedding"},
		"kb-1",
		options,
	)
	if err != nil {
		t.Fatalf("migrate with validation: %v", err)
	}
	if result.Status != "succeeded" || result.MigratedPointCount != 3 || result.BatchCount != 2 || result.ValidatedPointCount != 3 {
		t.Fatalf("unexpected migration result: %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if upsertAttempts != 3 {
		t.Fatalf("expected one failed attempt plus two successful batches, got %d", upsertAttempts)
	}
	if len(upserted) != 3 {
		t.Fatalf("expected three idempotent point IDs, got %d", len(upserted))
	}
}
