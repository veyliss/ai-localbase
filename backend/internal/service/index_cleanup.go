package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

var ErrDocumentDeletionPending = errors.New("document deletion is already pending")

const (
	indexCleanupTypeCollection    = "collection"
	indexCleanupTypeDocument      = "document"
	indexCleanupTypeGeneration    = "generation"
	indexCleanupTypeKnowledgeBase = "knowledge_base"
	indexCleanupRetryBaseDelay    = 5 * time.Second
	indexCleanupRetryMaxDelay     = 5 * time.Minute
	indexCleanupOperationTimeout  = 2 * time.Minute
)

// appendIndexCleanupTaskLocked adds an idempotent outbox entry while the app
// state lock is held. The outbox entry must be persisted in the same state
// snapshot as the metadata change that made cleanup necessary.
func appendIndexCleanupTaskLocked(tasks []model.IndexCleanupTask, task model.IndexCleanupTask) []model.IndexCleanupTask {
	if strings.TrimSpace(task.ID) == "" {
		return tasks
	}
	for _, existing := range tasks {
		if existing.ID == task.ID {
			return tasks
		}
	}
	return append(tasks, task)
}

func removeIndexCleanupTaskLocked(tasks []model.IndexCleanupTask, taskID string) ([]model.IndexCleanupTask, bool) {
	for index, task := range tasks {
		if task.ID != strings.TrimSpace(taskID) {
			continue
		}
		return append(tasks[:index], tasks[index+1:]...), true
	}
	return tasks, false
}

func newIndexCleanupTask(taskType, knowledgeBaseID string) model.IndexCleanupTask {
	now := util.NowRFC3339()
	return model.IndexCleanupTask{
		ID:              util.NextID("index-cleanup"),
		Type:            strings.TrimSpace(taskType),
		KnowledgeBaseID: strings.TrimSpace(knowledgeBaseID),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func generationCleanupTask(receipt indexGenerationReceipt) model.IndexCleanupTask {
	return generationCleanupTaskFor(receipt.KnowledgeBaseID, receipt.DocumentID, receipt.Fence, uniqueIndexPointIDs(receipt.WrittenPointIDs, receipt.SupersededPointIDs))
}

func generationCleanupTaskFor(knowledgeBaseID, documentID, indexFence string, pointIDs []any) model.IndexCleanupTask {
	pointIDs = uniqueIndexPointIDs(pointIDs)
	task := newIndexCleanupTask(indexCleanupTypeGeneration, knowledgeBaseID)
	task.ID = stableGenerationCleanupTaskID(knowledgeBaseID, documentID, indexFence, pointIDs)
	task.DocumentID = strings.TrimSpace(documentID)
	task.IndexFence = strings.TrimSpace(indexFence)
	task.PointIDs = pointIDs
	return task
}

// stableGenerationCleanupTaskID makes the outbox idempotent across retries and
// process restarts. Point IDs are part of the identity so a later exact
// deletion set cannot be silently collapsed into an older task.
func stableGenerationCleanupTaskID(knowledgeBaseID, documentID, indexFence string, pointIDs []any) string {
	key := strings.Builder{}
	key.WriteString(strings.TrimSpace(knowledgeBaseID))
	key.WriteByte(0)
	key.WriteString(strings.TrimSpace(documentID))
	key.WriteByte(0)
	key.WriteString(strings.TrimSpace(indexFence))
	key.WriteByte(0)
	for _, pointID := range pointIDs {
		encoded, err := json.Marshal(pointID)
		if err != nil {
			fmt.Fprintf(&key, "%T:%v", pointID, pointID)
		} else {
			key.Write(encoded)
		}
		key.WriteByte(0)
	}
	digest := sha256.Sum256([]byte(key.String()))
	return "index-cleanup-" + hex.EncodeToString(digest[:16])
}

func generationRetirementCleanupTasks(receipt indexGenerationReceipt) []model.IndexCleanupTask {
	tasks := make([]model.IndexCleanupTask, 0, 2)
	if strings.TrimSpace(receipt.PreviousFence) != "" || len(receipt.PreviousPointIDs) > 0 {
		tasks = append(tasks, generationCleanupTaskFor(
			receipt.KnowledgeBaseID,
			receipt.DocumentID,
			receipt.PreviousFence,
			receipt.PreviousPointIDs,
		))
	}
	if strings.TrimSpace(receipt.SupersededFence) != "" || len(receipt.SupersededPointIDs) > 0 {
		tasks = append(tasks, generationCleanupTaskFor(
			receipt.KnowledgeBaseID,
			receipt.DocumentID,
			receipt.SupersededFence,
			receipt.SupersededPointIDs,
		))
	}
	return tasks
}

func generationAbortCleanupTasks(receipt indexGenerationReceipt) []model.IndexCleanupTask {
	tasks := make([]model.IndexCleanupTask, 0, 2)
	if strings.TrimSpace(receipt.Fence) != "" || len(receipt.WrittenPointIDs) > 0 || len(receipt.SupersededPointIDs) > 0 {
		tasks = append(tasks, generationCleanupTask(receipt))
	}
	if strings.TrimSpace(receipt.SupersededFence) != "" && receipt.SupersededFence != receipt.Fence {
		tasks = append(tasks, generationCleanupTaskFor(
			receipt.KnowledgeBaseID,
			receipt.DocumentID,
			receipt.SupersededFence,
			receipt.SupersededPointIDs,
		))
	}
	return tasks
}

func (s *AppService) queueIndexCleanupTasks(tasks []model.IndexCleanupTask) {
	for _, task := range tasks {
		s.queueIndexCleanupTask(task)
	}
}

func (s *AppService) removeIndexCleanupTasks(tasks []model.IndexCleanupTask) {
	for _, task := range tasks {
		if err := s.removeIndexCleanupTask(task.ID); err != nil {
			fmt.Printf("remove index cleanup task failed: %v\n", err)
		}
	}
}

func (s *AppService) queueIndexCleanupTask(task model.IndexCleanupTask) {
	if s == nil || s.state == nil || strings.TrimSpace(task.ID) == "" {
		return
	}
	s.state.Mu.Lock()
	s.state.IndexCleanupTasks = appendIndexCleanupTaskLocked(s.state.IndexCleanupTasks, task)
	s.state.Mu.Unlock()
	if err := s.saveState(); err != nil {
		// The task remains in memory and will be included in the next successful
		// state write. The original operation still reports its own failure.
		fmt.Printf("persist index cleanup task failed: %v\n", err)
	}
}

// RetryPendingIndexCleanup drains the durable outbox opportunistically. A
// failed external delete remains in state with backoff, so a transient Qdrant
// or filesystem outage cannot turn into permanent orphaned data.
func (s *AppService) RetryPendingIndexCleanup() (int, error) {
	if s == nil || s.state == nil {
		return 0, nil
	}
	s.indexCleanupMu.Lock()
	defer s.indexCleanupMu.Unlock()

	now := time.Now().UTC()
	s.state.Mu.RLock()
	tasks := cloneIndexCleanupTasks(s.state.IndexCleanupTasks)
	s.state.Mu.RUnlock()
	processed := 0
	var firstErr error
	for _, task := range tasks {
		if !indexCleanupTaskDue(task, now) {
			continue
		}
		if err := s.executeIndexCleanupTask(task); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			s.recordIndexCleanupFailure(task.ID, err, now)
			continue
		}
		var err error
		if task.Type == indexCleanupTypeDocument {
			err = s.finalizeDocumentCleanup(task)
		} else {
			err = s.removeIndexCleanupTask(task.ID)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			s.recordIndexCleanupFailure(task.ID, err, now)
			continue
		}
		processed++
	}
	return processed, firstErr
}

// finalizeDocumentCleanup removes the pending document and its outbox entry in
// one persisted state snapshot. External cleanup is deliberately performed
// before this transition, so a failed state write remains retryable.
func (s *AppService) finalizeDocumentCleanup(task model.IndexCleanupTask) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("app state is not configured")
	}
	taskID := strings.TrimSpace(task.ID)
	documentID := strings.TrimSpace(task.DocumentID)
	knowledgeBaseID := strings.TrimSpace(task.KnowledgeBaseID)
	if taskID == "" || documentID == "" || knowledgeBaseID == "" {
		return fmt.Errorf("document cleanup task identity is incomplete")
	}
	s.state.Mu.Lock()
	previousTasks := cloneIndexCleanupTasks(s.state.IndexCleanupTasks)
	previousDocument := model.Document{}
	documentIndex := -1
	updatedTasks, removed := removeIndexCleanupTaskLocked(s.state.IndexCleanupTasks, taskID)
	if !removed {
		s.state.Mu.Unlock()
		return nil
	}
	if kb, ok := s.state.KnowledgeBases[knowledgeBaseID]; ok {
		for index, document := range kb.Documents {
			if strings.TrimSpace(document.ID) == documentID {
				previousDocument = document
				if document.DeletionPending {
					documentIndex = index
					kb.Documents = append(kb.Documents[:index], kb.Documents[index+1:]...)
				}
				s.state.KnowledgeBases[knowledgeBaseID] = kb
				break
			}
		}
	}
	s.state.IndexCleanupTasks = updatedTasks
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		if documentIndex >= 0 {
			if kb, ok := s.state.KnowledgeBases[knowledgeBaseID]; ok && indexOfDocument(kb.Documents, documentID) < 0 {
				if documentIndex > len(kb.Documents) {
					documentIndex = len(kb.Documents)
				}
				kb.Documents = append(kb.Documents, model.Document{})
				copy(kb.Documents[documentIndex+1:], kb.Documents[documentIndex:])
				kb.Documents[documentIndex] = previousDocument
				s.state.KnowledgeBases[knowledgeBaseID] = kb
			}
		}
		s.state.IndexCleanupTasks = previousTasks
		s.state.Mu.Unlock()
		return err
	}
	s.invalidateSemanticCache()
	return nil
}

func indexCleanupTaskDue(task model.IndexCleanupTask, now time.Time) bool {
	if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.Type) == "" {
		return false
	}
	if strings.TrimSpace(task.NextAttemptAt) == "" {
		return true
	}
	next, err := time.Parse(time.RFC3339Nano, task.NextAttemptAt)
	return err != nil || !next.After(now)
}

func (s *AppService) recordIndexCleanupFailure(taskID string, operationErr error, now time.Time) {
	if s == nil || s.state == nil || operationErr == nil {
		return
	}
	s.state.Mu.Lock()
	for index, task := range s.state.IndexCleanupTasks {
		if task.ID != taskID {
			continue
		}
		task.Attempts++
		task.UpdatedAt = now.Format(time.RFC3339Nano)
		task.LastError = redactMCPJobMessage(operationErr.Error())
		task.NextAttemptAt = now.Add(indexCleanupRetryDelay(task.Attempts)).Format(time.RFC3339Nano)
		s.state.IndexCleanupTasks[index] = task
		break
	}
	s.state.Mu.Unlock()
	if err := s.saveState(); err != nil {
		fmt.Printf("persist index cleanup retry state failed: %v\n", err)
	}
}

func indexCleanupRetryDelay(attempts int) time.Duration {
	delay := indexCleanupRetryBaseDelay
	for index := 1; index < attempts && delay < indexCleanupRetryMaxDelay; index++ {
		delay *= 2
	}
	if delay > indexCleanupRetryMaxDelay {
		return indexCleanupRetryMaxDelay
	}
	return delay
}

func (s *AppService) removeIndexCleanupTask(taskID string) error {
	s.state.Mu.Lock()
	previous := cloneIndexCleanupTasks(s.state.IndexCleanupTasks)
	updated, removed := removeIndexCleanupTaskLocked(s.state.IndexCleanupTasks, taskID)
	if !removed {
		s.state.Mu.Unlock()
		return nil
	}
	s.state.IndexCleanupTasks = updated
	s.state.Mu.Unlock()
	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		s.state.IndexCleanupTasks = previous
		s.state.Mu.Unlock()
		return err
	}
	return nil
}

func (s *AppService) executeIndexCleanupTask(task model.IndexCleanupTask) error {
	ctx, cancel := context.WithTimeout(context.Background(), indexCleanupOperationTimeout)
	defer cancel()
	knowledgeBaseID := strings.TrimSpace(task.KnowledgeBaseID)
	if knowledgeBaseID == "" {
		return fmt.Errorf("index cleanup knowledge base id is missing")
	}
	if (task.Type == indexCleanupTypeDocument || task.Type == indexCleanupTypeGeneration) && strings.TrimSpace(task.DocumentID) == "" {
		return fmt.Errorf("index cleanup document id is missing")
	}

	switch strings.TrimSpace(task.Type) {
	case indexCleanupTypeCollection, indexCleanupTypeKnowledgeBase:
		var firstErr error
		collectionDeleted := false
		if err := s.deleteKnowledgeBaseCollection(knowledgeBaseID); err != nil {
			firstErr = err
		} else {
			collectionDeleted = true
		}
		if task.Type == indexCleanupTypeCollection {
			return firstErr
		}
		if !collectionDeleted {
			for _, documentID := range task.DocumentIDs {
				if err := s.deleteDocumentPointsAndContent(ctx, knowledgeBaseID, strings.TrimSpace(documentID)); err != nil {
					if firstErr == nil {
						firstErr = err
					}
				}
			}
		}
		for _, documentID := range task.DocumentIDs {
			if collectionDeleted {
				if err := s.deleteIndexedDocument(knowledgeBaseID, strings.TrimSpace(documentID)); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
		if err := removeSourceFiles(task.SourcePaths); err != nil && firstErr == nil {
			firstErr = err
		}
		return firstErr
	case indexCleanupTypeDocument:
		firstErr := s.deleteDocumentPointsAndContent(ctx, knowledgeBaseID, task.DocumentID)
		if err := removeSourceFiles(task.SourcePaths); err != nil && firstErr == nil {
			firstErr = err
		}
		return firstErr
	case indexCleanupTypeGeneration:
		var firstErr error
		if s.qdrant != nil && s.qdrant.IsEnabled() && len(task.PointIDs) > 0 {
			if err := s.qdrant.DeletePointsByIDs(ctx, knowledgeBaseID, task.PointIDs); err != nil {
				firstErr = err
			}
		}
		if s.indexedContentStore != nil && task.DocumentID != "" && task.IndexFence != "" {
			if err := s.indexedContentStore.DeleteGeneration(knowledgeBaseID, task.DocumentID, task.IndexFence); err != nil {
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr
	default:
		return fmt.Errorf("unsupported index cleanup task type: %s", task.Type)
	}
}

func (s *AppService) deleteDocumentPointsAndContent(ctx context.Context, knowledgeBaseID, documentID string) error {
	if strings.TrimSpace(documentID) == "" {
		return fmt.Errorf("index cleanup document id is missing")
	}
	var firstErr error
	if s.qdrant != nil && s.qdrant.IsEnabled() {
		if err := s.qdrant.DeletePointsByFilter(ctx, knowledgeBaseID, documentFilter(documentID)); err != nil {
			firstErr = fmt.Errorf("delete qdrant points for document %s: %w", documentID, err)
		}
	}
	if s.indexedContentStore != nil {
		if err := s.indexedContentStore.Delete(knowledgeBaseID, documentID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func removeSourceFiles(paths []string) error {
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete source file: %w", err)
		}
	}
	return nil
}
