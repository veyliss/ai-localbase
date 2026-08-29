package service

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// CitationSupportReport describes how much of an answer can be grounded in
// the retrieved snippets. SupportedSources is an internal hand-off to the
// HTTP/MCP boundary and is intentionally omitted from JSON to avoid duplicating
// the public source list.
type CitationSupportReport struct {
	Status              string                 `json:"status"`
	Summary             string                 `json:"summary"`
	ClaimCount          int                    `json:"claimCount"`
	SupportedClaimCount int                    `json:"supportedClaimCount"`
	Coverage            float64                `json:"coverage"`
	Issues              []string               `json:"issues,omitempty"`
	Claims              []CitationClaimSupport `json:"claims,omitempty"`
	SupportedSources    []map[string]string    `json:"-"`
}

// CitationClaimSupport keeps the diagnostic small enough for chat metadata
// while making a partial answer explainable in the debug console.
type CitationClaimSupport struct {
	Text              string   `json:"text"`
	Supported         bool     `json:"supported"`
	EvidenceIDs       []string `json:"evidenceIds,omitempty"`
	MissingAnchors    []string `json:"missingAnchors,omitempty"`
	MatchedTermCount  int      `json:"matchedTermCount"`
	RequiredTermCount int      `json:"requiredTermCount"`
}

type citationSourceSupport struct {
	source          map[string]string
	index           int
	score           int
	supportedClaims int
}

type citationClaim struct {
	text    string
	terms   []string
	anchors []string
}

var citationAnchorPattern = regexp.MustCompile(`[0-9][0-9A-Za-z_.:/+%#-]*`)

// AssessCitationSupport evaluates answer claims against source snippets. It
// deliberately uses lexical anchors and exact numeric matching: this is a
// conservative post-generation check, not another semantic answer generator.
func AssessCitationSupport(question, answer string, sources []map[string]string, knowledgeBaseID, documentID string) CitationSupportReport {
	report := CitationSupportReport{
		Status:           "unsupported",
		Summary:          "没有可验证的证据支撑答案。",
		SupportedSources: nil,
	}

	if isEvidenceAbstention(answer) {
		report.Status = "abstained"
		report.Summary = "回答明确表示资料不足，不附加引用。"
		report.Issues = []string{"答案为资料不足或拒答，不应展示确定性引用"}
		return report
	}

	claims := splitCitationClaims(answer)
	if len(claims) == 0 {
		report.Issues = []string{"答案没有可分析的事实陈述"}
		return report
	}
	if len(claims) > 24 {
		claims = claims[:24]
	}
	report.ClaimCount = len(claims)

	questionTerms := evidenceSupportTerms(question)
	scoredSources := make([]citationSourceSupport, 0, len(sources))
	for index, source := range sources {
		if !isCompleteCitationSource(source) || !citationSourceMatchesScope(source, knowledgeBaseID, documentID) {
			continue
		}
		scoredSources = append(scoredSources, citationSourceSupport{
			source: cloneEvidenceSource(source),
			index:  index,
			score:  parseEvidenceScore(source["score"]),
		})
	}

	for _, claim := range claims {
		claimResult := CitationClaimSupport{
			Text:              truncateEvidenceRunes(claim.text, 180),
			RequiredTermCount: len(claim.terms),
		}
		bestSourceIndex := -1
		bestScore := -1
		bestMatched := 0
		bestMissing := []string(nil)
		for index := range scoredSources {
			sourceText := strings.TrimSpace(scoredSources[index].source["snippet"])
			matched, missing, supported := scoreCitationClaim(claim, questionTerms, sourceText)
			if !supported {
				if len(missing) > len(bestMissing) {
					bestMissing = missing
				}
				continue
			}
			score := matched*10 + scoredSources[index].score
			if score > bestScore {
				bestSourceIndex = index
				bestScore = score
				bestMatched = matched
				bestMissing = missing
			}
		}

		claimResult.MatchedTermCount = bestMatched
		claimResult.MissingAnchors = bestMissing
		if bestSourceIndex >= 0 {
			claimResult.Supported = true
			report.SupportedClaimCount++
			scoredSources[bestSourceIndex].supportedClaims++
			if evidenceID := strings.TrimSpace(scoredSources[bestSourceIndex].source["evidenceId"]); evidenceID != "" {
				claimResult.EvidenceIDs = []string{evidenceID}
			}
		}
		report.Claims = append(report.Claims, claimResult)
	}

	if report.ClaimCount > 0 {
		report.Coverage = float64(report.SupportedClaimCount) / float64(report.ClaimCount)
	}

	sort.SliceStable(scoredSources, func(i, j int) bool {
		if scoredSources[i].supportedClaims == scoredSources[j].supportedClaims {
			if scoredSources[i].score == scoredSources[j].score {
				return scoredSources[i].index < scoredSources[j].index
			}
			return scoredSources[i].score > scoredSources[j].score
		}
		return scoredSources[i].supportedClaims > scoredSources[j].supportedClaims
	})
	seen := make(map[string]struct{}, len(scoredSources))
	for _, scored := range scoredSources {
		if scored.supportedClaims == 0 {
			continue
		}
		key := strings.TrimSpace(scored.source["evidenceId"])
		if key == "" {
			key = strings.TrimSpace(scored.source["chunkId"])
		}
		if key != "" {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
		}
		source := cloneEvidenceSource(scored.source)
		source["citationSupport"] = "supported"
		source["supportedClaimCount"] = formatEvidenceInt(scored.supportedClaims)
		report.SupportedSources = append(report.SupportedSources, source)
		if len(report.SupportedSources) >= 4 {
			break
		}
	}

	switch {
	case report.SupportedClaimCount == report.ClaimCount && report.ClaimCount > 0:
		report.Status = "supported"
		report.Summary = "答案中的每条可识别事实陈述都能在引用片段中找到对应依据。"
	case report.SupportedClaimCount > 0:
		report.Status = "partial"
		report.Summary = "只有部分答案陈述能被引用片段直接支撑，未支撑内容不应视为已核实。"
		report.Issues = append(report.Issues, "存在未被引用片段覆盖的答案陈述")
	default:
		report.Issues = append(report.Issues, "答案中的实体、数字或事实词没有出现在引用片段中")
	}
	return report
}

func splitCitationClaims(answer string) []citationClaim {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return nil
	}
	answer = strings.ReplaceAll(answer, "\r\n", "\n")
	answer = strings.ReplaceAll(answer, "\r", "\n")
	lines := strings.Split(answer, "\n")
	claims := make([]citationClaim, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.Trim(line, "|"))
		line = strings.TrimSpace(strings.TrimLeft(line, "#> -*+"))
		if line == "" || isCitationSeparatorLine(line) || strings.HasPrefix(strings.ToLower(line), "```mermaid") {
			continue
		}
		for _, sentence := range splitCitationSentences(line) {
			sentence = strings.TrimSpace(sentence)
			if sentence == "" || isCitationHeading(sentence) || isEvidenceAbstention(sentence) {
				continue
			}
			terms := evidenceSupportTerms(sentence)
			if len(terms) == 0 {
				continue
			}
			claims = append(claims, citationClaim{
				text:    sentence,
				terms:   terms,
				anchors: citationAnchors(sentence),
			})
		}
	}
	return claims
}

func splitCitationSentences(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var result []string
	start := 0
	for index, r := range value {
		if strings.ContainsRune("。！？!?；;", r) {
			if sentence := strings.TrimSpace(value[start : index+len(string(r))]); sentence != "" {
				result = append(result, sentence)
			}
			start = index + len(string(r))
		}
	}
	if tail := strings.TrimSpace(value[start:]); tail != "" {
		result = append(result, tail)
	}
	return result
}

func scoreCitationClaim(claim citationClaim, questionTerms []string, sourceText string) (int, []string, bool) {
	if strings.TrimSpace(sourceText) == "" {
		return 0, append([]string(nil), claim.anchors...), false
	}
	sourceLower := strings.ToLower(sourceText)
	claimTerms := claim.terms
	questionSet := make(map[string]struct{}, len(questionTerms))
	for _, term := range questionTerms {
		questionSet[term] = struct{}{}
	}
	answerOnlyTerms := make([]string, 0, len(claimTerms))
	for _, term := range claimTerms {
		if _, exists := questionSet[term]; !exists {
			answerOnlyTerms = append(answerOnlyTerms, term)
		}
	}
	answerOnlyTerms = removeEvidenceContainedTerms(answerOnlyTerms)

	matched := 0
	for _, term := range claimTerms {
		if strings.Contains(sourceLower, term) {
			matched++
		}
	}
	missing := make([]string, 0, len(claim.anchors))
	for _, anchor := range claim.anchors {
		if !strings.Contains(sourceLower, strings.ToLower(anchor)) {
			missing = append(missing, anchor)
		}
	}
	if len(missing) > 0 {
		return matched, missing, false
	}

	answerOnlyHits := countEvidenceTerms(answerOnlyTerms, sourceLower)
	queryHits := countEvidenceTerms(questionTerms, sourceLower)
	if len(answerOnlyTerms) > 0 {
		return matched, nil, answerOnlyHits > 0 && (queryHits > 0 || matched >= 2)
	}
	return matched, nil, matched >= 2 || (matched > 0 && len(claim.anchors) > 0)
}

func evidenceSupportTerms(text string) []string {
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
			maxN := minEvidenceInt(4, len(hanRunes))
			for n := 2; n <= maxN; n++ {
				for index := 0; index+n <= len(hanRunes); index++ {
					terms = append(terms, string(hanRunes[index:index+n]))
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
		"什么": {}, "多少": {}, "几个": {}, "如何": {}, "怎么": {}, "是否": {}, "是谁": {}, "哪些": {},
		"有没有": {}, "请问": {}, "告诉": {}, "一下": {}, "当前": {}, "核心": {}, "观点": {}, "信息": {},
		"文档": {}, "内容": {}, "回答": {}, "来源": {}, "显示": {}, "可以": {}, "进行": {}, "相关": {},
		"一个": {}, "一种": {}, "根据": {}, "资料": {}, "答案": {}, "the": {}, "and": {}, "for": {},
		"with": {}, "what": {}, "which": {}, "who": {}, "how": {}, "where": {}, "when": {}, "is": {}, "are": {},
	}
	unique := make([]string, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
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
		unique = append(unique, term)
	}
	return unique
}

func citationAnchors(text string) []string {
	matches := citationAnchorPattern.FindAllString(text, -1)
	seen := make(map[string]struct{}, len(matches))
	anchors := make([]string, 0, len(matches))
	for _, match := range matches {
		match = strings.TrimSpace(match)
		if len([]rune(match)) < 2 {
			continue
		}
		if _, exists := seen[match]; exists {
			continue
		}
		seen[match] = struct{}{}
		anchors = append(anchors, match)
	}
	return anchors
}

func isCompleteCitationSource(source map[string]string) bool {
	for _, key := range []string{"knowledgeBaseId", "documentId", "documentName", "chunkId", "snippet"} {
		if strings.TrimSpace(source[key]) == "" {
			return false
		}
	}
	return true
}

func citationSourceMatchesScope(source map[string]string, knowledgeBaseID, documentID string) bool {
	if knowledgeBaseID = strings.TrimSpace(knowledgeBaseID); knowledgeBaseID != "" && strings.TrimSpace(source["knowledgeBaseId"]) != knowledgeBaseID {
		return false
	}
	if documentID = strings.TrimSpace(documentID); documentID != "" && strings.TrimSpace(source["documentId"]) != documentID {
		return false
	}
	return true
}

func isEvidenceAbstention(answer string) bool {
	normalized := strings.ToLower(strings.TrimSpace(answer))
	if normalized == "" {
		return true
	}
	for _, phrase := range []string{
		"无法确认", "无法确定", "无法回答", "没有足够信息", "缺少足够信息", "未找到可靠证据", "未找到相关证据", "暂无可靠证据",
		"cannot determine", "cannot answer", "no reliable evidence",
	} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func isCitationSeparatorLine(line string) bool {
	line = strings.TrimSpace(strings.Trim(line, "|"))
	if line == "" {
		return true
	}
	for _, r := range line {
		if !strings.ContainsRune("-:·= ", r) {
			return false
		}
	}
	return true
}

func isCitationHeading(line string) bool {
	line = strings.TrimSpace(strings.TrimLeft(line, "#"))
	return len([]rune(line)) <= 24 && strings.HasSuffix(line, "：")
}

func countEvidenceTerms(terms []string, lowerText string) int {
	count := 0
	for _, term := range terms {
		if strings.Contains(lowerText, term) {
			count++
		}
	}
	return count
}

func removeEvidenceContainedTerms(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := make([]string, 0, len(values))
	for index, value := range values {
		contained := false
		for otherIndex, other := range values {
			if index == otherIndex || len([]rune(value)) > 2 || len([]rune(other)) <= len([]rune(value)) {
				continue
			}
			if strings.Contains(other, value) {
				contained = true
				break
			}
		}
		if !contained {
			result = append(result, value)
		}
	}
	return result
}

func cloneEvidenceSource(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source)+2)
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func parseEvidenceScore(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var score int
	for _, r := range value {
		if r >= '0' && r <= '9' {
			score = score*10 + int(r-'0')
		}
	}
	return minEvidenceInt(score, 10000)
}

func formatEvidenceInt(value int) string {
	if value < 0 {
		return "0"
	}
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}

func truncateEvidenceRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

func minEvidenceInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
