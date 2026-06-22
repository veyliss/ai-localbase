package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"ai-localbase/internal/model"
	"ai-localbase/internal/service"

	"github.com/gin-gonic/gin"
)

type TokenProvider interface {
	GetConfig() model.AppConfig
}

type APIKeyValidator interface {
	ValidateAPIKey(token string) (service.AuthPrincipal, error)
}

type DangerConfirmationValidator interface {
	ConsumeMCPDangerConfirmation(toolName string, args map[string]any, nonce string) error
}

type SecurityEventRecorder interface {
	RecordSecurityEvent(eventType, username, ip, userAgent, message string)
}

type Server struct {
	registry                    *ToolRegistry
	tokenProvider               TokenProvider
	apiKeyValidator             APIKeyValidator
	dangerConfirmationValidator DangerConfirmationValidator
	auditRecorder               SecurityEventRecorder
	serverConfig                model.ServerConfig
	requestTimeout              time.Duration
	requestsPerMin              int
	rateMu                      sync.Mutex
	rateWindowStart             time.Time
	rateCount                   int
}

type authContext struct {
	Mode      string
	Principal service.AuthPrincipal
}

const (
	authModeAPIKey          = "api_key"
	authModeCompatibleToken = "compatible_token"

	scopeMCPRead   = "mcp:read"
	scopeMCPWrite  = "mcp:write"
	scopeMCPDanger = "mcp:danger"
	scopeMCPUpload = "mcp:upload"
	scopeMCPEval   = "mcp:eval"
	scopeMCPAdmin  = "mcp:admin"
)

func NewServer(registry *ToolRegistry, tokenProvider TokenProvider, apiKeyValidator APIKeyValidator, serverConfig model.ServerConfig) *Server {
	timeout := time.Duration(serverConfig.MCPRequestTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	requestsPerMin := serverConfig.MCPRequestsPerMinute
	if requestsPerMin <= 0 {
		requestsPerMin = 120
	}
	dangerConfirmationValidator, _ := tokenProvider.(DangerConfirmationValidator)
	auditRecorder, _ := apiKeyValidator.(SecurityEventRecorder)
	return &Server{
		registry:                    registry,
		tokenProvider:               tokenProvider,
		apiKeyValidator:             apiKeyValidator,
		dangerConfirmationValidator: dangerConfirmationValidator,
		auditRecorder:               auditRecorder,
		serverConfig:                serverConfig,
		requestTimeout:              timeout,
		requestsPerMin:              requestsPerMin,
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
	authCtx, ok := s.authenticate(c)
	if !ok || !s.authorizeScopes(c, authCtx, scopeMCPRead) {
		return
	}
	if !s.allowRequest(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"name":            serverName,
		"version":         serverVersion,
		"protocolVersion": protocolVersion,
		"jsonrpc":         jsonRPCVersion,
		"capabilities":    gin.H{"tools": gin.H{"listChanged": false}},
		"transport":       "http",
		"toolCount":       len(s.registry.List()),
	})
}

func (s *Server) handleListTools(c *gin.Context) {
	authCtx, ok := s.authenticate(c)
	if !ok || !s.authorizeScopes(c, authCtx, scopeMCPRead) {
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
	authCtx, ok := s.authenticate(c)
	if !ok {
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
		if !s.authorizeScopes(c, authCtx, scopeMCPRead) {
			return
		}
		log.Printf("mcp request method=%s remote=%s duration_ms=%d", method, c.ClientIP(), time.Since(startedAt).Milliseconds())
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: jsonRPCVersion,
			ID:      request.ID,
			Result: map[string]any{
				"protocolVersion": protocolVersion,
				"serverInfo": map[string]any{
					"name":    serverName,
					"version": serverVersion,
				},
				"capabilities": map[string]any{
					"tools": map[string]any{
						"listChanged": false,
					},
				},
			},
		})
	case "tools/list":
		if !s.authorizeScopes(c, authCtx, scopeMCPRead) {
			return
		}
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
		var definition ToolDefinition
		hasDefinition := false
		for _, tool := range s.registry.List() {
			if tool.Name == toolName {
				permissionLevel = string(tool.PermissionLevel)
				definition = tool
				hasDefinition = true
				break
			}
		}
		isDanger := hasDefinition && definition.PermissionLevel == ToolPermissionDanger
		if hasDefinition && !s.authorizeScopes(c, authCtx, requiredScopesForTool(definition)...) {
			s.recordMCPAudit(c, authCtx, toolName, permissionLevel, startedAt, false, isDanger, "missing required mcp scope")
			return
		}
		if !hasDefinition && !s.authorizeScopes(c, authCtx, scopeMCPRead) {
			s.recordMCPAudit(c, authCtx, toolName, permissionLevel, startedAt, false, false, "missing required mcp scope")
			return
		}
		if !s.authorizeDangerousTool(c, toolName, arguments, authCtx) {
			s.recordMCPAudit(c, authCtx, toolName, permissionLevel, startedAt, false, isDanger, "danger confirmation failed")
			return
		}
		if hasDefinition && definition.PermissionLevel == ToolPermissionDanger {
			arguments = withoutConfirmNonce(arguments)
		}
		log.Printf("mcp tool call start tool=%s permission=%s remote=%s args=%s", toolName, permissionLevel, c.ClientIP(), summarizeToolArguments(arguments))
		result, err := s.registry.Call(ctx, toolName, arguments)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("mcp tool call timeout tool=%s permission=%s remote=%s duration_ms=%d error=%v", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds(), ctx.Err())
				s.recordMCPAudit(c, authCtx, toolName, permissionLevel, startedAt, false, isDanger, ctx.Err().Error())
				c.JSON(http.StatusGatewayTimeout, errorResponse(request.ID, -32001, "mcp request timed out"))
				return
			}
			log.Printf("mcp tool call failed tool=%s permission=%s remote=%s duration_ms=%d error=%v", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds(), err)
			s.recordMCPAudit(c, authCtx, toolName, permissionLevel, startedAt, false, isDanger, err.Error())
			c.JSON(http.StatusOK, errorResponse(request.ID, -32000, err.Error()))
			return
		}
		if ctx.Err() != nil {
			log.Printf("mcp tool call timeout tool=%s permission=%s remote=%s duration_ms=%d error=%v", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds(), ctx.Err())
			s.recordMCPAudit(c, authCtx, toolName, permissionLevel, startedAt, false, isDanger, ctx.Err().Error())
			c.JSON(http.StatusGatewayTimeout, errorResponse(request.ID, -32001, "mcp request timed out"))
			return
		}
		log.Printf("mcp tool call tool=%s permission=%s remote=%s duration_ms=%d is_error=%t", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds(), result.IsError)
		s.recordMCPAudit(c, authCtx, toolName, permissionLevel, startedAt, !result.IsError, isDanger, "")
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
			"inputSchema": inputSchemaForTool(tool),
			"annotations": map[string]any{
				"readOnlyHint":    tool.ReadOnly,
				"permissionLevel": tool.PermissionLevel,
				"requiredScopes":  requiredScopesForTool(tool),
			},
		})
	}
	return items
}

func inputSchemaForTool(tool ToolDefinition) map[string]any {
	if tool.PermissionLevel != ToolPermissionDanger {
		return tool.InputSchema
	}
	schema := cloneSchemaMap(tool.InputSchema)
	sourceProperties, _ := schema["properties"].(map[string]any)
	properties := cloneSchemaMap(sourceProperties)
	properties["confirmNonce"] = map[string]any{
		"type":        "string",
		"description": "一次性危险工具确认 nonce，可通过 POST /api/config/mcp/danger-confirmations 获取。",
	}
	schema["properties"] = properties
	return schema
}

func cloneSchemaMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
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

func summarizeToolArguments(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}

	summary := make(map[string]any, len(args))
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		summary[key] = summarizeToolArgumentValue(key, args[key])
	}

	encoded, err := json.Marshal(summary)
	if err != nil {
		return fmt.Sprintf("<marshal_error:%v>", err)
	}
	return string(encoded)
}

func summarizeToolArgumentValue(key string, value any) any {
	trimmedKey := strings.TrimSpace(key)
	lowerKey := strings.ToLower(trimmedKey)

	switch typed := value.(type) {
	case string:
		length := len(typed)
		switch lowerKey {
		case "contentbase64":
			return map[string]any{"type": "string", "chars": length, "preview": "<base64 omitted>"}
		case "content":
			return map[string]any{"type": "string", "chars": length, "preview": previewLogString(typed, 120)}
		default:
			return map[string]any{"type": "string", "chars": length, "preview": previewLogString(typed, 80)}
		}
	case []any:
		return map[string]any{"type": "array", "len": len(typed)}
	case map[string]any:
		return map[string]any{"type": "object", "keys": sortedMapKeys(typed)}
	default:
		return value
	}
}

func previewLogString(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if limit <= 0 {
		limit = 80
	}
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return string(runes[:limit]) + "…"
}

func sortedMapKeys(items map[string]any) []string {
	if len(items) == 0 {
		return []string{}
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (s *Server) authenticate(c *gin.Context) (authContext, bool) {
	if s == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mcp server is unavailable"})
		return authContext{}, false
	}
	if !s.serverConfig.EnableAuth {
		c.JSON(http.StatusForbidden, gin.H{"error": "mcp requires ENABLE_AUTH=true and an API key or compatible token"})
		return authContext{}, false
	}

	authorization := strings.TrimSpace(c.GetHeader("Authorization"))
	if authorization == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
		return authContext{}, false
	}

	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(strings.ToLower(authorization), strings.ToLower(bearerPrefix)) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization scheme"})
		return authContext{}, false
	}

	providedToken := strings.TrimSpace(authorization[len(bearerPrefix):])
	if providedToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid bearer token"})
		return authContext{}, false
	}

	if strings.HasPrefix(providedToken, "ailb_sk_") {
		if s.apiKeyValidator == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mcp api key validator is unavailable"})
			return authContext{}, false
		}
		principal, err := s.apiKeyValidator.ValidateAPIKey(providedToken)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired api key"})
			return authContext{}, false
		}
		return authContext{Mode: authModeAPIKey, Principal: principal}, true
	}

	if s.tokenProvider == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mcp token provider is unavailable"})
		return authContext{}, false
	}

	cfg := s.tokenProvider.GetConfig()
	expectedToken := strings.TrimSpace(cfg.MCP.Token)
	if expectedToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "mcp token is not configured"})
		return authContext{}, false
	}

	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(expectedToken)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid mcp token"})
		return authContext{}, false
	}

	return authContext{Mode: authModeCompatibleToken}, true
}

func (s *Server) authorizeScopes(c *gin.Context, authCtx authContext, requiredScopes ...string) bool {
	if authCtx.Mode == authModeCompatibleToken {
		return true
	}
	if authCtx.Mode != authModeAPIKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid mcp authorization"})
		return false
	}
	if hasMCPScopes(authCtx.Principal.Scopes, requiredScopes...) {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{
		"error":          "api key does not have required mcp scope",
		"requiredScopes": requiredScopes,
	})
	return false
}

func (s *Server) authorizeDangerousTool(c *gin.Context, toolName string, args map[string]any, authCtx authContext) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || s == nil || s.registry == nil {
		return true
	}

	definition, ok := s.registry.tools[toolName]
	if !ok || definition.PermissionLevel != ToolPermissionDanger {
		return true
	}
	confirmNonce := optionalMCPConfirmNonce(args)
	if confirmNonce != "" {
		if s.dangerConfirmationValidator == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mcp danger confirmation validator is unavailable"})
			return false
		}
		if err := s.dangerConfirmationValidator.ConsumeMCPDangerConfirmation(toolName, args, confirmNonce); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return false
		}
		return true
	}

	confirmToken := strings.TrimSpace(c.GetHeader("X-MCP-Confirm"))
	if confirmToken == "" {
		confirmToken = strings.TrimSpace(c.Query("confirm_token"))
	}
	if confirmToken == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "dangerous tool requires confirmNonce"})
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

func optionalMCPConfirmNonce(args map[string]any) string {
	if args == nil {
		return ""
	}
	value, _ := args["confirmNonce"].(string)
	return strings.TrimSpace(value)
}

func withoutConfirmNonce(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	cloned := make(map[string]any, len(args))
	for key, value := range args {
		if key == "confirmNonce" {
			continue
		}
		cloned[key] = value
	}
	return cloned
}

func (s *Server) recordMCPAudit(c *gin.Context, authCtx authContext, toolName, permissionLevel string, startedAt time.Time, success bool, isDanger bool, errorSummary string) {
	if s == nil || s.auditRecorder == nil {
		return
	}
	eventType := "mcp_call_succeeded"
	if isDanger {
		eventType = "mcp_danger_succeeded"
	}
	if !success {
		eventType = "mcp_call_failed"
		if isDanger {
			eventType = "mcp_danger_failed"
		}
	}
	username := strings.TrimSpace(authCtx.Principal.Username)
	if username == "" {
		username = "mcp-compatible-token"
	}
	apiKeyID := strings.TrimSpace(authCtx.Principal.APIKeyID)
	if apiKeyID == "" {
		apiKeyID = "-"
	}
	message := fmt.Sprintf(
		"tool=%s permission=%s auth=%s apiKeyId=%s success=%t danger=%t durationMs=%d",
		toolName,
		permissionLevel,
		authCtx.Mode,
		apiKeyID,
		success,
		isDanger,
		time.Since(startedAt).Milliseconds(),
	)
	if trimmedError := strings.TrimSpace(errorSummary); trimmedError != "" {
		message += " error=" + previewLogString(trimmedError, 140)
	}
	s.auditRecorder.RecordSecurityEvent(eventType, username, c.ClientIP(), c.Request.UserAgent(), message)
}

func requiredScopesForTool(tool ToolDefinition) []string {
	switch {
	case tool.PermissionLevel == ToolPermissionDanger:
		return []string{scopeMCPDanger}
	case tool.Name == "generate_eval_dataset":
		return []string{scopeMCPEval}
	case isMCPUploadTool(tool.Name):
		return []string{scopeMCPUpload}
	case tool.PermissionLevel == ToolPermissionWrite:
		return []string{scopeMCPWrite}
	default:
		return []string{scopeMCPRead}
	}
}

func isMCPUploadTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "upload_text_document", "upload_document", "register_staged_upload":
		return true
	default:
		return false
	}
}

func hasMCPScopes(grantedScopes []string, requiredScopes ...string) bool {
	if len(requiredScopes) == 0 {
		return true
	}
	granted := make(map[string]struct{}, len(grantedScopes))
	for _, scope := range grantedScopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope != "" {
			granted[scope] = struct{}{}
		}
	}
	if _, ok := granted[scopeMCPAdmin]; ok {
		return true
	}
	for _, scope := range requiredScopes {
		if _, ok := granted[strings.ToLower(strings.TrimSpace(scope))]; !ok {
			return false
		}
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
