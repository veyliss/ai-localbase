package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Claims struct {
	Username  string `json:"username"`
	ExpiresAt int64  `json:"exp"`
}

var jwtSecret []byte
var configuredUsername string

func InitJWTSecret(secret, username string) {
	username = strings.TrimSpace(username)
	if username == "" {
		username = "root"
	}
	jwtSecret = []byte(strings.TrimSpace(secret))
	configuredUsername = username
}

func GetConfiguredUsername() string {
	return configuredUsername
}

// GenerateToken 生成 JWT token
func GenerateToken(username string, duration time.Duration) (string, error) {
	if len(jwtSecret) == 0 {
		return "", errors.New("jwt secret is not initialized")
	}

	claims := Claims{
		Username:  username,
		ExpiresAt: time.Now().Add(duration).Unix(),
	}

	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}

	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)

	headerEncoded := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsEncoded := base64.RawURLEncoding.EncodeToString(claimsJSON)

	message := headerEncoded + "." + claimsEncoded
	signature := sign(message)

	return message + "." + signature, nil
}

// ValidateToken 验证 JWT token
func ValidateToken(tokenString string) (*Claims, error) {
	if len(jwtSecret) == 0 {
		return nil, errors.New("jwt secret is not initialized")
	}

	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}

	message := parts[0] + "." + parts[1]
	signature := parts[2]

	expectedSignature := sign(message)
	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		return nil, errors.New("invalid signature")
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid claims encoding: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("invalid claims json: %w", err)
	}

	if time.Now().Unix() > claims.ExpiresAt {
		return nil, errors.New("token expired")
	}
	if claims.ExpiresAt <= 0 {
		return nil, errors.New("invalid expiration")
	}
	if configuredUsername != "" && claims.Username != configuredUsername {
		return nil, errors.New("invalid subject")
	}

	return &claims, nil
}

func sign(message string) string {
	h := hmac.New(sha256.New, jwtSecret)
	h.Write([]byte(message))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
