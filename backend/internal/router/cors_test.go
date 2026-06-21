package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newCORSTestRouter(authEnabled bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(corsMiddleware(authEnabled))
	router.GET("/probe", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	return router
}

func performCORSRequest(router *gin.Engine, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func TestCorsMiddlewareRestrictsOriginsWhenAuthEnabled(t *testing.T) {
	router := newCORSTestRouter(true)

	tests := []struct {
		name      string
		origin    string
		wantAllow string
	}{
		{
			name:      "localhost",
			origin:    "http://localhost:5173",
			wantAllow: "http://localhost:5173",
		},
		{
			name:      "loopback ipv4",
			origin:    "http://127.0.0.1:5173",
			wantAllow: "http://127.0.0.1:5173",
		},
		{
			name:      "loopback ipv6",
			origin:    "http://[::1]:5173",
			wantAllow: "http://[::1]:5173",
		},
		{
			name:   "remote host",
			origin: "https://example.com",
		},
		{
			name:   "non http scheme",
			origin: "chrome-extension://localhost",
		},
		{
			name: "missing origin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := performCORSRequest(router, tt.origin)
			if resp.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", resp.Code)
			}
			if got := resp.Header().Get("Access-Control-Allow-Origin"); got != tt.wantAllow {
				t.Fatalf("expected Access-Control-Allow-Origin %q, got %q", tt.wantAllow, got)
			}
			if tt.wantAllow != "" && resp.Header().Get("Vary") != "Origin" {
				t.Fatalf("expected Vary Origin for allowed auth origin, got %q", resp.Header().Get("Vary"))
			}
		})
	}
}

func TestCorsMiddlewareUsesWildcardWhenAuthDisabled(t *testing.T) {
	router := newCORSTestRouter(false)

	resp := performCORSRequest(router, "https://example.com")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard Access-Control-Allow-Origin, got %q", got)
	}
}
