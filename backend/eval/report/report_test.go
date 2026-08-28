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

func TestBuildReportSeparatesExpectedNoAnswerCases(t *testing.T) {
	dataset := &offline.Dataset{Cases: []offline.GroundTruthCase{
		{
			ID:             "answerable",
			Question:       "目标答案是什么",
			Answer:         "目标答案",
			AnswerSnippets: []string{"目标答案"},
			AnswerType:     "extractive",
			Difficulty:     "easy",
		},
		{
			ID:         "unanswerable",
			Question:   "资料是否提供了该指标",
			Answer:     "无法确认",
			AnswerType: "no_answer",
			Difficulty: "medium",
		},
	}}
	results := []offline.CaseResult{
		{
			CaseID:          "answerable",
			HitRank:         1,
			RetrievedChunks: []offline.RetrievedChunkInfo{{Text: "目标答案"}},
		},
		{
			CaseID:    "unanswerable",
			LLMAnswer: "无法确认，现有资料没有提供该信息。",
			Error:     "",
		},
	}

	rpt := BuildReport("no-answer", "<memory>", results, dataset, 0.5)
	if rpt.Metrics.AnswerableCases != 1 || rpt.Metrics.NoAnswerCases != 1 || rpt.Metrics.NoAnswerCorrectCases != 1 {
		t.Fatalf("unexpected no-answer metrics: %#v", rpt.Metrics)
	}
	if rpt.Metrics.NoAnswerAccuracy != 1 || rpt.Metrics.HitRate != 1 {
		t.Fatalf("expected correct no-answer handling without lowering hit rate: %#v", rpt.Metrics)
	}
	if !rpt.Cases[1].NoAnswer || !rpt.Cases[1].NoAnswerCorrect {
		t.Fatalf("expected case-level no-answer fields, got %#v", rpt.Cases[1])
	}

	markdownPath := t.TempDir() + "/no-answer.md"
	if err := rpt.WriteMarkdown(markdownPath); err != nil {
		t.Fatalf("write markdown report: %v", err)
	}
	contentBytes, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatalf("read markdown report: %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, "无答案正确率") || !strings.Contains(content, "正确拒答") {
		t.Fatalf("markdown report is missing no-answer details: %s", content)
	}
	if strings.Contains(content, "## 失败用例") {
		t.Fatalf("a correctly handled no-answer case must not appear as a failed case: %s", content)
	}
}
