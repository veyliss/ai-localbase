package report

import (
	"fmt"
	"math"
	"strings"
)

// StrategyCandidate associates a local evaluation report with the strategy
// that produced it. Reports are loaded by the caller, so this package never
// discovers or reads evaluation data implicitly.
type StrategyCandidate struct {
	Name   string
	Report *Report
}

// StrategyPolicy defines the regression gates for selecting a default strategy.
// The zero values for quality deltas are intentional: a candidate must not
// regress quality unless the caller explicitly relaxes a gate.
type StrategyPolicy struct {
	MaxHitRateDrop              float64
	MaxMRRDrop                  float64
	MaxFaithfulnessDrop         float64
	MaxDirectEvidenceDrop       float64
	MaxHallucinationIncrease    float64
	MaxUnsupportedClaimIncrease float64
	MaxP95Ratio                 float64
	RequireSameCaseCount        bool
}

// DefaultStrategyPolicy matches the current baseline-workbench acceptance
// rules: preserve quality and allow at most 30% P95 latency growth.
func DefaultStrategyPolicy() StrategyPolicy {
	return StrategyPolicy{
		MaxP95Ratio:          1.30,
		RequireSameCaseCount: true,
	}
}

// StrategyEvaluation records why one candidate passed or failed the gates.
type StrategyEvaluation struct {
	Name     string
	Approved bool
	Score    float64
	Reasons  []string
}

// StrategyDecision is the local, explainable default-strategy recommendation.
type StrategyDecision struct {
	Approved            bool
	RecommendedStrategy string
	Reasons             []string
	Evaluations         []StrategyEvaluation
}

// RecommendStrategy evaluates all candidates against one baseline and returns
// the best approved candidate. If none passes, the baseline remains the safe
// recommendation and Approved is false.
func RecommendStrategy(baseline *Report, candidates []StrategyCandidate, policy StrategyPolicy) StrategyDecision {
	decision := StrategyDecision{}
	if baseline == nil {
		decision.Reasons = []string{"缺少 baseline 报告，无法判断策略是否回归"}
		return decision
	}
	if policy.MaxP95Ratio <= 0 {
		policy.MaxP95Ratio = 1.30
	}

	bestIndex := -1
	for _, candidate := range candidates {
		name := strings.TrimSpace(candidate.Name)
		if name == "" && candidate.Report != nil {
			name = strings.TrimSpace(candidate.Report.RunID)
		}
		if name == "" {
			name = "未命名策略"
		}
		evaluation := evaluateStrategyCandidate(name, baseline, candidate.Report, policy)
		decision.Evaluations = append(decision.Evaluations, evaluation)
		if !evaluation.Approved {
			continue
		}
		currentIndex := len(decision.Evaluations) - 1
		if bestIndex == -1 || strategyEvaluationBetter(evaluation, decision.Evaluations[bestIndex]) {
			bestIndex = currentIndex
		}
	}

	if bestIndex == -1 {
		decision.RecommendedStrategy = strings.TrimSpace(baseline.RunID)
		decision.Reasons = []string{"没有候选策略通过质量、证据和延迟门槛，继续使用 baseline"}
		return decision
	}
	decision.Approved = true
	decision.RecommendedStrategy = decision.Evaluations[bestIndex].Name
	decision.Reasons = append([]string(nil), decision.Evaluations[bestIndex].Reasons...)
	return decision
}

func evaluateStrategyCandidate(name string, baseline, candidate *Report, policy StrategyPolicy) StrategyEvaluation {
	evaluation := StrategyEvaluation{Name: name}
	if candidate == nil {
		evaluation.Reasons = []string{"报告不存在或无法加载"}
		return evaluation
	}

	baseMetrics := baseline.Metrics
	candidateMetrics := candidate.Metrics
	if policy.RequireSameCaseCount && baseMetrics.TotalCases != candidateMetrics.TotalCases {
		evaluation.Reasons = append(evaluation.Reasons, fmt.Sprintf("样本数不一致：baseline=%d，候选=%d", baseMetrics.TotalCases, candidateMetrics.TotalCases))
	}
	if candidateMetrics.HitRate < baseMetrics.HitRate-policy.MaxHitRateDrop {
		evaluation.Reasons = append(evaluation.Reasons, "Hit Rate 低于 baseline")
	}
	if candidateMetrics.MRR < baseMetrics.MRR-policy.MaxMRRDrop {
		evaluation.Reasons = append(evaluation.Reasons, "MRR 低于 baseline")
	}
	if candidateMetrics.FaithfulnessScore < baseMetrics.FaithfulnessScore-policy.MaxFaithfulnessDrop {
		evaluation.Reasons = append(evaluation.Reasons, "Faithfulness 低于 baseline")
	}
	if candidateMetrics.DirectEvidenceHitRate < baseMetrics.DirectEvidenceHitRate-policy.MaxDirectEvidenceDrop {
		evaluation.Reasons = append(evaluation.Reasons, "直接证据命中率低于 baseline")
	}
	if candidateMetrics.HallucinationRate > baseMetrics.HallucinationRate+policy.MaxHallucinationIncrease {
		evaluation.Reasons = append(evaluation.Reasons, "未支撑答案率高于 baseline")
	}
	if candidateMetrics.UnsupportedClaimRate > baseMetrics.UnsupportedClaimRate+policy.MaxUnsupportedClaimIncrease {
		evaluation.Reasons = append(evaluation.Reasons, "未支撑陈述率高于 baseline")
	}
	if baseMetrics.RetrievalLatencyP95Ms > 0 && candidateMetrics.RetrievalLatencyP95Ms > baseMetrics.RetrievalLatencyP95Ms*policy.MaxP95Ratio {
		evaluation.Reasons = append(evaluation.Reasons, "检索 P95 超过 baseline 的允许比例")
	}
	if baseMetrics.GenerationLatencyP95Ms > 0 && candidateMetrics.GenerationLatencyP95Ms > baseMetrics.GenerationLatencyP95Ms*policy.MaxP95Ratio {
		evaluation.Reasons = append(evaluation.Reasons, "生成 P95 超过 baseline 的允许比例")
	}
	if reportErrorCount(candidate) > reportErrorCount(baseline) {
		evaluation.Reasons = append(evaluation.Reasons, "运行错误数高于 baseline")
	}

	evaluation.Approved = len(evaluation.Reasons) == 0
	evaluation.Score = strategyScore(candidateMetrics)
	if evaluation.Approved {
		evaluation.Reasons = []string{"通过质量、证据、错误和 P95 延迟门槛"}
	}
	return evaluation
}

func strategyEvaluationBetter(candidate, current StrategyEvaluation) bool {
	if math.Abs(candidate.Score-current.Score) > 0.000001 {
		return candidate.Score > current.Score
	}
	return candidate.Name < current.Name
}

func strategyScore(metrics Metrics) float64 {
	latencyScore := 0.0
	if metrics.RetrievalLatencyP95Ms > 0 {
		latencyScore = 1 / (1 + metrics.RetrievalLatencyP95Ms/1000)
	}
	return metrics.HitRate*0.35 +
		metrics.MRR*0.20 +
		metrics.FaithfulnessScore*0.25 +
		metrics.DirectEvidenceHitRate*0.15 +
		latencyScore*0.05
}

func reportErrorCount(value *Report) int {
	if value == nil {
		return 0
	}
	count := 0
	for _, item := range value.Cases {
		if strings.TrimSpace(item.Error) != "" {
			count++
		}
	}
	return count
}
