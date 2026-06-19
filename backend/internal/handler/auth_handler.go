package handler

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"sync"
	"time"

	"ai-localbase/internal/auth"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	username   string
	password   string
	enabled    bool
	attemptsMu sync.Mutex
	attempts   map[string]loginAttemptRecord
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

func NewAuthHandler(username, password string, enabled bool) *AuthHandler {
	if username == "" {
		username = "root"
	}
	return &AuthHandler{
		username: username,
		password: password,
		enabled:  enabled,
		attempts: make(map[string]loginAttemptRecord),
	}
}

type LoginRequest struct {
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	Username  string `json:"username"`
}

// Login 登录接口
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

	clientKey := c.ClientIP()
	if remaining := h.loginBlockRemaining(clientKey, time.Now()); remaining > 0 {
		c.Header("Retry-After", strconv.Itoa(int(remaining.Seconds())))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many failed login attempts, please try later"})
		return
	}

	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.password)) != 1 {
		if remaining := h.recordFailedLogin(clientKey, time.Now()); remaining > 0 {
			c.Header("Retry-After", strconv.Itoa(int(remaining.Seconds())))
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many failed login attempts, please try later"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid password"})
		return
	}

	h.clearLoginAttempts(clientKey)

	duration := 7 * 24 * time.Hour // 7 天有效期
	token, err := auth.GenerateToken(h.username, duration)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		Token:     token,
		ExpiresAt: time.Now().Add(duration).Unix(),
		Username:  h.username,
	})
}

// Status 验证 token 状态
func (h *AuthHandler) Status(c *gin.Context) {
	username, _ := c.Get("username")
	c.JSON(http.StatusOK, gin.H{
		"authenticated": true,
		"username":      username,
	})
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
