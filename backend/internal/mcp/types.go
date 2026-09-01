package mcp

import (
	"context"

	"ai-localbase/internal/version"
)

const (
	jsonRPCVersion                 = "2.0"
	protocolVersion                = "2024-11-05"
	resultContractVersion          = "1.0"
	resultContractVersion11        = "1.1"
	serverName                     = "ai-localbase-mcp"
	mcpResultContractVersionHeader = "X-MCP-Result-Contract-Version"
)

// MCPErrorCode is stable across minor server releases. Messages may become
// more specific, but clients should branch on the code and retryable flag.
type MCPErrorCode string

const (
	MCPErrorInvalidArgument       MCPErrorCode = "invalid_argument"
	MCPErrorUnauthenticated       MCPErrorCode = "unauthenticated"
	MCPErrorPermissionDenied      MCPErrorCode = "permission_denied"
	MCPErrorNotFound              MCPErrorCode = "not_found"
	MCPErrorConflict              MCPErrorCode = "conflict"
	MCPErrorIndexNotReady         MCPErrorCode = "index_not_ready"
	MCPErrorDependencyUnavailable MCPErrorCode = "dependency_unavailable"
	MCPErrorTimeout               MCPErrorCode = "timeout"
	MCPErrorRateLimited           MCPErrorCode = "rate_limited"
	MCPErrorConfirmationRequired  MCPErrorCode = "confirmation_required"
	MCPErrorCancelled             MCPErrorCode = "cancelled"
	MCPErrorInternal              MCPErrorCode = "internal_error"
)

type MCPErrorDescriptor struct {
	Code         MCPErrorCode `json:"code"`
	Retryable    bool         `json:"retryable"`
	Description  string       `json:"description"`
	ClientAction string       `json:"clientAction"`
}

var serverVersion = version.Value

type JSONRPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Method  string        `json:"method"`
	Params  JSONRPCParams `json:"params,omitempty"`
}

type JSONRPCParams map[string]any

type JSONRPCResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *JSONRPCError  `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type ToolCallHandler func(ctx context.Context, args map[string]any) (ToolCallResult, error)

type ToolPermissionLevel string

const (
	ToolPermissionReadOnly ToolPermissionLevel = "read-only"
	ToolPermissionWrite    ToolPermissionLevel = "write"
	ToolPermissionDanger   ToolPermissionLevel = "danger"
)

type ToolDefinition struct {
	Name            string
	Description     string
	InputSchema     map[string]any
	ReadOnly        bool
	PermissionLevel ToolPermissionLevel
	Handler         ToolCallHandler
}

type MCPToolRetryPolicy struct {
	Mode            string         `json:"mode"`
	MaxRetries      int            `json:"maxRetries"`
	SafeToReplay    bool           `json:"safeToReplay"`
	RetryableErrors []MCPErrorCode `json:"retryableErrors"`
}

type ToolCallResult struct {
	ContractVersion string         `json:"contractVersion"`
	Summary         string         `json:"summary,omitempty"`
	Content         []ToolContent  `json:"content"`
	Data            map[string]any `json:"data,omitempty"`
	Warnings        []string       `json:"warnings,omitempty"`
	NextActions     []string       `json:"nextActions,omitempty"`
	RequestID       string         `json:"requestId,omitempty"`
	IsError         bool           `json:"isError,omitempty"`
	Error           *ToolCallError `json:"error,omitempty"`
}

type ToolCallError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	RequestID string         `json:"requestId,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

type MCPToolMetrics struct {
	ToolName       string `json:"toolName"`
	CallsTotal     int64  `json:"callsTotal"`
	CallsSucceeded int64  `json:"callsSucceeded"`
	CallsFailed    int64  `json:"callsFailed"`
	P50Ms          int64  `json:"p50Ms"`
	P95Ms          int64  `json:"p95Ms"`
	MaxMs          int64  `json:"maxMs"`
}

type MCPMetricsSnapshot struct {
	ContractVersion           string           `json:"contractVersion"`
	SupportedContractVersions []string         `json:"supportedResultContractVersions"`
	ServerVersion             string           `json:"serverVersion"`
	StartedAt                 string           `json:"startedAt"`
	RequestsTotal             int64            `json:"requestsTotal"`
	RequestsSucceeded         int64            `json:"requestsSucceeded"`
	RequestsFailed            int64            `json:"requestsFailed"`
	ToolCallsTotal            int64            `json:"toolCallsTotal"`
	ToolCallsSucceeded        int64            `json:"toolCallsSucceeded"`
	ToolCallsFailed           int64            `json:"toolCallsFailed"`
	RateLimited               int64            `json:"rateLimited"`
	AuthFailures              int64            `json:"authFailures"`
	ScopeDenied               int64            `json:"scopeDenied"`
	RequestP50Ms              int64            `json:"requestP50Ms"`
	RequestP95Ms              int64            `json:"requestP95Ms"`
	RequestMaxMs              int64            `json:"requestMaxMs"`
	ToolP50Ms                 int64            `json:"toolP50Ms"`
	ToolP95Ms                 int64            `json:"toolP95Ms"`
	ToolMaxMs                 int64            `json:"toolMaxMs"`
	ToolMetrics               []MCPToolMetrics `json:"toolMetrics"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func NewTextResult(text string, data map[string]any) ToolCallResult {
	return ToolCallResult{
		ContractVersion: resultContractVersion,
		Summary:         text,
		Content: []ToolContent{{
			Type: "text",
			Text: text,
		}},
		Data: data,
	}
}
