package offline

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestEvaluatorUsesConfiguredConcurrencyAndPreservesOrder(t *testing.T) {
	cases := make([]GroundTruthCase, 8)
	for i := range cases {
		cases[i] = GroundTruthCase{
			ID:             fmt.Sprintf("case-%d", i),
			Question:       fmt.Sprintf("问题-%d", i),
			Answer:         fmt.Sprintf("证据-%d", i),
			AnswerSnippets: []string{fmt.Sprintf("证据-%d", i)},
			AnswerType:     "extractive",
			Difficulty:     "easy",
		}
	}

	var active int32
	var maxActive int32
	retrieval := func(ctx context.Context, question string) ([]RetrievedChunkInfo, time.Duration, error) {
		current := atomic.AddInt32(&active, 1)
		for {
			previous := atomic.LoadInt32(&maxActive)
			if current <= previous || atomic.CompareAndSwapInt32(&maxActive, previous, current) {
				break
			}
		}
		defer atomic.AddInt32(&active, -1)
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(15 * time.Millisecond):
		}
		return []RetrievedChunkInfo{{Text: "证据-" + question[len("问题-"):]}}, 15 * time.Millisecond, nil
	}
	generation := func(ctx context.Context, question string, chunks []RetrievedChunkInfo) (string, time.Duration, error) {
		return chunks[0].Text, time.Millisecond, nil
	}

	evaluator := NewEvaluator(retrieval, generation, EvaluatorConfig{HitThreshold: 0.5, MaxConcurrency: 3})
	results, err := evaluator.Run(context.Background(), &Dataset{Cases: cases})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if maxActive < 2 || maxActive > 3 {
		t.Fatalf("expected 2-3 concurrent retrievals, got %d", maxActive)
	}
	if len(results) != len(cases) {
		t.Fatalf("expected %d results, got %d", len(cases), len(results))
	}
	for i, result := range results {
		if result.CaseID != cases[i].ID {
			t.Fatalf("results lost input order at %d: got %q", i, result.CaseID)
		}
		if result.Error != "" {
			t.Fatalf("case %s failed: %s", result.CaseID, result.Error)
		}
	}
}

func TestEvaluatorTreatsExpectedNoAnswerAsCorrect(t *testing.T) {
	answer := "无法确认，现有资料没有提供该信息。"
	retrieval := func(ctx context.Context, question string) ([]RetrievedChunkInfo, time.Duration, error) {
		return nil, time.Millisecond, nil
	}
	generation := func(ctx context.Context, question string, chunks []RetrievedChunkInfo) (string, time.Duration, error) {
		return answer, time.Millisecond, nil
	}

	evaluator := NewEvaluator(retrieval, generation, EvaluatorConfig{})
	results, err := evaluator.Run(context.Background(), &Dataset{Cases: []GroundTruthCase{{
		ID:         "unanswerable",
		Question:   "资料是否提供了该指标？",
		Answer:     answer,
		AnswerType: "no_answer",
		Difficulty: "medium",
	}}})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].Error != "" {
		t.Fatalf("expected no runtime or miss error for a correct no-answer case, got %q", results[0].Error)
	}
	if results[0].FailureCategory != FailureCategoryNoAnswerConfirmed {
		t.Fatalf("expected confirmed no-answer category, got %q", results[0].FailureCategory)
	}
}
