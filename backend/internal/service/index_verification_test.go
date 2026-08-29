package service

import (
	"path/filepath"
	"testing"

	"ai-localbase/internal/model"
)

func TestVerifyIndexedDocumentUsesSnapshotWhenSourceIsMissing(t *testing.T) {
	content := "示例机构成立于1898年，位于示例城市。"
	document := model.Document{
		ID:                      "doc-1",
		KnowledgeBaseID:         "kb-1",
		Name:                    "institution.md",
		Path:                    filepath.Join(t.TempDir(), "missing.md"),
		Status:                  "indexed",
		IndexVersion:            currentIndexVersion,
		IndexedContentAvailable: true,
		IndexedContentChars:     len([]rune(content)),
	}
	rag := NewRagService()
	document.ChunkCount = len(rag.BuildDocumentChunks(document, content))
	store := NewIndexedContentStore(t.TempDir())
	if err := store.Put(document, content, nil); err != nil {
		t.Fatalf("store indexed content: %v", err)
	}
	appService := &AppService{
		state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
			"kb-1": {ID: "kb-1", Documents: []model.Document{document}},
		}},
		rag:                 rag,
		indexedContentStore: store,
	}

	verification, err := appService.VerifyIndexedDocument("kb-1", "doc-1")
	if err != nil {
		t.Fatalf("verify indexed document: %v", err)
	}
	if !verification.Valid || verification.Status != "healthy" {
		t.Fatalf("expected healthy snapshot verification, got %+v", verification)
	}
	if verification.ContentSource != "indexed" || !verification.SnapshotAvailable {
		t.Fatalf("expected indexed snapshot source, got %+v", verification)
	}
	if verification.EvidenceLocatedCount != verification.ChunkCount || verification.EvidenceMissingCount != 0 {
		t.Fatalf("expected every chunk to have evidence coordinates, got %+v", verification)
	}
}

func TestVerifyIndexedDocumentDetectsSnapshotAndEvidenceDrift(t *testing.T) {
	content := "统计摘要：示例表共有1条数据记录。\n第2行：姓名：成员甲。"
	document := model.Document{
		ID:                      "doc-1",
		KnowledgeBaseID:         "kb-1",
		Name:                    "records.csv",
		Path:                    filepath.Join(t.TempDir(), "missing.csv"),
		Status:                  "indexed",
		IndexVersion:            currentIndexVersion - 1,
		IndexedContentAvailable: true,
		IndexedContentChars:     1,
		IndexedTablesCount:      2,
		ChunkCount:              99,
	}
	store := NewIndexedContentStore(t.TempDir())
	if err := store.Put(document, content, []model.IndexedTable{{FileName: "records.csv", Headers: []string{"姓名"}}}); err != nil {
		t.Fatalf("store indexed content: %v", err)
	}
	appService := &AppService{
		state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
			"kb-1": {ID: "kb-1", Documents: []model.Document{document}},
		}},
		rag:                 NewRagService(),
		indexedContentStore: store,
	}

	verification, err := appService.VerifyIndexedDocument("kb-1", "doc-1")
	if err != nil {
		t.Fatalf("verify indexed document: %v", err)
	}
	if verification.Valid || verification.Status != "attention" {
		t.Fatalf("expected drift to require attention, got %+v", verification)
	}
	for _, issue := range verification.Issues {
		if issue == "index_version_outdated" {
			return
		}
	}
	t.Fatalf("expected index version issue, got %+v", verification.Issues)
}
