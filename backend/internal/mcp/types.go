package mcp

import "context"

const (
	jsonRPCVersion  = "2.0"
	protocolVersion = "2024-11-05"
)

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

type ToolCallResult struct {
	Content []ToolContent `json:"content"`
	Data    map[string]any
	IsError bool `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func NewTextResult(text string, data map[string]any) ToolCallResult {
	return ToolCallResult{
		Content: []ToolContent{{
			Type: "text",
			Text: text,
		}},
		Data: data,
	}
}
