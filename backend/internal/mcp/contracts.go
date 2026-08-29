package mcp

import "strings"

var mcpErrorDescriptors = []MCPErrorDescriptor{
	{Code: MCPErrorInvalidArgument, Retryable: false, Description: "请求参数或 JSON-RPC 参数无效。", ClientAction: "修正参数后重试。"},
	{Code: MCPErrorUnauthenticated, Retryable: false, Description: "缺少或无法验证 MCP 身份凭据。", ClientAction: "提供有效的 Bearer API key 或兼容令牌。"},
	{Code: MCPErrorPermissionDenied, Retryable: false, Description: "当前身份没有执行该操作所需的权限。", ClientAction: "申请对应 MCP scope，或改用允许的工具。"},
	{Code: MCPErrorNotFound, Retryable: false, Description: "请求的知识库、文档、会话或任务不存在。", ClientAction: "先列出资源并确认 ID。"},
	{Code: MCPErrorConflict, Retryable: false, Description: "请求与资源当前状态冲突。", ClientAction: "刷新资源状态后再决定是否重试。"},
	{Code: MCPErrorIndexNotReady, Retryable: true, Description: "目标文档的索引尚未就绪。", ClientAction: "查询任务状态，索引完成后重试。"},
	{Code: MCPErrorDependencyUnavailable, Retryable: true, Description: "模型、向量库或其他依赖暂时不可用。", ClientAction: "稍后重试并检查依赖服务健康状态。"},
	{Code: MCPErrorTimeout, Retryable: true, Description: "MCP 操作超过服务端超时限制。", ClientAction: "稍后重试，或缩小查询范围。"},
	{Code: MCPErrorRateLimited, Retryable: true, Description: "请求超过当前身份的频率限制。", ClientAction: "遵守 Retry-After 后再重试。"},
	{Code: MCPErrorConfirmationRequired, Retryable: false, Description: "危险操作需要一次性确认 nonce。", ClientAction: "先获取确认 nonce，再重新提交同一操作。"},
	{Code: MCPErrorCancelled, Retryable: true, Description: "操作被请求方取消。", ClientAction: "确认取消原因后再发起新请求。"},
	{Code: MCPErrorInternal, Retryable: false, Description: "服务端未分类的内部错误。", ClientAction: "使用 requestId 排查服务日志。"},
}

func mcpErrorCatalog() []MCPErrorDescriptor {
	items := make([]MCPErrorDescriptor, len(mcpErrorDescriptors))
	copy(items, mcpErrorDescriptors)
	return items
}

func mcpErrorDescriptor(code MCPErrorCode) MCPErrorDescriptor {
	for _, descriptor := range mcpErrorDescriptors {
		if descriptor.Code == code {
			return descriptor
		}
	}
	return MCPErrorDescriptor{
		Code:         MCPErrorInternal,
		Description:  "服务端未分类的内部错误。",
		ClientAction: "使用 requestId 排查服务日志。",
	}
}

func normalizeMCPErrorCode(value string) MCPErrorCode {
	value = strings.TrimSpace(strings.ToLower(value))
	for _, descriptor := range mcpErrorDescriptors {
		if string(descriptor.Code) == value {
			return descriptor.Code
		}
	}
	return MCPErrorInternal
}

func newMCPToolError(code MCPErrorCode, message, requestID string) ToolCallError {
	descriptor := mcpErrorDescriptor(code)
	return ToolCallError{
		Code:      string(descriptor.Code),
		Message:   sanitizeMCPError(message),
		Retryable: descriptor.Retryable,
		RequestID: strings.TrimSpace(requestID),
	}
}
