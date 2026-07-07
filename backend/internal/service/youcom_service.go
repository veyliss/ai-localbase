package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ai-localbase/internal/model"
)

// youComDefaultBaseURL is the You.com Search API endpoint used unless a test
// overrides YouComService.baseURL to point at an httptest server.
const youComDefaultBaseURL = "https://ydc-index.io/v1/search"

// youComMaxCount caps the number of results requested per call, independent
// of what the caller asks for, to keep MCP tool responses bounded.
const youComMaxCount = 20

// YouComService wraps the You.com Search API (GET /v1/search) for the
// search_web MCP tool. It never logs the API key and performs no retries.
type YouComService struct {
	apiKey     string
	baseURL    string
	client     *http.Client
	maxResults int
}

// YouComSearchRequest is the caller-facing search request. Count, Freshness,
// Country and Language are optional; zero values mean "let the service decide".
type YouComSearchRequest struct {
	Query     string
	Count     int
	Freshness string
	Country   string
	Language  string
}

// YouComResult mirrors a single entry under results.web or results.news.
// Every field beyond URL/Title/Description/Snippets is optional and only
// appears when livecrawl is used, so it is decoded defensively.
type YouComResult struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Snippets    []string `json:"snippets,omitempty"`
	PageAge     string   `json:"page_age,omitempty"`
}

// YouComSearchResults mirrors the "results" object. News may be absent from
// the API response entirely, so it decodes to nil rather than erroring.
type YouComSearchResults struct {
	Web  []YouComResult `json:"web,omitempty"`
	News []YouComResult `json:"news,omitempty"`
}

// YouComSearchMetadata mirrors the "metadata" object.
type YouComSearchMetadata struct {
	SearchUUID string `json:"search_uuid,omitempty"`
	Query      string `json:"query,omitempty"`
}

// YouComSearchResponse is the decoded You.com Search API response.
type YouComSearchResponse struct {
	Results  YouComSearchResults  `json:"results"`
	Metadata YouComSearchMetadata `json:"metadata"`
}

// NewYouComService builds a YouComService from ServerConfig. The service is
// always returned (never nil) so callers can check Enabled() rather than
// nil-check the pointer; Search fails cleanly when the API key is empty.
func NewYouComService(cfg model.ServerConfig) *YouComService {
	timeout := time.Duration(cfg.YouComTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	maxResults := cfg.YouComSearchMaxResults
	if maxResults <= 0 {
		maxResults = 5
	}

	return &YouComService{
		apiKey:     strings.TrimSpace(cfg.YouComAPIKey),
		baseURL:    youComDefaultBaseURL,
		maxResults: maxResults,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Enabled reports whether a You.com API key is configured.
func (s *YouComService) Enabled() bool {
	return s != nil && s.apiKey != ""
}

// Search calls GET /v1/search with the given query and options. It never
// retries and never includes the API key anywhere in a returned error.
func (s *YouComService) Search(ctx context.Context, req YouComSearchRequest) (YouComSearchResponse, error) {
	if s == nil {
		return YouComSearchResponse{}, fmt.Errorf("you.com service is not initialized")
	}
	if s.apiKey == "" {
		return YouComSearchResponse{}, fmt.Errorf("you.com search is not configured: set YDC_API_KEY")
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		return YouComSearchResponse{}, fmt.Errorf("query is required")
	}

	count := req.Count
	if count <= 0 {
		count = s.maxResults
	}
	if count > youComMaxCount {
		count = youComMaxCount
	}

	requestURL, err := url.Parse(s.baseURL)
	if err != nil {
		return YouComSearchResponse{}, fmt.Errorf("invalid you.com base url: %w", err)
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("count", strconv.Itoa(count))
	if freshness := strings.TrimSpace(req.Freshness); freshness != "" {
		params.Set("freshness", freshness)
	}
	if country := strings.TrimSpace(req.Country); country != "" {
		params.Set("country", country)
	}
	if language := strings.TrimSpace(req.Language); language != "" {
		params.Set("language", language)
	}
	requestURL.RawQuery = params.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return YouComSearchResponse{}, fmt.Errorf("build you.com request: %w", err)
	}
	httpReq.Header.Set("X-API-Key", s.apiKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return YouComSearchResponse{}, fmt.Errorf("you.com search timed out: %w", err)
		}
		if errors.Is(err, context.Canceled) {
			return YouComSearchResponse{}, fmt.Errorf("you.com search canceled: %w", err)
		}
		return YouComSearchResponse{}, fmt.Errorf("request you.com search: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return YouComSearchResponse{}, fmt.Errorf("read you.com response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return YouComSearchResponse{}, mapYouComStatusError(resp.StatusCode, body)
	}

	var result YouComSearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return YouComSearchResponse{}, fmt.Errorf("decode you.com response: %w", err)
	}

	return result, nil
}

func mapYouComStatusError(statusCode int, body []byte) error {
	detail := truncateForError(strings.TrimSpace(string(body)))
	switch statusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("you.com search unauthorized (401): check that YDC_API_KEY is set and valid")
	case http.StatusForbidden:
		return fmt.Errorf("you.com search forbidden (403): check the request endpoint and account access")
	case http.StatusUnprocessableEntity:
		return fmt.Errorf("you.com search rejected parameters (422): %s", detail)
	case http.StatusTooManyRequests:
		return fmt.Errorf("you.com search rate limited (429): slow down and retry later")
	default:
		if statusCode >= http.StatusInternalServerError {
			return fmt.Errorf("you.com search server error (%d): try again later", statusCode)
		}
		return fmt.Errorf("you.com search failed (%d): %s", statusCode, detail)
	}
}

func truncateForError(s string) string {
	const maxLen = 200
	if len(s) <= maxLen {
		return s
	}
	truncated := s[:maxLen]
	// Trim any bytes left over from a multi-byte rune split at the cut point.
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}
