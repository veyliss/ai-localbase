package service

import (
	"fmt"
	"strings"

	"ai-localbase/internal/model"
)

const (
	KnowledgeScopeNone     = "none"
	KnowledgeScopeSelected = "selected"
	KnowledgeScopeAll      = "all"
)

// ResolveKnowledgeScope makes the retrieval boundary explicit. An omitted
// scope remains backwards compatible when an older client sends an id, but it
// never turns an unscoped request into a full-database search.
func ResolveKnowledgeScope(rawScope, knowledgeBaseID, documentID string) (string, error) {
	scope := strings.ToLower(strings.TrimSpace(rawScope))
	hasKnowledgeBase := strings.TrimSpace(knowledgeBaseID) != ""
	hasDocument := strings.TrimSpace(documentID) != ""

	if scope == "" {
		if hasKnowledgeBase || hasDocument {
			return KnowledgeScopeSelected, nil
		}
		return KnowledgeScopeNone, nil
	}

	switch scope {
	case KnowledgeScopeNone:
		if hasKnowledgeBase || hasDocument {
			return "", fmt.Errorf("knowledgeScope none cannot include knowledgeBaseId or documentId")
		}
		return scope, nil
	case KnowledgeScopeSelected:
		if !hasKnowledgeBase && !hasDocument {
			return "", fmt.Errorf("knowledgeScope selected requires knowledgeBaseId or documentId")
		}
		return scope, nil
	case KnowledgeScopeAll:
		if hasKnowledgeBase || hasDocument {
			return "", fmt.Errorf("knowledgeScope all cannot include knowledgeBaseId or documentId")
		}
		return scope, nil
	default:
		return "", fmt.Errorf("invalid knowledgeScope %q; expected none, selected, or all", rawScope)
	}
}

func resolveKnowledgeScope(req model.ChatCompletionRequest) (string, error) {
	return ResolveKnowledgeScope(req.KnowledgeScope, req.KnowledgeBaseID, req.DocumentID)
}

func normalizeStoredKnowledgeScope(scope, knowledgeBaseID, documentID string) string {
	resolved, err := ResolveKnowledgeScope(scope, knowledgeBaseID, documentID)
	if err == nil {
		return resolved
	}
	if strings.TrimSpace(knowledgeBaseID) != "" || strings.TrimSpace(documentID) != "" {
		return KnowledgeScopeSelected
	}
	return KnowledgeScopeNone
}

func (s *AppService) ResolveKnowledgeScope(req model.ChatCompletionRequest) (string, error) {
	return resolveKnowledgeScope(req)
}
