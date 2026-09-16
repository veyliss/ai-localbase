package service

import (
	"math"
	"sync"
	"time"
)

const (
	defaultSemanticCacheThreshold  float32       = 0.92
	defaultSemanticCacheMaxEntries int           = 500
	defaultSemanticCacheTTL        time.Duration = time.Hour
)

// SemanticCacheEntry 语义缓存条目
type SemanticCacheEntry struct {
	Scope          string
	QueryEmbedding []float32
	Query          string
	Chunks         []RetrievedChunk
	CreatedAt      time.Time
	HitCount       int
}

// SemanticCache 语义相似缓存
// 使用 embedding cosine similarity 匹配相似查询
type SemanticCache struct {
	entries             []*SemanticCacheEntry
	mu                  sync.RWMutex
	similarityThreshold float32
	maxEntries          int
	ttl                 time.Duration
}

// NewSemanticCache 创建语义缓存
func NewSemanticCache(threshold float32, maxEntries int, ttl time.Duration) *SemanticCache {
	if threshold <= 0 {
		threshold = defaultSemanticCacheThreshold
	}
	if maxEntries <= 0 {
		maxEntries = defaultSemanticCacheMaxEntries
	}
	if ttl <= 0 {
		ttl = defaultSemanticCacheTTL
	}
	return &SemanticCache{
		entries:             make([]*SemanticCacheEntry, 0, maxEntries),
		similarityThreshold: threshold,
		maxEntries:          maxEntries,
		ttl:                 ttl,
	}
}

// Get 查找语义相似的缓存条目
// 遍历所有 entries，计算 cosine similarity，返回第一个超过阈值的
// 同时清理过期条目
func (c *SemanticCache) Get(scope string, queryEmbedding []float32) (*SemanticCacheEntry, bool) {
	if c == nil || scope == "" || len(queryEmbedding) == 0 {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	filtered := c.entries[:0]
	for _, entry := range c.entries {
		if entry == nil {
			continue
		}
		if c.ttl > 0 && now.Sub(entry.CreatedAt) > c.ttl {
			continue
		}
		filtered = append(filtered, entry)
	}
	c.entries = filtered

	for _, entry := range c.entries {
		if entry.Scope != scope || len(entry.QueryEmbedding) == 0 {
			continue
		}
		similarity := cosineSimilarityLocal(queryEmbedding, entry.QueryEmbedding)
		if similarity >= c.similarityThreshold {
			entry.HitCount++
			return cloneSemanticCacheEntry(entry), true
		}
	}

	return nil, false
}

// Set 存入缓存条目
// 若超过 maxEntries，移除最旧的条目（FIFO）
func (c *SemanticCache) Set(scope string, queryEmbedding []float32, query string, chunks []RetrievedChunk) {
	if c == nil || scope == "" || len(queryEmbedding) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	filtered := c.entries[:0]
	for _, entry := range c.entries {
		if entry == nil {
			continue
		}
		if c.ttl > 0 && now.Sub(entry.CreatedAt) > c.ttl {
			continue
		}
		filtered = append(filtered, entry)
	}
	c.entries = filtered

	entry := &SemanticCacheEntry{
		Scope:          scope,
		QueryEmbedding: cloneFloat32Slice(queryEmbedding),
		Query:          query,
		Chunks:         cloneRetrievedChunks(chunks),
		CreatedAt:      now,
		HitCount:       0,
	}
	c.entries = append(c.entries, entry)

	if c.maxEntries > 0 && len(c.entries) > c.maxEntries {
		overflow := len(c.entries) - c.maxEntries
		c.entries = c.entries[overflow:]
	}
}

// Clear removes all cached retrieval results. Cache entries are process-local
// and must not survive a configuration or index-generation change.
func (c *SemanticCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = c.entries[:0]
	c.mu.Unlock()
}

// Stats 返回缓存统计（总条目数、命中次数等）
func (c *SemanticCache) Stats() map[string]interface{} {
	if c == nil {
		return map[string]interface{}{
			"entries":    0,
			"hits":       0,
			"threshold":  0,
			"maxEntries": 0,
			"ttlSeconds": 0,
		}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	var hits int
	for _, entry := range c.entries {
		if entry == nil {
			continue
		}
		hits += entry.HitCount
	}

	return map[string]interface{}{
		"entries":    len(c.entries),
		"hits":       hits,
		"threshold":  c.similarityThreshold,
		"maxEntries": c.maxEntries,
		"ttlSeconds": int(c.ttl.Seconds()),
	}
}

func cosineSimilarityLocal(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}

func cloneSemanticCacheEntry(entry *SemanticCacheEntry) *SemanticCacheEntry {
	if entry == nil {
		return nil
	}
	cloned := &SemanticCacheEntry{
		Scope:          entry.Scope,
		QueryEmbedding: cloneFloat32Slice(entry.QueryEmbedding),
		Query:          entry.Query,
		Chunks:         cloneRetrievedChunks(entry.Chunks),
		CreatedAt:      entry.CreatedAt,
		HitCount:       entry.HitCount,
	}
	return cloned
}

func cloneFloat32Slice(input []float32) []float32 {
	if len(input) == 0 {
		return nil
	}
	out := make([]float32, len(input))
	copy(out, input)
	return out
}

func cloneRetrievedChunks(chunks []RetrievedChunk) []RetrievedChunk {
	if len(chunks) == 0 {
		return nil
	}
	out := make([]RetrievedChunk, len(chunks))
	copy(out, chunks)
	return out
}
