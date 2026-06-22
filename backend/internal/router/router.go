package router

import (
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ai-localbase/internal/auth"
	"ai-localbase/internal/handler"
	"ai-localbase/internal/mcp"
	"ai-localbase/internal/model"
	"ai-localbase/internal/service"
	"ai-localbase/internal/util"

	"github.com/gin-gonic/gin"
)

func NewRouter(appHandler *handler.AppHandler, configHandler *handler.ConfigHandler, authHandler *handler.AuthHandler, authService *service.AuthService, serverConfig model.ServerConfig, mcpServer *mcp.Server, frontendFS fs.FS) *gin.Engine {
	r := gin.New()
	r.Use(requestIDMiddleware(), accessLogMiddleware(), gin.Recovery(), corsMiddleware(serverConfig.EnableAuth))

	r.GET("/health", appHandler.Health)

	// Auth endpoints (always available for consistency)
	authGroup := r.Group("/api/auth")
	{
		authGroup.GET("/bootstrap", authHandler.Bootstrap)
		authGroup.POST("/setup", authHandler.Setup)
		authGroup.POST("/login", authHandler.Login)
	}

	// Apply auth middleware conditionally
	api := r.Group("/api")
	if serverConfig.EnableAuth {
		api.Use(auth.SessionMiddleware(authService))
	}
	{
		api.GET("/auth/status", authHandler.Status)
		api.POST("/auth/logout", authHandler.Logout)
		api.POST("/auth/logout-all", authHandler.LogoutAll)
		api.POST("/auth/change-password", authHandler.ChangePassword)
		api.GET("/auth/sessions", authHandler.ListSessions)
		api.GET("/auth/api-keys", authHandler.ListAPIKeys)
		api.POST("/auth/api-keys", authHandler.CreateAPIKey)
		api.DELETE("/auth/api-keys/:id", authHandler.RevokeAPIKey)
		api.GET("/auth/security-events", authHandler.ListSecurityEvents)
		api.GET("/config", appHandler.GetConfig)
		api.PUT("/config", appHandler.UpdateConfig)
		api.POST("/config/mcp/reset-token", appHandler.ResetMCPToken)
		api.POST("/config/mcp/danger-confirmations", appHandler.CreateMCPDangerConfirmation)
		api.POST("/config/test-chat-model", configHandler.TestChatModel)
		api.POST("/config/test-embedding-model", configHandler.TestEmbeddingModel)
		api.GET("/config/health-summary", configHandler.HealthSummary)
		api.GET("/conversations", appHandler.ListConversations)
		api.GET("/conversations/:id", appHandler.GetConversation)
		api.PUT("/conversations/:id", appHandler.SaveConversation)
		api.DELETE("/conversations/:id", appHandler.DeleteConversation)
		api.PUT("/conversations/:id/messages/:msgId", appHandler.EditMessage)
		api.DELETE("/conversations/:id/messages/:msgId", appHandler.DeleteMessage)
		api.POST("/conversations/:id/messages/:msgId/regenerate", appHandler.RegenerateMessage)
		api.GET("/conversations/:id/export", appHandler.ExportConversation)
		api.GET("/knowledge-bases", appHandler.ListKnowledgeBases)
		api.POST("/knowledge-bases", appHandler.CreateKnowledgeBase)
		api.DELETE("/knowledge-bases/:id", appHandler.DeleteKnowledgeBase)
		api.GET("/knowledge-bases/:id/health", appHandler.GetKnowledgeBaseHealth)
		api.POST("/knowledge-bases/:id/retrieval/debug", appHandler.DebugRetrieve)
		api.GET("/eval/datasets", appHandler.ListEvalDatasets)
		api.GET("/eval/runs", appHandler.ListEvalRuns)
		api.POST("/eval/datasets/generate", appHandler.GenerateEvalDataset)
		api.POST("/eval/datasets/review-candidates", appHandler.AddEvalDatasetCandidate)
		api.GET("/eval/datasets/:datasetId", appHandler.GetEvalDataset)
		api.POST("/eval/datasets/:datasetId/runs", appHandler.RunEvalDataset)
		api.PUT("/eval/datasets/:datasetId/items/:itemId", appHandler.UpdateEvalDatasetItem)
		api.DELETE("/eval/datasets/:datasetId/items/:itemId", appHandler.DeleteEvalDatasetItem)
		api.DELETE("/eval/datasets/:datasetId", appHandler.DeleteEvalDataset)
		api.POST("/uploads", appHandler.StageUpload)
		api.GET("/knowledge-bases/:id/documents", appHandler.ListDocuments)
		api.POST("/knowledge-bases/:id/documents", appHandler.UploadToKnowledgeBase)
		api.POST("/knowledge-bases/:id/documents/batch-index", appHandler.BatchIndexDocuments)
		api.GET("/knowledge-bases/:id/documents/:documentId", appHandler.GetDocumentDetail)
		api.GET("/knowledge-bases/:id/documents/:documentId/index-status", appHandler.GetDocumentIndexStatus)
		api.POST("/knowledge-bases/:id/documents/:documentId/reindex", appHandler.ReindexDocument)
		api.DELETE("/knowledge-bases/:id/documents/:documentId", appHandler.DeleteDocument)
	}

	// Upload endpoint (protected if auth enabled)
	if serverConfig.EnableAuth {
		r.POST("/upload", auth.SessionMiddleware(authService), appHandler.Upload)
	} else {
		r.POST("/upload", appHandler.Upload)
	}

	v1 := r.Group("/v1")
	if serverConfig.EnableAuth {
		v1.Use(auth.SessionOrAPIKeyMiddleware(authService, "openai:chat"))
	}
	{
		v1.POST("/chat/completions", appHandler.ChatCompletions)
		v1.POST("/chat/completions/stream", appHandler.ChatCompletionsStream)
	}

	basePath := strings.TrimSpace(serverConfig.MCPBasePath)
	if basePath == "" {
		basePath = "/mcp"
	}
	if serverConfig.EnableMCP && mcpServer != nil {
		mcpServer.RegisterRoutes(r.Group(basePath))
	} else {
		r.Any(basePath, mcpDisabledHandler)
		r.Any(basePath+"/*path", mcpDisabledHandler)
	}

	r.NoRoute(spaHandler(frontendFS))

	return r
}

func mcpDisabledHandler(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"error": "mcp is disabled"})
}

func spaHandler(frontendFS fs.FS) gin.HandlerFunc {
	fileServer := http.FileServer(http.FS(frontendFS))
	return func(c *gin.Context) {
		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		if path != "" {
			if f, err := frontendFS.Open(path); err == nil {
				f.Close()
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}

func corsMiddleware(authEnabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if !authEnabled {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if isLocalDevelopmentOrigin(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Id")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Expose-Headers", "X-Request-Id")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func isLocalDevelopmentOrigin(origin string) bool {
	if origin == "" {
		return false
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}

	host := strings.ToLower(parsed.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader("X-Request-Id"))
		if requestID == "" {
			requestID = util.NextRequestID()
		}

		c.Set("requestId", requestID)
		c.Header("X-Request-Id", requestID)
		c.Next()
	}
}

func accessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		requestID := strings.TrimSpace(c.GetHeader("X-Request-Id"))
		if requestID == "" {
			if value, ok := c.Get("requestId"); ok {
				requestID, _ = value.(string)
			}
		}

		c.Next()

		log.Printf(
			"request_id=%s method=%s path=%s status=%d duration_ms=%d client_ip=%s",
			requestID,
			c.Request.Method,
			c.Request.URL.Path,
			c.Writer.Status(),
			time.Since(startedAt).Milliseconds(),
			c.ClientIP(),
		)
	}
}
