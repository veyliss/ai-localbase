package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

const maxIndexHistoryRecords = 50

const (
	indexErrorDuplicate               = "duplicate_document"
	indexErrorSourceMissing           = "source_missing"
	indexErrorSourceChanged           = "source_changed"
	indexErrorSourceUnreadable        = "source_unreadable"
	indexErrorVectorDimensionMismatch = "vector_dimension_mismatch"
	indexErrorRuleOutdated            = "index_rule_outdated"
	indexErrorFailed                  = "index_failed"
)

// DuplicateDocumentError reports an idempotency conflict without exposing the
// stored document path or checksum to API callers.
type DuplicateDocumentError struct {
	Existing model.Document
}

func (e *DuplicateDocumentError) Error() string {
	if e == nil || strings.TrimSpace(e.Existing.ID) == "" {
		return "document already exists in knowledge base"
	}
	return fmt.Sprintf("document already exists in knowledge base: %s", e.Existing.ID)
}

// IndexCleanupError indicates that a document could not be removed from the
// vector index. The document remains in application state so the operation
// can be retried without leaving an invisible, searchable document behind.
type IndexCleanupError struct {
	Err error
}

func (e *IndexCleanupError) Error() string {
	if e == nil || e.Err == nil {
		return "document index cleanup failed"
	}
	return e.Err.Error()
}

func (e *IndexCleanupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func currentKnowledgeBaseIndexVersion(knowledgeBase model.KnowledgeBase) int {
	if knowledgeBase.CurrentIndexVersion > 0 {
		return knowledgeBase.CurrentIndexVersion
	}
	return currentIndexVersion
}

func indexedOrDocument(indexed, document model.Document) model.Document {
	if strings.TrimSpace(indexed.ID) != "" {
		return indexed
	}
	return document
}

func enrichDocumentGovernance(document model.Document) model.Document {
	if strings.TrimSpace(document.Source) == "" {
		document.Source = "upload"
	}
	if document.Version <= 0 {
		document.Version = 1
	}
	if strings.TrimSpace(document.Checksum) == "" && strings.TrimSpace(document.Path) != "" {
		if checksum, err := checksumFile(document.Path); err == nil {
			document.Checksum = checksum
		}
	}
	return document
}

func checksumFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func normalizeKnowledgeBaseTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len([]rune(tag)) > 32 {
			continue
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result
}

func classifyIndexError(err error) string {
	if err == nil {
		return ""
	}
	var duplicateErr *DuplicateDocumentError
	if errors.As(err, &duplicateErr) {
		return indexErrorDuplicate
	}
	if errors.Is(err, fs.ErrNotExist) || strings.Contains(strings.ToLower(err.Error()), "source file unavailable") || strings.Contains(strings.ToLower(err.Error()), "no such file") {
		return indexErrorSourceMissing
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "checksum"), strings.Contains(lower, "source file changed"):
		return indexErrorSourceChanged
	case strings.Contains(lower, "dimension"), strings.Contains(lower, "vector size"), strings.Contains(lower, "vector dimension"):
		return indexErrorVectorDimensionMismatch
	case strings.Contains(lower, "index version"), strings.Contains(lower, "reindex"):
		return indexErrorRuleOutdated
	case strings.Contains(lower, "extract"), strings.Contains(lower, "parse"):
		return indexErrorSourceUnreadable
	default:
		return indexErrorFailed
	}
}

func publicIndexError(code string) string {
	switch strings.TrimSpace(code) {
	case "":
		return ""
	case indexErrorDuplicate:
		return "相同内容的文档已存在"
	case indexErrorSourceMissing:
		return "原文文件不可用"
	case indexErrorSourceChanged:
		return "原文内容已变化，需要重新上传或确认后重建"
	case indexErrorSourceUnreadable:
		return "原文无法读取或解析"
	case indexErrorVectorDimensionMismatch:
		return "向量维度与当前索引配置不一致"
	case indexErrorRuleOutdated:
		return "索引规则已更新，需要重建索引"
	case indexErrorFailed:
		return "索引处理失败"
	default:
		return "索引处理失败"
	}
}

// PublicIndexFailure converts an internal indexing error into the stable,
// non-sensitive error contract used by HTTP and batch-index responses.
func PublicIndexFailure(err error) (string, string) {
	code := classifyIndexError(err)
	return code, publicIndexError(code)
}

func publicIndexRunRecord(record model.IndexRunRecord) model.IndexRunRecord {
	public := record
	if strings.TrimSpace(public.ErrorCode) == "" && strings.TrimSpace(public.Error) != "" {
		public.ErrorCode = classifyIndexError(errors.New(public.Error))
	}
	if public.ErrorCode != "" {
		public.Error = publicIndexError(public.ErrorCode)
	} else {
		public.Error = ""
	}
	return public
}

func publicIndexRunRecords(records []model.IndexRunRecord) []model.IndexRunRecord {
	if len(records) == 0 {
		return nil
	}
	result := make([]model.IndexRunRecord, len(records))
	for index, record := range records {
		result[index] = publicIndexRunRecord(record)
	}
	return result
}

func publicDocument(document model.Document) model.Document {
	code := documentIndexErrorCode(document)
	document.IndexErrorCode = code
	document.IndexError = publicIndexError(code)
	return document
}

func publicKnowledgeBase(knowledgeBase model.KnowledgeBase) model.KnowledgeBase {
	documents := make([]model.Document, len(knowledgeBase.Documents))
	for index, document := range knowledgeBase.Documents {
		documents[index] = publicDocument(document)
	}
	knowledgeBase.Documents = documents
	knowledgeBase.IndexHistory = publicIndexRunRecords(knowledgeBase.IndexHistory)
	return knowledgeBase
}

func documentIndexErrorCode(document model.Document) string {
	if code := strings.TrimSpace(document.IndexErrorCode); code != "" {
		return code
	}
	if strings.TrimSpace(document.IndexError) == "" {
		return ""
	}
	return classifyIndexError(errors.New(document.IndexError))
}

func verifyDocumentSource(document model.Document) error {
	path := strings.TrimSpace(document.Path)
	if path == "" {
		return fmt.Errorf("source file unavailable: document path is empty")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("source file unavailable")
	}
	if strings.TrimSpace(document.Checksum) == "" {
		return nil
	}
	actual, err := checksumFile(path)
	if err != nil {
		return fmt.Errorf("source file unreadable")
	}
	if !strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(document.Checksum)) {
		return fmt.Errorf("source file checksum changed")
	}
	return nil
}

func (s *AppService) recordIndexRun(
	knowledgeBaseID string,
	document model.Document,
	trigger string,
	startedAt time.Time,
	status string,
	err error,
) string {
	document = enrichDocumentGovernance(document)
	if s == nil || s.state == nil {
		return ""
	}
	completedAt := time.Now().UTC()
	errorCode, publicError := PublicIndexFailure(err)
	record := model.IndexRunRecord{
		ID:              util.NextID("index-run"),
		KnowledgeBaseID: strings.TrimSpace(knowledgeBaseID),
		DocumentID:      strings.TrimSpace(document.ID),
		DocumentName:    strings.TrimSpace(document.Name),
		Trigger:         strings.TrimSpace(trigger),
		Status:          strings.TrimSpace(status),
		IndexVersion:    currentIndexVersion,
		ChunkCount:      document.ChunkCount,
		ErrorCode:       errorCode,
		Error:           publicError,
		StartedAt:       startedAt.UTC().Format(time.RFC3339),
		CompletedAt:     completedAt.Format(time.RFC3339),
	}

	s.state.Mu.Lock()
	kb, ok := s.state.KnowledgeBases[record.KnowledgeBaseID]
	if ok {
		kb.UpdatedAt = completedAt.Format(time.RFC3339)
		if kb.CurrentIndexVersion == 0 {
			kb.CurrentIndexVersion = currentIndexVersion
		}
		if status == "succeeded" {
			kb.CurrentIndexVersion = currentIndexVersion
		}
		kb.IndexHistory = append([]model.IndexRunRecord{record}, kb.IndexHistory...)
		if len(kb.IndexHistory) > maxIndexHistoryRecords {
			kb.IndexHistory = kb.IndexHistory[:maxIndexHistoryRecords]
		}
		if document.ID != "" {
			for index, item := range kb.Documents {
				if item.ID != document.ID {
					continue
				}
				kb.Documents[index].IndexRunID = record.ID
				if err != nil {
					kb.Documents[index].IndexErrorCode = record.ErrorCode
					kb.Documents[index].IndexError = publicIndexError(record.ErrorCode)
					kb.Documents[index].Status = "failed"
				}
				break
			}
		}
		s.state.KnowledgeBases[record.KnowledgeBaseID] = kb
	}
	s.state.Mu.Unlock()

	if !ok {
		return ""
	}
	if saveErr := s.saveState(); saveErr != nil {
		// Indexing has already completed; persistence failure should be visible in logs
		// without replacing the original indexing result.
		fmt.Printf("persist index history failed: %v\n", saveErr)
	}
	return record.ID
}

func (s *AppService) GetKnowledgeBaseIndexHistory(knowledgeBaseID string) (model.KnowledgeBaseIndexHistoryResponse, error) {
	if s == nil || s.state == nil {
		return model.KnowledgeBaseIndexHistoryResponse{}, fmt.Errorf("app service is nil")
	}
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	if knowledgeBaseID == "" {
		return model.KnowledgeBaseIndexHistoryResponse{}, fmt.Errorf("knowledge base id is required")
	}

	s.state.Mu.RLock()
	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	history := publicIndexRunRecords(kb.IndexHistory)
	currentVersion := kb.CurrentIndexVersion
	s.state.Mu.RUnlock()
	if !ok {
		return model.KnowledgeBaseIndexHistoryResponse{}, fmt.Errorf("knowledge base not found")
	}
	if currentVersion == 0 {
		currentVersion = currentIndexVersion
	}
	return model.KnowledgeBaseIndexHistoryResponse{
		KnowledgeBaseID:     knowledgeBaseID,
		CurrentIndexVersion: currentVersion,
		Items:               history,
	}, nil
}
