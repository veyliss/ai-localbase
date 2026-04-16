package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ai-localbase/internal/model"
	"ai-localbase/internal/service"
	"ai-localbase/internal/util"
)

type AppServiceReader interface {
	GetConfig() model.AppConfig
	ListKnowledgeBases() []model.KnowledgeBase
	GetKnowledgeBaseDocuments(id string) ([]model.Document, error)
	ListConversations() ([]model.ConversationListItem, error)
	GetConversation(id string) (*model.Conversation, error)
	BuildRetrievalContext(req model.ChatCompletionRequest) (string, []map[string]string, error)
	CreateKnowledgeBase(req model.KnowledgeBaseInput) (model.KnowledgeBase, error)
	DeleteKnowledgeBase(id string) (int, error)
	DeleteDocument(knowledgeBaseID, documentID string) (model.Document, error)
	SaveConversation(req model.SaveConversationRequest) (*model.Conversation, error)
	DeleteConversation(id string) error
	ResolveKnowledgeBaseID(candidate string) (string, error)
	IndexDocument(document model.Document) (model.Document, error)
}

func NewReadOnlyTools(appService AppServiceReader) []ToolDefinition {
	return []ToolDefinition{
		{
			Name:            "get_config_summary",
			Description:     "返回当前聊天模型与嵌入模型配置摘要。",
			InputSchema:     emptyObjectSchema(),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				cfg := appService.GetConfig()
				return NewTextResult(
					fmt.Sprintf("当前 Chat 模型为 %s/%s，Embedding 模型为 %s/%s。", cfg.Chat.Provider, cfg.Chat.Model, cfg.Embedding.Provider, cfg.Embedding.Model),
					map[string]any{"config": cfg},
				), nil
			},
		},
		{
			Name:            "list_knowledge_bases",
			Description:     "返回全部知识库及基础统计信息。",
			InputSchema:     emptyObjectSchema(),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				knowledgeBases := appService.ListKnowledgeBases()
				items := make([]map[string]any, 0, len(knowledgeBases))
				for _, kb := range knowledgeBases {
					items = append(items, map[string]any{
						"id":            kb.ID,
						"name":          kb.Name,
						"description":   kb.Description,
						"documentCount": len(kb.Documents),
						"createdAt":     kb.CreatedAt,
					})
				}
				return NewTextResult(fmt.Sprintf("当前共有 %d 个知识库。", len(items)), map[string]any{"items": items}), nil
			},
		},
		{
			Name:            "list_documents",
			Description:     "按知识库列出文档列表。参数 knowledgeBaseId 为必填。",
			InputSchema:     requiredStringPropertySchema("knowledgeBaseId", "知识库 ID"),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				documents, err := appService.GetKnowledgeBaseDocuments(knowledgeBaseID)
				if err != nil {
					return ToolCallResult{}, err
				}
				items := make([]map[string]any, 0, len(documents))
				for _, document := range documents {
					items = append(items, map[string]any{
						"id":              document.ID,
						"knowledgeBaseId": document.KnowledgeBaseID,
						"name":            document.Name,
						"sizeLabel":       document.SizeLabel,
						"uploadedAt":      document.UploadedAt,
						"status":          document.Status,
						"contentPreview":  document.ContentPreview,
					})
				}
				return NewTextResult(fmt.Sprintf("知识库 %s 下共有 %d 份文档。", knowledgeBaseID, len(items)), map[string]any{"items": items}), nil
			},
		},
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
				return NewTextResult(fmt.Sprintf("当前共有 %d 个会话。", len(items)), map[string]any{"items": items}), nil
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
			Name:        "search_knowledge_base",
			Description: "按知识库执行检索并返回命中文本与来源。参数 knowledgeBaseId 与 query 为必填。",
			InputSchema: requiredStringPropertiesSchema(map[string]string{
				"knowledgeBaseId": "知识库 ID",
				"query":           "检索问题",
			}),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				query, err := requiredStringArg(args, "query")
				if err != nil {
					return ToolCallResult{}, err
				}
				contextText, sources, err := appService.BuildRetrievalContext(model.ChatCompletionRequest{
					KnowledgeBaseID: knowledgeBaseID,
					Messages: []model.ChatMessage{{
						Role:    "user",
						Content: query,
					}},
					Embedding: embeddingModelConfigFromAppConfig(appService.GetConfig()),
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				text := strings.TrimSpace(contextText)
				if text == "" {
					text = "未检索到相关内容。"
				}
				return NewTextResult(text, map[string]any{"sources": sources, "knowledgeBaseId": knowledgeBaseID, "query": query}), nil
			},
		},
		{
			Name:        "create_knowledge_base",
			Description: "创建新的知识库。参数 name 为必填，description 为选填。",
			InputSchema: objectSchema(
				map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "知识库名称",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "知识库描述",
					},
				},
				[]string{"name"},
			),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				name, err := requiredStringArg(args, "name")
				if err != nil {
					return ToolCallResult{}, err
				}
				description := optionalStringArg(args, "description")
				knowledgeBase, err := appService.CreateKnowledgeBase(model.KnowledgeBaseInput{
					Name:        name,
					Description: description,
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("知识库《%s》创建成功。", knowledgeBase.Name),
					map[string]any{"knowledgeBase": knowledgeBase},
				), nil
			},
		},
		{
			Name:            "delete_knowledge_base",
			Description:     "删除指定知识库。参数 knowledgeBaseId 为必填。该操作属于危险操作。",
			InputSchema:     requiredStringPropertySchema("knowledgeBaseId", "知识库 ID"),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionDanger,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				remaining, err := appService.DeleteKnowledgeBase(knowledgeBaseID)
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("知识库 %s 已删除，剩余 %d 个知识库。", knowledgeBaseID, remaining),
					map[string]any{"knowledgeBaseId": knowledgeBaseID, "remaining": remaining},
				), nil
			},
		},
		{
			Name:        "delete_document",
			Description: "删除指定知识库中的文档。参数 knowledgeBaseId 与 documentId 为必填。该操作属于危险操作。",
			InputSchema: requiredStringPropertiesSchema(map[string]string{
				"knowledgeBaseId": "知识库 ID",
				"documentId":      "文档 ID",
			}),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionDanger,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				documentID, err := requiredStringArg(args, "documentId")
				if err != nil {
					return ToolCallResult{}, err
				}
				removedDocument, err := appService.DeleteDocument(knowledgeBaseID, documentID)
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("文档《%s》已删除。", removedDocument.Name),
					map[string]any{"document": removedDocument},
				), nil
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
		{
			Name:        "upload_text_document",
			Description: "向指定知识库上传纯文本文档。参数 knowledgeBaseId、fileName、content 为必填，仅支持 .txt/.md/.csv。",
			InputSchema: objectSchema(
				map[string]any{
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID"},
					"fileName":        map[string]any{"type": "string", "description": "文件名，需带扩展名"},
					"content":         map[string]any{"type": "string", "description": "纯文本内容"},
				},
				[]string{"knowledgeBaseId", "fileName", "content"},
			),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				fileName, err := requiredStringArg(args, "fileName")
				if err != nil {
					return ToolCallResult{}, err
				}
				content, err := requiredStringArg(args, "content")
				if err != nil {
					return ToolCallResult{}, err
				}
				resolvedKnowledgeBaseID, err := appService.ResolveKnowledgeBaseID(knowledgeBaseID)
				if err != nil {
					return ToolCallResult{}, err
				}
				if err := validateTextUploadFileName(fileName); err != nil {
					return ToolCallResult{}, err
				}
				storedName := fmt.Sprintf("%d_%s", util.NowUnixNano(), util.SanitizeFilename(fileName))
				destination := filepath.Join("data/uploads", storedName)
				if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
					return ToolCallResult{}, fmt.Errorf("create upload directory: %w", err)
				}
				if err := os.WriteFile(destination, []byte(content), 0o644); err != nil {
					return ToolCallResult{}, fmt.Errorf("write uploaded file: %w", err)
				}
				document := model.Document{
					ID:              util.NextID("doc"),
					KnowledgeBaseID: resolvedKnowledgeBaseID,
					Name:            fileName,
					Size:            int64(len([]byte(content))),
					SizeLabel:       util.FormatFileSize(int64(len([]byte(content)))),
					UploadedAt:      util.NowRFC3339(),
					Status:          "processing",
					Path:            destination,
					ContentPreview:  util.BuildContentPreviewFromText(content),
				}
				uploaded, err := appService.IndexDocument(document)
				if err != nil {
					_ = os.Remove(destination)
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("文本文档《%s》上传成功。", uploaded.Name),
					map[string]any{"uploaded": uploaded, "knowledgeBaseId": resolvedKnowledgeBaseID},
				), nil
			},
		},
		{
			Name:        "upload_document",
			Description: "向指定知识库上传文档。参数 knowledgeBaseId、fileName、contentBase64 为必填。",
			InputSchema: objectSchema(
				map[string]any{
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID"},
					"fileName":        map[string]any{"type": "string", "description": "文件名，需带扩展名"},
					"contentBase64":   map[string]any{"type": "string", "description": "文件内容的 Base64 编码"},
				},
				[]string{"knowledgeBaseId", "fileName", "contentBase64"},
			),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				fileName, err := requiredStringArg(args, "fileName")
				if err != nil {
					return ToolCallResult{}, err
				}
				contentBase64, err := requiredStringArg(args, "contentBase64")
				if err != nil {
					return ToolCallResult{}, err
				}
				resolvedKnowledgeBaseID, err := appService.ResolveKnowledgeBaseID(knowledgeBaseID)
				if err != nil {
					return ToolCallResult{}, err
				}
				decoded, err := base64.StdEncoding.DecodeString(contentBase64)
				if err != nil {
					return ToolCallResult{}, errInvalidContentBase64(fileName)
				}
				if len(decoded) == 0 {
					return ToolCallResult{}, fmt.Errorf("decoded file content is empty")
				}
				if err := validateUploadFileName(fileName, appService.GetConfig()); err != nil {
					return ToolCallResult{}, err
				}
				storedName := fmt.Sprintf("%d_%s", util.NowUnixNano(), util.SanitizeFilename(fileName))
				destination := filepath.Join("data/uploads", storedName)
				if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
					return ToolCallResult{}, fmt.Errorf("create upload directory: %w", err)
				}
				if err := os.WriteFile(destination, decoded, 0o644); err != nil {
					return ToolCallResult{}, fmt.Errorf("write uploaded file: %w", err)
				}
				document := model.Document{
					ID:              util.NextID("doc"),
					KnowledgeBaseID: resolvedKnowledgeBaseID,
					Name:            fileName,
					Size:            int64(len(decoded)),
					SizeLabel:       util.FormatFileSize(int64(len(decoded))),
					UploadedAt:      util.NowRFC3339(),
					Status:          "processing",
					Path:            destination,
					ContentPreview:  util.ExtractContentPreview(destination),
				}
				uploaded, err := appService.IndexDocument(document)
				if err != nil {
					_ = os.Remove(destination)
					return ToolCallResult{}, wrapBinaryUploadParseError(fileName, err)
				}
				return NewTextResult(
					fmt.Sprintf("文档《%s》上传成功。", uploaded.Name),
					map[string]any{"uploaded": uploaded, "knowledgeBaseId": resolvedKnowledgeBaseID},
				), nil
			},
		},
	}
}

func DefaultRegistry(appService *service.AppService) *ToolRegistry {
	return NewToolRegistry(NewReadOnlyTools(appService)...)
}

func emptyObjectSchema() map[string]any {
	return objectSchema(map[string]any{}, []string{})
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func requiredStringPropertySchema(name, description string) map[string]any {
	return requiredStringPropertiesSchema(map[string]string{name: description})
}

func requiredStringPropertiesSchema(properties map[string]string) map[string]any {
	schemaProperties := make(map[string]any, len(properties))
	required := make([]string, 0, len(properties))
	for name, description := range properties {
		schemaProperties[name] = map[string]any{
			"type":        "string",
			"description": description,
		}
		required = append(required, name)
	}
	return objectSchema(schemaProperties, required)
}

func embeddingModelConfigFromAppConfig(cfg model.AppConfig) model.EmbeddingModelConfig {
	return model.EmbeddingModelConfig{
		Provider: cfg.Embedding.Provider,
		BaseURL:  cfg.Embedding.BaseURL,
		Model:    cfg.Embedding.Model,
		APIKey:   cfg.Embedding.APIKey,
	}
}

func modelNowRFC3339() string {
	return util.NowRFC3339()
}

func validateUploadFileName(fileName string, cfg model.AppConfig) error {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName)))
	allowed := map[string]struct{}{
		".txt": {},
		".md":  {},
		".pdf": {},
	}
	if service.IsSensitiveStructuredFileExtension(ext) {
		if !service.IsLocalOllamaConfig(cfg.Chat, cfg.Embedding) {
			return fmt.Errorf("sensitive structured file type %s requires local ollama for both chat and embedding", ext)
		}
		allowed[ext] = struct{}{}
	}
	if _, ok := allowed[ext]; !ok {
		if ext == "" {
			return fmt.Errorf("unsupported file type: missing extension, allowed types are .txt, .md, .pdf")
		}
		return fmt.Errorf("unsupported file type: %s, allowed types are .txt, .md, .pdf", ext)
	}
	return nil
}

func validateTextUploadFileName(fileName string) error {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName)))
	allowed := map[string]struct{}{
		".txt": {},
		".md":  {},
		".csv": {},
	}
	if _, ok := allowed[ext]; !ok {
		if ext == "" {
			return fmt.Errorf("unsupported text upload type: missing extension, allowed types are .txt, .md, .csv")
		}
		return fmt.Errorf("unsupported text upload type: %s, allowed types are .txt, .md, .csv", ext)
	}
	return nil
}

func errInvalidContentBase64(fileName string) error {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName)))
	switch ext {
	case ".pdf":
		return fmt.Errorf("invalid contentBase64: PDF 必须上传真实 PDF 文件字节的 Base64，而不是纯文本内容")
	case ".xlsx":
		return fmt.Errorf("invalid contentBase64: XLSX 必须上传真实 Excel 文件字节的 Base64，而不是表格文本内容")
	default:
		return fmt.Errorf("invalid contentBase64")
	}
}

func wrapBinaryUploadParseError(fileName string, err error) error {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName)))
	message := err.Error()
	switch {
	case ext == ".xlsx" && strings.Contains(message, "zip: not a valid zip file"):
		return fmt.Errorf("extract uploaded document text: 你提供的不是合法 Excel .xlsx 二进制文件，.xlsx 本质上是 zip 压缩格式，请上传真实文件字节的 Base64")
	case ext == ".pdf":
		return fmt.Errorf("extract uploaded document text: PDF 解析失败，请确认上传的是合法 PDF 文件字节的 Base64")
	default:
		return err
	}
}

func optionalStringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func requiredStringArg(args map[string]any, key string) (string, error) {
	if args == nil {
		return "", fmt.Errorf("missing arguments")
	}
	value, _ := args[key].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}
