package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ai-localbase/internal/config"
	"ai-localbase/internal/handler"
	"ai-localbase/internal/mcp"
	"ai-localbase/internal/model"
	"ai-localbase/internal/router"
	"ai-localbase/internal/service"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	serverConfig := config.LoadServerConfig()

	if err := validateAuthConfig(serverConfig); err != nil {
		return fmt.Errorf("invalid authentication configuration: %w", err)
	}

	if err := os.MkdirAll(serverConfig.UploadDir, 0o755); err != nil {
		return fmt.Errorf("failed to create upload directory: %w", err)
	}
	if strings.TrimSpace(serverConfig.StagingDir) != "" {
		if err := os.MkdirAll(serverConfig.StagingDir, 0o755); err != nil {
			return fmt.Errorf("failed to create staging directory: %w", err)
		}
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
		return fmt.Errorf("failed to initialize sqlite chat history store: %w", err)
	}
	defer func() {
		if closeErr := chatHistoryStore.Close(); closeErr != nil {
			log.Printf("failed to close sqlite chat history store: %v", closeErr)
		}
	}()

	mcpJobStore, err := service.NewMCPJobStore(serverConfig.MCPJobStoreFile)
	if err != nil {
		return fmt.Errorf("failed to initialize MCP job store: %w", err)
	}
	defer func() {
		if closeErr := mcpJobStore.Close(); closeErr != nil {
			log.Printf("failed to close MCP job store: %v", closeErr)
		}
	}()

	appService := service.NewAppServiceWithJobStore(qdrantService, stateStore, chatHistoryStore, serverConfig, mcpJobStore)
	stopUploadStagingCleanup := startUploadStagingCleanup(appService)
	defer stopUploadStagingCleanup()
	stopMCPJobMaintenance := startMCPJobMaintenance(appService)
	defer stopMCPJobMaintenance()
	authService, err := service.NewAuthService(appService, serverConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth service: %w", err)
	}
	if serverConfig.EnableAuth {
		bootstrap := authService.Bootstrap()
		if bootstrap.SetupRequired {
			log.Printf("Authentication enabled, setup required for username: %s", bootstrap.Username)
		} else {
			log.Printf("Authentication enabled, username: %s", bootstrap.Username)
		}
	}
	llmService := service.NewLLMService()
	mcpRegistry := mcp.DefaultRegistry(appService)
	appHandler := handler.NewAppHandler(serverConfig, appService, llmService)
	configHandler := handler.NewConfigHandler(appService, qdrantService)
	authHandler := handler.NewAuthHandler(authService, serverConfig.EnableAuth)
	mcpServer := mcp.NewServer(mcpRegistry, appService, authService, serverConfig)
	r := router.NewRouter(appHandler, configHandler, authHandler, authService, serverConfig, mcpServer, frontendFS())

	server := &http.Server{
		Addr:              ":" + serverConfig.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("backend server listening on :%s", serverConfig.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var listenErr error
	select {
	case err := <-serverErr:
		listenErr = err
	case sig := <-signals:
		log.Printf("received %s, shutting down", sig)
	}
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("failed to gracefully shut down HTTP server: %v", err)
	}
	if err := appService.ShutdownJobs(shutdownCtx); err != nil {
		log.Printf("failed to gracefully shut down MCP jobs: %v", err)
	}
	if listenErr != nil {
		return fmt.Errorf("failed to start server: %w", listenErr)
	}
	return nil
}

func validateAuthConfig(serverConfig model.ServerConfig) error {
	if !serverConfig.EnableAuth {
		return nil
	}
	if password := strings.TrimSpace(serverConfig.AuthPassword); password != "" && len([]rune(password)) < 8 {
		return fmt.Errorf("AUTH_PASSWORD must be at least 8 characters when ENABLE_AUTH=true")
	}
	hasResetToken := strings.TrimSpace(serverConfig.AuthResetToken) != ""
	hasResetPassword := strings.TrimSpace(serverConfig.AuthResetPassword) != ""
	if hasResetToken != hasResetPassword {
		return fmt.Errorf("AUTH_RESET_TOKEN and AUTH_RESET_PASSWORD must be set together")
	}
	if password := strings.TrimSpace(serverConfig.AuthResetPassword); password != "" && len([]rune(password)) < 8 {
		return fmt.Errorf("AUTH_RESET_PASSWORD must be at least 8 characters when ENABLE_AUTH=true")
	}
	return nil
}

func startUploadStagingCleanup(appService *service.AppService) func() {
	if appService == nil {
		return func() {}
	}
	if err := appService.CleanupUploadStaging(); err != nil {
		log.Printf("failed to clean upload staging directory at startup: %v", err)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := appService.CleanupUploadStaging(); err != nil {
					log.Printf("failed to clean upload staging directory: %v", err)
				}
			case <-stop:
				return
			}
		}
	}()

	return func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		<-done
	}
}

func startMCPJobMaintenance(appService *service.AppService) func() {
	if appService == nil {
		return func() {}
	}
	if deleted, err := appService.PruneMCPJobs(); err != nil {
		log.Printf("failed to prune MCP jobs at startup: %v", err)
	} else if deleted > 0 {
		log.Printf("pruned %d terminal MCP jobs at startup", deleted)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if deleted, err := appService.PruneMCPJobs(); err != nil {
					log.Printf("failed to prune MCP jobs: %v", err)
				} else if deleted > 0 {
					log.Printf("pruned %d terminal MCP jobs", deleted)
				}
			case <-stop:
				return
			}
		}
	}()

	return func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		<-done
	}
}
