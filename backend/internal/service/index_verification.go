package service

import (
	"fmt"
	"strings"

	"ai-localbase/internal/model"
)

// VerifyIndexedDocument checks the durable artifacts used by detail views and
// structured retrieval. It does not require the original path when an indexed
// content snapshot is available.
func (s *AppService) VerifyIndexedDocument(knowledgeBaseID, documentID string) (model.IndexedDocumentVerification, error) {
	document, err := s.findDocument(knowledgeBaseID, documentID)
	if err != nil {
		return model.IndexedDocumentVerification{}, err
	}

	verification := model.IndexedDocumentVerification{
		KnowledgeBaseID:     document.KnowledgeBaseID,
		DocumentID:          document.ID,
		DocumentName:        document.Name,
		Status:              "attention",
		ExpectedChunkCount:  document.ChunkCount,
		IndexVersion:        document.IndexVersion,
		CurrentIndexVersion: currentIndexVersion,
	}
	addIssue := func(issue string) {
		issue = strings.TrimSpace(issue)
		if issue == "" {
			return
		}
		for _, existing := range verification.Issues {
			if existing == issue {
				return
			}
		}
		verification.Issues = append(verification.Issues, issue)
	}

	artifact, found, err := s.loadIndexedDocumentArtifact(document)
	if err != nil {
		addIssue("indexed_content_snapshot_unreadable")
	} else if found {
		verification.SnapshotAvailable = true
		verification.ContentSource = "indexed"
		verification.SnapshotChars = len([]rune(artifact.Content))
		verification.SnapshotTables = len(artifact.Tables)
	} else {
		verification.ContentSource = "source"
		addIssue("indexed_content_snapshot_missing")
	}

	content, contentSource, contentErr := s.resolveDocumentContent(document)
	if contentErr != nil {
		addIssue("content_unavailable")
		verification.ContentSource = "unavailable"
	} else {
		verification.ContentSource = contentSource
		verification.SnapshotChars = len([]rune(content))
		if strings.TrimSpace(content) == "" {
			addIssue("content_empty")
		}
	}

	var chunks []DocumentChunk
	if contentErr == nil && s.rag != nil {
		chunks = s.rag.BuildDocumentChunks(document, content)
	}
	verification.ChunkCount = len(chunks)
	if contentErr == nil && document.ChunkCount != len(chunks) {
		addIssue(fmt.Sprintf("chunk_count_mismatch:%d:%d", document.ChunkCount, len(chunks)))
	}
	for _, chunk := range chunks {
		if chunk.Kind == "structured_row" {
			verification.StructuredRowCount++
		}
		if strings.TrimSpace(chunk.Text) == "" {
			continue
		}
		if chunk.CharEnd <= chunk.CharStart || chunk.LineStart <= 0 || chunk.LineEnd < chunk.LineStart {
			verification.EvidenceMissingCount++
			continue
		}
		verification.EvidenceLocatedCount++
	}
	if verification.EvidenceMissingCount > 0 {
		addIssue("evidence_location_missing")
	}

	if document.IndexedContentAvailable && !verification.SnapshotAvailable {
		addIssue("indexed_content_flag_without_snapshot")
	}
	if document.IndexedContentChars > 0 && verification.SnapshotChars != document.IndexedContentChars {
		addIssue("snapshot_character_count_mismatch")
	}
	if document.IndexVersion != currentIndexVersion {
		addIssue("index_version_outdated")
	}

	if isStructuredDocument(document) {
		tables, _, tablesErr := s.resolveStructuredTables(document)
		if tablesErr != nil {
			addIssue("structured_snapshot_unavailable")
		} else {
			if verification.SnapshotAvailable && verification.SnapshotTables != len(tables) {
				addIssue("structured_table_count_mismatch")
			}
			if document.IndexedTablesCount > 0 && document.IndexedTablesCount != len(tables) {
				addIssue("indexed_table_count_mismatch")
			}
		}
	}

	verification.Valid = len(verification.Issues) == 0
	if verification.Valid {
		verification.Status = "healthy"
	}
	return verification, nil
}
