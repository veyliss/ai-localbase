package mcp

import (
	"context"
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
	QueryStructuredData(req model.ChatCompletionRequest) (service.StructuredDataQueryResult, []map[string]string, bool, error)
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

// contextAwareAppService is implemented by the production AppService. Keeping
// these methods optional preserves compatibility with small tool test doubles.
type contextAwareAppService interface {
	BuildRetrievalContextWithContext(ctx context.Context, req model.ChatCompletionRequest) (string, []map[string]string, error)
	DebugRetrieveWithContext(ctx context.Context, req model.RetrievalDebugRequest) (model.RetrievalDebugResponse, error)
	StageInlineUploadAs(fileName string, content []byte, source string, owner service.AuthPrincipal) (model.StagedUpload, error)
	RegisterStagedUploadAs(ctx context.Context, uploadID, knowledgeBaseID, fileName string, owner service.AuthPrincipal) (model.Document, error)
	StartMCPImportJobAs(req model.MCPStartImportJobRequest, owner service.AuthPrincipal) (model.MCPJob, error)
	GetMCPJobStatusAs(jobID string, owner service.AuthPrincipal) (model.MCPJob, error)
	CancelMCPJobAs(jobID string, owner service.AuthPrincipal) (model.MCPJob, error)
	ListRecentMCPJobsAs(limit int, owner service.AuthPrincipal) []model.MCPJob
	ReindexDocumentWithContext(ctx context.Context, knowledgeBaseID, documentID string) (model.Document, error)
}

type mcpJobRetryService interface {
	RetryMCPJobAs(jobID string, owner service.AuthPrincipal) (model.MCPJob, error)
}

type mcpJobListPageService interface {
	ListMCPJobsPageAs(limit int, cursor string, owner service.AuthPrincipal) (service.MCPJobPage, error)
}

func contextAwareService(appService AppServiceReader) (contextAwareAppService, bool) {
	service, ok := appService.(contextAwareAppService)
	return service, ok
}

func buildRetrievalContextWithContext(appService AppServiceReader, ctx context.Context, req model.ChatCompletionRequest) (string, []map[string]string, error) {
	if enhanced, ok := contextAwareService(appService); ok {
		return enhanced.BuildRetrievalContextWithContext(ctx, req)
	}
	return appService.BuildRetrievalContext(req)
}

func debugRetrieveWithContext(appService AppServiceReader, ctx context.Context, req model.RetrievalDebugRequest) (model.RetrievalDebugResponse, error) {
	if enhanced, ok := contextAwareService(appService); ok {
		return enhanced.DebugRetrieveWithContext(ctx, req)
	}
	return appService.DebugRetrieve(req)
}

func stageInlineUploadAs(appService AppServiceReader, fileName string, content []byte, source string, owner service.AuthPrincipal) (model.StagedUpload, error) {
	if enhanced, ok := contextAwareService(appService); ok {
		return enhanced.StageInlineUploadAs(fileName, content, source, owner)
	}
	return appService.StageInlineUpload(fileName, content, source)
}

func registerStagedUploadAs(appService AppServiceReader, ctx context.Context, uploadID, knowledgeBaseID, fileName string, owner service.AuthPrincipal) (model.Document, error) {
	if enhanced, ok := contextAwareService(appService); ok {
		return enhanced.RegisterStagedUploadAs(ctx, uploadID, knowledgeBaseID, fileName, owner)
	}
	return appService.RegisterStagedUpload(uploadID, knowledgeBaseID, fileName)
}

func startMCPImportJobAs(appService AppServiceReader, req model.MCPStartImportJobRequest, owner service.AuthPrincipal) (model.MCPJob, error) {
	if enhanced, ok := contextAwareService(appService); ok {
		return enhanced.StartMCPImportJobAs(req, owner)
	}
	return appService.StartMCPImportJob(req)
}

func getMCPJobStatusAs(appService AppServiceReader, jobID string, owner service.AuthPrincipal) (model.MCPJob, error) {
	if enhanced, ok := contextAwareService(appService); ok {
		return enhanced.GetMCPJobStatusAs(jobID, owner)
	}
	return appService.GetMCPJobStatus(jobID)
}

func cancelMCPJobAs(appService AppServiceReader, jobID string, owner service.AuthPrincipal) (model.MCPJob, error) {
	if enhanced, ok := contextAwareService(appService); ok {
		return enhanced.CancelMCPJobAs(jobID, owner)
	}
	return appService.CancelMCPJob(jobID)
}

func listRecentMCPJobsAs(appService AppServiceReader, limit int, owner service.AuthPrincipal) []model.MCPJob {
	if enhanced, ok := contextAwareService(appService); ok {
		return enhanced.ListRecentMCPJobsAs(limit, owner)
	}
	return appService.ListRecentMCPJobs(limit)
}

func listMCPJobsPageAs(appService AppServiceReader, limit int, cursor string, owner service.AuthPrincipal) (service.MCPJobPage, error) {
	if enhanced, ok := appService.(mcpJobListPageService); ok {
		return enhanced.ListMCPJobsPageAs(limit, cursor, owner)
	}
	return service.MCPJobPage{Items: listRecentMCPJobsAs(appService, limit, owner)}, nil
}

func retryMCPJobAs(appService AppServiceReader, jobID string, owner service.AuthPrincipal) (model.MCPJob, error) {
	if enhanced, ok := appService.(mcpJobRetryService); ok {
		return enhanced.RetryMCPJobAs(jobID, owner)
	}
	return model.MCPJob{}, fmt.Errorf("mcp job retry is unavailable")
}

func reindexDocumentWithContext(appService AppServiceReader, ctx context.Context, knowledgeBaseID, documentID string) (model.Document, error) {
	if enhanced, ok := contextAwareService(appService); ok {
		return enhanced.ReindexDocumentWithContext(ctx, knowledgeBaseID, documentID)
	}
	return appService.ReindexDocument(knowledgeBaseID, documentID)
}

func NewReadOnlyTools(appService AppServiceReader) []ToolDefinition {
	tools := newSystemTools(appService)
	tools = append(tools, newKnowledgeBaseTools(appService)...)
	tools = append(tools, newRetrievalTools(appService)...)
	tools = append(tools, newEvaluationTools(appService)...)
	tools = append(tools, newConversationTools(appService)...)
	tools = append(tools, newImportTools(appService)...)
	return tools
}
func sanitizeMCPJob(job model.MCPJob) model.MCPJob {
	job.Error = sanitizeMCPError(job.Error)
	for index := range job.Warnings {
		job.Warnings[index] = sanitizeMCPError(job.Warnings[index])
	}
	job.Result = sanitizeMCPJobMap(job.Result)
	return job
}

func sanitizeMCPJobMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	sanitized := make(map[string]any, len(values))
	for key, value := range values {
		sanitized[key] = sanitizeMCPJobValue(key, value)
	}
	return sanitized
}

func sanitizeMCPJobValue(key string, value any) any {
	switch typed := value.(type) {
	case model.Document:
		return buildSafeMCPDocument(typed)
	case string:
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if strings.Contains(lowerKey, "error") || strings.Contains(lowerKey, "warning") {
			return sanitizeMCPError(typed)
		}
		return value
	case map[string]any:
		return sanitizeMCPJobMap(typed)
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = sanitizeMCPJobValue(key, item)
		}
		return items
	case []map[string]any:
		items := make([]map[string]any, len(typed))
		for index, item := range typed {
			items[index] = sanitizeMCPJobMap(item)
		}
		return items
	default:
		return value
	}
}

func formatKnowledgeBaseListText(items []model.KnowledgeBase) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "当前共有 %d 个知识库。", len(items))
	for _, item := range items {
		fmt.Fprintf(
			&builder,
			"\n- %s\n  ID: %s\n  文档: %d 篇",
			mcpListLabel(item.Name, "未命名知识库"),
			item.ID,
			len(item.Documents),
		)
	}
	return builder.String()
}

func formatDocumentListText(knowledgeBaseID string, items []model.Document) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "知识库 %s 下共有 %d 份文档。", knowledgeBaseID, len(items))
	for _, item := range items {
		fmt.Fprintf(
			&builder,
			"\n- %s\n  ID: %s\n  状态: %s",
			mcpListLabel(item.Name, "未命名文档"),
			item.ID,
			mcpListLabel(item.Status, "未知"),
		)
	}
	return builder.String()
}

func formatConversationListText(items []model.ConversationListItem) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "当前共有 %d 个会话。", len(items))
	for _, item := range items {
		fmt.Fprintf(
			&builder,
			"\n- %s\n  ID: %s\n  消息: %d 条",
			mcpListLabel(item.Title, "未命名会话"),
			item.ID,
			item.MessageCount,
		)
	}
	return builder.String()
}

func formatMCPJobListText(items []model.MCPJob) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "最近共有 %d 个 MCP 任务。", len(items))
	for _, item := range items {
		fmt.Fprintf(
			&builder,
			"\n- %s\n  ID: %s\n  状态: %s\n  进度: %d%%",
			mcpListLabel(item.Summary, item.Type),
			item.ID,
			mcpListLabel(item.Status, "未知"),
			item.Progress,
		)
	}
	return builder.String()
}

func mcpListLabel(value, fallback string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return fallback
	}
	return value
}

func DefaultRegistry(appService *service.AppService) *ToolRegistry {
	return NewToolRegistry(NewReadOnlyTools(appService)...)
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
	return buildMCPCapabilitiesForVersion(cfg, tools, resultContractVersion)
}

func buildMCPCapabilitiesForVersion(cfg model.AppConfig, tools []ToolDefinition, contractVersion string) map[string]any {
	contractVersion = normalizeMCPResultContractVersion(contractVersion)
	permissionCounts := map[string]int{
		string(ToolPermissionReadOnly): 0,
		string(ToolPermissionWrite):    0,
		string(ToolPermissionDanger):   0,
	}
	toolItems := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		permission := string(tool.PermissionLevel)
		permissionCounts[permission]++
		toolItem := map[string]any{
			"name":                  tool.Name,
			"readOnly":              tool.ReadOnly,
			"permissionLevel":       permission,
			"requiredScopes":        requiredScopesForTool(tool),
			"contractVersions":      supportedMCPResultContractVersions(),
			"resultContractVersion": contractVersion,
			"retryPolicy":           mcpToolRetryPolicy(tool),
		}
		if tool.Name == "start_import_job" {
			toolItem["scopeVariants"] = map[string][]string{
				"import":       []string{scopeMCPUpload},
				"batch_index":  []string{scopeMCPUpload},
				"reindex":      []string{scopeMCPWrite},
				"eval_dataset": []string{scopeMCPEval},
			}
		}
		toolItems = append(toolItems, toolItem)
	}

	return map[string]any{
		"name":                serverName,
		"version":             serverVersion,
		"protocolVersion":     protocolVersion,
		"contractNegotiation": mcpContractNegotiationPayload(contractVersion, ""),
		"jsonrpc":             jsonRPCVersion,
		"transport":           "http",
		"enabled":             cfg.MCP.Enabled,
		"basePath":            cfg.MCP.BasePath,
		"toolCount":           len(tools),
		"permissionCounts":    permissionCounts,
		"tools":               toolItems,
		"capabilities": map[string]any{
			"tools":   map[string]any{"listChanged": false},
			"metrics": map[string]any{"path": "/metrics", "scope": scopeMCPRead},
		},
		"authModel": "api_key_scope",
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
		"jobSupport":                      true,
		"resultContractVersion":           contractVersion,
		"supportedResultContractVersions": supportedMCPResultContractVersions(),
		"errorCodes":                      mcpErrorCatalog(),
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

// MCP success payloads use explicit DTOs instead of returning service models.
// Service models may gain operational fields such as local paths or parser
// errors that are useful internally but should not cross the MCP boundary.
func buildSafeMCPDocumentDetail(detail model.DocumentDetailResponse) map[string]any {
	return map[string]any{
		"knowledgeBaseId": detail.KnowledgeBaseID,
		"document":        buildSafeMCPDocument(detail.Document),
		"diagnostics":     buildSafeMCPDocumentDiagnostics(detail.Diagnostics),
		"rawContent":      detail.RawContent,
		"summary":         detail.Summary,
		"chunks":          previewDocumentChunks(detail.Chunks, len(detail.Chunks)),
	}
}

func buildSafeMCPDocument(document model.Document) map[string]any {
	return map[string]any{
		"id":              document.ID,
		"knowledgeBaseId": document.KnowledgeBaseID,
		"name":            document.Name,
		"size":            document.Size,
		"sizeLabel":       document.SizeLabel,
		"uploadedAt":      document.UploadedAt,
		"status":          document.Status,
		"contentPreview":  document.ContentPreview,
		"chunkCount":      document.ChunkCount,
		"indexedAt":       document.IndexedAt,
	}
}

func buildSafeMCPDocumentDiagnostics(diagnostics model.DocumentIndexDiagnostics) map[string]any {
	return map[string]any{
		"rawContentChars":       diagnostics.RawContentChars,
		"chunkCount":            diagnostics.ChunkCount,
		"vectorCount":           diagnostics.VectorCount,
		"summaryChunkCount":     diagnostics.SummaryChunkCount,
		"structuredRowCount":    diagnostics.StructuredRowCount,
		"rawContentAvailable":   diagnostics.RawContentAvailable,
		"qdrantEnabled":         diagnostics.QdrantEnabled,
		"rawContentTruncated":   diagnostics.RawContentTruncated,
		"chunkPreviewTruncated": diagnostics.ChunkPreviewTruncated,
	}
}

func buildSafeMCPKnowledgeBaseHealth(health model.KnowledgeBaseHealthResponse) map[string]any {
	documents := make([]map[string]any, 0, len(health.Documents))
	for _, document := range health.Documents {
		documents = append(documents, map[string]any{
			"documentId":          document.DocumentID,
			"documentName":        document.DocumentName,
			"status":              document.Status,
			"indexedAt":           document.IndexedAt,
			"errorCode":           document.IndexErrorCode,
			"indexVersion":        document.IndexVersion,
			"chunkCount":          document.ChunkCount,
			"vectorCount":         document.VectorCount,
			"summaryChunkCount":   document.SummaryChunkCount,
			"structuredRowCount":  document.StructuredRowCount,
			"rawContentChars":     document.RawContentChars,
			"rawContentAvailable": document.RawContentAvailable,
			"needsReindex":        document.NeedsReindex,
			"recommendation":      document.Recommendation,
		})
	}

	return map[string]any{
		"knowledgeBaseId":     health.KnowledgeBaseID,
		"name":                health.Name,
		"status":              health.Status,
		"score":               health.Score,
		"currentIndexVersion": health.CurrentIndexVersion,
		"metrics": map[string]any{
			"documentCount":      health.Metrics.DocumentCount,
			"indexedCount":       health.Metrics.IndexedCount,
			"processingCount":    health.Metrics.ProcessingCount,
			"failedCount":        health.Metrics.FailedCount,
			"emptyContentCount":  health.Metrics.EmptyContentCount,
			"chunkCount":         health.Metrics.ChunkCount,
			"vectorCount":        health.Metrics.VectorCount,
			"summaryChunkCount":  health.Metrics.SummaryChunkCount,
			"structuredRowCount": health.Metrics.StructuredRowCount,
			"rawContentChars":    health.Metrics.RawContentChars,
			"qdrantEnabled":      health.Metrics.QdrantEnabled,
			"lastIndexedAt":      health.Metrics.LastIndexedAt,
		},
		"recommendations": health.Recommendations,
		"documents":       documents,
		"indexHistory":    safeMCPIndexHistory(health.IndexHistory),
	}
}

func safeMCPIndexHistory(items []model.IndexRunRecord) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"id":              item.ID,
			"knowledgeBaseId": item.KnowledgeBaseID,
			"documentId":      item.DocumentID,
			"documentName":    item.DocumentName,
			"trigger":         item.Trigger,
			"status":          item.Status,
			"indexVersion":    item.IndexVersion,
			"chunkCount":      item.ChunkCount,
			"errorCode":       item.ErrorCode,
			"startedAt":       item.StartedAt,
			"completedAt":     item.CompletedAt,
		})
	}
	return result
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
	normalizedName, err := util.NormalizeFilename(fileName)
	if err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(normalizedName))
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
	normalizedName, err := util.NormalizeFilename(fileName)
	if err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(normalizedName))
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

func optionalStringSliceArg(args map[string]any, key string) ([]string, error) {
	if args == nil || args[key] == nil {
		return nil, nil
	}
	var rawItems []any
	switch value := args[key].(type) {
	case []any:
		rawItems = value
	case []string:
		items := make([]string, len(value))
		copy(items, value)
		return items, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	items := make([]string, 0, len(rawItems))
	for index, rawItem := range rawItems {
		item, ok := rawItem.(string)
		if !ok || strings.TrimSpace(item) == "" {
			return nil, fmt.Errorf("%s[%d] must be a non-empty string", key, index)
		}
		items = append(items, strings.TrimSpace(item))
	}
	return items, nil
}

func mcpJobTypeLabel(jobType string) string {
	switch jobType {
	case "reindex":
		return "重建索引"
	case "eval_dataset":
		return "评估数据集生成"
	case "batch_index":
		return "批量索引"
	default:
		return "导入"
	}
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
