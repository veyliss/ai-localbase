package handler

import (
	"net/http"
	"time"

	"ai-localbase/internal/auth"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	username string
	password string
	enabled  bool
}

func NewAuthHandler(username, password string, enabled bool) *AuthHandler {
	if username == "" {
		username = "admin"
	}
	return &AuthHandler{
		username: username,
		password: password,
		enabled:  enabled,
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

	if req.Password != h.password {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid password"})
		return
	}

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
