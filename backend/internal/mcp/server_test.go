package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-localbase/internal/model"

	"github.com/gin-gonic/gin"
)

type staticTokenProvider struct {
	config model.AppConfig
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
	}}, model.ServerConfig{
		EnableAuth:  true,
		EnableMCP:   true,
		MCPBasePath: "/mcp",
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
