package offline

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicGroundTruthDatasetIsCuratedAndRunnable(t *testing.T) {
	path := filepath.Join("..", "data", "ground_truth_v1.small.json")
	dataset, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("load public ground truth dataset: %v", err)
	}
	if err := dataset.Validate(); err != nil {
		t.Fatalf("validate public ground truth dataset: %v", err)
	}
	if len(dataset.Cases) < 18 {
		t.Fatalf("expected the public regression corpus to cover at least 18 cases, got %d", len(dataset.Cases))
	}

	for _, item := range dataset.Cases {
		isNoAnswer := strings.EqualFold(strings.TrimSpace(item.AnswerType), "no_answer")
		if item.ReviewStatus != "approved" {
			t.Errorf("case %s must be explicitly approved, got %q", item.ID, item.ReviewStatus)
		}
		if !isNoAnswer && len(item.SourceDocuments) == 0 && len(item.AnswerSnippets) == 0 {
			t.Errorf("case %s has neither source_documents nor answer_snippets", item.ID)
		}
		for _, snippet := range item.AnswerSnippets {
			if !strings.Contains(normalizeEvalText(item.Answer), normalizeEvalText(snippet)) {
				t.Errorf("case %s answer does not contain answer snippet %q", item.ID, snippet)
			}
		}
	}
}

func TestDatasetValidateRejectsDuplicateAndMalformedCases(t *testing.T) {
	dataset := &Dataset{Cases: []GroundTruthCase{
		{ID: "same", Question: "问题一", Answer: "答案", AnswerType: "extractive", Difficulty: "easy"},
		{ID: "same", Question: "问题二", Answer: "答案", AnswerType: "extractive", Difficulty: "easy"},
	}}
	if err := dataset.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate ID") {
		t.Fatalf("expected duplicate ID validation error, got %v", err)
	}

	dataset = &Dataset{Cases: []GroundTruthCase{{
		ID:             "malformed",
		Question:       "问题",
		Answer:         "答案",
		AnswerType:     "extractive",
		Difficulty:     "easy",
		AnswerSnippets: []string{""},
	}}}
	if err := dataset.Validate(); err == nil || !strings.Contains(err.Error(), "answer_snippets[0]") {
		t.Fatalf("expected empty snippet validation error, got %v", err)
	}
}
