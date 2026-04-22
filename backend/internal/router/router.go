package router

import (
	"log"
	"net/http"
	"strings"
	"time"

	"ai-localbase/internal/handler"
	"ai-localbase/internal/mcp"
	"ai-localbase/internal/model"
	"ai-localbase/internal/util"

	"github.com/gin-gonic/gin"
)

func NewRouter(appHandler *handler.AppHandler, serverConfig model.ServerConfig, mcpServer *mcp.Server) *gin.Engine {
	r := gin.New()
	r.Use(requestIDMiddleware(), accessLogMiddleware(), gin.Recovery(), corsMiddleware())

	r.GET("/", appHandler.Root)
	r.GET("/health", appHandler.Health)
	r.POST("/upload", appHandler.Upload)

	api := r.Group("/api")
	{
		api.GET("/config", appHandler.GetConfig)
		api.PUT("/config", appHandler.UpdateConfig)
		api.POST("/config/mcp/reset-token", appHandler.ResetMCPToken)
		api.GET("/conversations", appHandler.ListConversations)
		api.GET("/conversations/:id", appHandler.GetConversation)
		api.PUT("/conversations/:id", appHandler.SaveConversation)
		api.DELETE("/conversations/:id", appHandler.DeleteConversation)
		api.GET("/knowledge-bases", appHandler.ListKnowledgeBases)
		api.POST("/knowledge-bases", appHandler.CreateKnowledgeBase)
		api.DELETE("/knowledge-bases/:id", appHandler.DeleteKnowledgeBase)
		api.POST("/uploads", appHandler.StageUpload)
		api.GET("/knowledge-bases/:id/documents", appHandler.ListDocuments)
		api.POST("/knowledge-bases/:id/documents", appHandler.UploadToKnowledgeBase)
		api.DELETE("/knowledge-bases/:id/documents/:documentId", appHandler.DeleteDocument)
	}

	v1 := r.Group("/v1")
	{
		v1.POST("/chat/completions", appHandler.ChatCompletions)
		v1.POST("/chat/completions/stream", appHandler.ChatCompletionsStream)
	}

	if serverConfig.EnableMCP && mcpServer != nil {
		basePath := strings.TrimSpace(serverConfig.MCPBasePath)
		if basePath == "" {
			basePath = "/mcp"
		}
		mcpServer.RegisterRoutes(r.Group(basePath))
	}

	return r
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
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
