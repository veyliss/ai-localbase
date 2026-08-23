package service

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

const (
	structuredQueryRowLimit = 20
)

type structuredQueryIntent string

const (
	structuredIntentCount   structuredQueryIntent = "count"
	structuredIntentPreview structuredQueryIntent = "preview"
	structuredIntentFilter  structuredQueryIntent = "filter"
	structuredIntentMax     structuredQueryIntent = "max"
	structuredIntentMin     structuredQueryIntent = "min"
	structuredIntentAverage structuredQueryIntent = "average"
	structuredIntentGroup   structuredQueryIntent = "group"
)

type structuredQueryPlan struct {
	Intent      structuredQueryIntent
	FilterField string
	FilterValue string
	TargetField string
}

type StructuredDataQueryResult struct {
	Query         string                    `json:"query"`
	Intent        string                    `json:"intent"`
	FilterField   string                    `json:"filterField,omitempty"`
	FilterValue   string                    `json:"filterValue,omitempty"`
	TargetField   string                    `json:"targetField,omitempty"`
	TotalRows     int                       `json:"totalRows"`
	MatchedRows   int                       `json:"matchedRows"`
	Columns       []string                  `json:"columns,omitempty"`
	Rows          []StructuredDataResultRow `json:"rows,omitempty"`
	Aggregate     *StructuredDataAggregate  `json:"aggregate,omitempty"`
	Groups        []StructuredDataGroup     `json:"groups,omitempty"`
	RowsTruncated bool                      `json:"rowsTruncated,omitempty"`
}

type StructuredDataResultRow struct {
	KnowledgeBaseID string            `json:"knowledgeBaseId"`
	DocumentID      string            `json:"documentId"`
	DocumentName    string            `json:"documentName"`
	Sheet           string            `json:"sheet,omitempty"`
	RowNumber       int               `json:"rowNumber"`
	Values          map[string]string `json:"values"`
}

type StructuredDataAggregate struct {
	Operation   string  `json:"operation"`
	Field       string  `json:"field,omitempty"`
	Value       float64 `json:"value"`
	SampleCount int     `json:"sampleCount"`
}

type StructuredDataGroup struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type structuredTableDocument struct {
	Document model.Document
	Tables   []util.StructuredTable
}

type structuredRowMatch struct {
	Document model.Document
	Table    util.StructuredTable
	Row      util.StructuredTableRow
}

func (s *AppService) QueryStructuredData(req model.ChatCompletionRequest) (StructuredDataQueryResult, []map[string]string, bool, error) {
	query := latestUserMessage(req.Messages)
	if !looksLikeStructuredDataQuery(query) {
		return StructuredDataQueryResult{}, nil, false, nil
	}

	result, sources, ok, err := s.buildStructuredDataQueryResult(req, query)
	if err != nil || !ok {
		return StructuredDataQueryResult{}, nil, ok, err
	}
	return result, sources, true, nil
}

func (s *AppService) buildStructuredDataQueryResult(req model.ChatCompletionRequest, query string) (StructuredDataQueryResult, []map[string]string, bool, error) {
	if !looksLikeStructuredDataQuery(query) {
		return StructuredDataQueryResult{}, nil, false, nil
	}

	documents := s.resolveStructuredTableDocuments(req)
	if len(documents) == 0 {
		return StructuredDataQueryResult{}, nil, false, nil
	}
	documents = filterStructuredDocumentsByQueryFilenames(query, documents)
	if len(documents) == 0 {
		return StructuredDataQueryResult{}, nil, false, nil
	}

	tables := make([]structuredTableDocument, 0, len(documents))
	for _, document := range documents {
		parsed, err := util.ExtractStructuredTables(document.Path)
		if err != nil {
			return StructuredDataQueryResult{}, nil, true, err
		}
		if len(parsed) == 0 {
			continue
		}
		tables = append(tables, structuredTableDocument{Document: document, Tables: parsed})
	}
	if len(tables) == 0 {
		return StructuredDataQueryResult{}, nil, false, nil
	}

	plan := buildStructuredQueryPlan(query, tables)
	if plan.Intent == "" {
		return StructuredDataQueryResult{}, nil, false, nil
	}

	result, ok := buildStructuredDataResult(query, plan, tables)
	if !ok {
		return StructuredDataQueryResult{}, nil, false, nil
	}
	return result, structuredDataSources(tables), true, nil
}

func looksLikeStructuredDataQuery(query string) bool {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return false
	}
	markers := []string{
		"表格", "工作表", ".csv", ".xlsx", ".xls", "数据", "记录", "字段", "列名", "数据行", "行数",
		"筛选", "统计", "分布", "平均", "均值", "最大", "最小", "最高", "最低", "最多", "最少",
		"姓名", "名字", "名称", "学校", "单位", "项目", "负责人", "联系人", "电话", "手机号", "邮箱",
		"地址", "时间", "日期", "年龄", "编号", "职称", "职位", "岗位", "薪资", "工资", "薪水", "薪酬",
	}
	return containsAnyText(trimmed, markers)
}

func (s *AppService) resolveStructuredTableDocuments(req model.ChatCompletionRequest) []model.Document {
	if s == nil || s.state == nil {
		return nil
	}

	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()

	if documentID := strings.TrimSpace(req.DocumentID); documentID != "" {
		for _, kb := range s.state.KnowledgeBases {
			for _, document := range kb.Documents {
				if document.ID == documentID && isStructuredDocumentPath(document.Path) {
					return []model.Document{document}
				}
			}
		}
		return nil
	}

	if knowledgeBaseID := strings.TrimSpace(req.KnowledgeBaseID); knowledgeBaseID != "" {
		kb, ok := s.state.KnowledgeBases[knowledgeBaseID]
		if !ok {
			return nil
		}
		return structuredDocumentsFromKnowledgeBase(kb)
	}

	var documents []model.Document
	for _, kb := range s.state.KnowledgeBases {
		documents = append(documents, structuredDocumentsFromKnowledgeBase(kb)...)
	}
	if len(documents) == 1 {
		return documents
	}
	return nil
}

func structuredDocumentsFromKnowledgeBase(kb model.KnowledgeBase) []model.Document {
	documents := make([]model.Document, 0)
	for _, document := range kb.Documents {
		if isStructuredDocumentPath(document.Path) {
			documents = append(documents, document)
		}
	}
	sort.Slice(documents, func(i, j int) bool {
		return documents[i].ID < documents[j].ID
	})
	return documents
}

func filterStructuredDocumentsByQueryFilenames(query string, documents []model.Document) []model.Document {
	filenames := extractFilenamesFromQuery(query)
	if len(filenames) == 0 {
		return documents
	}

	filtered := make([]model.Document, 0, len(documents))
	seen := map[string]struct{}{}
	for _, filename := range filenames {
		matches := structuredDocumentsMatchingFilename(documents, filename)
		for _, document := range matches {
			if _, ok := seen[document.ID]; ok {
				continue
			}
			seen[document.ID] = struct{}{}
			filtered = append(filtered, document)
		}
	}
	return filtered
}

func structuredDocumentsMatchingFilename(documents []model.Document, filename string) []model.Document {
	cleanFilename := strings.TrimSpace(filename)
	if cleanFilename == "" {
		return nil
	}

	matches := make([]model.Document, 0)
	for _, document := range documents {
		if document.Name == cleanFilename {
			return []model.Document{document}
		}
	}
	for _, document := range documents {
		if strings.Contains(document.Name, cleanFilename) || strings.Contains(cleanFilename, document.Name) {
			matches = append(matches, document)
		}
	}
	if len(matches) > 0 {
		return matches
	}

	if !shouldAllowFilenameExtensionFallback(cleanFilename) {
		return nil
	}

	ext := filepath.Ext(cleanFilename)
	if ext == "" {
		return nil
	}
	for _, document := range documents {
		if filepath.Ext(document.Name) == ext {
			matches = append(matches, document)
		}
	}
	if len(matches) == 1 {
		return matches
	}
	return nil
}

func isStructuredDocumentPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv", ".xlsx":
		return true
	default:
		return false
	}
}

func buildStructuredQueryPlan(query string, documents []structuredTableDocument) structuredQueryPlan {
	normalized := strings.TrimSpace(query)
	aggregateIntent := detectStructuredAggregateIntent(normalized)
	targetField := detectStructuredTargetField(normalized, documents)
	if aggregateIntent != "" {
		if inferred := inferNumericStructuredTargetField(normalized, documents); inferred != "" {
			targetField = inferred
		}
	}
	filterField, filterValue := detectStructuredFilter(normalized, documents)
	if filterField == "" && filterValue == "" && targetField != "" {
		filterField, filterValue = detectStructuredSubjectFilter(normalized, documents, targetField)
	}
	if aggregateIntent != "" {
		return structuredQueryPlan{
			Intent:      aggregateIntent,
			FilterField: filterField,
			FilterValue: filterValue,
			TargetField: targetField,
		}
	}
	if filterField != "" && filterValue != "" {
		return structuredQueryPlan{Intent: structuredIntentFilter, FilterField: filterField, FilterValue: filterValue, TargetField: targetField}
	}

	if containsAnyText(normalized, []string{"分布", "按", "每个", "各"}) && targetField != "" {
		return structuredQueryPlan{Intent: structuredIntentGroup, TargetField: targetField}
	}
	if isStructuredCountQuestion(normalized) {
		return structuredQueryPlan{Intent: structuredIntentCount}
	}
	if containsAnyText(normalized, []string{"展示", "列出", "查看", "读取", "表格", "工作表", "数据", "记录", "明细", "详情"}) {
		return structuredQueryPlan{Intent: structuredIntentPreview}
	}
	return structuredQueryPlan{}
}

func detectStructuredAggregateIntent(query string) structuredQueryIntent {
	switch {
	case containsAnyText(query, []string{"最高", "最大", "最多"}):
		return structuredIntentMax
	case containsAnyText(query, []string{"最低", "最小", "最少"}):
		return structuredIntentMin
	case containsAnyText(query, []string{"平均", "均值"}):
		return structuredIntentAverage
	default:
		return ""
	}
}

type structuredSubjectCandidate struct {
	Field string
	Value string
	Score int
}

func detectStructuredSubjectFilter(query string, documents []structuredTableDocument, targetField string) (string, string) {
	normalizedQuery := normalizeStructuredComparable(query)
	if normalizedQuery == "" {
		return "", ""
	}

	var best structuredSubjectCandidate
	for _, row := range collectStructuredRows(documents, "", "") {
		for index, header := range row.Table.Headers {
			field := strings.TrimSpace(header)
			if field == "" || field == targetField || index >= len(row.Row.Values) {
				continue
			}
			value := strings.TrimSpace(row.Row.Values[index])
			normalizedValue := normalizeStructuredComparable(value)
			if !isStructuredSubjectValue(normalizedValue) || !strings.Contains(normalizedQuery, normalizedValue) {
				continue
			}
			score := structuredSubjectFieldPriority(field)*100 + len([]rune(normalizedValue))
			if score > best.Score {
				best = structuredSubjectCandidate{Field: field, Value: value, Score: score}
			}
		}
	}
	if best.Score == 0 {
		return "", ""
	}
	return best.Field, best.Value
}

func isStructuredSubjectValue(value string) bool {
	if value == "" {
		return false
	}
	runeCount := len([]rune(value))
	if runeCount >= 2 {
		return true
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return runeCount >= 4
}

func structuredSubjectFieldPriority(field string) int {
	switch {
	case containsAnyText(field, []string{"姓名", "名称", "名字", "学校", "单位", "项目", "用户", "账号"}):
		return 5
	case containsAnyText(field, []string{"负责人", "联系人", "教师", "员工", "人员"}):
		return 4
	case containsAnyText(field, []string{"编号", "工号", "ID", "id"}):
		return 3
	default:
		return 1
	}
}

func normalizeStructuredComparable(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.Trim(value, " \t\r\n，。；;、?？!！'\"`《》")
	return strings.Join(strings.Fields(value), "")
}

func detectStructuredFilter(query string, documents []structuredTableDocument) (string, string) {
	for _, header := range allStructuredHeaders(documents) {
		index := strings.Index(query, header)
		if index < 0 {
			continue
		}
		rest := strings.TrimSpace(query[index+len(header):])
		for _, marker := range []string{"等于", "为", "是", "=", "：", ":"} {
			if !strings.HasPrefix(rest, marker) {
				continue
			}
			valueText := strings.TrimSpace(strings.TrimPrefix(rest, marker))
			if value := findStructuredFieldValueInQuery(valueText, header, documents); value != "" {
				return header, value
			}
			value := trimQueryValue(valueText)
			if value != "" && !isStructuredQuestionValue(value) {
				return header, value
			}
		}
	}
	return "", ""
}

func findStructuredFieldValueInQuery(query, field string, documents []structuredTableDocument) string {
	normalizedQuery := normalizeStructuredComparable(query)
	if normalizedQuery == "" {
		return ""
	}

	values := make([]string, 0)
	seen := make(map[string]struct{})
	for _, row := range collectStructuredRows(documents, "", "") {
		index := headerIndex(row.Table.Headers, field)
		if index < 0 || index >= len(row.Row.Values) {
			continue
		}
		value := strings.TrimSpace(row.Row.Values[index])
		normalizedValue := normalizeStructuredComparable(value)
		if normalizedValue == "" || isStructuredQuestionValue(value) {
			continue
		}
		if _, ok := seen[normalizedValue]; ok {
			continue
		}
		seen[normalizedValue] = struct{}{}
		values = append(values, value)
	}
	sort.SliceStable(values, func(i, j int) bool {
		return len([]rune(values[i])) > len([]rune(values[j]))
	})
	for _, value := range values {
		if strings.Contains(normalizedQuery, normalizeStructuredComparable(value)) {
			return value
		}
	}
	return ""
}

func isStructuredQuestionValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}
	questionValues := []string{"多少", "几", "什么", "谁", "哪", "哪个", "哪些"}
	for _, item := range questionValues {
		if strings.HasPrefix(trimmed, item) {
			return true
		}
	}
	return false
}

func detectStructuredTargetField(query string, documents []structuredTableDocument) string {
	headers := allStructuredHeaders(documents)
	for _, header := range headers {
		if strings.Contains(query, header) {
			return header
		}
	}
	for _, header := range headers {
		if strings.Contains(header, "薪资") && containsAnyText(query, []string{"工资", "收入", "薪水"}) {
			return header
		}
		if strings.Contains(header, "年龄") && strings.Contains(query, "年纪") {
			return header
		}
		if strings.Contains(header, "教龄") && containsAnyText(query, []string{"教学年限", "任教年限", "工作年限"}) {
			return header
		}
		if strings.Contains(header, "编号") && containsAnyText(query, []string{"工号", "员工号", "教师号"}) {
			return header
		}
		if strings.Contains(header, "姓名") && containsAnyText(query, []string{"名字", "人员", "教师", "老师", "员工"}) {
			return header
		}
	}
	if inferred := inferNumericStructuredTargetField(query, documents); inferred != "" {
		return inferred
	}
	return ""
}

func inferNumericStructuredTargetField(query string, documents []structuredTableDocument) string {
	if containsAnyText(query, []string{"薪资", "工资", "收入", "薪水", "薪酬"}) {
		return firstHeaderContaining(documents, []string{"薪资", "工资", "收入", "薪水", "薪酬"})
	}
	if containsAnyText(query, []string{"年龄", "年纪", "岁数"}) {
		return firstHeaderContaining(documents, []string{"年龄", "年纪", "岁数"})
	}
	if containsAnyText(query, []string{"教龄", "教学年限", "任教年限"}) {
		return firstHeaderContaining(documents, []string{"教龄", "教学年限", "任教年限"})
	}
	if containsAnyText(query, []string{"分数", "成绩", "得分"}) {
		return firstHeaderContaining(documents, []string{"分数", "成绩", "得分"})
	}
	if containsAnyText(query, []string{"价格", "金额", "费用", "成本"}) {
		return firstHeaderContaining(documents, []string{"价格", "金额", "费用", "成本"})
	}
	return ""
}

func firstHeaderContaining(documents []structuredTableDocument, markers []string) string {
	for _, header := range allStructuredHeaders(documents) {
		for _, marker := range markers {
			if strings.Contains(header, marker) {
				return header
			}
		}
	}
	return ""
}

func allStructuredHeaders(documents []structuredTableDocument) []string {
	seen := map[string]struct{}{}
	headers := make([]string, 0)
	for _, document := range documents {
		for _, table := range document.Tables {
			for _, header := range table.Headers {
				clean := strings.TrimSpace(header)
				if clean == "" {
					continue
				}
				if _, ok := seen[clean]; ok {
					continue
				}
				seen[clean] = struct{}{}
				headers = append(headers, clean)
			}
		}
	}
	sort.SliceStable(headers, func(i, j int) bool {
		return len([]rune(headers[i])) > len([]rune(headers[j]))
	})
	return headers
}

func trimQueryValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, " ，。；;、?？!！'\"`")
	stoppers := []string{"的", "，", "。", "；", ";", "、", "?", "？", "!", "！", "并且", "然后"}
	for _, stopper := range stoppers {
		if index := strings.Index(value, stopper); index > 0 {
			value = strings.TrimSpace(value[:index])
		}
	}
	return value
}

func isStructuredCountQuestion(query string) bool {
	return containsAnyText(query, []string{"多少", "几", "数量", "总数", "共", "总共有"}) &&
		containsAnyText(query, []string{"记录", "行", "条", "数据", "人员", "名单", "用户", "教师", "老师", "员工"})
}

func buildStructuredDataResult(query string, plan structuredQueryPlan, documents []structuredTableDocument) (StructuredDataQueryResult, bool) {
	allRows := collectStructuredRows(documents, "", "")
	queryRows := allRows
	if plan.FilterField != "" && plan.FilterValue != "" {
		queryRows = collectStructuredRows(documents, plan.FilterField, plan.FilterValue)
	}
	result := StructuredDataQueryResult{
		Query:       strings.TrimSpace(query),
		Intent:      string(plan.Intent),
		FilterField: plan.FilterField,
		FilterValue: plan.FilterValue,
		TargetField: plan.TargetField,
		TotalRows:   len(allRows),
		Columns:     allStructuredHeaders(documents),
	}

	switch plan.Intent {
	case structuredIntentCount:
		result.MatchedRows = len(allRows)
		return result, true
	case structuredIntentPreview:
		limit := structuredQueryRowLimit
		if containsAnyText(query, []string{"完整", "全部", "所有"}) {
			limit *= 2
		}
		result.MatchedRows = len(allRows)
		result.Rows, result.RowsTruncated = structuredResultRows(allRows, limit)
		return result, true
	case structuredIntentFilter:
		matches := collectStructuredRows(documents, plan.FilterField, plan.FilterValue)
		result.MatchedRows = len(matches)
		result.Rows, result.RowsTruncated = structuredResultRows(matches, structuredQueryRowLimit)
		return result, true
	case structuredIntentMax, structuredIntentMin:
		return buildStructuredExtremumResult(result, plan, queryRows)
	case structuredIntentAverage:
		return buildStructuredAverageResult(result, plan, queryRows)
	case structuredIntentGroup:
		return buildStructuredGroupResult(result, plan, queryRows)
	default:
		return StructuredDataQueryResult{}, false
	}
}

func buildStructuredExtremumResult(result StructuredDataQueryResult, plan structuredQueryPlan, rows []structuredRowMatch) (StructuredDataQueryResult, bool) {
	targetField := strings.TrimSpace(plan.TargetField)
	if targetField == "" {
		return StructuredDataQueryResult{}, false
	}

	var best *structuredRowMatch
	bestValue := 0.0
	sampleCount := 0
	for _, row := range rows {
		index := headerIndex(row.Table.Headers, targetField)
		if index < 0 || index >= len(row.Row.Values) {
			continue
		}
		value, ok := parseStructuredNumber(row.Row.Values[index])
		if !ok {
			continue
		}
		sampleCount++
		if best == nil ||
			(plan.Intent == structuredIntentMax && value > bestValue) ||
			(plan.Intent == structuredIntentMin && value < bestValue) {
			item := row
			best = &item
			bestValue = value
		}
	}
	if best == nil {
		return StructuredDataQueryResult{}, false
	}

	result.MatchedRows = 1
	result.Rows, _ = structuredResultRows([]structuredRowMatch{*best}, 1)
	result.Aggregate = &StructuredDataAggregate{
		Operation:   string(plan.Intent),
		Field:       targetField,
		Value:       bestValue,
		SampleCount: sampleCount,
	}
	return result, true
}

func buildStructuredAverageResult(result StructuredDataQueryResult, plan structuredQueryPlan, rows []structuredRowMatch) (StructuredDataQueryResult, bool) {
	targetField := strings.TrimSpace(plan.TargetField)
	if targetField == "" {
		return StructuredDataQueryResult{}, false
	}

	total := 0.0
	count := 0
	numericRows := make([]structuredRowMatch, 0, len(rows))
	for _, row := range rows {
		index := headerIndex(row.Table.Headers, targetField)
		if index < 0 || index >= len(row.Row.Values) {
			continue
		}
		value, ok := parseStructuredNumber(row.Row.Values[index])
		if !ok {
			continue
		}
		total += value
		count++
		numericRows = append(numericRows, row)
	}
	if count == 0 {
		return StructuredDataQueryResult{}, false
	}

	result.MatchedRows = count
	result.Rows, result.RowsTruncated = structuredResultRows(numericRows, structuredQueryRowLimit)
	result.Aggregate = &StructuredDataAggregate{
		Operation:   string(structuredIntentAverage),
		Field:       targetField,
		Value:       total / float64(count),
		SampleCount: count,
	}
	return result, true
}

func buildStructuredGroupResult(result StructuredDataQueryResult, plan structuredQueryPlan, rows []structuredRowMatch) (StructuredDataQueryResult, bool) {
	targetField := strings.TrimSpace(plan.TargetField)
	if targetField == "" {
		return StructuredDataQueryResult{}, false
	}

	counts := map[string]int{}
	for _, row := range rows {
		index := headerIndex(row.Table.Headers, targetField)
		if index < 0 || index >= len(row.Row.Values) {
			continue
		}
		value := strings.TrimSpace(row.Row.Values[index])
		counts[value]++
	}
	if len(counts) == 0 {
		return StructuredDataQueryResult{}, false
	}

	result.Groups = make([]StructuredDataGroup, 0, len(counts))
	for value, count := range counts {
		result.Groups = append(result.Groups, StructuredDataGroup{Value: value, Count: count})
		result.MatchedRows += count
	}
	result.Rows, result.RowsTruncated = structuredResultRows(rows, structuredQueryRowLimit)
	sort.Slice(result.Groups, func(i, j int) bool {
		if result.Groups[i].Count == result.Groups[j].Count {
			return result.Groups[i].Value < result.Groups[j].Value
		}
		return result.Groups[i].Count > result.Groups[j].Count
	})
	return result, true
}

func collectStructuredRows(documents []structuredTableDocument, field, value string) []structuredRowMatch {
	matches := make([]structuredRowMatch, 0)
	for _, document := range documents {
		for _, table := range document.Tables {
			filterIndex := headerIndex(table.Headers, field)
			for _, row := range table.Rows {
				if filterIndex >= 0 {
					if filterIndex >= len(row.Values) {
						continue
					}
					if !strings.EqualFold(strings.TrimSpace(row.Values[filterIndex]), strings.TrimSpace(value)) {
						continue
					}
				}
				matches = append(matches, structuredRowMatch{Document: document.Document, Table: table, Row: row})
			}
		}
	}
	return matches
}

func structuredResultRows(matches []structuredRowMatch, limit int) ([]StructuredDataResultRow, bool) {
	if len(matches) == 0 || limit <= 0 {
		return nil, len(matches) > 0
	}
	truncated := len(matches) > limit
	if truncated {
		matches = matches[:limit]
	}

	rows := make([]StructuredDataResultRow, 0, len(matches))
	for _, match := range matches {
		values := make(map[string]string, len(match.Table.Headers))
		for index, header := range match.Table.Headers {
			value := ""
			if index < len(match.Row.Values) {
				value = match.Row.Values[index]
			}
			values[header] = value
		}
		rows = append(rows, StructuredDataResultRow{
			KnowledgeBaseID: match.Document.KnowledgeBaseID,
			DocumentID:      match.Document.ID,
			DocumentName:    match.Document.Name,
			Sheet:           match.Table.Sheet,
			RowNumber:       match.Row.Number,
			Values:          values,
		})
	}
	return rows, truncated
}

func headerIndex(headers []string, field string) int {
	field = strings.TrimSpace(field)
	if field == "" {
		return -1
	}
	for index, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header), field) {
			return index
		}
	}
	return -1
}

func parseStructuredNumber(value string) (float64, bool) {
	cleaned := strings.TrimSpace(value)
	cleaned = strings.ReplaceAll(cleaned, ",", "")
	cleaned = strings.TrimSuffix(cleaned, "%")
	if cleaned == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(cleaned, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, false
	}
	return parsed, true
}

func structuredDataSources(documents []structuredTableDocument) []map[string]string {
	sources := make([]map[string]string, 0, len(documents))
	for _, item := range documents {
		sources = append(sources, map[string]string{
			"knowledgeBaseId": item.Document.KnowledgeBaseID,
			"documentId":      item.Document.ID,
			"documentName":    item.Document.Name,
			"sourceType":      "structured-data",
		})
	}
	return sources
}

func (s *AppService) retrieveStructuredDataChunks(req model.ChatCompletionRequest) ([]RetrievedChunk, bool, error) {
	result, sources, ok, err := s.QueryStructuredData(req)
	if err != nil || !ok {
		return nil, ok, err
	}
	chunks := structuredDataResultChunks(result, sources)
	if len(chunks) == 0 {
		return nil, false, nil
	}
	return chunks, true, nil
}

func structuredDataResultChunks(result StructuredDataQueryResult, sources []map[string]string) []RetrievedChunk {
	if len(result.Rows) > 0 {
		chunks := make([]RetrievedChunk, 0, len(result.Rows))
		for index, row := range result.Rows {
			text := structuredDataResultText(result, &row)
			chunks = append(chunks, RetrievedChunk{
				DocumentChunk: DocumentChunk{
					ID:              fmt.Sprintf("structured-query-%s-%d", row.DocumentID, row.RowNumber),
					KnowledgeBaseID: row.KnowledgeBaseID,
					DocumentID:      row.DocumentID,
					DocumentName:    row.DocumentName,
					Text:            text,
					Index:           maxInt(row.RowNumber-1, index),
					Kind:            "structured_query",
					TableRow:        row.RowNumber,
					TableColumns:    append([]string(nil), result.Columns...),
				},
				Score:             1,
				RawScore:          1,
				RetrievalChannels: []string{"structured"},
			})
		}
		return chunks
	}

	text := structuredDataResultText(result, nil)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	chunks := make([]RetrievedChunk, 0, maxInt(len(sources), 1))
	if len(sources) == 0 {
		chunks = append(chunks, RetrievedChunk{
			DocumentChunk: DocumentChunk{
				ID:    "structured-query-result",
				Text:  text,
				Kind:  "structured_query",
				Index: 0,
			},
			Score:             1,
			RawScore:          1,
			RetrievalChannels: []string{"structured"},
		})
		return chunks
	}
	for index, source := range sources {
		documentID := strings.TrimSpace(source["documentId"])
		chunks = append(chunks, RetrievedChunk{
			DocumentChunk: DocumentChunk{
				ID:              fmt.Sprintf("structured-query-%s-%d", documentID, index),
				KnowledgeBaseID: strings.TrimSpace(source["knowledgeBaseId"]),
				DocumentID:      documentID,
				DocumentName:    strings.TrimSpace(source["documentName"]),
				Text:            text,
				Index:           index,
				Kind:            "structured_query",
			},
			Score:             1,
			RawScore:          1,
			RetrievalChannels: []string{"structured"},
		})
	}
	return chunks
}

func structuredDataResultText(result StructuredDataQueryResult, row *StructuredDataResultRow) string {
	parts := []string{"结构化查询结果。"}
	if result.TotalRows > 0 {
		parts = append(parts, fmt.Sprintf("总数据行数：%d。", result.TotalRows))
	}
	if result.MatchedRows > 0 {
		parts = append(parts, fmt.Sprintf("匹配记录数：%d。", result.MatchedRows))
	}
	if len(result.Columns) > 0 {
		parts = append(parts, fmt.Sprintf("字段：%s。", strings.Join(result.Columns, "、")))
	}
	if result.Intent == string(structuredIntentCount) {
		parts = append(parts, fmt.Sprintf("共有%d条数据记录。数据行数是%d。", result.TotalRows, result.TotalRows))
	}
	if result.Aggregate != nil {
		parts = append(parts, structuredAggregateText(*result.Aggregate))
	}
	if result.RowsTruncated {
		parts = append(parts, "参与计算的记录较多，当前仅展示部分证据行。")
	}
	if len(result.Groups) > 0 && strings.TrimSpace(result.TargetField) != "" {
		groupParts := make([]string, 0, len(result.Groups))
		for _, group := range result.Groups {
			groupParts = append(groupParts, fmt.Sprintf("%s(%d)", group.Value, group.Count))
		}
		parts = append(parts, fmt.Sprintf("字段“%s”的主要分布为：%s。", result.TargetField, strings.Join(groupParts, "、")))
	}
	if row != nil {
		parts = append(parts, structuredRowText(*row))
	}
	return strings.Join(parts, "")
}

func structuredAggregateText(aggregate StructuredDataAggregate) string {
	field := strings.TrimSpace(aggregate.Field)
	value := formatStructuredNumberForEvidence(aggregate.Value)
	switch aggregate.Operation {
	case string(structuredIntentMax):
		return fmt.Sprintf("字段“%s”的最大值是%s。样本数：%d。", field, value, aggregate.SampleCount)
	case string(structuredIntentMin):
		return fmt.Sprintf("字段“%s”的最小值是%s。样本数：%d。", field, value, aggregate.SampleCount)
	case string(structuredIntentAverage):
		return fmt.Sprintf("字段“%s”的平均值是%s。样本数：%d。", field, value, aggregate.SampleCount)
	default:
		return fmt.Sprintf("字段“%s”的%s结果是%s。样本数：%d。", field, aggregate.Operation, value, aggregate.SampleCount)
	}
}

func structuredRowText(row StructuredDataResultRow) string {
	keys := make([]string, 0, len(row.Values))
	for key := range row.Values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := make([]string, 0, len(keys))
	for _, key := range keys {
		fields = append(fields, fmt.Sprintf("%s：%s", key, row.Values[key]))
	}
	prefix := fmt.Sprintf("第%d行：", row.RowNumber)
	if strings.TrimSpace(row.Sheet) != "" {
		prefix = fmt.Sprintf("第%d行：工作表：%s；", row.RowNumber, row.Sheet)
	}
	return prefix + strings.Join(fields, "。") + "。"
}

func formatStructuredNumberForEvidence(value float64) string {
	plain := strconv.FormatFloat(value, 'f', -1, 64)
	fixed := fmt.Sprintf("%.2f", value)
	if fixed != plain {
		return plain + "（" + fixed + "）"
	}
	return plain
}
