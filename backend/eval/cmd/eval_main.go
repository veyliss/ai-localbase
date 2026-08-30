package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"ai-localbase/eval/offline"
	"ai-localbase/eval/report"
	"ai-localbase/internal/config"
	"ai-localbase/internal/model"
	"ai-localbase/internal/service"
)

type realEvalRuntime struct {
	appService      *service.AppService
	llmService      *service.LLMService
	casesByQuestion map[string]offline.GroundTruthCase
	realLLM         bool
	overrides       evalOverrides
	cleanup         func()
}

func main() {
	var (
		dataset                        = flag.String("dataset", "eval/data/ground_truth_v1.small.json", "评估数据集 JSON 文件路径")
		outputDir                      = flag.String("output", "eval/results", "报告输出目录")
		hitThreshold                   = flag.Float64("hit-threshold", 0.5, "命中文本匹配阈值 (0-1)")
		mockMode                       = flag.Bool("mock", true, "使用 mock 检索和生成函数（用于 CI/测试）")
		realLLM                        = flag.Bool("real-llm", false, "真实模式下调用真实 LLM 生成答案")
		runPrefix                      = flag.String("run-prefix", "", "报告运行前缀；默认 mock 为 eval，真实模式为 baseline")
		runLabel                       = flag.String("run-label", "", "报告运行标签；会追加到报告文件名中")
		evalKnowledgeBaseID            = flag.String("eval-kb-id", "", "真实模式下覆盖评估知识库 ID")
		retrievalTopKDocument          = flag.Int("retrieval-topk-document", -1, "真实模式下覆盖文档范围 finalTopK")
		retrievalCandidateTopKDocument = flag.Int("retrieval-candidate-topk-document", -1, "真实模式下覆盖文档范围 candidateTopK")
		retrievalTopKKnowledgeBase     = flag.Int("retrieval-topk-kb", -1, "真实模式下覆盖知识库范围 finalTopK")
		retrievalCandidateTopKAllDocs  = flag.Int("retrieval-candidate-topk-all-docs", -1, "真实模式下覆盖知识库范围 candidateTopK")
		retrievalMaxChunksPerDocument  = flag.Int("retrieval-max-chunks-per-document", -1, "真实模式下覆盖每文档最大 chunk 数")
		retrievalMaxContextChars       = flag.Int("retrieval-max-context-chars", -1, "真实模式下覆盖上下文最大字符数")
		retrievalAutoExpand            = flag.String("retrieval-auto-expand", "", "真实模式下覆盖自动扩召回开关，可选 true/false")
		retrievalSearchMode            = flag.String("retrieval-search-mode", "", "真实模式下覆盖检索模式，可选 auto/dense/hybrid")
		retrievalRerankStrategy        = flag.String("retrieval-rerank-strategy", "", "真实模式下覆盖重排策略，可选 keyword/semantic")
		retrievalQueryRewrite          = flag.String("retrieval-query-rewrite", "", "真实模式下覆盖查询改写开关，可选 true/false")
		retrievalQueryRewriteVariants  = flag.Int("retrieval-query-rewrite-max-variants", -1, "真实模式下覆盖查询改写最大变体数")
		evalEmbeddingBaseURL           = flag.String("eval-embedding-base-url", "", "真实模式下覆盖评估请求使用的 Embedding Base URL")
		evalChatBaseURL                = flag.String("eval-chat-base-url", "", "真实模式下覆盖评估请求使用的 Chat Base URL")
		evalPathMap                    = flag.String("eval-path-map", "", "真实模式下临时映射 app-state 中的文档路径，格式 from=to，多个映射用逗号分隔")
		evalFixtureManifest            = flag.String("eval-fixture-manifest", "auto", "评测 fixture manifest 路径；auto 会为 public-v1 数据集自动发现，none 表示关闭")
		evalAllowMissingSources        = flag.Bool("eval-allow-missing-sources", false, "真实模式下允许数据集 source_documents 引用不存在的知识库或文档")
		evalAllowFixtureMismatch       = flag.Bool("eval-allow-fixture-mismatch", false, "允许 fixture 与当前索引不一致并继续评估；结果会被标记为不可信")
		includeDisabled                = flag.Bool("include-disabled", false, "包含 disabled 或 rejected 的评估用例；默认只运行启用样本")
		evalConcurrency                = flag.Int("eval-concurrency", 1, "评估并发数；默认 1，适合真实模式逐步放大")
	)
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("[eval] 开始评估，数据集: %s", *dataset)

	ds, err := offline.LoadDataset(*dataset)
	if err != nil {
		log.Fatalf("[eval] 加载数据集失败: %v", err)
	}
	if err := ds.Validate(); err != nil {
		log.Fatalf("[eval] 数据集验证失败: %v", err)
	}
	fixtureManifestPath, err := resolveEvalFixtureManifestPath(*evalFixtureManifest, *dataset)
	if err != nil {
		log.Fatalf("[eval] 解析 fixture manifest 失败: %v", err)
	}
	var fixtureManifest *offline.FixtureManifest
	if fixtureManifestPath != "" {
		fixtureManifest, err = offline.LoadFixtureManifest(fixtureManifestPath)
		if err != nil {
			log.Fatalf("[eval] 加载 fixture manifest 失败: %v", err)
		}
		if err := fixtureManifest.ValidateDataset(fixtureManifestPath, ds); err != nil {
			log.Fatalf("[eval] fixture manifest 与数据集不一致: %v", err)
		}
		log.Printf("[eval] 已加载 fixture manifest: %s (version=%s)", fixtureManifestPath, fixtureManifest.Version)
	} else {
		log.Printf("[eval] 未启用 fixture manifest 校验")
	}
	if !*includeDisabled {
		before := len(ds.Cases)
		ds = ds.EnabledCases()
		log.Printf("[eval] 已过滤禁用样本: %d -> %d", before, len(ds.Cases))
		if len(ds.Cases) == 0 {
			log.Fatalf("[eval] 过滤禁用样本后没有可运行用例")
		}
	}
	log.Printf("[eval] 已加载 %d 个用例", len(ds.Cases))

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		log.Fatalf("[eval] 创建输出目录失败 (%s): %v", *outputDir, err)
	}

	var retrievalFn offline.RetrievalFunc
	var generationFn offline.GenerationFunc
	defaultRunPrefix := "eval"

	if *mockMode {
		log.Println("[eval] 使用 mock 模式")
		retrievalFn = mockRetrieval
		generationFn = mockGeneration
	} else {
		log.Printf("[eval] 使用真实模式，real-llm=%v", *realLLM)
		overrides, err := buildEvalOverrides(evalOverridesInput{
			knowledgeBaseID:                *evalKnowledgeBaseID,
			retrievalTopKDocument:          *retrievalTopKDocument,
			retrievalCandidateTopKDocument: *retrievalCandidateTopKDocument,
			retrievalTopKKnowledgeBase:     *retrievalTopKKnowledgeBase,
			retrievalCandidateTopKAllDocs:  *retrievalCandidateTopKAllDocs,
			retrievalMaxChunksPerDocument:  *retrievalMaxChunksPerDocument,
			retrievalMaxContextChars:       *retrievalMaxContextChars,
			retrievalAutoExpand:            *retrievalAutoExpand,
			retrievalSearchMode:            *retrievalSearchMode,
			retrievalRerankStrategy:        *retrievalRerankStrategy,
			retrievalQueryRewrite:          *retrievalQueryRewrite,
			retrievalQueryRewriteVariants:  *retrievalQueryRewriteVariants,
			evalEmbeddingBaseURL:           *evalEmbeddingBaseURL,
			evalChatBaseURL:                *evalChatBaseURL,
			evalPathMap:                    *evalPathMap,
			evalAllowFixtureMismatch:       *evalAllowFixtureMismatch,
			evalAllowMissingSources:        *evalAllowMissingSources,
		})
		if err != nil {
			log.Fatalf("[eval] 解析评估参数覆盖失败: %v", err)
		}
		runtime, err := newRealEvalRuntime(context.Background(), ds, *realLLM, overrides, fixtureManifestPath, fixtureManifest)
		if err != nil {
			log.Fatalf("[eval] 初始化真实评估模式失败: %v", err)
		}
		defer runtime.Close()
		log.Printf("[eval] 真实模式配置: evalKnowledgeBaseID=%q retrieval(topKDoc=%d candidateDoc=%d topKKB=%d candidateAll=%d perDocLimit=%d maxContextChars=%d autoExpand=%v)",
			runtime.appService.ServerConfig().EvalKnowledgeBaseID,
			runtime.appService.ServerConfig().RetrievalTopKDocument,
			runtime.appService.ServerConfig().RetrievalCandidateTopKDocument,
			runtime.appService.ServerConfig().RetrievalTopKKnowledgeBase,
			runtime.appService.ServerConfig().RetrievalCandidateTopKAllDocs,
			runtime.appService.ServerConfig().RetrievalMaxChunksPerDocument,
			runtime.appService.ServerConfig().RetrievalMaxContextChars,
			runtime.appService.ServerConfig().RetrievalEnableAutoExpand,
		)
		log.Printf("[eval] 真实模式策略覆盖: searchMode=%q rerankStrategy=%q queryRewrite=%s queryRewriteMaxVariants=%d",
			overrides.retrievalSearchMode,
			overrides.retrievalRerankStrategy,
			formatOptionalBool(overrides.retrievalQueryRewrite),
			overrides.retrievalQueryRewriteVariants,
		)
		log.Printf("[eval] 真实模式模型覆盖: embeddingBaseURL=%q chatBaseURL=%q",
			overrides.evalEmbeddingBaseURL,
			overrides.evalChatBaseURL,
		)
		if len(overrides.evalPathMaps) > 0 {
			log.Printf("[eval] 真实模式路径映射: %s", formatEvalPathMaps(overrides.evalPathMaps))
		}
		retrievalFn = runtime.retrieval
		generationFn = runtime.generation
		defaultRunPrefix = "baseline"
	}

	cfg := offline.EvaluatorConfig{
		HitThreshold:   *hitThreshold,
		MaxConcurrency: *evalConcurrency,
	}
	log.Printf("[eval] 评估并发数: %d", cfg.MaxConcurrency)
	evaluator := offline.NewEvaluator(retrievalFn, generationFn, cfg)

	ctx := context.Background()
	results, err := evaluator.Run(ctx, ds)
	if err != nil {
		log.Fatalf("[eval] 评估运行失败: %v", err)
	}
	log.Printf("[eval] 评估完成，共 %d 个用例", len(results))

	runID := buildRunID(defaultRunPrefix, *runPrefix, *runLabel, time.Now())
	rpt := report.BuildReport(runID, *dataset, results, ds, *hitThreshold)

	jsonPath := filepath.Join(*outputDir, runID+".json")
	if err := rpt.WriteJSON(jsonPath); err != nil {
		log.Fatalf("[eval] 写入 JSON 报告失败: %v", err)
	}
	log.Printf("[eval] JSON 报告已写入: %s", jsonPath)

	mdPath := filepath.Join(*outputDir, runID+".md")
	if err := rpt.WriteMarkdown(mdPath); err != nil {
		log.Fatalf("[eval] 写入 Markdown 报告失败: %v", err)
	}
	log.Printf("[eval] Markdown 报告已写入: %s", mdPath)

	printSummary(rpt)

	if rpt.Metrics.HitRate < 0.5 {
		log.Printf("[eval] 警告: HitRate=%.2f%% 低于 50%%，评估不通过", rpt.Metrics.HitRate*100)
		os.Exit(1)
	}
}

type evalOverridesInput struct {
	knowledgeBaseID                string
	retrievalTopKDocument          int
	retrievalCandidateTopKDocument int
	retrievalTopKKnowledgeBase     int
	retrievalCandidateTopKAllDocs  int
	retrievalMaxChunksPerDocument  int
	retrievalMaxContextChars       int
	retrievalAutoExpand            string
	retrievalSearchMode            string
	retrievalRerankStrategy        string
	retrievalQueryRewrite          string
	retrievalQueryRewriteVariants  int
	evalEmbeddingBaseURL           string
	evalChatBaseURL                string
	evalPathMap                    string
	evalAllowFixtureMismatch       bool
	evalAllowMissingSources        bool
}

type evalPathMapRule struct {
	from string
	to   string
}

type evalOverrides struct {
	knowledgeBaseID                string
	retrievalTopKDocument          int
	retrievalCandidateTopKDocument int
	retrievalTopKKnowledgeBase     int
	retrievalCandidateTopKAllDocs  int
	retrievalMaxChunksPerDocument  int
	retrievalMaxContextChars       int
	retrievalAutoExpand            *bool
	retrievalSearchMode            string
	retrievalRerankStrategy        string
	retrievalQueryRewrite          *bool
	retrievalQueryRewriteVariants  int
	evalEmbeddingBaseURL           string
	evalChatBaseURL                string
	evalPathMaps                   []evalPathMapRule
	evalAllowFixtureMismatch       bool
	evalAllowMissingSources        bool
}

func newRealEvalRuntime(ctx context.Context, ds *offline.Dataset, realLLM bool, overrides evalOverrides, fixtureManifestPath string, fixtureManifest *offline.FixtureManifest) (*realEvalRuntime, error) {
	serverConfig := applyEvalOverrides(config.LoadServerConfig(), overrides)
	if err := os.MkdirAll(serverConfig.UploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建上传目录失败: %w", err)
	}

	var cleanup func()
	runtimeReady := false
	defer func() {
		if !runtimeReady && cleanup != nil {
			cleanup()
		}
	}()
	if len(overrides.evalPathMaps) > 0 {
		mappedStateFile, err := writeMappedEvalStateFile(serverConfig.StateFile, overrides.evalPathMaps)
		if err != nil {
			return nil, err
		}
		serverConfig.StateFile = mappedStateFile
		cleanup = func() {
			if err := os.Remove(mappedStateFile); err != nil && !os.IsNotExist(err) {
				log.Printf("[eval] 清理临时 app-state 失败 (%s): %v", mappedStateFile, err)
			}
		}
	}

	stateStore := service.NewAppStateStore(serverConfig.StateFile)
	loadedState, err := stateStore.Load()
	if err != nil {
		return nil, fmt.Errorf("读取 app-state 失败 (%s): %w", serverConfig.StateFile, err)
	}
	if loadedState == nil {
		return nil, fmt.Errorf("app-state 不存在: %s", serverConfig.StateFile)
	}
	if len(loadedState.KnowledgeBases) == 0 {
		return nil, fmt.Errorf("app-state 中不存在可用知识库: %s", serverConfig.StateFile)
	}

	qdrantService := service.NewQdrantService(serverConfig)
	if qdrantService == nil || !qdrantService.IsEnabled() {
		return nil, fmt.Errorf("Qdrant 未启用，请检查配置 QDRANT_URL=%q", serverConfig.QdrantURL)
	}
	if err := qdrantService.Ping(ctx); err != nil {
		return nil, fmt.Errorf("Qdrant 不可用 (%s): %w", serverConfig.QdrantURL, err)
	}
	log.Printf("[eval] qdrant connected: %s", serverConfig.QdrantURL)

	var fixtureCaseIDs map[string]struct{}
	if fixtureManifest != nil {
		fixtureCaseIDs = evalFixtureCaseIDs(fixtureManifest)
		fixtureCheck := inspectEvalFixtureIndex(ctx, fixtureManifestPath, fixtureManifest, ds, loadedState.KnowledgeBases, serverConfig.EvalKnowledgeBaseID, qdrantService)
		if len(fixtureCheck.Issues) > 0 {
			formatted := formatEvalFixtureIndexIssues(fixtureCheck.Issues, 16)
			if !overrides.evalAllowFixtureMismatch {
				return nil, fmt.Errorf("fixture 与当前索引不一致，共 %d 处；请使用同一份公开夹具重新上传并等待索引完成后再评估，或显式添加 -eval-allow-fixture-mismatch 继续（结果不可信）\n%s", len(fixtureCheck.Issues), formatted)
			}
			log.Printf("[eval] 警告: fixture 与当前索引存在 %d 处不一致，已按 -eval-allow-fixture-mismatch 继续（结果不可信）\n%s", len(fixtureCheck.Issues), formatted)
		}
		if fixtureCheck.KnowledgeBaseID != "" {
			// A fixture can identify a document by checksum even when its
			// upload-generated knowledge base/document IDs are unknown. Use the
			// resolved knowledge base for every case in this run.
			serverConfig.EvalKnowledgeBaseID = fixtureCheck.KnowledgeBaseID
			log.Printf("[eval] fixture 解析到知识库: %s", fixtureCheck.KnowledgeBaseID)
		}
	}

	// Fixture-backed cases are validated against the manifest document
	// checksum and indexed payload above. Their source_documents may contain
	// IDs from another upload, so do not reject those cases solely because the
	// runtime-generated document ID changed. Non-fixture cases keep the strict
	// app-state source validation below.
	if issues := validateEvalDatasetSourcesExcluding(ds, loadedState.KnowledgeBases, serverConfig.EvalKnowledgeBaseID, fixtureCaseIDs); len(issues) > 0 {
		formatted := formatEvalDatasetSourceIssues(issues, 12)
		if !overrides.evalAllowMissingSources {
			return nil, fmt.Errorf("评估数据集引用了当前 app-state 中不存在的来源，共 %d 处；请清理/重建数据集，或显式添加 -eval-allow-missing-sources 继续运行\n%s", len(issues), formatted)
		}
		log.Printf("[eval] 警告: 评估数据集存在 %d 处失效来源，已按 -eval-allow-missing-sources 继续运行\n%s", len(issues), formatted)
	}

	appService := service.NewAppService(qdrantService, stateStore, nil, serverConfig)
	if _, err := appService.ResolveKnowledgeBaseID(""); err != nil {
		return nil, fmt.Errorf("未找到可用于评估的知识库: %w", err)
	}

	casesByQuestion := make(map[string]offline.GroundTruthCase, len(ds.Cases))
	for _, gtCase := range ds.Cases {
		if _, exists := casesByQuestion[gtCase.Question]; !exists {
			casesByQuestion[gtCase.Question] = gtCase
		}
	}

	runtimeReady = true
	return &realEvalRuntime{
		appService:      appService,
		llmService:      service.NewLLMService(),
		casesByQuestion: casesByQuestion,
		realLLM:         realLLM,
		overrides:       overrides,
		cleanup:         cleanup,
	}, nil
}

func (r *realEvalRuntime) Close() {
	if r == nil || r.cleanup == nil {
		return
	}
	r.cleanup()
	r.cleanup = nil
}

func buildEvalOverrides(input evalOverridesInput) (evalOverrides, error) {
	overrides := evalOverrides{
		knowledgeBaseID:                strings.TrimSpace(input.knowledgeBaseID),
		retrievalTopKDocument:          input.retrievalTopKDocument,
		retrievalCandidateTopKDocument: input.retrievalCandidateTopKDocument,
		retrievalTopKKnowledgeBase:     input.retrievalTopKKnowledgeBase,
		retrievalCandidateTopKAllDocs:  input.retrievalCandidateTopKAllDocs,
		retrievalMaxChunksPerDocument:  input.retrievalMaxChunksPerDocument,
		retrievalMaxContextChars:       input.retrievalMaxContextChars,
		retrievalQueryRewriteVariants:  input.retrievalQueryRewriteVariants,
		evalEmbeddingBaseURL:           strings.TrimSpace(input.evalEmbeddingBaseURL),
		evalChatBaseURL:                strings.TrimSpace(input.evalChatBaseURL),
		evalAllowFixtureMismatch:       input.evalAllowFixtureMismatch,
		evalAllowMissingSources:        input.evalAllowMissingSources,
	}

	pathMaps, err := parseEvalPathMaps(input.evalPathMap)
	if err != nil {
		return evalOverrides{}, err
	}
	overrides.evalPathMaps = pathMaps

	searchMode, err := parseOptionalSearchMode(input.retrievalSearchMode)
	if err != nil {
		return evalOverrides{}, err
	}
	overrides.retrievalSearchMode = searchMode

	rerankStrategy, err := parseOptionalRerankStrategy(input.retrievalRerankStrategy)
	if err != nil {
		return evalOverrides{}, err
	}
	overrides.retrievalRerankStrategy = rerankStrategy

	if strings.TrimSpace(input.retrievalAutoExpand) != "" {
		parsed, err := parseOptionalBool(input.retrievalAutoExpand)
		if err != nil {
			return evalOverrides{}, err
		}
		overrides.retrievalAutoExpand = &parsed
	}

	if strings.TrimSpace(input.retrievalQueryRewrite) != "" {
		parsed, err := parseOptionalBool(input.retrievalQueryRewrite)
		if err != nil {
			return evalOverrides{}, err
		}
		overrides.retrievalQueryRewrite = &parsed
	}
	return overrides, nil
}

func applyEvalOverrides(serverConfig model.ServerConfig, overrides evalOverrides) model.ServerConfig {
	if overrides.knowledgeBaseID != "" {
		serverConfig.EvalKnowledgeBaseID = overrides.knowledgeBaseID
	}
	if overrides.retrievalTopKDocument > 0 {
		serverConfig.RetrievalTopKDocument = overrides.retrievalTopKDocument
	}
	if overrides.retrievalCandidateTopKDocument > 0 {
		serverConfig.RetrievalCandidateTopKDocument = overrides.retrievalCandidateTopKDocument
	}
	if overrides.retrievalTopKKnowledgeBase > 0 {
		serverConfig.RetrievalTopKKnowledgeBase = overrides.retrievalTopKKnowledgeBase
	}
	if overrides.retrievalCandidateTopKAllDocs > 0 {
		serverConfig.RetrievalCandidateTopKAllDocs = overrides.retrievalCandidateTopKAllDocs
	}
	if overrides.retrievalMaxChunksPerDocument > 0 {
		serverConfig.RetrievalMaxChunksPerDocument = overrides.retrievalMaxChunksPerDocument
	}
	if overrides.retrievalMaxContextChars > 0 {
		serverConfig.RetrievalMaxContextChars = overrides.retrievalMaxContextChars
	}
	if overrides.retrievalAutoExpand != nil {
		serverConfig.RetrievalEnableAutoExpand = *overrides.retrievalAutoExpand
	}
	return serverConfig
}

func parseOptionalSearchMode(value string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(value))
	switch normalized {
	case "":
		return "", nil
	case "auto", "dense", "vector", "hybrid":
		if normalized == "vector" {
			return "dense", nil
		}
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid retrieval search mode %q, expected auto/dense/hybrid", value)
	}
}

func parseOptionalRerankStrategy(value string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(value))
	switch normalized {
	case "":
		return "", nil
	case "keyword", "lexical":
		return "keyword", nil
	case "semantic", "embedding":
		return "semantic", nil
	default:
		return "", fmt.Errorf("invalid rerank strategy %q, expected keyword/semantic", value)
	}
}

func formatOptionalBool(value *bool) string {
	if value == nil {
		return "default"
	}
	if *value {
		return "true"
	}
	return "false"
}

func parseEvalPathMaps(value string) ([]evalPathMapRule, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	rules := make([]evalPathMapRule, 0, len(parts))
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		from, to, ok := strings.Cut(part, "=")
		from = strings.TrimRight(strings.TrimSpace(from), "/")
		to = strings.TrimRight(strings.TrimSpace(to), "/")
		if !ok || from == "" || to == "" {
			return nil, fmt.Errorf("invalid eval path map %q, expected from=to", part)
		}
		if !filepath.IsAbs(to) {
			to = filepath.Join(wd, to)
		}
		rules = append(rules, evalPathMapRule{from: from, to: to})
	}
	return rules, nil
}

func formatEvalPathMaps(rules []evalPathMapRule) string {
	parts := make([]string, 0, len(rules))
	for _, rule := range rules {
		parts = append(parts, rule.from+"="+rule.to)
	}
	return strings.Join(parts, ",")
}

func writeMappedEvalStateFile(stateFile string, rules []evalPathMapRule) (string, error) {
	content, err := os.ReadFile(stateFile)
	if err != nil {
		return "", fmt.Errorf("read app state for eval path mapping: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(content, &raw); err != nil {
		return "", fmt.Errorf("decode app state for eval path mapping: %w", err)
	}
	applyEvalPathMaps(raw, rules)
	mapped, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode mapped app state: %w", err)
	}
	tempFile, err := os.CreateTemp("", "ai-localbase-eval-state-*.json")
	if err != nil {
		return "", fmt.Errorf("create mapped eval state: %w", err)
	}
	tempFileName := tempFile.Name()
	if _, err := tempFile.Write(mapped); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempFileName)
		return "", fmt.Errorf("write mapped eval state: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempFileName)
		return "", fmt.Errorf("close mapped eval state: %w", err)
	}
	return tempFileName, nil
}

func applyEvalPathMaps(raw map[string]any, rules []evalPathMapRule) {
	knowledgeBases, _ := raw["knowledgeBases"].(map[string]any)
	for _, kbValue := range knowledgeBases {
		kb, _ := kbValue.(map[string]any)
		documents, _ := kb["documents"].([]any)
		for _, docValue := range documents {
			document, _ := docValue.(map[string]any)
			path, _ := document["path"].(string)
			if path == "" {
				continue
			}
			document["path"] = rewriteEvalPath(path, rules)
		}
	}
}

func rewriteEvalPath(path string, rules []evalPathMapRule) string {
	cleanPath := strings.TrimSpace(path)
	for _, rule := range rules {
		if cleanPath == rule.from {
			return rule.to
		}
		prefix := rule.from + "/"
		if strings.HasPrefix(cleanPath, prefix) {
			return filepath.Join(rule.to, strings.TrimPrefix(cleanPath, prefix))
		}
	}
	return path
}

type evalDatasetSourceIssue struct {
	CaseID          string
	Question        string
	KnowledgeBaseID string
	DocumentID      string
	Reason          string
}

func validateEvalDatasetSources(ds *offline.Dataset, knowledgeBases map[string]model.KnowledgeBase, evalKnowledgeBaseID string) []evalDatasetSourceIssue {
	return validateEvalDatasetSourcesExcluding(ds, knowledgeBases, evalKnowledgeBaseID, nil)
}

func validateEvalDatasetSourcesExcluding(ds *offline.Dataset, knowledgeBases map[string]model.KnowledgeBase, evalKnowledgeBaseID string, excludedCaseIDs map[string]struct{}) []evalDatasetSourceIssue {
	if ds == nil || len(knowledgeBases) == 0 {
		return nil
	}

	issues := make([]evalDatasetSourceIssue, 0)
	for _, gtCase := range ds.Cases {
		if _, excluded := excludedCaseIDs[gtCase.ID]; excluded {
			continue
		}
		for _, source := range gtCase.SourceDocuments {
			documentID := strings.TrimSpace(source.DocumentID)
			if documentID == "" {
				issues = append(issues, evalDatasetSourceIssue{
					CaseID:          gtCase.ID,
					Question:        gtCase.Question,
					KnowledgeBaseID: strings.TrimSpace(source.KnowledgeBaseID),
					DocumentID:      documentID,
					Reason:          "source_documents.document_id 为空",
				})
				continue
			}

			knowledgeBaseID, ok := sourceValidationKnowledgeBaseID(knowledgeBases, source, evalKnowledgeBaseID)
			if !ok {
				issues = append(issues, evalDatasetSourceIssue{
					CaseID:          gtCase.ID,
					Question:        gtCase.Question,
					KnowledgeBaseID: firstNonEmpty(strings.TrimSpace(evalKnowledgeBaseID), strings.TrimSpace(source.KnowledgeBaseID)),
					DocumentID:      documentID,
					Reason:          "知识库不存在",
				})
				continue
			}

			if !knowledgeBaseContainsDocument(knowledgeBases[knowledgeBaseID], documentID) {
				issues = append(issues, evalDatasetSourceIssue{
					CaseID:          gtCase.ID,
					Question:        gtCase.Question,
					KnowledgeBaseID: knowledgeBaseID,
					DocumentID:      documentID,
					Reason:          "文档不存在于当前知识库",
				})
			}
		}
	}
	return issues
}

func sourceValidationKnowledgeBaseID(knowledgeBases map[string]model.KnowledgeBase, source offline.SourceDocument, evalKnowledgeBaseID string) (string, bool) {
	if configured := strings.TrimSpace(evalKnowledgeBaseID); configured != "" {
		return resolveKnowledgeBaseIDInState(knowledgeBases, configured)
	}
	if sourceKBID := strings.TrimSpace(source.KnowledgeBaseID); sourceKBID != "" {
		return resolveKnowledgeBaseIDInState(knowledgeBases, sourceKBID)
	}
	if len(knowledgeBases) == 1 {
		for id := range knowledgeBases {
			return id, true
		}
	}
	return "", false
}

func resolveKnowledgeBaseIDInState(knowledgeBases map[string]model.KnowledgeBase, value string) (string, bool) {
	if len(knowledgeBases) == 0 {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if _, ok := knowledgeBases[value]; ok {
		return value, true
	}
	for id, kb := range knowledgeBases {
		if strings.EqualFold(strings.TrimSpace(kb.Name), value) {
			return id, true
		}
	}
	return "", false
}

func knowledgeBaseContainsDocument(kb model.KnowledgeBase, documentID string) bool {
	documentID = strings.TrimSpace(documentID)
	for _, document := range kb.Documents {
		if document.ID == documentID {
			return true
		}
	}
	return false
}

func formatEvalDatasetSourceIssues(issues []evalDatasetSourceIssue, limit int) string {
	if len(issues) == 0 {
		return ""
	}
	if limit <= 0 || limit > len(issues) {
		limit = len(issues)
	}
	lines := make([]string, 0, limit+1)
	for i := 0; i < limit; i++ {
		issue := issues[i]
		question := strings.TrimSpace(issue.Question)
		if len([]rune(question)) > 80 {
			question = string([]rune(question)[:80]) + "..."
		}
		lines = append(lines, fmt.Sprintf("- case=%s kb=%s doc=%s reason=%s question=%q",
			issue.CaseID,
			firstNonEmpty(issue.KnowledgeBaseID, "<未指定>"),
			firstNonEmpty(issue.DocumentID, "<未指定>"),
			issue.Reason,
			question,
		))
	}
	if remaining := len(issues) - limit; remaining > 0 {
		lines = append(lines, fmt.Sprintf("- ... 还有 %d 处问题未展示", remaining))
	}
	return strings.Join(lines, "\n")
}

const autoEvalFixtureManifest = "auto"

func resolveEvalFixtureManifestPath(requested, datasetPath string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = autoEvalFixtureManifest
	}
	if strings.EqualFold(requested, "none") {
		return "", nil
	}
	if !strings.EqualFold(requested, autoEvalFixtureManifest) {
		path, err := filepath.Abs(requested)
		if err != nil {
			return "", fmt.Errorf("resolve fixture manifest path: %w", err)
		}
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("fixture manifest does not exist: %s", path)
		}
		return path, nil
	}

	baseName := strings.ToLower(filepath.Base(strings.TrimSpace(datasetPath)))
	if baseName != "ground_truth_v1.small.json" && !strings.Contains(baseName, "public-v1") {
		return "", nil
	}
	datasetAbsolute, err := filepath.Abs(datasetPath)
	if err != nil {
		return "", fmt.Errorf("resolve dataset path: %w", err)
	}
	datasetDir := filepath.Dir(datasetAbsolute)
	candidates := []string{
		filepath.Join(datasetDir, "..", "fixtures", "public-v1", "manifest.json"),
		filepath.Join(datasetDir, "..", "..", "fixtures", "public-v1", "manifest.json"),
		filepath.Join("eval", "fixtures", "public-v1", "manifest.json"),
		filepath.Join("backend", "eval", "fixtures", "public-v1", "manifest.json"),
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate, err = filepath.Abs(candidate)
		if err != nil {
			continue
		}
		candidate = filepath.Clean(candidate)
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", nil
}

type evalFixtureIndexIssue struct {
	CaseID          string
	DocumentKey     string
	KnowledgeBaseID string
	DocumentID      string
	Reason          string
}

type evalFixtureIndexCheck struct {
	KnowledgeBaseID string
	Issues          []evalFixtureIndexIssue
}

func evalFixtureCaseIDs(manifest *offline.FixtureManifest) map[string]struct{} {
	if manifest == nil || len(manifest.Cases) == 0 {
		return nil
	}
	caseIDs := make(map[string]struct{}, len(manifest.Cases))
	for _, fixtureCase := range manifest.Cases {
		if caseID := strings.TrimSpace(fixtureCase.ID); caseID != "" {
			caseIDs[caseID] = struct{}{}
		}
	}
	return caseIDs
}

type evalFixtureDocumentMatch struct {
	KnowledgeBaseID string
	Document        model.Document
}

func inspectEvalFixtureIndex(
	ctx context.Context,
	manifestPath string,
	manifest *offline.FixtureManifest,
	dataset *offline.Dataset,
	knowledgeBases map[string]model.KnowledgeBase,
	configuredKnowledgeBaseID string,
	qdrant *service.QdrantService,
) evalFixtureIndexCheck {
	check := evalFixtureIndexCheck{}
	if manifest == nil {
		return check
	}
	addIssue := func(issue evalFixtureIndexIssue) {
		check.Issues = append(check.Issues, issue)
	}

	kbIDs, err := evalFixtureKnowledgeBaseIDs(knowledgeBases, configuredKnowledgeBaseID)
	if err != nil {
		addIssue(evalFixtureIndexIssue{Reason: err.Error()})
		return check
	}
	casesByID := make(map[string]offline.GroundTruthCase)
	if dataset != nil {
		casesByID = make(map[string]offline.GroundTruthCase, len(dataset.Cases))
		for _, item := range dataset.Cases {
			casesByID[item.ID] = item
		}
	}

	for _, fixtureDocument := range manifest.Documents {
		documentKey := strings.TrimSpace(fixtureDocument.DocumentKey)
		fixturePath, err := manifest.ResolveDocumentPath(manifestPath, documentKey)
		if err != nil {
			addIssue(evalFixtureIndexIssue{DocumentKey: documentKey, Reason: err.Error()})
			continue
		}
		expectedChecksum, err := offline.FileSHA256(fixturePath)
		if err != nil {
			addIssue(evalFixtureIndexIssue{DocumentKey: documentKey, Reason: fmt.Sprintf("无法读取 fixture 文件: %v", err)})
			continue
		}
		if declaredChecksum := strings.TrimSpace(fixtureDocument.SHA256); declaredChecksum != "" && !strings.EqualFold(declaredChecksum, expectedChecksum) {
			addIssue(evalFixtureIndexIssue{
				DocumentKey: documentKey,
				Reason:      "manifest 中的 SHA-256 与 fixture 文件不一致",
			})
			continue
		}

		expectedName := filepath.Base(filepath.FromSlash(strings.TrimSpace(fixtureDocument.Path)))
		exactMatches, namedMatches := findEvalFixtureDocuments(kbIDs, knowledgeBases, expectedName, expectedChecksum)
		if len(exactMatches) == 0 {
			reason := "当前知识库不存在与 fixture 内容一致的已上传文档"
			if len(namedMatches) > 0 {
				reason = "同名文档内容版本与 fixture 不一致，需要重新上传或重建索引"
			}
			addIssue(evalFixtureIndexIssue{
				DocumentKey:     documentKey,
				KnowledgeBaseID: strings.TrimSpace(configuredKnowledgeBaseID),
				Reason:          reason,
			})
			continue
		}
		if len(exactMatches) > 1 {
			addIssue(evalFixtureIndexIssue{
				DocumentKey: documentKey,
				Reason:      fmt.Sprintf("fixture 内容在多个文档中出现，无法安全选择评估知识库（匹配数=%d）", len(exactMatches)),
			})
			continue
		}

		match := exactMatches[0]
		if check.KnowledgeBaseID == "" {
			check.KnowledgeBaseID = match.KnowledgeBaseID
		} else if check.KnowledgeBaseID != match.KnowledgeBaseID {
			addIssue(evalFixtureIndexIssue{
				DocumentKey:     documentKey,
				KnowledgeBaseID: match.KnowledgeBaseID,
				DocumentID:      match.Document.ID,
				Reason:          "manifest 中的多个 fixture 文档不属于同一个知识库",
			})
			continue
		}

		document := match.Document
		if !strings.EqualFold(strings.TrimSpace(document.Status), "indexed") {
			addIssue(evalFixtureIndexIssue{
				DocumentKey:     documentKey,
				KnowledgeBaseID: match.KnowledgeBaseID,
				DocumentID:      document.ID,
				Reason:          fmt.Sprintf("文档尚未处于 indexed 状态（当前=%s）", firstNonEmpty(document.Status, "unknown")),
			})
		}
		if document.IndexVersion <= 0 {
			addIssue(evalFixtureIndexIssue{
				DocumentKey:     documentKey,
				KnowledgeBaseID: match.KnowledgeBaseID,
				DocumentID:      document.ID,
				Reason:          "文档没有有效的索引版本",
			})
		}

		points, err := qdrant.ScrollPointPayloadsByFilter(ctx, match.KnowledgeBaseID, evalFixtureDocumentFilter(document.ID))
		if err != nil {
			addIssue(evalFixtureIndexIssue{
				DocumentKey:     documentKey,
				KnowledgeBaseID: match.KnowledgeBaseID,
				DocumentID:      document.ID,
				Reason:          fmt.Sprintf("读取 Qdrant 索引点失败: %v", err),
			})
			continue
		}
		if len(points) == 0 {
			addIssue(evalFixtureIndexIssue{
				DocumentKey:     documentKey,
				KnowledgeBaseID: match.KnowledgeBaseID,
				DocumentID:      document.ID,
				Reason:          "文档状态显示已索引，但 Qdrant 中没有对应索引点",
			})
			continue
		}
		if document.ChunkCount > 0 && len(points) != document.ChunkCount {
			addIssue(evalFixtureIndexIssue{
				DocumentKey:     documentKey,
				KnowledgeBaseID: match.KnowledgeBaseID,
				DocumentID:      document.ID,
				Reason:          fmt.Sprintf("Qdrant 点数量与文档 Chunk 数不一致（文档=%d，Qdrant=%d）", document.ChunkCount, len(points)),
			})
		}

		indexedText := evalFixtureIndexedText(points)
		for _, fixtureCase := range manifest.Cases {
			if strings.TrimSpace(fixtureCase.DocumentKey) != documentKey || strings.TrimSpace(fixtureCase.Section) == "no-source" {
				continue
			}
			gtCase, ok := casesByID[fixtureCase.ID]
			if !ok || offline.IsNoAnswerCase(gtCase) {
				continue
			}
			if !evalFixtureEvidenceContains(indexedText, gtCase) {
				addIssue(evalFixtureIndexIssue{
					CaseID:          fixtureCase.ID,
					DocumentKey:     documentKey,
					KnowledgeBaseID: match.KnowledgeBaseID,
					DocumentID:      document.ID,
					Reason:          "Qdrant 索引点中没有覆盖该用例的完整答案或全部答案片段",
				})
			}
		}
	}
	return check
}

func evalFixtureKnowledgeBaseIDs(knowledgeBases map[string]model.KnowledgeBase, configured string) ([]string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		resolved, ok := resolveKnowledgeBaseIDInState(knowledgeBases, configured)
		if !ok {
			return nil, fmt.Errorf("评估知识库不存在: %s", configured)
		}
		return []string{resolved}, nil
	}
	if len(knowledgeBases) == 0 {
		return nil, fmt.Errorf("当前 app-state 没有知识库")
	}
	ids := make([]string, 0, len(knowledgeBases))
	for id := range knowledgeBases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func findEvalFixtureDocuments(kbIDs []string, knowledgeBases map[string]model.KnowledgeBase, expectedName, expectedChecksum string) ([]evalFixtureDocumentMatch, []evalFixtureDocumentMatch) {
	exactMatches := make([]evalFixtureDocumentMatch, 0)
	namedMatches := make([]evalFixtureDocumentMatch, 0)
	for _, knowledgeBaseID := range kbIDs {
		knowledgeBase, ok := knowledgeBases[knowledgeBaseID]
		if !ok {
			continue
		}
		for _, document := range knowledgeBase.Documents {
			nameMatches := strings.EqualFold(strings.TrimSpace(document.Name), strings.TrimSpace(expectedName))
			checksum := strings.TrimSpace(document.Checksum)
			if checksum == "" && strings.TrimSpace(document.Path) != "" {
				if calculated, err := offline.FileSHA256(document.Path); err == nil {
					checksum = calculated
				}
			}
			match := evalFixtureDocumentMatch{KnowledgeBaseID: knowledgeBaseID, Document: document}
			if nameMatches {
				namedMatches = append(namedMatches, match)
			}
			if checksum != "" && strings.EqualFold(checksum, expectedChecksum) {
				exactMatches = append(exactMatches, match)
			}
		}
	}
	return exactMatches, namedMatches
}

func evalFixtureDocumentFilter(documentID string) map[string]any {
	return map[string]any{
		"must": []map[string]any{{
			"key":   "document_id",
			"match": map[string]any{"value": documentID},
		}},
	}
}

func evalFixtureIndexedText(points []service.QdrantStoredPoint) string {
	texts := make([]string, 0, len(points))
	for _, point := range points {
		text, ok := point.Payload["text"].(string)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n")
}

func evalFixtureEvidenceContains(indexedText string, gtCase offline.GroundTruthCase) bool {
	indexedText = normalizeFixtureComparisonText(indexedText)
	if indexedText == "" {
		return false
	}
	if answer := normalizeFixtureComparisonText(gtCase.Answer); answer != "" && strings.Contains(indexedText, answer) {
		return true
	}
	if len(gtCase.AnswerSnippets) == 0 {
		return false
	}
	for _, snippet := range gtCase.AnswerSnippets {
		normalizedSnippet := normalizeFixtureComparisonText(snippet)
		if normalizedSnippet == "" || !strings.Contains(indexedText, normalizedSnippet) {
			return false
		}
	}
	return true
}

func normalizeFixtureComparisonText(value string) string {
	var builder strings.Builder
	pendingSpace := false
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			pendingSpace = builder.Len() > 0
			continue
		}
		if pendingSpace {
			builder.WriteRune(' ')
			pendingSpace = false
		}
		builder.WriteRune(unicode.ToLower(r))
	}
	return strings.TrimSpace(builder.String())
}

func formatEvalFixtureIndexIssues(issues []evalFixtureIndexIssue, limit int) string {
	if len(issues) == 0 {
		return ""
	}
	if limit <= 0 || limit > len(issues) {
		limit = len(issues)
	}
	lines := make([]string, 0, limit+1)
	for i := 0; i < limit; i++ {
		issue := issues[i]
		lines = append(lines, fmt.Sprintf("- case=%s fixture=%s kb=%s doc=%s reason=%s",
			firstNonEmpty(issue.CaseID, "<document>"),
			firstNonEmpty(issue.DocumentKey, "<unknown>"),
			firstNonEmpty(issue.KnowledgeBaseID, "<未指定>"),
			firstNonEmpty(issue.DocumentID, "<未指定>"),
			issue.Reason,
		))
	}
	if remaining := len(issues) - limit; remaining > 0 {
		lines = append(lines, fmt.Sprintf("- ... 还有 %d 处问题未展示", remaining))
	}
	return strings.Join(lines, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func buildRunID(defaultPrefix, customPrefix, label string, now time.Time) string {
	prefix := sanitizeRunIDPart(customPrefix)
	if prefix == "" {
		prefix = sanitizeRunIDPart(defaultPrefix)
	}
	if prefix == "" {
		prefix = "eval"
	}

	parts := []string{prefix, now.Format("20060102-150405")}
	if sanitizedLabel := sanitizeRunIDPart(label); sanitizedLabel != "" {
		parts = append(parts, sanitizedLabel)
	}
	return strings.Join(parts, "_")
}

func sanitizeRunIDPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}

	var builder strings.Builder
	lastWasSeparator := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastWasSeparator = false
		case r == '-', r == '_', r == '.', r == ' ', r == '/':
			if builder.Len() == 0 || lastWasSeparator {
				continue
			}
			builder.WriteRune('-')
			lastWasSeparator = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func parseOptionalBool(value string) (bool, error) {
	normalized := strings.TrimSpace(strings.ToLower(value))
	switch normalized {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q, expected true/false", value)
	}
}

func (r *realEvalRuntime) retrieval(ctx context.Context, question string) ([]offline.RetrievedChunkInfo, time.Duration, error) {
	startedAt := time.Now()
	gtCase, ok := r.casesByQuestion[question]
	if !ok {
		return nil, time.Since(startedAt), fmt.Errorf("数据集中未找到问题对应的用例: %s", question)
	}

	knowledgeBaseID, err := r.resolveKnowledgeBaseID(gtCase)
	if err != nil {
		return nil, time.Since(startedAt), err
	}

	req := model.ChatCompletionRequest{
		KnowledgeBaseID:         knowledgeBaseID,
		RetrievalMode:           r.overrides.retrievalSearchMode,
		RerankStrategy:          r.overrides.retrievalRerankStrategy,
		EnableQueryRewrite:      r.overrides.retrievalQueryRewrite,
		QueryRewriteMaxVariants: r.overrides.retrievalQueryRewriteVariants,
		Config:                  r.evalChatConfig(),
		Messages: []model.ChatMessage{{
			Role:    "user",
			Content: question,
		}},
		Embedding: r.evalEmbeddingConfig(),
	}

	chunks, err := r.appService.EvaluateRetrieve(req)
	latency := time.Since(startedAt)
	if err != nil {
		return nil, latency, fmt.Errorf("真实检索失败 (kb=%s): %w", knowledgeBaseID, err)
	}

	result := make([]offline.RetrievedChunkInfo, 0, len(chunks))
	for _, chunk := range chunks {
		result = append(result, offline.RetrievedChunkInfo{
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			DocumentID:      chunk.DocumentID,
			ChunkID:         chunk.ID,
			Text:            chunk.Text,
			Score:           chunk.Score,
		})
	}
	return result, latency, nil
}

func (r *realEvalRuntime) generation(ctx context.Context, question string, chunks []offline.RetrievedChunkInfo) (string, time.Duration, error) {
	startedAt := time.Now()
	if !r.realLLM {
		return buildSummaryAnswer(question, chunks), time.Since(startedAt), nil
	}

	chatConfig := r.evalChatConfig()
	if strings.TrimSpace(chatConfig.Model) == "" {
		answer := buildSummaryAnswer(question, chunks) + "\n\n[degraded] 未配置 Chat 模型，已回退为检索摘要回答。"
		return answer, time.Since(startedAt), nil
	}

	prompt := buildRealLLMPrompt(question, chunks)
	resp, err := r.llmService.Chat(model.ChatCompletionRequest{
		Messages: []model.ChatMessage{{Role: "user", Content: prompt}},
		Config:   chatConfig,
	})
	latency := time.Since(startedAt)
	if err != nil {
		answer := buildSummaryAnswer(question, chunks) + fmt.Sprintf("\n\n[degraded] LLM 调用失败，已回退为检索摘要回答：%v", err)
		return answer, latency, nil
	}
	if len(resp.Choices) == 0 {
		answer := buildSummaryAnswer(question, chunks) + "\n\n[degraded] LLM 返回空结果，已回退为检索摘要回答。"
		return answer, latency, nil
	}

	answer := strings.TrimSpace(resp.Choices[0].Message.Content)
	if answer == "" {
		answer = buildSummaryAnswer(question, chunks)
	}
	if degraded, _ := resp.Metadata["degraded"].(bool); degraded {
		if upstream, _ := resp.Metadata["upstreamError"].(string); strings.TrimSpace(upstream) != "" {
			answer += "\n\n[degraded] " + upstream
		} else {
			answer += "\n\n[degraded] 已使用本地降级响应。"
		}
	}
	return answer, latency, nil
}

func (r *realEvalRuntime) evalEmbeddingConfig() model.EmbeddingModelConfig {
	cfg := r.appService.CurrentEmbeddingConfig()
	if baseURL := strings.TrimSpace(r.overrides.evalEmbeddingBaseURL); baseURL != "" {
		cfg.BaseURL = baseURL
	}
	return cfg
}

func (r *realEvalRuntime) evalChatConfig() model.ChatModelConfig {
	cfg := r.appService.CurrentChatConfig()
	if baseURL := strings.TrimSpace(r.overrides.evalChatBaseURL); baseURL != "" {
		cfg.BaseURL = baseURL
	}
	return cfg
}

func (r *realEvalRuntime) resolveKnowledgeBaseID(gtCase offline.GroundTruthCase) (string, error) {
	if configured := strings.TrimSpace(r.appService.ServerConfig().EvalKnowledgeBaseID); configured != "" {
		return r.appService.ResolveKnowledgeBaseID(configured)
	}
	if len(gtCase.SourceDocuments) > 0 {
		candidate := strings.TrimSpace(gtCase.SourceDocuments[0].KnowledgeBaseID)
		if candidate != "" {
			return r.appService.ResolveKnowledgeBaseID(candidate)
		}
	}
	if kbID, err := r.appService.ResolveKnowledgeBaseID("kb-1"); err == nil {
		return kbID, nil
	}
	return r.appService.ResolveKnowledgeBaseID("")
}

// buildSummaryAnswer is a deterministic retrieval-only fallback. Keep the
// answer limited to evidence text so Faithfulness measures the retrieval
// output instead of evaluator-generated IDs, scores, or truncation markers.
func buildSummaryAnswer(_ string, chunks []offline.RetrievedChunkInfo) string {
	if len(chunks) == 0 {
		return "未找到可用证据，无法基于当前资料回答。"
	}

	lines := make([]string, 0, minInt(len(chunks), 3))
	for i, chunk := range chunks {
		if i >= 3 {
			break
		}
		text := strings.TrimSpace(chunk.Text)
		if text == "" {
			continue
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n")
}

func buildRealLLMPrompt(question string, chunks []offline.RetrievedChunkInfo) string {
	if len(chunks) == 0 {
		return fmt.Sprintf("请直接回答用户问题。如果缺少上下文，请明确说明。\n问题：%s", question)
	}

	parts := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		parts = append(parts, fmt.Sprintf("[%d][doc=%s chunk=%s score=%.4f]\n%s", i+1, chunk.DocumentID, chunk.ChunkID, chunk.Score, chunk.Text))
	}
	return fmt.Sprintf("请严格基于以下检索上下文回答问题；如果上下文不足，请明确说明，不要编造。\n\n问题：%s\n\n检索上下文：\n%s", question, strings.Join(parts, "\n\n"))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func printSummary(rpt *report.Report) {
	fmt.Println()
	fmt.Println("====== RAG 评估摘要 ======")
	fmt.Printf("RunID:          %s\n", rpt.RunID)
	fmt.Printf("总用例数:       %d\n", rpt.Metrics.TotalCases)
	fmt.Printf("命中率:         %.2f%%\n", rpt.Metrics.HitRate*100)
	fmt.Printf("文档命中率:     %.2f%%\n", rpt.Metrics.DocumentHitRate*100)
	fmt.Printf("Chunk 命中率:    %.2f%%\n", rpt.Metrics.ChunkHitRate*100)
	fmt.Printf("答案片段命中率: %.2f%%\n", rpt.Metrics.AnswerSnippetHitRate*100)
	fmt.Printf("直接证据命中率: %.2f%%\n", rpt.Metrics.DirectEvidenceHitRate*100)
	fmt.Printf("Faithfulness:   %.2f%%\n", rpt.Metrics.FaithfulnessScore*100)
	fmt.Printf("未支撑答案率:   %.2f%%\n", rpt.Metrics.HallucinationRate*100)
	fmt.Printf("未支撑陈述率:   %.2f%%\n", rpt.Metrics.UnsupportedClaimRate*100)
	fmt.Printf("MRR:            %.4f\n", rpt.Metrics.MRR)
	fmt.Printf("检索时延 P50:   %.0fms\n", rpt.Metrics.RetrievalLatencyP50Ms)
	fmt.Printf("检索时延 P95:   %.0fms\n", rpt.Metrics.RetrievalLatencyP95Ms)
	fmt.Printf("生成时延 P50:   %.0fms\n", rpt.Metrics.GenerationLatencyP50Ms)
	fmt.Printf("生成时延 P95:   %.0fms\n", rpt.Metrics.GenerationLatencyP95Ms)
	fmt.Println("=========================")
}

func mockRetrieval(ctx context.Context, question string) ([]offline.RetrievedChunkInfo, time.Duration, error) {
	latency := 10 * time.Millisecond
	chunks := []offline.RetrievedChunkInfo{
		{
			ChunkID:    "mock-chunk-1",
			DocumentID: "mock-doc-1",
			Text:       "这是一个模拟检索结果，用于测试评估框架。" + question,
			Score:      0.85,
		},
	}
	return chunks, latency, nil
}

func mockGeneration(ctx context.Context, question string, chunks []offline.RetrievedChunkInfo) (string, time.Duration, error) {
	latency := 50 * time.Millisecond
	answer := fmt.Sprintf("这是关于 '%s' 的模拟回答。", question)
	return answer, latency, nil
}
