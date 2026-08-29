package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
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
	ConsumeMCPDangerConfirmationAs(toolName string, args map[string]any, nonce string, owner service.AuthPrincipal) error
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
	rateBuckets                 map[string]mcpRateBucket
	metricsMu                   sync.Mutex
	metrics                     mcpMetricsState
}

type mcpRateBucket struct {
	windowStart time.Time
	count       int
}

type mcpMetricsState struct {
	startedAt          time.Time
	requestsTotal      int64
	requestsSucceeded  int64
	requestsFailed     int64
	toolCallsTotal     int64
	toolCallsSucceeded int64
	toolCallsFailed    int64
	rateLimited        int64
	authFailures       int64
	scopeDenied        int64
	requestLatencies   []int64
	toolLatencies      []int64
	toolMetrics        map[string]mcpToolMetricState
}

type mcpToolMetricState struct {
	callsTotal     int64
	callsSucceeded int64
	callsFailed    int64
	latencies      []int64
}

type authContext struct {
	Mode      string
	Principal service.AuthPrincipal
}

type principalContextKey struct{}

func principalFromContext(ctx context.Context) service.AuthPrincipal {
	if ctx == nil {
		return service.AuthPrincipal{}
	}
	principal, _ := ctx.Value(principalContextKey{}).(service.AuthPrincipal)
	return principal
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

var (
	mcpURLPattern        = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
	mcpFilesystemPattern = regexp.MustCompile(`(?i)(?:/Users/|/app/|/var/|/tmp/|[A-Za-z]:[\\/])[^\s"'<>]+`)
	mcpCollectionPattern = regexp.MustCompile(`(?i)\b(?:qdrant\s+)?collection\s+[A-Za-z0-9_.:-]+`)
	mcpSecretPattern     = regexp.MustCompile(`(?i)(?:ailb_sk_|mcp_confirm_)[A-Za-z0-9_-]+`)
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
		rateBuckets:                 map[string]mcpRateBucket{},
		metrics: mcpMetricsState{
			startedAt:   time.Now().UTC(),
			toolMetrics: map[string]mcpToolMetricState{},
		},
	}
}

func (s *Server) RegisterRoutes(group *gin.RouterGroup) {
	if s == nil || group == nil {
		return
	}

	group.GET("", s.handleInfo)
	group.GET("/tools", s.handleListTools)
	group.GET("/metrics", s.handleMetrics)
	group.POST("", s.handleJSONRPC)
}

func (s *Server) handleInfo(c *gin.Context) {
	startedAt := time.Now()
	requestSucceeded := false
	defer func() {
		s.recordMCPRequestMetric(requestSucceeded, time.Since(startedAt))
	}()
	authCtx, ok := s.authenticate(c)
	if !ok || !s.authorizeScopes(c, authCtx, scopeMCPRead) {
		return
	}
	if !s.allowRequest(c, authCtx) {
		return
	}
	requestSucceeded = true
	c.JSON(http.StatusOK, gin.H{
		"name":                  serverName,
		"version":               serverVersion,
		"protocolVersion":       protocolVersion,
		"jsonrpc":               jsonRPCVersion,
		"capabilities":          gin.H{"tools": gin.H{"listChanged": false}, "metrics": gin.H{"path": "/metrics", "scope": scopeMCPRead}},
		"resultContractVersion": resultContractVersion,
		"errorCodes":            mcpErrorCatalog(),
		"transport":             "http",
		"toolCount":             len(s.registry.List()),
	})
	s.recordMCPRequestAudit(c, authCtx, "GET /mcp", startedAt, true, "")
}

func (s *Server) handleMetrics(c *gin.Context) {
	startedAt := time.Now()
	requestSucceeded := false
	defer func() {
		s.recordMCPRequestMetric(requestSucceeded, time.Since(startedAt))
	}()
	authCtx, ok := s.authenticate(c)
	if !ok || !s.authorizeScopes(c, authCtx, scopeMCPRead) {
		return
	}
	if !s.allowRequest(c, authCtx) {
		return
	}
	requestSucceeded = true
	c.JSON(http.StatusOK, s.metricsSnapshot())
	s.recordMCPRequestAudit(c, authCtx, "GET /mcp/metrics", startedAt, true, "")
}

func (s *Server) handleListTools(c *gin.Context) {
	startedAt := time.Now()
	requestSucceeded := false
	defer func() {
		s.recordMCPRequestMetric(requestSucceeded, time.Since(startedAt))
	}()
	authCtx, ok := s.authenticate(c)
	if !ok || !s.authorizeScopes(c, authCtx, scopeMCPRead) {
		return
	}
	if !s.allowRequest(c, authCtx) {
		return
	}
	requestSucceeded = true
	c.JSON(http.StatusOK, gin.H{
		"tools":                 s.toolDescriptors(),
		"resultContractVersion": resultContractVersion,
		"errorCodes":            mcpErrorCatalog(),
	})
	s.recordMCPRequestAudit(c, authCtx, "GET /mcp/tools", startedAt, true, "")
}

func (s *Server) handleJSONRPC(c *gin.Context) {
	startedAt := time.Now()
	requestSucceeded := false
	defer func() {
		s.recordMCPRequestMetric(requestSucceeded, time.Since(startedAt))
	}()
	authCtx, ok := s.authenticate(c)
	if !ok {
		return
	}
	if !s.allowRequest(c, authCtx) {
		return
	}
	if !validateJSONRPCHeaders(c) {
		s.recordMCPEvent(c, authCtx, "mcp_protocol_failed", "invalid MCP JSON-RPC HTTP headers")
		return
	}

	ctx := c.Request.Context()
	if s.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.requestTimeout)
		defer cancel()
	}
	ctx = context.WithValue(ctx, principalContextKey{}, authCtx.Principal)

	var request JSONRPCRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		s.recordMCPEvent(c, authCtx, "mcp_protocol_failed", "invalid JSON-RPC request body")
		log.Printf("mcp request failed remote=%s error=%s", c.ClientIP(), sanitizeMCPError(err.Error()))
		c.JSON(http.StatusBadRequest, errorResponse(nil, -32700, "invalid json-rpc request body"))
		return
	}
	if request.JSONRPC != jsonRPCVersion || strings.TrimSpace(request.Method) == "" {
		s.recordMCPEvent(c, authCtx, "mcp_protocol_failed", "invalid JSON-RPC request envelope")
		writeJSONRPCResponse(c, http.StatusOK, errorResponse(request.ID, -32600, "invalid json-rpc request"), request.ID == nil)
		return
	}

	method := strings.TrimSpace(request.Method)
	isNotification := request.ID == nil
	switch method {
	case "initialize":
		if !s.authorizeScopes(c, authCtx, scopeMCPRead) {
			return
		}
		requestSucceeded = true
		response := JSONRPCResponse{
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
					"metrics": map[string]any{
						"path":  "/metrics",
						"scope": scopeMCPRead,
					},
				},
				"resultContractVersion": resultContractVersion,
				"errorCodes":            mcpErrorCatalog(),
			},
		}
		s.recordMCPRequestAudit(c, authCtx, method, startedAt, true, "")
		log.Printf("mcp request method=%s remote=%s duration_ms=%d", method, c.ClientIP(), time.Since(startedAt).Milliseconds())
		writeJSONRPCResponse(c, http.StatusOK, response, isNotification)
	case "notifications/initialized":
		requestSucceeded = true
		s.recordMCPRequestAudit(c, authCtx, method, startedAt, true, "")
		c.Status(http.StatusAccepted)
	case "ping":
		if !s.authorizeScopes(c, authCtx, scopeMCPRead) {
			return
		}
		requestSucceeded = true
		s.recordMCPRequestAudit(c, authCtx, method, startedAt, true, "")
		writeJSONRPCResponse(c, http.StatusOK, JSONRPCResponse{
			JSONRPC: jsonRPCVersion,
			ID:      request.ID,
			Result:  map[string]any{},
		}, isNotification)
	case "tools/list":
		if !s.authorizeScopes(c, authCtx, scopeMCPRead) {
			return
		}
		requestSucceeded = true
		log.Printf("mcp request method=%s remote=%s duration_ms=%d", method, c.ClientIP(), time.Since(startedAt).Milliseconds())
		s.recordMCPRequestAudit(c, authCtx, method, startedAt, true, "")
		writeJSONRPCResponse(c, http.StatusOK, JSONRPCResponse{
			JSONRPC: jsonRPCVersion,
			ID:      request.ID,
			Result: map[string]any{
				"tools":                 s.toolDescriptors(),
				"resultContractVersion": resultContractVersion,
				"errorCodes":            mcpErrorCatalog(),
			},
		}, isNotification)
	case "tools/call":
		if request.Params == nil {
			s.recordMCPEvent(c, authCtx, "mcp_protocol_failed", "tools/call missing params")
			writeJSONRPCResponse(c, http.StatusOK, protocolErrorResponse(request.ID, "invalid tools/call params", MCPErrorInvalidArgument, requestIDFromContext(c)), isNotification)
			return
		}
		toolName, _ := request.Params["name"].(string)
		arguments := map[string]any{}
		if rawArguments, exists := request.Params["arguments"]; exists && rawArguments != nil {
			var valid bool
			arguments, valid = rawArguments.(map[string]any)
			if !valid {
				s.recordMCPEvent(c, authCtx, "mcp_protocol_failed", "tools/call arguments must be an object")
				writeJSONRPCResponse(c, http.StatusOK, protocolErrorResponse(request.ID, "invalid tools/call arguments", MCPErrorInvalidArgument, requestIDFromContext(c)), isNotification)
				return
			}
		}
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
		if hasDefinition && !s.authorizeScopes(c, authCtx, requiredScopesForToolCall(definition, arguments)...) {
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
				log.Printf("mcp tool call timeout tool=%s permission=%s remote=%s duration_ms=%d", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds())
				s.recordMCPAudit(c, authCtx, toolName, permissionLevel, startedAt, false, isDanger, ctx.Err().Error())
				writeJSONRPCResponse(c, http.StatusGatewayTimeout, toolErrorResponse(request.ID, toolName, requestIDFromContext(c), -32001, "mcp request timed out", ctx.Err(), ctx), isNotification)
				return
			}
			safeError := sanitizeMCPError(err.Error())
			log.Printf("mcp tool call failed tool=%s permission=%s remote=%s duration_ms=%d error=%s", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds(), safeError)
			s.recordMCPAudit(c, authCtx, toolName, permissionLevel, startedAt, false, isDanger, safeError)
			writeJSONRPCResponse(c, http.StatusOK, toolErrorResponse(request.ID, toolName, requestIDFromContext(c), -32000, safeError, err, ctx), isNotification)
			return
		}
		if ctx.Err() != nil {
			log.Printf("mcp tool call timeout tool=%s permission=%s remote=%s duration_ms=%d", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds())
			s.recordMCPAudit(c, authCtx, toolName, permissionLevel, startedAt, false, isDanger, ctx.Err().Error())
			writeJSONRPCResponse(c, http.StatusGatewayTimeout, toolErrorResponse(request.ID, toolName, requestIDFromContext(c), -32001, "mcp request timed out", ctx.Err(), ctx), isNotification)
			return
		}
		result = normalizeToolCallResult(result, requestIDFromContext(c))
		requestSucceeded = !result.IsError
		log.Printf("mcp tool call tool=%s permission=%s remote=%s duration_ms=%d is_error=%t", toolName, permissionLevel, c.ClientIP(), time.Since(startedAt).Milliseconds(), result.IsError)
		s.recordMCPAudit(c, authCtx, toolName, permissionLevel, startedAt, !result.IsError, isDanger, "")
		writeJSONRPCResponse(c, http.StatusOK, JSONRPCResponse{
			JSONRPC: jsonRPCVersion,
			ID:      request.ID,
			Result: map[string]any{
				"summary":         result.Summary,
				"content":         result.Content,
				"data":            result.Data,
				"warnings":        result.Warnings,
				"nextActions":     result.NextActions,
				"requestId":       result.RequestID,
				"isError":         result.IsError,
				"contractVersion": result.ContractVersion,
				"error":           result.Error,
			},
		}, isNotification)
	default:
		s.recordMCPEvent(c, authCtx, "mcp_protocol_failed", "method not found")
		log.Printf("mcp request method_not_found method=%s remote=%s duration_ms=%d", method, c.ClientIP(), time.Since(startedAt).Milliseconds())
		writeJSONRPCResponse(c, http.StatusOK, errorResponse(request.ID, -32601, "method not found"), isNotification)
	}
}

func writeJSONRPCResponse(c *gin.Context, status int, response JSONRPCResponse, notification bool) {
	if notification {
		c.Status(http.StatusAccepted)
		return
	}
	c.JSON(status, response)
}

func validateJSONRPCHeaders(c *gin.Context) bool {
	contentType := strings.ToLower(strings.TrimSpace(c.GetHeader("Content-Type")))
	if !strings.HasPrefix(contentType, "application/json") {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "mcp JSON-RPC requires Content-Type: application/json"})
		return false
	}
	accept := strings.ToLower(strings.TrimSpace(c.GetHeader("Accept")))
	if accept != "" && !strings.Contains(accept, "application/json") && !strings.Contains(accept, "*/*") {
		c.JSON(http.StatusNotAcceptable, gin.H{"error": "mcp server currently returns application/json responses"})
		return false
	}
	return true
}

func normalizeToolCallResult(result ToolCallResult, requestID string) ToolCallResult {
	if strings.TrimSpace(result.ContractVersion) == "" {
		result.ContractVersion = resultContractVersion
	}
	if strings.TrimSpace(result.Summary) == "" && len(result.Content) > 0 {
		result.Summary = strings.TrimSpace(result.Content[0].Text)
	}
	if result.Warnings == nil {
		result.Warnings = []string{}
	}
	if result.Content == nil {
		result.Content = []ToolContent{}
	}
	if result.NextActions == nil {
		result.NextActions = []string{}
	}
	result.RequestID = strings.TrimSpace(requestID)
	if result.Error != nil {
		result.IsError = true
	}
	if result.IsError && result.Error == nil {
		errorValue := newMCPToolError(MCPErrorInternal, "tool returned an error", requestID)
		result.Error = &errorValue
	}
	if result.Error != nil {
		result.Error.Code = string(normalizeMCPErrorCode(result.Error.Code))
		descriptor := mcpErrorDescriptor(MCPErrorCode(result.Error.Code))
		result.Error.Retryable = descriptor.Retryable
		result.Error.Message = sanitizeMCPError(result.Error.Message)
		result.Error.RequestID = strings.TrimSpace(requestID)
	}
	return result
}

func requestIDFromContext(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value, ok := c.Get("requestId"); ok {
		if requestID, ok := value.(string); ok {
			return requestID
		}
	}
	return strings.TrimSpace(c.GetHeader("X-Request-Id"))
}

func (s *Server) toolDescriptors() []map[string]any {
	tools := s.registry.List()
	items := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		annotations := map[string]any{
			"readOnlyHint":    tool.ReadOnly,
			"permissionLevel": tool.PermissionLevel,
			"requiredScopes":  requiredScopesForTool(tool),
		}
		if tool.Name == "start_import_job" {
			annotations["scopeVariants"] = map[string][]string{
				"import":       []string{scopeMCPUpload},
				"batch_index":  []string{scopeMCPUpload},
				"reindex":      []string{scopeMCPWrite},
				"eval_dataset": []string{scopeMCPEval},
			}
		}
		items = append(items, map[string]any{
			"name":                  tool.Name,
			"description":           tool.Description,
			"inputSchema":           inputSchemaForTool(tool),
			"resultContractVersion": resultContractVersion,
			"errorCodes":            mcpErrorCatalog(),
			"annotations":           annotations,
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
	required := []string{}
	if sourceRequired, ok := schema["required"].([]string); ok {
		required = append(required, sourceRequired...)
	} else if sourceRequired, ok := schema["required"].([]any); ok {
		for _, value := range sourceRequired {
			if name, ok := value.(string); ok {
				required = append(required, name)
			}
		}
	}
	seen := false
	for _, name := range required {
		if name == "confirmNonce" {
			seen = true
			break
		}
	}
	if !seen {
		required = append(required, "confirmNonce")
	}
	schema["required"] = required
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

func writeMCPHTTPError(c *gin.Context, status int, code MCPErrorCode, message string) {
	writeMCPHTTPErrorWithData(c, status, code, message, nil)
}

func writeMCPHTTPErrorWithData(c *gin.Context, status int, code MCPErrorCode, message string, data map[string]any) {
	if c == nil {
		return
	}
	payload := gin.H{
		"error":     sanitizeMCPError(message),
		"errorCode": string(normalizeMCPErrorCode(string(code))),
		"requestId": requestIDFromContext(c),
	}
	for key, value := range data {
		payload[key] = value
	}
	c.JSON(status, payload)
}

func protocolErrorResponse(id any, message string, code MCPErrorCode, requestID string) JSONRPCResponse {
	errorValue := newMCPToolError(code, message, requestID)
	response := errorResponse(id, -32602, sanitizeMCPError(message))
	response.Error.Data = map[string]any{
		"contractVersion": resultContractVersion,
		"requestId":       strings.TrimSpace(requestID),
		"error":           errorValue,
	}
	return response
}

func toolErrorResponse(id any, toolName, requestID string, jsonRPCCode int, message string, err error, ctx context.Context) JSONRPCResponse {
	toolError := classifyToolCallError(err, ctx)
	toolError.Message = sanitizeMCPError(message)
	toolError.RequestID = strings.TrimSpace(requestID)
	response := errorResponse(id, jsonRPCCode, toolError.Message)
	response.Error.Data = map[string]any{
		"contractVersion": resultContractVersion,
		"tool":            strings.TrimSpace(toolName),
		"requestId":       strings.TrimSpace(requestID),
		"error":           toolError,
	}
	return response
}

func classifyToolCallError(err error, ctx context.Context) ToolCallError {
	message := "mcp operation failed"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = sanitizeMCPError(err.Error())
	}
	if ctx != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return newMCPToolError(MCPErrorTimeout, "mcp request timed out", "")
		case errors.Is(ctx.Err(), context.Canceled):
			return newMCPToolError(MCPErrorCancelled, "mcp request was cancelled", "")
		}
	}

	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "required"), strings.Contains(lower, "must be"), strings.Contains(lower, "invalid"):
		return newMCPToolError(MCPErrorInvalidArgument, message, "")
	case strings.Contains(lower, "not found"), strings.Contains(lower, "does not exist"):
		return newMCPToolError(MCPErrorNotFound, message, "")
	case strings.Contains(lower, "already exists"), strings.Contains(lower, "conflict"):
		return newMCPToolError(MCPErrorConflict, message, "")
	case strings.Contains(lower, "not indexed"), strings.Contains(lower, "indexing") || strings.Contains(lower, "index not"):
		return newMCPToolError(MCPErrorIndexNotReady, message, "")
	case strings.Contains(lower, "permission"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "forbidden"):
		return newMCPToolError(MCPErrorPermissionDenied, message, "")
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "too many requests"):
		return newMCPToolError(MCPErrorRateLimited, message, "")
	case strings.Contains(lower, "confirmnonce"), strings.Contains(lower, "confirmation"):
		return newMCPToolError(MCPErrorConfirmationRequired, message, "")
	case strings.Contains(lower, "unavailable"), strings.Contains(lower, "qdrant"), strings.Contains(lower, "embedding"), strings.Contains(lower, "model"), strings.Contains(lower, "temporarily"):
		return newMCPToolError(MCPErrorDependencyUnavailable, message, "")
	default:
		return newMCPToolError(MCPErrorInternal, message, "")
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
		default:
			return map[string]any{"type": "string", "chars": length, "preview": "<omitted>"}
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

func sanitizeMCPError(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "mcp operation failed"
	}
	message = mcpURLPattern.ReplaceAllString(message, "<redacted-url>")
	message = mcpFilesystemPattern.ReplaceAllString(message, "<redacted-path>")
	message = mcpSecretPattern.ReplaceAllString(message, "<redacted-secret>")
	message = mcpCollectionPattern.ReplaceAllString(message, "vector collection")
	return previewLogString(message, 240)
}

func (s *Server) recordMCPEvent(c *gin.Context, authCtx authContext, eventType, message string) {
	if s == nil {
		return
	}
	s.recordMCPEventMetric(eventType)
	if s.auditRecorder == nil || c == nil {
		return
	}
	username := strings.TrimSpace(authCtx.Principal.Username)
	if username == "" {
		if authCtx.Mode == authModeCompatibleToken {
			username = "mcp-compatible-token"
		} else {
			username = "anonymous"
		}
	}
	message = sanitizeMCPError(message)
	s.auditRecorder.RecordSecurityEvent(eventType, username, c.ClientIP(), c.Request.UserAgent(), message)
}

func (s *Server) recordMCPRequestAudit(c *gin.Context, authCtx authContext, method string, startedAt time.Time, success bool, errorSummary string) {
	eventType := "mcp_request_succeeded"
	if !success {
		eventType = "mcp_request_failed"
	}
	message := fmt.Sprintf("method=%s success=%t durationMs=%d", method, success, time.Since(startedAt).Milliseconds())
	if strings.TrimSpace(errorSummary) != "" {
		message += " error=" + sanitizeMCPError(errorSummary)
	}
	s.recordMCPEvent(c, authCtx, eventType, message)
}

func (s *Server) authenticate(c *gin.Context) (authContext, bool) {
	if s == nil {
		writeMCPHTTPError(c, http.StatusServiceUnavailable, MCPErrorDependencyUnavailable, "mcp server is unavailable")
		return authContext{}, false
	}
	if !s.serverConfig.EnableAuth {
		s.rejectMCPAuth(c, http.StatusForbidden, "mcp requires ENABLE_AUTH=true and an API key or compatible token")
		return authContext{}, false
	}

	authorization := strings.TrimSpace(c.GetHeader("Authorization"))
	if authorization == "" {
		s.rejectMCPAuth(c, http.StatusUnauthorized, "missing authorization header")
		return authContext{}, false
	}

	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(strings.ToLower(authorization), strings.ToLower(bearerPrefix)) {
		s.rejectMCPAuth(c, http.StatusUnauthorized, "invalid authorization scheme")
		return authContext{}, false
	}

	providedToken := strings.TrimSpace(authorization[len(bearerPrefix):])
	if providedToken == "" {
		s.rejectMCPAuth(c, http.StatusUnauthorized, "invalid bearer token")
		return authContext{}, false
	}

	if strings.HasPrefix(providedToken, "ailb_sk_") {
		if s.apiKeyValidator == nil {
			s.rejectMCPAuth(c, http.StatusServiceUnavailable, "mcp api key validator is unavailable")
			return authContext{}, false
		}
		principal, err := s.apiKeyValidator.ValidateAPIKey(providedToken)
		if err != nil {
			s.rejectMCPAuth(c, http.StatusUnauthorized, "invalid or expired api key")
			return authContext{}, false
		}
		return authContext{Mode: authModeAPIKey, Principal: principal}, true
	}

	if !s.serverConfig.EnableMCPLegacyToken {
		s.rejectMCPAuth(c, http.StatusUnauthorized, "mcp legacy token authentication is disabled; use an API key with mcp scopes")
		return authContext{}, false
	}

	if s.tokenProvider == nil {
		s.rejectMCPAuth(c, http.StatusServiceUnavailable, "mcp token provider is unavailable")
		return authContext{}, false
	}

	cfg := s.tokenProvider.GetConfig()
	expectedToken := strings.TrimSpace(cfg.MCP.Token)
	if expectedToken == "" {
		s.rejectMCPAuth(c, http.StatusUnauthorized, "mcp token is not configured")
		return authContext{}, false
	}

	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(expectedToken)) != 1 {
		s.rejectMCPAuth(c, http.StatusUnauthorized, "invalid mcp token")
		return authContext{}, false
	}

	return authContext{Mode: authModeCompatibleToken}, true
}

func (s *Server) rejectMCPAuth(c *gin.Context, status int, message string) {
	s.recordMCPEvent(c, authContext{}, "mcp_auth_failed", message)
	code := MCPErrorUnauthenticated
	if status >= http.StatusInternalServerError {
		code = MCPErrorDependencyUnavailable
	}
	writeMCPHTTPError(c, status, code, message)
}

func (s *Server) authorizeScopes(c *gin.Context, authCtx authContext, requiredScopes ...string) bool {
	if authCtx.Mode == authModeCompatibleToken {
		return true
	}
	if authCtx.Mode != authModeAPIKey {
		s.recordMCPEvent(c, authCtx, "mcp_scope_denied", "invalid MCP authorization")
		writeMCPHTTPError(c, http.StatusUnauthorized, MCPErrorUnauthenticated, "invalid mcp authorization")
		return false
	}
	if hasMCPScopes(authCtx.Principal.Scopes, requiredScopes...) {
		return true
	}
	s.recordMCPEvent(c, authCtx, "mcp_scope_denied", "missing required mcp scope")
	writeMCPHTTPErrorWithData(c, http.StatusForbidden, MCPErrorPermissionDenied, "api key does not have required mcp scope", map[string]any{
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
			writeMCPHTTPError(c, http.StatusServiceUnavailable, MCPErrorDependencyUnavailable, "mcp danger confirmation validator is unavailable")
			return false
		}
		if err := s.dangerConfirmationValidator.ConsumeMCPDangerConfirmationAs(toolName, args, confirmNonce, authCtx.Principal); err != nil {
			writeMCPHTTPError(c, http.StatusForbidden, MCPErrorConfirmationRequired, err.Error())
			return false
		}
		return true
	}

	confirmToken := strings.TrimSpace(c.GetHeader("X-MCP-Confirm"))
	if confirmToken == "" {
		confirmToken = strings.TrimSpace(c.Query("confirm_token"))
	}
	if confirmToken == "" {
		writeMCPHTTPError(c, http.StatusForbidden, MCPErrorConfirmationRequired, "dangerous tool requires confirmNonce")
		return false
	}
	writeMCPHTTPError(c, http.StatusForbidden, MCPErrorConfirmationRequired, "legacy dangerous tool confirmation is disabled; use confirmNonce")
	return false
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
	s.recordMCPToolMetric(toolName, success, time.Since(startedAt))
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
		message += " error=" + sanitizeMCPError(trimmedError)
	}
	s.recordMCPEvent(c, authCtx, eventType, message)
}

func (s *Server) recordMCPEventMetric(eventType string) {
	if s == nil {
		return
	}
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if s.metrics.startedAt.IsZero() {
		s.metrics.startedAt = time.Now().UTC()
	}
	switch eventType {
	case "mcp_rate_limited":
		s.metrics.rateLimited++
	case "mcp_auth_failed":
		s.metrics.authFailures++
	case "mcp_scope_denied":
		s.metrics.scopeDenied++
	}
}

func (s *Server) recordMCPRequestMetric(success bool, duration time.Duration) {
	if s == nil {
		return
	}
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if s.metrics.startedAt.IsZero() {
		s.metrics.startedAt = time.Now().UTC()
	}
	s.metrics.requestsTotal++
	if success {
		s.metrics.requestsSucceeded++
	} else {
		s.metrics.requestsFailed++
	}
	s.metrics.requestLatencies = appendMetricLatency(s.metrics.requestLatencies, duration)
}

func (s *Server) recordMCPToolMetric(toolName string, success bool, duration time.Duration) {
	if s == nil {
		return
	}
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if s.metrics.startedAt.IsZero() {
		s.metrics.startedAt = time.Now().UTC()
	}
	s.metrics.toolCallsTotal++
	if success {
		s.metrics.toolCallsSucceeded++
	} else {
		s.metrics.toolCallsFailed++
	}
	s.metrics.toolLatencies = appendMetricLatency(s.metrics.toolLatencies, duration)
	if s.metrics.toolMetrics == nil {
		s.metrics.toolMetrics = map[string]mcpToolMetricState{}
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		toolName = "<unknown>"
	}
	toolMetric := s.metrics.toolMetrics[toolName]
	toolMetric.callsTotal++
	if success {
		toolMetric.callsSucceeded++
	} else {
		toolMetric.callsFailed++
	}
	toolMetric.latencies = appendMetricLatency(toolMetric.latencies, duration)
	s.metrics.toolMetrics[toolName] = toolMetric
}

func appendMetricLatency(values []int64, duration time.Duration) []int64 {
	if duration < 0 {
		duration = 0
	}
	values = append(values, duration.Milliseconds())
	const maxSamples = 512
	if len(values) > maxSamples {
		values = values[len(values)-maxSamples:]
	}
	return values
}

func (s *Server) metricsSnapshot() MCPMetricsSnapshot {
	if s == nil {
		return MCPMetricsSnapshot{ContractVersion: resultContractVersion, ServerVersion: serverVersion}
	}
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	startedAt := s.metrics.startedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
		s.metrics.startedAt = startedAt
	}
	toolMetrics := make([]MCPToolMetrics, 0, len(s.metrics.toolMetrics))
	for toolName, metric := range s.metrics.toolMetrics {
		toolMetrics = append(toolMetrics, MCPToolMetrics{
			ToolName:       toolName,
			CallsTotal:     metric.callsTotal,
			CallsSucceeded: metric.callsSucceeded,
			CallsFailed:    metric.callsFailed,
			P50Ms:          metricPercentile(metric.latencies, 0.50),
			P95Ms:          metricPercentile(metric.latencies, 0.95),
			MaxMs:          metricMax(metric.latencies),
		})
	}
	sort.Slice(toolMetrics, func(i, j int) bool {
		return toolMetrics[i].ToolName < toolMetrics[j].ToolName
	})
	return MCPMetricsSnapshot{
		ContractVersion:    resultContractVersion,
		ServerVersion:      serverVersion,
		StartedAt:          startedAt.Format(time.RFC3339),
		RequestsTotal:      s.metrics.requestsTotal,
		RequestsSucceeded:  s.metrics.requestsSucceeded,
		RequestsFailed:     s.metrics.requestsFailed,
		ToolCallsTotal:     s.metrics.toolCallsTotal,
		ToolCallsSucceeded: s.metrics.toolCallsSucceeded,
		ToolCallsFailed:    s.metrics.toolCallsFailed,
		RateLimited:        s.metrics.rateLimited,
		AuthFailures:       s.metrics.authFailures,
		ScopeDenied:        s.metrics.scopeDenied,
		RequestP50Ms:       metricPercentile(s.metrics.requestLatencies, 0.50),
		RequestP95Ms:       metricPercentile(s.metrics.requestLatencies, 0.95),
		RequestMaxMs:       metricMax(s.metrics.requestLatencies),
		ToolP50Ms:          metricPercentile(s.metrics.toolLatencies, 0.50),
		ToolP95Ms:          metricPercentile(s.metrics.toolLatencies, 0.95),
		ToolMaxMs:          metricMax(s.metrics.toolLatencies),
		ToolMetrics:        toolMetrics,
	}
}

func metricPercentile(values []int64, ratio float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(float64(len(sorted)-1) * ratio)
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func metricMax(values []int64) int64 {
	var maximum int64
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}

func requiredScopesForTool(tool ToolDefinition) []string {
	switch {
	case tool.PermissionLevel == ToolPermissionDanger:
		return []string{scopeMCPDanger}
	case tool.Name == "generate_eval_dataset", tool.Name == "create_eval_case_from_query":
		return []string{scopeMCPEval}
	case tool.Name == "start_import_job":
		// The actual scope is selected by jobType; the descriptor advertises
		// the default import scope and exposes the other variants separately.
		return []string{scopeMCPUpload}
	case isMCPUploadTool(tool.Name):
		return []string{scopeMCPUpload}
	case tool.PermissionLevel == ToolPermissionWrite:
		return []string{scopeMCPWrite}
	default:
		return []string{scopeMCPRead}
	}
}

func requiredScopesForToolCall(tool ToolDefinition, args map[string]any) []string {
	if tool.Name != "start_import_job" {
		return requiredScopesForTool(tool)
	}
	switch strings.ToLower(strings.TrimSpace(optionalStringArg(args, "jobType"))) {
	case "reindex":
		return []string{scopeMCPWrite}
	case "eval_dataset":
		return []string{scopeMCPEval}
	default:
		return []string{scopeMCPUpload}
	}
}

func isMCPUploadTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "upload_text_document", "upload_document", "register_staged_upload", "start_import_job":
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

func (s *Server) allowRequest(c *gin.Context, authCtx authContext) bool {
	if s == nil || s.requestsPerMin <= 0 {
		return true
	}

	now := time.Now()
	windowStart := now.Truncate(time.Minute)
	key := mcpRateKey(c, authCtx)

	s.rateMu.Lock()
	defer s.rateMu.Unlock()

	if s.rateBuckets == nil {
		s.rateBuckets = map[string]mcpRateBucket{}
	}
	for bucketKey, bucket := range s.rateBuckets {
		if !bucket.windowStart.Equal(windowStart) {
			delete(s.rateBuckets, bucketKey)
		}
	}
	bucket := s.rateBuckets[key]
	if bucket.windowStart.IsZero() || !bucket.windowStart.Equal(windowStart) {
		bucket = mcpRateBucket{windowStart: windowStart}
	}
	if bucket.count >= s.requestsPerMin {
		retryAfter := maxInt(1, int(time.Until(windowStart.Add(time.Minute)).Seconds()))
		c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
		s.recordMCPEvent(c, authCtx, "mcp_rate_limited", fmt.Sprintf("requestsPerMinute=%d", s.requestsPerMin))
		writeMCPHTTPError(c, http.StatusTooManyRequests, MCPErrorRateLimited, "mcp rate limit exceeded")
		return false
	}

	bucket.count++
	s.rateBuckets[key] = bucket
	return true
}

func mcpRateKey(c *gin.Context, authCtx authContext) string {
	if apiKeyID := strings.TrimSpace(authCtx.Principal.APIKeyID); apiKeyID != "" {
		return "api-key:" + apiKeyID
	}
	if userID := strings.TrimSpace(authCtx.Principal.UserID); userID != "" {
		return "user:" + userID
	}
	if c != nil && c.Request != nil {
		remote := strings.TrimSpace(c.Request.RemoteAddr)
		if host, _, err := net.SplitHostPort(remote); err == nil {
			remote = host
		}
		if remote != "" {
			return "remote:" + remote
		}
	}
	return "remote:unknown"
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
