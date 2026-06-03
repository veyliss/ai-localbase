package service

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

const (
	defaultEvalCasesPerDocument = 5
	maxEvalCasesPerDocument     = 20
	maxEvalRunHistoryPerKB      = 50
	maxCrossDocumentEvalCases   = 3
	evalDatasetKindGenerated    = "generated"
	evalDatasetKindReview       = "review"
	evalReviewStatusPending     = "pending"
	evalReviewStatusApproved    = "approved"
)

func (s *AppService) GenerateEvalDataset(req model.GenerateEvalDatasetRequest) (model.GenerateEvalDatasetResponse, error) {
	if s == nil {
		return model.GenerateEvalDatasetResponse{}, fmt.Errorf("app service is nil")
	}

	maxPerDocument := req.MaxPerDocument
	if maxPerDocument <= 0 {
		maxPerDocument = defaultEvalCasesPerDocument
	}
	if maxPerDocument > maxEvalCasesPerDocument {
		maxPerDocument = maxEvalCasesPerDocument
	}

	documents, err := s.evalDatasetDocuments(req)
	if err != nil {
		return model.GenerateEvalDatasetResponse{}, err
	}
	if len(documents) == 0 {
		return model.GenerateEvalDatasetResponse{}, fmt.Errorf("no documents available for eval dataset generation")
	}

	cases := make([]model.EvalGroundTruthCase, 0, len(documents)*maxPerDocument)
	crossDocumentEvidence := make([]evalCrossDocumentEvidence, 0, len(documents))
	for _, document := range documents {
		text, err := util.ExtractDocumentText(document.Path)
		if err != nil {
			return model.GenerateEvalDatasetResponse{}, fmt.Errorf("extract document %s: %w", document.ID, err)
		}
		chunks := s.rag.BuildDocumentChunks(document, text)
		selected := selectEvalChunkCandidates(chunks, len(chunks))
		if evidence, ok := selectCrossDocumentEvalEvidence(document, selected); ok {
			crossDocumentEvidence = append(crossDocumentEvidence, evidence)
		}
		for _, chunk := range selected {
			generated := buildEvalCasesFromChunk(document, chunk, maxPerDocument)
			for _, item := range generated {
				if len(cases) >= len(documents)*maxPerDocument {
					break
				}
				if !validateEvalCase(item, document.Name, chunk.Text) {
					continue
				}
				item.ID = fmt.Sprintf("auto-%s-%03d", sanitizeEvalIDPart(document.ID), len(cases)+1)
				cases = append(cases, item)
				if countEvalCasesForDocument(cases, document.ID) >= maxPerDocument {
					break
				}
			}
			if countEvalCasesForDocument(cases, document.ID) >= maxPerDocument {
				break
			}
		}
	}
	if strings.TrimSpace(req.DocumentID) == "" && len(crossDocumentEvidence) >= 2 {
		generated := buildCrossDocumentEvalCases(crossDocumentEvidence, maxCrossDocumentEvalCases)
		for _, item := range generated {
			if validateEvalCase(item, "", crossDocumentEvalEvidenceText(item, crossDocumentEvidence)) {
				item.ID = fmt.Sprintf("auto-cross-%03d", len(cases)+1)
				cases = append(cases, item)
			}
		}
	}

	if len(cases) == 0 {
		return model.GenerateEvalDatasetResponse{}, fmt.Errorf("no eval cases generated from selected documents")
	}

	datasetKnowledgeBaseID := strings.TrimSpace(req.KnowledgeBaseID)
	if datasetKnowledgeBaseID == "" && len(documents) == 1 {
		datasetKnowledgeBaseID = documents[0].KnowledgeBaseID
	}
	datasetDocumentID := strings.TrimSpace(req.DocumentID)
	now := time.Now().UTC().Format(time.RFC3339)

	dataset := model.EvalDataset{
		ID:              util.NextID("eval"),
		Name:            buildEvalDatasetName(req, documents),
		Kind:            evalDatasetKindGenerated,
		KnowledgeBaseID: datasetKnowledgeBaseID,
		DocumentID:      datasetDocumentID,
		Count:           len(cases),
		DocumentCount:   len(documents),
		CreatedAt:       now,
		UpdatedAt:       now,
		Items:           cloneEvalGroundTruthCases(cases),
	}
	if err := s.saveEvalDataset(dataset); err != nil {
		return model.GenerateEvalDatasetResponse{}, fmt.Errorf("save eval dataset: %w", err)
	}

	return model.GenerateEvalDatasetResponse{
		DatasetID:       dataset.ID,
		KnowledgeBaseID: dataset.KnowledgeBaseID,
		DocumentID:      dataset.DocumentID,
		Count:           len(cases),
		DocumentCount:   len(documents),
		CreatedAt:       dataset.CreatedAt,
		Items:           cloneEvalGroundTruthCases(cases),
	}, nil
}

func (s *AppService) ListEvalDatasets(knowledgeBaseID string) []model.EvalDatasetSummary {
	if s == nil || s.state == nil {
		return nil
	}

	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	s.state.Mu.RLock()
	items := make([]model.EvalDatasetSummary, 0, len(s.state.EvalDatasets))
	for _, dataset := range s.state.EvalDatasets {
		if knowledgeBaseID != "" && dataset.KnowledgeBaseID != knowledgeBaseID {
			continue
		}
		items = append(items, evalDatasetSummary(dataset))
	}
	s.state.Mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		return evalDatasetSortTime(items[i]) > evalDatasetSortTime(items[j])
	})
	return items
}

func (s *AppService) GetEvalDataset(id string) (model.EvalDataset, error) {
	if s == nil || s.state == nil {
		return model.EvalDataset{}, fmt.Errorf("app service is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return model.EvalDataset{}, fmt.Errorf("eval dataset id is required")
	}

	s.state.Mu.RLock()
	dataset, ok := s.state.EvalDatasets[id]
	s.state.Mu.RUnlock()
	if !ok {
		return model.EvalDataset{}, fmt.Errorf("eval dataset not found")
	}
	dataset.Items = cloneEvalGroundTruthCases(dataset.Items)
	return dataset, nil
}

func (s *AppService) AddEvalDatasetCandidate(req model.AddEvalDatasetCandidateRequest) (model.AddEvalDatasetCandidateResponse, error) {
	if s == nil || s.state == nil {
		return model.AddEvalDatasetCandidateResponse{}, fmt.Errorf("app service is nil")
	}

	item, err := normalizeEvalDatasetItemForSave(req.Item, normalizeEvalDatasetItemOptions{
		DefaultAnswerType:   "retrieval-debug-candidate",
		DefaultDifficulty:   "hard",
		DefaultReviewStatus: evalReviewStatusPending,
		ForceDisabled:       true,
		DefaultNotes:        "pending review from retrieval debug",
		RequireReviewNote:   true,
	})
	if err != nil {
		return model.AddEvalDatasetCandidateResponse{}, err
	}

	knowledgeBaseID, documentID, documentCount, datasetName, err := s.resolveEvalCandidateScope(req, item)
	if err != nil {
		return model.AddEvalDatasetCandidateResponse{}, err
	}
	if item.ID == "" {
		item.ID = fmt.Sprintf("manual-%s-%x", sanitizeEvalIDPart(knowledgeBaseID), qdrantPointID(item.Question))
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var previousDataset *model.EvalDataset
	createdDataset := false
	s.state.Mu.Lock()
	if s.state.EvalDatasets == nil {
		s.state.EvalDatasets = map[string]model.EvalDataset{}
	}

	datasetID := ""
	for id, dataset := range s.state.EvalDatasets {
		if dataset.Kind == evalDatasetKindReview &&
			dataset.KnowledgeBaseID == knowledgeBaseID &&
			dataset.DocumentID == documentID {
			datasetID = id
			break
		}
	}

	var dataset model.EvalDataset
	if datasetID == "" {
		dataset = model.EvalDataset{
			ID:              util.NextID("eval"),
			Name:            datasetName,
			Kind:            evalDatasetKindReview,
			KnowledgeBaseID: knowledgeBaseID,
			DocumentID:      documentID,
			DocumentCount:   documentCount,
			CreatedAt:       now,
		}
		datasetID = dataset.ID
		createdDataset = true
	} else {
		dataset = s.state.EvalDatasets[datasetID]
		snapshot := dataset
		snapshot.Items = cloneEvalGroundTruthCases(dataset.Items)
		previousDataset = &snapshot
	}

	replaced := false
	for index, existing := range dataset.Items {
		if existing.ID == item.ID {
			dataset.Items[index] = item
			replaced = true
			break
		}
	}
	if !replaced {
		dataset.Items = append(dataset.Items, item)
	}
	dataset.Count = len(dataset.Items)
	dataset.UpdatedAt = now
	s.state.EvalDatasets[datasetID] = dataset
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		if previousDataset != nil {
			s.state.EvalDatasets[datasetID] = *previousDataset
		} else {
			delete(s.state.EvalDatasets, datasetID)
		}
		s.state.Mu.Unlock()
		return model.AddEvalDatasetCandidateResponse{}, err
	}

	return model.AddEvalDatasetCandidateResponse{
		Dataset: evalDatasetSummary(dataset),
		Item:    item,
		Created: createdDataset || !replaced,
	}, nil
}

func (s *AppService) UpdateEvalDatasetItem(datasetID, itemID string, req model.UpdateEvalDatasetItemRequest) (model.UpdateEvalDatasetItemResponse, error) {
	if s == nil || s.state == nil {
		return model.UpdateEvalDatasetItemResponse{}, fmt.Errorf("app service is nil")
	}
	datasetID = strings.TrimSpace(datasetID)
	itemID = strings.TrimSpace(itemID)
	if datasetID == "" || itemID == "" {
		return model.UpdateEvalDatasetItemResponse{}, fmt.Errorf("eval dataset id and item id are required")
	}

	item, err := normalizeEvalDatasetItemForSave(req.Item, normalizeEvalDatasetItemOptions{
		DefaultAnswerType:   "extractive",
		DefaultDifficulty:   "medium",
		DefaultReviewStatus: evalReviewStatusApproved,
	})
	if err != nil {
		return model.UpdateEvalDatasetItemResponse{}, err
	}
	item.ID = itemID

	now := time.Now().UTC().Format(time.RFC3339)
	var previousDataset model.EvalDataset
	var updatedDataset model.EvalDataset
	found := false
	s.state.Mu.Lock()
	if s.state.EvalDatasets == nil {
		s.state.EvalDatasets = map[string]model.EvalDataset{}
	}
	dataset, ok := s.state.EvalDatasets[datasetID]
	if !ok {
		s.state.Mu.Unlock()
		return model.UpdateEvalDatasetItemResponse{}, fmt.Errorf("eval dataset not found")
	}
	previousDataset = dataset
	previousDataset.Items = cloneEvalGroundTruthCases(dataset.Items)
	for index, existing := range dataset.Items {
		if existing.ID == itemID {
			if len(item.SourceDocuments) == 0 {
				item.SourceDocuments = append([]model.EvalSourceDocument(nil), existing.SourceDocuments...)
			}
			dataset.Items[index] = item
			found = true
			break
		}
	}
	if !found {
		s.state.Mu.Unlock()
		return model.UpdateEvalDatasetItemResponse{}, fmt.Errorf("eval dataset item not found")
	}
	dataset.Count = len(dataset.Items)
	dataset.UpdatedAt = now
	s.state.EvalDatasets[datasetID] = dataset
	updatedDataset = dataset
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		s.state.EvalDatasets[datasetID] = previousDataset
		s.state.Mu.Unlock()
		return model.UpdateEvalDatasetItemResponse{}, err
	}

	return model.UpdateEvalDatasetItemResponse{
		Dataset: evalDatasetSummary(updatedDataset),
		Item:    item,
	}, nil
}

func (s *AppService) DeleteEvalDatasetItem(datasetID, itemID string) (model.DeleteEvalDatasetItemResponse, error) {
	if s == nil || s.state == nil {
		return model.DeleteEvalDatasetItemResponse{}, fmt.Errorf("app service is nil")
	}
	datasetID = strings.TrimSpace(datasetID)
	itemID = strings.TrimSpace(itemID)
	if datasetID == "" || itemID == "" {
		return model.DeleteEvalDatasetItemResponse{}, fmt.Errorf("eval dataset id and item id are required")
	}

	now := time.Now().UTC().Format(time.RFC3339)
	var previousDataset model.EvalDataset
	var updatedDataset model.EvalDataset
	s.state.Mu.Lock()
	if s.state.EvalDatasets == nil {
		s.state.EvalDatasets = map[string]model.EvalDataset{}
	}
	dataset, ok := s.state.EvalDatasets[datasetID]
	if !ok {
		s.state.Mu.Unlock()
		return model.DeleteEvalDatasetItemResponse{}, fmt.Errorf("eval dataset not found")
	}
	previousDataset = dataset
	previousDataset.Items = cloneEvalGroundTruthCases(dataset.Items)
	nextItems := make([]model.EvalGroundTruthCase, 0, len(dataset.Items))
	found := false
	for _, item := range dataset.Items {
		if item.ID == itemID {
			found = true
			continue
		}
		nextItems = append(nextItems, item)
	}
	if !found {
		s.state.Mu.Unlock()
		return model.DeleteEvalDatasetItemResponse{}, fmt.Errorf("eval dataset item not found")
	}
	dataset.Items = nextItems
	dataset.Count = len(nextItems)
	dataset.UpdatedAt = now
	s.state.EvalDatasets[datasetID] = dataset
	updatedDataset = dataset
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		s.state.EvalDatasets[datasetID] = previousDataset
		s.state.Mu.Unlock()
		return model.DeleteEvalDatasetItemResponse{}, err
	}

	return model.DeleteEvalDatasetItemResponse{
		Dataset: evalDatasetSummary(updatedDataset),
		Deleted: itemID,
	}, nil
}

func (s *AppService) DeleteEvalDataset(id string) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("app service is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("eval dataset id is required")
	}

	s.state.Mu.Lock()
	if s.state.EvalDatasets == nil {
		s.state.EvalDatasets = map[string]model.EvalDataset{}
	}
	removed, ok := s.state.EvalDatasets[id]
	if !ok {
		s.state.Mu.Unlock()
		return fmt.Errorf("eval dataset not found")
	}
	delete(s.state.EvalDatasets, id)
	if s.state.EvalRuns == nil {
		s.state.EvalRuns = map[string]model.RunEvalDatasetResponse{}
	}
	removedRuns := make(map[string]model.RunEvalDatasetResponse)
	for runID, run := range s.state.EvalRuns {
		if run.DatasetID == id {
			removedRuns[runID] = run
			delete(s.state.EvalRuns, runID)
		}
	}
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		s.state.EvalDatasets[id] = removed
		for runID, run := range removedRuns {
			s.state.EvalRuns[runID] = run
		}
		s.state.Mu.Unlock()
		return err
	}
	return nil
}

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
			CaseID:         item.ID,
			Question:       item.Question,
			ExpectedAnswer: item.Answer,
			HitRank:        -1,
			ElapsedMs:      response.ElapsedMs,
			LowConfidence:  response.LowConfidence,
			Confidence:     response.Confidence,
			Retrieved:      response.Items,
		}
		if err != nil {
			caseResult.Error = err.Error()
			results = append(results, caseResult)
			continue
		}

		hit, rank, matchedBy := evalCaseHit(item, response.Items)
		caseResult.Hit = hit
		caseResult.HitRank = rank
		caseResult.MatchedBy = matchedBy
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

func evalDatasetSummary(dataset model.EvalDataset) model.EvalDatasetSummary {
	return model.EvalDatasetSummary{
		ID:              dataset.ID,
		Name:            dataset.Name,
		Kind:            dataset.Kind,
		KnowledgeBaseID: dataset.KnowledgeBaseID,
		DocumentID:      dataset.DocumentID,
		Count:           dataset.Count,
		DocumentCount:   dataset.DocumentCount,
		CreatedAt:       dataset.CreatedAt,
		UpdatedAt:       dataset.UpdatedAt,
	}
}

func evalDatasetSortTime(dataset model.EvalDatasetSummary) string {
	if strings.TrimSpace(dataset.UpdatedAt) != "" {
		return dataset.UpdatedAt
	}
	return dataset.CreatedAt
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

func firstEvalSourceKnowledgeBaseID(item model.EvalGroundTruthCase) string {
	for _, source := range item.SourceDocuments {
		if strings.TrimSpace(source.KnowledgeBaseID) != "" {
			return strings.TrimSpace(source.KnowledgeBaseID)
		}
	}
	return ""
}

func evalCaseHit(item model.EvalGroundTruthCase, chunks []model.RetrievalDebugChunk) (bool, int, string) {
	if item.AnswerType == "cross_document" {
		return evalCrossDocumentCaseHit(item, chunks)
	}

	for index, chunk := range chunks {
		for _, source := range item.SourceDocuments {
			if strings.TrimSpace(source.ChunkID) != "" && chunk.ID == source.ChunkID {
				return true, index + 1, "chunk"
			}
		}
	}

	for index, chunk := range chunks {
		for _, source := range item.SourceDocuments {
			if strings.TrimSpace(source.DocumentID) != "" && chunk.DocumentID == source.DocumentID {
				return true, index + 1, "document"
			}
		}
	}

	for index, chunk := range chunks {
		chunkText := strings.ToLower(strings.TrimSpace(chunk.Text))
		if chunkText == "" {
			continue
		}
		for _, snippet := range item.AnswerSnippets {
			normalizedSnippet := strings.ToLower(strings.TrimSpace(snippet))
			if normalizedSnippet != "" && strings.Contains(chunkText, normalizedSnippet) {
				return true, index + 1, "snippet"
			}
		}
	}

	return false, -1, ""
}

func evalCrossDocumentCaseHit(item model.EvalGroundTruthCase, chunks []model.RetrievalDebugChunk) (bool, int, string) {
	required := map[string]model.EvalSourceDocument{}
	for _, source := range item.SourceDocuments {
		documentID := strings.TrimSpace(source.DocumentID)
		if documentID == "" {
			continue
		}
		if _, ok := required[documentID]; !ok {
			required[documentID] = source
		}
	}
	if len(required) < 2 {
		return false, -1, ""
	}

	maxRank := 0
	for documentID, source := range required {
		rank := 0
		for index, chunk := range chunks {
			if strings.TrimSpace(source.ChunkID) != "" && chunk.ID == source.ChunkID {
				rank = index + 1
				break
			}
			if chunk.DocumentID == documentID {
				rank = index + 1
				break
			}
		}
		if rank == 0 {
			return false, -1, ""
		}
		if rank > maxRank {
			maxRank = rank
		}
	}
	return true, maxRank, "cross_document"
}

func buildEvalRunMetrics(results []model.EvalRunCaseResult, skippedDisabled int) model.EvalRunMetrics {
	metrics := model.EvalRunMetrics{
		TotalCases:      len(results),
		SkippedDisabled: skippedDisabled,
	}
	if len(results) == 0 {
		return metrics
	}

	latencies := make([]int64, 0, len(results))
	var reciprocalSum float64
	for _, result := range results {
		if result.Hit {
			metrics.HitCount++
			reciprocalSum += result.ReciprocalRank
		} else {
			metrics.MissCount++
		}
		if result.LowConfidence {
			metrics.LowConfidence++
		}
		if strings.TrimSpace(result.Error) != "" && !result.Hit {
			metrics.ErrorCount++
		}
		latencies = append(latencies, result.ElapsedMs)
	}
	metrics.HitRate = float64(metrics.HitCount) / float64(len(results))
	metrics.MRR = reciprocalSum / float64(len(results))
	metrics.LatencyP50Ms = percentileInt64(latencies, 0.50)
	metrics.LatencyP95Ms = percentileInt64(latencies, 0.95)
	return metrics
}

func percentileInt64(values []int64, percentile float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool {
		return values[i] < values[j]
	})
	index := int(float64(len(values)-1) * percentile)
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func (s *AppService) saveEvalDataset(dataset model.EvalDataset) error {
	if s == nil || s.state == nil {
		return fmt.Errorf("app service is nil")
	}
	if strings.TrimSpace(dataset.ID) == "" {
		return fmt.Errorf("eval dataset id is required")
	}

	dataset.Items = cloneEvalGroundTruthCases(dataset.Items)
	s.state.Mu.Lock()
	if s.state.EvalDatasets == nil {
		s.state.EvalDatasets = map[string]model.EvalDataset{}
	}
	s.state.EvalDatasets[dataset.ID] = dataset
	s.state.Mu.Unlock()

	if err := s.saveState(); err != nil {
		s.state.Mu.Lock()
		delete(s.state.EvalDatasets, dataset.ID)
		s.state.Mu.Unlock()
		return err
	}
	return nil
}

func (s *AppService) evalDatasetDocuments(req model.GenerateEvalDatasetRequest) ([]model.Document, error) {
	knowledgeBaseID := strings.TrimSpace(req.KnowledgeBaseID)
	documentID := strings.TrimSpace(req.DocumentID)

	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	kbs := make([]model.KnowledgeBase, 0, len(s.state.KnowledgeBases))
	if knowledgeBaseID != "" {
		kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
		if !ok {
			return nil, fmt.Errorf("knowledge base not found")
		}
		kbs = append(kbs, kb)
	} else {
		for _, kb := range s.state.KnowledgeBases {
			kbs = append(kbs, kb)
		}
		sort.Slice(kbs, func(i, j int) bool {
			return kbs[i].CreatedAt < kbs[j].CreatedAt
		})
	}

	documents := make([]model.Document, 0)
	for _, kb := range kbs {
		for _, document := range kb.Documents {
			if documentID != "" && document.ID != documentID {
				continue
			}
			if strings.TrimSpace(document.Path) == "" {
				continue
			}
			documents = append(documents, document)
		}
	}
	if documentID != "" && len(documents) == 0 {
		return nil, fmt.Errorf("document not found")
	}
	return documents, nil
}

func buildEvalDatasetName(req model.GenerateEvalDatasetRequest, documents []model.Document) string {
	if strings.TrimSpace(req.DocumentID) != "" && len(documents) == 1 {
		return fmt.Sprintf("评估集 - %s", documents[0].Name)
	}
	if strings.TrimSpace(req.KnowledgeBaseID) != "" {
		return fmt.Sprintf("评估集 - %s", strings.TrimSpace(req.KnowledgeBaseID))
	}
	if len(documents) == 1 {
		return fmt.Sprintf("评估集 - %s", documents[0].Name)
	}
	return "评估集 - 全部知识库"
}

func (s *AppService) resolveEvalCandidateScope(req model.AddEvalDatasetCandidateRequest, item model.EvalGroundTruthCase) (string, string, int, string, error) {
	knowledgeBaseID := strings.TrimSpace(req.KnowledgeBaseID)
	documentID := strings.TrimSpace(req.DocumentID)
	for _, source := range item.SourceDocuments {
		if knowledgeBaseID == "" {
			knowledgeBaseID = strings.TrimSpace(source.KnowledgeBaseID)
		}
		if documentID == "" {
			documentID = strings.TrimSpace(source.DocumentID)
		}
		if knowledgeBaseID != "" && documentID != "" {
			break
		}
	}

	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	if knowledgeBaseID == "" && documentID != "" {
		for _, kb := range s.state.KnowledgeBases {
			for _, document := range kb.Documents {
				if document.ID == documentID {
					knowledgeBaseID = kb.ID
					break
				}
			}
			if knowledgeBaseID != "" {
				break
			}
		}
	}
	if knowledgeBaseID == "" {
		return "", "", 0, "", fmt.Errorf("knowledge base id is required")
	}

	kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
	if !ok {
		return "", "", 0, "", fmt.Errorf("knowledge base not found")
	}

	documentName := ""
	if documentID != "" {
		for _, document := range kb.Documents {
			if document.ID == documentID {
				documentName = document.Name
				break
			}
		}
		if documentName == "" {
			return "", "", 0, "", fmt.Errorf("document not found")
		}
	}

	documentCount := len(kb.Documents)
	datasetName := fmt.Sprintf("待审核评估样本 - %s", kb.Name)
	if documentID != "" {
		documentCount = 1
		datasetName = fmt.Sprintf("待审核评估样本 - %s", documentName)
	}
	return knowledgeBaseID, documentID, documentCount, datasetName, nil
}

type normalizeEvalDatasetItemOptions struct {
	DefaultAnswerType   string
	DefaultDifficulty   string
	DefaultReviewStatus string
	ForceDisabled       bool
	DefaultNotes        string
	RequireReviewNote   bool
}

func normalizeEvalDatasetItemForSave(item model.EvalGroundTruthCase, opts normalizeEvalDatasetItemOptions) (model.EvalGroundTruthCase, error) {
	item.ID = strings.TrimSpace(item.ID)
	item.Question = strings.TrimSpace(item.Question)
	item.Answer = strings.TrimSpace(item.Answer)
	if item.Question == "" {
		return model.EvalGroundTruthCase{}, fmt.Errorf("eval dataset item question is required")
	}

	item.AnswerType = strings.TrimSpace(item.AnswerType)
	if item.AnswerType == "" {
		item.AnswerType = strings.TrimSpace(opts.DefaultAnswerType)
	}
	item.Difficulty = strings.TrimSpace(item.Difficulty)
	if item.Difficulty == "" {
		item.Difficulty = strings.TrimSpace(opts.DefaultDifficulty)
	}
	if item.AnswerType == "" {
		item.AnswerType = "extractive"
	}
	if item.Difficulty == "" {
		item.Difficulty = "medium"
	}

	item.ReviewStatus = normalizeEvalReviewStatus(item.ReviewStatus, opts.DefaultReviewStatus)
	if opts.ForceDisabled {
		item.Disabled = true
	}

	item.AnswerSnippets = normalizeEvalSnippets(item.AnswerSnippets, 220)
	if item.Answer == "" && len(item.AnswerSnippets) == 0 {
		return model.EvalGroundTruthCase{}, fmt.Errorf("eval dataset item answer or snippets are required")
	}

	sources := make([]model.EvalSourceDocument, 0, len(item.SourceDocuments))
	seenSources := map[string]struct{}{}
	for _, source := range item.SourceDocuments {
		source.KnowledgeBaseID = strings.TrimSpace(source.KnowledgeBaseID)
		source.DocumentID = strings.TrimSpace(source.DocumentID)
		source.ChunkID = strings.TrimSpace(source.ChunkID)
		if source.KnowledgeBaseID == "" && source.DocumentID == "" && source.ChunkID == "" {
			continue
		}
		key := source.KnowledgeBaseID + "\x00" + source.DocumentID + "\x00" + source.ChunkID
		if _, ok := seenSources[key]; ok {
			continue
		}
		seenSources[key] = struct{}{}
		sources = append(sources, source)
	}
	item.SourceDocuments = sources

	item.Notes = strings.TrimSpace(item.Notes)
	if item.Notes == "" {
		item.Notes = strings.TrimSpace(opts.DefaultNotes)
	} else if opts.RequireReviewNote && !strings.Contains(strings.ToLower(item.Notes), "review") {
		item.Notes += "; pending review"
	}
	return item, nil
}

func normalizeEvalReviewStatus(status, fallback string) string {
	status = strings.TrimSpace(status)
	switch status {
	case evalReviewStatusPending, evalReviewStatusApproved:
		return status
	}
	fallback = strings.TrimSpace(fallback)
	switch fallback {
	case evalReviewStatusPending, evalReviewStatusApproved:
		return fallback
	default:
		return evalReviewStatusApproved
	}
}

func normalizeEvalSnippets(snippets []string, maxRunes int) []string {
	cleanSnippets := make([]string, 0, len(snippets))
	seen := map[string]struct{}{}
	for _, snippet := range snippets {
		snippet = clipEvalRunes(normalizeEvalWhitespace(snippet), maxRunes)
		if snippet == "" {
			continue
		}
		if _, ok := seen[snippet]; ok {
			continue
		}
		seen[snippet] = struct{}{}
		cleanSnippets = append(cleanSnippets, snippet)
	}
	return cleanSnippets
}

func cloneEvalGroundTruthCases(source []model.EvalGroundTruthCase) []model.EvalGroundTruthCase {
	if len(source) == 0 {
		return nil
	}
	cloned := make([]model.EvalGroundTruthCase, len(source))
	for index, item := range source {
		if item.AnswerSnippets != nil {
			item.AnswerSnippets = append([]string(nil), item.AnswerSnippets...)
		}
		if item.SourceDocuments != nil {
			item.SourceDocuments = append([]model.EvalSourceDocument(nil), item.SourceDocuments...)
		}
		cloned[index] = item
	}
	return cloned
}

func selectEvalChunkCandidates(chunks []DocumentChunk, maxCount int) []DocumentChunk {
	if len(chunks) == 0 || maxCount <= 0 {
		return nil
	}

	candidates := make([]DocumentChunk, 0, len(chunks))
	for _, chunk := range chunks {
		text := normalizeEvalWhitespace(chunk.Text)
		if utf8.RuneCountInString(text) < 40 && !isStructuredEvalChunkKind(chunk.Kind) {
			continue
		}
		if isLowValueEvalChunk(text) {
			continue
		}
		candidates = append(candidates, chunk)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left := evalChunkScore(candidates[i])
		right := evalChunkScore(candidates[j])
		if left == right {
			return candidates[i].Index < candidates[j].Index
		}
		return left > right
	})

	if len(candidates) > maxCount {
		candidates = candidates[:maxCount]
	}
	return candidates
}

func evalChunkScore(chunk DocumentChunk) int {
	text := normalizeEvalWhitespace(chunk.Text)
	score := 0
	if chunk.Kind == "structured_summary" {
		score += 5
	}
	if chunk.Kind == "structured_row" {
		score += 4
	}
	if regexp.MustCompile(`什么是|是指|指的是|简介|概述|介绍`).MatchString(text) {
		score += 4
	}
	if regexp.MustCompile(`包括|支持|提供|具有|分为|涵盖|用于|负责`).MatchString(text) {
		score += 3
	}
	if regexp.MustCompile(`流程|步骤|机制|策略|配置`).MatchString(text) {
		score += 3
	}
	if regexp.MustCompile(`\d+\s*(个|条|项|种|年|%|ms|秒|页|MB|GB)?`).MatchString(text) {
		score += 2
	}
	if utf8.RuneCountInString(text) >= 100 {
		score++
	}
	return score
}

func buildEvalCasesFromChunk(document model.Document, chunk DocumentChunk, limit int) []model.EvalGroundTruthCase {
	if limit <= 0 {
		return nil
	}

	var cases []model.EvalGroundTruthCase
	switch chunk.Kind {
	case "structured_summary":
		cases = append(cases, buildStructuredSummaryEvalCases(document, chunk)...)
	case "structured_row":
		cases = append(cases, buildStructuredRowEvalCases(document, chunk)...)
	default:
		cases = append(cases, buildStructuredHeaderEvalCases(document, chunk)...)
		cases = append(cases, buildHeadingTextEvalCases(document, chunk)...)
		cases = append(cases, buildKeywordTextEvalCases(document, chunk)...)
	}

	if len(cases) > limit {
		cases = cases[:limit]
	}
	return cases
}

func buildStructuredSummaryEvalCases(document model.Document, chunk DocumentChunk) []model.EvalGroundTruthCase {
	lines := evalEvidenceLines(chunk.Text)
	cases := make([]model.EvalGroundTruthCase, 0, len(lines))
	fileName := document.Name

	rowCountPattern := regexp.MustCompile(`^统计摘要：文件《([^》]+)》(?:工作表《([^》]+)》)?共有(\d+)条数据记录。?$`)
	numberPattern := regexp.MustCompile(`^统计摘要：字段“([^”]+)”为数值列，非空值(\d+)个，最小值([^，。]+)，最大值([^，。]+)，平均值([^，。]+)。?$`)
	categoryPattern := regexp.MustCompile(`^统计摘要：字段“([^”]+)”为类别列，共(\d+)个非空值，主要分布为：(.+?)。?$`)

	for _, line := range lines {
		if match := rowCountPattern.FindStringSubmatch(line); len(match) == 4 {
			fileName = strings.TrimSpace(match[1])
			scope := evalFileScope(fileName, match[2])
			cases = append(cases, newEvalCase(document, chunk, fmt.Sprintf("%s共有多少条数据记录？", scope), fmt.Sprintf("共有%s条数据记录。", match[3]), []string{line}, "numeric", "easy", "structured summary row count"))
			continue
		}

		if match := numberPattern.FindStringSubmatch(line); len(match) == 6 {
			field := strings.TrimSpace(match[1])
			scope := evalFileScope(fileName, "")
			cases = append(cases,
				newEvalCase(document, chunk, fmt.Sprintf("%s中“%s”的最大值是多少？", scope, field), fmt.Sprintf("“%s”的最大值是%s。", field, strings.TrimSpace(match[4])), []string{line}, "numeric", "easy", "structured summary max"),
				newEvalCase(document, chunk, fmt.Sprintf("%s中“%s”的最小值是多少？", scope, field), fmt.Sprintf("“%s”的最小值是%s。", field, strings.TrimSpace(match[3])), []string{line}, "numeric", "easy", "structured summary min"),
				newEvalCase(document, chunk, fmt.Sprintf("%s中“%s”的平均值是多少？", scope, field), fmt.Sprintf("“%s”的平均值是%s。", field, strings.TrimSpace(match[5])), []string{line}, "numeric", "medium", "structured summary average"),
			)
			continue
		}

		if match := categoryPattern.FindStringSubmatch(line); len(match) == 4 {
			field := strings.TrimSpace(match[1])
			distribution := strings.TrimSpace(match[3])
			scope := evalFileScope(fileName, "")
			cases = append(cases, newEvalCase(document, chunk, fmt.Sprintf("%s中“%s”的主要分布是什么？", scope, field), fmt.Sprintf("“%s”的主要分布为：%s。", field, distribution), []string{line}, "listing", "medium", "structured summary distribution"))
		}
	}

	return cases
}

func buildStructuredHeaderEvalCases(document model.Document, chunk DocumentChunk) []model.EvalGroundTruthCase {
	lines := evalEvidenceLines(chunk.Text)
	headerPattern := regexp.MustCompile(`^文件：(.+?)。(?:工作表：(.+?)。)?字段：(.+?)。数据行数：(\d+)。?$`)
	cases := make([]model.EvalGroundTruthCase, 0, 2)
	for _, line := range lines {
		match := headerPattern.FindStringSubmatch(line)
		if len(match) != 5 {
			continue
		}
		fileName := strings.TrimSpace(match[1])
		sheetName := strings.TrimSpace(match[2])
		fields := strings.TrimSpace(match[3])
		rowCount := strings.TrimSpace(match[4])
		scope := evalFileScope(fileName, sheetName)
		cases = append(cases,
			newEvalCase(document, chunk, fmt.Sprintf("%s包含哪些字段？", scope), fmt.Sprintf("字段包括：%s。", fields), []string{line}, "listing", "easy", "structured header fields"),
			newEvalCase(document, chunk, fmt.Sprintf("%s的数据行数是多少？", scope), fmt.Sprintf("数据行数是%s。", rowCount), []string{line}, "numeric", "easy", "structured header row count"),
		)
		break
	}
	return cases
}

func buildStructuredRowEvalCases(document model.Document, chunk DocumentChunk) []model.EvalGroundTruthCase {
	lines := evalEvidenceLines(chunk.Text)
	cases := make([]model.EvalGroundTruthCase, 0, 6)
	for _, line := range lines {
		rowNumber, fields, ok := parseStructuredEvalRow(line)
		if !ok {
			continue
		}
		for _, field := range selectStructuredEvalRowFields(fields, 2) {
			question := fmt.Sprintf("《%s》第%s行的“%s”是什么？", document.Name, rowNumber, field.name)
			answer := strings.TrimSpace(field.value)
			cases = append(cases, newEvalCase(document, chunk, question, answer, []string{line}, classifyEvalAnswerType(answer), "easy", "structured row field"))
		}
		if len(cases) >= 6 {
			break
		}
	}
	return cases
}

func buildHeadingTextEvalCases(document model.Document, chunk DocumentChunk) []model.EvalGroundTruthCase {
	lines := strings.Split(strings.ReplaceAll(chunk.Text, "\r\n", "\n"), "\n")
	for index, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		heading := strings.TrimSpace(regexp.MustCompile(`^#+\s*`).ReplaceAllString(line, ""))
		heading = strings.Trim(heading, "：:。；，、[]（）()")
		if utf8.RuneCountInString(heading) < 2 || utf8.RuneCountInString(heading) > 40 {
			continue
		}
		answerParts := make([]string, 0, 3)
		for next := index + 1; next < len(lines); next++ {
			part := strings.TrimSpace(lines[next])
			if part == "" {
				if len(answerParts) > 0 {
					break
				}
				continue
			}
			if strings.HasPrefix(part, "#") {
				break
			}
			part = strings.Trim(part, "-*• \t")
			if part != "" {
				answerParts = append(answerParts, part)
			}
			if len(answerParts) >= 3 {
				break
			}
		}
		answer := clipEvalRunes(normalizeEvalWhitespace(strings.Join(answerParts, "\n")), 260)
		if utf8.RuneCountInString(answer) < 24 {
			continue
		}
		question := fmt.Sprintf("文档《%s》中“%s”部分说明了什么？", document.Name, heading)
		return []model.EvalGroundTruthCase{newEvalCase(document, chunk, question, answer, []string{answer}, classifyEvalAnswerType(answer), classifyEvalDifficulty(answer), "heading section extract")}
	}
	return nil
}

func buildKeywordTextEvalCases(document model.Document, chunk DocumentChunk) []model.EvalGroundTruthCase {
	text := normalizeEvalWhitespace(chunk.Text)
	if utf8.RuneCountInString(text) < 60 {
		return nil
	}

	keywords := selectEvalQuestionKeywords(text, 2)
	fuzzyQuestions := selectEvalFuzzyQuestions(text, document.Name, 2)
	if len(keywords) == 0 && len(fuzzyQuestions) == 0 {
		return nil
	}

	answer := clipEvalRunes(text, 260)
	if utf8.RuneCountInString(answer) < 40 {
		return nil
	}

	cases := make([]model.EvalGroundTruthCase, 0, len(keywords)+len(fuzzyQuestions))
	seenQuestions := map[string]struct{}{}
	addCase := func(question, difficulty, note string) {
		key := normalizeEvalComparable(question)
		if key == "" {
			return
		}
		if _, ok := seenQuestions[key]; ok {
			return
		}
		seenQuestions[key] = struct{}{}
		cases = append(cases, newEvalCase(document, chunk, question, answer, []string{answer}, classifyEvalAnswerType(answer), difficulty, note))
	}
	for _, keyword := range keywords {
		question := fmt.Sprintf("文档《%s》中与“%s”相关的内容是什么？", document.Name, keyword)
		addCase(question, "medium", "keyword text extract")
	}
	for _, question := range fuzzyQuestions {
		addCase(question, "hard", "fuzzy intent text extract")
	}
	return cases
}

type evalCrossDocumentEvidence struct {
	document model.Document
	chunk    DocumentChunk
	keyword  string
	answer   string
}

func selectCrossDocumentEvalEvidence(document model.Document, chunks []DocumentChunk) (evalCrossDocumentEvidence, bool) {
	for _, chunk := range chunks {
		text := normalizeEvalWhitespace(chunk.Text)
		if utf8.RuneCountInString(text) < 60 {
			continue
		}
		keywords := selectEvalQuestionKeywords(text, 1)
		if len(keywords) == 0 {
			continue
		}
		answer := clipEvalRunes(text, 220)
		if utf8.RuneCountInString(answer) < 40 {
			continue
		}
		return evalCrossDocumentEvidence{
			document: document,
			chunk:    chunk,
			keyword:  keywords[0],
			answer:   answer,
		}, true
	}
	return evalCrossDocumentEvidence{}, false
}

func buildCrossDocumentEvalCases(evidence []evalCrossDocumentEvidence, limit int) []model.EvalGroundTruthCase {
	if limit <= 0 || len(evidence) < 2 {
		return nil
	}

	cases := make([]model.EvalGroundTruthCase, 0, limit)
	for leftIndex := 0; leftIndex < len(evidence); leftIndex++ {
		for rightIndex := leftIndex + 1; rightIndex < len(evidence); rightIndex++ {
			left := evidence[leftIndex]
			right := evidence[rightIndex]
			question := fmt.Sprintf("对比文档《%s》和《%s》中与“%s”和“%s”相关的内容，分别提到了什么？", left.document.Name, right.document.Name, left.keyword, right.keyword)
			answer := strings.Join([]string{
				fmt.Sprintf("《%s》：%s", left.document.Name, left.answer),
				fmt.Sprintf("《%s》：%s", right.document.Name, right.answer),
			}, "\n")
			item := model.EvalGroundTruthCase{
				Question:       question,
				Answer:         answer,
				AnswerSnippets: normalizeEvalSnippets([]string{left.answer, right.answer}, 220),
				SourceDocuments: []model.EvalSourceDocument{
					{
						KnowledgeBaseID: left.document.KnowledgeBaseID,
						DocumentID:      left.document.ID,
						ChunkID:         left.chunk.ID,
					},
					{
						KnowledgeBaseID: right.document.KnowledgeBaseID,
						DocumentID:      right.document.ID,
						ChunkID:         right.chunk.ID,
					},
				},
				AnswerType:   "cross_document",
				Difficulty:   "hard",
				ReviewStatus: evalReviewStatusApproved,
				Notes:        fmt.Sprintf("auto-generated from %s and %s; cross document comparison", left.document.Name, right.document.Name),
			}
			cases = append(cases, item)
			if len(cases) >= limit {
				return cases
			}
		}
	}
	return cases
}

func crossDocumentEvalEvidenceText(item model.EvalGroundTruthCase, evidence []evalCrossDocumentEvidence) string {
	parts := []string{item.Answer}
	for _, source := range item.SourceDocuments {
		for _, candidate := range evidence {
			if candidate.document.ID != source.DocumentID {
				continue
			}
			parts = append(parts, candidate.document.Name, candidate.answer, candidate.chunk.Text)
			break
		}
	}
	return strings.Join(parts, "\n")
}

func selectEvalQuestionKeywords(text string, limit int) []string {
	if limit <= 0 {
		return nil
	}

	patterns := []string{
		`知识库`,
		`检索(?:增强|调试|策略|结果|质量)?`,
		`评估(?:集|报告|指标|运行)?`,
		`混合检索`,
		`向量检索`,
		`结构化(?:数据|查询|表格)?`,
		`MCP(?:\s*Server)?`,
		`Docker(?:\s*Compose)?`,
		`配置(?:项|参数)?`,
		`权限(?:控制|配置)?`,
		`审计(?:日志)?`,
		`索引(?:状态|重建|任务)?`,
		`上传(?:任务|状态)?`,
		`低置信`,
		`RAG`,
		`Qdrant`,
		`Ollama`,
	}

	keywords := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for _, pattern := range patterns {
		matches := regexp.MustCompile(pattern).FindAllString(text, -1)
		for _, match := range matches {
			keyword := normalizeEvalWhitespace(match)
			key := normalizeEvalComparable(keyword)
			if keyword == "" || key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			keywords = append(keywords, keyword)
			if len(keywords) >= limit {
				return keywords
			}
		}
	}
	return keywords
}

func selectEvalFuzzyQuestions(text, documentName string, limit int) []string {
	if limit <= 0 {
		return nil
	}

	candidates := []struct {
		pattern  string
		question string
	}{
		{
			pattern:  `检索|向量检索|混合检索|召回|命中|低置信`,
			question: fmt.Sprintf("文档《%s》中如何改进检索命中效果？", documentName),
		},
		{
			pattern:  `评估|评估集|评估报告|指标|Hit Rate|MRR`,
			question: fmt.Sprintf("文档《%s》中如何验证知识库回答质量？", documentName),
		},
		{
			pattern:  `索引|重建|上传|文档`,
			question: fmt.Sprintf("文档《%s》中如何维护文档索引？", documentName),
		},
		{
			pattern:  `MCP|权限|审计|token|工具`,
			question: fmt.Sprintf("文档《%s》中如何控制外部工具调用？", documentName),
		},
		{
			pattern:  `结构化|表格|字段|数据`,
			question: fmt.Sprintf("文档《%s》中如何处理结构化数据查询？", documentName),
		},
	}

	questions := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if !regexp.MustCompile(candidate.pattern).MatchString(text) {
			continue
		}
		key := normalizeEvalComparable(candidate.question)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		questions = append(questions, candidate.question)
		if len(questions) >= limit {
			return questions
		}
	}
	return questions
}

func classifyEvalAnswerType(text string) string {
	switch {
	case regexp.MustCompile(`\d+\s*(个|条|项|种|年|%|ms|秒|页|MB|GB)?`).MatchString(text):
		return "numeric"
	case regexp.MustCompile(`流程|步骤|阶段`).MatchString(text):
		return "process"
	case regexp.MustCompile(`包括|支持|提供|具有|涵盖|分为`).MatchString(text):
		return "listing"
	default:
		return "extractive"
	}
}

func classifyEvalDifficulty(text string) string {
	length := utf8.RuneCountInString(normalizeEvalWhitespace(text))
	switch {
	case length >= 220:
		return "hard"
	case length >= 120:
		return "medium"
	default:
		return "easy"
	}
}

func isLowValueEvalChunk(text string) bool {
	if strings.HasPrefix(text, "|") && strings.Contains(text, "---") {
		return true
	}
	return regexp.MustCompile(`^(目录|参考|附录|版权|免责声明)$`).MatchString(text)
}

func isStructuredEvalChunkKind(kind string) bool {
	return kind == "structured_summary" || kind == "structured_row"
}

func newEvalCase(document model.Document, chunk DocumentChunk, question, answer string, snippets []string, answerType, difficulty, note string) model.EvalGroundTruthCase {
	return model.EvalGroundTruthCase{
		Question:       strings.TrimSpace(question),
		Answer:         strings.TrimSpace(answer),
		AnswerSnippets: normalizeEvalSnippets(snippets, 220),
		SourceDocuments: []model.EvalSourceDocument{{
			KnowledgeBaseID: document.KnowledgeBaseID,
			DocumentID:      document.ID,
			ChunkID:         chunk.ID,
		}},
		AnswerType:   strings.TrimSpace(answerType),
		Difficulty:   strings.TrimSpace(difficulty),
		ReviewStatus: evalReviewStatusApproved,
		Notes:        fmt.Sprintf("auto-generated from %s; %s", document.Name, note),
	}
}

func validateEvalCase(item model.EvalGroundTruthCase, documentName, sourceText string) bool {
	if strings.TrimSpace(item.Question) == "" || strings.TrimSpace(item.Answer) == "" {
		return false
	}
	if len(item.AnswerSnippets) == 0 || len(item.SourceDocuments) == 0 {
		return false
	}
	if regexp.MustCompile(`主要讲了什么|包括哪些要点|有哪些内容`).MatchString(item.Question) {
		return false
	}

	evidence := normalizeEvalComparable(sourceText + "\n" + documentName)
	for _, snippet := range item.AnswerSnippets {
		normalizedSnippet := normalizeEvalComparable(snippet)
		if normalizedSnippet == "" || (!strings.Contains(evidence, normalizedSnippet) && !strings.Contains(normalizeEvalComparable(item.Answer), normalizedSnippet)) {
			return false
		}
	}
	if !evalAnswerSupportedByEvidence(item.Answer, evidence) {
		return false
	}
	for _, term := range evalQuotedTerms(item.Question) {
		if !strings.Contains(evidence, normalizeEvalComparable(term)) {
			return false
		}
	}
	return true
}

func evalAnswerSupportedByEvidence(answer, evidence string) bool {
	normalizedAnswer := normalizeEvalComparable(answer)
	if normalizedAnswer == "" {
		return false
	}
	if strings.Contains(evidence, normalizedAnswer) {
		return true
	}

	terms := evalCriticalAnswerTerms(answer)
	if len(terms) == 0 {
		return false
	}
	for _, term := range terms {
		if !strings.Contains(evidence, normalizeEvalComparable(term)) {
			return false
		}
	}
	return true
}

func evalCriticalAnswerTerms(answer string) []string {
	terms := make([]string, 0)
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := normalizeEvalComparable(value)
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		terms = append(terms, value)
	}
	for _, term := range evalQuotedTerms(answer) {
		add(term)
	}
	for _, match := range regexp.MustCompile(`\d+(?:\.\d+)?`).FindAllString(answer, -1) {
		add(match)
	}
	if len(terms) == 0 && utf8.RuneCountInString(strings.TrimSpace(answer)) <= 30 {
		add(answer)
	}
	return terms
}

func evalQuotedTerms(text string) []string {
	matches := regexp.MustCompile(`[《“]([^》”]{1,60})[》”]`).FindAllStringSubmatch(text, -1)
	terms := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			terms = append(terms, strings.TrimSpace(match[1]))
		}
	}
	return terms
}

func evalEvidenceLines(text string) []string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = normalizeEvalWhitespace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func evalFileScope(fileName, sheetName string) string {
	fileName = strings.TrimSpace(fileName)
	sheetName = strings.TrimSpace(sheetName)
	if fileName == "" {
		fileName = "该文件"
	}
	if sheetName != "" {
		return fmt.Sprintf("《%s》工作表《%s》", fileName, sheetName)
	}
	return fmt.Sprintf("《%s》", fileName)
}

type structuredEvalRowField struct {
	name  string
	value string
}

func parseStructuredEvalRow(line string) (string, []structuredEvalRowField, bool) {
	match := regexp.MustCompile(`^第(\d+)行：(.+)$`).FindStringSubmatch(strings.TrimSpace(line))
	if len(match) != 3 {
		return "", nil, false
	}
	rowNumber := strings.TrimSpace(match[1])
	body := strings.TrimSpace(match[2])
	parts := regexp.MustCompile(`[。；;]\s*`).Split(body, -1)
	fields := make([]structuredEvalRowField, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pair := strings.SplitN(part, "：", 2)
		if len(pair) != 2 {
			pair = strings.SplitN(part, ":", 2)
		}
		if len(pair) != 2 {
			continue
		}
		name := strings.TrimSpace(pair[0])
		value := strings.TrimSpace(pair[1])
		if name == "" || value == "" || name == "工作表" {
			continue
		}
		fields = append(fields, structuredEvalRowField{name: name, value: value})
	}
	return rowNumber, fields, len(fields) > 0
}

func selectStructuredEvalRowFields(fields []structuredEvalRowField, limit int) []structuredEvalRowField {
	if len(fields) == 0 || limit <= 0 {
		return nil
	}
	preferredNames := []string{"姓名", "名称", "标题", "职称", "编号", "教师编号", "手机号", "薪资", "年龄", "教龄", "状态", "类别"}
	selected := make([]structuredEvalRowField, 0, limit)
	used := map[int]struct{}{}
	for _, preferred := range preferredNames {
		for index, field := range fields {
			if _, ok := used[index]; ok {
				continue
			}
			if strings.Contains(field.name, preferred) {
				selected = append(selected, field)
				used[index] = struct{}{}
				if len(selected) >= limit {
					return selected
				}
			}
		}
	}
	for index, field := range fields {
		if _, ok := used[index]; ok {
			continue
		}
		selected = append(selected, field)
		if len(selected) >= limit {
			break
		}
	}
	return selected
}

func countEvalCasesForDocument(cases []model.EvalGroundTruthCase, documentID string) int {
	count := 0
	for _, item := range cases {
		for _, source := range item.SourceDocuments {
			if source.DocumentID == documentID {
				count++
				break
			}
		}
	}
	return count
}

func normalizeEvalComparable(text string) string {
	text = normalizeEvalWhitespace(text)
	text = strings.ReplaceAll(text, " ", "")
	return text
}

func normalizeEvalWhitespace(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = regexp.MustCompile(`[ \t]+`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

func clipEvalRunes(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit]))
}

func sanitizeEvalIDPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "case"
	}
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == '-', r == '_', r == '.', r == ' ':
			if builder.Len() == 0 || lastDash {
				continue
			}
			builder.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(builder.String(), "-")
	if out == "" {
		return "case"
	}
	return out
}
