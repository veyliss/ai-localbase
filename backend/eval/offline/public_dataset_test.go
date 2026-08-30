package offline

import (
	"errors"
	"os"
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
	if len(dataset.Cases) < 50 {
		t.Fatalf("expected the public regression corpus to cover at least 50 cases, got %d", len(dataset.Cases))
	}

	answerTypes := make(map[string]bool)
	noAnswerCount := 0
	for _, item := range dataset.Cases {
		isNoAnswer := strings.EqualFold(strings.TrimSpace(item.AnswerType), "no_answer")
		answerTypes[strings.ToLower(strings.TrimSpace(item.AnswerType))] = true
		if isNoAnswer {
			noAnswerCount++
		}
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
	for _, answerType := range []string{"extractive", "abstractive", "yesno", "numeric", "no_answer"} {
		if !answerTypes[answerType] {
			t.Errorf("public regression corpus must cover answer type %q", answerType)
		}
	}
	if noAnswerCount < 2 {
		t.Errorf("public regression corpus must include at least 2 no_answer cases, got %d", noAnswerCount)
	}
}

func TestPublicFixtureManifestMatchesGroundTruth(t *testing.T) {
	dataset, err := LoadDataset(filepath.Join("..", "data", "ground_truth_v1.small.json"))
	if err != nil {
		t.Fatalf("load public ground truth dataset: %v", err)
	}

	manifestPath := filepath.Join("..", "fixtures", "public-v1", "manifest.json")
	manifest, err := LoadFixtureManifest(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skipf("public fixture is not checked in; skipping local fixture validation: %s", manifestPath)
		}
		t.Fatalf("read public fixture manifest: %v", err)
	}
	if manifest.Version != "public-v1" {
		t.Fatalf("expected public-v1 fixture manifest, got %q", manifest.Version)
	}
	if len(manifest.Documents) < 2 {
		t.Fatalf("expected public fixture manifest to cover multiple documents, got %d", len(manifest.Documents))
	}

	for _, document := range manifest.Documents {
		fixturePath, resolveErr := manifest.ResolveDocumentPath(manifestPath, document.DocumentKey)
		if resolveErr != nil {
			t.Fatalf("resolve public fixture document %q: %v", document.DocumentKey, resolveErr)
		}
		if _, readErr := os.ReadFile(fixturePath); readErr != nil {
			t.Fatalf("read public fixture document %q: %v", document.DocumentKey, readErr)
		}
		if strings.TrimSpace(document.SHA256) != "" {
			actual, checksumErr := FileSHA256(fixturePath)
			if checksumErr != nil {
				t.Fatalf("checksum public fixture document %q: %v", document.DocumentKey, checksumErr)
			}
			if !strings.EqualFold(strings.TrimSpace(document.SHA256), actual) {
				t.Fatalf("public fixture document %q checksum mismatch: manifest=%s actual=%s", document.DocumentKey, document.SHA256, actual)
			}
		}
	}
	if len(manifest.Cases) != len(dataset.Cases) {
		t.Fatalf("public fixture manifest must cover all dataset cases: manifest=%d dataset=%d", len(manifest.Cases), len(dataset.Cases))
	}
	if err := manifest.ValidateDataset(manifestPath, dataset); err != nil {
		t.Fatalf("validate public fixture manifest against dataset: %v", err)
	}

	manifestCaseIDs := make(map[string]struct{}, len(manifest.Cases))
	for _, fixtureCase := range manifest.Cases {
		manifestCaseIDs[fixtureCase.ID] = struct{}{}
	}
	for _, item := range dataset.Cases {
		if _, ok := manifestCaseIDs[item.ID]; !ok {
			t.Errorf("public fixture manifest is missing dataset case %s", item.ID)
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
