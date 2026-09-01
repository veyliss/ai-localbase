package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

func TestEnrichDocumentGovernanceAddsSourceVersionAndChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	content := []byte("索引治理测试内容")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write test document: %v", err)
	}

	document := enrichDocumentGovernance(model.Document{Path: path})
	if document.Source != "upload" {
		t.Fatalf("expected default source upload, got %q", document.Source)
	}
	if document.Version != 1 {
		t.Fatalf("expected default document version 1, got %d", document.Version)
	}
	if document.Checksum == "" {
		t.Fatal("expected document checksum")
	}
}

func TestVerifyDocumentSourceDetectsChangedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("第一版"), 0o600); err != nil {
		t.Fatalf("write test document: %v", err)
	}
	document := enrichDocumentGovernance(model.Document{Path: path})
	if err := os.WriteFile(path, []byte("第二版"), 0o600); err != nil {
		t.Fatalf("rewrite test document: %v", err)
	}

	err := verifyDocumentSource(document)
	if err == nil || classifyIndexError(err) != indexErrorSourceChanged {
		t.Fatalf("expected source_changed, got err=%v code=%q", err, classifyIndexError(err))
	}
}

func TestPublicIndexRunRecordDoesNotExposeInternalError(t *testing.T) {
	record := publicIndexRunRecord(model.IndexRunRecord{
		ErrorCode: indexErrorSourceMissing,
		Error:     "open /Users/private/data/notes.txt: no such file",
	})
	if record.Error != "原文文件不可用" {
		t.Fatalf("expected generic public error, got %q", record.Error)
	}
}

func TestPublicIndexRunRecordClassifiesLegacyInternalError(t *testing.T) {
	record := publicIndexRunRecord(model.IndexRunRecord{
		Error: "open /Users/private/data/notes.txt: no such file",
	})
	if record.ErrorCode != indexErrorSourceMissing || record.Error != publicIndexError(indexErrorSourceMissing) {
		t.Fatalf("expected classified public legacy error, got %+v", record)
	}
}

func TestRecordIndexRunStoresFailureClassificationAndRunID(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-1": {ID: "kb-1", Name: "测试知识库", Documents: []model.Document{{ID: "doc-1", Name: "notes.txt"}}},
	}}}

	runID := service.recordIndexRun(
		"kb-1",
		model.Document{ID: "doc-1", Name: "notes.txt"},
		"reindex",
		time.Now().Add(-time.Second),
		"failed",
		errors.New("source file unavailable"),
	)
	if runID == "" {
		t.Fatal("expected index run id")
	}

	service.state.Mu.RLock()
	kb := service.state.KnowledgeBases["kb-1"]
	service.state.Mu.RUnlock()
	if len(kb.IndexHistory) != 1 {
		t.Fatalf("expected one index history record, got %d", len(kb.IndexHistory))
	}
	if kb.IndexHistory[0].ErrorCode != "source_missing" {
		t.Fatalf("expected source_missing, got %q", kb.IndexHistory[0].ErrorCode)
	}
	if kb.Documents[0].IndexRunID != runID || kb.Documents[0].IndexErrorCode != "source_missing" {
		t.Fatalf("expected document governance fields to be updated, got %+v", kb.Documents[0])
	}
	if kb.Documents[0].Status != "failed" || kb.Documents[0].IndexError != publicIndexError(indexErrorSourceMissing) {
		t.Fatalf("expected public failed document state, got %+v", kb.Documents[0])
	}
}

func TestDocumentNeedsReindexWhenOnlyErrorCodeIsPresent(t *testing.T) {
	document := model.Document{
		Status:         "indexed",
		IndexErrorCode: indexErrorFailed,
		IndexedAt:      "2026-08-22T00:00:00Z",
		IndexVersion:   currentIndexVersion,
	}
	health := model.KnowledgeBaseDocumentHealth{
		ChunkCount:          1,
		RawContentAvailable: true,
	}
	if !documentNeedsReindex(document, health) {
		t.Fatal("expected an error-coded document to require reindex")
	}
}

func TestIndexCleanupErrorPreservesUnderlyingCause(t *testing.T) {
	underlying := errors.New("delete qdrant points failed")
	err := &IndexCleanupError{Err: underlying}
	if !errors.Is(err, underlying) {
		t.Fatalf("expected cleanup error to unwrap underlying cause, got %v", err)
	}
}

func TestGetKnowledgeBaseIndexHistoryFallsBackToCurrentVersion(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-1": {ID: "kb-1", Name: "测试知识库"},
	}}}

	response, err := service.GetKnowledgeBaseIndexHistory("kb-1")
	if err != nil {
		t.Fatalf("get index history: %v", err)
	}
	if response.CurrentIndexVersion != currentIndexVersion {
		t.Fatalf("expected fallback index version %d, got %d", currentIndexVersion, response.CurrentIndexVersion)
	}
}
