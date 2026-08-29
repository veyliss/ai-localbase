package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ai-localbase/internal/model"
)

// QdrantMigrationOptions controls a payload migration without coupling the
// migration command to a particular deployment size.
type QdrantMigrationOptions struct {
	// DryRun scans source payloads and returns counts without creating a target
	// collection, calling the embedding service, or writing points.
	DryRun bool `json:"dryRun"`
	// BatchSize controls embedding and upsert batches. Values <= 0 use 100.
	BatchSize int `json:"batchSize"`
	// MaxAttempts includes the first attempt. Values <= 0 use 3, and 1 disables
	// retries while still performing the operation once.
	MaxAttempts int `json:"maxAttempts"`
	// RetryBackoff is doubled after every failed attempt.
	RetryBackoff time.Duration `json:"-"`
	// Validate performs a target scroll after writing and checks every migrated
	// point ID and text payload. It defaults to true for the new API.
	Validate bool `json:"validate"`
}

// DefaultQdrantMigrationOptions returns conservative migration defaults.
func DefaultQdrantMigrationOptions() QdrantMigrationOptions {
	return QdrantMigrationOptions{
		BatchSize:    qdrantMigrationDefaultBatchSize,
		MaxAttempts:  qdrantMigrationDefaultMaxAttempts,
		RetryBackoff: qdrantMigrationDefaultRetryBackoff,
		Validate:     true,
	}
}

const (
	qdrantMigrationDefaultBatchSize    = 100
	qdrantMigrationDefaultMaxAttempts  = 3
	qdrantMigrationDefaultRetryBackoff = 200 * time.Millisecond
)

// QdrantMigrationResult is a stable, non-content-bearing migration report.
// It deliberately contains counts and diagnostic codes instead of source text
// so it can be logged or returned by a future administrative API safely.
type QdrantMigrationResult struct {
	KnowledgeBaseID        string         `json:"knowledgeBaseId"`
	SourceCollection       string         `json:"sourceCollection"`
	TargetCollection       string         `json:"targetCollection"`
	Status                 string         `json:"status"`
	DryRun                 bool           `json:"dryRun"`
	SourcePointCount       int            `json:"sourcePointCount"`
	TextPointCount         int            `json:"textPointCount"`
	SkippedPointCount      int            `json:"skippedPointCount"`
	MigratedPointCount     int            `json:"migratedPointCount"`
	BatchCount             int            `json:"batchCount"`
	ValidatedPointCount    int            `json:"validatedPointCount"`
	ValidationMissingCount int            `json:"validationMissingCount"`
	TextCharacters         int            `json:"textCharacters"`
	StructuredPointCount   int            `json:"structuredPointCount"`
	StructuredRowCount     int            `json:"structuredRowCount"`
	SummaryPointCount      int            `json:"summaryPointCount"`
	IndexVersionCounts     map[string]int `json:"indexVersionCounts,omitempty"`
	IssueCounts            map[string]int `json:"issueCounts,omitempty"`
}

func (r *QdrantMigrationResult) addIssue(code string) {
	if r == nil || strings.TrimSpace(code) == "" {
		return
	}
	if r.IssueCounts == nil {
		r.IssueCounts = make(map[string]int)
	}
	r.IssueCounts[code]++
}

func normalizeQdrantMigrationOptions(options QdrantMigrationOptions) QdrantMigrationOptions {
	if options.BatchSize <= 0 {
		options.BatchSize = qdrantMigrationDefaultBatchSize
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = qdrantMigrationDefaultMaxAttempts
	}
	if options.RetryBackoff <= 0 {
		options.RetryBackoff = qdrantMigrationDefaultRetryBackoff
	}
	return options
}

// MigrateQdrantPayloads re-embeds source payloads using the legacy return
// shape. New callers should use MigrateQdrantPayloadsWithOptions so they get
// dry-run and post-write validation results.
func MigrateQdrantPayloads(
	ctx context.Context,
	source *QdrantService,
	target *QdrantService,
	rag *RagService,
	embeddingConfig model.EmbeddingModelConfig,
	knowledgeBaseID string,
) (int, error) {
	options := DefaultQdrantMigrationOptions()
	// Preserve the original helper's behavior: it validates transport and
	// payload conversion but does not make an extra target scroll request.
	options.Validate = false
	result, err := MigrateQdrantPayloadsWithOptions(ctx, source, target, rag, embeddingConfig, knowledgeBaseID, options)
	return result.MigratedPointCount, err
}

// MigrateQdrantPayloadsWithOptions scans, re-embeds and writes a knowledge
// base's Qdrant payloads in bounded batches. Reusing the source point IDs makes
// reruns idempotent at the point level because Qdrant upsert replaces a point
// instead of creating a duplicate.
func MigrateQdrantPayloadsWithOptions(
	ctx context.Context,
	source *QdrantService,
	target *QdrantService,
	rag *RagService,
	embeddingConfig model.EmbeddingModelConfig,
	knowledgeBaseID string,
	options QdrantMigrationOptions,
) (QdrantMigrationResult, error) {
	options = normalizeQdrantMigrationOptions(options)
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	result := QdrantMigrationResult{
		KnowledgeBaseID:    knowledgeBaseID,
		DryRun:             options.DryRun,
		Status:             "scanning",
		IndexVersionCounts: make(map[string]int),
	}
	if source != nil {
		result.SourceCollection = source.CollectionName(knowledgeBaseID)
	}
	if target != nil {
		result.TargetCollection = target.CollectionName(knowledgeBaseID)
	}

	if source == nil || !source.IsEnabled() {
		return migrationFailure(result, "source qdrant service is not configured")
	}
	if !options.DryRun && (target == nil || !target.IsEnabled()) {
		return migrationFailure(result, "target qdrant service is not configured")
	}
	if !options.DryRun && rag == nil {
		return migrationFailure(result, "rag service is not configured")
	}
	if knowledgeBaseID == "" {
		return migrationFailure(result, "knowledge base id is required")
	}

	storedPoints, err := source.ScrollPointPayloads(ctx, knowledgeBaseID)
	if err != nil {
		return migrationFailure(result, fmt.Sprintf("read source qdrant payloads: %v", err))
	}
	result.SourcePointCount = len(storedPoints)

	pointsWithText := make([]QdrantStoredPoint, 0, len(storedPoints))
	texts := make([]string, 0, len(storedPoints))
	for _, point := range storedPoints {
		text := strings.TrimSpace(payloadString(point.Payload, "text", ""))
		if text == "" {
			result.SkippedPointCount++
			result.addIssue("missing_text_payload")
			continue
		}

		result.TextPointCount++
		result.TextCharacters += len([]rune(text))
		kind := strings.ToLower(strings.TrimSpace(payloadString(point.Payload, "chunk_kind", "text")))
		if strings.HasPrefix(kind, "structured_") {
			result.StructuredPointCount++
		}
		switch kind {
		case "structured_row":
			result.StructuredRowCount++
		case "structured_summary":
			result.SummaryPointCount++
		}
		if version := migrationIndexVersion(point.Payload); version != "" {
			result.IndexVersionCounts[version]++
		}
		pointsWithText = append(pointsWithText, point)
		texts = append(texts, text)
	}

	if options.DryRun {
		result.Status = "dry_run"
		return result, nil
	}
	if len(texts) == 0 {
		return migrationFailure(result, "source qdrant collection contains no text payloads")
	}

	if err := retryWithBackoff(ctx, options.MaxAttempts, options.RetryBackoff, func() error {
		return target.EnsureCollection(ctx, knowledgeBaseID)
	}); err != nil {
		result.addIssue("ensure_target_collection_failed")
		return migrationFailure(result, fmt.Sprintf("ensure target qdrant collection: %v", err))
	}

	for start := 0; start < len(pointsWithText); start += options.BatchSize {
		end := start + options.BatchSize
		if end > len(pointsWithText) {
			end = len(pointsWithText)
		}
		batchPoints := pointsWithText[start:end]
		batchTexts := texts[start:end]

		var vectors [][]float64
		err := retryWithBackoff(ctx, options.MaxAttempts, options.RetryBackoff, func() error {
			var embedErr error
			vectors, embedErr = rag.EmbedTexts(ctx, embeddingConfig, batchTexts, target.vectorSize)
			if embedErr != nil {
				return embedErr
			}
			if len(vectors) != len(batchPoints) {
				return fmt.Errorf("embedding response count mismatch: expected %d, got %d", len(batchPoints), len(vectors))
			}
			return nil
		})
		if err != nil {
			result.addIssue("embedding_failed")
			return migrationFailure(result, fmt.Sprintf("embed source qdrant payloads batch %d: %v", result.BatchCount+1, err))
		}

		targetPoints := make([]QdrantPoint, 0, len(batchPoints))
		for index, point := range batchPoints {
			targetPoints = append(targetPoints, QdrantPoint{
				ID:      point.ID,
				Vector:  qdrantPointVectors(vectors[index], BuildSparseVector(batchTexts[index])),
				Payload: clonePayload(point.Payload),
			})
		}
		if err := retryWithBackoff(ctx, options.MaxAttempts, options.RetryBackoff, func() error {
			return target.UpsertPoints(ctx, knowledgeBaseID, targetPoints)
		}); err != nil {
			result.addIssue("upsert_failed")
			return migrationFailure(result, fmt.Sprintf("write target qdrant payloads batch %d: %v", result.BatchCount+1, err))
		}
		result.BatchCount++
		result.MigratedPointCount += len(targetPoints)
	}

	if options.Validate {
		if err := validateQdrantMigration(ctx, target, knowledgeBaseID, pointsWithText, texts, &result); err != nil {
			result.addIssue("validation_failed")
			return migrationFailure(result, err.Error())
		}
	}
	result.Status = "succeeded"
	return result, nil
}

func migrationFailure(result QdrantMigrationResult, message string) (QdrantMigrationResult, error) {
	result.Status = "failed"
	return result, fmt.Errorf("%s", message)
}

func migrationIndexVersion(payload map[string]any) string {
	for _, key := range []string{"index_version", "indexVersion"} {
		if value := strings.TrimSpace(payloadString(payload, key, "")); value != "" {
			if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
				return strconv.Itoa(parsed)
			}
			return value
		}
	}
	return ""
}

func validateQdrantMigration(
	ctx context.Context,
	target *QdrantService,
	knowledgeBaseID string,
	sourcePoints []QdrantStoredPoint,
	texts []string,
	result *QdrantMigrationResult,
) error {
	targetPoints, err := target.ScrollPointPayloads(ctx, knowledgeBaseID)
	if err != nil {
		return fmt.Errorf("validate target qdrant payloads: %w", err)
	}
	byID := make(map[string]string, len(targetPoints))
	for _, point := range targetPoints {
		byID[fmt.Sprint(point.ID)] = strings.TrimSpace(payloadString(point.Payload, "text", ""))
	}
	for index, sourcePoint := range sourcePoints {
		id := fmt.Sprint(sourcePoint.ID)
		text, exists := byID[id]
		if !exists || text != texts[index] {
			result.ValidationMissingCount++
			continue
		}
		result.ValidatedPointCount++
	}
	if result.ValidationMissingCount > 0 {
		return fmt.Errorf("target qdrant validation failed: %d of %d points are missing or have different text payloads", result.ValidationMissingCount, len(sourcePoints))
	}
	return nil
}
