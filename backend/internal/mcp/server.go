package mcp

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"ai-localbase/internal/model"

	"github.com/gin-gonic/gin"
)

type TokenProvider interface {
	GetConfig() model.AppConfig
}

type Server struct {
	registry        *ToolRegistry
	tokenProvider   TokenProvider
	requestTimeout  time.Duration
	requestsPerMin  int
	rateMu          sync.Mutex
	rateWindowStart time.Time
	rateCount       int
}

func NewServer(registry *ToolRegistry, tokenProvider TokenProvider, serverConfig model.ServerConfig) *Server {
	timeout := time.Duration(serverConfig.MCPRequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	requestsPerMin := serverConfig.MCPRequestsPerMinute
	if requestsPerMin <= 0 {
		requestsPerMin = 120
	}
	return &Server{
		registry:       registry,
		tokenProvider:  tokenProvider,
		requestTimeout: timeout,
		requestsPerMin: requestsPerMin,
	}
}

func (s *Server) RegisterRoutes(group *gin.RouterGroup) {
	if s == nil || group == nil {
		return
	}

	group.GET("", s.handleInfo)
	group.GET("/tools", s.handleListTools)
	group.POST("", s.handleJSONRPC)
}

func (s *Server) handleInfo(c *gin.Context) {
	if !s.authorize(c) {
		return
	}
	if !s.allowRequest(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"name":            "ai-localbase-mcp",
		"protocolVersion": protocolVersion,
		"jsonrpc":         jsonRPCVersion,
		"capabilities":    gin.H{"tools": gin.H{"listChanged": false}},
		"transport":       "http",
		"toolCount":       len(s.registry.List()),
	})
}

func (s *Server) handleListTools(c *gin.Context) {
	if !s.authorize(c) {
		return
	}
	if !s.allowRequest(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"tools": s.toolDescriptors(),
	})
}

func (s *Server) handleJSONRPC(c *gin.Context) {
	if !s.authorize(c) {
		return
	}
	if !s.allowRequest(c) {
		return
	}

	startedAt := time.Now()
	ctx := c.Request.Context()
	if s.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.requestTimeout)
		defer cancel()
	}

	var request JSONRPCRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		log.Printf("mcp request failed remote=%s error=%v", c.ClientIP(), err)
		c.JSON(http.StatusBadRequest, errorResponse(nil, -32700, "invalid json-rpc request body"))
		return
	}

	method := strings.TrimSpace(request.Method)
	switch method {
	case "initialize":
		log.Printf("mcp request method=%s remote=%s duration_ms=%d", method, c.ClientIP(), time.Since(startedAt).Milliseconds())
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: jsonRPCVersion,
			ID:      request.ID,
			Result: map[string]any{
				"protocolVersion": protocolVersion,
				"serverInfo": map[string]any{
					"name":    "ai-localbase-mcp",
					"version": "0.1.0",
				},
				"capabilities": map[string]any{
					"tools": map[string]any{
						"listChanged": false,
					},
				},
			},
		})
	case "tools/list":
		log.Printf("mcp request method=%s remote=%s duration_ms=%d", method, c.ClientIP(), time.Since(startedAt).Milliseconds())
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: jsonRPCVersion,
			ID:      request.ID,
			Result: map[string]any{
				"tools": s.toolDescriptors(),
			},
		})
	case "tools/call":
		toolName, _ := request.Params["name"].(string)
		arguments, _ := request.Params["arguments"].(map[string]any)
		toolName = strings.TrimSpace(toolName)
		permissionLevel := "unknown"
		for _, tool := range s.registry.List() {
			if tool.Name == toolName {
				permissionLevel = string(tool.PermissionLevel)
				break
			}
		}
		if !s.authorizeDangerousTool(c, toolName) {
			return
		}
		result, err := s.registry.Call(ctx, toolName, arguments)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("mcp tool call timeout tool=%s permission=%s remote=%s duration_ms=%d error=%v", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds(), ctx.Err())
				c.JSON(http.StatusGatewayTimeout, errorResponse(request.ID, -32001, "mcp request timed out"))
				return
			}
			log.Printf("mcp tool call failed tool=%s permission=%s remote=%s duration_ms=%d error=%v", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds(), err)
			c.JSON(http.StatusOK, errorResponse(request.ID, -32000, err.Error()))
			return
		}
		if ctx.Err() != nil {
			log.Printf("mcp tool call timeout tool=%s permission=%s remote=%s duration_ms=%d error=%v", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds(), ctx.Err())
			c.JSON(http.StatusGatewayTimeout, errorResponse(request.ID, -32001, "mcp request timed out"))
			return
		}
		log.Printf("mcp tool call tool=%s permission=%s remote=%s duration_ms=%d is_error=%t", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds(), result.IsError)
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: jsonRPCVersion,
			ID:      request.ID,
			Result: map[string]any{
				"content": result.Content,
				"data":    result.Data,
				"isError": result.IsError,
			},
		})
	default:
		log.Printf("mcp request method_not_found method=%s remote=%s duration_ms=%d", method, c.ClientIP(), time.Since(startedAt).Milliseconds())
		c.JSON(http.StatusOK, errorResponse(request.ID, -32601, fmt.Sprintf("method not found: %s", method)))
	}
}

func (s *Server) toolDescriptors() []map[string]any {
	tools := s.registry.List()
	items := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		items = append(items, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
			"annotations": map[string]any{
				"readOnlyHint":    tool.ReadOnly,
				"permissionLevel": tool.PermissionLevel,
			},
		})
	}
	return items
}

func errorResponse(id any, code int, message string) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
}

func callTool(ctx context.Context, registry *ToolRegistry, name string, args map[string]any) (ToolCallResult, error) {
	if registry == nil {
		return ToolCallResult{}, fmt.Errorf("tool registry is nil")
	}
	return registry.Call(ctx, name, args)
}

func (s *Server) authorize(c *gin.Context) bool {
	if s == nil || s.tokenProvider == nil {
		return true
	}

	cfg := s.tokenProvider.GetConfig()
	expectedToken := strings.TrimSpace(cfg.MCP.Token)
	if expectedToken == "" {
		return true
	}

	authorization := strings.TrimSpace(c.GetHeader("Authorization"))
	if authorization == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
		return false
	}

	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(strings.ToLower(authorization), strings.ToLower(bearerPrefix)) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization scheme"})
		return false
	}

	providedToken := strings.TrimSpace(authorization[len(bearerPrefix):])
	if providedToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid mcp token"})
		return false
	}

	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(expectedToken)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid mcp token"})
		return false
	}

	return true
}

func (s *Server) authorizeDangerousTool(c *gin.Context, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || s == nil || s.registry == nil {
		return true
	}

	definition, ok := s.registry.tools[toolName]
	if !ok || definition.PermissionLevel != ToolPermissionDanger {
		return true
	}

	confirmToken := strings.TrimSpace(c.GetHeader("X-MCP-Confirm"))
	if confirmToken == "" {
		confirmToken = strings.TrimSpace(c.Query("confirm_token"))
	}
	if confirmToken == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "dangerous tool requires X-MCP-Confirm header"})
		return false
	}

	cfg := s.tokenProvider.GetConfig()
	expected := strings.TrimSpace(cfg.MCP.Token)
	if expected == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "dangerous tool confirmation is unavailable"})
		return false
	}

	if subtle.ConstantTimeCompare([]byte(confirmToken), []byte(expected)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid dangerous tool confirmation token"})
		return false
	}

	return true
}

func (s *Server) allowRequest(c *gin.Context) bool {
	if s == nil || s.requestsPerMin <= 0 {
		return true
	}

	now := time.Now()
	windowStart := now.Truncate(time.Minute)

	s.rateMu.Lock()
	defer s.rateMu.Unlock()

	if s.rateWindowStart.IsZero() || !s.rateWindowStart.Equal(windowStart) {
		s.rateWindowStart = windowStart
		s.rateCount = 0
	}

	if s.rateCount >= s.requestsPerMin {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "mcp rate limit exceeded"})
		return false
	}

	s.rateCount += 1
	return true
}
