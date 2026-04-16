package handler

import (
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ai-localbase/internal/mcp"
	"ai-localbase/internal/model"
	"ai-localbase/internal/service"
	"ai-localbase/internal/util"

	"github.com/gin-gonic/gin"
)

type AppHandler struct {
	serverConfig model.ServerConfig
	appService   *service.AppService
	llmService   *service.LLMService
	toolPlanner  *mcp.ToolUsePlanner
}

func NewAppHandler(serverConfig model.ServerConfig, appService *service.AppService, llmService *service.LLMService, toolPlanner *mcp.ToolUsePlanner) *AppHandler {
	return &AppHandler{
		serverConfig: serverConfig,
		appService:   appService,
		llmService:   llmService,
		toolPlanner:  toolPlanner,
	}
}

func (h *AppHandler) Root(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"name":    "AI LocalBase Backend",
		"version": "v0.3.0",
		"status":  "running",
	})
}

func (h *AppHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, model.HealthResponse{
		Status: "ok",
		Name:   "ai-localbase-backend",
		Config: h.appService.GetHealthConfigMap(h.serverConfig),
	})
}

func (h *AppHandler) GetConfig(c *gin.Context) {
	c.JSON(http.StatusOK, h.appService.GetConfig())
}

func (h *AppHandler) ResetMCPToken(c *gin.Context) {
	mcpConfig, err := h.appService.ResetMCPToken()
	if err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"mcp": mcpConfig})
}

func (h *AppHandler) ListConversations(c *gin.Context) {
	items, err := h.appService.ListConversations()
	if err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *AppHandler) GetConversation(c *gin.Context) {
	conversation, err := h.appService.GetConversation(c.Param("id"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if conversation == nil {
		writeError(c, http.StatusNotFound, "conversation not found")
		return
	}
	c.JSON(http.StatusOK, conversation)
}

func (h *AppHandler) SaveConversation(c *gin.Context) {
	var req model.SaveConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid conversation request body")
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		req.ID = c.Param("id")
	}
	conversation, err := h.appService.SaveConversation(req)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusOK, conversation)
}

func (h *AppHandler) DeleteConversation(c *gin.Context) {
	if err := h.appService.DeleteConversation(c.Param("id")); err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "conversation deleted", "id": c.Param("id")})
}

func (h *AppHandler) UpdateConfig(c *gin.Context) {
	var req model.ConfigUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid config request body")
		return
	}

	cfg, err := h.appService.UpdateConfig(req)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	c.JSON(http.StatusOK, cfg)
}

func (h *AppHandler) ListKnowledgeBases(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"items": h.appService.ListKnowledgeBases()})
}

func (h *AppHandler) CreateKnowledgeBase(c *gin.Context) {
	var req model.KnowledgeBaseInput
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid knowledge base request body")
		return
	}

	knowledgeBase, err := h.appService.CreateKnowledgeBase(req)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	c.JSON(http.StatusCreated, knowledgeBase)
}

func (h *AppHandler) DeleteKnowledgeBase(c *gin.Context) {
	remaining, err := h.appService.DeleteKnowledgeBase(c.Param("id"))
	if err != nil {
		writeError(c, http.StatusNotFound, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "knowledge base deleted",
		"remaining": remaining,
	})
}

func (h *AppHandler) ListDocuments(c *gin.Context) {
	items, err := h.appService.GetKnowledgeBaseDocuments(c.Param("id"))
	if err != nil {
		writeError(c, http.StatusNotFound, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"knowledgeBaseId": c.Param("id"),
		"items":           items,
	})
}

func (h *AppHandler) UploadToKnowledgeBase(c *gin.Context) {
	h.handleUpload(c, c.Param("id"))
}

func (h *AppHandler) Upload(c *gin.Context) {
	h.handleUpload(c, c.PostForm("knowledgeBaseId"))
}

func (h *AppHandler) DeleteDocument(c *gin.Context) {
	removedDocument, err := h.appService.DeleteDocument(c.Param("id"), c.Param("documentId"))
	if err != nil {
		writeError(c, http.StatusNotFound, err.Error())
		return
	}

	_ = os.Remove(removedDocument.Path)

	c.JSON(http.StatusOK, gin.H{
		"message":         "document deleted",
		"knowledgeBaseId": c.Param("id"),
		"documentId":      c.Param("documentId"),
	})
}

func (h *AppHandler) ChatCompletions(c *gin.Context) {
	var req model.ChatCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid chat request body")
		return
	}

	preparedReq, sources, err := h.prepareChatRequest(c.Request.Context(), req)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	response, err := h.llmService.Chat(preparedReq)
	if err != nil {
		writeError(c, http.StatusBadGateway, err.Error())
		return
	}

	if response.Metadata == nil {
		response.Metadata = map[string]any{}
	}
	response.Metadata["sources"] = sources
	response.Metadata["knowledgeBaseId"] = req.KnowledgeBaseID
	response.Metadata["documentId"] = req.DocumentID
	response.Metadata["toolUse"] = buildToolUseMetadata(sources)

	if assistantMessage := firstAssistantChoice(response); assistantMessage != nil {
		_, saveErr := h.appService.SaveConversation(model.SaveConversationRequest{
			ID:              req.ConversationID,
			Title:           "",
			KnowledgeBaseID: req.KnowledgeBaseID,
			DocumentID:      req.DocumentID,
			Messages:        buildStoredConversationMessages(req.Messages, assistantMessage.Content, response.Metadata),
		})
		if saveErr != nil {
			writeError(c, http.StatusInternalServerError, saveErr.Error())
			return
		}
	}

	c.JSON(http.StatusOK, response)
}

func (h *AppHandler) ChatCompletionsStream(c *gin.Context) {
	var req model.ChatCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid chat request body")
		return
	}

	preparedReq, sources, err := h.prepareChatRequest(c.Request.Context(), req)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		writeError(c, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	initialMeta := gin.H{
		"sources":         sources,
		"knowledgeBaseId": req.KnowledgeBaseID,
		"documentId":      req.DocumentID,
		"toolUse":         buildToolUseMetadata(sources),
	}
	c.SSEvent("meta", initialMeta)
	flusher.Flush()

	assistantContent := strings.Builder{}
	streamErr := h.llmService.StreamChat(preparedReq, func(chunk string) error {
		assistantContent.WriteString(chunk)
		c.SSEvent("chunk", gin.H{"content": chunk})
		flusher.Flush()
		return nil
	})
	if streamErr != nil {
		c.SSEvent("error", gin.H{"error": streamErr.Error()})
		flusher.Flush()
		return
	}

	fullAssistantContent := assistantContent.String()
	responseMetadata := streamResponseMetadata(fullAssistantContent)
	_, saveErr := h.appService.SaveConversation(model.SaveConversationRequest{
		ID:              req.ConversationID,
		Title:           "",
		KnowledgeBaseID: req.KnowledgeBaseID,
		DocumentID:      req.DocumentID,
		Messages:        buildStoredConversationMessages(req.Messages, fullAssistantContent, responseMetadata),
	})
	if saveErr != nil {
		c.SSEvent("error", gin.H{"error": saveErr.Error()})
		flusher.Flush()
		return
	}

	c.SSEvent("done", gin.H{"content": fullAssistantContent, "metadata": responseMetadata})
	flusher.Flush()
}

func (h *AppHandler) prepareChatRequest(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionRequest, []map[string]string, error) {
	if len(req.Messages) == 0 {
		return model.ChatCompletionRequest{}, nil, fmt.Errorf("messages cannot be empty")
	}

	retrievalContext, retrievalSources, err := h.appService.BuildRetrievalContext(req)
	if err != nil {
		return model.ChatCompletionRequest{}, nil, err
	}

	toolUseContext := ""
	toolUseSources := []map[string]string(nil)
	if h.toolPlanner != nil {
		plannedCalls := h.toolPlanner.Plan(req)
		toolExecutions := h.toolPlanner.Execute(ctx, plannedCalls)
		toolUseContext, toolUseSources = mcp.BuildToolUseContext(toolExecutions)
	}

	contextSummary, sources, err := h.appService.BuildChatContext(req)
	if err != nil {
		return model.ChatCompletionRequest{}, nil, err
	}

	allSources := append(retrievalSources, sources...)
	allSources = append(allSources, toolUseSources...)
	contextParts := make([]string, 0, 3)
	if strings.TrimSpace(retrievalContext) != "" {
		contextParts = append(contextParts, "检索命中的文档片段：\n"+retrievalContext)
	}
	if strings.TrimSpace(toolUseContext) != "" {
		contextParts = append(contextParts, "MCP 工具调用结果：\n"+toolUseContext)
	}
	if strings.TrimSpace(contextSummary) != "" {
		contextParts = append(contextParts, contextSummary)
	}

	preparedReq := req
	preparedReq.Config = h.appService.CurrentChatConfig()
	preparedReq.Config.ContextMessageLimit = h.appService.ContextMessageLimit()
	preparedReq.Messages = h.appService.TrimChatMessages(req.Messages)
	latestQuestion := latestUserQuestion(req.Messages)
	isDiagramRequest := strings.Contains(latestQuestion, "流程图") || strings.Contains(latestQuestion, "架构图") || strings.Contains(latestQuestion, "状态图") || strings.Contains(latestQuestion, "Mermaid")
	tableQuestionType := detectTableQuestionType(latestQuestion, retrievalContext, contextSummary)
	if len(contextParts) > 0 {
		promptSections := []string{
			"你是 AI LocalBase 知识库助手。请严格遵守以下规范输出 Markdown 格式的回答。",
			"",
			"## Markdown 格式规范（必须严格执行）",
			"",
			"### 标题规则",
			"- 标题符号（#）与标题文字之间必须有一个空格，例如：## 核心观点",
			"- 标题下方必须空一行再写正文，正文与下一段之间也必须空一行",
			"- 禁止将数字序号与标题符号混用，正确写法是 ### 标题",
			"- 全文只用一个 ## 作为主标题，子章节一律用 ###，细分内容用 ####",
			"- 标题文字简洁（10字以内），不加标点符号",
			"",
			"### 内容规则",
			"- 关键词、核心数据、重要结论用 **加粗** 标注",
			"- 并列事项必须用无序列表（每条以 - 开头）；有先后顺序的必须用有序列表（1. 2. 3.）；禁止把多个要点写成一行",
			"- 每个列表项单独一行，列表前后各留一空行，保证渲染换行",
			"- 引用原文关键句时使用 blockquote，格式为：> 原文内容（> 后加空格）",
			"- 有多个维度对比时使用表格",
		}

		if tableQuestionType != "" {
			promptSections = append(promptSections, buildTableAnswerRules(tableQuestionType)...)
		}

		if isDiagramRequest {
			promptSections = append(promptSections,
				"",
				"### Mermaid 专用输出规则（仅在用户明确要求流程图/架构图时生效）",
				"- 这次回答只允许输出两部分：1）一句简短标题；2）一个 Mermaid 代码块；不要输出额外解释段落、补充建议、表格、列表",
				"- 必须使用标准 Mermaid 围栏，严格格式如下：第一行单独写 ```mermaid，第二行单独写 graph TD / graph LR / flowchart TD / flowchart LR，最后一行单独写 ```",
				"- 每条 Mermaid 语句单独一行：每个节点定义、每条连线、每个 classDef、每个 style、每个 subgraph、每个 end 都必须单独一行",
				"- subgraph 必须使用标准结构：subgraph 名称 -> 若干语句 -> end",
				"- 禁止输出 mermaidgraphTD、```mermaidgraphTD、endsubgraph、classDefxxxfill:、A-->BB-->C 这类压缩格式",
				"- 禁止在 Mermaid 代码块中输出中文说明、Markdown 标题、HTML 标签、span/style 内联样式、emoji、补充建议",
				"- 如果不能保证 Mermaid 语法完全正确，就不要输出 Mermaid，改为普通 Markdown 有序列表描述流程",
			)
		} else if tableQuestionType == tableQuestionTypeCount {
			promptSections = append(promptSections,
				"",
				"### 表格计数类固定模板（必须优先遵循）",
				"- 首句直接给出数量结论，明确回答对象是“文档”或“表格”，不要先写分析过程",
				"- 第二句只保留最小必要依据，例如“按表头下方的数据行统计，共 X 条记录”",
				"- 若无歧义，不要输出字段列表、文件名、逐行记录、原始片段复述",
				"- 若存在重复记录或统计口径不确定，单独补一句说明，不要展开无关明细",
			)
		} else {
			promptSections = append(promptSections,
				"",
				"### 结构模板（总结类问题必须遵循）",
				"",
				"## 主题名称",
				"",
				"### 子主题一",
				"",
				"- **关键词**：说明",
				"- **关键词**：说明",
				"",
				"### 子主题二",
				"",
				"- **关键词**：说明",
				"",
				"> 用一句话概括最重要的发现或观点。",
			)
		}

		promptSections = append(promptSections,
			"",
			"## 内容规范",
			"- 只基于以下上下文作答；信息不足时明确说明",
			"- 不要重复用户的问题，直接输出结构化内容",
			"- 回答长度适中，每个子章节 2 至 4 条要点即可，保持空行分隔，禁止连续写成一行",
			"- 同一事实只表达一次，禁止重复段落、重复结论、重复示例",
			"- 用户问“当前文档”时，不要回答成“整个知识库”；回答对象必须与用户问题一致",
			"",
			"## 上下文",
			strings.Join(contextParts, "\n\n"),
		)

		systemPrompt := strings.Join(promptSections, "\n")
		preparedReq.Messages = append([]model.ChatMessage{{
			Role:    "system",
			Content: systemPrompt,
		}}, preparedReq.Messages...)
	}

	return preparedReq, allSources, nil
}

const (
	tableQuestionTypeCount = "count"
	tableQuestionTypeList  = "list"
)

func latestUserQuestion(messages []model.ChatMessage) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.EqualFold(strings.TrimSpace(messages[index].Role), "user") {
			return strings.TrimSpace(messages[index].Content)
		}
	}
	return ""
}

func detectTableQuestionType(question, retrievalContext, contextSummary string) string {
	if !looksLikeStructuredTableContext(retrievalContext, contextSummary) {
		return ""
	}
	if isTableCountQuestion(question) {
		return tableQuestionTypeCount
	}
	if isTableListQuestion(question) {
		return tableQuestionTypeList
	}
	return ""
}

func looksLikeStructuredTableContext(retrievalContext, contextSummary string) bool {
	combined := retrievalContext + "\n" + contextSummary
	return strings.Contains(combined, "字段：") && strings.Contains(combined, "数据行数：")
}

func isTableCountQuestion(question string) bool {
	trimmed := strings.TrimSpace(question)
	if trimmed == "" {
		return false
	}
	countMarkers := []string{"多少", "几", "数量", "总数", "共", "总共有"}
	entityMarkers := []string{"员工", "老师", "教师", "人员", "记录", "行", "条", "名单"}
	if !containsAny(trimmed, countMarkers) {
		return false
	}
	return containsAny(trimmed, entityMarkers)
}

func isTableListQuestion(question string) bool {
	trimmed := strings.TrimSpace(question)
	if trimmed == "" {
		return false
	}
	listMarkers := []string{"有哪些", "列出", "名单", "分别是", "都有谁", "分别是谁"}
	return containsAny(trimmed, listMarkers)
}

func containsAny(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func buildTableAnswerRules(questionType string) []string {
	rules := []string{
		"",
		"### 表格问答附加规则",
		"- 先回答问题本身，再补充最小必要依据；不要把检索片段直接改写成长段流水账",
		"- 非用户明确要求时，不要罗列全部字段、全部记录、文件内部过程信息",
		"- 对表格类问题优先使用短句、列表或表格，不要把多个字段拼成一整段",
		"- 若上下文出现重复片段，只保留一次结论和一次依据",
	}
	if questionType == tableQuestionTypeList {
		rules = append(rules,
			"- 若用户要求列举名单，先给总数，再按列表列出名称；无关字段不要混入名单中",
		)
	}
	return rules
}

func (h *AppHandler) handleUpload(c *gin.Context, candidateKnowledgeBaseID string) {
	file, err := c.FormFile("file")
	if err != nil {
		writeError(c, http.StatusBadRequest, "missing file field 'file'")
		return
	}

	if err := validateUploadFile(file, h.appService.GetConfig()); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	knowledgeBaseID, err := h.appService.ResolveKnowledgeBaseID(candidateKnowledgeBaseID)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	storedName := fmt.Sprintf("%d_%s", util.NowUnixNano(), util.SanitizeFilename(file.Filename))
	destination := filepath.Join(h.serverConfig.UploadDir, storedName)
	if err := c.SaveUploadedFile(file, destination); err != nil {
		writeError(c, http.StatusInternalServerError, "failed to save uploaded file")
		return
	}

	document := model.Document{
		ID:              util.NextID("doc"),
		KnowledgeBaseID: knowledgeBaseID,
		Name:            file.Filename,
		Size:            file.Size,
		SizeLabel:       util.FormatFileSize(file.Size),
		UploadedAt:      util.NowRFC3339(),
		Status:          "processing",
		Path:            destination,
		ContentPreview:  util.ExtractContentPreview(destination),
	}

	uploaded, err := h.appService.IndexDocument(document)
	if err != nil {
		_ = os.Remove(destination)
		writeError(c, http.StatusBadGateway, err.Error())
		return
	}

	c.JSON(http.StatusOK,
		model.UploadResponse{
			Message:       "file uploaded successfully",
			KnowledgeBase: knowledgeBaseID,
			Uploaded:      uploaded,
		})
}

func buildToolUseMetadata(sources []map[string]string) []model.ToolUseMetadata {
	items := make([]model.ToolUseMetadata, 0)
	for _, source := range sources {
		toolName := strings.TrimSpace(source["toolName"])
		if toolName == "" {
			continue
		}
		items = append(items, model.ToolUseMetadata{
			ToolName:        toolName,
			PermissionLevel: source["permissionLevel"],
		})
	}
	return items
}

func validateUploadFile(file *multipart.FileHeader, cfg model.AppConfig) error {
	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowed := map[string]struct{}{
		".txt": {},
		".md":  {},
		".pdf": {},
	}
	if service.IsSensitiveStructuredFileExtension(ext) {
		if !service.IsLocalOllamaConfig(cfg.Chat, cfg.Embedding) {
			return errSensitiveStructuredFileRequiresLocalOllama(ext)
		}
		allowed[ext] = struct{}{}
	}

	if _, ok := allowed[ext]; !ok {
		return errUnsupportedFileType(ext)
	}

	return nil
}

func errUnsupportedFileType(ext string) error {
	if ext == "" {
		return fmt.Errorf("unsupported file type: missing extension, allowed types are .txt, .md, .pdf")
	}

	return &fileTypeError{Extension: ext}
}

func errSensitiveStructuredFileRequiresLocalOllama(ext string) error {
	return fmt.Errorf("sensitive structured file type %s requires local ollama for both chat and embedding", ext)
}

type fileTypeError struct {
	Extension string
}

func (e *fileTypeError) Error() string {
	return "unsupported file type: " + e.Extension + ", allowed types are .txt, .md, .pdf"
}

func buildStoredConversationMessages(messages []model.ChatMessage, assistantContent string, metadata map[string]any) []model.StoredChatMessage {
	stored := make([]model.StoredChatMessage, 0, len(messages)+1)
	for index, message := range messages {
		stored = append(stored, model.StoredChatMessage{
			ID:        fmt.Sprintf("msg_%d_%d", time.Now().UnixNano(), index),
			Role:      strings.TrimSpace(message.Role),
			Content:   message.Content,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	assistantMessage := model.StoredChatMessage{
		ID:        fmt.Sprintf("msg_%d_assistant", time.Now().UnixNano()),
		Role:      "assistant",
		Content:   assistantContent,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if len(metadata) > 0 {
		assistantMessage.Metadata = metadata
	}
	stored = append(stored, assistantMessage)
	return stored
}

func firstAssistantChoice(response model.ChatCompletionResponse) *model.ChatMessage {
	for _, choice := range response.Choices {
		if strings.EqualFold(strings.TrimSpace(choice.Message.Role), "assistant") {
			message := choice.Message
			return &message
		}
	}
	return nil
}

func writeError(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, model.APIError{Error: message})
}

func streamResponseMetadata(content string) map[string]any {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	if !strings.HasPrefix(trimmed, "⚠️ AI 模型调用失败") && !strings.HasPrefix(trimmed, "⚠ AI 模型调用失败") {
		return nil
	}
	return map[string]any{
		"degraded":         true,
		"fallbackStrategy": "stream-fallback-message",
	}
}
