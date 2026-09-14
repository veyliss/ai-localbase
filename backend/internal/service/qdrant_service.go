package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"ai-localbase/internal/model"
)

type QdrantService struct {
	baseURL          string
	apiKey           string
	collectionPrefix string
	vectorSize       int
	distance         string
	httpClient       *http.Client
}

type QdrantPoint struct {
	ID      any            `json:"id"`
	Vector  any            `json:"vector"`
	Payload map[string]any `json:"payload"`
}

type QdrantSearchResult struct {
	ID      string
	Score   float64
	Payload map[string]any
}

type QdrantStoredPoint struct {
	ID      any
	Payload map[string]any
}

// SearchResult 对外统一的检索返回结构
type SearchResult = QdrantSearchResult

// HybridSearchParams 混合检索参数
type HybridSearchParams struct {
	CollectionName string
	DenseVector    []float32
	SparseVector   SparseVector
	TopK           int
	ScoreThreshold float32
	Filter         interface{}
}

type qdrantCollectionRequest struct {
	Vectors       any                                 `json:"vectors"`
	SparseVectors map[string]qdrantSparseVectorConfig `json:"sparse_vectors,omitempty"`
}

type qdrantVectorConfig struct {
	Size     int    `json:"size"`
	Distance string `json:"distance"`
}

type qdrantSparseVectorConfig struct{}

type qdrantPointUpsertRequest struct {
	Points []QdrantPoint `json:"points"`
}

type qdrantPointDeleteRequest struct {
	Filter map[string]any `json:"filter"`
}

type qdrantSearchRequest struct {
	Vector      any            `json:"vector"`
	Limit       int            `json:"limit"`
	Filter      map[string]any `json:"filter,omitempty"`
	WithPayload bool           `json:"with_payload"`
}

type qdrantQueryRequest struct {
	Query       any            `json:"query"`
	Using       string         `json:"using,omitempty"`
	Limit       int            `json:"limit"`
	Filter      map[string]any `json:"filter,omitempty"`
	WithPayload bool           `json:"with_payload"`
}

type qdrantScrollRequest struct {
	Limit       int            `json:"limit"`
	Offset      any            `json:"offset,omitempty"`
	Filter      map[string]any `json:"filter,omitempty"`
	WithPayload bool           `json:"with_payload"`
	WithVector  bool           `json:"with_vector"`
}

type qdrantScrollResponse struct {
	Result struct {
		Points []struct {
			ID      any            `json:"id"`
			Payload map[string]any `json:"payload"`
		} `json:"points"`
		NextPageOffset any `json:"next_page_offset"`
	} `json:"result"`
}

type qdrantCollectionInfoResponse struct {
	Result struct {
		Status      string `json:"status"`
		PointsCount *int   `json:"points_count"`
		Config      struct {
			Params struct {
				Vectors       json.RawMessage            `json:"vectors"`
				SparseVectors map[string]json.RawMessage `json:"sparse_vectors"`
			} `json:"params"`
		} `json:"config"`
	} `json:"result"`
}

type qdrantCountRequest struct {
	Filter map[string]any `json:"filter,omitempty"`
	Exact  bool           `json:"exact"`
}

type qdrantCountResponse struct {
	Result struct {
		Count int `json:"count"`
	} `json:"result"`
}

// QdrantCollectionHealth contains only the collection metadata needed by the
// knowledge-base health check. It deliberately does not include payloads.
type QdrantCollectionHealth struct {
	Exists                 bool
	Status                 string
	PointCount             int
	PointCountKnown        bool
	DenseVectorConfigured  bool
	DenseVectorSize        int
	SparseVectorConfigured bool
}

type qdrantScoredPoint struct {
	ID      any            `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload"`
}

type qdrantSearchResponse struct {
	Result []qdrantScoredPoint `json:"result"`
}

func (r *qdrantSearchResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.Result) == 0 || string(raw.Result) == "null" {
		r.Result = nil
		return nil
	}

	var points []qdrantScoredPoint
	if err := json.Unmarshal(raw.Result, &points); err == nil {
		r.Result = points
		return nil
	}

	var queryResult struct {
		Points []qdrantScoredPoint `json:"points"`
	}
	if err := json.Unmarshal(raw.Result, &queryResult); err != nil {
		return err
	}
	r.Result = queryResult.Points
	return nil
}

func NewQdrantService(cfg model.ServerConfig) *QdrantService {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.QdrantURL), "/")
	if baseURL == "" {
		return nil
	}

	timeout := time.Duration(cfg.QdrantTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	return &QdrantService{
		baseURL:          baseURL,
		apiKey:           strings.TrimSpace(cfg.QdrantAPIKey),
		collectionPrefix: strings.TrimSpace(cfg.QdrantCollectionPrefix),
		vectorSize:       cfg.QdrantVectorSize,
		distance:         normalizeQdrantDistance(cfg.QdrantDistance),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (s *QdrantService) IsEnabled() bool {
	return s != nil && s.baseURL != ""
}

func (s *QdrantService) CollectionName(knowledgeBaseID string) string {
	if s == nil {
		return ""
	}

	if s.collectionPrefix == "" {
		return knowledgeBaseID
	}

	return s.collectionPrefix + knowledgeBaseID
}

func (s *QdrantService) Ping(ctx context.Context) error {
	if !s.IsEnabled() {
		return nil
	}

	_, err := s.doJSON(ctx, http.MethodGet, "/collections", nil)
	return err
}

// InspectCollection reads lightweight collection metadata without creating the
// collection or loading any point payloads.
func (s *QdrantService) InspectCollection(ctx context.Context, knowledgeBaseID string) (QdrantCollectionHealth, error) {
	if !s.IsEnabled() {
		return QdrantCollectionHealth{}, nil
	}

	responseBody, err := s.doJSON(
		ctx,
		http.MethodGet,
		"/collections/"+url.PathEscape(s.CollectionName(knowledgeBaseID)),
		nil,
	)
	if err != nil {
		if isQdrantNotFound(err) {
			return QdrantCollectionHealth{}, nil
		}
		return QdrantCollectionHealth{}, err
	}

	var response qdrantCollectionInfoResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return QdrantCollectionHealth{}, fmt.Errorf("decode qdrant collection info: %w", err)
	}

	denseSize, denseConfigured := parseQdrantDenseVectorSize(response.Result.Config.Params.Vectors)
	return QdrantCollectionHealth{
		Exists:                 true,
		Status:                 response.Result.Status,
		PointCount:             intValue(response.Result.PointsCount),
		PointCountKnown:        response.Result.PointsCount != nil,
		DenseVectorConfigured:  denseConfigured,
		DenseVectorSize:        denseSize,
		SparseVectorConfigured: len(response.Result.Config.Params.SparseVectors) > 0,
	}, nil
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

// CountPointsByFilter asks Qdrant for an exact point count. Unlike scrolling,
// this transfers no point IDs, vectors, or payloads to the application.
func (s *QdrantService) CountPointsByFilter(ctx context.Context, knowledgeBaseID string, filter map[string]any) (int, error) {
	if !s.IsEnabled() {
		return 0, nil
	}

	responseBody, err := s.doJSON(
		ctx,
		http.MethodPost,
		"/collections/"+url.PathEscape(s.CollectionName(knowledgeBaseID))+"/points/count",
		qdrantCountRequest{Filter: filter, Exact: true},
	)
	if err != nil {
		return 0, err
	}

	var response qdrantCountResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return 0, fmt.Errorf("decode qdrant count response: %w", err)
	}
	return response.Result.Count, nil
}

func parseQdrantDenseVectorSize(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}

	var named map[string]qdrantVectorConfig
	if err := json.Unmarshal(raw, &named); err == nil {
		if config, ok := named[qdrantDenseVectorName]; ok {
			return config.Size, true
		}
		return 0, false
	}

	var unnamed qdrantVectorConfig
	if err := json.Unmarshal(raw, &unnamed); err != nil {
		return 0, false
	}
	return unnamed.Size, unnamed.Size > 0
}

func (s *QdrantService) EnsureCollection(ctx context.Context, knowledgeBaseID string) error {
	if !s.IsEnabled() {
		return nil
	}

	body := qdrantCollectionRequest{
		Vectors: map[string]qdrantVectorConfig{
			qdrantDenseVectorName: {
				Size:     s.vectorSize,
				Distance: s.distance,
			},
		},
		SparseVectors: map[string]qdrantSparseVectorConfig{
			qdrantSparseVectorName: {},
		},
	}

	_, err := s.doJSON(ctx, http.MethodPut, "/collections/"+url.PathEscape(s.CollectionName(knowledgeBaseID)), body)
	if err != nil && isQdrantAlreadyExists(err) {
		return nil
	}
	return err
}

func (s *QdrantService) DeleteCollection(ctx context.Context, knowledgeBaseID string) error {
	if !s.IsEnabled() {
		return nil
	}

	_, err := s.doJSON(ctx, http.MethodDelete, "/collections/"+url.PathEscape(s.CollectionName(knowledgeBaseID)), nil)
	if err != nil && isQdrantNotFound(err) {
		return nil
	}

	return err
}

const qdrantUpsertBatchSize = 100

func (s *QdrantService) UpsertPoints(ctx context.Context, knowledgeBaseID string, points []QdrantPoint) error {
	if !s.IsEnabled() || len(points) == 0 {
		return nil
	}

	collPath := "/collections/" + url.PathEscape(s.CollectionName(knowledgeBaseID)) + "/points"
	for i := 0; i < len(points); i += qdrantUpsertBatchSize {
		end := i + qdrantUpsertBatchSize
		if end > len(points) {
			end = len(points)
		}
		batch := points[i:end]
		if _, err := s.doJSON(ctx, http.MethodPut, collPath, qdrantPointUpsertRequest{Points: batch}); err != nil {
			legacyBatch := legacyDensePoints(batch)
			if len(legacyBatch) == 0 {
				return fmt.Errorf("upsert batch [%d:%d]: %w", i, end, err)
			}
			if _, legacyErr := s.doJSON(ctx, http.MethodPut, collPath, qdrantPointUpsertRequest{Points: legacyBatch}); legacyErr != nil {
				return fmt.Errorf("upsert batch [%d:%d]: %w", i, end, err)
			}
		}
	}
	return nil
}

func (s *QdrantService) ScrollPointPayloads(ctx context.Context, knowledgeBaseID string) ([]QdrantStoredPoint, error) {
	return s.ScrollPointPayloadsByFilter(ctx, knowledgeBaseID, nil)
}

func (s *QdrantService) ScrollPointPayloadsByFilter(ctx context.Context, knowledgeBaseID string, filter map[string]any) ([]QdrantStoredPoint, error) {
	if !s.IsEnabled() {
		return nil, nil
	}

	const (
		pageSize = 100
		maxPages = 10000
	)
	requestPath := "/collections/" + url.PathEscape(s.CollectionName(knowledgeBaseID)) + "/points/scroll"
	points := make([]QdrantStoredPoint, 0)
	var offset any
	for page := 0; page < maxPages; page++ {
		responseBody, err := s.doJSON(ctx, http.MethodPost, requestPath, qdrantScrollRequest{
			Limit:       pageSize,
			Offset:      offset,
			Filter:      filter,
			WithPayload: true,
			WithVector:  false,
		})
		if err != nil {
			return nil, err
		}

		decoder := json.NewDecoder(bytes.NewReader(responseBody))
		decoder.UseNumber()
		var response qdrantScrollResponse
		if err := decoder.Decode(&response); err != nil {
			return nil, fmt.Errorf("decode qdrant scroll response: %w", err)
		}
		for _, point := range response.Result.Points {
			points = append(points, QdrantStoredPoint{
				ID:      point.ID,
				Payload: clonePayload(point.Payload),
			})
		}
		if response.Result.NextPageOffset == nil {
			return points, nil
		}
		offset = response.Result.NextPageOffset
	}

	return nil, fmt.Errorf("qdrant scroll exceeded %d pages", maxPages)
}

func (s *QdrantService) DeletePointsByIDs(ctx context.Context, knowledgeBaseID string, ids []any) error {
	if s == nil || !s.IsEnabled() || len(ids) == 0 {
		return nil
	}
	_, err := s.doJSON(
		ctx,
		http.MethodPost,
		"/collections/"+url.PathEscape(s.CollectionName(knowledgeBaseID))+"/points/delete",
		qdrantPointDeleteRequest{Filter: map[string]any{"has_id": ids}},
	)
	if err != nil && isQdrantNotFound(err) {
		return nil
	}
	return err
}

func (s *QdrantService) DeletePointsByFilter(ctx context.Context, knowledgeBaseID string, filter map[string]any) error {
	if !s.IsEnabled() || len(filter) == 0 {
		return nil
	}

	_, err := s.doJSON(
		ctx,
		http.MethodPost,
		"/collections/"+url.PathEscape(s.CollectionName(knowledgeBaseID))+"/points/delete",
		qdrantPointDeleteRequest{Filter: filter},
	)
	if err != nil && isQdrantNotFound(err) {
		return nil
	}
	return err
}

const (
	qdrantDenseVectorName          = "dense"
	qdrantSparseVectorName         = "sparse"
	qdrantPayloadRetrievalChannels = "_retrieval_channels"
	qdrantPayloadDenseRank         = "_dense_rank"
	qdrantPayloadSparseRank        = "_sparse_rank"
)

func (s *QdrantService) Search(ctx context.Context, knowledgeBaseID string, vector []float64, limit int, filter map[string]any) ([]QdrantSearchResult, error) {
	if !s.IsEnabled() || len(vector) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}

	results, err := s.queryDense(ctx, knowledgeBaseID, vector, limit, filter)
	if err == nil {
		return results, nil
	}
	return s.searchLegacyDense(ctx, knowledgeBaseID, vector, limit, filter)
}

func (s *QdrantService) searchLegacyDense(ctx context.Context, knowledgeBaseID string, vector []float64, limit int, filter map[string]any) ([]QdrantSearchResult, error) {
	body := qdrantSearchRequest{
		Vector:      vector,
		Limit:       limit,
		Filter:      filter,
		WithPayload: true,
	}
	return s.searchWithBody(ctx, knowledgeBaseID, body)
}

func (s *QdrantService) queryDense(ctx context.Context, knowledgeBaseID string, vector []float64, limit int, filter map[string]any) ([]QdrantSearchResult, error) {
	body := qdrantQueryRequest{
		Query:       vector,
		Using:       qdrantDenseVectorName,
		Limit:       limit,
		Filter:      filter,
		WithPayload: true,
	}
	return s.queryWithBody(ctx, knowledgeBaseID, body)
}

func (s *QdrantService) querySparse(ctx context.Context, knowledgeBaseID string, vector SparseVector, limit int, filter map[string]any) ([]QdrantSearchResult, error) {
	if len(vector.Indices) == 0 || len(vector.Values) == 0 {
		return nil, nil
	}
	body := qdrantQueryRequest{
		Query: map[string]any{
			"indices": vector.Indices,
			"values":  vector.Values,
		},
		Using:       qdrantSparseVectorName,
		Limit:       limit,
		Filter:      filter,
		WithPayload: true,
	}
	return s.queryWithBody(ctx, knowledgeBaseID, body)
}

func (s *QdrantService) searchWithBody(ctx context.Context, knowledgeBaseID string, body qdrantSearchRequest) ([]QdrantSearchResult, error) {
	var responseBody []byte
	err := retryWithBackoff(ctx, 3, 200*time.Millisecond, func() error {
		var err error
		responseBody, err = s.doJSON(ctx, http.MethodPost, "/collections/"+url.PathEscape(s.CollectionName(knowledgeBaseID))+"/points/search", body)
		return err
	})
	if err != nil {
		return nil, err
	}

	var response qdrantSearchResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode qdrant search response: %w", err)
	}

	results := make([]QdrantSearchResult, 0, len(response.Result))
	for _, item := range response.Result {
		results = append(results, QdrantSearchResult{
			ID:      fmt.Sprint(item.ID),
			Score:   item.Score,
			Payload: item.Payload,
		})
	}

	return results, nil
}

func (s *QdrantService) queryWithBody(ctx context.Context, knowledgeBaseID string, body qdrantQueryRequest) ([]QdrantSearchResult, error) {
	var responseBody []byte
	err := retryWithBackoff(ctx, 3, 200*time.Millisecond, func() error {
		var err error
		responseBody, err = s.doJSON(ctx, http.MethodPost, "/collections/"+url.PathEscape(s.CollectionName(knowledgeBaseID))+"/points/query", body)
		return err
	})
	if err != nil {
		return nil, err
	}

	var response qdrantSearchResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode qdrant query response: %w", err)
	}

	results := make([]QdrantSearchResult, 0, len(response.Result))
	for _, item := range response.Result {
		results = append(results, QdrantSearchResult{
			ID:      fmt.Sprint(item.ID),
			Score:   item.Score,
			Payload: item.Payload,
		})
	}

	return results, nil
}

// SearchHybrid 执行混合检索（dense + sparse），使用 RRF 融合
// 内部并行执行两路检索，然后用 RRF 合并排名
func (s *QdrantService) SearchHybrid(ctx context.Context, params HybridSearchParams) ([]SearchResult, error) {
	if !s.IsEnabled() || len(params.DenseVector) == 0 {
		return nil, nil
	}

	topK := params.TopK
	if topK <= 0 {
		topK = 5
	}

	var filter map[string]any
	if params.Filter != nil {
		if typed, ok := params.Filter.(map[string]any); ok {
			filter = typed
		}
	}

	denseVector := float32ToFloat64(params.DenseVector)
	searchLimit := maxInt(topK, minInt(topK*2, 64))

	var denseResults []SearchResult
	var sparseResults []SearchResult
	var denseErr error
	var sparseErr error

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		results, err := s.Search(ctx, params.CollectionName, denseVector, searchLimit, filter)
		if err != nil {
			denseErr = err
			return
		}
		denseResults = applyScoreThreshold(results, float64(params.ScoreThreshold))
	}()

	if len(params.SparseVector.Indices) > 0 && len(params.SparseVector.Values) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := s.querySparse(ctx, params.CollectionName, params.SparseVector, searchLimit, filter)
			if err != nil {
				sparseErr = err
				return
			}
			sparseResults = applyScoreThreshold(results, float64(params.ScoreThreshold))
		}()
	}

	wg.Wait()
	if denseErr != nil {
		return nil, denseErr
	}
	if sparseErr != nil {
		return denseResults, nil
	}
	if len(sparseResults) == 0 {
		return denseResults, nil
	}

	return rrfFusion(denseResults, sparseResults, topK), nil
}

// rrfFusion 使用 RRF 融合
func rrfFusion(denseResults []SearchResult, sparseResults []SearchResult, topK int) []SearchResult {
	const k = 60.0
	if topK <= 0 {
		topK = 5
	}

	scores := make(map[string]float64)
	payloads := make(map[string]map[string]any)
	denseRanks := make(map[string]int)
	sparseRanks := make(map[string]int)
	channels := make(map[string]map[string]struct{})
	addResults := func(results []SearchResult, channel string, ranks map[string]int) {
		for idx, item := range results {
			rank := float64(idx + 1)
			scores[item.ID] += 1.0 / (k + rank)
			ranks[item.ID] = idx + 1
			if _, ok := channels[item.ID]; !ok {
				channels[item.ID] = make(map[string]struct{})
			}
			channels[item.ID][channel] = struct{}{}
			if _, ok := payloads[item.ID]; !ok {
				payloads[item.ID] = clonePayload(item.Payload)
			}
		}
	}

	addResults(denseResults, qdrantDenseVectorName, denseRanks)
	addResults(sparseResults, qdrantSparseVectorName, sparseRanks)

	merged := make([]SearchResult, 0, len(scores))
	for id, score := range scores {
		payload := payloads[id]
		payload[qdrantPayloadRetrievalChannels] = sortedChannelList(channels[id])
		payload[qdrantPayloadDenseRank] = denseRanks[id]
		payload[qdrantPayloadSparseRank] = sparseRanks[id]
		merged = append(merged, SearchResult{
			ID:      id,
			Score:   score,
			Payload: payload,
		})
	}

	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Score == merged[j].Score {
			return merged[i].ID < merged[j].ID
		}
		return merged[i].Score > merged[j].Score
	})

	if len(merged) > topK {
		return merged[:topK]
	}
	return merged
}

func clonePayload(payload map[string]any) map[string]any {
	cloned := make(map[string]any, len(payload)+3)
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}

func sortedChannelList(channels map[string]struct{}) []string {
	result := make([]string, 0, len(channels))
	for channel := range channels {
		result = append(result, channel)
	}
	sort.Strings(result)
	return result
}

func applyScoreThreshold(results []SearchResult, threshold float64) []SearchResult {
	if threshold <= 0 {
		return results
	}
	filtered := make([]SearchResult, 0, len(results))
	for _, item := range results {
		if item.Score >= threshold {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func float32ToFloat64(input []float32) []float64 {
	if len(input) == 0 {
		return nil
	}
	output := make([]float64, len(input))
	for i, value := range input {
		output[i] = float64(value)
	}
	return output
}

func qdrantPointVectors(dense []float64, sparse SparseVector) any {
	if len(sparse.Indices) == 0 || len(sparse.Values) == 0 {
		return map[string]any{
			qdrantDenseVectorName: dense,
		}
	}
	return map[string]any{
		qdrantDenseVectorName: dense,
		qdrantSparseVectorName: map[string]any{
			"indices": sparse.Indices,
			"values":  sparse.Values,
		},
	}
}

func legacyDensePoints(points []QdrantPoint) []QdrantPoint {
	legacy := make([]QdrantPoint, 0, len(points))
	for _, point := range points {
		dense := extractDenseVector(point.Vector)
		if len(dense) == 0 {
			continue
		}
		next := point
		next.Vector = dense
		legacy = append(legacy, next)
	}
	return legacy
}

func extractDenseVector(vector any) []float64 {
	switch typed := vector.(type) {
	case []float64:
		return typed
	case map[string]any:
		if dense, ok := typed[qdrantDenseVectorName].([]float64); ok {
			return dense
		}
	}
	return nil
}

func (s *QdrantService) doJSON(ctx context.Context, method, requestPath string, payload any) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("qdrant service is not initialized")
	}

	requestURL, err := url.Parse(s.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid qdrant base url: %w", err)
	}
	requestURL.Path = path.Join(requestURL.Path, requestPath)

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal qdrant payload: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build qdrant request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.apiKey != "" {
		req.Header.Set("api-key", s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request qdrant: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read qdrant response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &qdrantRequestError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(responseBody)),
		}
	}

	return responseBody, nil
}

func normalizeQdrantDistance(distance string) string {
	switch strings.ToLower(strings.TrimSpace(distance)) {
	case "dot":
		return "Dot"
	case "euclid":
		return "Euclid"
	case "manhattan":
		return "Manhattan"
	default:
		return "Cosine"
	}
}

func isQdrantNotFound(err error) bool {
	requestErr, ok := err.(*qdrantRequestError)
	return ok && requestErr.StatusCode == http.StatusNotFound
}

func isQdrantAlreadyExists(err error) bool {
	requestErr, ok := err.(*qdrantRequestError)
	return ok && requestErr.StatusCode == http.StatusConflict
}

type qdrantRequestError struct {
	StatusCode int
	Body       string
}

func (e *qdrantRequestError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("qdrant request failed with status %d", e.StatusCode)
	}

	return fmt.Sprintf("qdrant request failed with status %d: %s", e.StatusCode, e.Body)
}
