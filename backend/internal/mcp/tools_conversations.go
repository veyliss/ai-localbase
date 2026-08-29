package mcp

import (
	"ai-localbase/internal/model"
	"context"
	"fmt"
)

func newConversationTools(appService AppServiceReader) []ToolDefinition {
	return []ToolDefinition{
		{
			Name:            "list_conversations",
			Description:     "返回全部会话列表。",
			InputSchema:     emptyObjectSchema(),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				items, err := appService.ListConversations()
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(formatConversationListText(items), map[string]any{"items": items}), nil
			},
		},
		{
			Name:            "get_conversation",
			Description:     "按 conversationId 返回完整会话内容。",
			InputSchema:     requiredStringPropertySchema("conversationId", "会话 ID"),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				conversationID, err := requiredStringArg(args, "conversationId")
				if err != nil {
					return ToolCallResult{}, err
				}
				conversation, err := appService.GetConversation(conversationID)
				if err != nil {
					return ToolCallResult{}, err
				}
				if conversation == nil {
					return ToolCallResult{}, fmt.Errorf("conversation not found")
				}
				return NewTextResult(fmt.Sprintf("会话《%s》共有 %d 条消息。", conversation.Title, len(conversation.Messages)), map[string]any{"conversation": conversation}), nil
			},
		},
		{
			Name:        "save_conversation",
			Description: "保存完整会话。参数 id、messages 为必填，可选 title、knowledgeBaseId、documentId。",
			InputSchema: objectSchema(
				map[string]any{
					"id":              map[string]any{"type": "string", "description": "会话 ID"},
					"title":           map[string]any{"type": "string", "description": "会话标题"},
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID"},
					"documentId":      map[string]any{"type": "string", "description": "文档 ID"},
					"messages": map[string]any{
						"type":        "array",
						"description": "会话消息列表",
						"items": objectSchema(
							map[string]any{
								"id":        map[string]any{"type": "string", "description": "消息 ID"},
								"role":      map[string]any{"type": "string", "description": "消息角色，如 user / assistant / system"},
								"content":   map[string]any{"type": "string", "description": "消息内容"},
								"createdAt": map[string]any{"type": "string", "description": "消息创建时间，RFC3339 格式"},
							},
							[]string{"role", "content"},
						),
					},
				},
				[]string{"id", "messages"},
			),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				conversationID, err := requiredStringArg(args, "id")
				if err != nil {
					return ToolCallResult{}, err
				}
				rawMessages, ok := args["messages"].([]any)
				if !ok || len(rawMessages) == 0 {
					return ToolCallResult{}, fmt.Errorf("messages is required")
				}
				messages := make([]model.StoredChatMessage, 0, len(rawMessages))
				for _, rawMessage := range rawMessages {
					messageMap, ok := rawMessage.(map[string]any)
					if !ok {
						return ToolCallResult{}, fmt.Errorf("messages item must be object")
					}
					role, err := requiredStringArg(messageMap, "role")
					if err != nil {
						return ToolCallResult{}, err
					}
					content, err := requiredStringArg(messageMap, "content")
					if err != nil {
						return ToolCallResult{}, err
					}
					createdAt := optionalStringArg(messageMap, "createdAt")
					if createdAt == "" {
						createdAt = modelNowRFC3339()
					}
					messages = append(messages, model.StoredChatMessage{
						ID:        optionalStringArg(messageMap, "id"),
						Role:      role,
						Content:   content,
						CreatedAt: createdAt,
					})
				}
				conversation, err := appService.SaveConversation(model.SaveConversationRequest{
					ID:              conversationID,
					Title:           optionalStringArg(args, "title"),
					KnowledgeBaseID: optionalStringArg(args, "knowledgeBaseId"),
					DocumentID:      optionalStringArg(args, "documentId"),
					Messages:        messages,
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult("会话已保存。", map[string]any{"conversation": conversation}), nil
			},
		},
		{
			Name:            "delete_conversation",
			Description:     "删除指定会话。参数 id 为必填。该操作属于危险操作。",
			InputSchema:     requiredStringPropertySchema("id", "会话 ID"),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionDanger,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				conversationID, err := requiredStringArg(args, "id")
				if err != nil {
					return ToolCallResult{}, err
				}
				if err := appService.DeleteConversation(conversationID); err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("会话 %s 已删除。", conversationID),
					map[string]any{"id": conversationID},
				), nil
			},
		},
	}
}
