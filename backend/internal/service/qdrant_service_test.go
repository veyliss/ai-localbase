package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"ai-localbase/internal/model"
)

func TestQdrantServiceSendsConfiguredAPIKey(t *testing.T) {
	const expectedAPIKey = "qdrant-test-key"
	qdrantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("api-key"); got != expectedAPIKey {
			t.Errorf("expected Qdrant API key %q, got %q", expectedAPIKey, got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"collections":[]}}`))
	}))
	t.Cleanup(qdrantServer.Close)

	qdrant := NewQdrantService(model.ServerConfig{
		QdrantURL:    qdrantServer.URL,
		QdrantAPIKey: expectedAPIKey,
	})
	if err := qdrant.Ping(context.Background()); err != nil {
		t.Fatalf("expected authenticated Qdrant ping to succeed: %v", err)
	}
}
