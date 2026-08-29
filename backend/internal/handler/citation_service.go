package handler

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// scoredCitationSource keeps citation calibration independent from HTTP request handling.
// The handler only supplies the question, answer and candidate sources.
type scoredCitationSource struct {
	source         map[string]string
	score          int
	index          int
	answerHits     int
	answerOnlyHits int
	queryHits      int
}

func calibrateCitationSources(question, answer string, sources []map[string]string, knowledgeBaseID, documentID string) []map[string]string {
	if len(sources) == 0 {
		return nil
	}
	if isCitationAbstention(answer) {
		return nil
	}

	answerTerms := citationTerms(answer)
	queryTerms := citationTerms(question)
	answerOnlyTerms := termsNotIn(answerTerms, queryTerms)
	scored := make([]scoredCitationSource, 0, len(sources))
	for index, source := range sources {
		if !isDocumentCitationSource(source) || !citationSourceMatchesScope(source, knowledgeBaseID, documentID) {
			continue
		}

		// Only the stored snippet is evidence. Metadata such as a filename can
		// help users locate a source, but must never make an unrelated source
		// look like it supports the answer.
		text := citationSourceEvidenceText(source)
		answerHits := citationHitCount(answerTerms, text)
		answerOnlyHits := citationHitCount(answerOnlyTerms, text)
		queryHits := citationHitCount(queryTerms, text)
		rawScore := parseCitationRawScore(source["score"])
		score := answerOnlyHits*8 + answerHits*4 + queryHits*2 + int(rawScore*3)
		if !sourcePassesCitationGate(answerHits, answerOnlyHits, queryHits, len(answerTerms), len(answerOnlyTerms)) {
			continue
		}

		scored = append(scored, scoredCitationSource{
			source:         cloneStringMap(source),
			score:          score,
			index:          index,
			answerHits:     answerHits,
			answerOnlyHits: answerOnlyHits,
			queryHits:      queryHits,
		})
	}

	if len(scored) == 0 {
		return nil
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].index < scored[j].index
		}
		return scored[i].score > scored[j].score
	})

	const limit = 4
	out := make([]map[string]string, 0, minInt(len(scored), limit))
	seen := make(map[string]struct{}, len(scored))
	for _, item := range scored {
		key := strings.TrimSpace(item.source["chunkId"])
		if key == "" {
			key = strings.TrimSpace(item.source["documentId"]) + ":" + strings.TrimSpace(item.source["snippet"])
		}
		if key != "" {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, item.source)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func citationSourceMatchesScope(source map[string]string, knowledgeBaseID, documentID string) bool {
	knowledgeBaseID = strings.TrimSpace(knowledgeBaseID)
	documentID = strings.TrimSpace(documentID)
	if knowledgeBaseID != "" && strings.TrimSpace(source["knowledgeBaseId"]) != knowledgeBaseID {
		return false
	}
	if documentID != "" && strings.TrimSpace(source["documentId"]) != documentID {
		return false
	}
	return true
}

func isDocumentCitationSource(source map[string]string) bool {
	for _, key := range []string{"knowledgeBaseId", "documentId", "documentName", "chunkId", "snippet"} {
		if strings.TrimSpace(source[key]) == "" {
			return false
		}
	}
	return true
}

func citationSourceEvidenceText(source map[string]string) string {
	return strings.TrimSpace(source["snippet"])
}

func sourcePassesCitationGate(answerHits, answerOnlyHits, queryHits, answerTermCount, answerOnlyTermCount int) bool {
	if answerTermCount == 0 || answerHits == 0 {
		return false
	}
	if answerOnlyTermCount > 0 {
		// At least one answer-only term prevents a source that merely repeats
		// the question subject from being cited for a different answer.
		return answerOnlyHits >= 1 && (queryHits >= 1 || answerHits >= 2)
	}
	if answerHits >= 2 && queryHits >= 1 {
		return true
	}
	return false
}

func termsNotIn(values, excluded []string) []string {
	if len(values) == 0 {
		return nil
	}
	excludedSet := make(map[string]struct{}, len(excluded))
	for _, value := range excluded {
		excludedSet[value] = struct{}{}
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := excludedSet[value]; exists {
			continue
		}
		result = append(result, value)
	}
	return removeContainedTerms(result)
}

func removeContainedTerms(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := make([]string, 0, len(values))
	for index, value := range values {
		isContained := false
		for otherIndex, other := range values {
			// Two-rune fragments are usually generic Chinese word pieces
			// (for example, "成员" inside "成员甲"). Keep longer fragments
			// even when a four-rune window contains them, because adjacent
			// wording often changes between a question and an answer.
			if index == otherIndex || len([]rune(value)) > 2 || len([]rune(other)) <= len([]rune(value)) {
				continue
			}
			if strings.Contains(other, value) {
				isContained = true
				break
			}
		}
		if !isContained {
			result = append(result, value)
		}
	}
	return result
}

func isCitationAbstention(answer string) bool {
	normalized := strings.ToLower(strings.TrimSpace(answer))
	if normalized == "" {
		return true
	}
	for _, phrase := range []string{
		"无法确认",
		"无法确定",
		"无法回答",
		"没有足够信息",
		"缺少足够信息",
		"未找到可靠证据",
		"未找到相关证据",
		"暂无可靠证据",
		"cannot determine",
		"cannot answer",
		"no reliable evidence",
	} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func parseCitationRawScore(value string) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	if parsed < 0 {
		return 0
	}
	if parsed > 1 {
		return 1
	}
	return parsed
}

func citationHitCount(terms []string, text string) int {
	if len(terms) == 0 || strings.TrimSpace(text) == "" {
		return 0
	}
	lowered := strings.ToLower(text)
	hits := 0
	for _, term := range terms {
		if strings.Contains(lowered, term) {
			hits++
		}
	}
	return hits
}

func citationTerms(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}

	terms := make([]string, 0)
	var wordRunes []rune
	var hanRunes []rune
	flushWord := func() {
		if len(wordRunes) >= 2 {
			terms = append(terms, string(wordRunes))
		}
		wordRunes = wordRunes[:0]
	}
	flushHan := func() {
		if len(hanRunes) >= 2 {
			maxN := minInt(4, len(hanRunes))
			for n := 2; n <= maxN; n++ {
				for i := 0; i+n <= len(hanRunes); i++ {
					terms = append(terms, string(hanRunes[i:i+n]))
				}
			}
		}
		hanRunes = hanRunes[:0]
	}

	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Han):
			flushWord()
			hanRunes = append(hanRunes, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushHan()
			wordRunes = append(wordRunes, r)
		default:
			flushWord()
			flushHan()
		}
	}
	flushWord()
	flushHan()

	stopTerms := map[string]struct{}{
		"什么": {}, "多少": {}, "几个": {}, "如何": {}, "怎么": {}, "是否": {},
		"是谁": {}, "哪些": {}, "有没有": {}, "请问": {}, "告诉": {}, "一下": {},
		"当前": {}, "核心": {}, "观点": {}, "信息": {}, "文档": {}, "内容": {},
		"回答": {}, "来源": {}, "显示": {}, "可以": {}, "进行": {}, "相关": {},
		"一个": {}, "一种": {}, "世界": {}, "同时": {}, "关系": {},
		"the": {}, "and": {}, "for": {}, "with": {}, "what": {}, "which": {},
		"who": {}, "how": {}, "where": {}, "when": {}, "is": {}, "are": {},
	}
	out := make([]string, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(strings.ToLower(term))
		if len([]rune(term)) < 2 {
			continue
		}
		if _, stop := stopTerms[term]; stop {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
	}
	return out
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input)+1)
	for key, value := range input {
		out[key] = value
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
