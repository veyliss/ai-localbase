package mcp

import (
	"context"
	"fmt"
	"sort"
)

type ToolRegistry struct {
	tools map[string]ToolDefinition
}

func NewToolRegistry(definitions ...ToolDefinition) *ToolRegistry {
	registry := &ToolRegistry{tools: make(map[string]ToolDefinition, len(definitions))}
	for _, definition := range definitions {
		registry.Register(definition)
	}
	return registry
}

func (r *ToolRegistry) Register(definition ToolDefinition) {
	if r == nil {
		return
	}
	if definition.Name == "" || definition.Handler == nil {
		return
	}
	r.tools[definition.Name] = definition
}

func (r *ToolRegistry) List() []ToolDefinition {
	if r == nil {
		return nil
	}
	items := make([]ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		items = append(items, tool)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}

func (r *ToolRegistry) Call(ctx context.Context, name string, args map[string]any) (ToolCallResult, error) {
	if r == nil {
		return ToolCallResult{}, fmt.Errorf("tool registry is nil")
	}
	tool, ok := r.tools[name]
	if !ok {
		return ToolCallResult{}, fmt.Errorf("tool not found: %s", name)
	}
	return tool.Handler(ctx, args)
}
