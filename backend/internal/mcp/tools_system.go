package mcp

import (
	"context"
	"fmt"
)

// newSystemTools contains server introspection tools. Keeping these definitions
// separate gives future MCP tool groups a stable registration boundary while
// preserving the existing public NewReadOnlyTools catalog.
func newSystemTools(appService AppServiceReader) []ToolDefinition {
	return []ToolDefinition{
		{
			Name:            "get_mcp_capabilities",
			Description:     "返回 MCP Server 能力摘要，包括版本、协议、工具数量、权限分布、启用状态和基础配置。",
			InputSchema:     emptyObjectSchema(),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				tools := NewReadOnlyTools(appService)
				capabilities := buildMCPCapabilitiesForVersion(appService.GetConfig(), tools, mcpResultContractVersionFromContext(ctx))
				return NewTextResult(
					fmt.Sprintf("MCP Server %s 当前提供 %d 个工具。", serverVersion, capabilities["toolCount"]),
					map[string]any{"capabilities": capabilities},
				), nil
			},
		},
		{
			Name:            "get_config_summary",
			Description:     "返回当前聊天模型与嵌入模型配置摘要。",
			InputSchema:     emptyObjectSchema(),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				_ = ctx
				cfg := appService.GetConfig()
				return NewTextResult(
					fmt.Sprintf("当前 Chat 模型为 %s/%s，Embedding 模型为 %s/%s。", cfg.Chat.Provider, cfg.Chat.Model, cfg.Embedding.Provider, cfg.Embedding.Model),
					map[string]any{"config": buildSafeConfigSummary(cfg)},
				), nil
			},
		},
	}
}
