package mcp

import (
	"context"
	"fmt"
	"strings"

	"ai-localbase/internal/model"
)

type ToolUsePlanner struct {
	registry *ToolRegistry
}

type PlannedToolCall struct {
	ToolName        string
	Arguments       map[string]any
	Reason          string
	PermissionLevel ToolPermissionLevel
}

type ToolUseExecution struct {
	ToolName        string              `json:"toolName"`
	Reason          string              `json:"reason"`
	PermissionLevel ToolPermissionLevel `json:"permissionLevel"`
	Arguments       map[string]any      `json:"arguments,omitempty"`
	Data            map[string]any      `json:"data,omitempty"`
	Content         []ToolContent       `json:"content,omitempty"`
	IsError         bool                `json:"isError,omitempty"`
	Error           string              `json:"error,omitempty"`
}

func NewToolUsePlanner(registry *ToolRegistry) *ToolUsePlanner {
	return &ToolUsePlanner{registry: registry}
}

func (p *ToolUsePlanner) Plan(req model.ChatCompletionRequest) []PlannedToolCall {
	if p == nil || p.registry == nil {
		return nil
	}

	question := strings.TrimSpace(latestUserMessageForToolUse(req.Messages))
	if question == "" {
		return nil
	}

	lowerQuestion := strings.ToLower(question)
	plans := make([]PlannedToolCall, 0, 1)

	if req.DocumentID == "" && req.KnowledgeBaseID != "" && shouldUseKnowledgeSearch(lowerQuestion) {
		plans = append(plans, PlannedToolCall{
			ToolName:  "search_knowledge_base",
			Arguments: map[string]any{"knowledgeBaseId": req.KnowledgeBaseID, "query": question},
			Reason:    "用户正在针对知识库提问，优先通过 MCP 检索工具补充上下文。",
		})
	}

	return p.attachPermissionLevels(plans)
}

func (p *ToolUsePlanner) Execute(ctx context.Context, plans []PlannedToolCall) []ToolUseExecution {
	if p == nil || p.registry == nil || len(plans) == 0 {
		return nil
	}

	executions := make([]ToolUseExecution, 0, len(plans))
	for _, plan := range plans {
		result, err := p.registry.Call(ctx, plan.ToolName, plan.Arguments)
		execution := ToolUseExecution{
			ToolName:        plan.ToolName,
			Reason:          plan.Reason,
			PermissionLevel: plan.PermissionLevel,
			Arguments:       plan.Arguments,
		}
		if err != nil {
			execution.IsError = true
			execution.Error = err.Error()
			executions = append(executions, execution)
			continue
		}
		execution.Content = result.Content
		execution.Data = result.Data
		execution.IsError = result.IsError
		executions = append(executions, execution)
	}

	return executions
}

func BuildToolUseContext(executions []ToolUseExecution) (string, []map[string]string) {
	if len(executions) == 0 {
		return "", nil
	}

	sections := make([]string, 0, len(executions))
	sources := make([]map[string]string, 0, len(executions))
	for _, execution := range executions {
		if execution.IsError {
			sections = append(sections, fmt.Sprintf("[工具 %s 调用失败]\n原因：%s", execution.ToolName, execution.Error))
			sources = append(sources, map[string]string{
				"toolName":        execution.ToolName,
				"permissionLevel": string(execution.PermissionLevel),
				"status":          "error",
			})
			continue
		}

		textParts := make([]string, 0, len(execution.Content))
		for _, item := range execution.Content {
			if strings.TrimSpace(item.Text) != "" {
				textParts = append(textParts, item.Text)
			}
		}
		sections = append(sections, fmt.Sprintf("[工具 %s 输出]\n%s", execution.ToolName, strings.Join(textParts, "\n")))
		sources = append(sources, map[string]string{
			"toolName":        execution.ToolName,
			"permissionLevel": string(execution.PermissionLevel),
			"status":          "ok",
		})
	}

	return strings.Join(sections, "\n\n"), sources
}

func (p *ToolUsePlanner) attachPermissionLevels(plans []PlannedToolCall) []PlannedToolCall {
	if len(plans) == 0 {
		return plans
	}

	definitions := p.registry.List()
	byName := make(map[string]ToolDefinition, len(definitions))
	for _, definition := range definitions {
		byName[definition.Name] = definition
	}

	for index := range plans {
		if definition, ok := byName[plans[index].ToolName]; ok {
			plans[index].PermissionLevel = definition.PermissionLevel
		}
	}
	return plans
}

func shouldUseKnowledgeSearch(question string) bool {
	if question == "" {
		return false
	}

	markers := []string{
		"是什么",
		"什么是",
		"介绍",
		"说明",
		"总结",
		"概述",
		"有哪些",
		"列出",
		"区别",
		"如何",
		"为什么",
		"redis",
		"文档",
	}
	for _, marker := range markers {
		if strings.Contains(question, marker) {
			return true
		}
	}
	return false
}

func latestUserMessageForToolUse(messages []model.ChatMessage) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.EqualFold(strings.TrimSpace(messages[index].Role), "user") {
			return messages[index].Content
		}
	}
	return ""
}
