package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-localbase/internal/model"

	"github.com/gin-gonic/gin"
)

func TestResourceLimiterEnforcesPerClientConcurrency(t *testing.T) {
	limiter := newResourceLimiter(model.ServerConfig{
		APIRequestsPerMinute:      10,
		APIMaxConcurrentRequests:  2,
		ChatMaxConcurrentRequests: 1,
	})

	if _, acquired := limiter.tryAcquire(resourceClassChat, "user:root", limiter.policies[resourceClassChat]); !acquired {
		t.Fatal("expected first request to acquire the chat slot")
	}
	if retryAfter, acquired := limiter.tryAcquire(resourceClassChat, "user:root", limiter.policies[resourceClassChat]); acquired || retryAfter <= 0 {
		t.Fatalf("expected second concurrent request to be rejected, retryAfter=%s acquired=%t", retryAfter, acquired)
	}
	limiter.release(resourceClassChat, "user:root")
	if _, acquired := limiter.tryAcquire(resourceClassChat, "user:root", limiter.policies[resourceClassChat]); !acquired {
		t.Fatal("expected slot to be released after request completion")
	}
	limiter.release(resourceClassChat, "user:root")
}

func TestResourceLimitMiddlewareReturnsRetryableResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := newResourceLimiter(model.ServerConfig{
		APIRequestsPerMinute:      10,
		APIMaxConcurrentRequests:  1,
		ChatMaxConcurrentRequests: 1,
	})
	engine := gin.New()
	engine.Use(requestIDMiddleware(), resourceLimitMiddleware(limiter))
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	first := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	firstReq.RemoteAddr = "192.0.2.10:1234"
	engine.ServeHTTP(first, firstReq)
	if first.Code != http.StatusNoContent {
		t.Fatalf("expected first request to succeed, got %d", first.Code)
	}

	// A completed request releases the slot, so use the direct limiter to hold it
	// while checking the HTTP response path.
	if _, acquired := limiter.tryAcquire(resourceClassChat, "ip:192.0.2.10", limiter.policies[resourceClassChat]); !acquired {
		t.Fatal("expected test request to hold chat slot")
	}
	defer limiter.release(resourceClassChat, "ip:192.0.2.10")

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	secondReq.RemoteAddr = "192.0.2.10:5678"
	engine.ServeHTTP(second, secondReq)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d body=%s", second.Code, second.Body.String())
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestClassifyResourceRequest(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodPost, "/v1/chat/completions", resourceClassChat},
		{http.MethodPost, "/api/config/test-chat-model", resourceClassModelTest},
		{http.MethodPost, "/api/knowledge-bases/kb/retrieval/debug", resourceClassRetrieval},
		{http.MethodPost, "/api/knowledge-bases/kb/documents/batch-index", resourceClassIndex},
		{http.MethodPost, "/api/eval/datasets/id/runs", resourceClassEval},
		{http.MethodPost, "/api/uploads", resourceClassUpload},
		{http.MethodGet, "/api/knowledge-bases", ""},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			if got := classifyResourceRequest(request); got != test.want {
				t.Fatalf("classifyResourceRequest() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResourceTimeoutsArePositiveForProtectedClasses(t *testing.T) {
	for _, class := range []string{resourceClassChat, resourceClassRetrieval, resourceClassEval, resourceClassIndex, resourceClassUpload, resourceClassModelTest} {
		if timeout := resourceTimeout(class); timeout <= 0 || timeout > 10*time.Minute {
			t.Fatalf("unexpected timeout for %s: %s", class, timeout)
		}
	}
}

func TestRequestIDMiddlewareRejectsUnsafeClientValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(requestIDMiddleware())
	router.GET("/probe", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	tests := []struct {
		name  string
		value string
	}{
		{name: "control character", value: "trace\ncorrelation"},
		{name: "unsupported character", value: "trace/correlation"},
		{name: "too long", value: strings.Repeat("x", 129)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			req.Header.Set("X-Request-Id", test.value)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)
			requestID := resp.Header().Get("X-Request-Id")
			if requestID == test.value || !strings.HasPrefix(requestID, "req-") {
				t.Fatalf("expected generated safe request id, got %q", requestID)
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-Request-Id", "trace-mcp-11")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if got := resp.Header().Get("X-Request-Id"); got != "trace-mcp-11" {
		t.Fatalf("expected safe client request id to be preserved, got %q", got)
	}
}
