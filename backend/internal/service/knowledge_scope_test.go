package service

import (
	"errors"
	"testing"

	"ai-localbase/internal/model"
)

func TestResolveKnowledgeScopeDefaultsToNoRetrieval(t *testing.T) {
	tests := []struct {
		name            string
		rawScope        string
		knowledgeBaseID string
		documentID      string
		want            string
	}{
		{name: "empty request", want: KnowledgeScopeNone},
		{name: "knowledge base id", knowledgeBaseID: "kb-1", want: KnowledgeScopeSelected},
		{name: "document id", documentID: "doc-1", want: KnowledgeScopeSelected},
		{name: "explicit all", rawScope: KnowledgeScopeAll, want: KnowledgeScopeAll},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolveKnowledgeScope(test.rawScope, test.knowledgeBaseID, test.documentID)
			if err != nil {
				t.Fatalf("resolve knowledge scope: %v", err)
			}
			if got != test.want {
				t.Fatalf("expected scope %q, got %q", test.want, got)
			}
		})
	}
}

func TestResolveKnowledgeScopeRejectsAmbiguousCombinations(t *testing.T) {
	tests := []struct {
		name            string
		rawScope        string
		knowledgeBaseID string
		documentID      string
	}{
		{name: "none with knowledge base", rawScope: KnowledgeScopeNone, knowledgeBaseID: "kb-1"},
		{name: "selected without target", rawScope: KnowledgeScopeSelected},
		{name: "all with document", rawScope: KnowledgeScopeAll, documentID: "doc-1"},
		{name: "unknown scope", rawScope: "workspace"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ResolveKnowledgeScope(test.rawScope, test.knowledgeBaseID, test.documentID); err == nil {
				t.Fatal("expected invalid knowledge scope to be rejected")
			}
		})
	}
}

func TestResolveRetrievalKnowledgeBaseIDsRequiresExplicitAllScope(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-novel":  {ID: "kb-novel"},
		"kb-school": {ID: "kb-school"},
	}}}

	noScope, err := service.resolveRetrievalKnowledgeBaseIDs(model.ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("resolve empty retrieval scope: %v", err)
	}
	if len(noScope) != 0 {
		t.Fatalf("expected no retrieval targets for an empty scope, got %#v", noScope)
	}

	selected, err := service.resolveRetrievalKnowledgeBaseIDs(model.ChatCompletionRequest{
		KnowledgeBaseID: "kb-school",
	})
	if err != nil {
		t.Fatalf("resolve selected retrieval scope: %v", err)
	}
	if len(selected) != 1 || selected[0] != "kb-school" {
		t.Fatalf("expected only kb-school, got %#v", selected)
	}

	all, err := service.resolveRetrievalKnowledgeBaseIDs(model.ChatCompletionRequest{
		KnowledgeScope: KnowledgeScopeAll,
	})
	if err != nil {
		t.Fatalf("resolve explicit all retrieval scope: %v", err)
	}
	if len(all) != 2 || all[0] != "kb-novel" || all[1] != "kb-school" {
		t.Fatalf("expected all knowledge bases in stable order, got %#v", all)
	}
}

func TestRetrievalBoundarySkipsEmptyKnowledgeScope(t *testing.T) {
	service := &AppService{state: &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{
		"kb-1": {ID: "kb-1"},
	}}}

	chunks, err := service.EvaluateRetrieve(model.ChatCompletionRequest{
		Messages: []model.ChatMessage{{Role: "user", Content: "这个问题不应检索知识库"}},
	})
	if err != nil {
		t.Fatalf("evaluate empty retrieval scope: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected no chunks for empty retrieval scope, got %#v", chunks)
	}
}

func TestConversationScopeSeparatesExplicitAllFromNormalChat(t *testing.T) {
	store := newMemoryChatHistoryStore()
	service := &AppService{
		chatHistory: store,
		state:       &model.AppState{KnowledgeBases: map[string]model.KnowledgeBase{}},
	}
	if err := store.SaveConversation(model.Conversation{
		ID:             "conversation-all",
		KnowledgeScope: KnowledgeScopeAll,
		ScopeVersion:   conversationScopeVersion,
		Messages:       []model.StoredChatMessage{{ID: "message-1", Role: "user", Content: "全库问题"}},
	}); err != nil {
		t.Fatalf("seed all-scope conversation: %v", err)
	}

	err := service.ValidateChatRequestScope(model.ChatCompletionRequest{
		ConversationID: "conversation-all",
		Messages:       []model.ChatMessage{{Role: "user", Content: "普通聊天"}},
	})
	if !errors.Is(err, ErrConversationScopeMismatch) {
		t.Fatalf("expected explicit all scope to differ from empty scope, got %v", err)
	}
}
