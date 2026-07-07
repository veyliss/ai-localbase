package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"ai-localbase/internal/service"
)

// fakeWebSearcher is a local WebSearcher test double so websearch_tools_test.go
// never has to reach the real You.com HTTP API.
type fakeWebSearcher struct {
	enabled bool
	result  service.YouComSearchResponse
	err     error

	lastRequest service.YouComSearchRequest
}

func (f *fakeWebSearcher) Enabled() bool {
	return f.enabled
}

func (f *fakeWebSearcher) Search(_ context.Context, req service.YouComSearchRequest) (service.YouComSearchResponse, error) {
	f.lastRequest = req
	if f.err != nil {
		return service.YouComSearchResponse{}, f.err
	}
	return f.result, nil
}

func findSearchWebTool(t *testing.T, tools []ToolDefinition) ToolDefinition {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == "search_web" {
			return tool
		}
	}
	t.Fatal("search_web tool not registered")
	return ToolDefinition{}
}

func TestNewWebSearchToolsRegistration(t *testing.T) {
	tools := NewWebSearchTools(&fakeWebSearcher{enabled: true})
	if len(tools) != 1 {
		t.Fatalf("expected exactly one tool, got %d", len(tools))
	}

	tool := findSearchWebTool(t, tools)
	if tool.PermissionLevel != ToolPermissionReadOnly {
		t.Fatalf("expected read-only permission, got %q", tool.PermissionLevel)
	}
	if !tool.ReadOnly {
		t.Fatal("expected ReadOnly to be true")
	}
	if tool.Handler == nil {
		t.Fatal("expected handler to be set")
	}

	required, ok := tool.InputSchema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "query" {
		t.Fatalf("expected required=[query], got %#v", tool.InputSchema["required"])
	}
	properties, ok := tool.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map in schema")
	}
	for _, name := range []string{"query", "count", "freshness", "language", "country"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("expected schema property %q", name)
		}
	}
}

func TestSearchWebMissingQueryReturnsError(t *testing.T) {
	tool := findSearchWebTool(t, NewWebSearchTools(&fakeWebSearcher{enabled: true}))

	_, err := tool.Handler(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error when query argument is missing")
	}
}

func TestSearchWebUnconfiguredReturnsFriendlyResult(t *testing.T) {
	tool := findSearchWebTool(t, NewWebSearchTools(&fakeWebSearcher{enabled: false}))

	result, err := tool.Handler(context.Background(), map[string]any{"query": "test"})
	if err != nil {
		t.Fatalf("unconfigured search_web must not return an error, got %v", err)
	}
	if result.IsError {
		t.Fatal("unconfigured search_web result must have IsError=false")
	}
	if !strings.Contains(result.Summary, "YDC_API_KEY") {
		t.Fatalf("expected friendly message to mention YDC_API_KEY, got %q", result.Summary)
	}
	configured, ok := result.Data["configured"].(bool)
	if !ok || configured {
		t.Fatalf("expected data.configured=false, got %#v", result.Data["configured"])
	}
}

func TestSearchWebSuccessPopulatesSummaryAndData(t *testing.T) {
	fake := &fakeWebSearcher{
		enabled: true,
		result: service.YouComSearchResponse{
			Results: service.YouComSearchResults{
				Web: []service.YouComResult{
					{URL: "https://example.com/a", Title: "Example A", Description: "desc a", Snippets: []string{"snippet a"}},
				},
				News: []service.YouComResult{
					{URL: "https://news.example.com/b", Title: "News B", Description: "desc b"},
				},
			},
		},
	}
	tool := findSearchWebTool(t, NewWebSearchTools(fake))

	result, err := tool.Handler(context.Background(), map[string]any{
		"query":     "golang mcp",
		"count":     3,
		"freshness": "week",
		"language":  "en",
		"country":   "US",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedSummary := fmt.Sprintf("You.com 检索完成：返回 %d 条网页结果、%d 条新闻结果。", 1, 1)
	if result.Summary != expectedSummary {
		t.Fatalf("expected summary %q, got %q", expectedSummary, result.Summary)
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "Example A") {
		t.Fatalf("expected content to mention result title, got %#v", result.Content)
	}
	if _, ok := result.Data["web"]; !ok {
		t.Fatal("expected data.web to be present")
	}
	if _, ok := result.Data["news"]; !ok {
		t.Fatal("expected data.news to be present")
	}
	if result.Data["query"] != "golang mcp" {
		t.Fatalf("expected data.query to echo the query, got %#v", result.Data["query"])
	}

	if fake.lastRequest.Query != "golang mcp" || fake.lastRequest.Count != 3 ||
		fake.lastRequest.Freshness != "week" || fake.lastRequest.Language != "en" || fake.lastRequest.Country != "US" {
		t.Fatalf("expected all optional args forwarded to Search, got %#v", fake.lastRequest)
	}
}

func TestSearchWebErrorPropagates(t *testing.T) {
	fake := &fakeWebSearcher{enabled: true, err: fmt.Errorf("you.com search rate limited (429): slow down and retry later")}
	tool := findSearchWebTool(t, NewWebSearchTools(fake))

	_, err := tool.Handler(context.Background(), map[string]any{"query": "test"})
	if err == nil {
		t.Fatal("expected search error to propagate")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected propagated error to retain status detail, got %q", err.Error())
	}
}
