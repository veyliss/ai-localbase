package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

// ListEvalRuns returns persisted run summaries without exposing the full case
// payload. Keeping run history operations together makes dataset generation
// and evaluation execution easier to evolve independently.
func (s *AppService) ListEvalRuns(knowledgeBaseID, datasetID string) []model.EvalRunSummary {
	if s == nil || s.state == nil {
		return nil
	}

	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	datasetID = strings.TrimSpace(datasetID)
	s.state.Mu.RLock()
	items := make([]model.EvalRunSummary, 0, len(s.state.EvalRuns))
	for _, run := range s.state.EvalRuns {
		if knowledgeBaseID != "" && run.KnowledgeBaseID != knowledgeBaseID {
			continue
		}
		if datasetID != "" && run.DatasetID != datasetID {
			continue
		}
		items = append(items, evalRunSummary(run))
	}
	s.state.Mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		return items[i].StartedAt > items[j].StartedAt
	})
	return items
}

// RunEvalDataset executes one stored dataset against the current retrieval
// boundary. It intentionally keeps the public AppService method unchanged.
func (s *AppService) RunEvalDataset(datasetID string, req model.RunEvalDatasetRequest) (model.RunEvalDatasetResponse, error) {
	if s == nil || s.state == nil {
		return model.RunEvalDatasetResponse{}, fmt.Errorf("app service is nil")
	}

	dataset, err := s.GetEvalDataset(datasetID)
	if err != nil {
		return model.RunEvalDatasetResponse{}, err
	}

	topK := req.TopK
	if topK <= 0 {
		topK = 12
	}
	if topK > 50 {
		topK = 50
	}
	searchMode := normalizeRetrievalMode(req.SearchMode)
	rerankStrategy := normalizeRerankStrategy(req.RerankStrategy)
	if rerankStrategy == "" {
		rerankStrategy = s.rerankStrategy()
	}

	startedAt := time.Now()
	startedAtLabel := startedAt.UTC().Format(time.RFC3339)
	results := make([]model.EvalRunCaseResult, 0, len(dataset.Items))
	skippedDisabled := 0
	runSearchMode := ""
	for _, item := range dataset.Items {
		if item.Disabled && !req.IncludeDisabled {
			skippedDisabled++
			continue
		}
		if strings.TrimSpace(item.Question) == "" {
			results = append(results, model.EvalRunCaseResult{
				CaseID:         item.ID,
				Question:       item.Question,
				ExpectedAnswer: item.Answer,
				HitRank:        -1,
				Error:          "question is empty",
			})
			continue
		}

		debugReq := model.RetrievalDebugRequest{
			Query:                   item.Question,
			KnowledgeBaseID:         dataset.KnowledgeBaseID,
			DocumentID:              dataset.DocumentID,
			TopK:                    topK,
			SearchMode:              searchMode,
			RerankStrategy:          rerankStrategy,
			EnableQueryRewrite:      req.EnableQueryRewrite,
			QueryRewriteMaxVariants: req.QueryRewriteMaxVariants,
		}
		if debugReq.KnowledgeBaseID == "" {
			debugReq.KnowledgeBaseID = firstEvalSourceKnowledgeBaseID(item)
		}

		response, err := s.DebugRetrieve(debugReq)
		if runSearchMode == "" && strings.TrimSpace(response.SearchMode) != "" {
			runSearchMode = response.SearchMode
		}
		caseResult := model.EvalRunCaseResult{
			CaseID:                   item.ID,
			Question:                 item.Question,
			ExpectedAnswer:           item.Answer,
			HitRank:                  -1,
			ElapsedMs:                response.ElapsedMs,
			LowConfidence:            response.LowConfidence,
			Confidence:               response.Confidence,
			EvidenceGateInputCount:   response.EvidenceGateInputCount,
			EvidenceGateOutputCount:  response.EvidenceGateOutputCount,
			EvidenceGateDroppedCount: response.EvidenceGateDroppedCount,
			Retrieved:                response.Items,
		}
		if err != nil {
			caseResult.Error = err.Error()
			results = append(results, caseResult)
			continue
		}

		hit, rank, matchedBy := evalCaseHit(item, response.Items)
		evidenceSupported, evidenceIssue := evalCaseEvidenceSupport(item, response.Items, hit)
		directEvidence := evalCaseDirectEvidence(item, response.Items)
		caseResult.Hit = hit
		caseResult.HitRank = rank
		caseResult.MatchedBy = matchedBy
		caseResult.EvidenceSupport = evidenceSupported
		caseResult.EvidenceIssue = evidenceIssue
		caseResult.DirectEvidence = directEvidence
		if hit && rank > 0 {
			caseResult.ReciprocalRank = 1 / float64(rank)
		} else {
			caseResult.Error = "未命中"
		}
		results = append(results, caseResult)
	}

	if len(results) == 0 {
		return model.RunEvalDatasetResponse{}, fmt.Errorf("no enabled eval cases to run")
	}

	response := model.RunEvalDatasetResponse{
		RunID:            util.NextID("eval-run"),
		DatasetID:        dataset.ID,
		DatasetName:      dataset.Name,
		KnowledgeBaseID:  dataset.KnowledgeBaseID,
		DocumentID:       dataset.DocumentID,
		SearchMode:       evalRunSearchModeLabel(runSearchMode, searchMode),
		RerankStrategy:   rerankStrategy,
		QueryRewriteUsed: evalRunQueryRewriteUsed(req, s.queryRewriteEnabled()),
		StartedAt:        startedAtLabel,
		ElapsedMs:        time.Since(startedAt).Milliseconds(),
		Metrics:          buildEvalRunMetrics(results, skippedDisabled),
		Cases:            results,
	}
	if err := s.saveEvalRun(response); err != nil {
		return model.RunEvalDatasetResponse{}, fmt.Errorf("save eval run: %w", err)
	}
	return response, nil
}

func evalRunSummary(run model.RunEvalDatasetResponse) model.EvalRunSummary {
	return model.EvalRunSummary{
		RunID:            run.RunID,
		DatasetID:        run.DatasetID,
		DatasetName:      run.DatasetName,
		KnowledgeBaseID:  run.KnowledgeBaseID,
		DocumentID:       run.DocumentID,
		SearchMode:       run.SearchMode,
		RerankStrategy:   run.RerankStrategy,
		QueryRewriteUsed: run.QueryRewriteUsed,
		StartedAt:        run.StartedAt,
		ElapsedMs:        run.ElapsedMs,
		Metrics:          run.Metrics,
	}
}

func evalRunQueryRewriteUsed(req model.RunEvalDatasetRequest, defaultEnabled bool) bool {
	if req.EnableQueryRewrite != nil {
		return *req.EnableQueryRewrite
	}
	return defaultEnabled
}

func evalRunSearchModeLabel(actualMode, requestedMode string) string {
	actualMode = strings.TrimSpace(actualMode)
	if actualMode != "" {
		return actualMode
	}
	requestedMode = normalizeRetrievalMode(requestedMode)
	if requestedMode == "hybrid" {
		return "hybrid"
	}
	return "dense"
}

func (s *AppService) saveEvalRun(run model.RunEvalDatasetResponse) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("app service is nil")
	}
	if strings.TrimSpace(run.RunID) == "" {
		run.RunID = util.NextID("eval-run")
	}
	if strings.TrimSpace(run.StartedAt) == "" {
		run.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	run.Cases = cloneEvalRunCaseResults(run.Cases)

	s.state.Mu.Lock()
	if s.state.EvalRuns == nil {
		s.state.EvalRuns = map[string]model.RunEvalDatasetResponse{}
	}
	s.state.EvalRuns[run.RunID] = run
	pruneEvalRunHistoryLocked(s.state.EvalRuns, run.KnowledgeBaseID, maxEvalRunHistoryPerKB)
	s.state.Mu.Unlock()

	return s.saveState()
}

func pruneEvalRunHistoryLocked(runs map[string]model.RunEvalDatasetResponse, knowledgeBaseID string, limit int) {
	if limit <= 0 || knowledgeBaseID == "" {
		return
	}
	items := make([]model.RunEvalDatasetResponse, 0, len(runs))
	for _, run := range runs {
		if run.KnowledgeBaseID == knowledgeBaseID {
			items = append(items, run)
		}
	}
	if len(items) <= limit {
		return
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].StartedAt > items[j].StartedAt
	})
	for _, run := range items[limit:] {
		delete(runs, run.RunID)
	}
}
