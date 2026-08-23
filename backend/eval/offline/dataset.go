package offline

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"
)

// GroundTruthCase 表示单个测试用例
type GroundTruthCase struct {
	ID              string           `json:"id"`
	Question        string           `json:"question"`
	Answer          string           `json:"answer"`
	AnswerSnippets  []string         `json:"answer_snippets"`
	SourceDocuments []SourceDocument `json:"source_documents"`
	AnswerType      string           `json:"answer_type"` // extractive|abstractive|yesno|numeric
	Difficulty      string           `json:"difficulty"`  // easy|medium|hard
	Notes           string           `json:"notes"`
	ReviewStatus    string           `json:"review_status,omitempty"`
	Disabled        bool             `json:"disabled,omitempty"`
}

type SourceDocument struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	DocumentID      string `json:"document_id"`
	ChunkID         string `json:"chunk_id"`
}

type Dataset struct {
	Cases []GroundTruthCase
}

// LoadDataset 从 JSON 文件加载数据集
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read dataset file: %w", err)
	}

	var cases []GroundTruthCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("failed to unmarshal dataset JSON: %w", err)
	}

	return &Dataset{Cases: cases}, nil
}

// Validate 验证数据集（检查必填字段等）
func (d *Dataset) Validate() error {
	if d == nil {
		return fmt.Errorf("dataset is empty")
	}
	if len(d.Cases) == 0 {
		return fmt.Errorf("dataset is empty")
	}
	seenIDs := make(map[string]struct{}, len(d.Cases))
	seenQuestions := make(map[string]struct{}, len(d.Cases))
	for i, c := range d.Cases {
		caseID := strings.TrimSpace(c.ID)
		question := strings.TrimSpace(c.Question)
		answer := strings.TrimSpace(c.Answer)
		if caseID == "" {
			return fmt.Errorf("case %d: ID is required", i)
		}
		if _, exists := seenIDs[caseID]; exists {
			return fmt.Errorf("case %d (%s): duplicate ID", i, caseID)
		}
		seenIDs[caseID] = struct{}{}
		if question == "" {
			return fmt.Errorf("case %d (%s): Question is required", i, c.ID)
		}
		questionKey := normalizeDatasetKey(question)
		if _, exists := seenQuestions[questionKey]; exists {
			return fmt.Errorf("case %d (%s): duplicate question", i, caseID)
		}
		seenQuestions[questionKey] = struct{}{}
		if answer == "" {
			return fmt.Errorf("case %d (%s): Answer is required", i, c.ID)
		}
		if strings.TrimSpace(c.AnswerType) == "" {
			return fmt.Errorf("case %d (%s): AnswerType is required", i, c.ID)
		}
		if strings.TrimSpace(c.Difficulty) == "" {
			return fmt.Errorf("case %d (%s): Difficulty is required", i, c.ID)
		}
		for snippetIndex, snippet := range c.AnswerSnippets {
			if strings.TrimSpace(snippet) == "" {
				return fmt.Errorf("case %d (%s): answer_snippets[%d] is empty", i, caseID, snippetIndex)
			}
		}
		for sourceIndex, source := range c.SourceDocuments {
			if strings.TrimSpace(source.DocumentID) == "" {
				return fmt.Errorf("case %d (%s): source_documents[%d].document_id is required", i, caseID, sourceIndex)
			}
		}
		status := strings.ToLower(strings.TrimSpace(c.ReviewStatus))
		if status != "" && status != "pending" && status != "approved" && status != "rejected" && status != "disabled" {
			return fmt.Errorf("case %d (%s): unsupported review_status %q", i, caseID, c.ReviewStatus)
		}
	}
	return nil
}

func normalizeDatasetKey(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

// EnabledCases returns a copy that excludes explicitly disabled or rejected cases.
// Legacy datasets without review_status remain runnable for compatibility.
func (d *Dataset) EnabledCases() *Dataset {
	if d == nil {
		return &Dataset{}
	}

	active := make([]GroundTruthCase, 0, len(d.Cases))
	for _, item := range d.Cases {
		status := strings.ToLower(strings.TrimSpace(item.ReviewStatus))
		if item.Disabled || status == "disabled" || status == "rejected" {
			continue
		}
		active = append(active, item)
	}
	return &Dataset{Cases: active}
}

// Sample 随机采样 n 个用例（n<=0 时返回全部）
func (d *Dataset) Sample(n int) *Dataset {
	if n <= 0 || n >= len(d.Cases) {
		return d
	}

	src := rand.NewSource(time.Now().UnixNano())
	r := rand.New(src)

	indices := r.Perm(len(d.Cases))
	sampledCases := make([]GroundTruthCase, n)
	for i := 0; i < n; i++ {
		sampledCases[i] = d.Cases[indices[i]]
	}

	return &Dataset{Cases: sampledCases}
}
