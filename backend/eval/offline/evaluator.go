package offline

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RetrievalFunc 检索函数签名（由调用方注入，可 mock）
// 输入 question，返回检索到的 chunks 和耗时
type RetrievalFunc func(ctx context.Context, question string) (chunks []RetrievedChunkInfo, latency time.Duration, err error)

// GenerationFunc LLM 生成函数签名（由调用方注入，可 mock）
// 输入 question 和上下文 chunks，返回答案和耗时
type GenerationFunc func(ctx context.Context, question string, chunks []RetrievedChunkInfo) (answer string, latency time.Duration, err error)

// EvaluatorConfig 评估器配置
type EvaluatorConfig struct {
	HitThreshold   float64 // 文本匹配阈值，默认 0.5
	MaxConcurrency int     // 并发数，默认 1（串行）
}

// Evaluator 核心评估器
type Evaluator struct {
	retrieval  RetrievalFunc
	generation GenerationFunc
	config     EvaluatorConfig
}

// NewEvaluator 创建评估器
func NewEvaluator(retrieval RetrievalFunc, generation GenerationFunc, cfg EvaluatorConfig) *Evaluator {
	if cfg.HitThreshold == 0 {
		cfg.HitThreshold = 0.5
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 1 // 默认串行
	}
	return &Evaluator{
		retrieval:  retrieval,
		generation: generation,
		config:     cfg,
	}
}

// Run 运行完整评估，返回每个用例结果
func (e *Evaluator) Run(ctx context.Context, dataset *Dataset) ([]CaseResult, error) {
	if dataset == nil || len(dataset.Cases) == 0 {
		return nil, fmt.Errorf("dataset is empty")
	}

	results := make([]CaseResult, len(dataset.Cases))
	workerCount := e.config.MaxConcurrency
	if workerCount > len(dataset.Cases) {
		workerCount = len(dataset.Cases)
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for index := range jobs {
				gtCase := dataset.Cases[index]
				result, err := e.EvaluateCase(ctx, gtCase)
				if err != nil {
					result.Error = err.Error()
				}
				failure := ClassifyFailure(result, gtCase, e.config.HitThreshold)
				result.FailureCategory = failure.Category
				result.FailureReason = failure.Reason
				results[index] = result
			}
		}()
	}

	for index := range dataset.Cases {
		select {
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return nil, ctx.Err()
		case jobs <- index:
		}
	}
	close(jobs)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// EvaluateCase 评估单个用例
func (e *Evaluator) EvaluateCase(ctx context.Context, gt GroundTruthCase) (CaseResult, error) {
	result := CaseResult{
		CaseID:      gt.ID,
		Question:    gt.Question,
		GroundTruth: gt,
		HitRank:     -1, // 默认未命中
	}

	// 1. 执行检索
	chunks, retrievalLatency, err := e.retrieval(ctx, gt.Question)
	result.RetrievalLatency = retrievalLatency
	if err != nil {
		return result, fmt.Errorf("retrieval failed: %w", err)
	}
	result.RetrievedChunks = chunks

	// 2. 执行生成
	answer, generationLatency, err := e.generation(ctx, gt.Question, chunks)
	result.GenerationLatency = generationLatency
	if err != nil {
		return result, fmt.Errorf("generation failed: %w", err)
	}
	result.LLMAnswer = answer
	ApplyFaithfulness(&result)

	// 3. 计算命中指标
	classification := ClassifyHit(result, gt, e.config.HitThreshold)
	result.DocumentHit = classification.DocumentHit
	result.ChunkHit = classification.ChunkHit
	result.AnswerSnippetHit = classification.AnswerSnippetHit
	result.DirectEvidenceHit = classification.DirectEvidenceHit
	if classification.Hit {
		result.HitRank = classification.Rank
		result.ReciprocalRank = 1.0 / float64(classification.Rank)
	} else if !isNoAnswerCase(gt) {
		result.Error = "未命中"
	}
	failure := ClassifyFailure(result, gt, e.config.HitThreshold)
	result.FailureCategory = failure.Category
	result.FailureReason = failure.Reason

	return result, nil
}
