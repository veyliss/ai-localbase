package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"ai-localbase/internal/auth"

	"github.com/gin-gonic/gin"
)

func TestCSRFMiddlewareRejectsSessionWriteWithoutToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(csrfMiddleware())
	router.POST("/api/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sess-token"})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "csrf-token"})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d, body=%s", resp.Code, resp.Body.String())
	}
}

func TestCSRFMiddlewareAllowsSessionWriteWithMatchingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(csrfMiddleware())
	router.POST("/api/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	req.Header.Set(csrfHeaderName, "csrf-token")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sess-token"})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "csrf-token"})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}
}

func TestCSRFMiddlewareAllowsForwardedSameOriginSessionWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(csrfMiddleware())
	router.POST("/api/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	req.Host = "backend:8080"
	req.Header.Set("Origin", "http://localhost:4173")
	req.Header.Set("X-Forwarded-Host", "localhost:4173")
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set(csrfHeaderName, "csrf-token")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sess-token"})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "csrf-token"})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}
}

func TestCSRFMiddlewareRejectsCrossOriginSessionWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(csrfMiddleware())
	router.POST("/api/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/config", nil)
	req.Host = "localhost:4173"
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set(csrfHeaderName, "csrf-token")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sess-token"})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "csrf-token"})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d, body=%s", resp.Code, resp.Body.String())
	}
}

func TestCSRFMiddlewareAllowsAPIKeyWriteWithoutCSRFToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(csrfMiddleware())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer ailb_sk_test")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}
}
