package router

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-localbase/internal/auth"
	"ai-localbase/internal/model"

	"github.com/gin-gonic/gin"
)

const (
	resourceClassChat      = "chat"
	resourceClassRetrieval = "retrieval"
	resourceClassEval      = "eval"
	resourceClassIndex     = "index"
	resourceClassUpload    = "upload"
	resourceClassModelTest = "model_test"

	defaultAPIRequestsPerMinute     = 120
	defaultAPIMaxConcurrentRequests = 16
	resourceClientStateTTL          = 10 * time.Minute
	resourceClientStateMaxEntries   = 10_000
)

type resourcePolicy struct {
	perKeyConcurrent int
	globalConcurrent int
}

type resourceClientState struct {
	windowStart  time.Time
	requestCount int
	active       int
	lastSeen     time.Time
}

type resourceLimiter struct {
	mu                sync.Mutex
	requestsPerMinute int
	policies          map[string]resourcePolicy
	clientStates      map[string]*resourceClientState
	globalActive      int
	lastPruneAt       time.Time
}

func newResourceLimiter(config model.ServerConfig) *resourceLimiter {
	requestsPerMinute := config.APIRequestsPerMinute
	if requestsPerMinute <= 0 {
		requestsPerMinute = defaultAPIRequestsPerMinute
	}
	globalConcurrent := config.APIMaxConcurrentRequests
	if globalConcurrent <= 0 {
		globalConcurrent = defaultAPIMaxConcurrentRequests
	}

	perKey := func(value, fallback int) int {
		if value > 0 {
			return value
		}
		return fallback
	}
	chatConcurrent := perKey(config.ChatMaxConcurrentRequests, 4)
	return &resourceLimiter{
		requestsPerMinute: requestsPerMinute,
		policies: map[string]resourcePolicy{
			resourceClassChat:      {perKeyConcurrent: chatConcurrent, globalConcurrent: globalConcurrent},
			resourceClassRetrieval: {perKeyConcurrent: chatConcurrent, globalConcurrent: globalConcurrent},
			resourceClassEval:      {perKeyConcurrent: perKey(config.EvalMaxConcurrentRequests, 1), globalConcurrent: globalConcurrent},
			resourceClassIndex:     {perKeyConcurrent: perKey(config.IndexMaxConcurrentRequests, 2), globalConcurrent: globalConcurrent},
			resourceClassUpload:    {perKeyConcurrent: perKey(config.UploadMaxConcurrentRequests, 4), globalConcurrent: globalConcurrent},
			resourceClassModelTest: {perKeyConcurrent: perKey(config.ModelTestMaxConcurrentRequests, 2), globalConcurrent: globalConcurrent},
		},
		clientStates: make(map[string]*resourceClientState),
	}
}

func resourceLimitMiddleware(limiter *resourceLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		class := classifyResourceRequest(c.Request)
		if class == "" || limiter == nil {
			c.Next()
			return
		}

		policy, ok := limiter.policies[class]
		if !ok {
			c.Next()
			return
		}

		clientKey := resourceClientKey(c)
		retryAfter, acquired := limiter.tryAcquire(class, clientKey, policy)
		if !acquired {
			seconds := int(retryAfter / time.Second)
			if retryAfter%time.Second != 0 {
				seconds++
			}
			if seconds < 1 {
				seconds = 1
			}
			c.Header("Retry-After", strconv.Itoa(seconds))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, model.APIError{Error: model.ErrorDetail{
				Code:      "resource_limit_exceeded",
				Message:   "request is temporarily limited; retry later",
				RequestID: c.GetString("requestId"),
			}})
			return
		}
		defer limiter.release(class, clientKey)
		if timeout := resourceTimeout(class); timeout > 0 {
			ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	}
}

func (l *resourceLimiter) tryAcquire(class, clientKey string, policy resourcePolicy) (time.Duration, bool) {
	now := time.Now()
	stateKey := class + "\x00" + clientKey

	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)

	state := l.clientStates[stateKey]
	if state == nil {
		state = &resourceClientState{windowStart: now}
		l.clientStates[stateKey] = state
	}
	state.lastSeen = now
	if now.Sub(state.windowStart) >= time.Minute {
		state.windowStart = now
		state.requestCount = 0
	}

	if l.requestsPerMinute > 0 && state.requestCount >= l.requestsPerMinute {
		return time.Minute - now.Sub(state.windowStart), false
	}
	if policy.perKeyConcurrent > 0 && state.active >= policy.perKeyConcurrent {
		return time.Second, false
	}
	if policy.globalConcurrent > 0 && l.globalActive >= policy.globalConcurrent {
		return time.Second, false
	}

	state.requestCount++
	state.active++
	l.globalActive++
	return 0, true
}

func (l *resourceLimiter) release(class, clientKey string) {
	stateKey := class + "\x00" + clientKey
	l.mu.Lock()
	defer l.mu.Unlock()
	if state := l.clientStates[stateKey]; state != nil {
		if state.active > 0 {
			state.active--
		}
	}
	if l.globalActive > 0 {
		l.globalActive--
	}
}

func (l *resourceLimiter) pruneLocked(now time.Time) {
	if len(l.clientStates) < resourceClientStateMaxEntries && now.Sub(l.lastPruneAt) < time.Minute {
		return
	}
	l.lastPruneAt = now
	cutoff := now.Add(-resourceClientStateTTL)
	for key, state := range l.clientStates {
		if state.active == 0 && state.lastSeen.Before(cutoff) {
			delete(l.clientStates, key)
		}
	}
}

func classifyResourceRequest(request *http.Request) string {
	if request == nil {
		return ""
	}
	path := strings.TrimRight(strings.TrimSpace(request.URL.Path), "/")
	if request.Method == http.MethodPost {
		switch {
		case path == "/v1/chat/completions" || path == "/v1/chat/completions/stream" || strings.HasSuffix(path, "/regenerate"):
			return resourceClassChat
		case path == "/api/config/test-chat-model" || path == "/api/config/test-embedding-model":
			return resourceClassModelTest
		case path == "/api/eval/datasets/generate" || strings.HasSuffix(path, "/runs"):
			return resourceClassEval
		case path == "/upload" || path == "/api/uploads" || strings.HasSuffix(path, "/documents"):
			return resourceClassUpload
		case strings.HasSuffix(path, "/batch-index") || strings.HasSuffix(path, "/reindex"):
			return resourceClassIndex
		case strings.HasSuffix(path, "/retrieval/debug"):
			return resourceClassRetrieval
		}
	}
	if request.Method == http.MethodGet && path == "/api/config/health-summary" {
		return resourceClassModelTest
	}
	return ""
}

func resourceClientKey(c *gin.Context) string {
	if c == nil {
		return "ip:unknown"
	}
	principal := auth.PrincipalFromContext(c)
	if principal.APIKeyID != "" {
		return "api-key:" + principal.APIKeyID
	}
	if principal.UserID != "" {
		return "user:" + principal.UserID
	}
	if principal.Username != "" {
		return "user:" + principal.Username
	}

	ip := strings.TrimSpace(c.ClientIP())
	if ip == "" {
		ip = "unknown"
	}
	return "ip:" + ip
}

func resourceTimeout(class string) time.Duration {
	switch class {
	case resourceClassChat:
		return 3 * time.Minute
	case resourceClassRetrieval:
		return 30 * time.Second
	case resourceClassEval:
		return 10 * time.Minute
	case resourceClassIndex, resourceClassUpload:
		return 10 * time.Minute
	case resourceClassModelTest:
		return 30 * time.Second
	default:
		return 0
	}
}
