package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"ai-localbase/internal/model"
)

type LLMService struct {
	client       *http.Client
	streamClient *http.Client
}

const (
	defaultChatRequestTimeout   = 75 * time.Second
	defaultStreamHeaderTimeout  = 45 * time.Second
	defaultStreamRequestTimeout = 150 * time.Second
)

// ── OpenAI-compatible structs ────────────────────────────────────────────────

type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []model.ChatMessage `json:"messages"`
	Temperature float64             `json:"temperature"`
}

type openAIChatResponse struct {
	ID      string                        `json:"id"`
	Object  string                        `json:"object"`
	Created int64                         `json:"created"`
	Model   string                        `json:"model"`
	Choices []model.ChatCompletionChoice  `json:"choices"`
	Error   *openAICompatibleErrorPayload `json:"error,omitempty"`
}

type openAICompatibleErrorPayload struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

type openAIChatStreamRequest struct {
	Model       string              `json:"model"`
	Messages    []model.ChatMessage `json:"messages"`
	Temperature float64             `json:"temperature"`
	Stream      bool                `json:"stream"`
}

type openAIChatStreamChunk struct {
	Choices []struct {
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Error *openAICompatibleErrorPayload `json:"error,omitempty"`
}

// ── Ollama native API structs ────────────────────────────────────────────────

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []model.ChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Think    *bool               `json:"think,omitempty"`
	Options  *ollamaOptions      `json:"options,omitempty"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature"`
}

type ollamaChatResponse struct {
	Model     string            `json:"model"`
	CreatedAt string            `json:"created_at"`
	Message   model.ChatMessage `json:"message"`
	Done      bool              `json:"done"`
	Error     string            `json:"error,omitempty"`
}

// ── Constructor ──────────────────────────────────────────────────────────────

func NewLLMService() *LLMService {
	nonStreamTransport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: defaultStreamRequestTimeout,
		DisableCompression:    false,
	}
	streamTransport := nonStreamTransport.Clone()
	streamTransport.ResponseHeaderTimeout = defaultStreamHeaderTimeout

	return &LLMService{
		client: &http.Client{
			Transport: nonStreamTransport,
		},
		streamClient: &http.Client{
			Transport: streamTransport,
		},
	}
}

// ── Public methods ───────────────────────────────────────────────────────────

func (s *LLMService) Chat(req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	return s.ChatWithContext(context.Background(), req)
}

func (s *LLMService) ChatWithContext(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	cfg, err := normalizeChatConfig(req)
	if err != nil {
		return model.ChatCompletionResponse{}, err
	}
	ctx = normalizeServiceContext(ctx)

	requestTimeout := defaultChatRequestTimeout
	if req.Think != nil && *req.Think {
		requestTimeout = defaultStreamRequestTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	if cfg.Provider == "ollama" {
		var result model.ChatCompletionResponse
		err = sharedModelRuntimeScheduler.run(ctx, modelRuntimePriorityHigh, func(runCtx context.Context) error {
			var callErr error
			result, callErr = s.ollamaChat(runCtx, cfg, req)
			return callErr
		})
		if err != nil {
			return model.ChatCompletionResponse{}, err
		}
		return result, nil
	}

	result, err := s.openAIChat(ctx, cfg, req)
	if err != nil {
		return model.ChatCompletionResponse{}, err
	}

	return result, nil
}

func (s *LLMService) StreamChat(req model.ChatCompletionRequest, onChunk func(string) error) error {
	return s.StreamChatWithContext(context.Background(), req, onChunk)
}

func (s *LLMService) StreamChatWithContext(ctx context.Context, req model.ChatCompletionRequest, onChunk func(string) error) error {
	cfg, err := normalizeChatConfig(req)
	if err != nil {
		return err
	}
	ctx = normalizeServiceContext(ctx)

	ctx, cancel := context.WithTimeout(ctx, defaultStreamRequestTimeout)
	defer cancel()

	if cfg.Provider == "ollama" {
		err = sharedModelRuntimeScheduler.run(ctx, modelRuntimePriorityHigh, func(runCtx context.Context) error {
			return s.ollamaStreamChat(runCtx, cfg, req, onChunk)
		})
	} else {
		err = s.openAIStreamChat(ctx, cfg, req, onChunk)
	}

	if err != nil {
		return err
	}

	return nil
}

// ── OpenAI-compatible implementation ─────────────────────────────────────────

func (s *LLMService) openAIChat(ctx context.Context, cfg model.ChatModelConfig, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	payload := openAIChatRequest{
		Model:       cfg.Model,
		Messages:    req.Messages,
		Temperature: cfg.Temperature,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return model.ChatCompletionResponse{}, fmt.Errorf("failed to encode chat request")
	}

	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	var result model.ChatCompletionResponse
	err = retryModelCall(ctx, 3, 250*time.Millisecond, func() error {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create model request")
		}

		httpReq.Header.Set("Content-Type", "application/json")
		if cfg.APIKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}

		resp, err := s.client.Do(httpReq)
		if err != nil {
			return fmt.Errorf("failed to call model api: %w", err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read model response")
		}

		var llmResp openAIChatResponse
		if err := json.Unmarshal(respBody, &llmResp); err != nil {
			return fmt.Errorf("invalid model response format")
		}

		if resp.StatusCode >= http.StatusBadRequest {
			if llmResp.Error != nil && strings.TrimSpace(llmResp.Error.Message) != "" {
				return fmt.Errorf("model api error: %s", llmResp.Error.Message)
			}
			return fmt.Errorf("model api error: http %d", resp.StatusCode)
		}

		if len(llmResp.Choices) == 0 {
			return fmt.Errorf("model api returned empty choices")
		}
		if strings.TrimSpace(llmResp.Choices[0].Message.Content) == "" {
			return fmt.Errorf("model api returned empty response")
		}

		result = model.ChatCompletionResponse{
			ID:      llmResp.ID,
			Object:  llmResp.Object,
			Created: llmResp.Created,
			Model:   llmResp.Model,
			Choices: llmResp.Choices,
		}
		return nil
	})

	return result, err
}

func (s *LLMService) openAIStreamChat(ctx context.Context, cfg model.ChatModelConfig, req model.ChatCompletionRequest, onChunk func(string) error) error {
	payload := openAIChatStreamRequest{
		Model:       cfg.Model,
		Messages:    req.Messages,
		Temperature: cfg.Temperature,
		Stream:      true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode chat request")
	}

	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	return retryModelCall(ctx, 2, 200*time.Millisecond, func() error {
		attemptDeliveredContent := false
		streamCompleted := false
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create model request")
		}

		httpReq.Header.Set("Content-Type", "application/json")
		if cfg.APIKey != "" {
			httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}

		resp, err := s.streamClient.Do(httpReq)
		if err != nil {
			return fmt.Errorf("failed to call model api: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= http.StatusBadRequest {
			respBody, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				return fmt.Errorf("model api error: http %d", resp.StatusCode)
			}

			var llmResp openAIChatResponse
			if err := json.Unmarshal(respBody, &llmResp); err == nil && llmResp.Error != nil && strings.TrimSpace(llmResp.Error.Message) != "" {
				return fmt.Errorf("model api error: %s", llmResp.Error.Message)
			}

			return fmt.Errorf("model api error: http %d", resp.StatusCode)
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || !strings.HasPrefix(line, "data:") {
				continue
			}

			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "[DONE]" {
				streamCompleted = true
				break
			}

			var chunk openAIChatStreamChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				streamErr := fmt.Errorf("invalid model stream chunk: %w", err)
				if attemptDeliveredContent {
					return stopRetryError{err: streamErr}
				}
				return streamErr
			}

			if chunk.Error != nil && strings.TrimSpace(chunk.Error.Message) != "" {
				return fmt.Errorf("model api error: %s", chunk.Error.Message)
			}

			for _, choice := range chunk.Choices {
				if strings.TrimSpace(choice.FinishReason) != "" {
					streamCompleted = true
				}
				if choice.Delta.Content == "" {
					continue
				}
				if err := onChunk(choice.Delta.Content); err != nil {
					return stopRetryError{err: err}
				}
				if strings.TrimSpace(choice.Delta.Content) != "" {
					attemptDeliveredContent = true
				}
			}
		}

		if err := scanner.Err(); err != nil {
			if attemptDeliveredContent {
				return stopRetryError{err: fmt.Errorf("model stream interrupted after output: %w", err)}
			}
			return fmt.Errorf("failed to read model stream")
		}
		if !attemptDeliveredContent {
			return fmt.Errorf("model api returned empty stream")
		}
		if !streamCompleted {
			return stopRetryError{err: fmt.Errorf("model stream ended before completion")}
		}

		return nil
	})
}

// ── Ollama native implementation ──────────────────────────────────────────────

func buildOllamaChatRequest(cfg model.ChatModelConfig, req model.ChatCompletionRequest, stream bool) ollamaChatRequest {
	think := false
	if req.Think != nil {
		think = *req.Think
	}
	payload := ollamaChatRequest{
		Model:    cfg.Model,
		Messages: req.Messages,
		Stream:   stream,
		Think:    &think,
		Options:  &ollamaOptions{Temperature: cfg.Temperature},
	}
	return payload
}

func (s *LLMService) ollamaChat(ctx context.Context, cfg model.ChatModelConfig, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	payload := buildOllamaChatRequest(cfg, req, false)

	body, err := json.Marshal(payload)
	if err != nil {
		return model.ChatCompletionResponse{}, fmt.Errorf("failed to encode chat request")
	}

	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/api/chat"
	var result model.ChatCompletionResponse
	err = retryModelCall(ctx, 3, 250*time.Millisecond, func() error {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create model request")
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := s.client.Do(httpReq)
		if err != nil {
			return fmt.Errorf("failed to call model api: %w", err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read model response")
		}

		var ollamaResp ollamaChatResponse
		if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
			return fmt.Errorf("invalid model response format")
		}

		if resp.StatusCode >= http.StatusBadRequest {
			if strings.TrimSpace(ollamaResp.Error) != "" {
				return fmt.Errorf("model api error: %s", ollamaResp.Error)
			}
			return fmt.Errorf("model api error: http %d", resp.StatusCode)
		}

		if strings.TrimSpace(ollamaResp.Message.Content) == "" {
			return fmt.Errorf("model api returned empty response")
		}

		result = model.ChatCompletionResponse{
			ID:      "ollama-" + ollamaResp.Model,
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   ollamaResp.Model,
			Choices: []model.ChatCompletionChoice{{
				Index:   0,
				Message: ollamaResp.Message,
			}},
		}
		return nil
	})

	return result, err
}

func (s *LLMService) ollamaStreamChat(ctx context.Context, cfg model.ChatModelConfig, req model.ChatCompletionRequest, onChunk func(string) error) error {
	payload := buildOllamaChatRequest(cfg, req, true)

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode chat request")
	}

	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/api/chat"
	return retryModelCall(ctx, 2, 200*time.Millisecond, func() error {
		attemptDeliveredContent := false
		streamCompleted := false
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create model request")
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := s.streamClient.Do(httpReq)
		if err != nil {
			return fmt.Errorf("failed to call model api: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= http.StatusBadRequest {
			respBody, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				return fmt.Errorf("model api error: http %d", resp.StatusCode)
			}
			var ollamaResp ollamaChatResponse
			if err := json.Unmarshal(respBody, &ollamaResp); err == nil && strings.TrimSpace(ollamaResp.Error) != "" {
				return fmt.Errorf("model api error: %s", ollamaResp.Error)
			}
			return fmt.Errorf("model api error: http %d", resp.StatusCode)
		}

		// Ollama streams newline-delimited JSON objects (NDJSON), not SSE
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			var chunk ollamaChatResponse
			if err := json.Unmarshal([]byte(line), &chunk); err != nil {
				streamErr := fmt.Errorf("invalid model stream chunk: %w", err)
				if attemptDeliveredContent {
					return stopRetryError{err: streamErr}
				}
				return streamErr
			}

			if strings.TrimSpace(chunk.Error) != "" {
				return fmt.Errorf("model api error: %s", chunk.Error)
			}

			if chunk.Done {
				streamCompleted = true
				break
			}

			if content := chunk.Message.Content; content != "" {
				if err := onChunk(content); err != nil {
					return stopRetryError{err: err}
				}
				if strings.TrimSpace(content) != "" {
					attemptDeliveredContent = true
				}
			}
		}

		if err := scanner.Err(); err != nil {
			if attemptDeliveredContent {
				return stopRetryError{err: fmt.Errorf("model stream interrupted after output: %w", err)}
			}
			return fmt.Errorf("failed to read model stream")
		}
		if !attemptDeliveredContent {
			return fmt.Errorf("model api returned empty stream")
		}
		if !streamCompleted {
			return stopRetryError{err: fmt.Errorf("model stream ended before completion")}
		}

		return nil
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func IsLegacyOperationalAssistantContent(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	for _, exact := range []string{
		"你好，我是 AI LocalBase 助手。你可以先选择知识库，或者进一步选中某个文档后再提问。",
		"你好，我是 AI Local Base 助手。你可以先选择知识库，或者进一步选中某个文档后再提问。",
		"当前会话已清空。你可以继续发起新的提问。",
	} {
		if trimmed == exact {
			return true
		}
	}
	if strings.HasPrefix(trimmed, "当前模型正在后台处理会话「") && strings.HasSuffix(trimmed, "请等待其完成后再发起新问题。") {
		return true
	}
	for _, prefix := range []string{
		"⚠️ AI 模型调用失败",
		"⚠ AI 模型调用失败",
		"⚠️ AI 模型调用已降级",
		"⚠ AI 模型调用已降级",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	compact := strings.ReplaceAll(trimmed, " ", "")
	return strings.Contains(compact, "AI模型调用已降级至安全阈值")
}

func retryModelCall(ctx context.Context, attempts int, baseDelay time.Duration, fn func() error) error {
	return retryWithBackoff(ctx, attempts, baseDelay, func() error {
		err := fn()
		if err == nil {
			return nil
		}
		if !isRetryableModelError(err) {
			return stopRetryError{err: err}
		}
		return err
	})
}

type stopRetryError struct {
	err error
}

func (e stopRetryError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e stopRetryError) Unwrap() error {
	return e.err
}

func (e stopRetryError) StopRetry() bool {
	return true
}

func isRetryableModelError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "model not found") ||
		strings.Contains(message, "model is required") ||
		strings.Contains(message, "invalid model response format") ||
		strings.Contains(message, "returned empty choices") ||
		strings.Contains(message, "returned empty response") {
		return false
	}
	if strings.Contains(message, "http 429") ||
		strings.Contains(message, "http 502") ||
		strings.Contains(message, "http 503") ||
		strings.Contains(message, "http 504") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "timeout") ||
		strings.Contains(message, "temporarily unavailable") ||
		strings.Contains(message, "failed to call model api") ||
		strings.Contains(message, "failed to read model stream") {
		return true
	}
	return false
}

func normalizeChatConfig(req model.ChatCompletionRequest) (model.ChatModelConfig, error) {
	cfg := req.Config
	if strings.TrimSpace(cfg.Model) == "" {
		return model.ChatModelConfig{}, fmt.Errorf("model is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		if cfg.Provider == "ollama" {
			cfg.BaseURL = "http://localhost:11434"
		} else {
			cfg.BaseURL = "http://localhost:11434/v1"
		}
	}
	if cfg.Temperature <= 0 {
		cfg.Temperature = 0.7
	}
	if strings.TrimSpace(cfg.Provider) == "" {
		cfg.Provider = "ollama"
	}
	return cfg, nil
}
