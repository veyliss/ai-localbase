package mcp

import (
	"ai-localbase/internal/model"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func newRetrievalTools(appService AppServiceReader) []ToolDefinition {
	return []ToolDefinition{
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
				contextText, sources, err := buildRetrievalContextWithContext(appService, ctx, model.ChatCompletionRequest{
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
				contextText, sources, err := buildRetrievalContextWithContext(appService, ctx, model.ChatCompletionRequest{
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
			Description: "对 CSV / XLSX 结构化文档执行查询，返回行、字段、聚合值和来源数据，不生成最终回答。参数 query 必填，documentId 或 knowledgeBaseId 至少提供一个。",
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
				result, sources, ok, err := appService.QueryStructuredData(model.ChatCompletionRequest{
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
				encoded, err := json.Marshal(result)
				if err != nil {
					return ToolCallResult{}, fmt.Errorf("encode structured data result: %w", err)
				}
				return ToolCallResult{
					Summary: fmt.Sprintf("结构化查询完成：类型 %s，共 %d 行，匹配 %d 行。", result.Intent, result.TotalRows, result.MatchedRows),
					Content: []ToolContent{{Type: "text", Text: string(encoded)}},
					Data: map[string]any{
						"structuredData":  result,
						"sources":         sources,
						"documentId":      documentID,
						"knowledgeBaseId": knowledgeBaseID,
						"query":           query,
						"matched":         true,
					},
				}, nil
			},
		},
		{
			Name:        "debug_retrieval",
			Description: "执行检索调试，返回命中 chunk、检索分数、低置信标记和评测候选。参数 query 必填，knowledgeBaseId 或 documentId 至少提供一个。",
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
				response, err := debugRetrieveWithContext(appService, ctx, model.RetrievalDebugRequest{
					Query:           query,
					KnowledgeBaseID: knowledgeBaseID,
					DocumentID:      documentID,
					TopK:            optionalIntArg(args, "topK"),
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				summary := fmt.Sprintf("检索调试完成：命中 %d 个 chunk，耗时 %d ms。", response.Count, response.ElapsedMs)
				if response.LowConfidence {
					summary += " 当前结果低置信，可沉淀为评测样本。"
				}
				return NewTextResult(summary, map[string]any{"debug": response}), nil
			},
		},
		{
			Name:        "answer_with_sources",
			Description: "从知识库或文档整理可引用证据；结构化文档返回查询数据，不调用聊天模型，也不生成最终回答。参数 query 必填，knowledgeBaseId 或 documentId 至少提供一个。",
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
				structuredData, sources, structuredUsed, err := appService.QueryStructuredData(chatReq)
				if err != nil {
					return ToolCallResult{}, err
				}

				mode := "structured_data"
				contentText := ""
				data := map[string]any{
					"sources":         sources,
					"mode":            mode,
					"query":           query,
					"knowledgeBaseId": knowledgeBaseID,
					"documentId":      documentID,
				}
				if structuredUsed {
					encoded, err := json.Marshal(structuredData)
					if err != nil {
						return ToolCallResult{}, fmt.Errorf("encode structured data result: %w", err)
					}
					contentText = string(encoded)
					data["structuredData"] = structuredData
				} else {
					mode = "retrieval_evidence"
					evidence, retrievalSources, err := buildRetrievalContextWithContext(appService, ctx, chatReq)
					if err != nil {
						return ToolCallResult{}, err
					}
					evidence = strings.TrimSpace(evidence)
					sources = retrievalSources
					contentText = evidence
					data["mode"] = mode
					data["evidence"] = evidence
					data["sources"] = sources
				}

				warnings := []string{}
				if contentText == "" {
					contentText = "未检索到可用证据。"
					warnings = append(warnings, "未找到相关证据，建议换用更具体的问题或扩大检索范围。")
				}
				if len(sources) == 0 {
					warnings = append(warnings, "本次结果没有可引用来源。")
				}

				return ToolCallResult{
					Summary:  fmt.Sprintf("已整理证据包，模式为 %s，来源 %d 条。", mode, len(sources)),
					Content:  []ToolContent{{Type: "text", Text: contentText}},
					Data:     data,
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
						"health":        buildSafeMCPKnowledgeBaseHealth(health),
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
				dense, err := debugRetrieveWithContext(appService, ctx, model.RetrievalDebugRequest{
					Query:           query,
					KnowledgeBaseID: knowledgeBaseID,
					DocumentID:      documentID,
					TopK:            topK,
					SearchMode:      "dense",
				})
				if err != nil {
					return ToolCallResult{}, err
				}
				hybrid, err := debugRetrieveWithContext(appService, ctx, model.RetrievalDebugRequest{
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
	}
}
