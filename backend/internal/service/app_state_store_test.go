package service

import (
	"os"
	"path/filepath"
	"testing"

	"ai-localbase/internal/model"
)

func TestAppStateStoreSaveAndLoad(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "app-state.json")
	store := NewAppStateStore(statePath)

	state := persistentAppState{
		Config: model.AppConfig{
			Chat: model.ChatConfig{
				Provider:    "ollama",
				BaseURL:     "http://example.invalid/v1",
				Model:       "chat-model-a",
				Temperature: 0.5,
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
					ID:   "doc-1",
					Name: "demo.md",
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
	if len(loaded.KnowledgeBases["kb-1"].Documents) != 1 {
		t.Fatalf("expected persisted documents, got %d", len(loaded.KnowledgeBases["kb-1"].Documents))
	}
	if loaded.EvalDatasets["eval-1"].Count != 1 {
		t.Fatalf("expected persisted eval dataset, got %#v", loaded.EvalDatasets["eval-1"])
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
