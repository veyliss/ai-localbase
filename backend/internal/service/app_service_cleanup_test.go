package service

import (
	"path/filepath"
	"strings"
	"testing"

	"ai-localbase/internal/model"
)

func TestDeleteKnowledgeBaseCleansIndexedContentWhenQdrantDeleteFails(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	indexedContentDir := t.TempDir()
	config := model.ServerConfig{
		StateFile:              statePath,
		IndexedContentDir:      indexedContentDir,
		QdrantURL:              "http://127.0.0.1:1",
		QdrantTimeoutSeconds:   1,
		QdrantCollectionPrefix: "kb-",
	}
	service := NewAppService(NewQdrantService(config), NewAppStateStore(statePath), nil, config)
	service.state.KnowledgeBases = map[string]model.KnowledgeBase{
		"kb-cleanup": {
			ID: "kb-cleanup",
			Documents: []model.Document{{
				ID:              "doc-cleanup",
				KnowledgeBaseID: "kb-cleanup",
				Name:            "公开合成文档.md",
			}},
		},
	}
	document := service.state.KnowledgeBases["kb-cleanup"].Documents[0]
	if err := service.indexedContentStore.Put(document, "公开索引快照", nil); err != nil {
		t.Fatalf("put indexed content: %v", err)
	}

	if _, err := service.DeleteKnowledgeBase("kb-cleanup"); err == nil || !strings.Contains(err.Error(), "delete qdrant collection") {
		t.Fatalf("expected qdrant deletion error, got %v", err)
	}
	if _, found, err := service.indexedContentStore.Load(document); err != nil || found {
		t.Fatalf("expected indexed content cleanup despite qdrant error, found=%v err=%v", found, err)
	}
}
