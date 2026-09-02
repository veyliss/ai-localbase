package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ai-localbase/internal/model"
)

func TestAppStateStoreSaveAndLoad(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "app-state.json")
	store := NewAppStateStore(statePath)

	state := persistentAppState{
		Config: model.AppConfig{
			Chat: model.ChatConfig{
				Provider:             "ollama",
				BaseURL:              "http://example.invalid/v1",
				Model:                "chat-model-a",
				Temperature:          0.5,
				KnowledgeTemperature: 0.2,
			},
			Embedding: model.EmbeddingConfig{
				Provider: "ollama",
				BaseURL:  "http://example.invalid/v1",
				Model:    "embed-model-a",
			},
		},
		KnowledgeBases: map[string]model.KnowledgeBase{
			"kb-1": {
				ID:        "kb-1",
				Name:      "示例知识库",
				CreatedAt: "2026-03-12T00:00:00Z",
				Documents: []model.Document{{
					ID:           "doc-1",
					Name:         "demo.md",
					Path:         "/srv/ai-localbase/uploads/doc-1_demo.md",
					IndexVersion: currentIndexVersion,
					IndexFence:   "mcp:job-state:1",
				}},
			},
		},
		EvalDatasets: map[string]model.EvalDataset{
			"eval-1": {
				ID:              "eval-1",
				Name:            "示例评估集",
				KnowledgeBaseID: "kb-1",
				Count:           1,
				DocumentCount:   1,
				CreatedAt:       "2026-03-12T00:00:01Z",
				Items: []model.EvalGroundTruthCase{{
					ID:       "case-1",
					Question: "示例问题？",
					Answer:   "示例答案。",
				}},
			},
		},
	}

	if err := store.Save(state); err != nil {
		t.Fatalf("save app state: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load app state: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected loaded state")
	}
	if loaded.Config.Chat.Model != "chat-model-a" {
		t.Fatalf("expected chat model chat-model-a, got %s", loaded.Config.Chat.Model)
	}
	if loaded.Config.Chat.KnowledgeTemperature != 0.2 {
		t.Fatalf("expected knowledge temperature 0.2, got %v", loaded.Config.Chat.KnowledgeTemperature)
	}
	if len(loaded.KnowledgeBases["kb-1"].Documents) != 1 {
		t.Fatalf("expected persisted documents, got %d", len(loaded.KnowledgeBases["kb-1"].Documents))
	}
	if got := loaded.KnowledgeBases["kb-1"].Documents[0].Path; got != "/srv/ai-localbase/uploads/doc-1_demo.md" {
		t.Fatalf("expected persisted document path, got %q", got)
	}
	if got := loaded.KnowledgeBases["kb-1"].Documents[0].IndexVersion; got != currentIndexVersion {
		t.Fatalf("expected persisted index version %d, got %d", currentIndexVersion, got)
	}
	if got := loaded.KnowledgeBases["kb-1"].Documents[0].IndexFence; got != "mcp:job-state:1" {
		t.Fatalf("expected persisted index fence, got %q", got)
	}
	publicJSON, err := json.Marshal(loaded.KnowledgeBases["kb-1"].Documents[0])
	if err != nil {
		t.Fatalf("marshal public document: %v", err)
	}
	if strings.Contains(string(publicJSON), "doc-1_demo.md") {
		t.Fatalf("document path must not be exposed in public JSON: %s", publicJSON)
	}
	if loaded.EvalDatasets["eval-1"].Count != 1 {
		t.Fatalf("expected persisted eval dataset, got %#v", loaded.EvalDatasets["eval-1"])
	}
}

func TestAppStateStoreSerializesConcurrentSaves(t *testing.T) {
	store := NewAppStateStore(filepath.Join(t.TempDir(), "app-state.json"))
	const writers = 12

	var wg sync.WaitGroup
	for index := 0; index < writers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			state := persistentAppState{
				KnowledgeBases: map[string]model.KnowledgeBase{
					"kb": {ID: "kb", Name: "writer", Documents: []model.Document{{ID: string(rune('a' + index))}}},
				},
			}
			if err := store.Save(state); err != nil {
				t.Errorf("concurrent save %d failed: %v", index, err)
			}
		}(index)
	}
	wg.Wait()

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load state after concurrent saves: %v", err)
	}
	if loaded == nil || len(loaded.KnowledgeBases["kb"].Documents) != 1 {
		t.Fatalf("expected a complete final state, got %#v", loaded)
	}
}

func TestAppStateStoreLoadMissingFile(t *testing.T) {
	store := NewAppStateStore(filepath.Join(t.TempDir(), "missing.json"))
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load missing app state: %v", err)
	}
	if loaded != nil {
		t.Fatalf("expected nil state for missing file, got %#v", loaded)
	}
}

func TestAppStateStoreMigratesLegacyKnowledgeBaseGovernance(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "legacy-state.json")
	legacy := `{
  "knowledgeBases": {
    "kb-legacy": {
      "name": "旧知识库",
      "documents": [{
        "id": "doc-legacy",
        "name": "notes.txt",
        "status": "indexed",
        "indexError": "open /Users/private/notes.txt: no such file"
      }],
      "indexHistory": [{
        "id": "run-legacy",
        "documentId": "doc-legacy",
        "status": "failed",
        "error": "qdrant request failed at http://qdrant:6333/collections/private"
      }]
    }
  }
}`
	if err := os.WriteFile(statePath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	loaded, err := NewAppStateStore(statePath).Load()
	if err != nil {
		t.Fatalf("load legacy state: %v", err)
	}
	kb := loaded.KnowledgeBases["kb-legacy"]
	if kb.ID != "kb-legacy" || kb.CurrentIndexVersion != currentIndexVersion {
		t.Fatalf("expected migrated knowledge base identity/version, got %+v", kb)
	}
	if len(kb.Documents) != 1 {
		t.Fatalf("expected one migrated document, got %d", len(kb.Documents))
	}
	document := kb.Documents[0]
	if document.KnowledgeBaseID != kb.ID || document.Source != "legacy" || document.Version != 1 {
		t.Fatalf("expected migrated document governance, got %+v", document)
	}
	if document.IndexErrorCode != indexErrorSourceMissing || document.IndexError != publicIndexError(indexErrorSourceMissing) {
		t.Fatalf("expected classified public error, got %+v", document)
	}
	if len(kb.IndexHistory) != 1 || kb.IndexHistory[0].Error == "qdrant request failed at http://qdrant:6333/collections/private" {
		t.Fatalf("expected migrated history error to be sanitized, got %+v", kb.IndexHistory)
	}
}

func TestNewAppServiceLoadsPersistedState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "persisted.json")
	store := NewAppStateStore(statePath)
	persisted := persistentAppState{
		Config: model.AppConfig{
			Chat: model.ChatConfig{
				Provider:    "ollama",
				BaseURL:     "http://chat.example.invalid/v1",
				Model:       "persisted-chat-model-a",
				Temperature: 0.3,
			},
			Embedding: model.EmbeddingConfig{
				Provider: "openai-compatible",
				BaseURL:  "http://embed.example.invalid/v1",
				Model:    "persisted-embed-model-a",
			},
		},
		KnowledgeBases: map[string]model.KnowledgeBase{
			"kb-persisted": {
				ID:          "kb-persisted",
				Name:        "示例持久化知识库",
				Description: "来自示例磁盘状态",
				CreatedAt:   "2026-03-12T00:00:00Z",
			},
		},
		EvalDatasets: map[string]model.EvalDataset{
			"eval-persisted": {
				ID:              "eval-persisted",
				Name:            "示例持久化评估集",
				KnowledgeBaseID: "kb-persisted",
				Count:           1,
				DocumentCount:   1,
				CreatedAt:       "2026-03-12T00:00:01Z",
			},
		},
		EvalRuns: map[string]model.RunEvalDatasetResponse{
			"eval-run-persisted": {
				RunID:           "eval-run-persisted",
				DatasetID:       "eval-persisted",
				DatasetName:     "示例持久化评估集",
				KnowledgeBaseID: "kb-persisted",
				StartedAt:       "2026-03-12T00:00:02Z",
				Metrics:         model.EvalRunMetrics{TotalCases: 1, HitCount: 1, HitRate: 1, MRR: 1},
			},
		},
	}
	if err := store.Save(persisted); err != nil {
		t.Fatalf("save persisted state: %v", err)
	}

	service := NewAppService(nil, store, nil, model.ServerConfig{})
	config := service.GetConfig()
	if config.Chat.Model != "persisted-chat-model-a" {
		t.Fatalf("expected persisted chat model, got %s", config.Chat.Model)
	}
	if config.Chat.KnowledgeTemperature != defaultKnowledgeTemperature {
		t.Fatalf("expected legacy state to use knowledge temperature %.1f, got %v", defaultKnowledgeTemperature, config.Chat.KnowledgeTemperature)
	}

	knowledgeBases := service.ListKnowledgeBases()
	if len(knowledgeBases) != 1 || knowledgeBases[0].ID != "kb-persisted" {
		t.Fatalf("expected persisted knowledge base, got %#v", knowledgeBases)
	}
	evalDatasets := service.ListEvalDatasets("kb-persisted")
	if len(evalDatasets) != 1 || evalDatasets[0].ID != "eval-persisted" {
		t.Fatalf("expected persisted eval dataset, got %#v", evalDatasets)
	}
	evalRuns := service.ListEvalRuns("kb-persisted", "")
	if len(evalRuns) != 1 || evalRuns[0].RunID != "eval-run-persisted" {
		t.Fatalf("expected persisted eval run, got %#v", evalRuns)
	}
}

func TestNewAppServicePersistsDefaultState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "default-state.json")
	store := NewAppStateStore(statePath)

	service := NewAppService(nil, store, nil, model.ServerConfig{})
	if service == nil {
		t.Fatal("expected app service")
	}

	content, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read persisted default state: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected non-empty persisted state file")
	}
}
