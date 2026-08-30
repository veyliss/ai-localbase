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

func TestIsHitRequiresExactChunkWhenGroundTruthProvidesChunkID(t *testing.T) {
	gt := GroundTruthCase{
		SourceDocuments: []SourceDocument{{
			KnowledgeBaseID: "kb-1",
			DocumentID:      "doc-1",
			ChunkID:         "chunk-target",
		}},
	}

	wrongOnly, rank := IsHit(CaseResult{RetrievedChunks: []RetrievedChunkInfo{
		{KnowledgeBaseID: "kb-1", DocumentID: "doc-1", ChunkID: "chunk-other"},
	}}, gt, 0.5)
	assert.False(t, wrongOnly)
	assert.Equal(t, -1, rank)

	matched, rank := IsHit(CaseResult{RetrievedChunks: []RetrievedChunkInfo{
		{KnowledgeBaseID: "kb-1", DocumentID: "doc-1", ChunkID: "chunk-other"},
		{KnowledgeBaseID: "kb-1", DocumentID: "doc-1", ChunkID: "chunk-target"},
	}}, gt, 0.5)
	assert.True(t, matched)
	assert.Equal(t, 2, rank)

	wrongKnowledgeBase, rank := IsHit(CaseResult{RetrievedChunks: []RetrievedChunkInfo{
		{KnowledgeBaseID: "kb-2", DocumentID: "doc-1", ChunkID: "chunk-target"},
	}}, gt, 0.5)
	assert.False(t, wrongKnowledgeBase)
	assert.Equal(t, -1, rank)
}

func TestClassifyHitSeparatesDocumentHitFromDirectEvidence(t *testing.T) {
	gt := GroundTruthCase{SourceDocuments: []SourceDocument{{
		KnowledgeBaseID: "kb-1",
		DocumentID:      "doc-1",
		ChunkID:         "chunk-target",
	}}}
	classification := ClassifyHit(CaseResult{RetrievedChunks: []RetrievedChunkInfo{{
		KnowledgeBaseID: "kb-1",
		DocumentID:      "doc-1",
		ChunkID:         "chunk-other",
	}}}, gt, 0.5)

	if !classification.DocumentHit {
		t.Fatal("expected document hit")
	}
	if classification.ChunkHit || classification.DirectEvidenceHit || classification.Hit {
		t.Fatalf("expected document-only hit without direct evidence, got %#v", classification)
	}
}

func TestIsHitUsesThresholdForPartialAnswerSnippet(t *testing.T) {
	gt := GroundTruthCase{AnswerSnippets: []string{"示例机构成立于1900年"}}
	result := CaseResult{RetrievedChunks: []RetrievedChunkInfo{{Text: "示例机构成立于1898年，位于示例城市。"}}}

	hit, _ := IsHit(result, gt, 0.4)
	if !hit {
		t.Fatal("expected partial snippet to pass the lower threshold")
	}
	hit, _ = IsHit(result, gt, 1)
	if hit {
		t.Fatal("expected partial snippet to fail the exact threshold")
	}
}

func TestSnippetMatchDoesNotJoinSeparatedASCIITerms(t *testing.T) {
	text := "Qdrant 的 payload 可以存储 JSON。来源：https://qdrant.tech/documentation/manage-data/indexing/index.md"
	if score := snippetMatchScore(text, "payload index"); score >= 0.5 {
		t.Fatalf("expected separated payload and index terms not to match, got %.3f", score)
	}
	if score := snippetMatchScore("Qdrant 的 payload index 用于加快点查询。", "payload index"); score != 1 {
		t.Fatalf("expected contiguous API phrase to match exactly, got %.3f", score)
	}
}

func TestComputeMetricsMatchGroundTruthByCaseID(t *testing.T) {
	groundTruth := []GroundTruthCase{
		{ID: "case-a", SourceDocuments: []SourceDocument{{DocumentID: "doc-a"}}},
		{ID: "case-b", SourceDocuments: []SourceDocument{{DocumentID: "doc-b"}}},
	}
	results := []CaseResult{
		{CaseID: "case-b", RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-b"}}},
		{CaseID: "case-a", RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-a"}}},
	}

	if got := ComputeHitRate(results, groundTruth, 0.5); got != 1 {
		t.Fatalf("expected ID-aligned hit rate 1, got %v", got)
	}
	if got := ComputeMRR(results, groundTruth, 0.5); got != 1 {
		t.Fatalf("expected ID-aligned MRR 1, got %v", got)
	}
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

func TestClassifyFailureDistinguishesRecallRankAndCitation(t *testing.T) {
	gt := GroundTruthCase{
		SourceDocuments: []SourceDocument{{DocumentID: "doc-1", ChunkID: "chunk-1"}},
		AnswerSnippets:  []string{"目标答案"},
	}

	recall := ClassifyFailure(CaseResult{RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-2"}}}, gt, 0.5)
	assert.Equal(t, FailureCategoryRecallMiss, recall.Category)

	rank := ClassifyFailure(CaseResult{RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-1", ChunkID: "chunk-2"}}}, gt, 0.5)
	assert.Equal(t, FailureCategoryRankMiss, rank.Category)

	citation := ClassifyFailure(CaseResult{RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-1", ChunkID: "chunk-2", Text: "目标答案"}}}, gt, 0.5)
	assert.Equal(t, FailureCategoryCitationMismatch, citation.Category)
}

func TestNoAnswerCasesAreSeparatedFromRetrievalMetrics(t *testing.T) {
	groundTruth := []GroundTruthCase{
		{
			ID:             "answerable",
			AnswerSnippets: []string{"目标答案"},
			AnswerType:     "extractive",
		},
		{
			ID:         "unanswerable",
			Answer:     "无法确认",
			AnswerType: "no_answer",
		},
	}
	results := []CaseResult{
		{CaseID: "answerable", RetrievedChunks: []RetrievedChunkInfo{{Text: "包含目标答案的片段"}}},
		{CaseID: "unanswerable"},
	}

	metrics := Aggregate(results, groundTruth, 0.5)
	if metrics.TotalCases != 2 || metrics.AnswerableCases != 1 || metrics.NoAnswerCases != 1 {
		t.Fatalf("unexpected case counts: %#v", metrics)
	}
	if metrics.NoAnswerCorrectCases != 1 || metrics.NoAnswerAccuracy != 1 {
		t.Fatalf("expected one correctly handled no-answer case: %#v", metrics)
	}
	if metrics.HitRate != 1 || metrics.MRR != 1 || metrics.DirectEvidenceHitRate != 1 {
		t.Fatalf("no-answer case should not lower answerable retrieval metrics: %#v", metrics)
	}

	classification := ClassifyFailure(results[1], groundTruth[1], 0.5)
	if classification.Category != FailureCategoryNoAnswerConfirmed {
		t.Fatalf("expected confirmed no-answer category, got %#v", classification)
	}
}

func TestNoAnswerPolicyMissRemainsFailure(t *testing.T) {
	groundTruth := GroundTruthCase{
		ID:         "unanswerable",
		Answer:     "无法确认",
		AnswerType: "no_answer",
		SourceDocuments: []SourceDocument{{
			DocumentID: "doc-1",
		}},
	}
	result := CaseResult{RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-1"}}}

	classification := ClassifyFailure(result, groundTruth, 0.5)
	if classification.Category != FailureCategoryNoAnswerPolicy {
		t.Fatalf("expected no-answer policy failure, got %#v", classification)
	}
	if IsNoAnswerCorrect(result, groundTruth, 0.5) {
		t.Fatal("a positive hit must not count as a correct no-answer result")
	}
}

func TestEnabledCasesKeepsLegacyAndApprovedCases(t *testing.T) {
	dataset := (&Dataset{Cases: []GroundTruthCase{
		{ID: "legacy"},
		{ID: "approved", ReviewStatus: "approved"},
		{ID: "disabled", Disabled: true},
		{ID: "rejected", ReviewStatus: "rejected"},
	}}).EnabledCases()

	assert.Equal(t, []string{"legacy", "approved"}, []string{dataset.Cases[0].ID, dataset.Cases[1].ID})
}
