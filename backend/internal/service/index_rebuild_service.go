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
	document = documentWithIndexFence(document, ctx)
	startedAt := time.Now()
	if document.Version <= 0 {
		document.Version = 1
	}
	defer func() {
		status := "failed"
		if err == nil {
			status = "succeeded"
		}
		if runID := s.recordIndexRunWithContext(ctx, document.KnowledgeBaseID, indexedOrDocument(indexed, document), "upload", startedAt, status, err); runID != "" && indexed.ID != "" {
			indexed.IndexRunID = runID
		}
	}()
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

	content, err := util.ExtractDocumentText(document.Path)
	if err != nil {
		return model.Document{}, fmt.Errorf("extract uploaded document text: %w", err)
	}
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		return model.Document{}, err
	}

	chunks := s.rag.BuildDocumentChunks(document, content)
	if len(chunks) == 0 {
		if err := s.replaceDocumentChunksWithContext(ctx, document.KnowledgeBaseID, document.ID, nil, nil); err != nil {
			return model.Document{}, err
		}
		document, err = s.captureIndexedDocumentWithContext(ctx, document, content)
		if err != nil {
			return model.Document{}, err
		}
		document.ContentPreview = util.BuildContentPreviewFromText(content)
		document.Status = "ready"
		document.ChunkCount = 0
		document.IndexedAt = util.NowRFC3339()
		document.IndexError = ""
		document.IndexErrorCode = ""
		document.IndexVersion = currentIndexVersion
		uploaded, err := s.addDocumentWithContext(ctx, document.KnowledgeBaseID, document)
		if err != nil {
			_ = s.deleteIndexedDocumentWithContext(ctx, document.KnowledgeBaseID, document.ID)
			return model.Document{}, err
		}
		return uploaded, nil
	}

	vectors, err := s.rag.EmbedTexts(ctx, s.currentEmbeddingConfig(), chunkTexts(chunks), s.qdrantVectorSize())
	if err != nil {
		return model.Document{}, err
	}
	if err := s.ensureIndexOperationLease(ctx); err != nil {
		return model.Document{}, err
	}

	if err := s.replaceDocumentChunksWithContext(ctx, document.KnowledgeBaseID, document.ID, chunks, vectors); err != nil {
		return model.Document{}, err
	}
	document, err = s.captureIndexedDocumentWithContext(ctx, document, content)
	if err != nil {
		_ = s.deleteDocumentChunksWithContext(ctx, document.KnowledgeBaseID, document.ID)
		_ = s.deleteIndexedDocumentWithContext(ctx, document.KnowledgeBaseID, document.ID)
		return model.Document{}, err
	}

	document.Status = "indexed"
	document.ContentPreview = previewFromChunks(chunks)
	document.ChunkCount = len(chunks)
	document.IndexedAt = util.NowRFC3339()
	document.IndexError = ""
	document.IndexErrorCode = ""
	document.IndexVersion = currentIndexVersion
	uploaded, err := s.addDocumentWithContext(ctx, document.KnowledgeBaseID, document)
	if err != nil {
		_ = s.deleteDocumentChunksWithContext(ctx, document.KnowledgeBaseID, document.ID)
		_ = s.deleteIndexedDocumentWithContext(ctx, document.KnowledgeBaseID, document.ID)
		return model.Document{}, err
	}
	return uploaded, nil
}

// ReindexDocument rebuilds a document from its source file and records a
// lifecycle entry. The source checksum is verified before Qdrant is changed.
func (s *AppService) ReindexDocument(knowledgeBaseID, documentID string) (model.Document, error) {
	return s.ReindexDocumentWithContext(context.Background(), knowledgeBaseID, documentID)
}

func (s *AppService) ReindexDocumentWithContext(ctx context.Context, knowledgeBaseID, documentID string) (indexed model.Document, err error) {
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
	defer func() {
		status := "failed"
		if err == nil {
			status = "succeeded"
		}
		if runID := s.recordIndexRunWithContext(ctx, knowledgeBaseID, indexedOrDocument(indexed, document), "reindex", startedAt, status, err); runID != "" && indexed.ID != "" {
			indexed.IndexRunID = runID
		}
	}()
	if err := verifyDocumentSource(document); err != nil {
		document.Status = "failed"
		document.IndexErrorCode = classifyIndexError(err)
		document.IndexError = publicIndexError(document.IndexErrorCode)
		document.IndexedAt = util.NowRFC3339()
		_ = s.updateDocumentWithContext(ctx, knowledgeBaseID, document)
		return model.Document{}, err
	}
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
	previousIndexFence := strings.TrimSpace(document.IndexFence)
	document = documentWithIndexFence(document, ctx)
	s.state.Mu.RLock()
	config := s.state.Config
	s.state.Mu.RUnlock()

	indexed, err = reindexDocumentWithConfig(ctx, s, config, document)
	if err != nil {
		document.IndexFence = previousIndexFence
		document.Status = "failed"
		document.IndexErrorCode = classifyIndexError(err)
		document.IndexError = publicIndexError(document.IndexErrorCode)
		document.IndexedAt = util.NowRFC3339()
		_ = s.updateDocumentWithContext(ctx, knowledgeBaseID, document)
		return model.Document{}, err
	}
	if err := s.updateDocumentWithContext(ctx, knowledgeBaseID, indexed); err != nil {
		return model.Document{}, err
	}
	if err := s.cleanupSupersededDocumentIndexWithContext(ctx, knowledgeBaseID, documentID, indexed.IndexFence); err != nil {
		// The current generation is already durable and retrieval is fenced by
		// the document metadata. Cleanup is best-effort and can be retried later.
		log.Printf("failed to cleanup superseded index generations for document %s: %v", documentID, err)
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
	config := s.state.Config
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
		doc := enrichDocumentGovernance(document)
		reservation, err := s.reserveDocumentIndex(context.Background(), doc)
		if err != nil {
			s.recordIndexRun(knowledgeBaseID, doc, "bulk_reindex", time.Now(), "failed", err)
			return nil, fmt.Errorf("reindex document %s: %w", doc.ID, err)
		}
		startedAt := time.Now()
		indexed, err := reindexDocumentWithConfig(context.Background(), s, config, doc)
		reservation()
		if err != nil {
			s.recordIndexRun(knowledgeBaseID, doc, "bulk_reindex", startedAt, "failed", err)
			return nil, fmt.Errorf("reindex document %s: %w", doc.ID, err)
		}
		runID := s.recordIndexRun(knowledgeBaseID, indexed, "bulk_reindex", startedAt, "succeeded", nil)
		indexed.IndexRunID = runID
		if err := s.updateDocument(knowledgeBaseID, indexed); err != nil {
			return nil, err
		}
		reindexed = append(reindexed, indexed)
	}
	return reindexed, nil
}

func reindexDocumentWithConfig(ctx context.Context, s *AppService, cfg model.AppConfig, document model.Document) (model.Document, error) {
	ctx = normalizeServiceContext(ctx)
	content, err := util.ExtractDocumentText(document.Path)
	if err != nil {
		return model.Document{}, fmt.Errorf("extract uploaded document text: %w", err)
	}

	chunks := s.rag.BuildDocumentChunks(document, content)
	if len(chunks) == 0 {
		if err := s.replaceDocumentChunksWithContext(ctx, document.KnowledgeBaseID, document.ID, nil, nil); err != nil {
			return model.Document{}, err
		}
		document, err = s.captureIndexedDocumentWithContext(ctx, document, content)
		if err != nil {
			return model.Document{}, err
		}
		document.ContentPreview = util.BuildContentPreviewFromText(content)
		document.Status = "ready"
		document.ChunkCount = 0
		document.IndexedAt = util.NowRFC3339()
		document.IndexError = ""
		document.IndexErrorCode = ""
		document.IndexVersion = currentIndexVersion
		return document, nil
	}

	vectors, err := s.rag.EmbedTexts(ctx, model.EmbeddingModelConfig{
		Provider: strings.TrimSpace(cfg.Embedding.Provider),
		BaseURL:  strings.TrimSpace(cfg.Embedding.BaseURL),
		Model:    strings.TrimSpace(cfg.Embedding.Model),
		APIKey:   strings.TrimSpace(cfg.Embedding.APIKey),
	}, chunkTexts(chunks), s.qdrantVectorSize())
	if err != nil {
		return model.Document{}, err
	}

	if err := s.replaceDocumentChunksWithContext(ctx, document.KnowledgeBaseID, document.ID, chunks, vectors); err != nil {
		return model.Document{}, err
	}
	document, err = s.captureIndexedDocumentWithContext(ctx, document, content)
	if err != nil {
		_ = s.deleteDocumentChunksWithContext(ctx, document.KnowledgeBaseID, document.ID)
		return model.Document{}, err
	}

	document.Status = "indexed"
	document.ContentPreview = previewFromChunks(chunks)
	document.ChunkCount = len(chunks)
	document.IndexedAt = util.NowRFC3339()
	document.IndexError = ""
	document.IndexErrorCode = ""
	document.IndexVersion = currentIndexVersion
	return document, nil
}
