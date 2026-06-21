package service

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"

	"golang.org/x/crypto/bcrypt"
)

const (
	authSessionDuration         = 7 * 24 * time.Hour
	authLastSeenPersistInterval = 5 * time.Minute
	authPasswordHashCost        = 12
	authMinPasswordLength       = 8
	authMaxPasswordLength       = 256
	authMaxSecurityEvents       = 100
	maxAppliedPasswordResets    = 20
	apiKeyScopeOpenAIChat       = "openai:chat"
	apiKeyScopeKnowledgeRead    = "knowledge:read"
	apiKeyScopeKnowledgeWrite   = "knowledge:write"
	apiKeyScopeConfigRead       = "config:read"
	defaultAPIKeyScope          = apiKeyScopeOpenAIChat
	sessionTokenPrefix          = "ailb_sess_"
	apiKeyTokenPrefix           = "ailb_sk_"
	apiKeyVisiblePrefixLength   = 18
)

var (
	ErrAuthDisabled        = errors.New("authentication is disabled")
	ErrSetupRequired       = errors.New("authentication setup is required")
	ErrSetupNotRequired    = errors.New("authentication setup is not required")
	ErrInvalidSetupToken   = errors.New("invalid setup token")
	ErrInvalidCredentials  = errors.New("invalid username or password")
	ErrInvalidAuthToken    = errors.New("invalid or expired token")
	ErrInvalidPassword     = errors.New("invalid password")
	ErrAPIKeyNotFound      = errors.New("api key not found")
	ErrCurrentPasswordFail = errors.New("current password is incorrect")
)

var allowedAPIKeyScopes = map[string]struct{}{
	apiKeyScopeOpenAIChat:     {},
	apiKeyScopeKnowledgeRead:  {},
	apiKeyScopeKnowledgeWrite: {},
	apiKeyScopeConfigRead:     {},
}

type AuthService struct {
	app          *AppService
	serverConfig model.ServerConfig
}

type AuthBootstrap struct {
	AuthEnabled        bool   `json:"auth_enabled"`
	SetupRequired      bool   `json:"setup_required"`
	SetupTokenRequired bool   `json:"setup_token_required"`
	Username           string `json:"username"`
}

type AuthLoginResult struct {
	Token     string
	ExpiresAt time.Time
	User      model.AuthUser
	Session   model.AuthSession
}

type AuthPrincipal struct {
	AuthType  string
	UserID    string
	Username  string
	Role      string
	SessionID string
	APIKeyID  string
	Scopes    []string
	ExpiresAt time.Time
}

type AuthSessionView struct {
	ID         string `json:"id"`
	CreatedAt  string `json:"createdAt"`
	ExpiresAt  string `json:"expiresAt"`
	LastSeenAt string `json:"lastSeenAt"`
	RevokedAt  string `json:"revokedAt,omitempty"`
	UserAgent  string `json:"userAgent,omitempty"`
	IP         string `json:"ip,omitempty"`
	Current    bool   `json:"current,omitempty"`
}

type APIKeyView struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"createdAt"`
	LastUsedAt string   `json:"lastUsedAt,omitempty"`
	RevokedAt  string   `json:"revokedAt,omitempty"`
}

type CreatedAPIKey struct {
	Item  APIKeyView `json:"item"`
	Token string     `json:"token"`
}

func NewAuthService(app *AppService, serverConfig model.ServerConfig) (*AuthService, error) {
	service := &AuthService{
		app:          app,
		serverConfig: serverConfig,
	}
	if err := service.initialize(); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *AuthService) initialize() error {
	if s == nil || s.app == nil || s.app.state == nil {
		return nil
	}

	if !s.serverConfig.EnableAuth {
		return nil
	}

	s.app.state.Mu.Lock()
	ensureAuthState(&s.app.state.Auth)
	hasUser := hasAuthUser(s.app.state.Auth)
	s.app.state.Mu.Unlock()

	if hasUser {
		return s.applyRootPasswordResetFromEnv()
	}

	if strings.TrimSpace(s.serverConfig.AuthPassword) == "" {
		log.Printf("authentication enabled, setup required for username: %s", s.defaultUsername())
		return nil
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(s.serverConfig.AuthPassword), authPasswordHashCost)
	if err != nil {
		return fmt.Errorf("hash auth password: %w", err)
	}

	now := nowRFC3339()
	username := s.defaultUsername()
	user := model.AuthUser{
		ID:                util.NextID("usr"),
		Username:          username,
		PasswordHash:      string(passwordHash),
		Role:              "root",
		CreatedAt:         now,
		UpdatedAt:         now,
		PasswordChangedAt: now,
	}

	s.app.state.Mu.Lock()
	ensureAuthState(&s.app.state.Auth)
	if hasAuthUser(s.app.state.Auth) {
		s.app.state.Mu.Unlock()
		return nil
	}
	s.app.state.Auth.Users[user.ID] = user
	appendSecurityEventLocked(&s.app.state.Auth, "root_bootstrapped_from_env", username, "", "", "Root user created from AUTH_PASSWORD.")
	if len([]rune(s.serverConfig.AuthPassword)) < authMinPasswordLength {
		appendSecurityEventLocked(&s.app.state.Auth, "weak_env_password", username, "", "", "AUTH_PASSWORD is shorter than the recommended length.")
	}
	s.app.state.Mu.Unlock()

	if err := s.app.saveState(); err != nil {
		return err
	}
	log.Printf("authentication root user initialized from AUTH_PASSWORD, username: %s", username)
	return nil
}

func (s *AuthService) applyRootPasswordResetFromEnv() error {
	resetToken := strings.TrimSpace(s.serverConfig.AuthResetToken)
	resetPassword := strings.TrimSpace(s.serverConfig.AuthResetPassword)
	if resetToken == "" && resetPassword == "" {
		return nil
	}
	if resetToken == "" || resetPassword == "" {
		return fmt.Errorf("AUTH_RESET_TOKEN and AUTH_RESET_PASSWORD must be set together")
	}
	if err := validateInteractivePassword(resetPassword); err != nil {
		return fmt.Errorf("invalid AUTH_RESET_PASSWORD: %w", err)
	}

	resetTokenHash := hashSecret(resetToken)
	nextHash, err := bcrypt.GenerateFromPassword([]byte(resetPassword), authPasswordHashCost)
	if err != nil {
		return fmt.Errorf("hash reset password: %w", err)
	}

	now := nowRFC3339()
	s.app.state.Mu.Lock()
	ensureAuthState(&s.app.state.Auth)
	if passwordResetTokenAppliedLocked(s.app.state.Auth, resetTokenHash) {
		s.app.state.Mu.Unlock()
		log.Printf("AUTH_RESET_TOKEN already applied, skipping root password reset")
		return nil
	}

	userID, user, ok := rootUserWithIDLocked(s.app.state.Auth)
	if !ok {
		s.app.state.Mu.Unlock()
		log.Printf("AUTH_RESET_TOKEN configured but root user does not exist, skipping password reset")
		return nil
	}

	user.PasswordHash = string(nextHash)
	user.UpdatedAt = now
	user.PasswordChangedAt = now
	s.app.state.Auth.Users[userID] = user
	for id, session := range s.app.state.Auth.Sessions {
		if session.UserID == userID && session.RevokedAt == "" {
			session.RevokedAt = now
			s.app.state.Auth.Sessions[id] = session
		}
	}
	s.app.state.Auth.AppliedPasswordResetTokens = append(s.app.state.Auth.AppliedPasswordResetTokens, resetTokenHash)
	if len(s.app.state.Auth.AppliedPasswordResetTokens) > maxAppliedPasswordResets {
		s.app.state.Auth.AppliedPasswordResetTokens = s.app.state.Auth.AppliedPasswordResetTokens[len(s.app.state.Auth.AppliedPasswordResetTokens)-maxAppliedPasswordResets:]
	}
	appendSecurityEventLocked(&s.app.state.Auth, "root_password_reset_from_env", user.Username, "", "", "Root password reset from AUTH_RESET_PASSWORD and all sessions revoked.")
	if len([]rune(resetPassword)) < authMinPasswordLength {
		appendSecurityEventLocked(&s.app.state.Auth, "weak_env_reset_password", user.Username, "", "", "AUTH_RESET_PASSWORD is shorter than the recommended length.")
	}
	s.app.state.Mu.Unlock()

	if err := s.app.saveState(); err != nil {
		return err
	}
	log.Printf("authentication root password reset from AUTH_RESET_PASSWORD, username: %s", user.Username)
	return nil
}

func (s *AuthService) Bootstrap() AuthBootstrap {
	bootstrap := AuthBootstrap{
		AuthEnabled:        s != nil && s.serverConfig.EnableAuth,
		SetupTokenRequired: s != nil && strings.TrimSpace(s.serverConfig.AuthSetupToken) != "",
		Username:           "root",
	}
	if s == nil {
		return bootstrap
	}

	bootstrap.Username = s.defaultUsername()
	if s.app == nil || s.app.state == nil {
		return bootstrap
	}

	s.app.state.Mu.RLock()
	if user, ok := rootUserLocked(s.app.state.Auth); ok {
		bootstrap.Username = user.Username
	}
	bootstrap.SetupRequired = s.serverConfig.EnableAuth && !hasAuthUser(s.app.state.Auth)
	s.app.state.Mu.RUnlock()
	return bootstrap
}

func (s *AuthService) IsSetupRequired() bool {
	if s == nil || !s.serverConfig.EnableAuth || s.app == nil || s.app.state == nil {
		return false
	}

	s.app.state.Mu.RLock()
	defer s.app.state.Mu.RUnlock()
	return !hasAuthUser(s.app.state.Auth)
}

func (s *AuthService) SetupRoot(username, password, setupToken, ip, userAgent string) (AuthLoginResult, error) {
	if s == nil || !s.serverConfig.EnableAuth {
		return AuthLoginResult{}, ErrAuthDisabled
	}
	if !s.IsSetupRequired() {
		return AuthLoginResult{}, ErrSetupNotRequired
	}
	if configuredToken := strings.TrimSpace(s.serverConfig.AuthSetupToken); configuredToken != "" && !constantCompareString(configuredToken, setupToken) {
		return AuthLoginResult{}, ErrInvalidSetupToken
	}
	if err := validateInteractivePassword(password); err != nil {
		return AuthLoginResult{}, err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), authPasswordHashCost)
	if err != nil {
		return AuthLoginResult{}, fmt.Errorf("hash password: %w", err)
	}

	now := nowRFC3339()
	username = strings.TrimSpace(username)
	if username == "" {
		username = s.defaultUsername()
	}
	user := model.AuthUser{
		ID:                util.NextID("usr"),
		Username:          username,
		PasswordHash:      string(passwordHash),
		Role:              "root",
		CreatedAt:         now,
		UpdatedAt:         now,
		PasswordChangedAt: now,
	}

	token, session, err := newSession(user.ID, ip, userAgent)
	if err != nil {
		return AuthLoginResult{}, err
	}

	s.app.state.Mu.Lock()
	ensureAuthState(&s.app.state.Auth)
	if hasAuthUser(s.app.state.Auth) {
		s.app.state.Mu.Unlock()
		return AuthLoginResult{}, ErrSetupNotRequired
	}
	s.app.state.Auth.Users[user.ID] = user
	s.app.state.Auth.Sessions[session.ID] = session
	appendSecurityEventLocked(&s.app.state.Auth, "root_setup_completed", username, ip, userAgent, "Root user initialized.")
	s.app.state.Mu.Unlock()

	if err := s.app.saveState(); err != nil {
		return AuthLoginResult{}, err
	}
	return AuthLoginResult{Token: token, ExpiresAt: parseTime(session.ExpiresAt), User: user, Session: session}, nil
}

func (s *AuthService) Login(username, password, ip, userAgent string) (AuthLoginResult, error) {
	if s == nil || !s.serverConfig.EnableAuth {
		return AuthLoginResult{}, ErrAuthDisabled
	}
	if s.IsSetupRequired() {
		return AuthLoginResult{}, ErrSetupRequired
	}

	username = strings.TrimSpace(username)
	if username == "" {
		username = s.defaultUsername()
	}

	s.app.state.Mu.RLock()
	user, ok := findUserByUsernameLocked(s.app.state.Auth, username)
	s.app.state.Mu.RUnlock()
	if !ok {
		s.recordSecurityEvent("login_failed", username, ip, userAgent, "Invalid username or password.")
		return AuthLoginResult{}, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		s.recordSecurityEvent("login_failed", username, ip, userAgent, "Invalid username or password.")
		return AuthLoginResult{}, ErrInvalidCredentials
	}

	token, session, err := newSession(user.ID, ip, userAgent)
	if err != nil {
		return AuthLoginResult{}, err
	}

	s.app.state.Mu.Lock()
	currentUser, ok := s.app.state.Auth.Users[user.ID]
	if !ok || currentUser.PasswordHash != user.PasswordHash {
		s.app.state.Mu.Unlock()
		return AuthLoginResult{}, ErrInvalidCredentials
	}
	ensureAuthState(&s.app.state.Auth)
	s.app.state.Auth.Sessions[session.ID] = session
	appendSecurityEventLocked(&s.app.state.Auth, "login_succeeded", currentUser.Username, ip, userAgent, "Root user signed in.")
	s.app.state.Mu.Unlock()

	if err := s.app.saveState(); err != nil {
		return AuthLoginResult{}, err
	}
	return AuthLoginResult{Token: token, ExpiresAt: parseTime(session.ExpiresAt), User: user, Session: session}, nil
}

func (s *AuthService) ValidateSessionToken(token string) (AuthPrincipal, error) {
	if s == nil || !s.serverConfig.EnableAuth {
		return AuthPrincipal{}, ErrAuthDisabled
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return AuthPrincipal{}, ErrInvalidAuthToken
	}

	tokenHash := hashSecret(token)
	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339)
	shouldPersist := false

	s.app.state.Mu.Lock()
	ensureAuthState(&s.app.state.Auth)
	sessionID, session, ok := findSessionByTokenHashLocked(s.app.state.Auth, tokenHash)
	if !ok || session.RevokedAt != "" {
		s.app.state.Mu.Unlock()
		return AuthPrincipal{}, ErrInvalidAuthToken
	}

	expiresAt := parseTime(session.ExpiresAt)
	if expiresAt.IsZero() || !expiresAt.After(now) {
		session.RevokedAt = nowText
		s.app.state.Auth.Sessions[sessionID] = session
		s.app.state.Mu.Unlock()
		_ = s.app.saveState()
		return AuthPrincipal{}, ErrInvalidAuthToken
	}

	user, ok := s.app.state.Auth.Users[session.UserID]
	if !ok {
		s.app.state.Mu.Unlock()
		return AuthPrincipal{}, ErrInvalidAuthToken
	}

	lastSeenAt := parseTime(session.LastSeenAt)
	if lastSeenAt.IsZero() || now.Sub(lastSeenAt) >= authLastSeenPersistInterval {
		session.LastSeenAt = nowText
		s.app.state.Auth.Sessions[sessionID] = session
		shouldPersist = true
	}
	principal := AuthPrincipal{
		AuthType:  "session",
		UserID:    user.ID,
		Username:  user.Username,
		Role:      user.Role,
		SessionID: session.ID,
		ExpiresAt: expiresAt,
	}
	s.app.state.Mu.Unlock()

	if shouldPersist {
		if err := s.app.saveState(); err != nil {
			log.Printf("failed to persist session last seen: %v", err)
		}
	}
	return principal, nil
}

func (s *AuthService) ValidateAPIKey(token string) (AuthPrincipal, error) {
	if s == nil || !s.serverConfig.EnableAuth {
		return AuthPrincipal{}, ErrAuthDisabled
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return AuthPrincipal{}, ErrInvalidAuthToken
	}

	keyHash := hashSecret(token)
	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339)
	shouldPersist := false

	s.app.state.Mu.Lock()
	ensureAuthState(&s.app.state.Auth)
	keyID, apiKey, ok := findAPIKeyByHashLocked(s.app.state.Auth, keyHash)
	if !ok || apiKey.RevokedAt != "" {
		s.app.state.Mu.Unlock()
		return AuthPrincipal{}, ErrInvalidAuthToken
	}

	lastUsedAt := parseTime(apiKey.LastUsedAt)
	if lastUsedAt.IsZero() || now.Sub(lastUsedAt) >= authLastSeenPersistInterval {
		apiKey.LastUsedAt = nowText
		s.app.state.Auth.APIKeys[keyID] = apiKey
		shouldPersist = true
	}

	username := s.defaultUsername()
	userID := ""
	role := "root"
	if user, ok := rootUserLocked(s.app.state.Auth); ok {
		username = user.Username
		userID = user.ID
		role = user.Role
	}
	principal := AuthPrincipal{
		AuthType: "api_key",
		UserID:   userID,
		Username: username,
		Role:     role,
		APIKeyID: apiKey.ID,
		Scopes:   append([]string(nil), apiKey.Scopes...),
	}
	s.app.state.Mu.Unlock()

	if shouldPersist {
		if err := s.app.saveState(); err != nil {
			log.Printf("failed to persist api key last used: %v", err)
		}
	}
	return principal, nil
}

func (s *AuthService) Logout(token string) error {
	if s == nil || !s.serverConfig.EnableAuth {
		return ErrAuthDisabled
	}

	tokenHash := hashSecret(strings.TrimSpace(token))
	now := nowRFC3339()
	changed := false

	s.app.state.Mu.Lock()
	sessionID, session, ok := findSessionByTokenHashLocked(s.app.state.Auth, tokenHash)
	if ok && session.RevokedAt == "" {
		session.RevokedAt = now
		s.app.state.Auth.Sessions[sessionID] = session
		username := ""
		if user, ok := s.app.state.Auth.Users[session.UserID]; ok {
			username = user.Username
		}
		appendSecurityEventLocked(&s.app.state.Auth, "logout", username, session.IP, session.UserAgent, "Session revoked.")
		changed = true
	}
	s.app.state.Mu.Unlock()

	if changed {
		return s.app.saveState()
	}
	return nil
}

func (s *AuthService) LogoutAll(userID string) error {
	if s == nil || !s.serverConfig.EnableAuth {
		return ErrAuthDisabled
	}

	now := nowRFC3339()
	changed := false
	username := ""

	s.app.state.Mu.Lock()
	if user, ok := s.app.state.Auth.Users[userID]; ok {
		username = user.Username
	}
	for id, session := range s.app.state.Auth.Sessions {
		if session.UserID == userID && session.RevokedAt == "" {
			session.RevokedAt = now
			s.app.state.Auth.Sessions[id] = session
			changed = true
		}
	}
	if changed {
		appendSecurityEventLocked(&s.app.state.Auth, "logout_all", username, "", "", "All sessions revoked.")
	}
	s.app.state.Mu.Unlock()

	if changed {
		return s.app.saveState()
	}
	return nil
}

func (s *AuthService) ChangePassword(userID, currentPassword, nextPassword, ip, userAgent string) error {
	if s == nil || !s.serverConfig.EnableAuth {
		return ErrAuthDisabled
	}
	if err := validateInteractivePassword(nextPassword); err != nil {
		return err
	}

	s.app.state.Mu.RLock()
	user, ok := s.app.state.Auth.Users[userID]
	s.app.state.Mu.RUnlock()
	if !ok {
		return ErrInvalidAuthToken
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
		s.recordSecurityEvent("password_change_failed", user.Username, ip, userAgent, "Current password mismatch.")
		return ErrCurrentPasswordFail
	}

	nextHash, err := bcrypt.GenerateFromPassword([]byte(nextPassword), authPasswordHashCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	now := nowRFC3339()
	s.app.state.Mu.Lock()
	currentUser, ok := s.app.state.Auth.Users[userID]
	if !ok || currentUser.PasswordHash != user.PasswordHash {
		s.app.state.Mu.Unlock()
		return ErrInvalidAuthToken
	}
	currentUser.PasswordHash = string(nextHash)
	currentUser.UpdatedAt = now
	currentUser.PasswordChangedAt = now
	s.app.state.Auth.Users[userID] = currentUser
	for id, session := range s.app.state.Auth.Sessions {
		if session.UserID == userID && session.RevokedAt == "" {
			session.RevokedAt = now
			s.app.state.Auth.Sessions[id] = session
		}
	}
	appendSecurityEventLocked(&s.app.state.Auth, "password_changed", currentUser.Username, ip, userAgent, "Password changed and all sessions revoked.")
	s.app.state.Mu.Unlock()

	return s.app.saveState()
}

func (s *AuthService) ListSessions(userID, currentSessionID string) []AuthSessionView {
	if s == nil || s.app == nil || s.app.state == nil {
		return nil
	}

	s.app.state.Mu.RLock()
	items := make([]AuthSessionView, 0, len(s.app.state.Auth.Sessions))
	for _, session := range s.app.state.Auth.Sessions {
		if userID != "" && session.UserID != userID {
			continue
		}
		items = append(items, sessionView(session, session.ID == currentSessionID))
	}
	s.app.state.Mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt > items[j].CreatedAt
	})
	return items
}

func (s *AuthService) CreateAPIKey(name string, scopes []string, username, ip, userAgent string) (CreatedAPIKey, error) {
	if s == nil || !s.serverConfig.EnableAuth {
		return CreatedAPIKey{}, ErrAuthDisabled
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return CreatedAPIKey{}, fmt.Errorf("api key name is required")
	}
	scopes = normalizeAPIKeyScopes(scopes)

	token, err := randomToken(apiKeyTokenPrefix)
	if err != nil {
		return CreatedAPIKey{}, err
	}
	now := nowRFC3339()
	apiKey := model.APIKey{
		ID:        util.NextID("key"),
		Name:      name,
		Prefix:    visibleTokenPrefix(token),
		KeyHash:   hashSecret(token),
		Scopes:    scopes,
		CreatedAt: now,
	}

	s.app.state.Mu.Lock()
	ensureAuthState(&s.app.state.Auth)
	s.app.state.Auth.APIKeys[apiKey.ID] = apiKey
	appendSecurityEventLocked(&s.app.state.Auth, "api_key_created", username, ip, userAgent, fmt.Sprintf("API key created: %s.", name))
	s.app.state.Mu.Unlock()

	if err := s.app.saveState(); err != nil {
		return CreatedAPIKey{}, err
	}
	return CreatedAPIKey{Item: apiKeyView(apiKey), Token: token}, nil
}

func (s *AuthService) ListAPIKeys() []APIKeyView {
	if s == nil || s.app == nil || s.app.state == nil {
		return nil
	}

	s.app.state.Mu.RLock()
	items := make([]APIKeyView, 0, len(s.app.state.Auth.APIKeys))
	for _, apiKey := range s.app.state.Auth.APIKeys {
		items = append(items, apiKeyView(apiKey))
	}
	s.app.state.Mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt > items[j].CreatedAt
	})
	return items
}

func (s *AuthService) RevokeAPIKey(id, username, ip, userAgent string) error {
	if s == nil || !s.serverConfig.EnableAuth {
		return ErrAuthDisabled
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return ErrAPIKeyNotFound
	}
	now := nowRFC3339()

	s.app.state.Mu.Lock()
	apiKey, ok := s.app.state.Auth.APIKeys[id]
	if !ok {
		s.app.state.Mu.Unlock()
		return ErrAPIKeyNotFound
	}
	if apiKey.RevokedAt == "" {
		apiKey.RevokedAt = now
		s.app.state.Auth.APIKeys[id] = apiKey
		appendSecurityEventLocked(&s.app.state.Auth, "api_key_revoked", username, ip, userAgent, fmt.Sprintf("API key revoked: %s.", apiKey.Name))
	}
	s.app.state.Mu.Unlock()

	return s.app.saveState()
}

func (s *AuthService) ListSecurityEvents(limit int) []model.SecurityEvent {
	if s == nil || s.app == nil || s.app.state == nil {
		return nil
	}
	if limit <= 0 || limit > authMaxSecurityEvents {
		limit = authMaxSecurityEvents
	}

	s.app.state.Mu.RLock()
	events := append([]model.SecurityEvent(nil), s.app.state.Auth.SecurityEvents...)
	s.app.state.Mu.RUnlock()

	sort.Slice(events, func(i, j int) bool {
		return events[i].CreatedAt > events[j].CreatedAt
	})
	if len(events) > limit {
		events = events[:limit]
	}
	return events
}

func (s *AuthService) recordSecurityEvent(eventType, username, ip, userAgent, message string) {
	if s == nil || s.app == nil || s.app.state == nil {
		return
	}

	s.app.state.Mu.Lock()
	ensureAuthState(&s.app.state.Auth)
	appendSecurityEventLocked(&s.app.state.Auth, eventType, username, ip, userAgent, message)
	s.app.state.Mu.Unlock()
	if err := s.app.saveState(); err != nil {
		log.Printf("failed to persist security event: %v", err)
	}
}

func (s *AuthService) defaultUsername() string {
	if s == nil {
		return "root"
	}
	username := strings.TrimSpace(s.serverConfig.AuthUsername)
	if username == "" {
		return "root"
	}
	return username
}

func validateInteractivePassword(password string) error {
	if strings.TrimSpace(password) == "" {
		return ErrInvalidPassword
	}
	length := len([]rune(password))
	if length < authMinPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalidPassword, authMinPasswordLength)
	}
	if length > authMaxPasswordLength {
		return fmt.Errorf("%w: password must be at most %d characters", ErrInvalidPassword, authMaxPasswordLength)
	}
	return nil
}

func newSession(userID, ip, userAgent string) (string, model.AuthSession, error) {
	token, err := randomToken(sessionTokenPrefix)
	if err != nil {
		return "", model.AuthSession{}, err
	}
	now := time.Now().UTC()
	session := model.AuthSession{
		ID:         util.NextID("ses"),
		UserID:     userID,
		TokenHash:  hashSecret(token),
		CreatedAt:  now.Format(time.RFC3339),
		ExpiresAt:  now.Add(authSessionDuration).Format(time.RFC3339),
		LastSeenAt: now.Format(time.RFC3339),
		UserAgent:  truncateForAudit(userAgent, 240),
		IP:         truncateForAudit(ip, 80),
	}
	return token, session, nil
}

func randomToken(prefix string) (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func constantCompareString(expected, actual string) bool {
	expectedHash := sha256.Sum256([]byte(strings.TrimSpace(expected)))
	actualHash := sha256.Sum256([]byte(strings.TrimSpace(actual)))
	return subtle.ConstantTimeCompare(expectedHash[:], actualHash[:]) == 1
}

func findUserByUsernameLocked(state model.AuthState, username string) (model.AuthUser, bool) {
	for _, user := range state.Users {
		if strings.EqualFold(user.Username, username) {
			return user, true
		}
	}
	return model.AuthUser{}, false
}

func rootUserLocked(state model.AuthState) (model.AuthUser, bool) {
	_, user, ok := rootUserWithIDLocked(state)
	return user, ok
}

func rootUserWithIDLocked(state model.AuthState) (string, model.AuthUser, bool) {
	for id, user := range state.Users {
		if user.Role == "root" {
			return id, user, true
		}
	}
	for id, user := range state.Users {
		return id, user, true
	}
	return "", model.AuthUser{}, false
}

func findSessionByTokenHashLocked(state model.AuthState, tokenHash string) (string, model.AuthSession, bool) {
	for id, session := range state.Sessions {
		if session.TokenHash == tokenHash {
			return id, session, true
		}
	}
	return "", model.AuthSession{}, false
}

func findAPIKeyByHashLocked(state model.AuthState, keyHash string) (string, model.APIKey, bool) {
	for id, apiKey := range state.APIKeys {
		if apiKey.KeyHash == keyHash {
			return id, apiKey, true
		}
	}
	return "", model.APIKey{}, false
}

func passwordResetTokenAppliedLocked(state model.AuthState, resetTokenHash string) bool {
	for _, appliedTokenHash := range state.AppliedPasswordResetTokens {
		if appliedTokenHash == resetTokenHash {
			return true
		}
	}
	return false
}

func appendSecurityEventLocked(state *model.AuthState, eventType, username, ip, userAgent, message string) {
	if state == nil {
		return
	}
	ensureAuthState(state)
	state.SecurityEvents = append(state.SecurityEvents, model.SecurityEvent{
		ID:        util.NextID("evt"),
		Type:      eventType,
		Username:  truncateForAudit(username, 80),
		IP:        truncateForAudit(ip, 80),
		UserAgent: truncateForAudit(userAgent, 240),
		CreatedAt: nowRFC3339(),
		Message:   truncateForAudit(message, 300),
	})
	if len(state.SecurityEvents) > authMaxSecurityEvents {
		state.SecurityEvents = state.SecurityEvents[len(state.SecurityEvents)-authMaxSecurityEvents:]
	}
}

func normalizeAPIKeyScopes(scopes []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.ToLower(strings.TrimSpace(scope))
		if scope == "" {
			continue
		}
		if _, ok := allowedAPIKeyScopes[scope]; !ok {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	if len(normalized) == 0 {
		return []string{defaultAPIKeyScope}
	}
	return normalized
}

func visibleTokenPrefix(token string) string {
	if len(token) <= apiKeyVisiblePrefixLength {
		return token
	}
	return token[:apiKeyVisiblePrefixLength]
}

func sessionView(session model.AuthSession, current bool) AuthSessionView {
	return AuthSessionView{
		ID:         session.ID,
		CreatedAt:  session.CreatedAt,
		ExpiresAt:  session.ExpiresAt,
		LastSeenAt: session.LastSeenAt,
		RevokedAt:  session.RevokedAt,
		UserAgent:  session.UserAgent,
		IP:         session.IP,
		Current:    current,
	}
}

func apiKeyView(apiKey model.APIKey) APIKeyView {
	return APIKeyView{
		ID:         apiKey.ID,
		Name:       apiKey.Name,
		Prefix:     apiKey.Prefix,
		Scopes:     append([]string(nil), apiKey.Scopes...),
		CreatedAt:  apiKey.CreatedAt,
		LastUsedAt: apiKey.LastUsedAt,
		RevokedAt:  apiKey.RevokedAt,
	}
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func truncateForAudit(value string, maxLength int) string {
	value = strings.TrimSpace(value)
	if maxLength <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxLength {
		return value
	}
	return string(runes[:maxLength])
}
