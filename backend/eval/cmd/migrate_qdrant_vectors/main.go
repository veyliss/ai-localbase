package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"ai-localbase/internal/config"
	"ai-localbase/internal/service"
)

func main() {
	sourcePrefix := flag.String("source-prefix", "", "source Qdrant collection prefix")
	targetPrefix := flag.String("target-prefix", "", "target Qdrant collection prefix; defaults to QDRANT_COLLECTION_PREFIX")
	knowledgeBaseID := flag.String("kb", "", "knowledge base id; empty migrates all knowledge bases")
	dryRun := flag.Bool("dry-run", false, "scan source payloads without writing vectors")
	batchSize := flag.Int("batch-size", 100, "embedding and upsert batch size")
	maxAttempts := flag.Int("max-attempts", 3, "maximum attempts for collection, embedding and upsert operations")
	retryBackoff := flag.Duration("retry-backoff", 200*time.Millisecond, "initial retry backoff")
	validate := flag.Bool("validate", true, "validate migrated point IDs and text payloads")
	migrationTimeout := flag.Duration("timeout", 30*time.Minute, "overall migration timeout")
	continueOnError := flag.Bool("continue-on-error", false, "continue with other knowledge bases after one migration fails")
	flag.Parse()

	serverConfig := config.LoadServerConfig()
	if strings.TrimSpace(*sourcePrefix) == "" {
		log.Fatal("source-prefix is required")
	}
	if *migrationTimeout <= 0 {
		log.Fatal("timeout must be greater than zero")
	}
	targetCollectionPrefix := strings.TrimSpace(*targetPrefix)
	if targetCollectionPrefix != "" {
		serverConfig.QdrantCollectionPrefix = targetCollectionPrefix
	}
	targetCollectionPrefix = strings.TrimSpace(serverConfig.QdrantCollectionPrefix)
	if strings.TrimSpace(*sourcePrefix) == targetCollectionPrefix {
		log.Fatal("source and target collection prefixes must differ")
	}

	target := service.NewQdrantService(serverConfig)
	sourceConfig := serverConfig
	sourceConfig.QdrantCollectionPrefix = strings.TrimSpace(*sourcePrefix)
	source := service.NewQdrantService(sourceConfig)
	appService := service.NewAppService(target, service.NewAppStateStore(serverConfig.StateFile), nil, serverConfig)
	rag := service.NewRagService()
	embeddingConfig := appService.CurrentEmbeddingConfig()

	ctx, cancel := context.WithTimeout(context.Background(), *migrationTimeout)
	defer cancel()

	requestedKnowledgeBaseID := strings.TrimSpace(*knowledgeBaseID)
	foundKnowledgeBase := false
	total := 0
	failedKnowledgeBases := 0
	errors := make([]map[string]string, 0)
	options := service.DefaultQdrantMigrationOptions()
	options.DryRun = *dryRun
	options.BatchSize = *batchSize
	options.MaxAttempts = *maxAttempts
	options.RetryBackoff = *retryBackoff
	options.Validate = *validate
	for _, knowledgeBase := range appService.ListKnowledgeBases() {
		if requestedKnowledgeBaseID != "" && requestedKnowledgeBaseID != knowledgeBase.ID {
			continue
		}
		foundKnowledgeBase = true
		result, err := service.MigrateQdrantPayloadsWithOptions(ctx, source, target, rag, embeddingConfig, knowledgeBase.ID, options)
		printMigrationResult(result)
		if err != nil {
			failedKnowledgeBases++
			errors = append(errors, map[string]string{
				"knowledgeBaseId": knowledgeBase.ID,
				"error":           err.Error(),
			})
			if !*continueOnError {
				break
			}
			continue
		}
		total += result.MigratedPointCount
	}
	if requestedKnowledgeBaseID != "" && !foundKnowledgeBase {
		failedKnowledgeBases++
		errors = append(errors, map[string]string{
			"knowledgeBaseId": requestedKnowledgeBaseID,
			"error":           "knowledge base was not found",
		})
	}
	status := "complete"
	if failedKnowledgeBases > 0 {
		status = "failed"
	}
	printMigrationResult(map[string]any{
		"status":                status,
		"dryRun":                *dryRun,
		"sourcePrefix":          strings.TrimSpace(*sourcePrefix),
		"targetPrefix":          targetCollectionPrefix,
		"migratedPointCount":    total,
		"knowledgeBaseSelected": foundKnowledgeBase,
		"failedKnowledgeBases":  failedKnowledgeBases,
		"migrationErrorCount":   len(errors),
		"errors":                errors,
	})
	if failedKnowledgeBases > 0 {
		os.Exit(1)
	}
}

func printMigrationResult(value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		log.Printf("encode migration result: %v", err)
		return
	}
	fmt.Println(string(encoded))
}
