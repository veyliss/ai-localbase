package main

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"

	"ai-localbase/internal/auth"
	"ai-localbase/internal/config"
	"ai-localbase/internal/handler"
	"ai-localbase/internal/mcp"
	"ai-localbase/internal/model"
	"ai-localbase/internal/router"
	"ai-localbase/internal/service"
)

func main() {
	serverConfig := config.LoadServerConfig()

	if err := validateAuthConfig(serverConfig); err != nil {
		log.Fatal(err)
	}

	if serverConfig.EnableAuth {
		auth.InitJWTSecret(serverConfig.JWTSecret, serverConfig.AuthUsername)
		log.Printf("Authentication enabled, username: %s", serverConfig.AuthUsername)
	}

	if err := os.MkdirAll(serverConfig.UploadDir, 0o755); err != nil {
		log.Fatalf("failed to create upload directory: %v", err)
	}

	qdrantService := service.NewQdrantService(serverConfig)
	if qdrantService != nil && qdrantService.IsEnabled() {
		if err := qdrantService.Ping(context.Background()); err != nil {
			log.Printf("qdrant ping failed: %v", err)
		} else {
			log.Printf("qdrant connected: %s", serverConfig.QdrantURL)
		}
	}

	stateStore := service.NewAppStateStore(serverConfig.StateFile)
	chatHistoryStore, err := service.NewSQLiteChatHistoryStore(serverConfig.ChatHistoryFile)
	if err != nil {
		log.Fatalf("failed to initialize sqlite chat history store: %v", err)
	}
	defer func() {
		if closeErr := chatHistoryStore.Close(); closeErr != nil {
			log.Printf("failed to close sqlite chat history store: %v", closeErr)
		}
	}()

	appService := service.NewAppService(qdrantService, stateStore, chatHistoryStore, serverConfig)
	llmService := service.NewLLMService()
	mcpRegistry := mcp.DefaultRegistry(appService)
	toolPlanner := mcp.NewToolUsePlanner(mcpRegistry)
	appHandler := handler.NewAppHandler(serverConfig, appService, llmService, toolPlanner)
	configHandler := handler.NewConfigHandler(appService, qdrantService)
	authHandler := handler.NewAuthHandler(serverConfig.AuthUsername, serverConfig.AuthPassword, serverConfig.EnableAuth)
	mcpServer := mcp.NewServer(mcpRegistry, appService, serverConfig)
	r := router.NewRouter(appHandler, configHandler, authHandler, serverConfig, mcpServer, frontendFS())

	log.Printf("backend server listening on :%s", serverConfig.Port)
	if err := r.Run(":" + serverConfig.Port); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}

func validateAuthConfig(serverConfig model.ServerConfig) error {
	if !serverConfig.EnableAuth {
		return nil
	}
	if serverConfig.AuthPassword == "" {
		return errors.New("AUTH_PASSWORD must be set when ENABLE_AUTH=true")
	}
	jwtSecret := strings.TrimSpace(serverConfig.JWTSecret)
	if jwtSecret == "" {
		return errors.New("JWT_SECRET must be set when ENABLE_AUTH=true")
	}
	if len(jwtSecret) < 32 {
		return errors.New("JWT_SECRET must be at least 32 characters when ENABLE_AUTH=true")
	}
	return nil
}
