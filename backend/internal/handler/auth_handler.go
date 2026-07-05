package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-localbase/internal/auth"
	"ai-localbase/internal/service"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	authService *service.AuthService
	enabled     bool
	attemptsMu  sync.Mutex
	attempts    map[string]loginAttemptRecord
}

type loginAttemptRecord struct {
	failures     []time.Time
	blockedUntil time.Time
}

const (
	maxFailedLoginAttempts = 5
	loginAttemptWindow     = 5 * time.Minute
	loginBlockDuration     = 10 * time.Minute
)

func NewAuthHandler(authService *service.AuthService, enabled bool) *AuthHandler {
	return &AuthHandler{
		authService: authService,
		enabled:     enabled,
		attempts:    make(map[string]loginAttemptRecord),
	}
}

type SetupRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password" binding:"required"`
	SetupToken string `json:"setupToken"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	ExpiresAt int64  `json:"expires_at"`
	Username  string `json:"username"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"currentPassword" binding:"required"`
	NewPassword     string `json:"newPassword" binding:"required"`
}

type CreateAPIKeyRequest struct {
	Name   string   `json:"name" binding:"required"`
	Scopes []string `json:"scopes"`
}

func (h *AuthHandler) Bootstrap(c *gin.Context) {
	if h.authService == nil {
		c.JSON(http.StatusOK, service.AuthBootstrap{AuthEnabled: h.enabled, Username: "root"})
		return
	}
	c.JSON(http.StatusOK, h.authService.Bootstrap())
}

func (h *AuthHandler) Setup(c *gin.Context) {
	var req SetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	result, err := h.authService.SetupRoot(req.Username, req.Password, req.SetupToken, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		h.writeAuthServiceError(c, err)
		return
	}

	writeLoginResponse(c, result)
}

func (h *AuthHandler) Login(c *gin.Context) {
	if !h.enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "authentication is disabled"})
		return
	}

	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	clientKey := c.ClientIP() + ":" + normalizedLoginUsername(req.Username)
	if remaining := h.loginBlockRemaining(clientKey, time.Now()); remaining > 0 {
		c.Header("Retry-After", strconv.Itoa(int(remaining.Seconds())))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many failed login attempts, please try later"})
		return
	}

	result, err := h.authService.Login(req.Username, req.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			if remaining := h.recordFailedLogin(clientKey, time.Now()); remaining > 0 {
				c.Header("Retry-After", strconv.Itoa(int(remaining.Seconds())))
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many failed login attempts, please try later"})
				return
			}
		}
		h.writeAuthServiceError(c, err)
		return
	}

	h.clearLoginAttempts(clientKey)
	writeLoginResponse(c, result)
}

func (h *AuthHandler) Status(c *gin.Context) {
	if !h.enabled {
		c.JSON(http.StatusOK, gin.H{
			"authenticated": false,
			"auth_enabled":  false,
		})
		return
	}

	username, _ := c.Get("username")
	userID, _ := c.Get("user_id")
	sessionID, _ := c.Get("session_id")
	authType, _ := c.Get("auth_type")
	expiresAt, _ := c.Get("expires_at")
	c.JSON(http.StatusOK, gin.H{
		"authenticated": true,
		"username":      username,
		"userId":        userID,
		"sessionId":     sessionID,
		"authType":      authType,
		"expires_at":    expiresAt,
	})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	token, _ := c.Get("auth_token")
	if err := h.authService.Logout(stringValue(token)); err != nil {
		h.writeAuthServiceError(c, err)
		return
	}
	clearSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AuthHandler) LogoutAll(c *gin.Context) {
	userID, _ := c.Get("user_id")
	if err := h.authService.LogoutAll(stringValue(userID)); err != nil {
		h.writeAuthServiceError(c, err)
		return
	}
	clearSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	userID, _ := c.Get("user_id")
	if err := h.authService.ChangePassword(stringValue(userID), req.CurrentPassword, req.NewPassword, c.ClientIP(), c.Request.UserAgent()); err != nil {
		h.writeAuthServiceError(c, err)
		return
	}
	clearSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AuthHandler) ListSessions(c *gin.Context) {
	if !h.enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "authentication is disabled"})
		return
	}

	userID, _ := c.Get("user_id")
	sessionID, _ := c.Get("session_id")
	c.JSON(http.StatusOK, gin.H{
		"items": h.authService.ListSessions(stringValue(userID), stringValue(sessionID)),
	})
}

func (h *AuthHandler) ListAPIKeys(c *gin.Context) {
	if !h.enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "authentication is disabled"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": h.authService.ListAPIKeys()})
}

func (h *AuthHandler) CreateAPIKey(c *gin.Context) {
	var req CreateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	username, _ := c.Get("username")
	result, err := h.authService.CreateAPIKey(req.Name, req.Scopes, stringValue(username), c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		h.writeAuthServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (h *AuthHandler) RevokeAPIKey(c *gin.Context) {
	username, _ := c.Get("username")
	if err := h.authService.RevokeAPIKey(c.Param("id"), stringValue(username), c.ClientIP(), c.Request.UserAgent()); err != nil {
		h.writeAuthServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AuthHandler) ListSecurityEvents(c *gin.Context) {
	if !h.enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "authentication is disabled"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	c.JSON(http.StatusOK, gin.H{"items": h.authService.ListSecurityEvents(limit)})
}

func (h *AuthHandler) writeAuthServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAuthDisabled):
		c.JSON(http.StatusForbidden, gin.H{"error": "authentication is disabled"})
	case errors.Is(err, service.ErrSetupRequired):
		c.JSON(http.StatusConflict, gin.H{"error": "authentication setup is required", "setup_required": true})
	case errors.Is(err, service.ErrSetupNotRequired):
		c.JSON(http.StatusConflict, gin.H{"error": "authentication setup is already completed"})
	case errors.Is(err, service.ErrInvalidSetupToken):
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid setup token"})
	case errors.Is(err, service.ErrSetupTokenRequired):
		c.JSON(http.StatusForbidden, gin.H{"error": "setup token required for non-local initialization", "setup_token_required": true})
	case errors.Is(err, service.ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
	case errors.Is(err, service.ErrInvalidAuthToken):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
	case errors.Is(err, service.ErrCurrentPasswordFail):
		c.JSON(http.StatusBadRequest, gin.H{"error": "current password is incorrect"})
	case errors.Is(err, service.ErrInvalidPassword):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrAPIKeyNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func writeLoginResponse(c *gin.Context, result service.AuthLoginResult) {
	setSessionCookie(c, result.Token, result.ExpiresAt)
	ensureCSRFCookie(c, result.ExpiresAt)
	c.JSON(http.StatusOK, LoginResponse{
		ExpiresAt: result.ExpiresAt.Unix(),
		Username:  result.User.Username,
	})
}

func setSessionCookie(c *gin.Context, token string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.SessionCookieName, token, maxAge, "/", "", requestIsHTTPS(c), true)
}

func clearSessionCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.SessionCookieName, "", -1, "/", "", requestIsHTTPS(c), true)
	c.SetCookie(auth.CSRFCookieName, "", -1, "/", "", requestIsHTTPS(c), false)
}

func ensureCSRFCookie(c *gin.Context, expiresAt time.Time) {
	if c == nil {
		return
	}
	if token, err := c.Cookie(auth.CSRFCookieName); err == nil && strings.TrimSpace(token) != "" {
		return
	}

	csrfToken, err := service.GenerateCSRFToken()
	if err != nil {
		return
	}
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CSRFCookieName, csrfToken, maxAge, "/", "", requestIsHTTPS(c), false)
}

func requestIsHTTPS(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	if c.Request.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")), "https")
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func normalizedLoginUsername(username string) string {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return "root"
	}
	return username
}

func (h *AuthHandler) loginBlockRemaining(clientKey string, now time.Time) time.Duration {
	h.attemptsMu.Lock()
	defer h.attemptsMu.Unlock()

	record, ok := h.attempts[clientKey]
	if !ok {
		return 0
	}
	if record.blockedUntil.After(now) {
		return record.blockedUntil.Sub(now)
	}

	record.blockedUntil = time.Time{}
	record.failures = recentLoginFailures(record.failures, now)
	if len(record.failures) == 0 {
		delete(h.attempts, clientKey)
		return 0
	}

	h.attempts[clientKey] = record
	return 0
}

func (h *AuthHandler) recordFailedLogin(clientKey string, now time.Time) time.Duration {
	h.attemptsMu.Lock()
	defer h.attemptsMu.Unlock()

	record := h.attempts[clientKey]
	record.failures = append(recentLoginFailures(record.failures, now), now)
	if len(record.failures) >= maxFailedLoginAttempts {
		record.blockedUntil = now.Add(loginBlockDuration)
		h.attempts[clientKey] = record
		return loginBlockDuration
	}

	h.attempts[clientKey] = record
	return 0
}

func (h *AuthHandler) clearLoginAttempts(clientKey string) {
	h.attemptsMu.Lock()
	defer h.attemptsMu.Unlock()
	delete(h.attempts, clientKey)
}

func recentLoginFailures(failures []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-loginAttemptWindow)
	recent := failures[:0]
	for _, failure := range failures {
		if failure.After(cutoff) {
			recent = append(recent, failure)
		}
	}
	return recent
}
