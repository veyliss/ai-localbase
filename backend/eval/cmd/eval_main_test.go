package main

import (
	"strings"
	"testing"
	"time"

	"ai-localbase/eval/offline"
	"ai-localbase/internal/model"
)

func TestBuildEvalOverrides(t *testing.T) {
	overrides, err := buildEvalOverrides(evalOverridesInput{
		knowledgeBaseID:                " kb-eval ",
		retrievalTopKDocument:          7,
		retrievalCandidateTopKDocument: 14,
		retrievalTopKKnowledgeBase:     11,
		retrievalCandidateTopKAllDocs:  40,
		retrievalMaxChunksPerDocument:  3,
		retrievalMaxContextChars:       3200,
		retrievalAutoExpand:            "true",
		retrievalSearchMode:            "hybrid",
		retrievalRerankStrategy:        "semantic",
		retrievalQueryRewrite:          "false",
		retrievalQueryRewriteVariants:  4,
		evalEmbeddingBaseURL:           " http://localhost:11434 ",
		evalChatBaseURL:                " http://localhost:11435 ",
		evalPathMap:                    "/app=.",
		evalAllowMissingSources:        true,
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if overrides.knowledgeBaseID != "kb-eval" {
		t.Fatalf("expected trimmed knowledge base id, got %q", overrides.knowledgeBaseID)
	}
	if overrides.retrievalTopKDocument != 7 {
		t.Fatalf("expected retrievalTopKDocument 7, got %d", overrides.retrievalTopKDocument)
	}
	if overrides.retrievalCandidateTopKDocument != 14 {
		t.Fatalf("expected retrievalCandidateTopKDocument 14, got %d", overrides.retrievalCandidateTopKDocument)
	}
	if overrides.retrievalTopKKnowledgeBase != 11 {
		t.Fatalf("expected retrievalTopKKnowledgeBase 11, got %d", overrides.retrievalTopKKnowledgeBase)
	}
	if overrides.retrievalCandidateTopKAllDocs != 40 {
		t.Fatalf("expected retrievalCandidateTopKAllDocs 40, got %d", overrides.retrievalCandidateTopKAllDocs)
	}
	if overrides.retrievalMaxChunksPerDocument != 3 {
		t.Fatalf("expected retrievalMaxChunksPerDocument 3, got %d", overrides.retrievalMaxChunksPerDocument)
	}
	if overrides.retrievalMaxContextChars != 3200 {
		t.Fatalf("expected retrievalMaxContextChars 3200, got %d", overrides.retrievalMaxContextChars)
	}
	if overrides.retrievalAutoExpand == nil || !*overrides.retrievalAutoExpand {
		t.Fatal("expected retrievalAutoExpand to be true")
	}
	if overrides.retrievalSearchMode != "hybrid" {
		t.Fatalf("expected retrievalSearchMode hybrid, got %q", overrides.retrievalSearchMode)
	}
	if overrides.retrievalRerankStrategy != "semantic" {
		t.Fatalf("expected retrievalRerankStrategy semantic, got %q", overrides.retrievalRerankStrategy)
	}
	if overrides.retrievalQueryRewrite == nil || *overrides.retrievalQueryRewrite {
		t.Fatal("expected retrievalQueryRewrite to be false")
	}
	if overrides.retrievalQueryRewriteVariants != 4 {
		t.Fatalf("expected retrievalQueryRewriteVariants 4, got %d", overrides.retrievalQueryRewriteVariants)
	}
	if overrides.evalEmbeddingBaseURL != "http://localhost:11434" {
		t.Fatalf("expected trimmed evalEmbeddingBaseURL, got %q", overrides.evalEmbeddingBaseURL)
	}
	if overrides.evalChatBaseURL != "http://localhost:11435" {
		t.Fatalf("expected trimmed evalChatBaseURL, got %q", overrides.evalChatBaseURL)
	}
	if len(overrides.evalPathMaps) != 1 || overrides.evalPathMaps[0].from != "/app" || overrides.evalPathMaps[0].to == "." {
		t.Fatalf("expected absolute /app path map, got %#v", overrides.evalPathMaps)
	}
	if !overrides.evalAllowMissingSources {
		t.Fatal("expected evalAllowMissingSources to be true")
	}
}

func TestBuildEvalOverridesRejectsInvalidBool(t *testing.T) {
	_, err := buildEvalOverrides(evalOverridesInput{retrievalAutoExpand: "maybe"})
	if err == nil {
		t.Fatal("expected invalid boolean value error")
	}
}

func TestBuildEvalOverridesRejectsInvalidStrategy(t *testing.T) {
	if _, err := buildEvalOverrides(evalOverridesInput{retrievalSearchMode: "bm25"}); err == nil {
		t.Fatal("expected invalid search mode error")
	}
	if _, err := buildEvalOverrides(evalOverridesInput{retrievalRerankStrategy: "random"}); err == nil {
		t.Fatal("expected invalid rerank strategy error")
	}
	if _, err := buildEvalOverrides(evalOverridesInput{retrievalQueryRewrite: "maybe"}); err == nil {
		t.Fatal("expected invalid query rewrite error")
	}
	if _, err := buildEvalOverrides(evalOverridesInput{evalPathMap: "/app"}); err == nil {
		t.Fatal("expected invalid eval path map error")
	}
}

func TestRewriteEvalPath(t *testing.T) {
	rules := []evalPathMapRule{{from: "/app", to: "/workspace/backend"}}
	if actual := rewriteEvalPath("/app/data/uploads/demo.csv", rules); actual != "/workspace/backend/data/uploads/demo.csv" {
		t.Fatalf("unexpected mapped path: %s", actual)
	}
	if actual := rewriteEvalPath("/other/data/uploads/demo.csv", rules); actual != "/other/data/uploads/demo.csv" {
		t.Fatalf("unexpected unchanged path: %s", actual)
	}
}

func TestValidateEvalDatasetSources(t *testing.T) {
	knowledgeBases := map[string]model.KnowledgeBase{
		"kb-1": {
			ID:   "kb-1",
			Name: "主知识库",
			Documents: []model.Document{{
				ID: "doc-active",
			}},
		},
	}
	ds := &offline.Dataset{Cases: []offline.GroundTruthCase{
		{
			ID:       "case-ok",
			Question: "有效问题",
			SourceDocuments: []offline.SourceDocument{{
				KnowledgeBaseID: "kb-1",
				DocumentID:      "doc-active",
			}},
		},
		{
			ID:       "case-missing-doc",
			Question: "失效文档问题",
			SourceDocuments: []offline.SourceDocument{{
				KnowledgeBaseID: "kb-1",
				DocumentID:      "doc-stale",
			}},
		},
		{
			ID:       "case-missing-kb",
			Question: "失效知识库问题",
			SourceDocuments: []offline.SourceDocument{{
				KnowledgeBaseID: "kb-missing",
				DocumentID:      "doc-active",
			}},
		},
	}}

	issues := validateEvalDatasetSources(ds, knowledgeBases, "")
	if len(issues) != 2 {
		t.Fatalf("expected 2 source issues, got %#v", issues)
	}
	if issues[0].CaseID != "case-missing-doc" || issues[0].Reason != "文档不存在于当前知识库" {
		t.Fatalf("unexpected missing doc issue: %#v", issues[0])
	}
	if issues[1].CaseID != "case-missing-kb" || issues[1].Reason != "知识库不存在" {
		t.Fatalf("unexpected missing kb issue: %#v", issues[1])
	}
	formatted := formatEvalDatasetSourceIssues(issues, 1)
	if formatted == "" || !strings.Contains(formatted, "case-missing-doc") || !strings.Contains(formatted, "还有 1 处问题") {
		t.Fatalf("unexpected formatted issues: %q", formatted)
	}
}

func TestValidateEvalDatasetSourcesUsesOverrideKnowledgeBase(t *testing.T) {
	knowledgeBases := map[string]model.KnowledgeBase{
		"kb-override": {
			ID: "kb-override",
			Documents: []model.Document{{
				ID: "doc-active",
			}},
		},
	}
	ds := &offline.Dataset{Cases: []offline.GroundTruthCase{{
		ID: "case-override",
		SourceDocuments: []offline.SourceDocument{{
			KnowledgeBaseID: "kb-old",
			DocumentID:      "doc-active",
		}},
	}}}

	if issues := validateEvalDatasetSources(ds, knowledgeBases, "kb-override"); len(issues) != 0 {
		t.Fatalf("expected override knowledge base to validate source document, got %#v", issues)
	}
}

func TestApplyEvalOverrides(t *testing.T) {
	serverConfig := applyEvalOverrides(model.ServerConfig{
		EvalKnowledgeBaseID:            "kb-default",
		RetrievalTopKDocument:          6,
		RetrievalCandidateTopKDocument: 12,
		RetrievalTopKKnowledgeBase:     10,
		RetrievalCandidateTopKAllDocs:  32,
		RetrievalMaxChunksPerDocument:  2,
		RetrievalMaxContextChars:       2400,
		RetrievalEnableAutoExpand:      false,
	}, evalOverrides{
		knowledgeBaseID:                "kb-override",
		retrievalTopKDocument:          8,
		retrievalCandidateTopKDocument: 16,
		retrievalTopKKnowledgeBase:     12,
		retrievalCandidateTopKAllDocs:  48,
		retrievalMaxChunksPerDocument:  4,
		retrievalMaxContextChars:       3600,
		retrievalAutoExpand:            boolPtr(true),
	})

	if serverConfig.EvalKnowledgeBaseID != "kb-override" {
		t.Fatalf("expected EvalKnowledgeBaseID kb-override, got %q", serverConfig.EvalKnowledgeBaseID)
	}
	if serverConfig.RetrievalTopKDocument != 8 {
		t.Fatalf("expected RetrievalTopKDocument 8, got %d", serverConfig.RetrievalTopKDocument)
	}
	if serverConfig.RetrievalCandidateTopKDocument != 16 {
		t.Fatalf("expected RetrievalCandidateTopKDocument 16, got %d", serverConfig.RetrievalCandidateTopKDocument)
	}
	if serverConfig.RetrievalTopKKnowledgeBase != 12 {
		t.Fatalf("expected RetrievalTopKKnowledgeBase 12, got %d", serverConfig.RetrievalTopKKnowledgeBase)
	}
	if serverConfig.RetrievalCandidateTopKAllDocs != 48 {
		t.Fatalf("expected RetrievalCandidateTopKAllDocs 48, got %d", serverConfig.RetrievalCandidateTopKAllDocs)
	}
	if serverConfig.RetrievalMaxChunksPerDocument != 4 {
		t.Fatalf("expected RetrievalMaxChunksPerDocument 4, got %d", serverConfig.RetrievalMaxChunksPerDocument)
	}
	if serverConfig.RetrievalMaxContextChars != 3600 {
		t.Fatalf("expected RetrievalMaxContextChars 3600, got %d", serverConfig.RetrievalMaxContextChars)
	}
	if !serverConfig.RetrievalEnableAutoExpand {
		t.Fatal("expected RetrievalEnableAutoExpand true")
	}
}

func TestBuildRunID(t *testing.T) {
	runID := buildRunID("baseline", "", "Hybrid Search V1", time.Date(2026, 5, 7, 10, 11, 12, 0, time.UTC))
	if runID != "baseline_20260507-101112_hybrid-search-v1" {
		t.Fatalf("unexpected runID: %s", runID)
	}
}

func TestBuildRunIDUsesCustomPrefix(t *testing.T) {
	runID := buildRunID("baseline", "eval.custom", "", time.Date(2026, 5, 7, 10, 11, 12, 0, time.UTC))
	if runID != "eval-custom_20260507-101112" {
		t.Fatalf("unexpected runID: %s", runID)
	}
}

func TestBuildSummaryAnswerUsesEvidenceOnly(t *testing.T) {
	chunks := []offline.RetrievedChunkInfo{
		{DocumentID: "private-doc", ChunkID: "private-chunk", Score: 0.93, Text: "Qdrant 的 payload 可以存储能够表示为 JSON 的任意信息。"},
	}

	answer := buildSummaryAnswer("无关问题", chunks)
	if !strings.Contains(answer, "payload") {
		t.Fatalf("expected evidence text in fallback answer, got %q", answer)
	}
	for _, marker := range []string{"private-doc", "private-chunk", "0.9300", "..."} {
		if strings.Contains(answer, marker) {
			t.Fatalf("fallback answer should not contain evaluator metadata %q: %q", marker, answer)
		}
	}

	faithfulness := offline.EvaluateFaithfulness(answer, chunks)
	if !faithfulness.Evaluated || faithfulness.UnsupportedClaimCount != 0 {
		t.Fatalf("expected evidence-only fallback to be fully supported, got %#v", faithfulness)
	}
}

func TestBuildSummaryAnswerMarksMissingEvidenceAsAbstention(t *testing.T) {
	answer := buildSummaryAnswer("无关问题", nil)
	faithfulness := offline.EvaluateFaithfulness(answer, nil)
	if faithfulness.Evaluated || faithfulness.ClaimCount != 0 || faithfulness.UnsupportedClaimCount != 0 {
		t.Fatalf("expected missing-evidence fallback to be excluded from factual claims, got %#v", faithfulness)
	}
}

func TestParseOptionalBool(t *testing.T) {
	cases := map[string]bool{
		"true":  true,
		"1":     true,
		"yes":   true,
		"false": false,
		"0":     false,
		"off":   false,
	}

	for input, expected := range cases {
		got, err := parseOptionalBool(input)
		if err != nil {
			t.Fatalf("expected success for %q, got %v", input, err)
		}
		if got != expected {
			t.Fatalf("expected %v for %q, got %v", expected, input, got)
		}
	}
}

func boolPtr(v bool) *bool {
	return &v
}
