package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-localbase/eval/offline"
	"ai-localbase/internal/model"
	"ai-localbase/internal/service"
)

func TestResolveEvalFixtureManifestPathAutoDiscoversPublicFixture(t *testing.T) {
	root := t.TempDir()
	datasetPath := filepath.Join(root, "eval", "data", "ground_truth_v1.small.json")
	manifestPath := filepath.Join(root, "eval", "fixtures", "public-v1", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(datasetPath), 0o700); err != nil {
		t.Fatalf("create dataset directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	resolved, err := resolveEvalFixtureManifestPath("auto", datasetPath)
	if err != nil {
		t.Fatalf("resolve manifest: %v", err)
	}
	if resolved != manifestPath {
		t.Fatalf("expected %s, got %s", manifestPath, resolved)
	}
	resolved, err = resolveEvalFixtureManifestPath("none", datasetPath)
	if err != nil || resolved != "" {
		t.Fatalf("expected fixture discovery to be disabled, got path=%q err=%v", resolved, err)
	}
}

func TestInspectEvalFixtureIndexAcceptsMatchingIndexedDocument(t *testing.T) {
	_, manifestPath, manifest, dataset, expectedChecksum := writeEvalFixture(t)

	qdrantServer := newFixtureScrollServer(t, `{"result":{"points":[{"id":"point-1","payload":{"document_id":"doc-1","chunk_id":"doc-1-chunk-0","text":"示例机构成立于1898年。","chunk_index":0}}],"next_page_offset":null}}`)
	qdrant := service.NewQdrantService(model.ServerConfig{
		QdrantURL:              qdrantServer.URL,
		QdrantCollectionPrefix: "kb_",
		QdrantVectorSize:       4,
	})

	check := inspectEvalFixtureIndex(
		t.Context(),
		manifestPath,
		manifest,
		dataset,
		map[string]model.KnowledgeBase{
			"kb-1": {
				ID: "kb-1",
				Documents: []model.Document{{
					ID:           "doc-1",
					Name:         "facts.md",
					Checksum:     expectedChecksum,
					Status:       "indexed",
					ChunkCount:   1,
					IndexVersion: 2,
				}},
			},
		},
		"",
		qdrant,
	)
	if len(check.Issues) != 0 {
		t.Fatalf("expected matching fixture index to pass, got %#v", check.Issues)
	}
	if check.KnowledgeBaseID != "kb-1" {
		t.Fatalf("expected fixture to resolve kb-1, got %q", check.KnowledgeBaseID)
	}
	mapped := check.SourceMappings["case-1"]
	if len(mapped) != 1 || mapped[0].KnowledgeBaseID != "kb-1" || mapped[0].DocumentID != "doc-1" || mapped[0].ChunkID != "doc-1-chunk-0" {
		t.Fatalf("expected fixture source ID mapping, got %#v", mapped)
	}
}

func TestApplyEvalFixtureSourceMappingsReplacesOnlyFixtureSources(t *testing.T) {
	dataset := &offline.Dataset{Cases: []offline.GroundTruthCase{
		{
			ID: "fixture-case",
			SourceDocuments: []offline.SourceDocument{{
				KnowledgeBaseID: "kb-old",
				DocumentID:      "doc-old",
				ChunkID:         "chunk-old",
			}},
		},
		{
			ID: "snippet-case",
		},
	}}
	mapped := applyEvalFixtureSourceMappings(dataset, map[string][]offline.SourceDocument{
		"fixture-case": {{
			KnowledgeBaseID: "kb-current",
			DocumentID:      "doc-current",
			ChunkID:         "chunk-current",
		}},
		"snippet-case": {{
			KnowledgeBaseID: "kb-current",
			DocumentID:      "doc-current",
		}},
	})
	if mapped != 1 {
		t.Fatalf("expected one source mapping, got %d", mapped)
	}
	if got := dataset.Cases[0].SourceDocuments[0]; got.DocumentID != "doc-current" || got.ChunkID != "chunk-current" {
		t.Fatalf("expected fixture source to be replaced, got %#v", got)
	}
	if len(dataset.Cases[1].SourceDocuments) != 0 {
		t.Fatalf("expected snippet-only case to remain unchanged, got %#v", dataset.Cases[1].SourceDocuments)
	}
}

func TestInspectEvalFixtureIndexRejectsStaleDocument(t *testing.T) {
	_, manifestPath, manifest, dataset, _ := writeEvalFixture(t)
	qdrant := service.NewQdrantService(model.ServerConfig{QdrantURL: "http://127.0.0.1:1", QdrantCollectionPrefix: "kb_"})
	check := inspectEvalFixtureIndex(
		t.Context(),
		manifestPath,
		manifest,
		dataset,
		map[string]model.KnowledgeBase{
			"kb-1": {
				ID: "kb-1",
				Documents: []model.Document{{
					ID:       "doc-1",
					Name:     "facts.md",
					Checksum: strings.Repeat("0", 64),
					Status:   "indexed",
				}},
			},
		},
		"kb-1",
		qdrant,
	)
	if len(check.Issues) != 1 || !strings.Contains(check.Issues[0].Reason, "版本") {
		t.Fatalf("expected stale fixture issue, got %#v", check.Issues)
	}
}

func TestInspectEvalFixtureIndexRejectsMissingQdrantPoints(t *testing.T) {
	_, manifestPath, manifest, dataset, expectedChecksum := writeEvalFixture(t)
	qdrantServer := newFixtureScrollServer(t, `{"result":{"points":[],"next_page_offset":null}}`)
	qdrant := service.NewQdrantService(model.ServerConfig{
		QdrantURL:              qdrantServer.URL,
		QdrantCollectionPrefix: "kb_",
		QdrantVectorSize:       4,
	})
	check := inspectEvalFixtureIndex(
		t.Context(),
		manifestPath,
		manifest,
		dataset,
		map[string]model.KnowledgeBase{
			"kb-1": {
				ID: "kb-1",
				Documents: []model.Document{{
					ID:           "doc-1",
					Name:         "facts.md",
					Checksum:     expectedChecksum,
					Status:       "indexed",
					ChunkCount:   1,
					IndexVersion: 2,
				}},
			},
		},
		"kb-1",
		qdrant,
	)
	if len(check.Issues) != 1 || !strings.Contains(check.Issues[0].Reason, "没有对应索引点") {
		t.Fatalf("expected missing point issue, got %#v", check.Issues)
	}
}

func writeEvalFixture(t *testing.T) (string, string, *offline.FixtureManifest, *offline.Dataset, string) {
	t.Helper()
	root := t.TempDir()
	fixturePath := filepath.Join(root, "facts.md")
	if err := os.WriteFile(fixturePath, []byte("### 事实 {#fact}\n\n示例机构成立于1898年。\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	manifest := &offline.FixtureManifest{
		Version:   "test-v1",
		Documents: []offline.FixtureDocument{{DocumentKey: "facts", Path: "facts.md"}},
		Cases:     []offline.FixtureCase{{ID: "case-1", DocumentKey: "facts", Section: "fact"}},
	}
	dataset := &offline.Dataset{Cases: []offline.GroundTruthCase{{
		ID:             "case-1",
		Question:       "机构何时成立？",
		Answer:         "示例机构成立于1898年。",
		AnswerSnippets: []string{"1898年"},
		SourceDocuments: []offline.SourceDocument{{
			KnowledgeBaseID: "kb-old",
			DocumentID:      "doc-old",
			ChunkID:         "chunk-old",
		}},
		AnswerType: "extractive",
		Difficulty: "easy",
	}}}
	if err := os.WriteFile(manifestPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write manifest placeholder: %v", err)
	}
	checksum, err := offline.FileSHA256(fixturePath)
	if err != nil {
		t.Fatalf("checksum fixture: %v", err)
	}
	return fixturePath, manifestPath, manifest, dataset, checksum
}

func newFixtureScrollServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/points/scroll") {
			t.Fatalf("unexpected qdrant request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server
}
