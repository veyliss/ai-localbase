package auth

import (
	"net/http"
	"strings"

	"ai-localbase/internal/service"

	"github.com/gin-gonic/gin"
)

const SessionCookieName = "ai_localbase_session"

// Middleware JWT 认证中间件
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing authorization header",
			})
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid authorization format",
			})
			return
		}

		claims, err := ValidateToken(tokenString)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": err.Error(),
			})
			return
		}

		c.Set("username", claims.Username)
		c.Next()
	}
}

func SessionMiddleware(authService *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := sessionToken(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		principal, err := authService.ValidateSessionToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired session"})
			return
		}

		setPrincipal(c, principal)
		c.Set("auth_token", token)
		c.Next()
	}
}

func SessionOrAPIKeyMiddleware(authService *service.AuthService, requiredScopes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		bearer, hasBearer := bearerToken(c)
		cookieToken, hasCookie := cookieSessionToken(c)
		if !hasBearer && !hasCookie {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		var (
			principal service.AuthPrincipal
			err       error
		)
		token := cookieToken
		if hasBearer && strings.HasPrefix(bearer, "ailb_sk_") {
			token = bearer
			principal, err = authService.ValidateAPIKey(bearer)
		} else if hasCookie {
			principal, err = authService.ValidateSessionToken(cookieToken)
		} else {
			token = bearer
			principal, err = authService.ValidateSessionToken(bearer)
			if err != nil {
				principal, err = authService.ValidateAPIKey(bearer)
			}
		}
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		if principal.AuthType == "api_key" && !hasRequiredScopes(principal.Scopes, requiredScopes) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "api key does not have required scope"})
			return
		}

		setPrincipal(c, principal)
		if principal.AuthType == "session" {
			c.Set("auth_token", token)
		}
		c.Next()
	}
}

func hasRequiredScopes(grantedScopes, requiredScopes []string) bool {
	if len(requiredScopes) == 0 {
		return true
	}
	granted := make(map[string]struct{}, len(grantedScopes))
	for _, scope := range grantedScopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope != "" {
			granted[scope] = struct{}{}
		}
	}
	for _, scope := range requiredScopes {
		if _, ok := granted[strings.ToLower(strings.TrimSpace(scope))]; !ok {
			return false
		}
	}
	return true
}

func sessionToken(c *gin.Context) (string, bool) {
	if token, ok := cookieSessionToken(c); ok {
		return token, true
	}
	return bearerToken(c)
}

func cookieSessionToken(c *gin.Context) (string, bool) {
	token, err := c.Cookie(SessionCookieName)
	if err != nil {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func bearerToken(c *gin.Context) (string, bool) {
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if authHeader == "" {
		return "", false
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	return token, token != ""
}

func setPrincipal(c *gin.Context, principal service.AuthPrincipal) {
	c.Set("auth_type", principal.AuthType)
	c.Set("user_id", principal.UserID)
	c.Set("username", principal.Username)
	c.Set("role", principal.Role)
	c.Set("session_id", principal.SessionID)
	c.Set("api_key_id", principal.APIKeyID)
	c.Set("scopes", principal.Scopes)
	if !principal.ExpiresAt.IsZero() {
		c.Set("expires_at", principal.ExpiresAt.Unix())
	}
}
