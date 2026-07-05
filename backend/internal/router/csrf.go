package router

import (
	"net/http"
	"net/url"
	"strings"

	"ai-localbase/internal/auth"

	"github.com/gin-gonic/gin"
)

const csrfHeaderName = "X-CSRF-Token"

func csrfMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !requiresCSRFProtection(c.Request.Method) {
			c.Next()
			return
		}
		if isSessionExemptPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		if !requestUsesSessionCookie(c) {
			c.Next()
			return
		}
		if !sameOriginRequest(c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cross-site request blocked"})
			return
		}
		if !validCSRFToken(c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "missing or invalid csrf token"})
			return
		}
		c.Next()
	}
}

func requiresCSRFProtection(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isSessionExemptPath(path string) bool {
	switch path {
	case "/api/auth/login", "/api/auth/setup":
		return true
	default:
		return false
	}
}

func requestUsesSessionCookie(c *gin.Context) bool {
	if c == nil {
		return false
	}
	token, err := c.Cookie(auth.SessionCookieName)
	return err == nil && strings.TrimSpace(token) != ""
}

func sameOriginRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	origin := strings.TrimSpace(c.GetHeader("Origin"))
	if origin == "" {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Scheme == "" || originURL.Host == "" {
		return false
	}
	return strings.EqualFold(originURL.Scheme+"://"+originURL.Host, requestOrigin(c))
}

func requestOrigin(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil || strings.EqualFold(firstForwardedValue(c.GetHeader("X-Forwarded-Proto")), "https") {
		scheme = "https"
	}
	host := firstForwardedValue(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(c.Request.Host)
	}
	return scheme + "://" + host
}

func firstForwardedValue(value string) string {
	if index := strings.Index(value, ","); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}

func validCSRFToken(c *gin.Context) bool {
	if c == nil {
		return false
	}
	headerToken := strings.TrimSpace(c.GetHeader(csrfHeaderName))
	cookieToken, err := c.Cookie(auth.CSRFCookieName)
	if err != nil {
		return false
	}
	cookieToken = strings.TrimSpace(cookieToken)
	return headerToken != "" && cookieToken != "" && headerToken == cookieToken
}
