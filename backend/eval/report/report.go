package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ai-localbase/eval/offline"
)

// Report 完整评估报告
type Report struct {
	RunID       string       `json:"run_id"`
	RunAt       time.Time    `json:"run_at"`
	DatasetPath string       `json:"dataset_path"`
	Metrics     Metrics      `json:"metrics"`
	Cases       []CaseReport `json:"cases"`
}

// Metrics 报告中的聚合指标
type Metrics struct {
	TotalCases                 int     `json:"total_cases"`
	HitRate                    float64 `json:"hit_rate"`
	DocumentHitRate            float64 `json:"document_hit_rate"`
	ChunkHitRate               float64 `json:"chunk_hit_rate"`
	AnswerSnippetHitRate       float64 `json:"answer_snippet_hit_rate"`
	DirectEvidenceHitRate      float64 `json:"direct_evidence_hit_rate"`
	FaithfulnessScore          float64 `json:"faithfulness_score"`
	HallucinationRate          float64 `json:"hallucination_rate"`
	UnsupportedClaimRate       float64 `json:"unsupported_claim_rate"`
	FaithfulnessEvaluatedCases int     `json:"faithfulness_evaluated_cases"`
	UnsupportedAnswerCases     int     `json:"unsupported_answer_cases"`
	MRR                        float64 `json:"mrr"`
	RetrievalLatencyP50Ms      float64 `json:"retrieval_latency_p50_ms"`
	RetrievalLatencyP95Ms      float64 `json:"retrieval_latency_p95_ms"`
	GenerationLatencyP50Ms     float64 `json:"generation_latency_p50_ms"`
	GenerationLatencyP95Ms     float64 `json:"generation_latency_p95_ms"`
}

// CaseReport 单个用例在报告中的摘要
type CaseReport struct {
	CaseID                string  `json:"case_id"`
	Question              string  `json:"question"`
	Hit                   bool    `json:"hit"`
	HitRank               int     `json:"hit_rank"`
	DocumentHit           bool    `json:"document_hit"`
	ChunkHit              bool    `json:"chunk_hit"`
	AnswerSnippetHit      bool    `json:"answer_snippet_hit"`
	DirectEvidenceHit     bool    `json:"direct_evidence_hit"`
	FaithfulnessScore     float64 `json:"faithfulness_score"`
	AnswerClaimCount      int     `json:"answer_claim_count"`
	SupportedClaimCount   int     `json:"supported_claim_count"`
	UnsupportedClaimCount int     `json:"unsupported_claim_count"`
	FaithfulnessEvaluated bool    `json:"faithfulness_evaluated"`
	UnsupportedAnswer     bool    `json:"unsupported_answer"`
	FailureCategory       string  `json:"failure_category,omitempty"`
	FailureReason         string  `json:"failure_reason,omitempty"`
	LLMAnswer             string  `json:"llm_answer,omitempty"`
	Error                 string  `json:"error,omitempty"`
}

// BuildReport 从 CaseResult 列表和 Dataset 构建报告
func BuildReport(runID string, datasetPath string, results []offline.CaseResult, dataset *offline.Dataset, hitThreshold float64) *Report {
	aggMetrics := offline.Aggregate(results, dataset.Cases, hitThreshold)

	caseReports := make([]CaseReport, len(results))
	for i, res := range results {
		hit := res.HitRank != -1
		faithfulness := offline.AnalyzeCaseFaithfulness(res)
		caseReports[i] = CaseReport{
			CaseID:                res.CaseID,
			Question:              res.Question,
			Hit:                   hit,
			HitRank:               res.HitRank,
			DocumentHit:           res.DocumentHit,
			ChunkHit:              res.ChunkHit,
			AnswerSnippetHit:      res.AnswerSnippetHit,
			DirectEvidenceHit:     res.DirectEvidenceHit,
			FaithfulnessScore:     faithfulness.Score,
			AnswerClaimCount:      faithfulness.ClaimCount,
			SupportedClaimCount:   faithfulness.SupportedClaimCount,
			UnsupportedClaimCount: faithfulness.UnsupportedClaimCount,
			FaithfulnessEvaluated: faithfulness.Evaluated,
			UnsupportedAnswer:     faithfulness.UnsupportedClaimCount > 0,
			FailureCategory:       res.FailureCategory,
			FailureReason:         res.FailureReason,
			LLMAnswer:             res.LLMAnswer,
			Error:                 res.Error,
		}
	}

	return &Report{
		RunID:       runID,
		RunAt:       time.Now(),
		DatasetPath: datasetPath,
		Metrics: Metrics{
			TotalCases:                 aggMetrics.TotalCases,
			HitRate:                    aggMetrics.HitRate,
			DocumentHitRate:            aggMetrics.DocumentHitRate,
			ChunkHitRate:               aggMetrics.ChunkHitRate,
			AnswerSnippetHitRate:       aggMetrics.AnswerSnippetHitRate,
			DirectEvidenceHitRate:      aggMetrics.DirectEvidenceHitRate,
			FaithfulnessScore:          aggMetrics.FaithfulnessScore,
			HallucinationRate:          aggMetrics.HallucinationRate,
			UnsupportedClaimRate:       aggMetrics.UnsupportedClaimRate,
			FaithfulnessEvaluatedCases: aggMetrics.FaithfulnessEvaluatedCases,
			UnsupportedAnswerCases:     aggMetrics.UnsupportedAnswerCases,
			MRR:                        aggMetrics.MRR,
			RetrievalLatencyP50Ms:      float64(aggMetrics.LatencyRetrievalP50.Milliseconds()),
			RetrievalLatencyP95Ms:      float64(aggMetrics.LatencyRetrievalP95.Milliseconds()),
			GenerationLatencyP50Ms:     float64(aggMetrics.LatencyGenerationP50.Milliseconds()),
			GenerationLatencyP95Ms:     float64(aggMetrics.LatencyGenerationP95.Milliseconds()),
		},
		Cases: caseReports,
	}
}

// LoadJSON loads a previously generated report for local comparison.
func LoadJSON(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read report %s: %w", path, err)
	}

	var result Report
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to decode report %s: %w", path, err)
	}
	return &result, nil
}

// WriteJSON 将报告写入 JSON 文件
func (r *Report) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal report to JSON: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", dir, err)
	}

	return os.WriteFile(path, data, 0644)
}

// WriteMarkdown 将报告写入 Markdown 文件
func (r *Report) WriteMarkdown(path string) error {
	var md strings.Builder
	md.WriteString("# RAG 评估报告\n")
	md.WriteString(fmt.Sprintf("运行时间: %s\n", r.RunAt.Format("2006-01-02 15:04:05")))
	md.WriteString(fmt.Sprintf("数据集: %s\n\n", r.DatasetPath))

	md.WriteString("## 聚合指标\n")
	md.WriteString("| 指标 | 值 |\n")
	md.WriteString("|------|----| \n")
	md.WriteString(fmt.Sprintf("| 总用例数 | %d |\n", r.Metrics.TotalCases))
	md.WriteString(fmt.Sprintf("| 命中率 (Hit Rate) | %.2f%% |\n", r.Metrics.HitRate*100))
	md.WriteString(fmt.Sprintf("| 文档命中率 | %.2f%% |\n", r.Metrics.DocumentHitRate*100))
	md.WriteString(fmt.Sprintf("| Chunk 命中率 | %.2f%% |\n", r.Metrics.ChunkHitRate*100))
	md.WriteString(fmt.Sprintf("| 答案片段命中率 | %.2f%% |\n", r.Metrics.AnswerSnippetHitRate*100))
	md.WriteString(fmt.Sprintf("| 直接证据命中率 | %.2f%% |\n", r.Metrics.DirectEvidenceHitRate*100))
	md.WriteString(fmt.Sprintf("| Faithfulness 证据忠实度 | %.2f%% |\n", r.Metrics.FaithfulnessScore*100))
	md.WriteString(fmt.Sprintf("| 未支撑答案率（启发式） | %.2f%% |\n", r.Metrics.HallucinationRate*100))
	md.WriteString(fmt.Sprintf("| 未支撑陈述率（启发式） | %.2f%% |\n", r.Metrics.UnsupportedClaimRate*100))
	md.WriteString(fmt.Sprintf("| 可评估答案数 | %d |\n", r.Metrics.FaithfulnessEvaluatedCases))
	md.WriteString(fmt.Sprintf("| MRR | %.4f |\n", r.Metrics.MRR))
	md.WriteString(fmt.Sprintf("| 检索时延 P50 | %.0fms |\n", r.Metrics.RetrievalLatencyP50Ms))
	md.WriteString(fmt.Sprintf("| 检索时延 P95 | %.0fms |\n", r.Metrics.RetrievalLatencyP95Ms))
	md.WriteString(fmt.Sprintf("| 生成时延 P50 | %.0fms |\n", r.Metrics.GenerationLatencyP50Ms))
	md.WriteString(fmt.Sprintf("| 生成时延 P95 | %.0fms |\n\n", r.Metrics.GenerationLatencyP95Ms))

	failedCases := make([]CaseReport, 0)
	for _, c := range r.Cases {
		if !c.Hit || c.Error != "" || c.UnsupportedAnswer {
			failedCases = append(failedCases, c)
		}
	}

	if len(failedCases) > 0 {
		md.WriteString("## 失败用例\n")
		md.WriteString("| ID | 问题 | 分类 | 原因 | 未支撑陈述 | 错误 |\n")
		md.WriteString("|----|----|------|------|------------|-----|\n")
		for _, c := range failedCases {
			errorMsg := c.Error
			if !c.Hit && c.Error == "" {
				errorMsg = "未命中"
			}
			md.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %d | %s |\n", c.CaseID, c.Question, c.FailureCategory, c.FailureReason, c.UnsupportedClaimCount, errorMsg))
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", dir, err)
	}

	return os.WriteFile(path, []byte(md.String()), 0644)
}
