package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ai-localbase/internal/auth"
	"ai-localbase/internal/model"
	"ai-localbase/internal/service"

	"github.com/gin-gonic/gin"
)

func newAuthTestRouter(authHandler *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/auth/login", authHandler.Login)
	return router
}

func newAuthSetupTestRouter(authHandler *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/auth/bootstrap", authHandler.Bootstrap)
	router.POST("/api/auth/setup", authHandler.Setup)
	router.POST("/api/auth/login", authHandler.Login)
	return router
}

func newAuthTestRouterWithSession(authHandler *AuthHandler, authService *service.AuthService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/auth/login", authHandler.Login)
	api := router.Group("/api")
	api.Use(auth.SessionMiddleware(authService))
	api.GET("/auth/status", authHandler.Status)
	api.POST("/auth/logout", authHandler.Logout)
	return router
}

func performLoginRequest(t *testing.T, router *gin.Engine, password string) *httptest.ResponseRecorder {
	t.Helper()
	return performLoginRequestForUsername(t, router, "admin", password)
}

func performLoginRequestForUsername(t *testing.T, router *gin.Engine, username, password string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatalf("marshal login request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.10:4567"

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func newAuthHandlerForTest(t *testing.T, username, password string) *AuthHandler {
	t.Helper()

	authHandler, _ := newAuthHandlerAndServiceForTest(t, username, password)
	return authHandler
}

func newAuthHandlerAndServiceForTest(t *testing.T, username, password string) (*AuthHandler, *service.AuthService) {
	t.Helper()

	return newAuthHandlerAndServiceWithConfigForTest(t, model.ServerConfig{
		EnableAuth:   true,
		AuthUsername: username,
		AuthPassword: password,
		EnableMCP:    false,
	})
}

func newAuthHandlerAndServiceWithConfigForTest(t *testing.T, serverConfig model.ServerConfig) (*AuthHandler, *service.AuthService) {
	t.Helper()

	tempDir := t.TempDir()
	if serverConfig.StateFile == "" {
		serverConfig.StateFile = filepath.Join(tempDir, "app-state.json")
	}
	if serverConfig.AuthUsername == "" {
		serverConfig.AuthUsername = "root"
	}
	serverConfig.EnableAuth = true
	chatHistoryStore, err := service.NewSQLiteChatHistoryStore(filepath.Join(tempDir, "chat-history.db"))
	if err != nil {
		t.Fatalf("create chat history store: %v", err)
	}
	t.Cleanup(func() {
		_ = chatHistoryStore.Close()
	})

	appService := service.NewAppService(nil, service.NewAppStateStore(serverConfig.StateFile), chatHistoryStore, serverConfig)
	authService, err := service.NewAuthService(appService, serverConfig)
	if err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	return NewAuthHandler(authService, true), authService
}

func TestAuthBootstrapAndSetupFlow(t *testing.T) {
	authHandler, _ := newAuthHandlerAndServiceWithConfigForTest(t, model.ServerConfig{
		AuthUsername:   "root",
		AuthSetupToken: "setup-token",
		EnableMCP:      false,
	})
	router := newAuthSetupTestRouter(authHandler)

	bootstrapResp := performAuthJSONRequest(t, router, http.MethodGet, "/api/auth/bootstrap", nil)
	if bootstrapResp.Code != http.StatusOK {
		t.Fatalf("expected bootstrap status 200, got %d, body=%s", bootstrapResp.Code, bootstrapResp.Body.String())
	}
	var bootstrap service.AuthBootstrap
	if err := json.Unmarshal(bootstrapResp.Body.Bytes(), &bootstrap); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	if !bootstrap.AuthEnabled || !bootstrap.SetupRequired || !bootstrap.SetupTokenRequired || bootstrap.Username != "root" {
		t.Fatalf("unexpected bootstrap response: %+v", bootstrap)
	}

	badSetupResp := performAuthJSONRequest(t, router, http.MethodPost, "/api/auth/setup", map[string]string{
		"username":   "root",
		"password":   "setup-password",
		"setupToken": "wrong-token",
	})
	if badSetupResp.Code != http.StatusForbidden {
		t.Fatalf("expected invalid setup token status 403, got %d, body=%s", badSetupResp.Code, badSetupResp.Body.String())
	}

	setupResp := performAuthJSONRequest(t, router, http.MethodPost, "/api/auth/setup", map[string]string{
		"username":   "root",
		"password":   "setup-password",
		"setupToken": "setup-token",
	})
	if setupResp.Code != http.StatusOK {
		t.Fatalf("expected setup status 200, got %d, body=%s", setupResp.Code, setupResp.Body.String())
	}
	if sessionCookie := findCookie(setupResp.Result().Cookies(), auth.SessionCookieName); sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("expected setup to create %s cookie", auth.SessionCookieName)
	}

	bootstrapResp = performAuthJSONRequest(t, router, http.MethodGet, "/api/auth/bootstrap", nil)
	if bootstrapResp.Code != http.StatusOK {
		t.Fatalf("expected bootstrap status 200 after setup, got %d, body=%s", bootstrapResp.Code, bootstrapResp.Body.String())
	}
	if err := json.Unmarshal(bootstrapResp.Body.Bytes(), &bootstrap); err != nil {
		t.Fatalf("decode post-setup bootstrap response: %v", err)
	}
	if bootstrap.SetupRequired {
		t.Fatalf("expected setup_required=false after setup, got %+v", bootstrap)
	}

	loginResp := performLoginRequestForUsername(t, router, "root", "setup-password")
	if loginResp.Code != http.StatusOK {
		t.Fatalf("expected login after setup status 200, got %d, body=%s", loginResp.Code, loginResp.Body.String())
	}
}

func TestLoginRateLimitBlocksFifthFailedAttempt(t *testing.T) {
	authHandler := newAuthHandlerForTest(t, "admin", "correct-password")
	router := newAuthTestRouter(authHandler)

	for attempt := 1; attempt < maxFailedLoginAttempts; attempt++ {
		resp := performLoginRequest(t, router, "wrong-password")
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected status 401, got %d, body=%s", attempt, resp.Code, resp.Body.String())
		}
	}

	resp := performLoginRequest(t, router, "wrong-password")
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429 on fifth failed attempt, got %d, body=%s", resp.Code, resp.Body.String())
	}
	if resp.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on rate limited response")
	}
}

func TestLoginRateLimitNormalizesUsername(t *testing.T) {
	authHandler := newAuthHandlerForTest(t, "admin", "correct-password")
	router := newAuthTestRouter(authHandler)
	usernames := []string{" admin ", "ADMIN", "Admin", "admin"}

	for attempt := 1; attempt < maxFailedLoginAttempts; attempt++ {
		resp := performLoginRequestForUsername(t, router, usernames[(attempt-1)%len(usernames)], "wrong-password")
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected status 401, got %d, body=%s", attempt, resp.Code, resp.Body.String())
		}
	}

	resp := performLoginRequestForUsername(t, router, "ADMIN", "wrong-password")
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429 on normalized fifth failed attempt, got %d, body=%s", resp.Code, resp.Body.String())
	}
}

func TestSuccessfulLoginClearsFailedAttempts(t *testing.T) {
	authHandler := newAuthHandlerForTest(t, "admin", "correct-password")
	router := newAuthTestRouter(authHandler)

	for attempt := 1; attempt < maxFailedLoginAttempts; attempt++ {
		resp := performLoginRequest(t, router, "wrong-password")
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected status 401, got %d, body=%s", attempt, resp.Code, resp.Body.String())
		}
	}

	resp := performLoginRequest(t, router, "correct-password")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected successful login to return 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	resp = performLoginRequest(t, router, "wrong-password")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected failed attempt after successful login to restart at 401, got %d, body=%s", resp.Code, resp.Body.String())
	}
}

func TestSuccessfulLoginSetsHttpOnlySessionCookie(t *testing.T) {
	authHandler, authService := newAuthHandlerAndServiceForTest(t, "admin", "correct-password")
	router := newAuthTestRouterWithSession(authHandler, authService)

	unauthStatusReq := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	unauthStatusResp := httptest.NewRecorder()
	router.ServeHTTP(unauthStatusResp, unauthStatusReq)
	if unauthStatusResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated status to return 401, got %d, body=%s", unauthStatusResp.Code, unauthStatusResp.Body.String())
	}

	resp := performLoginRequest(t, router, "correct-password")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected successful login to return 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if _, ok := body["token"]; ok {
		t.Fatal("login response must not expose the session token in JSON")
	}
	if _, ok := body["expires_at"]; !ok {
		t.Fatal("login response should include expires_at metadata")
	}

	sessionCookie := findCookie(resp.Result().Cookies(), auth.SessionCookieName)
	if sessionCookie == nil {
		t.Fatalf("expected %s cookie to be set", auth.SessionCookieName)
	}
	if !sessionCookie.HttpOnly {
		t.Fatal("expected session cookie to be HttpOnly")
	}
	if sessionCookie.Value == "" {
		t.Fatal("expected session cookie value")
	}
	if setCookie := resp.Header().Values("Set-Cookie"); !strings.Contains(strings.Join(setCookie, "; "), "SameSite=Lax") {
		t.Fatalf("expected SameSite=Lax on session cookie, got %v", setCookie)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	statusReq.AddCookie(sessionCookie)
	statusResp := httptest.NewRecorder()
	router.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("expected cookie session to authorize status, got %d, body=%s", statusResp.Code, statusResp.Body.String())
	}
	var statusBody map[string]any
	if err := json.Unmarshal(statusResp.Body.Bytes(), &statusBody); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if authenticated, ok := statusBody["authenticated"].(bool); !ok || !authenticated {
		t.Fatalf("expected authenticated status response, got %+v", statusBody)
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutResp := httptest.NewRecorder()
	router.ServeHTTP(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusOK {
		t.Fatalf("expected logout to return 200, got %d, body=%s", logoutResp.Code, logoutResp.Body.String())
	}
	clearCookie := findCookie(logoutResp.Result().Cookies(), auth.SessionCookieName)
	if clearCookie == nil || clearCookie.Value != "" {
		t.Fatal("expected logout to clear the session cookie")
	}

	statusAfterLogoutReq := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	statusAfterLogoutReq.AddCookie(sessionCookie)
	statusAfterLogoutResp := httptest.NewRecorder()
	router.ServeHTTP(statusAfterLogoutResp, statusAfterLogoutReq)
	if statusAfterLogoutResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected logged out session to return 401, got %d, body=%s", statusAfterLogoutResp.Code, statusAfterLogoutResp.Body.String())
	}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func performAuthJSONRequest(t *testing.T, router *gin.Engine, method, target string, payload any) *httptest.ResponseRecorder {
	t.Helper()

	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		requestBody, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal auth json request: %v", err)
		}
		body = bytes.NewReader(requestBody)
	}
	req := httptest.NewRequest(method, target, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = "203.0.113.11:4567"

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}
