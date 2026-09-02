package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-localbase/internal/model"
	"ai-localbase/internal/service"
	"github.com/gin-gonic/gin"
)

func TestExpectedEmbeddingVectorSizeUsesServerConfig(t *testing.T) {
	appService := service.NewAppService(nil, nil, nil, model.ServerConfig{QdrantVectorSize: 1024})
	handler := NewConfigHandler(appService, nil)
	if actual := handler.expectedEmbeddingVectorSize(); actual != 1024 {
		t.Fatalf("expected configured vector size 1024, got %d", actual)
	}
}

func TestEmbeddingModelResponseIncludesConfiguredVectorSize(t *testing.T) {
	embeddingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3,0.4]]}`))
	}))
	t.Cleanup(embeddingServer.Close)

	appService := service.NewAppService(nil, nil, nil, model.ServerConfig{QdrantVectorSize: 4})
	handler := NewConfigHandler(appService, nil)
	payload, err := json.Marshal(TestEmbeddingModelRequest{
		Provider: "ollama",
		BaseURL:  embeddingServer.URL,
		Model:    "test-embedding",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/config/test-embedding-model", bytes.NewReader(payload))
	context.Request.Header.Set("Content-Type", "application/json")
	handler.TestEmbeddingModel(context)

	var response TestModelResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusOK || !response.Success {
		t.Fatalf("expected successful embedding probe, status=%d response=%#v", recorder.Code, response)
	}
	if response.VectorSize != 4 || response.ExpectedVectorSize != 4 {
		t.Fatalf("expected actual and configured dimensions to be 4, got %#v", response)
	}
}

func TestFormatErrorMessageExplainsEmbeddingDimensionMigration(t *testing.T) {
	err := &service.EmbeddingDimensionMismatchError{BatchItem: 0, Expected: 768, Actual: 1024}
	message := formatErrorMessage(err)
	for _, expected := range []string{"1024", "QDRANT_VECTOR_SIZE=768", "QDRANT_COLLECTION_PREFIX", "重新索引"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("expected migration message to contain %q, got %q", expected, message)
		}
	}

	actual, configured := embeddingDimensionDetails(err)
	if actual != 1024 || configured != 768 {
		t.Fatalf("unexpected dimension details: actual=%d configured=%d", actual, configured)
	}
}

func TestReadinessReturnsReadyWithoutOptionalDependencies(t *testing.T) {
	appService := service.NewAppService(nil, nil, nil, model.ServerConfig{})
	handler := NewConfigHandler(appService, nil)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	handler.Readiness(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected readiness status 200, got %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response ReadinessResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode readiness response: %v", err)
	}
	if response.Status != "ready" {
		t.Fatalf("expected ready status, got %#v", response)
	}
	if response.Checks["chat_model"].Status != "configured" {
		t.Fatalf("expected default chat model configuration to be reported, got %#v", response.Checks["chat_model"])
	}
}

func TestReadinessReturnsUnavailableWhenQdrantIsDown(t *testing.T) {
	qdrantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(qdrantServer.Close)

	serverConfig := model.ServerConfig{QdrantURL: qdrantServer.URL}
	qdrant := service.NewQdrantService(serverConfig)
	appService := service.NewAppService(qdrant, nil, nil, serverConfig)
	handler := NewConfigHandler(appService, qdrant)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	handler.Readiness(context)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness status 503, got %d, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestReadinessReturnsUnavailableWhenStagingManifestIsCorrupt(t *testing.T) {
	stagingDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stagingDir, "manifest.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write corrupt staging manifest: %v", err)
	}
	appService := service.NewAppService(nil, nil, nil, model.ServerConfig{StagingDir: stagingDir})
	handler := NewConfigHandler(appService, nil)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	handler.Readiness(context)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness status 503, got %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response ReadinessResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode readiness response: %v", err)
	}
	if response.Checks["upload_staging"].Status != "error" {
		t.Fatalf("expected staging readiness check to fail, got %#v", response.Checks["upload_staging"])
	}
	if strings.Contains(response.Checks["upload_staging"].ErrorMessage, stagingDir) {
		t.Fatalf("readiness error must not expose staging path: %#v", response.Checks["upload_staging"])
	}
}
