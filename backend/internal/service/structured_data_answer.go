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
	structuredAnswerPreviewLimit = 20
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

type structuredTableDocument struct {
	Document model.Document
	Tables   []util.StructuredTable
}

type structuredRowMatch struct {
	Table util.StructuredTable
	Row   util.StructuredTableRow
}

func (s *AppService) TryBuildStructuredDataAnswer(req model.ChatCompletionRequest) (string, []map[string]string, bool, error) {
	query := latestUserMessage(req.Messages)
	if !looksLikeStructuredDataQuery(query) {
		return "", nil, false, nil
	}

	documents := s.resolveStructuredTableDocuments(req)
	if len(documents) == 0 {
		return "", nil, false, nil
	}

	tables := make([]structuredTableDocument, 0, len(documents))
	for _, document := range documents {
		parsed, err := util.ExtractStructuredTables(document.Path)
		if err != nil {
			return "", nil, true, err
		}
		if len(parsed) == 0 {
			continue
		}
		tables = append(tables, structuredTableDocument{Document: document, Tables: parsed})
	}
	if len(tables) == 0 {
		return "", nil, false, nil
	}

	plan := buildStructuredQueryPlan(query, tables)
	if plan.Intent == "" {
		return "", nil, false, nil
	}

	content := renderStructuredQueryAnswer(query, plan, tables)
	if strings.TrimSpace(content) == "" {
		return "", nil, false, nil
	}

	sources := structuredDataSources(tables)
	return content, sources, true, nil
}

func looksLikeStructuredDataQuery(query string) bool {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return false
	}
	markers := []string{
		"表格", "数据", "记录", "行", "条", "字段", "列", "名单", "展示", "列出", "筛选",
		"查找", "查询", "统计", "分布", "平均", "最大", "最小", "最高", "最低", "最多", "最少",
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
		documents := structuredDocumentsFromKnowledgeBase(kb)
		if len(documents) == 1 {
			return documents
		}
		return nil
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
	filterField, filterValue := detectStructuredFilter(normalized, documents)
	if filterField != "" && filterValue != "" {
		return structuredQueryPlan{Intent: structuredIntentFilter, FilterField: filterField, FilterValue: filterValue}
	}

	targetField := detectStructuredTargetField(normalized, documents)
	if containsAnyText(normalized, []string{"最高", "最大", "最多"}) {
		return structuredQueryPlan{Intent: structuredIntentMax, TargetField: targetField}
	}
	if containsAnyText(normalized, []string{"最低", "最小", "最少"}) {
		return structuredQueryPlan{Intent: structuredIntentMin, TargetField: targetField}
	}
	if containsAnyText(normalized, []string{"平均", "均值"}) {
		return structuredQueryPlan{Intent: structuredIntentAverage, TargetField: targetField}
	}
	if containsAnyText(normalized, []string{"分布", "按", "每个", "各"}) && targetField != "" {
		return structuredQueryPlan{Intent: structuredIntentGroup, TargetField: targetField}
	}
	if isStructuredCountQuestion(normalized) {
		return structuredQueryPlan{Intent: structuredIntentCount}
	}
	if containsAnyText(normalized, []string{"展示", "列出", "查看", "读取", "表格", "数据", "名单", "明细", "详情"}) {
		return structuredQueryPlan{Intent: structuredIntentPreview}
	}
	return structuredQueryPlan{}
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
			value := strings.TrimSpace(strings.TrimPrefix(rest, marker))
			value = trimQueryValue(value)
			if value != "" && !isStructuredQuestionValue(value) {
				return header, value
			}
		}
	}
	return "", ""
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

func renderStructuredQueryAnswer(query string, plan structuredQueryPlan, documents []structuredTableDocument) string {
	switch plan.Intent {
	case structuredIntentCount:
		return renderStructuredCountAnswer(documents)
	case structuredIntentFilter:
		return renderStructuredFilterAnswer(plan, documents)
	case structuredIntentMax, structuredIntentMin:
		return renderStructuredExtremumAnswer(plan, documents)
	case structuredIntentAverage:
		return renderStructuredAverageAnswer(plan, documents)
	case structuredIntentGroup:
		return renderStructuredGroupAnswer(plan, documents)
	case structuredIntentPreview:
		return renderStructuredPreviewAnswer(query, documents)
	default:
		return ""
	}
}

func renderStructuredCountAnswer(documents []structuredTableDocument) string {
	total := 0
	for _, document := range documents {
		for _, table := range document.Tables {
			total += len(table.Rows)
		}
	}
	return strings.TrimSpace(fmt.Sprintf(`## 数据统计

### 记录数量

- **总记录数**：%d 条

> 该结果由后端直接读取结构化文件行数得到。`, total))
}

func renderStructuredPreviewAnswer(query string, documents []structuredTableDocument) string {
	limit := structuredAnswerPreviewLimit
	if containsAnyText(query, []string{"完整", "全部", "所有"}) {
		limit = structuredAnswerPreviewLimit * 2
	}

	matches := collectStructuredRows(documents, "", "")
	if len(matches) == 0 {
		return ""
	}
	return renderStructuredRowsAnswer("数据预览", matches, limit)
}

func renderStructuredFilterAnswer(plan structuredQueryPlan, documents []structuredTableDocument) string {
	matches := collectStructuredRows(documents, plan.FilterField, plan.FilterValue)
	if len(matches) == 0 {
		return strings.TrimSpace(fmt.Sprintf(`## 筛选结果

### 命中记录

- **筛选条件**：%s = %s
- **命中数量**：0 条

> 当前结构化文件中没有找到符合条件的记录。`, plan.FilterField, plan.FilterValue))
	}
	return renderStructuredRowsAnswer(
		fmt.Sprintf("筛选结果：%s = %s", plan.FilterField, plan.FilterValue),
		matches,
		structuredAnswerPreviewLimit,
	)
}

func renderStructuredExtremumAnswer(plan structuredQueryPlan, documents []structuredTableDocument) string {
	targetField := strings.TrimSpace(plan.TargetField)
	if targetField == "" {
		return ""
	}

	var best *structuredRowMatch
	var bestValue float64
	for _, match := range collectStructuredRows(documents, "", "") {
		index := headerIndex(match.Table.Headers, targetField)
		if index < 0 || index >= len(match.Row.Values) {
			continue
		}
		value, ok := parseStructuredNumber(match.Row.Values[index])
		if !ok {
			continue
		}
		if best == nil ||
			(plan.Intent == structuredIntentMax && value > bestValue) ||
			(plan.Intent == structuredIntentMin && value < bestValue) {
			item := match
			best = &item
			bestValue = value
		}
	}
	if best == nil {
		return ""
	}

	title := "最大值"
	if plan.Intent == structuredIntentMin {
		title = "最小值"
	}
	return strings.TrimSpace(fmt.Sprintf(`## %s

### 结果

- **字段**：%s
- **数值**：%s

%s`, title, targetField, formatStructuredNumber(bestValue), markdownTable(best.Table.Headers, []util.StructuredTableRow{best.Row})))
}

func renderStructuredAverageAnswer(plan structuredQueryPlan, documents []structuredTableDocument) string {
	targetField := strings.TrimSpace(plan.TargetField)
	if targetField == "" {
		return ""
	}

	total := 0.0
	count := 0
	for _, match := range collectStructuredRows(documents, "", "") {
		index := headerIndex(match.Table.Headers, targetField)
		if index < 0 || index >= len(match.Row.Values) {
			continue
		}
		value, ok := parseStructuredNumber(match.Row.Values[index])
		if !ok {
			continue
		}
		total += value
		count++
	}
	if count == 0 {
		return ""
	}

	return strings.TrimSpace(fmt.Sprintf(`## 平均值

### 结果

- **字段**：%s
- **有效记录数**：%d 条
- **平均值**：%s

> 该结果由后端直接读取结构化文件并计算得到。`, targetField, count, formatStructuredNumber(total/float64(count))))
}

func renderStructuredGroupAnswer(plan structuredQueryPlan, documents []structuredTableDocument) string {
	targetField := strings.TrimSpace(plan.TargetField)
	if targetField == "" {
		return ""
	}

	counts := map[string]int{}
	for _, match := range collectStructuredRows(documents, "", "") {
		index := headerIndex(match.Table.Headers, targetField)
		if index < 0 || index >= len(match.Row.Values) {
			continue
		}
		value := strings.TrimSpace(match.Row.Values[index])
		if value == "" {
			value = "空值"
		}
		counts[value]++
	}
	if len(counts) == 0 {
		return ""
	}

	type item struct {
		Value string
		Count int
	}
	items := make([]item, 0, len(counts))
	for value, count := range counts {
		items = append(items, item{Value: value, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Value < items[j].Value
		}
		return items[i].Count > items[j].Count
	})

	builder := &strings.Builder{}
	fmt.Fprintf(builder, "## 分布统计\n\n### %s\n\n", targetField)
	builder.WriteString("|取值|数量|\n|---|---:|\n")
	for _, item := range items {
		fmt.Fprintf(builder, "|%s|%d|\n", escapeMarkdownCell(item.Value), item.Count)
	}
	return strings.TrimSpace(builder.String())
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
				matches = append(matches, structuredRowMatch{Table: table, Row: row})
			}
		}
	}
	return matches
}

func renderStructuredRowsAnswer(title string, matches []structuredRowMatch, limit int) string {
	if len(matches) == 0 || limit <= 0 {
		return ""
	}
	if len(matches) < limit {
		limit = len(matches)
	}

	builder := &strings.Builder{}
	fmt.Fprintf(builder, "## %s\n\n### 命中记录\n\n", title)
	fmt.Fprintf(builder, "- **总数**：%d 条\n", len(matches))
	if len(matches) > limit {
		fmt.Fprintf(builder, "- **当前展示**：前 %d 条\n", limit)
	}
	builder.WriteString("\n")

	currentTable := matches[0].Table
	rows := make([]util.StructuredTableRow, 0, limit)
	for _, match := range matches[:limit] {
		if !sameStructuredTable(currentTable, match.Table) {
			builder.WriteString(markdownTable(currentTable.Headers, rows))
			builder.WriteString("\n\n")
			currentTable = match.Table
			rows = rows[:0]
		}
		rows = append(rows, match.Row)
	}
	builder.WriteString(markdownTable(currentTable.Headers, rows))

	if len(matches) > limit {
		builder.WriteString("\n\n> 数据较多，已按当前上下文限制展示部分记录。")
	}
	return strings.TrimSpace(builder.String())
}

func markdownTable(headers []string, rows []util.StructuredTableRow) string {
	builder := &strings.Builder{}
	builder.WriteString("|")
	for _, header := range headers {
		builder.WriteString(escapeMarkdownCell(header))
		builder.WriteString("|")
	}
	builder.WriteString("\n|")
	for range headers {
		builder.WriteString("---|")
	}
	builder.WriteString("\n")

	for _, row := range rows {
		builder.WriteString("|")
		for index := range headers {
			value := ""
			if index < len(row.Values) {
				value = row.Values[index]
			}
			builder.WriteString(escapeMarkdownCell(value))
			builder.WriteString("|")
		}
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

func sameStructuredTable(left, right util.StructuredTable) bool {
	return left.FileName == right.FileName && left.Sheet == right.Sheet && strings.Join(left.Headers, "\x00") == strings.Join(right.Headers, "\x00")
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

func formatStructuredNumber(value float64) string {
	if math.Abs(value-math.Round(value)) < 1e-9 {
		return fmt.Sprintf("%.0f", value)
	}
	return fmt.Sprintf("%.2f", value)
}

func escapeMarkdownCell(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	if value == "" {
		return "-"
	}
	return value
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
