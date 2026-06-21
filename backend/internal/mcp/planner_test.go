package mcp

import (
	"context"
	"testing"

	"ai-localbase/internal/model"
)

func TestToolUsePlannerUsesDocumentSearchForDocumentScope(t *testing.T) {
	registry := NewToolRegistry(
		ToolDefinition{
			Name:            "search_document",
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler:         noopToolHandler,
		},
		ToolDefinition{
			Name:            "search_knowledge_base",
			ReadOnly:        true,
			PermissionLevel: ToolPermissionReadOnly,
			Handler:         noopToolHandler,
		},
	)
	planner := NewToolUsePlanner(registry)

	plans := planner.Plan(model.ChatCompletionRequest{
		DocumentID: "doc-1",
		Messages: []model.ChatMessage{{
			Role:    "user",
			Content: "请介绍这份文档",
		}},
	})
	if len(plans) != 1 {
		t.Fatalf("expected one plan, got %#v", plans)
	}
	if plans[0].ToolName != "search_document" {
		t.Fatalf("expected search_document, got %#v", plans[0])
	}
	if plans[0].Arguments["documentId"] != "doc-1" {
		t.Fatalf("expected documentId argument, got %#v", plans[0].Arguments)
	}
}

func TestBuildToolUseContextPropagatesToolSources(t *testing.T) {
	contextText, sources := BuildToolUseContext([]ToolUseExecution{{
		ToolName:        "search_document",
		PermissionLevel: ToolPermissionReadOnly,
		Content:         []ToolContent{{Type: "text", Text: "命中文本"}},
		Data: map[string]any{
			"sources": []map[string]string{{
				"documentId":   "doc-1",
				"documentName": "demo.md",
			}},
		},
	}})

	if contextText == "" {
		t.Fatal("expected context text")
	}
	if len(sources) != 2 {
		t.Fatalf("expected base tool source plus propagated document source, got %#v", sources)
	}
	if sources[1]["documentId"] != "doc-1" || sources[1]["toolName"] != "search_document" {
		t.Fatalf("expected propagated source metadata, got %#v", sources[1])
	}
}

func noopToolHandler(_ context.Context, _ map[string]any) (ToolCallResult, error) {
	return ToolCallResult{}, nil
}
