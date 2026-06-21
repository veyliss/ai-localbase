package service

import (
	"fmt"
	"testing"

	"ai-localbase/internal/model"

	"github.com/stretchr/testify/require"
)

type memoryChatHistoryStore struct {
	conversations map[string]model.Conversation
}

func newMemoryChatHistoryStore() *memoryChatHistoryStore {
	return &memoryChatHistoryStore{
		conversations: map[string]model.Conversation{},
	}
}

func (s *memoryChatHistoryStore) SaveConversation(conversation model.Conversation) error {
	if s == nil {
		return fmt.Errorf("chat history store is nil")
	}
	s.conversations[conversation.ID] = cloneTestConversation(conversation)
	return nil
}

func (s *memoryChatHistoryStore) ListConversations() ([]model.ConversationListItem, error) {
	if s == nil {
		return nil, fmt.Errorf("chat history store is nil")
	}
	return []model.ConversationListItem{}, nil
}

func (s *memoryChatHistoryStore) GetConversation(id string) (*model.Conversation, error) {
	if s == nil {
		return nil, fmt.Errorf("chat history store is nil")
	}
	conversation, ok := s.conversations[id]
	if !ok {
		return nil, nil
	}
	cloned := cloneTestConversation(conversation)
	return &cloned, nil
}

func (s *memoryChatHistoryStore) DeleteConversation(id string) error {
	if s == nil {
		return fmt.Errorf("chat history store is nil")
	}
	delete(s.conversations, id)
	return nil
}

func cloneTestConversation(conversation model.Conversation) model.Conversation {
	conversation.Messages = cloneStoredMessages(conversation.Messages)
	return conversation
}

func TestAppServiceDeleteMessage(t *testing.T) {
	store := newMemoryChatHistoryStore()
	service := NewAppService(nil, nil, store, model.ServerConfig{})
	conversation := model.Conversation{
		ID:        "conversation-1",
		Title:     "测试对话",
		CreatedAt: "2026-06-18T06:00:00Z",
		UpdatedAt: "2026-06-18T06:00:10Z",
		Messages: []model.StoredChatMessage{
			{
				ID:        "message-1",
				Role:      "user",
				Content:   "第一个问题",
				CreatedAt: "2026-06-18T06:00:00Z",
			},
			{
				ID:        "message-2",
				Role:      "assistant",
				Content:   "第一个回答",
				CreatedAt: "2026-06-18T06:00:10Z",
			},
		},
	}
	require.NoError(t, store.SaveConversation(conversation))

	updated, err := service.DeleteMessage("conversation-1", "message-1")

	require.NoError(t, err)
	require.Len(t, updated.Messages, 1)
	require.Equal(t, "message-2", updated.Messages[0].ID)
	require.NotEqual(t, conversation.UpdatedAt, updated.UpdatedAt)

	saved, err := store.GetConversation("conversation-1")
	require.NoError(t, err)
	require.NotNil(t, saved)
	require.Len(t, saved.Messages, 1)
	require.Equal(t, "message-2", saved.Messages[0].ID)
}

func TestAppServiceDeleteMessageRejectsLastMessage(t *testing.T) {
	store := newMemoryChatHistoryStore()
	service := NewAppService(nil, nil, store, model.ServerConfig{})
	conversation := model.Conversation{
		ID:        "conversation-1",
		Title:     "测试对话",
		CreatedAt: "2026-06-18T06:00:00Z",
		UpdatedAt: "2026-06-18T06:00:00Z",
		Messages: []model.StoredChatMessage{
			{
				ID:        "message-1",
				Role:      "assistant",
				Content:   "欢迎使用",
				CreatedAt: "2026-06-18T06:00:00Z",
			},
		},
	}
	require.NoError(t, store.SaveConversation(conversation))

	updated, err := service.DeleteMessage("conversation-1", "message-1")

	require.Nil(t, updated)
	require.ErrorContains(t, err, "cannot delete the last message")
}
