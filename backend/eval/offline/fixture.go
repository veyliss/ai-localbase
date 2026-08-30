package offline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FixtureManifest describes the stable document and section names used by a
// local evaluation fixture. Document IDs are intentionally absent because an
// upload creates new IDs in every knowledge base.
type FixtureManifest struct {
	Version     string            `json:"version"`
	GeneratedAt string            `json:"generated_at,omitempty"`
	Documents   []FixtureDocument `json:"documents"`
	Cases       []FixtureCase     `json:"cases"`
}

type FixtureDocument struct {
	DocumentKey string   `json:"document_key"`
	Path        string   `json:"path"`
	Format      string   `json:"format,omitempty"`
	SHA256      string   `json:"sha256,omitempty"`
	SourceURLs  []string `json:"source_urls,omitempty"`
}

type FixtureCase struct {
	ID          string `json:"id"`
	DocumentKey string `json:"document_key"`
	Section     string `json:"section"`
}

// LoadFixtureManifest loads and validates a manifest without modifying it.
func LoadFixtureManifest(path string) (*FixtureManifest, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("fixture manifest path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture manifest %s: %w", path, err)
	}

	var manifest FixtureManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode fixture manifest %s: %w", path, err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate fixture manifest %s: %w", path, err)
	}
	return &manifest, nil
}

func (m *FixtureManifest) Validate() error {
	if m == nil {
		return fmt.Errorf("fixture manifest is nil")
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("version is required")
	}
	if len(m.Documents) == 0 {
		return fmt.Errorf("at least one document is required")
	}

	documents := make(map[string]struct{}, len(m.Documents))
	for index, document := range m.Documents {
		key := strings.TrimSpace(document.DocumentKey)
		if key == "" {
			return fmt.Errorf("documents[%d].document_key is required", index)
		}
		if _, exists := documents[key]; exists {
			return fmt.Errorf("duplicate document_key %q", key)
		}
		documents[key] = struct{}{}

		path := strings.TrimSpace(document.Path)
		if path == "" || filepath.IsAbs(path) || filepath.Clean(path) == "." || strings.HasPrefix(filepath.ToSlash(filepath.Clean(path)), "../") || filepath.ToSlash(filepath.Clean(path)) == ".." {
			return fmt.Errorf("documents[%d].path must be a safe relative path", index)
		}
		if checksum := strings.TrimSpace(document.SHA256); checksum != "" {
			if len(checksum) != sha256.Size*2 {
				return fmt.Errorf("documents[%d].sha256 must be a SHA-256 hex digest", index)
			}
			if _, err := hex.DecodeString(checksum); err != nil {
				return fmt.Errorf("documents[%d].sha256 must be a SHA-256 hex digest: %w", index, err)
			}
		}
		for sourceIndex, sourceURL := range document.SourceURLs {
			if !strings.HasPrefix(strings.TrimSpace(sourceURL), "https://") {
				return fmt.Errorf("documents[%d].source_urls[%d] must use HTTPS", index, sourceIndex)
			}
		}
	}

	cases := make(map[string]struct{}, len(m.Cases))
	for index, fixtureCase := range m.Cases {
		id := strings.TrimSpace(fixtureCase.ID)
		if id == "" {
			return fmt.Errorf("cases[%d].id is required", index)
		}
		if _, exists := cases[id]; exists {
			return fmt.Errorf("duplicate case id %q", id)
		}
		cases[id] = struct{}{}
		if _, exists := documents[strings.TrimSpace(fixtureCase.DocumentKey)]; !exists {
			return fmt.Errorf("cases[%d] references unknown document_key %q", index, fixtureCase.DocumentKey)
		}
		if strings.TrimSpace(fixtureCase.Section) == "" {
			return fmt.Errorf("cases[%d].section is required", index)
		}
	}
	return nil
}

// ResolveDocumentPath returns a manifest document path relative to the
// manifest itself. It rejects path traversal so a manifest cannot make an
// evaluation read an unrelated local file.
func (m *FixtureManifest) ResolveDocumentPath(manifestPath, documentKey string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("fixture manifest is nil")
	}
	for _, document := range m.Documents {
		if strings.TrimSpace(document.DocumentKey) != strings.TrimSpace(documentKey) {
			continue
		}
		path := strings.TrimSpace(document.Path)
		clean := filepath.Clean(filepath.FromSlash(path))
		if path == "" || filepath.IsAbs(path) || clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
			return "", fmt.Errorf("fixture document %q has an unsafe path", documentKey)
		}
		base := "."
		if strings.TrimSpace(manifestPath) != "" {
			base = filepath.Dir(manifestPath)
		}
		return filepath.Join(base, clean), nil
	}
	return "", fmt.Errorf("fixture document %q not found", documentKey)
}

// ValidateDataset checks that every case described by the fixture manifest is
// present in the dataset and that its answer still belongs to the fixture
// section. The dataset may contain additional non-fixture cases.
func (m *FixtureManifest) ValidateDataset(manifestPath string, dataset *Dataset) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if dataset == nil {
		return fmt.Errorf("dataset is nil")
	}

	casesByID := make(map[string]GroundTruthCase, len(dataset.Cases))
	for _, item := range dataset.Cases {
		casesByID[item.ID] = item
	}
	contents := make(map[string]string, len(m.Documents))
	for _, fixtureCase := range m.Cases {
		item, ok := casesByID[fixtureCase.ID]
		if !ok {
			return fmt.Errorf("fixture case %s is missing from dataset", fixtureCase.ID)
		}
		isNoAnswer := isNoAnswerCase(item)
		if fixtureCase.Section == "no-source" {
			if !isNoAnswer {
				return fmt.Errorf("fixture case %s uses no-source but is not no_answer", fixtureCase.ID)
			}
			continue
		}
		if isNoAnswer {
			return fmt.Errorf("no_answer fixture case %s must not reference a source section", fixtureCase.ID)
		}

		documentKey := strings.TrimSpace(fixtureCase.DocumentKey)
		fixturePath, err := m.ResolveDocumentPath(manifestPath, documentKey)
		if err != nil {
			return err
		}
		content, ok := contents[documentKey]
		if !ok {
			data, readErr := os.ReadFile(fixturePath)
			if readErr != nil {
				return fmt.Errorf("read fixture document %q: %w", documentKey, readErr)
			}
			content = string(data)
			contents[documentKey] = content
		}
		if !strings.Contains(content, "{#"+strings.TrimSpace(fixtureCase.Section)+"}") {
			return fmt.Errorf("fixture case %s references missing section %q", fixtureCase.ID, fixtureCase.Section)
		}
		if !strings.Contains(content, item.Answer) {
			return fmt.Errorf("fixture case %s answer is not present in fixture document", fixtureCase.ID)
		}
		for _, snippet := range item.AnswerSnippets {
			if !strings.Contains(content, snippet) {
				return fmt.Errorf("fixture case %s answer snippet %q is not present in fixture document", fixtureCase.ID, snippet)
			}
		}
	}
	return nil
}

// FileSHA256 returns the SHA-256 digest of a local fixture or document.
func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
