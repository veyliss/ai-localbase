package service

import (
	"errors"
	"path/filepath"
	"testing"

	"ai-localbase/internal/model"
)

func TestAuthServiceRejectsWeakEnvBootstrapPassword(t *testing.T) {
	config := model.ServerConfig{
		EnableAuth:   true,
		AuthUsername: "root",
		AuthPassword: "123456",
		StateFile:    filepath.Join(t.TempDir(), "app-state.json"),
	}
	appService := NewAppService(nil, NewAppStateStore(config.StateFile), nil, config)

	_, err := NewAuthService(appService, config)
	if err == nil {
		t.Fatal("expected weak AUTH_PASSWORD to be rejected")
	}
	if !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("expected invalid password error, got %v", err)
	}

	appService.state.Mu.RLock()
	defer appService.state.Mu.RUnlock()
	if hasAuthUser(appService.state.Auth) {
		t.Fatal("weak AUTH_PASSWORD must not create a root user")
	}
}
