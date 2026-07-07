package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"ai-localbase/internal/model"
	"ai-localbase/internal/service"
	"ai-localbase/internal/util"
)

type AppServiceReader interface {
	GetConfig() model.AppConfig
	ListKnowledgeBases() []model.KnowledgeBase
	GetKnowledgeBaseDocuments(id string) ([]model.Document, error)
	GetDocumentDetail(knowledgeBaseID, documentID, focusChunkID string) (model.DocumentDetailResponse, error)
	GetKnowledgeBaseHealth(knowledgeBaseID string) (model.KnowledgeBaseHealthResponse, error)
	ReindexDocument(knowledgeBaseID, documentID string) (model.Document, error)
	ListConversations() ([]model.ConversationListItem, error)
	GetConversation(id string) (*model.Conversation, error)
	BuildRetrievalContext(req model.ChatCompletionRequest) (string, []map[string]string, error)
	DebugRetrieve(req model.RetrievalDebugRequest) (model.RetrievalDebugResponse, error)
	TryBuildStructuredDataAnswer(req model.ChatCompletionRequest) (string, []map[string]string, bool, error)
	ListEvalRuns(knowledgeBaseID, datasetID string) []model.EvalRunSummary
	GenerateEvalDataset(req model.GenerateEvalDatasetRequest) (model.GenerateEvalDatasetResponse, error)
	AddEvalDatasetCandidate(req model.AddEvalDatasetCandidateRequest) (model.AddEvalDatasetCandidateResponse, error)
	CreateKnowledgeBase(req model.KnowledgeBaseInput) (model.KnowledgeBase, error)
	DeleteKnowledgeBase(id string) (int, error)
	DeleteDocument(knowledgeBaseID, documentID string) (model.Document, error)
	SaveConversation(req model.SaveConversationRequest) (*model.Conversation, error)
	DeleteConversation(id string) error
	StageInlineUpload(fileName string, content []byte, source string) (model.StagedUpload, error)
	RegisterStagedUpload(uploadID, knowledgeBaseID, fileName string) (model.Document, error)
	StartMCPImportJob(req model.MCPStartImportJobRequest) (model.MCPJob, error)
	GetMCPJobStatus(jobID string) (model.MCPJob, error)
	CancelMCPJob(jobID string) (model.MCPJob, error)
	ListRecentMCPJobs(limit int) []model.MCPJob
}

func NewReadOnlyTools(appService AppServiceReader) []ToolDefinition {
	return []ToolDefinition{
		{
			Name:            "get_mcp_capabilities",
			Description:     "返回 MCP Server 能力摘要，包括版本、协议、工具数量、权限分布、启用状态和基础配置。",
			InputSchema:     emptyObjectSchema(),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				tools := NewReadOnlyTools(appService)
				capabilities := buildMCPCapabilities(appService.GetConfig(), tools)
				return NewTextResult(
					fmt.Sprintf("MCP Server %s 当前提供 %d 个工具。", serverVersion, capabilities["toolCount"]),
					map[string]any{"capabilities": capabilities},
				), nil
			},
		},
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
					map[string]any{"config": buildSafeConfigSummary(cfg)},
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
					map[string]any{"detail": detail},
				), nil
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
			Name:        "search_document",
			Description: "按单个文档执行检索并返回命中文本与来源。参数 documentId 与 query 为必填。",
			InputSchema: requiredStringPropertiesSchema(map[string]string{
				"documentId": "文档 ID",
				"query":      "检索问题",
			}),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				documentID, err := requiredStringArg(args, "documentId")
				if err != nil {
					return ToolCallResult{}, err
				}
				query, err := requiredStringArg(args, "query")
				if err != nil {
					return ToolCallResult{}, err
				}
				contextText, sources, err := appService.BuildRetrievalContext(model.ChatCompletionRequest{
					DocumentID: documentID,
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
				return NewTextResult(text, map[string]any{"sources": sources, "documentId": documentID, "query": query}), nil
			},
		},
		{
			Name:        "query_structured_data",
			Description: "对 CSV / XLSX 结构化文档执行确定性查询，可用于预览、筛选、最大/最小值、平均值和分布统计。参数 query 必填，documentId 或 knowledgeBaseId 至少提供一个。",
			InputSchema: objectSchema(
				map[string]any{
					"query":           map[string]any{"type": "string", "description": "结构化数据问题，例如：展示数据表格、筛选城市是上海的数据、薪资最高的是谁、按城市统计分布"},
					"documentId":      map[string]any{"type": "string", "description": "文档 ID，推荐提供以避免多表歧义"},
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID；当知识库只有一个结构化文档时可使用"},
				},
				[]string{"query"},
			),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				query, err := requiredStringArg(args, "query")
				if err != nil {
					return ToolCallResult{}, err
				}
				documentID := optionalStringArg(args, "documentId")
				knowledgeBaseID := optionalStringArg(args, "knowledgeBaseId")
				if documentID == "" && knowledgeBaseID == "" {
					return ToolCallResult{}, fmt.Errorf("documentId or knowledgeBaseId is required")
				}
				content, sources, ok, err := appService.TryBuildStructuredDataAnswer(model.ChatCompletionRequest{
					KnowledgeBaseID: knowledgeBaseID,
					DocumentID:      documentID,
					Messages: []model.ChatMessage{{
						Role:    "user",
						Content: query,
					}},
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				if !ok {
					return NewTextResult(
						"未能执行结构化数据查询。请确认目标文档是 CSV / XLSX，且问题属于预览、筛选、统计、最大/最小值或平均值类型。",
						map[string]any{"documentId": documentID, "knowledgeBaseId": knowledgeBaseID, "query": query, "matched": false},
					), nil
				}
				return NewTextResult(content, map[string]any{"sources": sources, "documentId": documentID, "knowledgeBaseId": knowledgeBaseID, "query": query, "matched": true}), nil
			},
		},
		{
			Name:        "debug_retrieval",
			Description: "执行检索调试，返回命中 chunk、分数、低置信标记、结构化确定性补全状态和评测候选。参数 query 必填，knowledgeBaseId 或 documentId 至少提供一个。",
			InputSchema: objectSchema(
				map[string]any{
					"query":           map[string]any{"type": "string", "description": "检索调试问题"},
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID，可选"},
					"documentId":      map[string]any{"type": "string", "description": "文档 ID，可选"},
					"topK":            map[string]any{"type": "integer", "description": "最多返回多少个命中 chunk，默认使用服务端默认值"},
				},
				[]string{"query"},
			),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				query, err := requiredStringArg(args, "query")
				if err != nil {
					return ToolCallResult{}, err
				}
				knowledgeBaseID := optionalStringArg(args, "knowledgeBaseId")
				documentID := optionalStringArg(args, "documentId")
				if knowledgeBaseID == "" && documentID == "" {
					return ToolCallResult{}, fmt.Errorf("knowledgeBaseId or documentId is required")
				}
				response, err := appService.DebugRetrieve(model.RetrievalDebugRequest{
					Query:           query,
					KnowledgeBaseID: knowledgeBaseID,
					DocumentID:      documentID,
					TopK:            optionalIntArg(args, "topK"),
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				summary := fmt.Sprintf("检索调试完成：命中 %d 个 chunk，耗时 %d ms。", response.Count, response.ElapsedMs)
				if response.DeterministicUsed {
					summary += " 已使用结构化确定性补全。"
				}
				if response.LowConfidence {
					summary += " 当前结果低置信，可沉淀为评测样本。"
				}
				return NewTextResult(summary, map[string]any{"debug": response}), nil
			},
		},
		{
			Name:        "answer_with_sources",
			Description: "基于知识库或文档生成带来源的答案草稿。参数 query 必填，knowledgeBaseId 或 documentId 至少提供一个。",
			InputSchema: objectSchema(
				map[string]any{
					"query":           map[string]any{"type": "string", "description": "用户问题"},
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID，可选"},
					"documentId":      map[string]any{"type": "string", "description": "文档 ID，可选"},
				},
				[]string{"query"},
			),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				query, err := requiredStringArg(args, "query")
				if err != nil {
					return ToolCallResult{}, err
				}
				knowledgeBaseID := optionalStringArg(args, "knowledgeBaseId")
				documentID := optionalStringArg(args, "documentId")
				if knowledgeBaseID == "" && documentID == "" {
					return ToolCallResult{}, fmt.Errorf("knowledgeBaseId or documentId is required")
				}

				chatReq := model.ChatCompletionRequest{
					KnowledgeBaseID: knowledgeBaseID,
					DocumentID:      documentID,
					Messages: []model.ChatMessage{{
						Role:    "user",
						Content: query,
					}},
					Embedding: embeddingModelConfigFromAppConfig(appService.GetConfig()),
				}
				answer, sources, structuredUsed, err := appService.TryBuildStructuredDataAnswer(chatReq)
				if err != nil {
					return ToolCallResult{}, err
				}
				answer = strings.TrimSpace(answer)
				mode := "structured"
				if !structuredUsed {
					mode = "retrieval_context"
					answer, sources, err = appService.BuildRetrievalContext(chatReq)
					if err != nil {
						return ToolCallResult{}, err
					}
					answer = strings.TrimSpace(answer)
				}
				warnings := []string{}
				if answer == "" {
					answer = "未检索到可用于回答的内容。"
					warnings = append(warnings, "未找到相关证据，建议换用更具体的问题或扩大检索范围。")
				}
				if len(sources) == 0 {
					warnings = append(warnings, "本次结果没有可引用来源。")
				}

				return ToolCallResult{
					Summary: fmt.Sprintf("已生成带来源答案草稿，模式为 %s，来源 %d 条。", mode, len(sources)),
					Content: []ToolContent{{Type: "text", Text: answer}},
					Data: map[string]any{
						"answer":          answer,
						"sources":         sources,
						"mode":            mode,
						"query":           query,
						"knowledgeBaseId": knowledgeBaseID,
						"documentId":      documentID,
					},
					Warnings: warnings,
					NextActions: []string{
						"需要排查命中质量时调用 debug_retrieval。",
						"需要沉淀回归样本时调用 create_eval_case_from_query。",
					},
				}, nil
			},
		},
		{
			Name:            "inspect_knowledge_base_quality",
			Description:     "聚合知识库索引健康、最近评估结果和改进建议。参数 knowledgeBaseId 为必填。",
			InputSchema:     requiredStringPropertySchema("knowledgeBaseId", "知识库 ID"),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				health, err := appService.GetKnowledgeBaseHealth(knowledgeBaseID)
				if err != nil {
					return ToolCallResult{}, err
				}
				evalRuns := appService.ListEvalRuns(knowledgeBaseID, "")
				var latestEvalRun any
				if len(evalRuns) > 0 {
					latestEvalRun = evalRuns[0]
				}
				insights := buildKnowledgeBaseQualityInsights(health, evalRuns)
				warnings := []string{}
				if health.Status != "healthy" {
					warnings = append(warnings, fmt.Sprintf("知识库健康状态为 %s。", health.Status))
				}

				return ToolCallResult{
					Summary: fmt.Sprintf("知识库《%s》健康分 %d，状态 %s，最近评估 %d 次。", health.Name, health.Score, health.Status, len(evalRuns)),
					Content: []ToolContent{{Type: "text", Text: strings.Join(insights, "\n")}},
					Data: map[string]any{
						"health":        health,
						"latestEvalRun": latestEvalRun,
						"evalRuns":      evalRuns,
						"insights":      insights,
					},
					Warnings: warnings,
					NextActions: []string{
						"需要定位单个问题时调用 debug_retrieval。",
						"需要补充覆盖时调用 create_eval_case_from_query 或 generate_eval_dataset。",
					},
				}, nil
			},
		},
		{
			Name:        "compare_retrieval_modes",
			Description: "对同一问题比较 dense 与 hybrid 检索结果。参数 query 必填，knowledgeBaseId 或 documentId 至少提供一个。",
			InputSchema: objectSchema(
				map[string]any{
					"query":           map[string]any{"type": "string", "description": "检索问题"},
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID，可选"},
					"documentId":      map[string]any{"type": "string", "description": "文档 ID，可选"},
					"topK":            map[string]any{"type": "integer", "description": "每种模式最多返回多少个命中，默认 5"},
				},
				[]string{"query"},
			),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				query, err := requiredStringArg(args, "query")
				if err != nil {
					return ToolCallResult{}, err
				}
				knowledgeBaseID := optionalStringArg(args, "knowledgeBaseId")
				documentID := optionalStringArg(args, "documentId")
				if knowledgeBaseID == "" && documentID == "" {
					return ToolCallResult{}, fmt.Errorf("knowledgeBaseId or documentId is required")
				}
				topK := optionalIntArg(args, "topK")
				if topK <= 0 {
					topK = 5
				}
				dense, err := appService.DebugRetrieve(model.RetrievalDebugRequest{
					Query:           query,
					KnowledgeBaseID: knowledgeBaseID,
					DocumentID:      documentID,
					TopK:            topK,
					SearchMode:      "dense",
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				hybrid, err := appService.DebugRetrieve(model.RetrievalDebugRequest{
					Query:           query,
					KnowledgeBaseID: knowledgeBaseID,
					DocumentID:      documentID,
					TopK:            topK,
					SearchMode:      "hybrid",
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				recommendation := recommendRetrievalMode(dense, hybrid)
				return ToolCallResult{
					Summary: fmt.Sprintf("检索模式对比完成：dense 命中 %d，hybrid 命中 %d，建议 %s。", dense.Count, hybrid.Count, recommendation),
					Content: []ToolContent{{Type: "text", Text: buildRetrievalModeComparisonText(dense, hybrid, recommendation)}},
					Data: map[string]any{
						"query":           query,
						"knowledgeBaseId": knowledgeBaseID,
						"documentId":      documentID,
						"recommendation":  recommendation,
						"dense":           dense,
						"hybrid":          hybrid,
					},
					Warnings: retrievalComparisonWarnings(dense, hybrid),
					NextActions: []string{
						"如果两种模式均低置信，建议补充文档或创建评测样本。",
						"如果 hybrid 明显更稳，可在检索策略中启用混合检索。",
					},
				}, nil
			},
		},
		{
			Name:        "create_eval_case_from_query",
			Description: "根据一次检索问题创建待审核评测样本。参数 query 必填，knowledgeBaseId 或 documentId 至少提供一个。",
			InputSchema: objectSchema(
				map[string]any{
					"query":           map[string]any{"type": "string", "description": "需要沉淀为评测样本的问题"},
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID，可选"},
					"documentId":      map[string]any{"type": "string", "description": "文档 ID，可选"},
					"topK":            map[string]any{"type": "integer", "description": "生成样本时参考的命中数量，默认 5"},
				},
				[]string{"query"},
			),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				query, err := requiredStringArg(args, "query")
				if err != nil {
					return ToolCallResult{}, err
				}
				knowledgeBaseID := optionalStringArg(args, "knowledgeBaseId")
				documentID := optionalStringArg(args, "documentId")
				if knowledgeBaseID == "" && documentID == "" {
					return ToolCallResult{}, fmt.Errorf("knowledgeBaseId or documentId is required")
				}
				topK := optionalIntArg(args, "topK")
				if topK <= 0 {
					topK = 5
				}
				debug, err := appService.DebugRetrieve(model.RetrievalDebugRequest{
					Query:           query,
					KnowledgeBaseID: knowledgeBaseID,
					DocumentID:      documentID,
					TopK:            topK,
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				candidate := buildEvalCaseFromDebugResponse(debug)
				response, err := appService.AddEvalDatasetCandidate(model.AddEvalDatasetCandidateRequest{
					KnowledgeBaseID: knowledgeBaseID,
					DocumentID:      documentID,
					Item:            candidate,
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				return ToolCallResult{
					Summary: fmt.Sprintf("已创建待审核评测样本：%s。", response.Item.ID),
					Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("问题：%s\n答案草稿：%s", response.Item.Question, response.Item.Answer)}},
					Data: map[string]any{
						"candidate": response.Item,
						"dataset":   response.Dataset,
						"created":   response.Created,
						"debug":     debug,
					},
					Warnings: evalCaseWarningsFromDebug(debug),
					NextActions: []string{
						"在评估数据集中审核并启用该样本。",
						"审核通过后运行评估，观察 Hit Rate、MRR 和证据支撑率。",
					},
				}, nil
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
						"document":        detail.Document,
						"diagnostics":     detail.Diagnostics,
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
			Name:        "generate_eval_dataset",
			Description: "从知识库或指定文档生成 RAG 评估数据集。参数 knowledgeBaseId、documentId 可选，maxPerDocument 可选。",
			InputSchema: objectSchema(
				map[string]any{
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID，可选"},
					"documentId":      map[string]any{"type": "string", "description": "文档 ID，可选"},
					"maxPerDocument":  map[string]any{"type": "integer", "description": "每个文档最多生成多少条，默认 5，最大 20"},
				},
				[]string{},
			),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				response, err := appService.GenerateEvalDataset(model.GenerateEvalDatasetRequest{
					KnowledgeBaseID: optionalStringArg(args, "knowledgeBaseId"),
					DocumentID:      optionalStringArg(args, "documentId"),
					MaxPerDocument:  optionalIntArg(args, "maxPerDocument"),
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("已生成 %d 条评估样本，覆盖 %d 个文档。", response.Count, response.DocumentCount),
					map[string]any{"dataset": response},
				), nil
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
			Name:        "reindex_document",
			Description: "重建指定文档索引。参数 knowledgeBaseId 与 documentId 为必填。该操作会重新解析文件、重建 chunk 并刷新向量索引。",
			InputSchema: requiredStringPropertiesSchema(map[string]string{
				"knowledgeBaseId": "知识库 ID",
				"documentId":      "文档 ID",
			}),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
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
				document, err := appService.ReindexDocument(knowledgeBaseID, documentID)
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("文档《%s》已完成重建索引，当前状态为 %s。", document.Name, document.Status),
					map[string]any{"document": document, "knowledgeBaseId": knowledgeBaseID},
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
				if int64(len(content)) > maxInlineUploadBytes {
					return ToolCallResult{}, fmt.Errorf("inline text upload too large: current=%s, max=%s; please POST file stream to /api/uploads first, then call register_staged_upload", util.FormatFileSize(int64(len(content))), util.FormatFileSize(maxInlineUploadBytes))
				}
				if err := validateTextUploadFileName(fileName, appService.GetConfig()); err != nil {
					return ToolCallResult{}, err
				}
				staged, err := appService.StageInlineUpload(fileName, []byte(content), "mcp-text")
				if err != nil {
					return ToolCallResult{}, err
				}
				uploaded, err := appService.RegisterStagedUpload(staged.ID, knowledgeBaseID, fileName)
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("文本文档《%s》上传成功。", uploaded.Name),
					map[string]any{"uploaded": uploaded, "knowledgeBaseId": uploaded.KnowledgeBaseID, "stagedUploadId": staged.ID},
				), nil
			},
		},
		{
			Name:        "upload_document",
			Description: "向指定知识库上传文档。参数 knowledgeBaseId、fileName、contentBase64 为必填。仅适用于小文件，大文件请先走 HTTP /api/uploads 暂存再调用 register_staged_upload。",
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
				if err := validateUploadFileName(fileName, appService.GetConfig()); err != nil {
					return ToolCallResult{}, err
				}
				decoded, err := base64.StdEncoding.DecodeString(contentBase64)
				if err != nil {
					return ToolCallResult{}, errInvalidContentBase64(fileName)
				}
				if len(decoded) == 0 {
					return ToolCallResult{}, fmt.Errorf("decoded file content is empty")
				}
				if int64(len(decoded)) > maxInlineUploadBytes {
					return ToolCallResult{}, fmt.Errorf("inline upload too large: current=%s, max=%s; please POST file stream to /api/uploads first, then call register_staged_upload", util.FormatFileSize(int64(len(decoded))), util.FormatFileSize(maxInlineUploadBytes))
				}
				staged, err := appService.StageInlineUpload(fileName, decoded, "mcp-inline")
				if err != nil {
					return ToolCallResult{}, err
				}
				uploaded, err := appService.RegisterStagedUpload(staged.ID, knowledgeBaseID, fileName)
				if err != nil {
					return ToolCallResult{}, wrapBinaryUploadParseError(fileName, err)
				}
				return NewTextResult(
					fmt.Sprintf("文档《%s》上传成功。", uploaded.Name),
					map[string]any{"uploaded": uploaded, "knowledgeBaseId": uploaded.KnowledgeBaseID, "stagedUploadId": staged.ID},
				), nil
			},
		},
		{
			Name:        "register_staged_upload",
			Description: "基于已暂存的 uploadId 将文件注册到指定知识库。适合大文件上传场景。参数 uploadId、knowledgeBaseId 为必填，fileName 为选填。",
			InputSchema: objectSchema(
				map[string]any{
					"uploadId":        map[string]any{"type": "string", "description": "通过 HTTP /api/uploads 返回的上传 ID"},
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID"},
					"fileName":        map[string]any{"type": "string", "description": "可选，注册后的文件名"},
				},
				[]string{"uploadId", "knowledgeBaseId"},
			),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				uploadID, err := requiredStringArg(args, "uploadId")
				if err != nil {
					return ToolCallResult{}, err
				}
				knowledgeBaseID, err := requiredStringArg(args, "knowledgeBaseId")
				if err != nil {
					return ToolCallResult{}, err
				}
				fileName := optionalStringArg(args, "fileName")
				uploaded, err := appService.RegisterStagedUpload(uploadID, knowledgeBaseID, fileName)
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("暂存文件《%s》已注册到知识库。", uploaded.Name),
					map[string]any{"uploaded": uploaded, "knowledgeBaseId": uploaded.KnowledgeBaseID, "uploadId": uploadID},
				), nil
			},
		},
		{
			Name:        "start_import_job",
			Description: "启动异步文本文档导入任务。参数 knowledgeBaseId、fileName 为必填，content 为文本内容。",
			InputSchema: objectSchema(
				map[string]any{
					"knowledgeBaseId": map[string]any{"type": "string", "description": "知识库 ID"},
					"fileName":        map[string]any{"type": "string", "description": "文件名，需带扩展名"},
					"content":         map[string]any{"type": "string", "description": "纯文本内容；留空会创建失败状态用于排查"},
				},
				[]string{"knowledgeBaseId", "fileName"},
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
				job, err := appService.StartMCPImportJob(model.MCPStartImportJobRequest{
					KnowledgeBaseID: knowledgeBaseID,
					FileName:        fileName,
					Content:         optionalStringArg(args, "content"),
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("导入任务已启动：%s。", job.ID),
					map[string]any{"job": job},
				), nil
			},
		},
		{
			Name:            "get_job_status",
			Description:     "查询 MCP 长任务状态。参数 jobId 为必填。",
			InputSchema:     requiredStringPropertySchema("jobId", "Job ID"),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				jobID, err := requiredStringArg(args, "jobId")
				if err != nil {
					return ToolCallResult{}, err
				}
				job, err := appService.GetMCPJobStatus(jobID)
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("任务 %s 当前状态为 %s，进度 %d%%。", job.ID, job.Status, job.Progress),
					map[string]any{"job": job},
				), nil
			},
		},
		{
			Name:            "cancel_job",
			Description:     "取消 MCP 长任务。参数 jobId 为必填。",
			InputSchema:     requiredStringPropertySchema("jobId", "Job ID"),
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				jobID, err := requiredStringArg(args, "jobId")
				if err != nil {
					return ToolCallResult{}, err
				}
				job, err := appService.CancelMCPJob(jobID)
				if err != nil {
					return ToolCallResult{}, err
				}
				return NewTextResult(
					fmt.Sprintf("任务 %s 当前状态为 %s。", job.ID, job.Status),
					map[string]any{"job": job},
				), nil
			},
		},
		{
			Name:        "list_recent_jobs",
			Description: "列出最近 MCP 长任务。参数 limit 可选，默认 20。",
			InputSchema: objectSchema(
				map[string]any{
					"limit": map[string]any{"type": "integer", "description": "最多返回多少个 job，默认 20，最大 20"},
				},
				[]string{},
			),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				jobs := appService.ListRecentMCPJobs(optionalIntArg(args, "limit"))
				return NewTextResult(
					fmt.Sprintf("最近共有 %d 个 MCP 任务。", len(jobs)),
					map[string]any{"jobs": jobs},
				), nil
			},
		},
	}
}

func DefaultRegistry(appService *service.AppService) *ToolRegistry {
	registry := NewToolRegistry(NewReadOnlyTools(appService)...)
	// The get_mcp_capabilities defined in NewReadOnlyTools recomputes the tool
	// list statically, so its toolCount drifts once callers register extra
	// tools (e.g. search_web in main.go). Rebind it to the live registry so
	// the summary always matches tools/list.
	registry.Register(newRegistryCapabilitiesTool(appService, registry))
	return registry
}

// newRegistryCapabilitiesTool returns a get_mcp_capabilities definition bound
// to the live registry, so the reported summary reflects every registered
// tool rather than only the NewReadOnlyTools set.
func newRegistryCapabilitiesTool(cfgProvider TokenProvider, registry *ToolRegistry) ToolDefinition {
	return ToolDefinition{
		Name:            "get_mcp_capabilities",
		Description:     "返回 MCP Server 能力摘要，包括版本、协议、工具数量、权限分布、启用状态和基础配置。",
		InputSchema:     emptyObjectSchema(),
		ReadOnly:        true,
		PermissionLevel: ToolPermissionReadOnly,
		Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
			_ = ctx
			tools := registry.List()
			capabilities := buildMCPCapabilities(cfgProvider.GetConfig(), tools)
			return NewTextResult(
				fmt.Sprintf("MCP Server %s 当前提供 %d 个工具。", serverVersion, capabilities["toolCount"]),
				map[string]any{"capabilities": capabilities},
			), nil
		},
	}
}

func buildKnowledgeBaseQualityInsights(health model.KnowledgeBaseHealthResponse, evalRuns []model.EvalRunSummary) []string {
	insights := make([]string, 0, 8)
	if health.Score >= 90 {
		insights = append(insights, "索引健康度较好，当前更适合继续补充评测覆盖。")
	} else {
		insights = append(insights, fmt.Sprintf("索引健康分为 %d，建议先处理未索引、失败或需要重建的文档。", health.Score))
	}
	if health.Metrics.DocumentCount == 0 {
		insights = append(insights, "知识库暂无文档，需要先导入内容。")
	}
	if health.Metrics.FailedCount > 0 {
		insights = append(insights, fmt.Sprintf("存在 %d 个索引失败文档，优先查看错误并重新导入。", health.Metrics.FailedCount))
	}
	if health.Metrics.EmptyContentCount > 0 {
		insights = append(insights, fmt.Sprintf("存在 %d 个原文不可用文档，可能影响摘要、引用和重建索引。", health.Metrics.EmptyContentCount))
	}
	if health.Metrics.VectorCount == 0 && health.Metrics.DocumentCount > 0 {
		insights = append(insights, "当前没有向量索引，RAG 检索质量会明显受限。")
	}
	if len(health.Recommendations) > 0 {
		insights = append(insights, health.Recommendations...)
	}
	if len(evalRuns) == 0 {
		insights = append(insights, "暂无评估历史，建议先生成评测集并运行一次基线评估。")
		return insights
	}

	latest := evalRuns[0]
	metrics := latest.Metrics
	if metrics.TotalCases == 0 {
		insights = append(insights, "最近评估没有有效样本，建议检查评测集是否启用样本。")
		return insights
	}
	if metrics.HitRate < 0.8 {
		insights = append(insights, fmt.Sprintf("最近评估 Hit Rate 为 %s，建议检查漏召回问题。", formatPercent(metrics.HitRate)))
	}
	if metrics.MRR < 0.7 {
		insights = append(insights, fmt.Sprintf("最近评估 MRR 为 %s，建议优化重排策略或 chunk 粒度。", formatPercent(metrics.MRR)))
	}
	if metrics.LowConfidence > 0 {
		insights = append(insights, fmt.Sprintf("最近评估出现 %d 个低置信样本，建议沉淀为回归用例。", metrics.LowConfidence))
	}
	if metrics.EvidenceSupportRate > 0 && metrics.EvidenceSupportRate < 0.85 {
		insights = append(insights, fmt.Sprintf("证据支撑率为 %s，建议排查引用和答案依据是否一致。", formatPercent(metrics.EvidenceSupportRate)))
	}
	if metrics.LatencyP95Ms > 0 && metrics.LatencyP95Ms > 3000 {
		insights = append(insights, fmt.Sprintf("P95 延迟为 %d ms，建议压缩候选量或上下文长度。", metrics.LatencyP95Ms))
	}
	if len(insights) == 0 {
		insights = append(insights, "暂无明显质量风险。")
	}
	return insights
}

func formatPercent(value float64) string {
	return fmt.Sprintf("%.0f%%", value*100)
}

func recommendRetrievalMode(dense, hybrid model.RetrievalDebugResponse) string {
	denseScore := retrievalModeScore(dense)
	hybridScore := retrievalModeScore(hybrid)
	if hybridScore > denseScore+0.05 {
		return "hybrid"
	}
	if denseScore > hybridScore+0.05 {
		return "dense"
	}
	return "tie"
}

func retrievalModeScore(response model.RetrievalDebugResponse) float64 {
	score := response.Confidence.TopScore
	if response.Confidence.EvidenceCoverage > score {
		score = response.Confidence.EvidenceCoverage
	}
	if response.LowConfidence {
		score -= 0.2
	}
	if response.Count == 0 {
		score -= 0.3
	}
	if response.DeterministicUsed {
		score += 0.1
	}
	return score
}

func buildRetrievalModeComparisonText(dense, hybrid model.RetrievalDebugResponse, recommendation string) string {
	return strings.Join([]string{
		fmt.Sprintf("dense：命中 %d，置信 %s，TopScore %.3f，耗时 %d ms。", dense.Count, dense.Confidence.Status, dense.Confidence.TopScore, dense.ElapsedMs),
		fmt.Sprintf("hybrid：命中 %d，置信 %s，TopScore %.3f，耗时 %d ms。", hybrid.Count, hybrid.Confidence.Status, hybrid.Confidence.TopScore, hybrid.ElapsedMs),
		fmt.Sprintf("建议：%s。", recommendation),
	}, "\n")
}

func retrievalComparisonWarnings(dense, hybrid model.RetrievalDebugResponse) []string {
	warnings := []string{}
	if dense.LowConfidence && hybrid.LowConfidence {
		warnings = append(warnings, "dense 与 hybrid 均为低置信，建议补充文档内容或创建评测样本。")
	}
	if dense.Count == 0 || hybrid.Count == 0 {
		warnings = append(warnings, "至少一种检索模式没有命中结果。")
	}
	return warnings
}

func buildEvalCaseFromDebugResponse(debug model.RetrievalDebugResponse) model.EvalGroundTruthCase {
	if debug.EvalCandidate != nil {
		candidate := *debug.EvalCandidate
		if strings.TrimSpace(candidate.ID) == "" {
			candidate.ID = util.NextID("mcp-eval-case")
		}
		return candidate
	}

	answer := strings.TrimSpace(debug.ContextPreview)
	if answer == "" && len(debug.Items) > 0 {
		answer = debug.Items[0].Text
	}
	answer = truncateText(answer, 800)
	snippets := snippetsFromDebugItems(debug.Items, 3)
	if len(snippets) == 0 && answer != "" {
		snippets = []string{truncateText(answer, 120)}
	}
	return model.EvalGroundTruthCase{
		ID:              util.NextID("mcp-eval-case"),
		Question:        debug.Query,
		Answer:          answer,
		AnswerSnippets:  snippets,
		SourceDocuments: sourceDocumentsFromDebugItems(debug.Items, 5),
		AnswerType:      "retrieval-debug-candidate",
		Difficulty:      "medium",
		ReviewStatus:    "pending",
		Disabled:        true,
		Notes:           "created from MCP create_eval_case_from_query; review before enabling",
	}
}

func sourceDocumentsFromDebugItems(items []model.RetrievalDebugChunk, limit int) []model.EvalSourceDocument {
	if limit <= 0 {
		limit = 5
	}
	sources := make([]model.EvalSourceDocument, 0, min(limit, len(items)))
	seen := map[string]struct{}{}
	for _, item := range items {
		if strings.TrimSpace(item.DocumentID) == "" {
			continue
		}
		key := item.KnowledgeBaseID + "\x00" + item.DocumentID + "\x00" + item.ID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		sources = append(sources, model.EvalSourceDocument{
			KnowledgeBaseID: item.KnowledgeBaseID,
			DocumentID:      item.DocumentID,
			ChunkID:         item.ID,
		})
		if len(sources) >= limit {
			break
		}
	}
	return sources
}

func snippetsFromDebugItems(items []model.RetrievalDebugChunk, limit int) []string {
	if limit <= 0 {
		limit = 3
	}
	snippets := make([]string, 0, min(limit, len(items)))
	for _, item := range items {
		text := truncateText(strings.TrimSpace(item.Text), 120)
		if text == "" {
			continue
		}
		snippets = append(snippets, text)
		if len(snippets) >= limit {
			break
		}
	}
	return snippets
}

func evalCaseWarningsFromDebug(debug model.RetrievalDebugResponse) []string {
	warnings := []string{}
	if debug.LowConfidence {
		warnings = append(warnings, "该样本来自低置信检索结果，必须人工审核答案和证据。")
	}
	if len(debug.Items) == 0 {
		warnings = append(warnings, "检索没有命中 chunk，样本答案可能为空。")
	}
	return warnings
}

func documentSummaryText(detail model.DocumentDetailResponse) string {
	summary := strings.TrimSpace(detail.Summary)
	if summary != "" {
		return summary
	}
	parts := make([]string, 0, 3)
	if preview := strings.TrimSpace(detail.Document.ContentPreview); preview != "" {
		parts = append(parts, preview)
	}
	for _, chunk := range detail.Chunks {
		text := strings.TrimSpace(chunk.Text)
		if text == "" {
			continue
		}
		parts = append(parts, truncateText(text, 220))
		if len(parts) >= 3 {
			break
		}
	}
	if len(parts) == 0 {
		return "暂无可用摘要。"
	}
	return strings.Join(parts, "\n\n")
}

func previewDocumentChunks(chunks []model.DocumentChunkPreview, limit int) []model.DocumentChunkPreview {
	if limit <= 0 {
		limit = 5
	}
	if len(chunks) < limit {
		limit = len(chunks)
	}
	preview := make([]model.DocumentChunkPreview, 0, limit)
	for _, chunk := range chunks[:limit] {
		chunk.Text = truncateText(chunk.Text, 300)
		preview = append(preview, chunk)
	}
	return preview
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || value == "" {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func buildMCPCapabilities(cfg model.AppConfig, tools []ToolDefinition) map[string]any {
	permissionCounts := map[string]int{
		string(ToolPermissionReadOnly): 0,
		string(ToolPermissionWrite):    0,
		string(ToolPermissionDanger):   0,
	}
	toolItems := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		permission := string(tool.PermissionLevel)
		permissionCounts[permission]++
		toolItems = append(toolItems, map[string]any{
			"name":            tool.Name,
			"readOnly":        tool.ReadOnly,
			"permissionLevel": permission,
			"requiredScopes":  requiredScopesForTool(tool),
		})
	}

	return map[string]any{
		"name":             serverName,
		"version":          serverVersion,
		"protocolVersion":  protocolVersion,
		"jsonrpc":          jsonRPCVersion,
		"transport":        "http",
		"enabled":          cfg.MCP.Enabled,
		"basePath":         cfg.MCP.BasePath,
		"toolCount":        len(tools),
		"permissionCounts": permissionCounts,
		"tools":            toolItems,
		"capabilities":     map[string]any{"tools": map[string]any{"listChanged": false}},
		"authModel":        "api_key_scope",
		"requiredScopes": []string{
			scopeMCPRead,
			scopeMCPWrite,
			scopeMCPUpload,
			scopeMCPEval,
			scopeMCPDanger,
			scopeMCPAdmin,
		},
		"dangerConfirmation": map[string]any{
			"type":         "confirmNonce",
			"endpoint":     "/api/config/mcp/danger-confirmations",
			"legacyHeader": "X-MCP-Confirm",
		},
		"jobSupport":            true,
		"resultContractVersion": "1.0",
		"auth": map[string]any{
			"type":                  "api_key_scope",
			"legacyTokenCompatible": cfg.MCP.LegacyTokenEnabled,
			"legacyTokenConfigured": strings.TrimSpace(cfg.MCP.Token) != "",
			"legacyTokenPermission": "mcp:admin-compatible",
			"adminScope":            scopeMCPAdmin,
		},
		"dangerousToolGate": map[string]any{
			"type":         "confirmNonce",
			"endpoint":     "/api/config/mcp/danger-confirmations",
			"legacyHeader": "X-MCP-Confirm",
		},
	}
}

func buildSafeConfigSummary(cfg model.AppConfig) map[string]any {
	return map[string]any{
		"chat": map[string]any{
			"provider":              cfg.Chat.Provider,
			"model":                 cfg.Chat.Model,
			"baseUrlConfigured":     strings.TrimSpace(cfg.Chat.BaseURL) != "",
			"credentialsConfigured": strings.TrimSpace(cfg.Chat.APIKey) != "",
			"temperature":           cfg.Chat.Temperature,
			"contextMessageLimit":   cfg.Chat.ContextMessageLimit,
		},
		"embedding": map[string]any{
			"provider":              cfg.Embedding.Provider,
			"model":                 cfg.Embedding.Model,
			"baseUrlConfigured":     strings.TrimSpace(cfg.Embedding.BaseURL) != "",
			"credentialsConfigured": strings.TrimSpace(cfg.Embedding.APIKey) != "",
		},
		"mcp": map[string]any{
			"enabled":  cfg.MCP.Enabled,
			"basePath": cfg.MCP.BasePath,
		},
	}
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

const maxInlineUploadBytes int64 = 256 * 1024

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

func validateTextUploadFileName(fileName string, cfg model.AppConfig) error {
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
	if service.IsSensitiveStructuredFileExtension(ext) && !service.IsLocalOllamaConfig(cfg.Chat, cfg.Embedding) {
		return fmt.Errorf("sensitive structured file type %s requires local ollama for both chat and embedding", ext)
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

func optionalIntArg(args map[string]any, key string) int {
	if args == nil {
		return 0
	}
	switch value := args[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return parsed
		}
	}
	return 0
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
