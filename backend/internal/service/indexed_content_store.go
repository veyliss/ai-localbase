package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

const indexedContentArtifactVersion = 1

type indexedDocumentArtifact struct {
	Version         int                  `json:"version"`
	KnowledgeBaseID string               `json:"knowledgeBaseId"`
	DocumentID      string               `json:"documentId"`
	DocumentName    string               `json:"documentName"`
	IndexFence      string               `json:"indexFence,omitempty"`
	Content         string               `json:"content"`
	Tables          []model.IndexedTable `json:"tables,omitempty"`
}

type IndexedContentStore struct {
	root string
}

func NewIndexedContentStore(root string) *IndexedContentStore {
	return &IndexedContentStore{root: strings.TrimSpace(root)}
}

func (s *IndexedContentStore) Put(document model.Document, content string, tables []model.IndexedTable) error {
	if s == nil || s.root == "" {
		return fmt.Errorf("indexed content store is not configured")
	}
	if strings.TrimSpace(document.KnowledgeBaseID) == "" || strings.TrimSpace(document.ID) == "" {
		return fmt.Errorf("indexed content store requires knowledge base and document ids")
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create indexed content directory: %w", err)
	}

	artifact := indexedDocumentArtifact{
		Version:         indexedContentArtifactVersion,
		KnowledgeBaseID: document.KnowledgeBaseID,
		DocumentID:      document.ID,
		DocumentName:    document.Name,
		IndexFence:      strings.TrimSpace(document.IndexFence),
		Content:         content,
		Tables:          cloneIndexedTables(tables),
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return fmt.Errorf("encode indexed content: %w", err)
	}

	tempFile, err := os.CreateTemp(s.root, ".indexed-content-*.tmp")
	if err != nil {
		return fmt.Errorf("create indexed content temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()

	if err := tempFile.Chmod(0o600); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("protect indexed content temp file: %w", err)
	}
	if _, err := tempFile.Write(encoded); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write indexed content: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("flush indexed content: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close indexed content temp file: %w", err)
	}

	if err := os.Rename(tempPath, s.pathForGeneration(document.KnowledgeBaseID, document.ID, document.IndexFence)); err != nil {
		return fmt.Errorf("replace indexed content: %w", err)
	}
	return nil
}

func (s *IndexedContentStore) Load(document model.Document) (indexedDocumentArtifact, bool, error) {
	if s == nil || s.root == "" {
		return indexedDocumentArtifact{}, false, nil
	}
	content, err := os.ReadFile(s.pathForGeneration(document.KnowledgeBaseID, document.ID, document.IndexFence))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return indexedDocumentArtifact{}, false, nil
		}
		return indexedDocumentArtifact{}, false, fmt.Errorf("read indexed content: %w", err)
	}

	var artifact indexedDocumentArtifact
	if err := json.Unmarshal(content, &artifact); err != nil {
		return indexedDocumentArtifact{}, false, fmt.Errorf("decode indexed content: %w", err)
	}
	if artifact.Version != indexedContentArtifactVersion {
		return indexedDocumentArtifact{}, false, fmt.Errorf("unsupported indexed content version %d", artifact.Version)
	}
	if artifact.KnowledgeBaseID != document.KnowledgeBaseID || artifact.DocumentID != document.ID {
		return indexedDocumentArtifact{}, false, fmt.Errorf("indexed content identity mismatch")
	}
	if strings.TrimSpace(artifact.IndexFence) != strings.TrimSpace(document.IndexFence) {
		return indexedDocumentArtifact{}, false, fmt.Errorf("indexed content generation mismatch")
	}
	return artifact, true, nil
}

func (s *IndexedContentStore) Delete(knowledgeBaseID, documentID string) error {
	if s == nil || s.root == "" {
		return nil
	}
	return s.deleteAllGenerations(knowledgeBaseID, documentID)
}

func (s *IndexedContentStore) DeleteGeneration(knowledgeBaseID, documentID, indexFence string) error {
	if s == nil || s.root == "" {
		return nil
	}
	paths := []string{s.pathFor(knowledgeBaseID, documentID), s.pathForGeneration(knowledgeBaseID, documentID, indexFence)}
	matches, err := filepath.Glob(s.generationPattern(knowledgeBaseID, documentID))
	if err != nil {
		return fmt.Errorf("find indexed content generations: %w", err)
	}
	paths = append(paths, matches...)
	seen := make(map[string]struct{}, len(paths))
	for _, filePath := range paths {
		if _, exists := seen[filePath]; exists {
			continue
		}
		seen[filePath] = struct{}{}
		if err := s.deletePaths(filePath); err != nil {
			return err
		}
	}
	return nil
}

func (s *IndexedContentStore) deleteAllGenerations(knowledgeBaseID, documentID string) error {
	paths := []string{s.pathFor(knowledgeBaseID, documentID)}
	matches, err := filepath.Glob(s.generationPattern(knowledgeBaseID, documentID))
	if err != nil {
		return fmt.Errorf("find indexed content generations: %w", err)
	}
	paths = append(paths, matches...)
	return s.deletePaths(paths...)
}

func (s *IndexedContentStore) deletePaths(paths ...string) error {
	for _, filePath := range paths {
		if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete indexed content: %w", err)
		}
	}
	return nil
}

func (s *IndexedContentStore) pathFor(knowledgeBaseID, documentID string) string {
	hash := sha256.Sum256([]byte(knowledgeBaseID + "\x00" + documentID))
	return filepath.Join(s.root, hex.EncodeToString(hash[:])+".json")
}

func (s *IndexedContentStore) pathForGeneration(knowledgeBaseID, documentID, indexFence string) string {
	if strings.TrimSpace(indexFence) == "" {
		return s.pathFor(knowledgeBaseID, documentID)
	}
	baseHash := sha256.Sum256([]byte(knowledgeBaseID + "\x00" + documentID))
	fenceHash := sha256.Sum256([]byte(strings.TrimSpace(indexFence)))
	return filepath.Join(s.root, hex.EncodeToString(baseHash[:])+"-"+hex.EncodeToString(fenceHash[:])[:16]+".json")
}

func (s *IndexedContentStore) generationPattern(knowledgeBaseID, documentID string) string {
	hash := sha256.Sum256([]byte(knowledgeBaseID + "\x00" + documentID))
	return filepath.Join(s.root, hex.EncodeToString(hash[:])+"-*.json")
}

func cloneIndexedTables(source []model.IndexedTable) []model.IndexedTable {
	if source == nil {
		return nil
	}
	cloned := make([]model.IndexedTable, len(source))
	for index, table := range source {
		cloned[index] = model.IndexedTable{
			FileName: table.FileName,
			Sheet:    table.Sheet,
			Headers:  append([]string(nil), table.Headers...),
			Rows:     make([]model.IndexedTableRow, len(table.Rows)),
		}
		for rowIndex, row := range table.Rows {
			cloned[index].Rows[rowIndex] = model.IndexedTableRow{
				Number: row.Number,
				Values: append([]string(nil), row.Values...),
			}
		}
	}
	return cloned
}

func structuredTablesToModel(source []util.StructuredTable) []model.IndexedTable {
	if source == nil {
		return nil
	}
	tables := make([]model.IndexedTable, len(source))
	for index, table := range source {
		tables[index] = model.IndexedTable{
			FileName: table.FileName,
			Sheet:    table.Sheet,
			Headers:  append([]string(nil), table.Headers...),
			Rows:     make([]model.IndexedTableRow, len(table.Rows)),
		}
		for rowIndex, row := range table.Rows {
			tables[index].Rows[rowIndex] = model.IndexedTableRow{
				Number: row.Number,
				Values: append([]string(nil), row.Values...),
			}
		}
	}
	return tables
}

func indexedTablesToStructured(source []model.IndexedTable) []util.StructuredTable {
	if source == nil {
		return nil
	}
	tables := make([]util.StructuredTable, len(source))
	for index, table := range source {
		tables[index] = util.StructuredTable{
			FileName: table.FileName,
			Sheet:    table.Sheet,
			Headers:  append([]string(nil), table.Headers...),
			Rows:     make([]util.StructuredTableRow, len(table.Rows)),
		}
		for rowIndex, row := range table.Rows {
			tables[index].Rows[rowIndex] = util.StructuredTableRow{
				Number: row.Number,
				Values: append([]string(nil), row.Values...),
			}
		}
	}
	return tables
}
