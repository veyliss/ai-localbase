package service

import (
	"fmt"
	"path/filepath"
	"strings"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

func isStructuredDocument(document model.Document) bool {
	if isStructuredDocumentPath(document.Path) {
		return true
	}
	switch strings.ToLower(filepath.Ext(document.Name)) {
	case ".csv", ".xlsx":
		return true
	default:
		return false
	}
}

func (s *AppService) captureIndexedDocument(document model.Document, content string) (model.Document, error) {
	document.IndexedContentAvailable = true
	document.IndexedContentChars = len([]rune(content))
	document.IndexedTablesCount = 0

	var tables []model.IndexedTable
	if isStructuredDocument(document) {
		parsed, err := util.ExtractStructuredTables(document.Path)
		if err != nil {
			return model.Document{}, fmt.Errorf("extract indexed structured tables: %w", err)
		}
		tables = structuredTablesToModel(parsed)
		document.IndexedTablesCount = len(tables)
	}

	if s == nil || s.indexedContentStore == nil || s.indexedContentStore.root == "" {
		return document, nil
	}
	if err := s.indexedContentStore.Put(document, content, tables); err != nil {
		return model.Document{}, err
	}
	return document, nil
}

func (s *AppService) loadIndexedDocumentArtifact(document model.Document) (indexedDocumentArtifact, bool, error) {
	if s == nil || s.indexedContentStore == nil {
		return indexedDocumentArtifact{}, false, nil
	}
	return s.indexedContentStore.Load(document)
}

func (s *AppService) resolveDocumentContent(document model.Document) (string, string, error) {
	artifact, found, err := s.loadIndexedDocumentArtifact(document)
	if err != nil {
		return "", "unavailable", err
	}
	if found {
		return artifact.Content, "indexed", nil
	}

	content, err := util.ExtractDocumentText(document.Path)
	if err != nil {
		return "", "unavailable", fmt.Errorf("extract document text: %w", err)
	}
	return content, "source", nil
}

func (s *AppService) resolveStructuredTables(document model.Document) ([]util.StructuredTable, string, error) {
	artifact, found, err := s.loadIndexedDocumentArtifact(document)
	if err != nil {
		return nil, "unavailable", err
	}
	if found {
		return indexedTablesToStructured(artifact.Tables), "indexed", nil
	}

	tables, err := util.ExtractStructuredTables(document.Path)
	if err != nil {
		return nil, "unavailable", fmt.Errorf("extract structured tables: %w", err)
	}
	return tables, "source", nil
}

func (s *AppService) deleteIndexedDocument(knowledgeBaseID, documentID string) error {
	if s == nil || s.indexedContentStore == nil {
		return nil
	}
	return s.indexedContentStore.Delete(knowledgeBaseID, documentID)
}
