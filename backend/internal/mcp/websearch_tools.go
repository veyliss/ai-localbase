package mcp

import (
	"context"
	"fmt"
	"strings"

	"ai-localbase/internal/service"
)

// WebSearcher is the minimal surface websearch_tools.go needs from
// service.YouComService, mirroring how tools.go depends on AppServiceReader
// instead of the concrete *service.AppService. This keeps the mcp package
// testable with a fake searcher and avoids coupling it to the real HTTP client.
type WebSearcher interface {
	Enabled() bool
	Search(ctx context.Context, req service.YouComSearchRequest) (service.YouComSearchResponse, error)
}

// NewWebSearchTools returns the You.com-backed search_web MCP tool. Call it
// alongside NewReadOnlyTools and register the results into the same
// ToolRegistry as DefaultRegistry.
func NewWebSearchTools(youcom WebSearcher) []ToolDefinition {
	return []ToolDefinition{
		{
			Name: "search_web",
			Description: "使用 You.com Search API 执行互联网检索，返回带来源链接和摘录片段的网页与新闻结果。" +
				"参数 query 为必填。需要配置 YDC_API_KEY 环境变量。",
			InputSchema: objectSchema(
				map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "检索关键词或问题",
					},
					"count": map[string]any{
						"type":        "integer",
						"description": "返回结果数量，默认使用服务端配置，最大 20",
					},
					"freshness": map[string]any{
						"type":        "string",
						"description": "结果时效范围，可选 day/week/month/year",
					},
					"language": map[string]any{
						"type":        "string",
						"description": "结果语言，BCP 47 格式，例如 zh-CN",
					},
					"country": map[string]any{
						"type":        "string",
						"description": "结果地区，ISO 3166-1 alpha-2 格式，例如 CN",
					},
				},
				[]string{"query"},
			),
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler: func(ctx context.Context, args map[string]any) (ToolCallResult, error) {
				query, err := requiredStringArg(args, "query")
				if err != nil {
					return ToolCallResult{}, err
				}

				if !youcom.Enabled() {
					return NewTextResult(
						"You.com 网络检索未启用：请设置环境变量 YDC_API_KEY 后重启服务，工具才能返回真实检索结果。",
						map[string]any{"configured": false},
					), nil
				}

				result, err := youcom.Search(ctx, service.YouComSearchRequest{
					Query:     query,
					Count:     optionalIntArg(args, "count"),
					Freshness: optionalStringArg(args, "freshness"),
					Language:  optionalStringArg(args, "language"),
					Country:   optionalStringArg(args, "country"),
				})
				if err != nil {
					return ToolCallResult{}, err
				}

				return ToolCallResult{
					Summary: fmt.Sprintf("You.com 检索完成：返回 %d 条网页结果、%d 条新闻结果。", len(result.Results.Web), len(result.Results.News)),
					Content: []ToolContent{{Type: "text", Text: formatYouComResultText(result)}},
					Data: map[string]any{
						"web":   result.Results.Web,
						"news":  result.Results.News,
						"query": query,
					},
				}, nil
			},
		},
	}
}

func formatYouComResultText(result service.YouComSearchResponse) string {
	lines := make([]string, 0, len(result.Results.Web)+len(result.Results.News)+2)

	if len(result.Results.Web) > 0 {
		lines = append(lines, "网页结果：")
		lines = append(lines, formatYouComResultItems(result.Results.Web)...)
	}
	if len(result.Results.News) > 0 {
		lines = append(lines, "新闻结果：")
		lines = append(lines, formatYouComResultItems(result.Results.News)...)
	}

	if len(lines) == 0 {
		return "未检索到相关内容。"
	}
	return strings.Join(lines, "\n")
}

func formatYouComResultItems(items []service.YouComResult) []string {
	lines := make([]string, 0, len(items))
	for index, item := range items {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = "（无标题）"
		}
		line := fmt.Sprintf("%d. %s - %s", index+1, title, item.URL)
		if description := strings.TrimSpace(item.Description); description != "" {
			line += "\n   " + description
		}
		if len(item.Snippets) > 0 {
			if snippet := strings.TrimSpace(item.Snippets[0]); snippet != "" {
				line += "\n   摘录：" + snippet
			}
		}
		lines = append(lines, line)
	}
	return lines
}
