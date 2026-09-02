package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ai-localbase/internal/model"
)

type persistentAppState struct {
	Config         model.AppConfig                         `json:"config"`
	KnowledgeBases map[string]model.KnowledgeBase          `json:"knowledgeBases"`
	EvalDatasets   map[string]model.EvalDataset            `json:"evalDatasets,omitempty"`
	EvalRuns       map[string]model.RunEvalDatasetResponse `json:"evalRuns,omitempty"`
	Auth           model.AuthState                         `json:"auth,omitempty"`
}

type persistedAppStateJSON struct {
	Config         model.AppConfig                         `json:"config"`
	KnowledgeBases map[string]persistedKnowledgeBase       `json:"knowledgeBases"`
	EvalDatasets   map[string]model.EvalDataset            `json:"evalDatasets,omitempty"`
	EvalRuns       map[string]model.RunEvalDatasetResponse `json:"evalRuns,omitempty"`
	Auth           model.AuthState                         `json:"auth,omitempty"`
}

type persistedKnowledgeBase struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	Description         string                 `json:"description"`
	Tags                []string               `json:"tags,omitempty"`
	Documents           []persistedDocument    `json:"documents"`
	CreatedAt           string                 `json:"createdAt"`
	UpdatedAt           string                 `json:"updatedAt,omitempty"`
	CurrentIndexVersion int                    `json:"currentIndexVersion,omitempty"`
	IndexHistory        []model.IndexRunRecord `json:"indexHistory,omitempty"`
}

type persistedDocument struct {
	ID                      string `json:"id"`
	KnowledgeBaseID         string `json:"knowledgeBaseId"`
	Name                    string `json:"name"`
	Size                    int64  `json:"size"`
	SizeLabel               string `json:"sizeLabel"`
	UploadedAt              string `json:"uploadedAt"`
	Status                  string `json:"status"`
	Source                  string `json:"source,omitempty"`
	Version                 int    `json:"version,omitempty"`
	Checksum                string `json:"checksum,omitempty"`
	Path                    string `json:"path"`
	ContentPreview          string `json:"contentPreview"`
	ChunkCount              int    `json:"chunkCount,omitempty"`
	IndexedAt               string `json:"indexedAt,omitempty"`
	IndexError              string `json:"indexError,omitempty"`
	IndexErrorCode          string `json:"indexErrorCode,omitempty"`
	IndexRunID              string `json:"indexRunId,omitempty"`
	IndexVersion            int    `json:"indexVersion,omitempty"`
	IndexedContentAvailable bool   `json:"indexedContentAvailable,omitempty"`
	IndexedContentChars     int    `json:"indexedContentChars,omitempty"`
	IndexedTablesCount      int    `json:"indexedTablesCount,omitempty"`
	IndexFence              string `json:"indexFence,omitempty"`
}

func (s persistentAppState) MarshalJSON() ([]byte, error) {
	knowledgeBases := make(map[string]persistedKnowledgeBase, len(s.KnowledgeBases))
	for id, knowledgeBase := range s.KnowledgeBases {
		documents := make([]persistedDocument, len(knowledgeBase.Documents))
		for index, document := range knowledgeBase.Documents {
			documents[index] = persistedDocumentFromModel(document)
		}
		knowledgeBases[id] = persistedKnowledgeBase{
			ID:                  knowledgeBase.ID,
			Name:                knowledgeBase.Name,
			Description:         knowledgeBase.Description,
			Tags:                append([]string(nil), knowledgeBase.Tags...),
			Documents:           documents,
			CreatedAt:           knowledgeBase.CreatedAt,
			UpdatedAt:           knowledgeBase.UpdatedAt,
			CurrentIndexVersion: knowledgeBase.CurrentIndexVersion,
			IndexHistory:        append([]model.IndexRunRecord(nil), knowledgeBase.IndexHistory...),
		}
	}
	return json.Marshal(persistedAppStateJSON{
		Config:         s.Config,
		KnowledgeBases: knowledgeBases,
		EvalDatasets:   s.EvalDatasets,
		EvalRuns:       s.EvalRuns,
		Auth:           s.Auth,
	})
}

func (s *persistentAppState) UnmarshalJSON(data []byte) error {
	var raw persistedAppStateJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Config = raw.Config
	s.KnowledgeBases = make(map[string]model.KnowledgeBase, len(raw.KnowledgeBases))
	for id, knowledgeBase := range raw.KnowledgeBases {
		s.KnowledgeBases[id] = migratePersistedKnowledgeBase(id, knowledgeBase)
	}
	s.EvalDatasets = raw.EvalDatasets
	s.EvalRuns = raw.EvalRuns
	s.Auth = raw.Auth
	return nil
}

func migratePersistedKnowledgeBase(mapKey string, raw persistedKnowledgeBase) model.KnowledgeBase {
	knowledgeBaseID := strings.TrimSpace(raw.ID)
	if knowledgeBaseID == "" {
		knowledgeBaseID = strings.TrimSpace(mapKey)
	}
	createdAt := strings.TrimSpace(raw.CreatedAt)
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}
	updatedAt := strings.TrimSpace(raw.UpdatedAt)
	if updatedAt == "" {
		updatedAt = createdAt
	}
	indexVersion := raw.CurrentIndexVersion
	if indexVersion <= 0 {
		indexVersion = currentIndexVersion
	}

	documents := make([]model.Document, len(raw.Documents))
	for index, persisted := range raw.Documents {
		document := documentToModel(persisted)
		if strings.TrimSpace(document.KnowledgeBaseID) == "" {
			document.KnowledgeBaseID = knowledgeBaseID
		}
		if strings.TrimSpace(document.Source) == "" {
			document.Source = "legacy"
		}
		if document.Version <= 0 {
			document.Version = 1
		}
		if strings.TrimSpace(document.Status) == "" {
			document.Status = "ready"
		}
		if strings.TrimSpace(document.IndexError) != "" && strings.TrimSpace(document.IndexErrorCode) == "" {
			document.IndexErrorCode = classifyIndexError(fmt.Errorf("%s", document.IndexError))
			document.IndexError = publicIndexError(document.IndexErrorCode)
		}
		documents[index] = document
	}

	history := make([]model.IndexRunRecord, len(raw.IndexHistory))
	for index, record := range raw.IndexHistory {
		if strings.TrimSpace(record.KnowledgeBaseID) == "" {
			record.KnowledgeBaseID = knowledgeBaseID
		}
		if record.IndexVersion <= 0 {
			record.IndexVersion = indexVersion
		}
		record = publicIndexRunRecord(record)
		history[index] = record
	}

	return model.KnowledgeBase{
		ID:                  knowledgeBaseID,
		Name:                raw.Name,
		Description:         raw.Description,
		Tags:                normalizeKnowledgeBaseTags(raw.Tags),
		Documents:           documents,
		CreatedAt:           createdAt,
		UpdatedAt:           updatedAt,
		CurrentIndexVersion: indexVersion,
		IndexHistory:        history,
	}
}

func persistedDocumentFromModel(document model.Document) persistedDocument {
	return persistedDocument{
		ID:                      document.ID,
		KnowledgeBaseID:         document.KnowledgeBaseID,
		Name:                    normalizeDocumentName(document.Name),
		Size:                    document.Size,
		SizeLabel:               document.SizeLabel,
		UploadedAt:              document.UploadedAt,
		Status:                  document.Status,
		Source:                  document.Source,
		Version:                 document.Version,
		Checksum:                document.Checksum,
		Path:                    document.Path,
		ContentPreview:          document.ContentPreview,
		ChunkCount:              document.ChunkCount,
		IndexedAt:               document.IndexedAt,
		IndexError:              document.IndexError,
		IndexErrorCode:          document.IndexErrorCode,
		IndexRunID:              document.IndexRunID,
		IndexVersion:            document.IndexVersion,
		IndexedContentAvailable: document.IndexedContentAvailable,
		IndexedContentChars:     document.IndexedContentChars,
		IndexedTablesCount:      document.IndexedTablesCount,
		IndexFence:              strings.TrimSpace(document.IndexFence),
	}
}

func documentToModel(document persistedDocument) model.Document {
	return model.Document{
		ID:                      document.ID,
		KnowledgeBaseID:         document.KnowledgeBaseID,
		Name:                    normalizeDocumentName(document.Name),
		Size:                    document.Size,
		SizeLabel:               document.SizeLabel,
		UploadedAt:              document.UploadedAt,
		Status:                  document.Status,
		Source:                  document.Source,
		Version:                 document.Version,
		Checksum:                document.Checksum,
		Path:                    document.Path,
		ContentPreview:          document.ContentPreview,
		ChunkCount:              document.ChunkCount,
		IndexedAt:               document.IndexedAt,
		IndexError:              document.IndexError,
		IndexErrorCode:          document.IndexErrorCode,
		IndexRunID:              document.IndexRunID,
		IndexVersion:            document.IndexVersion,
		IndexedContentAvailable: document.IndexedContentAvailable,
		IndexedContentChars:     document.IndexedContentChars,
		IndexedTablesCount:      document.IndexedTablesCount,
		IndexFence:              strings.TrimSpace(document.IndexFence),
	}
}

type AppStateStore struct {
	path string
	mu   sync.Mutex
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
	if state.Auth.Users == nil {
		state.Auth.Users = map[string]model.AuthUser{}
	}
	if state.Auth.Sessions == nil {
		state.Auth.Sessions = map[string]model.AuthSession{}
	}
	if state.Auth.APIKeys == nil {
		state.Auth.APIKeys = map[string]model.APIKey{}
	}
	if state.Auth.AppliedPasswordResetTokens == nil {
		state.Auth.AppliedPasswordResetTokens = []string{}
	}
	return &state, nil
}

func (s *AppStateStore) Save(state persistentAppState) error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create app state directory: %w", err)
	}

	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode app state: %w", err)
	}

	tempFile := s.path + ".tmp"
	if err := os.WriteFile(tempFile, content, 0o600); err != nil {
		return fmt.Errorf("write app state temp file: %w", err)
	}
	if err := os.Rename(tempFile, s.path); err != nil {
		return fmt.Errorf("replace app state file: %w", err)
	}
	return nil
}
