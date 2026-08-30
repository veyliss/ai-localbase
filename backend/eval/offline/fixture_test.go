package offline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureManifestValidateDataset(t *testing.T) {
	root := t.TempDir()
	fixturePath := filepath.Join(root, "facts.md")
	fixtureText := "### 事实 {#fact}\n\n示例机构成立于1898年。\n"
	if err := os.WriteFile(fixturePath, []byte(fixtureText), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	manifest := FixtureManifest{
		Version:   "test-v1",
		Documents: []FixtureDocument{{DocumentKey: "facts", Path: "facts.md"}},
		Cases:     []FixtureCase{{ID: "case-1", DocumentKey: "facts", Section: "fact"}},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	loaded, err := LoadFixtureManifest(manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	dataset := &Dataset{Cases: []GroundTruthCase{{
		ID:             "case-1",
		Question:       "机构何时成立？",
		Answer:         "示例机构成立于1898年。",
		AnswerSnippets: []string{"1898年"},
		AnswerType:     "extractive",
		Difficulty:     "easy",
	}}}
	if err := loaded.ValidateDataset(manifestPath, dataset); err != nil {
		t.Fatalf("expected fixture dataset to validate: %v", err)
	}

	checksum, err := FileSHA256(fixturePath)
	if err != nil || len(checksum) != 64 {
		t.Fatalf("expected SHA-256 checksum, got %q err=%v", checksum, err)
	}
}

func TestFixtureManifestRejectsUnsafePath(t *testing.T) {
	manifest := &FixtureManifest{
		Version:   "test-v1",
		Documents: []FixtureDocument{{DocumentKey: "facts", Path: "../facts.md"}},
		Cases:     []FixtureCase{{ID: "case-1", DocumentKey: "facts", Section: "fact"}},
	}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "safe relative path") {
		t.Fatalf("expected unsafe path validation error, got %v", err)
	}
}

func TestFixtureManifestRejectsMissingDatasetCase(t *testing.T) {
	manifest := &FixtureManifest{
		Version:   "test-v1",
		Documents: []FixtureDocument{{DocumentKey: "facts", Path: "facts.md"}},
		Cases:     []FixtureCase{{ID: "case-missing", DocumentKey: "facts", Section: "fact"}},
	}
	if err := manifest.ValidateDataset("manifest.json", &Dataset{Cases: []GroundTruthCase{}}); err == nil || !strings.Contains(err.Error(), "missing from dataset") {
		t.Fatalf("expected missing case validation error, got %v", err)
	}
}
