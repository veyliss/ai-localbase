package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-localbase/internal/model"
)

func TestStreamChatWithContextCancelsUpstreamRequest(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	service := &LLMService{streamClient: server.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.StreamChatWithContext(ctx, model.ChatCompletionRequest{
			Messages: []model.ChatMessage{{Role: "user", Content: "hello"}},
			Config:   model.ChatModelConfig{Provider: "openai", BaseURL: server.URL, Model: "test-model"},
		}, func(string) error { return nil })
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream request did not start")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream request did not stop after context cancellation")
	}
}

func TestOllamaStreamChatPreservesChunkWhitespace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		encoder := json.NewEncoder(w)
		for _, content := range []string{"您好", "， ", "欢迎使用", "\n\n**AI LocalBase**"} {
			if err := encoder.Encode(map[string]any{
				"message": map[string]any{"role": "assistant", "content": content},
				"done":    false,
			}); err != nil {
				t.Fatalf("encode stream chunk: %v", err)
			}
		}
		if err := encoder.Encode(map[string]any{"done": true}); err != nil {
			t.Fatalf("encode stream completion: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	service := &LLMService{streamClient: server.Client()}
	var output strings.Builder
	err := service.ollamaStreamChat(
		t.Context(),
		model.ChatModelConfig{BaseURL: server.URL, Model: "test-model"},
		model.ChatCompletionRequest{},
		func(chunk string) error {
			output.WriteString(chunk)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("stream chat: %v", err)
	}
	if output.String() != "您好， 欢迎使用\n\n**AI LocalBase**" {
		t.Fatalf("stream content changed whitespace: %q", output.String())
	}
}

func TestBuildOllamaChatRequestThinkingMode(t *testing.T) {
	enabled := true
	disabled := false
	tests := []struct {
		name     string
		think    *bool
		expected bool
	}{
		{name: "defaults to fast mode", think: nil, expected: false},
		{name: "keeps fast mode", think: &disabled, expected: false},
		{name: "enables thinking mode", think: &enabled, expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := buildOllamaChatRequest(
				model.ChatModelConfig{Model: "qwen3.5:0.8b"},
				model.ChatCompletionRequest{Think: tt.think},
				false,
			)
			if payload.Think == nil {
				t.Fatal("expected Ollama thinking mode to be explicit")
			}
			if *payload.Think != tt.expected {
				t.Fatalf("expected think=%v, got %v", tt.expected, *payload.Think)
			}
		})
	}
}

func TestChatRequestPayloadsPreserveZeroTemperature(t *testing.T) {
	payloads := []any{
		openAIChatRequest{},
		openAIChatStreamRequest{},
		buildOllamaChatRequest(model.ChatModelConfig{}, model.ChatCompletionRequest{}, false),
	}

	for _, payload := range payloads {
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		if !strings.Contains(string(body), `"temperature":0`) {
			t.Fatalf("expected explicit zero temperature, got %s", body)
		}
	}
}

func TestChatReturnsModelErrorWithoutFallbackResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"model unavailable"}}`, http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	service := &LLMService{client: server.Client()}
	response, err := service.Chat(model.ChatCompletionRequest{
		Messages: []model.ChatMessage{{Role: "user", Content: "hello"}},
		Config: model.ChatModelConfig{
			Provider: "openai",
			BaseURL:  server.URL,
			Model:    "test-model",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "model unavailable") {
		t.Fatalf("expected upstream model error, got %v", err)
	}
	if len(response.Choices) != 0 || len(response.Metadata) != 0 {
		t.Fatalf("expected no fabricated response, got %#v", response)
	}
}

func TestStreamChatReturnsModelErrorWithoutFallbackChunk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"stream unavailable"}}`, http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	service := &LLMService{streamClient: server.Client()}
	chunks := make([]string, 0)
	err := service.StreamChat(model.ChatCompletionRequest{
		Messages: []model.ChatMessage{{Role: "user", Content: "hello"}},
		Config: model.ChatModelConfig{
			Provider: "openai",
			BaseURL:  server.URL,
			Model:    "test-model",
		},
	}, func(chunk string) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "stream unavailable") {
		t.Fatalf("expected upstream stream error, got %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected no fabricated fallback chunks, got %#v", chunks)
	}
}

func TestStreamChatRejectsEmptySuccessfulStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)

	service := &LLMService{streamClient: server.Client()}
	err := service.StreamChat(model.ChatCompletionRequest{
		Messages: []model.ChatMessage{{Role: "user", Content: "hello"}},
		Config: model.ChatModelConfig{
			Provider: "openai",
			BaseURL:  server.URL,
			Model:    "test-model",
		},
	}, func(string) error {
		t.Fatal("empty stream must not emit content")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "empty stream") {
		t.Fatalf("expected empty stream error, got %v", err)
	}
}

func TestStreamChatRejectsIncompleteStreamWithoutRetryingPartialOutput(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
	}))
	t.Cleanup(server.Close)

	service := &LLMService{streamClient: server.Client()}
	chunks := make([]string, 0)
	err := service.StreamChat(model.ChatCompletionRequest{
		Messages: []model.ChatMessage{{Role: "user", Content: "hello"}},
		Config: model.ChatModelConfig{
			Provider: "openai",
			BaseURL:  server.URL,
			Model:    "test-model",
		},
	}, func(chunk string) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "ended before completion") {
		t.Fatalf("expected incomplete stream error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected partial stream not to be retried, got %d calls", calls)
	}
	if len(chunks) != 1 || chunks[0] != "partial" {
		t.Fatalf("expected only the real partial chunk, got %#v", chunks)
	}
}

func TestOllamaStreamChatRejectsIncompleteStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte("{\"message\":{\"role\":\"assistant\",\"content\":\"partial\"},\"done\":false}\n"))
	}))
	t.Cleanup(server.Close)

	service := &LLMService{streamClient: server.Client()}
	err := service.StreamChat(model.ChatCompletionRequest{
		Messages: []model.ChatMessage{{Role: "user", Content: "hello"}},
		Config: model.ChatModelConfig{
			Provider: "ollama",
			BaseURL:  server.URL,
			Model:    "test-model",
		},
	}, func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "ended before completion") {
		t.Fatalf("expected incomplete ollama stream error, got %v", err)
	}
}

func TestChatDoesNotRetryNonRetryableModelError(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"error":{"message":"model not found"}}`, http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	service := &LLMService{client: server.Client()}
	_, err := service.Chat(model.ChatCompletionRequest{
		Messages: []model.ChatMessage{{Role: "user", Content: "hello"}},
		Config: model.ChatModelConfig{
			Provider: "openai",
			BaseURL:  server.URL,
			Model:    "missing-model",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("expected model-not-found error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one non-retryable request, got %d", calls)
	}
}

func TestIsLegacyOperationalAssistantContent(t *testing.T) {
	for _, content := range []string{
		"⚠️ AI 模型调用已降级\n\n模型超时",
		"### 结果说明\nAI模型调用已降级至安全阈值，无法回答。",
		"你好，我是 AI LocalBase 助手。你可以先选择知识库，或者进一步选中某个文档后再提问。",
		"当前会话已清空。你可以继续发起新的提问。",
		"当前模型正在后台处理会话「测试」，请等待其完成后再发起新问题。",
	} {
		if !IsLegacyOperationalAssistantContent(content) {
			t.Fatalf("expected operational degradation content to be detected: %q", content)
		}
	}
	if IsLegacyOperationalAssistantContent("模型介绍了系统的降级设计，但当前回答正常。") {
		t.Fatal("expected ordinary content not to be treated as a degraded response")
	}
}
