package main

import (
	"testing"
	"time"

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
}

func TestBuildEvalOverridesRejectsInvalidBool(t *testing.T) {
	_, err := buildEvalOverrides(evalOverridesInput{retrievalAutoExpand: "maybe"})
	if err == nil {
		t.Fatal("expected invalid boolean value error")
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
