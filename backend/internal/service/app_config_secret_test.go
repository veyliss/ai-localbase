package service

import (
	"testing"

	"ai-localbase/internal/model"
)

func TestGetPublicConfigRedactsSecrets(t *testing.T) {
	service := NewAppService(nil, NewAppStateStore(""), nil, model.ServerConfig{
		EnableMCP:            true,
		EnableMCPLegacyToken: true,
	})

	cfg := service.GetConfig()
	cfg.Chat.APIKey = "chat-secret"
	cfg.Embedding.APIKey = "embedding-secret"
	if _, err := service.UpdateConfig(model.ConfigUpdateRequest(cfg)); err != nil {
		t.Fatalf("update config with secrets: %v", err)
	}

	public := service.GetPublicConfig()
	if public.Chat.APIKey != "" || public.Embedding.APIKey != "" || public.MCP.Token != "" {
		t.Fatalf("expected public config secrets to be redacted, got %+v", public)
	}
	if !public.Chat.APIKeyConfigured || !public.Embedding.APIKeyConfigured || !public.MCP.TokenConfigured {
		t.Fatalf("expected configured secret flags, got %+v", public)
	}
	if !public.MCP.LegacyTokenEnabled {
		t.Fatalf("expected public config to expose legacy token enabled status")
	}
}

func TestUpdateConfigPreservesConfiguredSecretsWhenPublicConfigIsSaved(t *testing.T) {
	service := NewAppService(nil, NewAppStateStore(""), nil, model.ServerConfig{
		EnableMCP:            true,
		EnableMCPLegacyToken: true,
	})

	cfg := service.GetConfig()
	cfg.Chat.APIKey = "chat-secret"
	cfg.Embedding.APIKey = "embedding-secret"
	if _, err := service.UpdateConfig(model.ConfigUpdateRequest(cfg)); err != nil {
		t.Fatalf("update config with secrets: %v", err)
	}

	public := service.GetPublicConfig()
	public.Chat.Model = "updated-chat-model"
	public.Embedding.Model = "updated-embedding-model"
	if _, err := service.UpdateConfig(model.ConfigUpdateRequest(public)); err != nil {
		t.Fatalf("update config from public config: %v", err)
	}

	internal := service.GetConfig()
	if internal.Chat.APIKey != "chat-secret" {
		t.Fatalf("expected chat secret to be preserved, got %q", internal.Chat.APIKey)
	}
	if internal.Embedding.APIKey != "embedding-secret" {
		t.Fatalf("expected embedding secret to be preserved, got %q", internal.Embedding.APIKey)
	}
	if internal.MCP.Token == "" {
		t.Fatalf("expected mcp token to be preserved")
	}
	if internal.Chat.Model != "updated-chat-model" || internal.Embedding.Model != "updated-embedding-model" {
		t.Fatalf("expected non-secret config fields to update, got %+v", internal)
	}
}

func TestUpdateConfigClearsConfiguredSecretsWhenExplicitlyRequested(t *testing.T) {
	service := NewAppService(nil, NewAppStateStore(""), nil, model.ServerConfig{
		EnableMCP:            true,
		EnableMCPLegacyToken: true,
	})

	cfg := service.GetConfig()
	cfg.Chat.APIKey = "chat-secret"
	cfg.Embedding.APIKey = "embedding-secret"
	if _, err := service.UpdateConfig(model.ConfigUpdateRequest(cfg)); err != nil {
		t.Fatalf("update config with secrets: %v", err)
	}

	public := service.GetPublicConfig()
	public.Chat.ClearAPIKey = true
	public.Embedding.ClearAPIKey = true
	if _, err := service.UpdateConfig(model.ConfigUpdateRequest(public)); err != nil {
		t.Fatalf("clear configured secrets: %v", err)
	}

	internal := service.GetConfig()
	if internal.Chat.APIKey != "" || internal.Chat.APIKeyConfigured {
		t.Fatalf("expected chat secret to be cleared, got %+v", internal.Chat)
	}
	if internal.Embedding.APIKey != "" || internal.Embedding.APIKeyConfigured {
		t.Fatalf("expected embedding secret to be cleared, got %+v", internal.Embedding)
	}
}
