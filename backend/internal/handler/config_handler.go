package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ai-localbase/internal/model"
	"ai-localbase/internal/service"

	"github.com/gin-gonic/gin"
)

type ConfigHandler struct {
	appService    *service.AppService
	qdrantService *service.QdrantService
}

func NewConfigHandler(appService *service.AppService, qdrantService *service.QdrantService) *ConfigHandler {
	return &ConfigHandler{
		appService:    appService,
		qdrantService: qdrantService,
	}
}

// TestChatModelRequest 测试聊天模型请求
type TestChatModelRequest struct {
	Provider    string  `json:"provider" binding:"required"`
	BaseURL     string  `json:"baseUrl" binding:"required"`
	Model       string  `json:"model" binding:"required"`
	APIKey      string  `json:"apiKey"`
	Temperature float64 `json:"temperature"`
}

// TestEmbeddingModelRequest 测试嵌入模型请求
type TestEmbeddingModelRequest struct {
	Provider string `json:"provider" binding:"required"`
	BaseURL  string `json:"baseUrl" binding:"required"`
	Model    string `json:"model" binding:"required"`
	APIKey   string `json:"apiKey"`
}

// TestModelResponse 测试响应
type TestModelResponse struct {
	Success            bool   `json:"success"`
	LatencyMs          int64  `json:"latency_ms,omitempty"`
	ErrorMessage       string `json:"error_message,omitempty"`
	VectorSize         int    `json:"vector_size,omitempty"`          // embedding only
	ExpectedVectorSize int    `json:"expected_vector_size,omitempty"` // embedding only
	ModelInfo          string `json:"model_info,omitempty"`
}

// HealthSummaryResponse 综合健康检查响应
type HealthSummaryResponse struct {
	Qdrant         ComponentHealth `json:"qdrant"`
	ChatModel      ComponentHealth `json:"chat_model"`
	EmbeddingModel ComponentHealth `json:"embedding_model"`
	Storage        ComponentHealth `json:"storage"`
	Auth           ComponentHealth `json:"auth"`
}

type ReadinessResponse struct {
	Status string                     `json:"status"`
	Checks map[string]ComponentHealth `json:"checks"`
}

type ComponentHealth struct {
	Status       string `json:"status"` // "ok", "configured", "error", "not_configured"
	Message      string `json:"message,omitempty"`
	LatencyMs    int64  `json:"latency_ms,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// TestChatModel 测试聊天模型连通性
func (h *ConfigHandler) TestChatModel(c *gin.Context) {
	var req TestChatModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request: %v", err))
		return
	}

	start := time.Now()

	// 创建临时 LLM 服务实例
	llmService := service.NewLLMService()

	// 构造测试消息
	testMessages := []model.ChatMessage{
		{Role: "user", Content: "Hello"},
	}

	// 调用聊天接口
	apiKey := h.resolveChatAPIKey(req.APIKey, req.Provider, req.BaseURL)
	response, err := llmService.Chat(model.ChatCompletionRequest{
		Messages: testMessages,
		Config: model.ChatModelConfig{
			Provider:    req.Provider,
			BaseURL:     req.BaseURL,
			Model:       req.Model,
			APIKey:      apiKey,
			Temperature: req.Temperature,
		},
	})
	latency := time.Since(start).Milliseconds()

	if err != nil {
		c.JSON(http.StatusOK, TestModelResponse{
			Success:      false,
			LatencyMs:    latency,
			ErrorMessage: formatErrorMessage(err),
		})
		return
	}

	// 检查响应是否有效
	if len(response.Choices) == 0 || response.Choices[0].Message.Content == "" {
		c.JSON(http.StatusOK, TestModelResponse{
			Success:      false,
			LatencyMs:    latency,
			ErrorMessage: "Model returned empty response",
		})
		return
	}

	c.JSON(http.StatusOK, TestModelResponse{
		Success:   true,
		LatencyMs: latency,
		ModelInfo: fmt.Sprintf("Model responded successfully (response length: %d chars)", len(response.Choices[0].Message.Content)),
	})
}

// TestEmbeddingModel 测试嵌入模型连通性
func (h *ConfigHandler) TestEmbeddingModel(c *gin.Context) {
	var req TestEmbeddingModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request: %v", err))
		return
	}

	start := time.Now()

	// 创建临时 RAG 服务实例
	ragService := service.NewRagService()

	// 测试文本
	testText := "Hello, this is a test."

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	// 调用嵌入接口
	apiKey := h.resolveEmbeddingAPIKey(req.APIKey, req.Provider, req.BaseURL)
	vectors, err := ragService.EmbedTexts(ctx, model.EmbeddingModelConfig{
		Provider: req.Provider,
		BaseURL:  req.BaseURL,
		Model:    req.Model,
		APIKey:   apiKey,
	}, []string{testText}, h.expectedEmbeddingVectorSize())

	latency := time.Since(start).Milliseconds()

	if err != nil {
		actualSize, expectedSize := embeddingDimensionDetails(err)
		c.JSON(http.StatusOK, TestModelResponse{
			Success:            false,
			LatencyMs:          latency,
			ErrorMessage:       formatErrorMessage(err),
			VectorSize:         actualSize,
			ExpectedVectorSize: expectedSize,
		})
		return
	}

	// 检查向量是否有效
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		c.JSON(http.StatusOK, TestModelResponse{
			Success:      false,
			LatencyMs:    latency,
			ErrorMessage: "Model returned empty vector",
		})
		return
	}

	c.JSON(http.StatusOK, TestModelResponse{
		Success:            true,
		LatencyMs:          latency,
		VectorSize:         len(vectors[0]),
		ExpectedVectorSize: h.expectedEmbeddingVectorSize(),
		ModelInfo:          fmt.Sprintf("Embedding successful (vector size: %d)", len(vectors[0])),
	})
}

func (h *ConfigHandler) resolveChatAPIKey(candidate, provider, baseURL string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate != "" {
		return candidate
	}
	if h == nil || h.appService == nil {
		return ""
	}
	config := h.appService.GetConfig()
	if !sameModelEndpoint(provider, baseURL, config.Chat.Provider, config.Chat.BaseURL) {
		return ""
	}
	return strings.TrimSpace(config.Chat.APIKey)
}

func (h *ConfigHandler) resolveEmbeddingAPIKey(candidate, provider, baseURL string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate != "" {
		return candidate
	}
	if h == nil || h.appService == nil {
		return ""
	}
	config := h.appService.GetConfig()
	if !sameModelEndpoint(provider, baseURL, config.Embedding.Provider, config.Embedding.BaseURL) {
		return ""
	}
	return strings.TrimSpace(config.Embedding.APIKey)
}

func sameModelEndpoint(provider, baseURL, storedProvider, storedBaseURL string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), strings.TrimSpace(storedProvider)) &&
		strings.TrimRight(strings.TrimSpace(baseURL), "/") == strings.TrimRight(strings.TrimSpace(storedBaseURL), "/")
}

func (h *ConfigHandler) expectedEmbeddingVectorSize() int {
	if h == nil || h.appService == nil {
		return 768
	}
	vectorSize := h.appService.ServerConfig().QdrantVectorSize
	if vectorSize <= 0 {
		return 768
	}
	return vectorSize
}

// HealthSummary 综合健康检查
func (h *ConfigHandler) HealthSummary(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	summary := HealthSummaryResponse{}

	// 1. 检查 Qdrant
	summary.Qdrant = h.checkQdrantHealth(ctx)

	// 2. 检查聊天模型
	summary.ChatModel = h.checkChatModelHealth(ctx)

	// 3. 检查嵌入模型
	summary.EmbeddingModel = h.checkEmbeddingModelHealth(ctx)

	// 4. 检查存储
	summary.Storage = h.checkStorageHealth()

	// 5. 检查认证部署建议
	summary.Auth = h.checkAuthHealth()

	c.JSON(http.StatusOK, summary)
}

// Readiness reports whether the backend can serve normal requests. It avoids
// model inference probes because Docker polls this endpoint repeatedly.
func (h *ConfigHandler) Readiness(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]ComponentHealth{}
	if h == nil || h.appService == nil {
		checks["backend"] = ComponentHealth{Status: "error", ErrorMessage: "application service is unavailable"}
		c.JSON(http.StatusServiceUnavailable, ReadinessResponse{Status: "not_ready", Checks: checks})
		return
	}

	checks["qdrant"] = h.checkQdrantHealth(ctx)
	checks["storage"] = h.checkStorageHealth()
	checks["upload_staging"] = h.checkUploadStagingHealth()
	config := h.appService.GetConfig()
	checks["chat_model"] = modelConfigurationHealth("chat", config.Chat.BaseURL, config.Chat.Model)
	checks["embedding_model"] = modelConfigurationHealth("embedding", config.Embedding.BaseURL, config.Embedding.Model)

	qdrantReady := checks["qdrant"].Status == "ok" || checks["qdrant"].Status == "not_configured"
	ready := qdrantReady && checks["storage"].Status == "ok" && checks["upload_staging"].Status == "ok"
	status := "not_ready"
	statusCode := http.StatusServiceUnavailable
	if ready {
		status = "ready"
		statusCode = http.StatusOK
	}
	c.JSON(statusCode, ReadinessResponse{Status: status, Checks: checks})
}

func (h *ConfigHandler) checkUploadStagingHealth() ComponentHealth {
	if h == nil || h.appService == nil {
		return ComponentHealth{Status: "error", ErrorMessage: "upload staging is unavailable"}
	}
	if err := h.appService.UploadStagingManifestLoadError(); err != nil {
		return ComponentHealth{
			Status:       "error",
			Message:      "Upload staging manifest cannot be loaded",
			ErrorMessage: "upload staging manifest is unavailable",
		}
	}
	return ComponentHealth{Status: "ok", Message: "Upload staging is accessible"}
}

func modelConfigurationHealth(kind, baseURL, modelName string) ComponentHealth {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(modelName) == "" {
		return ComponentHealth{
			Status:  "not_configured",
			Message: fmt.Sprintf("%s model is not configured", kind),
		}
	}
	return ComponentHealth{
		Status:  "configured",
		Message: fmt.Sprintf("%s model configuration is present", kind),
	}
}

func (h *ConfigHandler) checkQdrantHealth(ctx context.Context) ComponentHealth {
	if h.qdrantService == nil || !h.qdrantService.IsEnabled() {
		return ComponentHealth{
			Status:  "not_configured",
			Message: "Qdrant is not enabled",
		}
	}

	start := time.Now()
	err := h.qdrantService.Ping(ctx)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return ComponentHealth{
			Status:       "error",
			ErrorMessage: formatErrorMessage(err),
			LatencyMs:    latency,
		}
	}

	return ComponentHealth{
		Status:    "ok",
		Message:   "Qdrant is accessible",
		LatencyMs: latency,
	}
}

func (h *ConfigHandler) checkChatModelHealth(ctx context.Context) ComponentHealth {
	config := h.appService.GetConfig()

	if config.Chat.BaseURL == "" || config.Chat.Model == "" {
		return ComponentHealth{
			Status:  "not_configured",
			Message: "Chat model not configured",
		}
	}

	start := time.Now()
	llmService := service.NewLLMService()

	testMessages := []model.ChatMessage{
		{Role: "user", Content: "Hi"},
	}

	response, err := llmService.Chat(model.ChatCompletionRequest{
		Messages: testMessages,
		Config: model.ChatModelConfig{
			Provider:    config.Chat.Provider,
			BaseURL:     config.Chat.BaseURL,
			Model:       config.Chat.Model,
			APIKey:      config.Chat.APIKey,
			Temperature: config.Chat.Temperature,
		},
	})
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return ComponentHealth{
			Status:       "error",
			ErrorMessage: formatErrorMessage(err),
			LatencyMs:    latency,
		}
	}

	if len(response.Choices) == 0 || response.Choices[0].Message.Content == "" {
		return ComponentHealth{
			Status:       "error",
			ErrorMessage: "Model returned empty response",
			LatencyMs:    latency,
		}
	}

	return ComponentHealth{
		Status:    "ok",
		Message:   fmt.Sprintf("Chat model '%s' is working", config.Chat.Model),
		LatencyMs: latency,
	}
}

func (h *ConfigHandler) checkEmbeddingModelHealth(ctx context.Context) ComponentHealth {
	config := h.appService.GetConfig()

	if config.Embedding.BaseURL == "" || config.Embedding.Model == "" {
		return ComponentHealth{
			Status:  "not_configured",
			Message: "Embedding model not configured",
		}
	}

	start := time.Now()
	ragService := service.NewRagService()

	vectors, err := ragService.EmbedTexts(ctx, model.EmbeddingModelConfig{
		Provider: config.Embedding.Provider,
		BaseURL:  config.Embedding.BaseURL,
		Model:    config.Embedding.Model,
		APIKey:   config.Embedding.APIKey,
	}, []string{"test"}, h.expectedEmbeddingVectorSize())
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return ComponentHealth{
			Status:       "error",
			ErrorMessage: formatErrorMessage(err),
			LatencyMs:    latency,
		}
	}

	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return ComponentHealth{
			Status:       "error",
			ErrorMessage: "Model returned empty vector",
			LatencyMs:    latency,
		}
	}

	return ComponentHealth{
		Status:    "ok",
		Message:   fmt.Sprintf("Embedding model '%s' is working (vector size: %d)", config.Embedding.Model, len(vectors[0])),
		LatencyMs: latency,
	}
}

func (h *ConfigHandler) checkStorageHealth() ComponentHealth {
	// 简单检查：尝试获取配置（说明状态文件可读）
	config := h.appService.GetConfig()
	if config.Chat.BaseURL != "" || config.Embedding.BaseURL != "" {
		return ComponentHealth{
			Status:  "ok",
			Message: "Storage is accessible",
		}
	}

	return ComponentHealth{
		Status:  "ok",
		Message: "Storage is accessible (no config yet)",
	}
}

func (h *ConfigHandler) checkAuthHealth() ComponentHealth {
	if h == nil || h.appService == nil {
		return ComponentHealth{Status: "not_configured", Message: "Authentication health unavailable"}
	}

	warnings := h.appService.AuthDeploymentWarnings()
	if len(warnings) > 0 {
		return ComponentHealth{
			Status:  "warning",
			Message: strings.Join(warnings, "；"),
		}
	}

	return ComponentHealth{
		Status:  "ok",
		Message: "Authentication deployment safeguards look good",
	}
}

// formatErrorMessage 格式化错误消息，提供友好的提示
func formatErrorMessage(err error) string {
	if err == nil {
		return ""
	}

	var dimensionErr *service.EmbeddingDimensionMismatchError
	if errors.As(err, &dimensionErr) {
		return fmt.Sprintf(
			"Embedding 模型输出 %d 维，但当前 QDRANT_VECTOR_SIZE=%d。请修改维度、使用新的 QDRANT_COLLECTION_PREFIX，并重新索引知识库。",
			dimensionErr.Actual,
			dimensionErr.Expected,
		)
	}

	errMsg := err.Error()

	// 常见错误的友好提示
	if strings.Contains(errMsg, "connection refused") {
		return "Connection refused. Please check if the service is running and the URL is correct."
	}
	if strings.Contains(errMsg, "no such host") {
		return "Cannot resolve host. Please check the URL."
	}
	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline exceeded") {
		return "Request timeout. The service may be slow or unreachable."
	}
	if strings.Contains(errMsg, "401") || strings.Contains(errMsg, "unauthorized") {
		return "Authentication failed. Please check your API key."
	}
	if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "not found") {
		return "Model not found. Please check the model name."
	}
	if strings.Contains(errMsg, "500") || strings.Contains(errMsg, "internal server error") {
		return "Server error. The service may be experiencing issues."
	}

	return errMsg
}

func embeddingDimensionDetails(err error) (actual int, expected int) {
	var dimensionErr *service.EmbeddingDimensionMismatchError
	if errors.As(err, &dimensionErr) {
		return dimensionErr.Actual, dimensionErr.Expected
	}
	return 0, 0
}
