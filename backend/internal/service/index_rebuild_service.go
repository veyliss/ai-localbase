package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

// IndexDocument indexes one uploaded document. The public method remains on
// AppService for compatibility, while the implementation lives with the
// other index lifecycle operations instead of the general application facade.
func (s *AppService) IndexDocument(document model.Document) (model.Document, error) {
	return s.IndexDocumentWithContext(context.Background(), document)
}

func (s *AppService) IndexDocumentWithContext(ctx context.Context, document model.Document) (indexed model.Document, err error) {
	ctx = normalizeServiceContext(ctx)
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		return model.Document{}, err
	}
	document = enrichDocumentGovernance(document)
	startedAt := time.Now()
	if existing, found, findErr := s.findDocumentByID(document.KnowledgeBaseID, document.ID); findErr != nil {
		return model.Document{}, findErr
	} else if found {
		if strings.EqualFold(strings.TrimSpace(existing.Checksum), strings.TrimSpace(document.Checksum)) || strings.TrimSpace(document.Checksum) == "" {
			return existing, nil
		}
		return model.Document{}, &DuplicateDocumentError{Existing: existing}
	}
	reservation, err := s.reserveDocumentIndex(ctx, document)
	if err != nil {
		return model.Document{}, err
	}
	defer reservation()

	// Re-read after taking the process-local reservation. Another caller may
	// have completed the same upload while the first state check was running.
	if existing, found, findErr := s.findDocumentByID(document.KnowledgeBaseID, document.ID); findErr != nil {
		return model.Document{}, findErr
	} else if found {
		if strings.EqualFold(strings.TrimSpace(existing.Checksum), strings.TrimSpace(document.Checksum)) || strings.TrimSpace(document.Checksum) == "" {
			return existing, nil
		}
		return model.Document{}, &DuplicateDocumentError{Existing: existing}
	}

	operation, err := s.beginIndexOperation(ctx, document)
	if err != nil {
		return model.Document{}, err
	}
	document.IndexFence = operation.Fence
	document.IndexOperationFence = operation.Fence
	document.IndexOperationOwner = operation.Owner
	document.IndexOperationAttempt = operation.Attempt

	s.state.Mu.RLock()
	config := s.state.Config
	s.state.Mu.RUnlock()
	var generation indexGenerationReceipt
	indexed, generation, err = reindexDocumentWithConfig(ctx, s, config, operation, document)
	if err != nil {
		_ = s.abortIndexGeneration(ctx, generation)
		_ = s.failIndexOperation(ctx, operation, "upload", startedAt, err)
		return model.Document{}, err
	}
	indexed, err = s.commitIndexOperation(ctx, operation, indexed, "upload", startedAt)
	if err != nil {
		_ = s.abortIndexGeneration(ctx, generation)
		return model.Document{}, err
	}
	if err := s.retirePreviousGeneration(ctx, generation); err != nil {
		log.Printf("failed to cleanup superseded index generation for document %s: %v", document.ID, err)
	}
	return indexed, nil
}

// ReindexDocument rebuilds a document from its source file and records a
// lifecycle entry. The source checksum is verified before Qdrant is changed.
func (s *AppService) ReindexDocument(knowledgeBaseID, documentID string) (model.Document, error) {
	return s.ReindexDocumentWithContext(context.Background(), knowledgeBaseID, documentID)
}

func (s *AppService) ReindexDocumentWithContext(ctx context.Context, knowledgeBaseID, documentID string) (indexed model.Document, err error) {
	return s.reindexDocumentWithContext(ctx, knowledgeBaseID, documentID, "reindex")
}

func (s *AppService) reindexDocumentWithContext(ctx context.Context, knowledgeBaseID, documentID, trigger string) (indexed model.Document, err error) {
	ctx = normalizeServiceContext(ctx)
	if s == nil {
		return model.Document{}, fmt.Errorf("app service is nil")
	}
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		return model.Document{}, err
	}

	document, err := s.findDocument(knowledgeBaseID, documentID)
	if err != nil {
		return model.Document{}, err
	}
	document = enrichDocumentGovernance(document)
	startedAt := time.Now()
	reservation, err := s.reserveDocumentIndex(ctx, document)
	if err != nil {
		return model.Document{}, err
	}
	defer reservation()
	latestDocument, err := s.findDocument(knowledgeBaseID, documentID)
	if err != nil {
		return model.Document{}, err
	}
	document = enrichDocumentGovernance(latestDocument)
	operation, err := s.beginIndexOperation(ctx, document)
	if err != nil {
		return model.Document{}, err
	}
	document.IndexFence = operation.Fence
	document.IndexOperationFence = operation.Fence
	document.IndexOperationOwner = operation.Owner
	document.IndexOperationAttempt = operation.Attempt
	if err := verifyDocumentSource(document); err != nil {
		_ = s.failIndexOperation(ctx, operation, trigger, startedAt, err)
		return model.Document{}, err
	}
	s.state.Mu.RLock()
	config := s.state.Config
	s.state.Mu.RUnlock()

	var generation indexGenerationReceipt
	indexed, generation, err = reindexDocumentWithConfig(ctx, s, config, operation, document)
	if err != nil {
		_ = s.abortIndexGeneration(ctx, generation)
		_ = s.failIndexOperation(ctx, operation, trigger, startedAt, err)
		return model.Document{}, err
	}
	indexed, err = s.commitIndexOperation(ctx, operation, indexed, trigger, startedAt)
	if err != nil {
		_ = s.abortIndexGeneration(ctx, generation)
		return model.Document{}, err
	}
	if err := s.retirePreviousGeneration(ctx, generation); err != nil {
		log.Printf("failed to cleanup superseded index generation for document %s: %v", documentID, err)
	}
	return indexed, nil
}

// ReindexKnowledgeBase preflights every source before changing any Qdrant
// point, then rebuilds documents in deterministic state order.
func (s *AppService) ReindexKnowledgeBase(knowledgeBaseID string) ([]model.Document, error) {
	if s == nil {
		return nil, fmt.Errorf("app service is nil")
	}
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	if knowledgeBaseID == "" {
		return nil, fmt.Errorf("knowledge base id is required")
	}

	s.state.Mu.RLock()
	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	if !ok {
		s.state.Mu.RUnlock()
		return nil, fmt.Errorf("knowledge base not found")
	}
	originalDocs := make([]model.Document, len(kb.Documents))
	copy(originalDocs, kb.Documents)
	s.state.Mu.RUnlock()

	// Confirm the complete source set before the first write, so a missing
	// staged file cannot leave a partially rebuilt knowledge base.
	for _, document := range originalDocs {
		if err := verifyDocumentSource(enrichDocumentGovernance(document)); err != nil {
			s.recordIndexRun(knowledgeBaseID, document, "bulk_reindex", time.Now(), "failed", err)
			return nil, fmt.Errorf("document %s: %w", document.ID, err)
		}
	}

	reindexed := make([]model.Document, 0, len(originalDocs))
	for _, document := range originalDocs {
		indexed, err := s.reindexDocumentWithContext(context.Background(), knowledgeBaseID, document.ID, "bulk_reindex")
		if err != nil {
			return nil, fmt.Errorf("reindex document %s: %w", document.ID, err)
		}
		reindexed = append(reindexed, indexed)
	}
	return reindexed, nil
}

func reindexDocumentWithConfig(ctx context.Context, s *AppService, cfg model.AppConfig, operation indexOperation, document model.Document) (model.Document, indexGenerationReceipt, error) {
	ctx = normalizeServiceContext(ctx)
	if err := s.ensureIndexOperationActive(ctx, operation); err != nil {
		return model.Document{}, indexGenerationReceipt{}, err
	}
	document.IndexFence = operation.Fence
	content, err := util.ExtractDocumentText(document.Path)
	if err != nil {
		return model.Document{}, indexGenerationReceipt{}, fmt.Errorf("extract uploaded document text: %w", err)
	}
	if err := s.ensureIndexOperationActive(ctx, operation); err != nil {
		return model.Document{}, indexGenerationReceipt{}, err
	}

	chunks := s.rag.BuildDocumentChunks(document, content)
	if err := s.ensureIndexOperationActive(ctx, operation); err != nil {
		return model.Document{}, indexGenerationReceipt{}, err
	}
	var generation indexGenerationReceipt
	if len(chunks) == 0 {
		generation, err = s.replaceDocumentChunksWithContextReceipt(ctx, operation, nil, nil)
		if err != nil {
			return model.Document{}, generation, err
		}
		document, err = s.captureIndexedDocumentWithContextForOperation(ctx, operation, document, content)
		if err != nil {
			return model.Document{}, generation, err
		}
		document.ContentPreview = util.BuildContentPreviewFromText(content)
		document.Status = "ready"
		document.ChunkCount = 0
		document.IndexedAt = util.NowRFC3339()
		document.IndexError = ""
		document.IndexErrorCode = ""
		document.IndexVersion = currentIndexVersion
		return document, generation, nil
	}

	vectors, err := s.rag.EmbedTexts(ctx, model.EmbeddingModelConfig{
		Provider: strings.TrimSpace(cfg.Embedding.Provider),
		BaseURL:  strings.TrimSpace(cfg.Embedding.BaseURL),
		Model:    strings.TrimSpace(cfg.Embedding.Model),
		APIKey:   strings.TrimSpace(cfg.Embedding.APIKey),
	}, chunkTexts(chunks), s.qdrantVectorSize())
	if err != nil {
		return model.Document{}, generation, err
	}
	if err := s.ensureIndexOperationActive(ctx, operation); err != nil {
		return model.Document{}, generation, err
	}
	generation, err = s.replaceDocumentChunksWithContextReceipt(ctx, operation, chunks, vectors)
	if err != nil {
		return model.Document{}, generation, err
	}
	document, err = s.captureIndexedDocumentWithContextForOperation(ctx, operation, document, content)
	if err != nil {
		return model.Document{}, generation, err
	}

	document.Status = "indexed"
	document.ContentPreview = previewFromChunks(chunks)
	document.ChunkCount = len(chunks)
	document.IndexedAt = util.NowRFC3339()
	document.IndexError = ""
	document.IndexErrorCode = ""
	document.IndexVersion = currentIndexVersion
	return document, generation, nil
}
