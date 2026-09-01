package service

import (
	"context"
	"fmt"
	"strings"

	"ai-localbase/internal/model"
)

// reserveDocumentIndex serializes indexing attempts for the same knowledge
// base and content fingerprint. The reservation is process-local, while the
// persisted checksum check below protects retries after a process restart.
func (s *AppService) reserveDocumentIndex(ctx context.Context, document model.Document) (func(), error) {
	ctx = normalizeServiceContext(ctx)
	if s == nil {
		return nil, fmt.Errorf("app service is nil")
	}
	checksum := strings.ToLower(strings.TrimSpace(document.Checksum))
	knowledgeBaseID := strings.TrimSpace(document.KnowledgeBaseID)
	documentID := strings.TrimSpace(document.ID)
	if knowledgeBaseID == "" || documentID == "" {
		return func() {}, nil
	}

	key := knowledgeBaseID + "\x00"
	if checksum != "" {
		key += "checksum\x00" + checksum
	} else {
		key += "document\x00" + documentID
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		var existing model.Document
		var found bool
		var err error
		if checksum != "" {
			existing, found, err = s.findDocumentByChecksum(knowledgeBaseID, checksum)
			if err != nil {
				return nil, err
			}
			if found && strings.TrimSpace(existing.ID) != documentID {
				return nil, &DuplicateDocumentError{Existing: existing}
			}
		}

		s.indexReservationMu.Lock()
		if s.indexReservations == nil {
			s.indexReservations = make(map[string]chan struct{})
		}
		waiter, busy := s.indexReservations[key]
		if !busy {
			done := make(chan struct{})
			s.indexReservations[key] = done
			s.indexReservationMu.Unlock()

			// Check once more after acquiring the reservation. This closes the
			// small window between the state read and map insertion.
			if checksum != "" {
				existing, found, err = s.findDocumentByChecksum(knowledgeBaseID, checksum)
				if err != nil {
					s.releaseDocumentIndex(key, done)
					return nil, err
				}
				if found && strings.TrimSpace(existing.ID) != documentID {
					s.releaseDocumentIndex(key, done)
					return nil, &DuplicateDocumentError{Existing: existing}
				}
			}
			return func() { s.releaseDocumentIndex(key, done) }, nil
		}
		s.indexReservationMu.Unlock()

		select {
		case <-waiter:
			// The previous attempt finished. Re-read persisted state and either
			// report its successful document or take over after a failure.
			continue
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (s *AppService) releaseDocumentIndex(key string, done chan struct{}) {
	if s == nil || done == nil {
		return
	}
	s.indexReservationMu.Lock()
	if current, ok := s.indexReservations[key]; ok && current == done {
		delete(s.indexReservations, key)
		close(done)
	}
	s.indexReservationMu.Unlock()
}

func (s *AppService) findDocumentByChecksum(knowledgeBaseID, checksum string) (model.Document, bool, error) {
	if s == nil || s.state == nil {
		return model.Document{}, false, nil
	}
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	checksum = strings.ToLower(strings.TrimSpace(checksum))
	if knowledgeBaseID == "" || checksum == "" {
		return model.Document{}, false, nil
	}

	s.state.Mu.RLock()
	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	documents := append([]model.Document(nil), kb.Documents...)
	s.state.Mu.RUnlock()
	if !ok {
		return model.Document{}, false, fmt.Errorf("knowledge base not found")
	}

	for _, document := range documents {
		storedChecksum := strings.ToLower(strings.TrimSpace(document.Checksum))
		if storedChecksum == "" && strings.TrimSpace(document.Path) != "" {
			// Legacy state files did not persist checksums. Computing the
			// fingerprint here keeps duplicate protection effective without
			// exposing or rewriting the legacy document during a read.
			if calculated, err := checksumFile(document.Path); err == nil {
				storedChecksum = strings.ToLower(calculated)
			}
		}
		if storedChecksum == checksum {
			return document, true, nil
		}
	}
	return model.Document{}, false, nil
}

func (s *AppService) findDocumentByID(knowledgeBaseID, documentID string) (model.Document, bool, error) {
	if s == nil || s.state == nil {
		return model.Document{}, false, nil
	}
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	documentID = strings.TrimSpace(documentID)
	if knowledgeBaseID == "" || documentID == "" {
		return model.Document{}, false, nil
	}

	s.state.Mu.RLock()
	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	documents := append([]model.Document(nil), kb.Documents...)
	s.state.Mu.RUnlock()
	if !ok {
		return model.Document{}, false, fmt.Errorf("knowledge base not found")
	}
	for _, document := range documents {
		if strings.TrimSpace(document.ID) == documentID {
			return document, true, nil
		}
	}
	return model.Document{}, false, nil
}

func findDuplicateDocument(documents []model.Document, incoming model.Document) (model.Document, bool) {
	incomingID := strings.TrimSpace(incoming.ID)
	incomingChecksum := strings.ToLower(strings.TrimSpace(incoming.Checksum))
	for _, existing := range documents {
		if incomingID != "" && strings.TrimSpace(existing.ID) == incomingID {
			return existing, true
		}
		if incomingChecksum != "" && strings.EqualFold(strings.TrimSpace(existing.Checksum), incomingChecksum) {
			return existing, true
		}
	}
	return model.Document{}, false
}
