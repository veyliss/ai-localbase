package offline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type publicFixtureManifest struct {
	Version   string                  `json:"version"`
	Documents []publicFixtureDocument `json:"documents"`
	Cases     []publicFixtureCase     `json:"cases"`
}

type publicFixtureDocument struct {
	DocumentKey string   `json:"document_key"`
	Path        string   `json:"path"`
	SourceURLs  []string `json:"source_urls"`
}

type publicFixtureCase struct {
	ID          string `json:"id"`
	DocumentKey string `json:"document_key"`
	Section     string `json:"section"`
}

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

func TestPublicFixtureManifestMatchesGroundTruth(t *testing.T) {
	dataset, err := LoadDataset(filepath.Join("..", "data", "ground_truth_v1.small.json"))
	if err != nil {
		t.Fatalf("load public ground truth dataset: %v", err)
	}

	manifestPath := filepath.Join("..", "fixtures", "public-v1", "manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("public fixture is not checked in; skipping local fixture validation: %s", manifestPath)
		}
		t.Fatalf("read public fixture manifest: %v", err)
	}
	var manifest publicFixtureManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode public fixture manifest: %v", err)
	}
	if manifest.Version != "public-v1" {
		t.Fatalf("expected public-v1 fixture manifest, got %q", manifest.Version)
	}
	if len(manifest.Documents) != 1 {
		t.Fatalf("expected one public fixture document, got %d", len(manifest.Documents))
	}

	document := manifest.Documents[0]
	if document.DocumentKey == "" || filepath.IsAbs(document.Path) || strings.Contains(document.Path, "..") {
		t.Fatalf("public fixture document path must be relative and safe: %#v", document)
	}
	for _, sourceURL := range document.SourceURLs {
		if !strings.HasPrefix(sourceURL, "https://") {
			t.Errorf("public fixture source URL must use HTTPS, got %q", sourceURL)
		}
	}
	fixturePath := filepath.Join("..", "fixtures", "public-v1", document.Path)
	fixtureData, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read public fixture document: %v", err)
	}
	fixtureText := string(fixtureData)

	caseByID := make(map[string]GroundTruthCase, len(dataset.Cases))
	for _, item := range dataset.Cases {
		caseByID[item.ID] = item
	}
	if len(manifest.Cases) < 15 {
		t.Fatalf("expected at least 15 public fixture cases, got %d", len(manifest.Cases))
	}
	for _, fixtureCase := range manifest.Cases {
		item, ok := caseByID[fixtureCase.ID]
		if !ok {
			t.Errorf("fixture case %s is missing from ground truth", fixtureCase.ID)
			continue
		}
		if fixtureCase.DocumentKey != document.DocumentKey {
			t.Errorf("fixture case %s references unknown document %q", fixtureCase.ID, fixtureCase.DocumentKey)
		}
		isNoAnswer := strings.EqualFold(strings.TrimSpace(item.AnswerType), "no_answer")
		if fixtureCase.Section == "no-source" {
			if !isNoAnswer {
				t.Errorf("fixture case %s uses no-source section but is not no_answer", fixtureCase.ID)
			}
			continue
		}
		if isNoAnswer {
			t.Errorf("no_answer case %s must not reference a fixture section", fixtureCase.ID)
			continue
		}
		if !strings.Contains(fixtureText, "{#"+fixtureCase.Section+"}") {
			t.Errorf("fixture case %s references missing section %q", fixtureCase.ID, fixtureCase.Section)
		}
		if !strings.Contains(fixtureText, item.Answer) {
			t.Errorf("fixture case %s does not contain the full answer", fixtureCase.ID)
		}
		for _, snippet := range item.AnswerSnippets {
			if !strings.Contains(fixtureText, snippet) {
				t.Errorf("fixture case %s does not contain answer snippet %q", fixtureCase.ID, snippet)
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
