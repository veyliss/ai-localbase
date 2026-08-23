package report

import (
	"os"
	"strings"
	"testing"

	"ai-localbase/eval/offline"
)

func TestBuildReportIncludesFaithfulnessMetricsWithoutExternalData(t *testing.T) {
	dataset := &offline.Dataset{Cases: []offline.GroundTruthCase{
		{
			ID:       "case-supported",
			Question: "负责人是谁",
			Answer:   "成员甲",
		},
		{
			ID:       "case-unsupported",
			Question: "成立年份是什么",
			Answer:   "1900年",
		},
	}}
	results := []offline.CaseResult{
		{
			CaseID:          "case-supported",
			LLMAnswer:       "成员甲是负责人。",
			RetrievedChunks: []offline.RetrievedChunkInfo{{Text: "成员甲是负责人。"}},
		},
		{
			CaseID:          "case-unsupported",
			LLMAnswer:       "机构成立于1900年。",
			RetrievedChunks: []offline.RetrievedChunkInfo{{Text: "机构成立于1898年。"}},
		},
	}

	rpt := BuildReport("synthetic", "<memory>", results, dataset, 0.5)
	if rpt.Metrics.FaithfulnessScore != 0.5 {
		t.Fatalf("expected 50%% faithfulness, got %#v", rpt.Metrics)
	}
	if rpt.Metrics.HallucinationRate != 0.5 || rpt.Metrics.UnsupportedClaimRate != 0.5 {
		t.Fatalf("expected unsupported rates to be 50%%, got %#v", rpt.Metrics)
	}
	if rpt.Metrics.FaithfulnessEvaluatedCases != 2 || rpt.Metrics.UnsupportedAnswerCases != 1 {
		t.Fatalf("unexpected evaluated case counts: %#v", rpt.Metrics)
	}
	if !rpt.Cases[1].UnsupportedAnswer || rpt.Cases[1].UnsupportedClaimCount != 1 {
		t.Fatalf("expected case-level unsupported answer details, got %#v", rpt.Cases[1])
	}

	markdownPath := t.TempDir() + "/synthetic.md"
	if err := rpt.WriteMarkdown(markdownPath); err != nil {
		t.Fatalf("write markdown report: %v", err)
	}
	contentBytes, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatalf("read synthetic markdown report: %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, "Faithfulness 证据忠实度") || !strings.Contains(content, "未支撑答案率") {
		t.Fatalf("markdown report is missing faithfulness metrics: %s", content)
	}
}
