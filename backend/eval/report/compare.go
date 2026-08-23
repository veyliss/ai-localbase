package report

import "sort"

// MetricComparison contains a single baseline/candidate metric delta.
type MetricComparison struct {
	Name       string
	Baseline   float64
	Candidate  float64
	Delta      float64
	HigherWins bool
}

// CaseRegression identifies a case that lost a hit or direct evidence.
type CaseRegression struct {
	CaseID          string
	BaselineHit     bool
	CandidateHit    bool
	BaselineDirect  bool
	CandidateDirect bool
	Category        string
	Reason          string
}

// ReportComparison is intentionally free of question text and answer content,
// so it can be shared without copying local evaluation data into a report.
type ReportComparison struct {
	BaselineRunID  string
	CandidateRunID string
	Metrics        []MetricComparison
	Regressions    []CaseRegression
	NewHits        []string
}

// Compare reports aggregate deltas and case-level regressions for two runs.
func Compare(baseline, candidate *Report) ReportComparison {
	comparison := ReportComparison{}
	if baseline != nil {
		comparison.BaselineRunID = baseline.RunID
	}
	if candidate != nil {
		comparison.CandidateRunID = candidate.RunID
	}
	if baseline == nil || candidate == nil {
		return comparison
	}

	comparison.Metrics = []MetricComparison{
		metric("hit_rate", baseline.Metrics.HitRate, candidate.Metrics.HitRate, true),
		metric("mrr", baseline.Metrics.MRR, candidate.Metrics.MRR, true),
		metric("direct_evidence_hit_rate", baseline.Metrics.DirectEvidenceHitRate, candidate.Metrics.DirectEvidenceHitRate, true),
		metric("retrieval_latency_p95_ms", baseline.Metrics.RetrievalLatencyP95Ms, candidate.Metrics.RetrievalLatencyP95Ms, false),
		metric("generation_latency_p95_ms", baseline.Metrics.GenerationLatencyP95Ms, candidate.Metrics.GenerationLatencyP95Ms, false),
	}

	baseCases := make(map[string]CaseReport, len(baseline.Cases))
	for _, item := range baseline.Cases {
		baseCases[item.CaseID] = item
	}
	for _, item := range candidate.Cases {
		previous, ok := baseCases[item.CaseID]
		if !ok {
			continue
		}
		if previous.Hit && !item.Hit || previous.DirectEvidenceHit && !item.DirectEvidenceHit {
			comparison.Regressions = append(comparison.Regressions, CaseRegression{
				CaseID:          item.CaseID,
				BaselineHit:     previous.Hit,
				CandidateHit:    item.Hit,
				BaselineDirect:  previous.DirectEvidenceHit,
				CandidateDirect: item.DirectEvidenceHit,
				Category:        item.FailureCategory,
				Reason:          item.FailureReason,
			})
		}
		if !previous.Hit && item.Hit {
			comparison.NewHits = append(comparison.NewHits, item.CaseID)
		}
	}
	sort.Strings(comparison.NewHits)
	sort.Slice(comparison.Regressions, func(i, j int) bool {
		return comparison.Regressions[i].CaseID < comparison.Regressions[j].CaseID
	})
	return comparison
}

func metric(name string, baseline, candidate float64, higherWins bool) MetricComparison {
	return MetricComparison{
		Name:       name,
		Baseline:   baseline,
		Candidate:  candidate,
		Delta:      candidate - baseline,
		HigherWins: higherWins,
	}
}
