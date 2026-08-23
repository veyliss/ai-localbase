package report

import "testing"

func TestRecommendStrategyRejectsQualityRegression(t *testing.T) {
	baseline := &Report{
		RunID: "baseline",
		Metrics: Metrics{
			TotalCases: 10, HitRate: 0.8, MRR: 0.7, FaithfulnessScore: 0.9,
			DirectEvidenceHitRate: 0.8, RetrievalLatencyP95Ms: 100,
		},
	}
	decision := RecommendStrategy(baseline, []StrategyCandidate{
		{Name: "hybrid", Report: &Report{RunID: "hybrid", Metrics: Metrics{
			TotalCases: 10, HitRate: 0.8, MRR: 0.72, FaithfulnessScore: 0.92,
			DirectEvidenceHitRate: 0.82, RetrievalLatencyP95Ms: 110,
		}}},
		{Name: "semantic", Report: &Report{RunID: "semantic", Metrics: Metrics{
			TotalCases: 10, HitRate: 0.7, MRR: 0.8, FaithfulnessScore: 0.95,
			DirectEvidenceHitRate: 0.8, RetrievalLatencyP95Ms: 110,
		}}},
	}, DefaultStrategyPolicy())

	if !decision.Approved || decision.RecommendedStrategy != "hybrid" {
		t.Fatalf("expected hybrid recommendation, got %#v", decision)
	}
	if len(decision.Evaluations) != 2 || decision.Evaluations[1].Approved {
		t.Fatalf("expected semantic quality regression, got %#v", decision.Evaluations)
	}
}

func TestRecommendStrategyFallsBackToBaselineWhenP95Regresses(t *testing.T) {
	baseline := &Report{RunID: "baseline", Metrics: Metrics{
		TotalCases: 2, HitRate: 1, MRR: 1, FaithfulnessScore: 1,
		DirectEvidenceHitRate: 1, RetrievalLatencyP95Ms: 100,
	}}
	decision := RecommendStrategy(baseline, []StrategyCandidate{{Name: "slow", Report: &Report{
		Metrics: Metrics{TotalCases: 2, HitRate: 1, MRR: 1, FaithfulnessScore: 1,
			DirectEvidenceHitRate: 1, RetrievalLatencyP95Ms: 131},
	}}}, DefaultStrategyPolicy())

	if decision.Approved || decision.RecommendedStrategy != "baseline" {
		t.Fatalf("expected baseline fallback, got %#v", decision)
	}
	if len(decision.Evaluations) != 1 || len(decision.Evaluations[0].Reasons) == 0 {
		t.Fatalf("expected p95 rejection reason, got %#v", decision.Evaluations)
	}
}
