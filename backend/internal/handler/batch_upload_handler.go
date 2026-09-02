package handler

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"ai-localbase/internal/auth"
	"ai-localbase/internal/model"
	"ai-localbase/internal/service"

	"github.com/gin-gonic/gin"
)

// BatchIndexRequest 批量索引请求
type BatchIndexRequest struct {
	UploadIDs   []string `json:"uploadIds" binding:"required"`
	Concurrency int      `json:"concurrency,omitempty"` // 并发数，默认3
	Async       bool     `json:"async,omitempty"`
}

// IndexResult 单个文档的索引结果
type IndexResult struct {
	UploadID   string         `json:"uploadId"`
	DocumentID string         `json:"documentId,omitempty"`
	FileName   string         `json:"fileName"`
	Success    bool           `json:"success"`
	ErrorCode  string         `json:"errorCode,omitempty"`
	Error      string         `json:"error,omitempty"`
	Document   model.Document `json:"document,omitempty"`
}

// BatchIndexResponse 批量索引响应
type BatchIndexResponse struct {
	Total      int           `json:"total"`
	Successful int           `json:"successful"`
	Failed     int           `json:"failed"`
	Results    []IndexResult `json:"results"`
	DurationMs int64         `json:"duration_ms"`
	Job        *model.MCPJob `json:"job,omitempty"`
}

// BatchIndexDocuments 批量索引文档
func (h *AppHandler) BatchIndexDocuments(c *gin.Context) {
	knowledgeBaseID := c.Param("id")
	if knowledgeBaseID == "" {
		writeError(c, http.StatusBadRequest, "knowledge base id is required")
		return
	}

	var req BatchIndexRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request: %v", err))
		return
	}

	if len(req.UploadIDs) == 0 {
		writeError(c, http.StatusBadRequest, "uploadIds cannot be empty")
		return
	}
	if err := service.ValidateBatchIndexInputs(req.UploadIDs); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	concurrency, err := service.ValidateMCPBatchConcurrency(req.Concurrency)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.Async {
		job, err := h.appService.StartBatchIndexJobAs(knowledgeBaseID, req.UploadIDs, concurrency, auth.PrincipalFromContext(c))
		if err != nil {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
		c.JSON(http.StatusAccepted, BatchIndexResponse{Total: len(req.UploadIDs), Job: &job})
		return
	}

	start := time.Now()

	// 批量索引
	results := h.batchIndexFromStaged(knowledgeBaseID, req.UploadIDs, concurrency, auth.PrincipalFromContext(c))

	// 统计结果
	successful := 0
	failed := 0
	for _, r := range results {
		if r.Success {
			successful++
		} else {
			failed++
		}
	}

	response := BatchIndexResponse{
		Total:      len(results),
		Successful: successful,
		Failed:     failed,
		Results:    results,
		DurationMs: time.Since(start).Milliseconds(),
	}

	c.JSON(http.StatusOK, response)
}

// batchIndexFromStaged 从暂存文件批量索引
func (h *AppHandler) batchIndexFromStaged(knowledgeBaseID string, uploadIDs []string, concurrency int, owner service.AuthPrincipal) []IndexResult {
	if err := service.ValidateBatchIndexInputs(uploadIDs); err != nil {
		return []IndexResult{{Success: false, ErrorCode: "invalid_argument", Error: err.Error()}}
	}
	validatedConcurrency, err := service.ValidateMCPBatchConcurrency(concurrency)
	if err != nil {
		return []IndexResult{{Success: false, ErrorCode: "invalid_argument", Error: err.Error()}}
	}
	concurrency = validatedConcurrency
	var wg sync.WaitGroup
	resultChan := make(chan IndexResult, len(uploadIDs))
	work := make(chan string)
	workerCount := concurrency
	if len(uploadIDs) < workerCount {
		workerCount = len(uploadIDs)
	}
	for index := 0; index < workerCount; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for uploadID := range work {
				resultChan <- h.indexSingleStaged(knowledgeBaseID, uploadID, owner)
			}
		}()
	}
	go func() {
		for _, uploadID := range uploadIDs {
			work <- uploadID
		}
		close(work)
	}()

	// 等待所有goroutine完成
	wg.Wait()
	close(resultChan)

	// 收集结果
	results := make([]IndexResult, 0, len(uploadIDs))
	for r := range resultChan {
		results = append(results, r)
	}

	return results
}

// indexSingleStaged 索引单个暂存文件
func (h *AppHandler) indexSingleStaged(knowledgeBaseID, uploadID string, owner service.AuthPrincipal) IndexResult {
	// 使用现有的 RegisterStagedUpload 方法
	document, err := h.appService.RegisterStagedUploadAs(context.Background(), uploadID, knowledgeBaseID, "", owner)

	if err != nil {
		code, message := service.PublicIndexFailure(err)
		return IndexResult{
			UploadID:  uploadID,
			FileName:  "",
			Success:   false,
			ErrorCode: code,
			Error:     message,
		}
	}

	return IndexResult{
		UploadID:   uploadID,
		DocumentID: document.ID,
		FileName:   document.Name,
		Success:    true,
		Document:   document,
	}
}

// DocumentIndexStatus 文档索引状态
type DocumentIndexStatus struct {
	DocumentID     string `json:"documentId"`
	Status         string `json:"status"` // processing, indexed, failed
	ChunkCount     int    `json:"chunkCount,omitempty"`
	IndexedAt      string `json:"indexedAt,omitempty"`
	IndexError     string `json:"indexError,omitempty"`
	IndexErrorCode string `json:"indexErrorCode,omitempty"`
	IndexRunID     string `json:"indexRunId,omitempty"`
	IndexVersion   int    `json:"indexVersion,omitempty"`
	ProgressPct    int    `json:"progressPct,omitempty"` // 0-100
}

// GetDocumentIndexStatus 获取文档索引状态
func (h *AppHandler) GetDocumentIndexStatus(c *gin.Context) {
	knowledgeBaseID := c.Param("id")
	documentID := c.Param("documentId")

	if knowledgeBaseID == "" || documentID == "" {
		writeError(c, http.StatusBadRequest, "knowledge base id and document id are required")
		return
	}

	document, err := h.appService.GetDocumentIndexStatus(knowledgeBaseID, documentID)
	if err != nil {
		writeError(c, http.StatusNotFound, "document not found")
		return
	}

	// 构建状态响应
	status := DocumentIndexStatus{
		DocumentID:     document.ID,
		Status:         document.Status,
		ChunkCount:     document.ChunkCount,
		IndexedAt:      document.IndexedAt,
		IndexError:     document.IndexError,
		IndexErrorCode: document.IndexErrorCode,
		IndexRunID:     document.IndexRunID,
		IndexVersion:   document.IndexVersion,
	}

	// 简单的进度估算
	switch document.Status {
	case "processing":
		status.ProgressPct = 50 // 处理中显示50%
	case "indexed":
		status.ProgressPct = 100
	case "failed":
		status.ProgressPct = 0
	default:
		status.ProgressPct = 0
	}

	c.JSON(http.StatusOK, status)
}

// VerifyDocumentIndex checks the durable content snapshot and evidence
// coordinates without exposing the original filesystem path.
func (h *AppHandler) VerifyDocumentIndex(c *gin.Context) {
	verification, err := h.appService.VerifyIndexedDocument(c.Param("id"), c.Param("documentId"))
	if err != nil {
		writeError(c, http.StatusNotFound, "document not found")
		return
	}
	c.JSON(http.StatusOK, verification)
}
