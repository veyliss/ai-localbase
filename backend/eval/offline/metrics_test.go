package offline

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIsHit(t *testing.T) {
	// Case 1: Doc-ID hit
	gt1 := GroundTruthCase{
		SourceDocuments: []SourceDocument{
			{DocumentID: "doc-1"},
		},
	}
	result1 := CaseResult{
		RetrievedChunks: []RetrievedChunkInfo{
			{DocumentID: "doc-2"},
			{DocumentID: "doc-1"},
		},
	}
	hit, rank := IsHit(result1, gt1, 0.5)
	assert.True(t, hit)
	assert.Equal(t, 2, rank)

	// Case 2: Text snippet hit
	gt2 := GroundTruthCase{
		AnswerSnippets: []string{"apple", "banana"},
	}
	result2 := CaseResult{
		RetrievedChunks: []RetrievedChunkInfo{
			{Text: "orange juice"},
			{Text: "I like banana and grape"},
		},
	}
	hit, rank = IsHit(result2, gt2, 0.5)
	assert.True(t, hit)
	assert.Equal(t, 2, rank)

	// Case 3: No hit
	gt3 := GroundTruthCase{
		SourceDocuments: []SourceDocument{
			{DocumentID: "doc-3"},
		},
		AnswerSnippets: []string{"cherry"},
	}
	result3 := CaseResult{
		RetrievedChunks: []RetrievedChunkInfo{
			{DocumentID: "doc-4"},
			{Text: "pineapple"},
		},
	}
	hit, rank = IsHit(result3, gt3, 0.5)
	assert.False(t, hit)
	assert.Equal(t, -1, rank)
}

func TestComputeHitRate(t *testing.T) {
	gts := []GroundTruthCase{
		{SourceDocuments: []SourceDocument{{DocumentID: "doc-a"}}},
		{AnswerSnippets: []string{"text-b"}},
		{SourceDocuments: []SourceDocument{{DocumentID: "doc-c"}}},
	}
	results := []CaseResult{
		{RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-a"}}},
		{RetrievedChunks: []RetrievedChunkInfo{{Text: "some text-b content"}}},
		{RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-x"}}},
	}

	hitRate := ComputeHitRate(results, gts, 0.5)
	assert.InDelta(t, 2.0/3.0, hitRate, 0.001)

	// Empty results
	hitRate = ComputeHitRate([]CaseResult{}, []GroundTruthCase{}, 0.5)
	assert.Equal(t, 0.0, hitRate)
}

func TestComputeMRR(t *testing.T) {
	gts := []GroundTruthCase{
		{SourceDocuments: []SourceDocument{{DocumentID: "doc-a"}}},
		{AnswerSnippets: []string{"text-b"}},
		{SourceDocuments: []SourceDocument{{DocumentID: "doc-c"}}},
		{SourceDocuments: []SourceDocument{{DocumentID: "doc-d"}}},
	}
	results := []CaseResult{
		{RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-x"}, {DocumentID: "doc-a"}}},
		{RetrievedChunks: []RetrievedChunkInfo{{Text: "some text-b content"}}},
		{RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-c"}}},
		{RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-y"}}},
	}

	mrr := ComputeMRR(results, gts, 0.5)
	// Case 1: rank 2 -> 1/2
	// Case 2: rank 1 -> 1/1
	// Case 3: rank 1 -> 1/1
	// Case 4: no hit -> 0
	expectedMRR := (0.5 + 1.0 + 1.0 + 0.0) / 4.0
	assert.InDelta(t, expectedMRR, mrr, 0.001)

	// Empty results
	mrr = ComputeMRR([]CaseResult{}, []GroundTruthCase{}, 0.5)
	assert.Equal(t, 0.0, mrr)
}

func TestComputeLatencyPercentiles(t *testing.T) {
	durations := []time.Duration{
		10 * time.Millisecond,
		50 * time.Millisecond,
		20 * time.Millisecond,
		100 * time.Millisecond,
		5 * time.Millisecond,
		70 * time.Millisecond,
		30 * time.Millisecond,
		80 * time.Millisecond,
		60 * time.Millisecond,
		40 * time.Millisecond,
	}

	p50, p95 := ComputeLatencyPercentiles(durations)
	assert.Equal(t, 50*time.Millisecond, p50)
	assert.Equal(t, 80*time.Millisecond, p95)

	// Odd number of elements
	durationsOdd := []time.Duration{
		10 * time.Millisecond,
		50 * time.Millisecond,
		20 * time.Millisecond,
		100 * time.Millisecond,
		5 * time.Millisecond,
		70 * time.Millisecond,
		30 * time.Millisecond,
		80 * time.Millisecond,
		60 * time.Millisecond,
	}
	p50Odd, p95Odd := ComputeLatencyPercentiles(durationsOdd)
	assert.Equal(t, 50*time.Millisecond, p50Odd)
	assert.Equal(t, 80*time.Millisecond, p95Odd)

	// Single element
	durationsSingle := []time.Duration{100 * time.Millisecond}
	p50Single, p95Single := ComputeLatencyPercentiles(durationsSingle)
	assert.Equal(t, 100*time.Millisecond, p50Single)
	assert.Equal(t, 100*time.Millisecond, p95Single)

	// Empty durations
	p50Empty, p95Empty := ComputeLatencyPercentiles([]time.Duration{})
	assert.Equal(t, 0*time.Millisecond, p50Empty)
	assert.Equal(t, 0*time.Millisecond, p95Empty)
}
