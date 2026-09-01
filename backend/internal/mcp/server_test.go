package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-localbase/internal/model"
	"ai-localbase/internal/service"

	"github.com/gin-gonic/gin"
)

type staticTokenProvider struct {
	config model.AppConfig
}

func noopToolHandler(_ context.Context, _ map[string]any) (ToolCallResult, error) {
	return ToolCallResult{}, nil
}

func (p staticTokenProvider) GetConfig() model.AppConfig {
	return p.config
}

func TestMCPRejectsEmptyCompatibleToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	registry := NewToolRegistry(ToolDefinition{
		Name:            "list_knowledge_bases",
		Description:     "list knowledge bases",
		InputSchema:     emptyObjectSchema(),
		ReadOnly:        true,
		PermissionLevel: ToolPermissionReadOnly,
		Handler:         noopToolHandler,
	})
	server := NewServer(registry, staticTokenProvider{config: model.AppConfig{
		MCP: model.MCPConfig{Token: ""},
	}}, nil, model.ServerConfig{
		EnableAuth:           true,
		EnableMCP:            true,
		EnableMCPLegacyToken: true,
		MCPBasePath:          "/mcp",
	})

	router := gin.New()
	server.RegisterRoutes(router.Group("/mcp"))

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer anything")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d, body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "mcp token is not configured") {
		t.Fatalf("expected empty token error, got %s", resp.Body.String())
	}
}

func newProtocolTestServer() (*Server, *gin.Engine) {
	registry := NewToolRegistry(ToolDefinition{
		Name:            "list_knowledge_bases",
		Description:     "list knowledge bases",
		InputSchema:     emptyObjectSchema(),
		ReadOnly:        true,
		PermissionLevel: ToolPermissionReadOnly,
		Handler:         noopToolHandler,
	})
	server := NewServer(registry, staticTokenProvider{config: model.AppConfig{
		MCP: model.MCPConfig{Token: "test-token"},
	}}, nil, model.ServerConfig{
		EnableAuth:           true,
		EnableMCP:            true,
		EnableMCPLegacyToken: true,
		MCPBasePath:          "/mcp",
	})
	router := gin.New()
	server.RegisterRoutes(router.Group("/mcp"))
	return server, router
}

func performProtocolRequest(router http.Handler, body string, contentType, accept string) *httptest.ResponseRecorder {
	return performProtocolRequestWithHeaders(router, body, contentType, accept, nil)
}

func performProtocolRequestWithHeaders(router http.Handler, body string, contentType, accept string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func TestMCPContractNegotiationDefaultsToV10AndSupportsV11(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, router := newProtocolTestServer()

	defaultResponse := performProtocolRequest(router, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_knowledge_bases","arguments":{}}}`, "application/json", "application/json")
	if defaultResponse.Code != http.StatusOK {
		t.Fatalf("expected default contract call to succeed, got %d body=%s", defaultResponse.Code, defaultResponse.Body.String())
	}
	if got := defaultResponse.Header().Get(mcpResultContractVersionHeader); got != resultContractVersion {
		t.Fatalf("expected default contract %q, got %q", resultContractVersion, got)
	}
	if requestID := defaultResponse.Header().Get("X-Request-Id"); requestID == "" || !isValidMCPRequestID(requestID) {
		t.Fatalf("expected generated request id, got %q", requestID)
	}
	if strings.Contains(defaultResponse.Body.String(), `"meta"`) {
		t.Fatalf("did not expect 1.1 metadata in default result: %s", defaultResponse.Body.String())
	}

	v11Response := performProtocolRequestWithHeaders(router, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_knowledge_bases","arguments":{}}}`, "application/json", "application/json", map[string]string{
		mcpResultContractVersionHeader: "1.1",
		"X-Request-Id":                 "trace-mcp-11",
	})
	if v11Response.Code != http.StatusOK {
		t.Fatalf("expected v1.1 contract call to succeed, got %d body=%s", v11Response.Code, v11Response.Body.String())
	}
	if got := v11Response.Header().Get(mcpResultContractVersionHeader); got != resultContractVersion11 {
		t.Fatalf("expected negotiated contract %q, got %q", resultContractVersion11, got)
	}
	if got := v11Response.Header().Get("X-Request-Id"); got != "trace-mcp-11" {
		t.Fatalf("expected request id to be preserved, got %q", got)
	}
	for _, expected := range []string{`"contractVersion":"1.1"`, `"meta"`, `"requestId":"trace-mcp-11"`} {
		if !strings.Contains(v11Response.Body.String(), expected) {
			t.Fatalf("expected v1.1 response to contain %q, got %s", expected, v11Response.Body.String())
		}
	}
	if got := v11Response.Header().Get("X-MCP-Supported-Contract-Versions"); got != "1.0, 1.1" {
		t.Fatalf("expected supported contract response header, got %q", got)
	}
}

func TestMCPContractNegotiationUsesInitializeParameterAndFallsBackSafely(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, router := newProtocolTestServer()

	response := performProtocolRequest(router, `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"resultContractVersion":"1.1"}}`, "application/json", "application/json")
	if response.Code != http.StatusOK {
		t.Fatalf("expected initialize to succeed, got %d body=%s", response.Code, response.Body.String())
	}
	for _, expected := range []string{`"resultContractVersion":"1.1"`, `"requestedResultContractVersion":"1.1"`, `"fallback":false`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("expected initialize negotiation field %q, got %s", expected, response.Body.String())
		}
	}

	unknown := performProtocolRequestWithHeaders(router, `{"jsonrpc":"2.0","id":4,"method":"ping"}`, "application/json", "application/json", map[string]string{
		mcpResultContractVersionHeader: "9.9",
	})
	if unknown.Code != http.StatusOK || unknown.Header().Get(mcpResultContractVersionHeader) != resultContractVersion {
		t.Fatalf("expected unknown contract to fall back to 1.0, status=%d header=%q body=%s", unknown.Code, unknown.Header().Get(mcpResultContractVersionHeader), unknown.Body.String())
	}
}

func TestMCPV11ErrorsExposeStructuredMetadataAndHTTPDiscoveryHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, router := newProtocolTestServer()

	missingTool := performProtocolRequestWithHeaders(router, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"missing_tool","arguments":{}}}`, "application/json", "application/json", map[string]string{
		mcpResultContractVersionHeader: "1.1",
	})
	if missingTool.Code != http.StatusOK {
		t.Fatalf("expected missing tool to return JSON-RPC error, got %d body=%s", missingTool.Code, missingTool.Body.String())
	}
	for _, expected := range []string{`"contractVersion":"1.1"`, `"error":{"code":"not_found"`, `"meta"`, `"retryable":false`} {
		if !strings.Contains(missingTool.Body.String(), expected) {
			t.Fatalf("expected v1.1 error metadata %q, got %s", expected, missingTool.Body.String())
		}
	}

	infoRequest := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	infoRequest.Header.Set("Authorization", "Bearer test-token")
	infoRequest.Header.Set(mcpResultContractVersionHeader, "1.1")
	infoResponse := httptest.NewRecorder()
	router.ServeHTTP(infoResponse, infoRequest)
	if infoResponse.Code != http.StatusOK || infoResponse.Header().Get(mcpResultContractVersionHeader) != resultContractVersion11 {
		t.Fatalf("expected v1.1 discovery response, status=%d header=%q body=%s", infoResponse.Code, infoResponse.Header().Get(mcpResultContractVersionHeader), infoResponse.Body.String())
	}
	if infoResponse.Header().Get("X-Request-Id") == "" || !strings.Contains(infoResponse.Body.String(), `"supportedResultContractVersions":["1.0","1.1"]`) {
		t.Fatalf("expected discovery request correlation and supported versions, headers=%v body=%s", infoResponse.Header(), infoResponse.Body.String())
	}
}

func TestMCPJSONRPCProtocolBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, router := newProtocolTestServer()

	invalidVersion := performProtocolRequest(router, `{"jsonrpc":"1.0","id":1,"method":"ping"}`, "application/json", "application/json")
	if invalidVersion.Code != http.StatusOK || !strings.Contains(invalidVersion.Body.String(), "invalid json-rpc request") {
		t.Fatalf("expected invalid JSON-RPC envelope response, got status=%d body=%s", invalidVersion.Code, invalidVersion.Body.String())
	}

	ping := performProtocolRequest(router, `{"jsonrpc":"2.0","id":2,"method":"ping"}`, "application/json", "application/json")
	if ping.Code != http.StatusOK || !strings.Contains(ping.Body.String(), `"jsonrpc":"2.0"`) {
		t.Fatalf("expected ping response, got status=%d body=%s", ping.Code, ping.Body.String())
	}

	notification := performProtocolRequest(router, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "application/json", "application/json")
	if notification.Code != http.StatusAccepted || notification.Body.Len() != 0 {
		t.Fatalf("expected empty 202 notification response, got status=%d body=%s", notification.Code, notification.Body.String())
	}

	unsupportedContentType := performProtocolRequest(router, `{"jsonrpc":"2.0","id":3,"method":"ping"}`, "text/plain", "application/json")
	if unsupportedContentType.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected unsupported content type status 415, got %d", unsupportedContentType.Code)
	}

	unsupportedAccept := performProtocolRequest(router, `{"jsonrpc":"2.0","id":4,"method":"ping"}`, "application/json", "text/event-stream")
	if unsupportedAccept.Code != http.StatusNotAcceptable {
		t.Fatalf("expected unacceptable response format status 406, got %d", unsupportedAccept.Code)
	}
}

func TestMCPMetricsEndpointIsProtectedAndReportsContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, router := newProtocolTestServer()

	ping := performProtocolRequest(router, `{"jsonrpc":"2.0","id":9,"method":"ping"}`, "application/json", "application/json")
	if ping.Code != http.StatusOK {
		t.Fatalf("expected ping to succeed, got %d", ping.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp/metrics", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected metrics status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	for _, expected := range []string{`"contractVersion":"1.0"`, `"requestsTotal":1`, `"toolP95Ms"`} {
		if !strings.Contains(resp.Body.String(), expected) {
			t.Fatalf("expected metrics response to contain %q, got %s", expected, resp.Body.String())
		}
	}
}

func TestMCPMetricsCountProtocolAndAuthenticationFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, router := newProtocolTestServer()

	unauthorized := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	unauthorizedResponse := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized status 401, got %d", unauthorizedResponse.Code)
	}

	invalidBody := performProtocolRequest(router, "{", "application/json", "application/json")
	if invalidBody.Code != http.StatusBadRequest {
		t.Fatalf("expected malformed body status 400, got %d", invalidBody.Code)
	}

	metricsRequest := httptest.NewRequest(http.MethodGet, "/mcp/metrics", nil)
	metricsRequest.Header.Set("Authorization", "Bearer test-token")
	metricsResponse := httptest.NewRecorder()
	router.ServeHTTP(metricsResponse, metricsRequest)
	if metricsResponse.Code != http.StatusOK {
		t.Fatalf("expected metrics status 200, got %d body=%s", metricsResponse.Code, metricsResponse.Body.String())
	}
	for _, expected := range []string{`"requestsTotal":2`, `"requestsFailed":2`, `"authFailures":1`} {
		if !strings.Contains(metricsResponse.Body.String(), expected) {
			t.Fatalf("expected metrics response to contain %q, got %s", expected, metricsResponse.Body.String())
		}
	}
}

func TestMCPToolErrorContainsStructuredContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := NewToolRegistry(ToolDefinition{
		Name:            "failing_tool",
		Description:     "test tool",
		InputSchema:     emptyObjectSchema(),
		ReadOnly:        true,
		PermissionLevel: ToolPermissionReadOnly,
		Handler: func(context.Context, map[string]any) (ToolCallResult, error) {
			return ToolCallResult{}, fmt.Errorf("document not found")
		},
	})
	server := NewServer(registry, staticTokenProvider{config: model.AppConfig{MCP: model.MCPConfig{Token: "test-token"}}}, nil, model.ServerConfig{
		EnableAuth:           true,
		EnableMCP:            true,
		EnableMCPLegacyToken: true,
	})
	router := gin.New()
	server.RegisterRoutes(router.Group("/mcp"))

	resp := performProtocolRequest(router, `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"failing_tool","arguments":{}}}`, "application/json", "application/json")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected JSON-RPC tool error status 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	for _, expected := range []string{`"contractVersion":"1.0"`, `"code":"not_found"`, `"retryable":false`} {
		if !strings.Contains(resp.Body.String(), expected) {
			t.Fatalf("expected structured tool error to contain %q, got %s", expected, resp.Body.String())
		}
	}
}

func TestMCPToolDescriptorPublishesDynamicImportScopes(t *testing.T) {
	server := NewServer(NewToolRegistry(ToolDefinition{
		Name:            "start_import_job",
		Description:     "start import",
		InputSchema:     emptyObjectSchema(),
		ReadOnly:        false,
		PermissionLevel: ToolPermissionWrite,
		Handler:         noopToolHandler,
	}), nil, nil, model.ServerConfig{})

	descriptors := server.toolDescriptors()
	if len(descriptors) != 1 {
		t.Fatalf("expected one descriptor, got %d", len(descriptors))
	}
	annotations, ok := descriptors[0]["annotations"].(map[string]any)
	if !ok {
		t.Fatalf("expected descriptor annotations, got %#v", descriptors[0]["annotations"])
	}
	variants, ok := annotations["scopeVariants"].(map[string][]string)
	if !ok {
		t.Fatalf("expected dynamic scope variants, got %#v", annotations["scopeVariants"])
	}
	if variants["reindex"][0] != scopeMCPWrite || variants["eval_dataset"][0] != scopeMCPEval {
		t.Fatalf("unexpected dynamic scopes: %#v", variants)
	}
	contractVersions, ok := descriptors[0]["contractVersions"].([]string)
	if !ok || len(contractVersions) != 2 || contractVersions[1] != resultContractVersion11 {
		t.Fatalf("expected tool contract versions, got %#v", descriptors[0]["contractVersions"])
	}
	retryPolicy, ok := annotations["retryPolicy"].(MCPToolRetryPolicy)
	if !ok || retryPolicy.Mode == "" {
		t.Fatalf("expected tool retry policy, got %#v", annotations["retryPolicy"])
	}
}

func TestMCPRetryToolDescriptorPublishesExplicitRetryPolicy(t *testing.T) {
	server := NewServer(NewToolRegistry(ToolDefinition{
		Name:            "retry_job",
		Description:     "retry job",
		InputSchema:     requiredStringPropertySchema("jobId", "Job ID"),
		PermissionLevel: ToolPermissionWrite,
		Handler:         noopToolHandler,
	}), nil, nil, model.ServerConfig{})

	descriptors := server.toolDescriptors()
	if len(descriptors) != 1 {
		t.Fatalf("expected one descriptor, got %d", len(descriptors))
	}
	annotations, ok := descriptors[0]["annotations"].(map[string]any)
	if !ok {
		t.Fatalf("expected descriptor annotations, got %#v", descriptors[0]["annotations"])
	}
	policy, ok := annotations["retryPolicy"].(MCPToolRetryPolicy)
	if !ok || policy.Mode != "explicit_job_retry" || policy.MaxRetries != 3 || policy.SafeToReplay {
		t.Fatalf("expected explicit non-replay retry policy, got %#v", annotations["retryPolicy"])
	}
	if scopes, ok := annotations["requiredScopes"].([]string); !ok || len(scopes) != 1 || scopes[0] != scopeMCPWrite {
		t.Fatalf("expected write scope on retry tool, got %#v", annotations["requiredScopes"])
	}
}

func TestNormalizeToolCallResultAlwaysHasContractContent(t *testing.T) {
	result := normalizeToolCallResult(ToolCallResult{}, "req-1")
	if result.ContractVersion != resultContractVersion || result.Content == nil || result.Warnings == nil || result.NextActions == nil {
		t.Fatalf("expected complete result contract, got %#v", result)
	}
}

func TestNormalizeToolCallResultMarksExplicitErrorAsError(t *testing.T) {
	result := normalizeToolCallResult(ToolCallResult{
		Error: &ToolCallError{Code: string(MCPErrorNotFound), Message: "document not found"},
	}, "req-2")
	if !result.IsError || result.Error == nil {
		t.Fatalf("expected explicit error to set isError, got %#v", result)
	}
	if result.Error.Code != string(MCPErrorNotFound) || result.Error.RequestID != "req-2" {
		t.Fatalf("expected normalized error metadata, got %#v", result.Error)
	}
}

func TestBuildMCPCapabilitiesPublishesErrorCatalogAndScopeVariants(t *testing.T) {
	capabilities := buildMCPCapabilities(model.AppConfig{}, []ToolDefinition{
		{Name: "list_knowledge_bases", ReadOnly: true, PermissionLevel: ToolPermissionReadOnly},
		{Name: "start_import_job", PermissionLevel: ToolPermissionWrite},
	})

	if capabilities["resultContractVersion"] != resultContractVersion {
		t.Fatalf("expected result contract version, got %#v", capabilities["resultContractVersion"])
	}
	errorCodes, ok := capabilities["errorCodes"].([]MCPErrorDescriptor)
	if !ok || len(errorCodes) != len(mcpErrorDescriptors) {
		t.Fatalf("expected complete error catalog, got %#v", capabilities["errorCodes"])
	}

	tools, ok := capabilities["tools"].([]map[string]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected capability tool summaries, got %#v", capabilities["tools"])
	}
	for _, item := range tools {
		if item["resultContractVersion"] != resultContractVersion {
			t.Fatalf("expected tool contract version, got %#v", item)
		}
		if item["name"] != "start_import_job" {
			continue
		}
		variants, ok := item["scopeVariants"].(map[string][]string)
		if !ok || variants["reindex"][0] != scopeMCPWrite || variants["eval_dataset"][0] != scopeMCPEval {
			t.Fatalf("expected dynamic import scopes, got %#v", item["scopeVariants"])
		}
	}
}

func TestStartImportJobScopesFollowJobType(t *testing.T) {
	definition := ToolDefinition{Name: "start_import_job", PermissionLevel: ToolPermissionWrite}
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{name: "import", expected: scopeMCPUpload},
		{name: "reindex", args: map[string]any{"jobType": "reindex"}, expected: scopeMCPWrite},
		{name: "eval dataset", args: map[string]any{"jobType": "eval_dataset"}, expected: scopeMCPEval},
		{name: "batch index", args: map[string]any{"jobType": "batch_index"}, expected: scopeMCPUpload},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scopes := requiredScopesForToolCall(definition, test.args)
			if len(scopes) != 1 || scopes[0] != test.expected {
				t.Fatalf("expected %q scope, got %v", test.expected, scopes)
			}
		})
	}
}

func TestSanitizeMCPErrorHidesDeploymentDetails(t *testing.T) {
	message := sanitizeMCPError("qdrant request failed for collection kb-secret at /app/data/state.json using https://internal.example/v1 with ailb_sk_secret")
	for _, secret := range []string{"kb-secret", "/app/data/state.json", "https://internal.example/v1", "ailb_sk_secret"} {
		if strings.Contains(message, secret) {
			t.Fatalf("expected %q to be redacted in %q", secret, message)
		}
	}
	if !strings.Contains(message, "vector collection") || !strings.Contains(message, "<redacted-url>") {
		t.Fatalf("expected redaction markers, got %q", message)
	}
}

func TestMCPRateLimitIsolatedPerAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{
		requestsPerMin: 1,
		rateBuckets:    map[string]mcpRateBucket{},
	}
	firstContext, firstRecorder := newRateLimitTestContext("10.0.0.1:1000")
	firstAuth := authContext{Principal: service.AuthPrincipal{APIKeyID: "key-a"}}
	if !server.allowRequest(firstContext, firstAuth) {
		t.Fatal("expected first API key request to be allowed")
	}
	if server.allowRequest(firstContext, firstAuth) {
		t.Fatal("expected the second request for the same API key to be rejected")
	}
	if firstRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limit status 429, got %d", firstRecorder.Code)
	}

	secondContext, secondRecorder := newRateLimitTestContext("10.0.0.2:1000")
	if !server.allowRequest(secondContext, authContext{Principal: service.AuthPrincipal{APIKeyID: "key-b"}}) {
		t.Fatalf("expected another API key to have an independent bucket, status=%d", secondRecorder.Code)
	}
}

func TestMCPToolArgumentLogsOmitStringValues(t *testing.T) {
	summary := summarizeToolArguments(map[string]any{
		"query":         "private question",
		"apiKey":        "secret-api-key",
		"contentBase64": "c2Vuc2l0aXZl",
	})
	for _, forbidden := range []string{"private question", "secret-api-key", "c2Vuc2l0aXZl"} {
		if strings.Contains(summary, forbidden) {
			t.Fatalf("expected tool argument summary to omit %q, got %s", forbidden, summary)
		}
	}
	if !strings.Contains(summary, `"chars"`) {
		t.Fatalf("expected tool argument summary to preserve string lengths, got %s", summary)
	}
}

func TestMCPErrorCatalogAndPerToolMetricsArePublished(t *testing.T) {
	catalog := mcpErrorCatalog()
	if len(catalog) < 10 {
		t.Fatalf("expected a stable MCP error catalog, got %d entries", len(catalog))
	}
	seen := map[MCPErrorCode]bool{}
	for _, descriptor := range catalog {
		if descriptor.Code == "" || seen[descriptor.Code] {
			t.Fatalf("expected unique non-empty error code, got %+v", descriptor)
		}
		seen[descriptor.Code] = true
	}
	for _, required := range []MCPErrorCode{MCPErrorInvalidArgument, MCPErrorNotFound, MCPErrorIndexNotReady, MCPErrorTimeout, MCPErrorRateLimited} {
		if !seen[required] {
			t.Fatalf("expected catalog to contain %q", required)
		}
	}

	server := &Server{metrics: mcpMetricsState{
		startedAt:   time.Now().UTC(),
		toolMetrics: map[string]mcpToolMetricState{},
	}}
	server.recordMCPToolMetric("search_document", true, 10*time.Millisecond)
	server.recordMCPToolMetric("search_document", false, 30*time.Millisecond)
	snapshot := server.metricsSnapshot()
	if len(snapshot.ToolMetrics) != 1 {
		t.Fatalf("expected one per-tool metric, got %+v", snapshot.ToolMetrics)
	}
	metric := snapshot.ToolMetrics[0]
	if metric.ToolName != "search_document" || metric.CallsTotal != 2 || metric.CallsSucceeded != 1 || metric.CallsFailed != 1 {
		t.Fatalf("unexpected per-tool metric: %+v", metric)
	}
	if metric.P95Ms < metric.P50Ms || metric.MaxMs != 30 {
		t.Fatalf("expected ordered per-tool latency metrics, got %+v", metric)
	}
}

func TestNormalizeToolCallResultUsesStableErrorCodeAndRequestID(t *testing.T) {
	result := normalizeToolCallResult(ToolCallResult{
		IsError: true,
		Error: &ToolCallError{
			Code:      "unknown-code",
			Message:   "内部失败",
			Retryable: true,
		},
	}, "req-42")
	if result.Error == nil || result.Error.Code != string(MCPErrorInternal) || result.Error.RequestID != "req-42" || result.Error.Retryable {
		t.Fatalf("expected normalized stable error contract, got %+v", result.Error)
	}
}

func newRateLimitTestContext(remoteAddr string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/mcp", nil)
	context.Request.RemoteAddr = remoteAddr
	return context, recorder
}
