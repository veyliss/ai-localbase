package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"ai-localbase/internal/model"
)

type persistentAppState struct {
	Config         model.AppConfig                         `json:"config"`
	KnowledgeBases map[string]model.KnowledgeBase          `json:"knowledgeBases"`
	EvalDatasets   map[string]model.EvalDataset            `json:"evalDatasets,omitempty"`
	EvalRuns       map[string]model.RunEvalDatasetResponse `json:"evalRuns,omitempty"`
}

type AppStateStore struct {
	path string
}

func NewAppStateStore(path string) *AppStateStore {
	return &AppStateStore{path: path}
}

func (s *AppStateStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *AppStateStore) Load() (*persistentAppState, error) {
	if s == nil || s.path == "" {
		return nil, nil
	}

	content, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read app state: %w", err)
	}

	var state persistentAppState
	if err := json.Unmarshal(content, &state); err != nil {
		return nil, fmt.Errorf("decode app state: %w", err)
	}
	if state.KnowledgeBases == nil {
		state.KnowledgeBases = map[string]model.KnowledgeBase{}
	}
	if state.EvalDatasets == nil {
		state.EvalDatasets = map[string]model.EvalDataset{}
	}
	if state.EvalRuns == nil {
		state.EvalRuns = map[string]model.RunEvalDatasetResponse{}
	}
	return &state, nil
}

func (s *AppStateStore) Save(state persistentAppState) error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create app state directory: %w", err)
	}

	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode app state: %w", err)
	}

	tempFile := s.path + ".tmp"
	if err := os.WriteFile(tempFile, content, 0o644); err != nil {
		return fmt.Errorf("write app state temp file: %w", err)
	}
	if err := os.Rename(tempFile, s.path); err != nil {
		return fmt.Errorf("replace app state file: %w", err)
	}
	return nil
}
