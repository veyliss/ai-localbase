package service

import (
	"math"
	"testing"
	"time"
)

func TestSemanticCacheHit(t *testing.T) {
	cache := NewSemanticCache(0.92, 10, time.Minute)
	vecA := normalizeFloat32Vector([]float32{1, 0, 0})
	vecB := normalizeFloat32Vector([]float32{0.99, 0.01, 0})
	chunks := []RetrievedChunk{{DocumentChunk: DocumentChunk{ID: "c1", DocumentName: "doc", Text: "hello"}}}

	cache.Set("kb=kb-1", vecA, "query-a", chunks)
	entry, ok := cache.Get("kb=kb-1", vecB)
	if !ok || entry == nil {
		t.Fatalf("expected cache hit")
	}
	if entry.Query != "query-a" {
		t.Fatalf("expected query-a, got %s", entry.Query)
	}
	stats := cache.Stats()
	if stats["hits"].(int) != 1 {
		t.Fatalf("expected hit count 1, got %v", stats["hits"])
	}
}

func TestSemanticCacheMiss(t *testing.T) {
	cache := NewSemanticCache(0.92, 10, time.Minute)
	vecA := normalizeFloat32Vector([]float32{1, 0, 0})
	vecB := normalizeFloat32Vector([]float32{0, 1, 0})
	cache.Set("kb=kb-1", vecA, "query-a", nil)
	if _, ok := cache.Get("kb=kb-1", vecB); ok {
		t.Fatalf("expected cache miss")
	}
}

func TestSemanticCacheTTL(t *testing.T) {
	ttl := 20 * time.Millisecond
	cache := NewSemanticCache(0.92, 10, ttl)
	vec := normalizeFloat32Vector([]float32{1, 0, 0})
	cache.Set("kb=kb-1", vec, "query-a", nil)
	if len(cache.entries) != 1 {
		t.Fatalf("expected 1 entry")
	}
	cache.entries[0].CreatedAt = time.Now().Add(-2 * ttl)
	if _, ok := cache.Get("kb=kb-1", vec); ok {
		t.Fatalf("expected cache miss after ttl")
	}
	if len(cache.entries) != 0 {
		t.Fatalf("expected entries cleared after ttl")
	}
}

func TestSemanticCacheMaxEntries(t *testing.T) {
	cache := NewSemanticCache(0.92, 2, time.Minute)
	cache.Set("kb=kb-1", normalizeFloat32Vector([]float32{1, 0, 0}), "q1", nil)
	cache.Set("kb=kb-1", normalizeFloat32Vector([]float32{0, 1, 0}), "q2", nil)
	cache.Set("kb=kb-1", normalizeFloat32Vector([]float32{0, 0, 1}), "q3", nil)
	if len(cache.entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(cache.entries))
	}
	if cache.entries[0].Query != "q2" || cache.entries[1].Query != "q3" {
		t.Fatalf("expected FIFO eviction, got %s, %s", cache.entries[0].Query, cache.entries[1].Query)
	}
}

func TestSemanticCacheDoesNotCrossScopes(t *testing.T) {
	cache := NewSemanticCache(0.92, 10, time.Minute)
	vector := normalizeFloat32Vector([]float32{1, 0, 0})
	cache.Set("kb=kb-novel", vector, "详细介绍", []RetrievedChunk{{
		DocumentChunk: DocumentChunk{KnowledgeBaseID: "kb-novel", DocumentID: "doc-novel"},
	}})

	if _, ok := cache.Get("kb=kb-school", vector); ok {
		t.Fatal("semantic cache must not reuse chunks across knowledge base scopes")
	}
	if _, ok := cache.Get("kb=kb-novel|doc=doc-other", vector); ok {
		t.Fatal("semantic cache must not reuse chunks across document scopes")
	}
}

func TestSemanticCacheClear(t *testing.T) {
	cache := NewSemanticCache(0.92, 10, time.Minute)
	cache.Set("kb=kb-1", normalizeFloat32Vector([]float32{1, 0, 0}), "query-a", nil)
	cache.Clear()

	if len(cache.entries) != 0 {
		t.Fatalf("expected cache entries to be cleared, got %d", len(cache.entries))
	}
}

func normalizeFloat32Vector(vec []float32) []float32 {
	var sum float64
	for _, v := range vec {
		sum += float64(v * v)
	}
	if sum == 0 {
		return vec
	}
	den := float32(math.Sqrt(sum))
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = v / den
	}
	return out
}
