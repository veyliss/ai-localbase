package mcp

import (
	"ai-localbase/internal/model"
	"context"
	"fmt"
)

func newKnowledgeBaseTools(appService AppServiceReader) []ToolDefinition {
	return []ToolDefinition{
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
						"id":                  kb.ID,
						"name":                kb.Name,
						"description":         kb.Description,
						"tags":                append([]string(nil), kb.Tags...),
						"documentCount":       len(kb.Documents),
						"createdAt":           kb.CreatedAt,
						"updatedAt":           kb.UpdatedAt,
						"currentIndexVersion": kb.CurrentIndexVersion,
					})
				}
				return NewTextResult(formatKnowledgeBaseListText(knowledgeBases), map[string]any{"items": items}), nil
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
				return NewTextResult(formatDocumentListText(knowledgeBaseID, documents), map[string]any{"items": items}), nil
			},
		},
		{
			Name:        "get_document_detail",
			Description: "返回指定文档的索引诊断、摘要、原文预览和 chunk 预览。参数 knowledgeBaseId、documentId 为必填。",
			InputSchema: requiredStringPropertiesSchema(map[string]string{
				"knowledgeBaseId": "知识库 ID",
				"documentId":      "文档 ID",
			}),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
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
				detail, err := appService.GetDocumentDetail(knowledgeBaseID, documentID, "")
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("文档《%s》共有 %d 个 chunk，结构化行 chunk %d 个。",
						detail.Document.Name,
						detail.Diagnostics.ChunkCount,
						detail.Diagnostics.StructuredRowCount,
					),
					map[string]any{"detail": buildSafeMCPDocumentDetail(detail)},
				), nil
			},
		},
		{
			Name:        "summarize_document",
			Description: "返回文档摘要、索引诊断和关键 chunk 预览。参数 knowledgeBaseId、documentId 为必填。",
			InputSchema: requiredStringPropertiesSchema(map[string]string{
				"knowledgeBaseId": "知识库 ID",
				"documentId":      "文档 ID",
			}),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
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
				detail, err := appService.GetDocumentDetail(knowledgeBaseID, documentID, "")
				if err != nil {
					return ToolCallResult{}, err
				}
				summary := documentSummaryText(detail)
				warnings := []string{}
				if !detail.Diagnostics.RawContentAvailable {
					warnings = append(warnings, "文档原文不可用，摘要只能基于已保存的索引信息。")
				}
				if detail.Diagnostics.ChunkCount == 0 {
					warnings = append(warnings, "文档没有可检索 chunk，建议重建索引。")
				}
				return ToolCallResult{
					Summary: fmt.Sprintf("文档《%s》摘要完成，chunk %d 个。", detail.Document.Name, detail.Diagnostics.ChunkCount),
					Content: []ToolContent{{Type: "text", Text: summary}},
					Data: map[string]any{
						"knowledgeBaseId": knowledgeBaseID,
						"document":        buildSafeMCPDocument(detail.Document),
						"diagnostics":     buildSafeMCPDocumentDiagnostics(detail.Diagnostics),
						"summary":         summary,
						"chunks":          previewDocumentChunks(detail.Chunks, 5),
					},
					Warnings: warnings,
					NextActions: []string{
						"需要验证某个问题时调用 answer_with_sources。",
						"需要查看完整命中链路时调用 debug_retrieval。",
					},
				}, nil
			},
		},
		{
			Name:        "create_knowledge_base",
			Description: "创建新的知识库。参数 name 为必填，description 和 tags 为选填。",
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
					"tags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "用于分类的标签列表",
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
				tags, err := optionalStringSliceArg(args, "tags")
				if err != nil {
					return ToolCallResult{}, err
				}
				knowledgeBase, err := appService.CreateKnowledgeBase(model.KnowledgeBaseInput{
					Name:        name,
					Description: description,
					Tags:        tags,
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
					map[string]any{"document": buildSafeMCPDocument(removedDocument)},
				), nil
			},
		},
		{
			Name:        "reindex_document",
			Description: "重建指定文档索引。参数 knowledgeBaseId 与 documentId 为必填。该操作会重新解析文件、重建 chunk 并刷新向量索引。",
			InputSchema: requiredStringPropertiesSchema(map[string]string{
				"knowledgeBaseId": "知识库 ID",
				"documentId":      "文档 ID",
			}),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				documentID, err := requiredStringArg(args, "documentId")
				if err != nil {
					return ToolCallResult{}, err
				}
				document, err := reindexDocumentWithContext(appService, ctx, knowledgeBaseID, documentID)
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("文档《%s》已完成重建索引，当前状态为 %s。", document.Name, document.Status),
					map[string]any{"document": buildSafeMCPDocument(document), "knowledgeBaseId": knowledgeBaseID},
				), nil
			},
		},
	}
}
