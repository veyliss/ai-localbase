package report

import "testing"

func TestCompareReportsFindsMetricAndCaseRegression(t *testing.T) {
	baseline := &Report{
		RunID:   "baseline",
		Metrics: Metrics{HitRate: 0.8, MRR: 0.7, DirectEvidenceHitRate: 0.75, RetrievalLatencyP95Ms: 100},
		Cases:   []CaseReport{{CaseID: "case-1", Hit: true, DirectEvidenceHit: true}},
	}
	candidate := &Report{
		RunID:   "candidate",
		Metrics: Metrics{HitRate: 0.7, MRR: 0.6, DirectEvidenceHitRate: 0.7, RetrievalLatencyP95Ms: 120},
		Cases:   []CaseReport{{CaseID: "case-1", Hit: false, DirectEvidenceHit: false, FailureCategory: "recall_miss"}},
	}

	comparison := Compare(baseline, candidate)
	if len(comparison.Metrics) != 5 {
		t.Fatalf("expected five metrics, got %d", len(comparison.Metrics))
	}
	if len(comparison.Regressions) != 1 || comparison.Regressions[0].CaseID != "case-1" {
		t.Fatalf("expected case regression, got %#v", comparison.Regressions)
	}
	if comparison.Metrics[0].Delta >= 0 {
		t.Fatalf("expected hit rate regression, got %#v", comparison.Metrics[0])
	}
}
