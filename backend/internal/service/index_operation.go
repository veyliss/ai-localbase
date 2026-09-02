package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

// indexOperation is the immutable authority for one indexing attempt. The
// current document fence remains the read-visible generation; Fence is only
// promoted after the compare-and-swap commit succeeds.
type indexOperation struct {
	KnowledgeBaseID string
	DocumentID      string
	Fence           string
	PreviousFence   string
	SupersededFence string
	ExpectedVersion int
	Owner           string
	Attempt         int
	Managed         bool
	NewDocument     bool
}

// indexGenerationReceipt records exactly what an operation wrote or observed.
// Cleanup uses these IDs instead of a broad document filter.
type indexGenerationReceipt struct {
	KnowledgeBaseID    string
	DocumentID         string
	Fence              string
	PreviousFence      string
	SupersededFence    string
	WrittenPointIDs    []any
	PreviousPointIDs   []any
	SupersededPointIDs []any
}

func newIndexFence(ctx context.Context) string {
	if fence := indexFenceForContext(ctx); fence != "" {
		return fence
	}
	return "index:" + util.NextID("generation")
}

func newIndexOperation(ctx context.Context, document model.Document) indexOperation {
	execution, _ := mcpJobExecutionFromContext(ctx)
	expectedVersion := document.Version
	if expectedVersion <= 0 {
		expectedVersion = 1
	}
	return indexOperation{
		KnowledgeBaseID: strings.TrimSpace(document.KnowledgeBaseID),
		DocumentID:      strings.TrimSpace(document.ID),
		Fence:           newIndexFence(ctx),
		PreviousFence:   strings.TrimSpace(document.IndexFence),
		ExpectedVersion: expectedVersion,
		Owner:           strings.TrimSpace(execution.Lease.Owner),
		Attempt:         execution.Lease.Attempt,
		Managed:         true,
	}
}

func (s *AppService) beginIndexOperation(ctx context.Context, document model.Document) (indexOperation, error) {
	ctx = normalizeServiceContext(ctx)
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		return indexOperation{}, err
	}
	if s == nil || s.state == nil {
		return indexOperation{}, fmt.Errorf("app state is not configured")
	}
	document = enrichDocumentGovernance(document)
	op := newIndexOperation(ctx, document)
	if op.KnowledgeBaseID == "" || op.DocumentID == "" {
		return indexOperation{}, fmt.Errorf("index operation requires knowledge base and document ids")
	}

	s.state.Mu.Lock()
	kb, ok := s.state.KnowledgeBases[op.KnowledgeBaseID]
	if !ok {
		s.state.Mu.Unlock()
		return indexOperation{}, fmt.Errorf("knowledge base not found")
	}
	previousKB := cloneKnowledgeBases(map[string]model.KnowledgeBase{op.KnowledgeBaseID: kb})[op.KnowledgeBaseID]
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		s.state.Mu.Unlock()
		return indexOperation{}, err
	}
	updated := false
	for index, current := range kb.Documents {
		if strings.TrimSpace(current.ID) != op.DocumentID {
			continue
		}
		if current.Version != op.ExpectedVersion || strings.TrimSpace(current.IndexFence) != op.PreviousFence {
			s.state.Mu.Unlock()
			return indexOperation{}, ErrIndexOperationSuperseded
		}
		op.PreviousFence = strings.TrimSpace(current.IndexFence)
		op.ExpectedVersion = current.Version
		op.SupersededFence = strings.TrimSpace(current.IndexOperationFence)
		current.IndexOperationFence = op.Fence
		current.IndexOperationOwner = op.Owner
		current.IndexOperationAttempt = op.Attempt
		kb.Documents[index] = current
		updated = true
		break
	}
	if !updated {
		if existing, duplicate := findDuplicateDocument(kb.Documents, document); duplicate {
			s.state.Mu.Unlock()
			return indexOperation{}, &DuplicateDocumentError{Existing: existing}
		}
		op.NewDocument = true
		document.Version = op.ExpectedVersion
		document.IndexFence = op.PreviousFence
		document.IndexOperationFence = op.Fence
		document.IndexOperationOwner = op.Owner
		document.IndexOperationAttempt = op.Attempt
		if strings.TrimSpace(document.Status) == "" {
			document.Status = "processing"
		}
		kb.Documents = append([]model.Document{document}, kb.Documents...)
	}
	kb.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.state.KnowledgeBases[op.KnowledgeBaseID] = kb
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.rollbackIndexOperationStart(op, previousKB)
		return indexOperation{}, fmt.Errorf("persist index operation: %w", err)
	}
	return op, nil
}

func indexOperationMarkerMatches(document model.Document, op indexOperation) bool {
	return strings.TrimSpace(document.IndexOperationFence) == op.Fence &&
		strings.TrimSpace(document.IndexOperationOwner) == op.Owner &&
		document.IndexOperationAttempt == op.Attempt
}

func (s *AppService) rollbackIndexOperationStart(op indexOperation, previousKB model.KnowledgeBase) {
	if s == nil || s.state == nil || op.DocumentID == "" {
		return
	}
	s.state.Mu.Lock()
	kb, ok := s.state.KnowledgeBases[op.KnowledgeBaseID]
	if ok {
		index := indexOfDocument(kb.Documents, op.DocumentID)
		if index >= 0 && indexOperationMarkerMatches(kb.Documents[index], op) {
			if op.NewDocument {
				kb.Documents = append(kb.Documents[:index], kb.Documents[index+1:]...)
			} else if previousIndex := indexOfDocument(previousKB.Documents, op.DocumentID); previousIndex >= 0 {
				kb.Documents[index] = previousKB.Documents[previousIndex]
			}
			s.state.KnowledgeBases[op.KnowledgeBaseID] = kb
		}
	}
	s.state.Mu.Unlock()
	_ = s.saveState()
}

// ensureIndexOperationActive validates both the MCP lease and the durable
// document marker. The marker is the application-state fencing token: it
// prevents a worker that has lost its lease from continuing after a newer
// indexing attempt has taken ownership.
func (s *AppService) ensureIndexOperationActive(ctx context.Context, op indexOperation) error {
	ctx = normalizeServiceContext(ctx)
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		return err
	}
	if !op.Managed {
		return nil
	}
	if s == nil || s.state == nil {
		return fmt.Errorf("app state is not configured")
	}
	s.state.Mu.RLock()
	kb, ok := s.state.KnowledgeBases[op.KnowledgeBaseID]
	if ok {
		index := indexOfDocument(kb.Documents, op.DocumentID)
		if index < 0 {
			s.state.Mu.RUnlock()
			return ErrIndexOperationSuperseded
		}
		current := kb.Documents[index]
		if current.Version != op.ExpectedVersion || strings.TrimSpace(current.IndexFence) != op.PreviousFence || !indexOperationMarkerMatches(current, op) {
			s.state.Mu.RUnlock()
			return ErrIndexOperationSuperseded
		}
	}
	s.state.Mu.RUnlock()
	if !ok {
		return fmt.Errorf("knowledge base not found")
	}
	return ctx.Err()
}

func (s *AppService) clearIndexOperationMarker(op indexOperation) {
	if s == nil || s.state == nil || op.DocumentID == "" {
		return
	}
	s.state.Mu.Lock()
	kb, ok := s.state.KnowledgeBases[op.KnowledgeBaseID]
	if ok {
		for index, document := range kb.Documents {
			if strings.TrimSpace(document.ID) != op.DocumentID || !indexOperationMarkerMatches(document, op) {
				continue
			}
			document.IndexOperationFence = ""
			document.IndexOperationOwner = ""
			document.IndexOperationAttempt = 0
			kb.Documents[index] = document
			break
		}
		s.state.KnowledgeBases[op.KnowledgeBaseID] = kb
	}
	s.state.Mu.Unlock()
	_ = s.saveState()
}

func (s *AppService) commitIndexOperation(
	ctx context.Context,
	op indexOperation,
	indexed model.Document,
	trigger string,
	startedAt time.Time,
) (model.Document, error) {
	ctx = normalizeServiceContext(ctx)
	if err := s.ensureIndexOperationActive(ctx, op); err != nil {
		return model.Document{}, err
	}
	if op.DocumentID == "" || strings.TrimSpace(indexed.ID) != op.DocumentID {
		return model.Document{}, fmt.Errorf("index operation document mismatch")
	}
	completedAt := time.Now().UTC()
	record := model.IndexRunRecord{
		ID:              util.NextID("index-run"),
		KnowledgeBaseID: op.KnowledgeBaseID,
		DocumentID:      op.DocumentID,
		DocumentName:    strings.TrimSpace(indexed.Name),
		Trigger:         strings.TrimSpace(trigger),
		Status:          "succeeded",
		IndexVersion:    currentIndexVersion,
		ChunkCount:      indexed.ChunkCount,
		StartedAt:       startedAt.UTC().Format(time.RFC3339),
		CompletedAt:     completedAt.Format(time.RFC3339),
	}

	s.state.Mu.Lock()
	kb, ok := s.state.KnowledgeBases[op.KnowledgeBaseID]
	if !ok {
		s.state.Mu.Unlock()
		return model.Document{}, fmt.Errorf("knowledge base not found")
	}
	previousKB := cloneKnowledgeBases(map[string]model.KnowledgeBase{op.KnowledgeBaseID: kb})[op.KnowledgeBaseID]
	updated := false
	for index, current := range kb.Documents {
		if strings.TrimSpace(current.ID) != op.DocumentID {
			continue
		}
		if current.Version != op.ExpectedVersion || strings.TrimSpace(current.IndexFence) != op.PreviousFence || !indexOperationMarkerMatches(current, op) {
			s.state.Mu.Unlock()
			return model.Document{}, ErrIndexOperationSuperseded
		}
		indexed.IndexFence = op.Fence
		indexed.IndexOperationFence = ""
		indexed.IndexOperationOwner = ""
		indexed.IndexOperationAttempt = 0
		indexed.IndexRunID = record.ID
		if indexed.Version <= 0 {
			indexed.Version = current.Version
		}
		kb.Documents[index] = indexed
		updated = true
		break
	}
	if !updated {
		s.state.Mu.Unlock()
		return model.Document{}, fmt.Errorf("document not found")
	}
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		s.state.KnowledgeBases[op.KnowledgeBaseID] = previousKB
		s.state.Mu.Unlock()
		return model.Document{}, err
	}
	kb.UpdatedAt = completedAt.Format(time.RFC3339)
	kb.CurrentIndexVersion = currentIndexVersion
	kb.IndexHistory = append([]model.IndexRunRecord{record}, kb.IndexHistory...)
	if len(kb.IndexHistory) > maxIndexHistoryRecords {
		kb.IndexHistory = kb.IndexHistory[:maxIndexHistoryRecords]
	}
	s.state.KnowledgeBases[op.KnowledgeBaseID] = kb
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		s.state.KnowledgeBases[op.KnowledgeBaseID] = previousKB
		s.state.Mu.Unlock()
		return model.Document{}, err
	}
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		if currentKB, exists := s.state.KnowledgeBases[op.KnowledgeBaseID]; exists {
			if restored, ok := rollbackCommittedIndexOperationLocked(currentKB, previousKB, op, record.ID); ok {
				currentKB = restored
			}
			s.state.KnowledgeBases[op.KnowledgeBaseID] = currentKB
		}
		s.state.Mu.Unlock()
		return model.Document{}, fmt.Errorf("persist indexed document: %w", err)
	}
	return indexed, nil
}

func indexOfDocument(documents []model.Document, documentID string) int {
	for index, document := range documents {
		if strings.TrimSpace(document.ID) == documentID {
			return index
		}
	}
	return -1
}

func rollbackCommittedIndexOperationLocked(currentKB, previousKB model.KnowledgeBase, op indexOperation, recordID string) (model.KnowledgeBase, bool) {
	index := indexOfDocument(currentKB.Documents, op.DocumentID)
	if index < 0 {
		return currentKB, false
	}
	current := currentKB.Documents[index]
	if strings.TrimSpace(current.IndexFence) != strings.TrimSpace(op.Fence) ||
		current.IndexRunID != recordID || strings.TrimSpace(current.IndexOperationFence) != "" {
		return currentKB, false
	}

	if previousIndex := indexOfDocument(previousKB.Documents, op.DocumentID); previousIndex >= 0 && !op.NewDocument {
		currentKB.Documents[index] = clearIndexOperationMarker(previousKB.Documents[previousIndex])
	} else {
		currentKB.Documents = append(currentKB.Documents[:index], currentKB.Documents[index+1:]...)
	}
	currentKB.IndexHistory = removeIndexRunRecord(currentKB.IndexHistory, recordID)
	if previousKB.UpdatedAt != "" {
		currentKB.UpdatedAt = previousKB.UpdatedAt
	}
	currentKB.CurrentIndexVersion = previousKB.CurrentIndexVersion
	return currentKB, true
}

func rollbackFailedIndexOperationLocked(currentKB, previousKB model.KnowledgeBase, op indexOperation, recordID string) (model.KnowledgeBase, bool) {
	index := indexOfDocument(currentKB.Documents, op.DocumentID)
	if index < 0 {
		return currentKB, false
	}
	current := currentKB.Documents[index]
	if current.IndexRunID != recordID || strings.TrimSpace(current.IndexOperationFence) != "" {
		return currentKB, false
	}

	if previousIndex := indexOfDocument(previousKB.Documents, op.DocumentID); previousIndex >= 0 && !op.NewDocument {
		currentKB.Documents[index] = clearIndexOperationMarker(previousKB.Documents[previousIndex])
	} else {
		currentKB.Documents = append(currentKB.Documents[:index], currentKB.Documents[index+1:]...)
	}
	currentKB.IndexHistory = removeIndexRunRecord(currentKB.IndexHistory, recordID)
	if previousKB.UpdatedAt != "" {
		currentKB.UpdatedAt = previousKB.UpdatedAt
	}
	currentKB.CurrentIndexVersion = previousKB.CurrentIndexVersion
	return currentKB, true
}

// beginIndexOperation stores the operation marker before the final state
// transition. If that transition cannot be persisted, the marker must not be
// left behind as a phantom in-progress operation in the in-memory state.
func clearIndexOperationMarker(document model.Document) model.Document {
	document.IndexOperationFence = ""
	document.IndexOperationOwner = ""
	document.IndexOperationAttempt = 0
	return document
}

func removeIndexRunRecord(records []model.IndexRunRecord, recordID string) []model.IndexRunRecord {
	for index, record := range records {
		if record.ID != recordID {
			continue
		}
		return append(records[:index], records[index+1:]...)
	}
	return records
}

func (s *AppService) failIndexOperation(ctx context.Context, op indexOperation, trigger string, startedAt time.Time, operationErr error) error {
	ctx = normalizeServiceContext(ctx)
	if err := s.ensureIndexOperationActive(ctx, op); err != nil {
		return err
	}
	if s == nil || s.state == nil {
		return fmt.Errorf("app state is not configured")
	}
	completedAt := time.Now().UTC()
	errorCode, publicError := PublicIndexFailure(operationErr)
	record := model.IndexRunRecord{
		ID:              util.NextID("index-run"),
		KnowledgeBaseID: op.KnowledgeBaseID,
		DocumentID:      op.DocumentID,
		Trigger:         strings.TrimSpace(trigger),
		Status:          "failed",
		IndexVersion:    currentIndexVersion,
		ErrorCode:       errorCode,
		Error:           publicError,
		StartedAt:       startedAt.UTC().Format(time.RFC3339),
		CompletedAt:     completedAt.Format(time.RFC3339),
	}

	s.state.Mu.Lock()
	kb, ok := s.state.KnowledgeBases[op.KnowledgeBaseID]
	if !ok {
		s.state.Mu.Unlock()
		return fmt.Errorf("knowledge base not found")
	}
	previousKB := cloneKnowledgeBases(map[string]model.KnowledgeBase{op.KnowledgeBaseID: kb})[op.KnowledgeBaseID]
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		s.state.Mu.Unlock()
		return err
	}
	updated := false
	for index, document := range kb.Documents {
		if strings.TrimSpace(document.ID) != op.DocumentID {
			continue
		}
		if document.Version != op.ExpectedVersion || strings.TrimSpace(document.IndexFence) != op.PreviousFence || !indexOperationMarkerMatches(document, op) {
			s.state.Mu.Unlock()
			return ErrIndexOperationSuperseded
		}
		document.Status = "failed"
		document.IndexErrorCode = errorCode
		document.IndexError = publicError
		document.IndexedAt = completedAt.Format(time.RFC3339)
		document.IndexRunID = record.ID
		document.IndexOperationFence = ""
		document.IndexOperationOwner = ""
		document.IndexOperationAttempt = 0
		kb.Documents[index] = document
		updated = true
		break
	}
	if !updated {
		s.state.Mu.Unlock()
		return fmt.Errorf("document not found")
	}
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		s.state.KnowledgeBases[op.KnowledgeBaseID] = previousKB
		s.state.Mu.Unlock()
		return err
	}
	kb.UpdatedAt = completedAt.Format(time.RFC3339)
	kb.IndexHistory = append([]model.IndexRunRecord{record}, kb.IndexHistory...)
	if len(kb.IndexHistory) > maxIndexHistoryRecords {
		kb.IndexHistory = kb.IndexHistory[:maxIndexHistoryRecords]
	}
	s.state.KnowledgeBases[op.KnowledgeBaseID] = kb
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		s.state.KnowledgeBases[op.KnowledgeBaseID] = previousKB
		s.state.Mu.Unlock()
		return err
	}
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		if currentKB, exists := s.state.KnowledgeBases[op.KnowledgeBaseID]; exists {
			if restored, ok := rollbackFailedIndexOperationLocked(currentKB, previousKB, op, record.ID); ok {
				currentKB = restored
			}
			s.state.KnowledgeBases[op.KnowledgeBaseID] = currentKB
		}
		s.state.Mu.Unlock()
		return fmt.Errorf("persist failed index operation: %w", err)
	}
	return nil
}

// abortIndexGeneration only removes the generation owned by this operation.
// It remains safe to invoke after lease loss because the receipt contains no
// document-wide delete operation.
func (s *AppService) abortIndexGeneration(ctx context.Context, receipt indexGenerationReceipt) error {
	if s == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var firstErr error
	pointIDs := uniqueIndexPointIDs(receipt.WrittenPointIDs, receipt.SupersededPointIDs)
	if s.qdrant != nil && s.qdrant.IsEnabled() && len(pointIDs) > 0 {
		if err := s.qdrant.DeletePointsByIDs(cleanupCtx, receipt.KnowledgeBaseID, pointIDs); err != nil {
			firstErr = err
		}
	}
	if s.indexedContentStore != nil && strings.TrimSpace(receipt.Fence) != "" {
		if err := s.indexedContentStore.DeleteGeneration(receipt.KnowledgeBaseID, receipt.DocumentID, receipt.Fence); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.indexedContentStore != nil && strings.TrimSpace(receipt.SupersededFence) != "" && receipt.SupersededFence != receipt.Fence {
		if err := s.indexedContentStore.DeleteGeneration(receipt.KnowledgeBaseID, receipt.DocumentID, receipt.SupersededFence); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	_ = ctx
	return firstErr
}

func (s *AppService) retirePreviousGeneration(ctx context.Context, receipt indexGenerationReceipt) error {
	if s == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var firstErr error
	pointIDs := uniqueIndexPointIDs(receipt.PreviousPointIDs, receipt.SupersededPointIDs)
	if s.qdrant != nil && s.qdrant.IsEnabled() && len(pointIDs) > 0 {
		if err := s.qdrant.DeletePointsByIDs(cleanupCtx, receipt.KnowledgeBaseID, pointIDs); err != nil {
			firstErr = err
		}
	}
	if s.indexedContentStore != nil && (strings.TrimSpace(receipt.PreviousFence) != "" || len(receipt.PreviousPointIDs) > 0) {
		if err := s.indexedContentStore.DeleteGeneration(receipt.KnowledgeBaseID, receipt.DocumentID, receipt.PreviousFence); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.indexedContentStore != nil && (strings.TrimSpace(receipt.SupersededFence) != "" || len(receipt.SupersededPointIDs) > 0) && receipt.SupersededFence != receipt.PreviousFence {
		if err := s.indexedContentStore.DeleteGeneration(receipt.KnowledgeBaseID, receipt.DocumentID, receipt.SupersededFence); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	_ = ctx
	return firstErr
}

func uniqueIndexPointIDs(groups ...[]any) []any {
	seen := make(map[string]struct{})
	result := make([]any, 0)
	for _, group := range groups {
		for _, id := range group {
			key := fmt.Sprintf("%T:%v", id, id)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, id)
		}
	}
	return result
}

var ErrIndexOperationSuperseded = errors.New("index operation was superseded")
