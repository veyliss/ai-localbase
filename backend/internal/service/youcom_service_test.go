package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

func newTestYouComService(t *testing.T, serverURL, apiKey string, maxResults int) *YouComService {
	t.Helper()
	svc := NewYouComService(model.ServerConfig{
		YouComAPIKey:           apiKey,
		YouComSearchMaxResults: maxResults,
		YouComTimeoutSeconds:   5,
	})
	svc.baseURL = serverURL
	return svc
}

func TestYouComServiceEnabled(t *testing.T) {
	enabled := NewYouComService(model.ServerConfig{YouComAPIKey: "test-key"})
	if !enabled.Enabled() {
		t.Fatal("expected service with API key to be enabled")
	}

	disabled := NewYouComService(model.ServerConfig{YouComAPIKey: ""})
	if disabled.Enabled() {
		t.Fatal("expected service without API key to be disabled")
	}
}

func TestYouComSearchEmptyAPIKeyReturnsClearError(t *testing.T) {
	svc := NewYouComService(model.ServerConfig{YouComAPIKey: ""})

	_, err := svc.Search(context.Background(), YouComSearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error when API key is not configured")
	}
	if !strings.Contains(err.Error(), "YDC_API_KEY") {
		t.Fatalf("expected error to mention YDC_API_KEY, got %q", err.Error())
	}
}

func TestYouComSearchRequiresQuery(t *testing.T) {
	svc := NewYouComService(model.ServerConfig{YouComAPIKey: "test-key"})

	_, err := svc.Search(context.Background(), YouComSearchRequest{Query: "   "})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestYouComSearchRequestConstruction(t *testing.T) {
	var gotPath string
	var gotHeader string
	var gotQuery url.Values

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Get("X-API-Key")
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(YouComSearchResponse{})
	}))
	defer server.Close()

	svc := newTestYouComService(t, server.URL+"/v1/search", "secret-key", 5)

	_, err := svc.Search(context.Background(), YouComSearchRequest{
		Query:     "golang mcp",
		Count:     7,
		Freshness: "week",
		Country:   "US",
		Language:  "en",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != "/v1/search" {
		t.Fatalf("expected path /v1/search, got %q", gotPath)
	}
	if gotHeader != "secret-key" {
		t.Fatalf("expected X-API-Key header to be forwarded, got %q", gotHeader)
	}
	if gotQuery.Get("query") != "golang mcp" {
		t.Fatalf("expected query param 'golang mcp', got %q", gotQuery.Get("query"))
	}
	if gotQuery.Get("count") != "7" {
		t.Fatalf("expected count param 7, got %q", gotQuery.Get("count"))
	}
	if gotQuery.Get("freshness") != "week" {
		t.Fatalf("expected freshness param week, got %q", gotQuery.Get("freshness"))
	}
	if gotQuery.Get("country") != "US" {
		t.Fatalf("expected country param US, got %q", gotQuery.Get("country"))
	}
	if gotQuery.Get("language") != "en" {
		t.Fatalf("expected language param en, got %q", gotQuery.Get("language"))
	}
}

func TestYouComSearchCountDefaultsAndCaps(t *testing.T) {
	var gotCount string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCount = r.URL.Query().Get("count")
		_ = json.NewEncoder(w).Encode(YouComSearchResponse{})
	}))
	defer server.Close()

	svc := newTestYouComService(t, server.URL, "test-key", 5)

	if _, err := svc.Search(context.Background(), YouComSearchRequest{Query: "test"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCount != "5" {
		t.Fatalf("expected default count 5 from config, got %q", gotCount)
	}

	if _, err := svc.Search(context.Background(), YouComSearchRequest{Query: "test", Count: 500}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCount != "20" {
		t.Fatalf("expected count capped at 20, got %q", gotCount)
	}

	if _, err := svc.Search(context.Background(), YouComSearchRequest{Query: "test", Count: 3}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCount != "3" {
		t.Fatalf("expected explicit count 3 to be forwarded, got %q", gotCount)
	}
}

func TestYouComSearchResponseMappingWebAndNews(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": map[string]any{
				"web": []map[string]any{
					{
						"url":         "https://example.com/a",
						"title":       "Example A",
						"description": "desc a",
						"snippets":    []string{"snippet a1", "snippet a2"},
					},
				},
				"news": []map[string]any{
					{
						"url":         "https://news.example.com/b",
						"title":       "News B",
						"description": "desc b",
						"snippets":    []string{"snippet b1"},
					},
				},
			},
			"metadata": map[string]any{
				"search_uuid": "uuid-1",
				"query":       "test",
			},
		})
	}))
	defer server.Close()

	svc := newTestYouComService(t, server.URL, "test-key", 5)

	resp, err := svc.Search(context.Background(), YouComSearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results.Web) != 1 || resp.Results.Web[0].URL != "https://example.com/a" {
		t.Fatalf("unexpected web results: %#v", resp.Results.Web)
	}
	if len(resp.Results.News) != 1 || resp.Results.News[0].Title != "News B" {
		t.Fatalf("unexpected news results: %#v", resp.Results.News)
	}
	if resp.Metadata.SearchUUID != "uuid-1" {
		t.Fatalf("unexpected metadata: %#v", resp.Metadata)
	}
}

func TestYouComSearchResponseMappingNewsAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": map[string]any{
				"web": []map[string]any{
					{"url": "https://example.com/a", "title": "Example A", "description": "desc a"},
				},
			},
		})
	}))
	defer server.Close()

	svc := newTestYouComService(t, server.URL, "test-key", 5)

	resp, err := svc.Search(context.Background(), YouComSearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Results.News != nil {
		t.Fatalf("expected nil news results when absent, got %#v", resp.Results.News)
	}
	if len(resp.Results.Web) != 1 {
		t.Fatalf("expected one web result, got %#v", resp.Results.Web)
	}
}

func TestYouComSearchResponseMappingOptionalFieldsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": map[string]any{
				"web": []map[string]any{
					{"url": "https://example.com/a", "title": "Example A"},
				},
			},
		})
	}))
	defer server.Close()

	svc := newTestYouComService(t, server.URL, "test-key", 5)

	resp, err := svc.Search(context.Background(), YouComSearchRequest{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results.Web) != 1 {
		t.Fatalf("expected one web result, got %#v", resp.Results.Web)
	}
	if resp.Results.Web[0].Description != "" {
		t.Fatalf("expected empty description, got %q", resp.Results.Web[0].Description)
	}
	if len(resp.Results.Web[0].Snippets) != 0 {
		t.Fatalf("expected empty snippets, got %#v", resp.Results.Web[0].Snippets)
	}
}

func TestYouComSearchErrorPaths(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{"unauthorized", http.StatusUnauthorized, `{"error":"invalid key"}`},
		{"forbidden", http.StatusForbidden, `{"error":"forbidden"}`},
		{"unprocessable", http.StatusUnprocessableEntity, `{"error":"bad params"}`},
		{"rate limited", http.StatusTooManyRequests, `{"error":"rate limited"}`},
		{"server error", http.StatusInternalServerError, `{"error":"boom"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			svc := newTestYouComService(t, server.URL, "test-key", 5)

			_, err := svc.Search(context.Background(), YouComSearchRequest{Query: "test"})
			if err == nil {
				t.Fatalf("expected error for status %d", tt.statusCode)
			}
			if strings.Contains(err.Error(), "test-key") {
				t.Fatalf("error must never echo the API key, got %q", err.Error())
			}
		})
	}
}

func TestYouComSearchMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()

	svc := newTestYouComService(t, server.URL, "test-key", 5)

	_, err := svc.Search(context.Background(), YouComSearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error for malformed JSON response")
	}
}

func TestYouComSearchContextTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(YouComSearchResponse{})
	}))
	defer server.Close()

	svc := newTestYouComService(t, server.URL, "test-key", 5)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := svc.Search(ctx, YouComSearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout-labeled error, got %q", err.Error())
	}
}

// TestYouComSearchIntegration is the ONLY test that spends real You.com API
// credit. It runs a single Search call and is skipped entirely when
// YDC_API_KEY is not exported, so `go test ./...` stays offline by default.
func TestYouComSearchIntegration(t *testing.T) {
	apiKey := os.Getenv("YDC_API_KEY")
	if apiKey == "" {
		t.Skip("YDC_API_KEY not set, skipping live You.com integration test")
	}

	svc := NewYouComService(model.ServerConfig{
		YouComAPIKey:           apiKey,
		YouComSearchMaxResults: 3,
		YouComTimeoutSeconds:   10,
	})

	resp, err := svc.Search(context.Background(), YouComSearchRequest{Query: "ai-localbase github"})
	if err != nil {
		t.Fatalf("live you.com search failed: %v", err)
	}
	if len(resp.Results.Web) == 0 {
		t.Fatal("expected at least one live web result")
	}
	if resp.Results.Web[0].URL == "" || resp.Results.Web[0].Title == "" {
		t.Fatalf("expected non-empty url and title, got %#v", resp.Results.Web[0])
	}
}

func TestYouComSearchContextCanceled(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(YouComSearchResponse{})
	}))
	defer server.Close()

	svc := newTestYouComService(t, server.URL, "test-key", 5)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	_, err := svc.Search(ctx, YouComSearchRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled-labeled error, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Fatalf("cancellation must not be labeled as timeout, got %q", err.Error())
	}
}

func TestTruncateForErrorKeepsRunesIntact(t *testing.T) {
	long := strings.Repeat("错", 100) // 3 bytes per rune, 300 bytes total
	truncated := truncateForError(long)
	if len(truncated) > 200 {
		t.Fatalf("expected truncation to at most 200 bytes, got %d", len(truncated))
	}
	if !strings.HasPrefix(long, truncated) {
		t.Fatal("truncated string must be a prefix of the original")
	}
	for _, r := range truncated {
		if r != '错' {
			t.Fatalf("found mangled rune %q — multi-byte character was split", r)
		}
	}

	short := "short"
	if truncateForError(short) != short {
		t.Fatal("short strings must be returned unchanged")
	}
}
