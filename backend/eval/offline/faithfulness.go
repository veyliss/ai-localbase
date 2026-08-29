package offline

import (
	"strings"
	"unicode"
)

// FaithfulnessResult describes whether generated answer claims can be found in
// the retrieved evidence. It is deliberately lexical and deterministic so it
// can run in offline CI without another model or an evaluation data service.
type FaithfulnessResult struct {
	Score                 float64
	ClaimCount            int
	SupportedClaimCount   int
	UnsupportedClaimCount int
	Evaluated             bool
}

const (
	faithfulnessNumericThreshold = 0.55
	faithfulnessTextThreshold    = 0.78
)

// EvaluateFaithfulness performs a conservative, deterministic evidence check.
// A claim is supported when its normalized text is present in one chunk, or
// when its distinctive lexical overlap is high enough and all numeric/ASCII
// literals are preserved. This is a baseline signal, not a replacement for a
// human or model-assisted quality review.
func EvaluateFaithfulness(answer string, chunks []RetrievedChunkInfo) FaithfulnessResult {
	claims := extractAnswerClaims(answer)
	if len(claims) == 0 {
		return FaithfulnessResult{}
	}

	result := FaithfulnessResult{
		ClaimCount: len(claims),
		Evaluated:  true,
	}
	for _, claim := range claims {
		if claimSupportedByChunks(claim, chunks) {
			result.SupportedClaimCount++
			continue
		}
		result.UnsupportedClaimCount++
	}
	result.Score = float64(result.SupportedClaimCount) / float64(result.ClaimCount)
	return result
}

// AnalyzeCaseFaithfulness evaluates a CaseResult. Keeping this helper in the
// offline package lets aggregation and reports use the same calculation even
// when callers construct CaseResult values directly in tests.
func AnalyzeCaseFaithfulness(result CaseResult) FaithfulnessResult {
	if result.FaithfulnessEvaluated || result.AnswerClaimCount > 0 || result.UnsupportedClaimCount > 0 {
		return FaithfulnessResult{
			Score:                 result.FaithfulnessScore,
			ClaimCount:            result.AnswerClaimCount,
			SupportedClaimCount:   result.SupportedClaimCount,
			UnsupportedClaimCount: result.UnsupportedClaimCount,
			Evaluated:             result.FaithfulnessEvaluated,
		}
	}
	return EvaluateFaithfulness(result.LLMAnswer, result.RetrievedChunks)
}

// ApplyFaithfulness stores the deterministic analysis on a case result before
// failure classification and report generation.
func ApplyFaithfulness(result *CaseResult) {
	if result == nil {
		return
	}
	analysis := EvaluateFaithfulness(result.LLMAnswer, result.RetrievedChunks)
	result.FaithfulnessScore = analysis.Score
	result.AnswerClaimCount = analysis.ClaimCount
	result.SupportedClaimCount = analysis.SupportedClaimCount
	result.UnsupportedClaimCount = analysis.UnsupportedClaimCount
	result.FaithfulnessEvaluated = analysis.Evaluated
}

func extractAnswerClaims(answer string) []string {
	claims := make([]string, 0)
	for _, rawLine := range strings.Split(strings.ReplaceAll(answer, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "```") || strings.HasPrefix(line, "#") {
			continue
		}
		line = stripListPrefix(line)
		line = stripMarkdownDecorators(line)
		if isMarkdownDivider(line) {
			continue
		}

		parts := strings.FieldsFunc(line, func(r rune) bool {
			switch r {
			case '。', '！', '？', '!', '?', ';', '；':
				return true
			default:
				return false
			}
		})
		for _, part := range parts {
			claim := cleanClaim(part)
			if claim == "" || isNonFactualClaim(claim) {
				continue
			}
			claims = append(claims, claim)
		}
	}
	return claims
}

func stripListPrefix(value string) string {
	runes := []rune(strings.TrimSpace(value))
	for len(runes) > 0 {
		switch runes[0] {
		case '-', '*', '+', '>', '•':
			runes = []rune(strings.TrimSpace(string(runes[1:])))
		default:
			return string(runes)
		}
	}
	return string(runes)
}

func stripMarkdownDecorators(value string) string {
	value = strings.ReplaceAll(value, "**", "")
	value = strings.ReplaceAll(value, "__", "")
	value = strings.ReplaceAll(value, "`", "")
	value = strings.ReplaceAll(value, "|", " ")
	value = removeBracketedText(value, '[', ']')
	value = removeBracketedText(value, '【', '】')
	return strings.TrimSpace(value)
}

func removeBracketedText(value string, opening, closing rune) string {
	runes := []rune(value)
	result := make([]rune, 0, len(runes))
	inside := false
	for _, r := range runes {
		switch r {
		case opening:
			inside = true
		case closing:
			inside = false
		default:
			if !inside {
				result = append(result, r)
			}
		}
	}
	return string(result)
}

func cleanClaim(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimLeft(value, "0123456789.)、 \t")
	value = strings.TrimSpace(value)
	for _, prefix := range []string{
		"根据现有资料",
		"根据检索结果",
		"根据资料",
		"基于检索上下文",
		"基于现有信息",
	} {
		if strings.HasPrefix(value, prefix) {
			value = strings.TrimSpace(strings.TrimLeft(value[len(prefix):], "：:,， \t"))
			break
		}
	}
	return strings.TrimSpace(value)
}

func isMarkdownDivider(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	for _, r := range value {
		if r != '-' && r != ':' && r != '|' && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func isNonFactualClaim(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, phrase := range []string{
		"无法确定",
		"无法确认",
		"无法回答",
		"没有足够信息",
		"缺少足够信息",
		"未找到相关",
		"未找到可用证据",
		"暂无相关",
		"不清楚",
		"无法基于当前资料回答",
		"不能确认",
		"cannot determine",
		"cannot answer",
		"not enough information",
		"no reliable evidence",
	} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}

func claimSupportedByChunks(claim string, chunks []RetrievedChunkInfo) bool {
	for _, chunk := range chunks {
		if claimSupportScore(claim, chunk.Text) >= claimSupportThreshold(claim) {
			return true
		}
	}
	return false
}

func claimSupportThreshold(claim string) float64 {
	if len(extractLiteralTokens(claim)) > 0 {
		return faithfulnessNumericThreshold
	}
	return faithfulnessTextThreshold
}

func claimSupportScore(claim, evidence string) float64 {
	normalizedClaim := normalizeEvalText(claim)
	normalizedEvidence := normalizeEvalText(evidence)
	if normalizedClaim == "" || normalizedEvidence == "" {
		return 0
	}
	if strings.Contains(normalizedEvidence, normalizedClaim) {
		return 1
	}

	for _, literal := range extractLiteralTokens(claim) {
		if !strings.Contains(normalizedEvidence, normalizeEvalText(literal)) {
			return 0
		}
	}
	return snippetMatchScore(normalizedEvidence, normalizedClaim)
}

func extractLiteralTokens(value string) []string {
	value = normalizeEvalText(value)
	if value == "" {
		return nil
	}
	runes := []rune(value)
	tokens := make([]string, 0)
	for i := 0; i < len(runes); {
		if !isASCIILiteralRune(runes[i]) {
			i++
			continue
		}
		start := i
		for i < len(runes) && isASCIILiteralRune(runes[i]) {
			i++
		}
		if token := string(runes[start:i]); len([]rune(token)) >= 2 {
			tokens = appendUniqueString(tokens, token)
		}
	}
	return tokens
}

func isASCIILiteralRune(value rune) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
