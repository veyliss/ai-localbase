package service

import (
	"os"
	"path/filepath"
	"testing"

	"ai-localbase/internal/model"
)

func TestIndexedContentStoreRoundTripAndDelete(t *testing.T) {
	root := t.TempDir()
	store := NewIndexedContentStore(root)
	document := model.Document{KnowledgeBaseID: "kb-public", ID: "doc-public", Name: "guide.md"}
	tables := []model.IndexedTable{{
		FileName: "records.csv",
		Headers:  []string{"名称", "数量"},
		Rows:     []model.IndexedTableRow{{Number: 2, Values: []string{"示例项目", "3"}}},
	}}

	if err := store.Put(document, "索引后的完整内容。", tables); err != nil {
		t.Fatalf("put indexed content: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read indexed content directory: %v", err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".json" {
		t.Fatalf("expected one json artifact, got %#v", entries)
	}

	loaded, found, err := store.Load(document)
	if err != nil || !found {
		t.Fatalf("load indexed content: found=%v err=%v", found, err)
	}
	if loaded.Content != "索引后的完整内容。" || len(loaded.Tables) != 1 || loaded.Tables[0].Rows[0].Values[0] != "示例项目" {
		t.Fatalf("unexpected indexed artifact: %#v", loaded)
	}

	if err := store.Delete(document.KnowledgeBaseID, document.ID); err != nil {
		t.Fatalf("delete indexed content: %v", err)
	}
	if _, found, err := store.Load(document); err != nil || found {
		t.Fatalf("expected deleted artifact, found=%v err=%v", found, err)
	}
}

func TestIndexedContentStoreSeparatesIndexGenerations(t *testing.T) {
	store := NewIndexedContentStore(t.TempDir())
	legacy := model.Document{KnowledgeBaseID: "kb-public", ID: "doc-generations", Name: "guide.md"}
	fenced := legacy
	fenced.IndexFence = "mcp:job-1:2"
	if err := store.Put(legacy, "旧代内容", nil); err != nil {
		t.Fatalf("put legacy indexed content: %v", err)
	}
	if err := store.Put(fenced, "新代内容", nil); err != nil {
		t.Fatalf("put fenced indexed content: %v", err)
	}
	loaded, found, err := store.Load(fenced)
	if err != nil || !found || loaded.Content != "新代内容" || loaded.IndexFence != fenced.IndexFence {
		t.Fatalf("expected fenced generation to load independently, found=%t err=%v artifact=%+v", found, err, loaded)
	}
	loaded, found, err = store.Load(legacy)
	if err != nil || !found || loaded.Content != "旧代内容" {
		t.Fatalf("expected legacy generation to remain readable, found=%t err=%v artifact=%+v", found, err, loaded)
	}
	if err := store.DeleteGeneration(legacy.KnowledgeBaseID, legacy.ID, fenced.IndexFence); err != nil {
		t.Fatalf("delete indexed generations: %v", err)
	}
	if _, found, err := store.Load(fenced); err != nil || found {
		t.Fatalf("expected fenced generation to be deleted, found=%t err=%v", found, err)
	}
}
