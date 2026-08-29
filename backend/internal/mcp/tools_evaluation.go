package mcp

import (
	"ai-localbase/internal/model"
	"context"
	"fmt"
)

func newEvaluationTools(appService AppServiceReader) []ToolDefinition {
	return []ToolDefinition{
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
				debug, err := debugRetrieveWithContext(appService, ctx, model.RetrievalDebugRequest{
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
			ReadOnly:        false,
			PermissionLevel: ToolPermissionWrite,
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
	}
}
