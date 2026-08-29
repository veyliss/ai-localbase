package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type evidenceTextIndex struct {
	text                 string
	normalized           string
	normalizedByteStarts []int
	sourceByteStarts     []int
	sourceByteEnds       []int
}

func newEvidenceTextIndex(text string) evidenceTextIndex {
	index := evidenceTextIndex{text: text}
	if text == "" {
		return index
	}

	builder := strings.Builder{}
	builder.Grow(len(text))
	for sourceByte := 0; sourceByte < len(text); {
		r, size := utf8.DecodeRuneInString(text[sourceByte:])
		if size <= 0 {
			break
		}
		if !unicode.IsSpace(r) {
			index.normalizedByteStarts = append(index.normalizedByteStarts, builder.Len())
			index.sourceByteStarts = append(index.sourceByteStarts, sourceByte)
			index.sourceByteEnds = append(index.sourceByteEnds, sourceByte+size)
			builder.WriteRune(r)
		}
		sourceByte += size
	}
	index.normalized = builder.String()
	return index
}

func normalizeEvidenceText(text string) string {
	if text == "" {
		return ""
	}
	builder := strings.Builder{}
	builder.Grow(len(text))
	for _, r := range text {
		if !unicode.IsSpace(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func (index evidenceTextIndex) normalizedByteFromSourceByte(sourceByte int) int {
	if sourceByte <= 0 || len(index.sourceByteStarts) == 0 {
		return 0
	}
	if sourceByte >= len(index.text) {
		return len(index.normalized)
	}
	position := sort.Search(len(index.sourceByteStarts), func(i int) bool {
		return index.sourceByteStarts[i] >= sourceByte
	})
	if position >= len(index.normalizedByteStarts) {
		return len(index.normalized)
	}
	return index.normalizedByteStarts[position]
}

func (index evidenceTextIndex) sourceRangeForNormalizedByte(normalizedByte int, fragmentLength int) (int, int, bool) {
	if normalizedByte < 0 || normalizedByte >= len(index.normalized) || fragmentLength <= 0 {
		return 0, 0, false
	}
	position := sort.Search(len(index.normalizedByteStarts), func(i int) bool {
		return index.normalizedByteStarts[i] >= normalizedByte
	})
	if position >= len(index.normalizedByteStarts) || index.normalizedByteStarts[position] != normalizedByte {
		return 0, 0, false
	}
	endByte := normalizedByte + fragmentLength
	endPosition := sort.Search(len(index.normalizedByteStarts), func(i int) bool {
		return index.normalizedByteStarts[i] >= endByte
	})
	if endPosition <= position || endPosition > len(index.sourceByteEnds) {
		return 0, 0, false
	}
	return index.sourceByteStarts[position], index.sourceByteEnds[endPosition-1], true
}

func findExactEvidenceMatch(text, fragment string, fromByte int) (int, int, bool) {
	// Overlap adds the tail of the previous chunk to the next one. If the
	// overlapped occurrence crosses fromByte, prefer the latest such repeat.
	searchFrom := fromByte - len(fragment) + 1
	if searchFrom < 0 {
		searchFrom = 0
	}
	overlapStart, overlapEnd := 0, 0
	for cursor := searchFrom; cursor < fromByte; {
		relative := strings.Index(text[cursor:], fragment)
		if relative < 0 {
			break
		}
		start := cursor + relative
		if start >= fromByte {
			break
		}
		end := start + len(fragment)
		if end > fromByte {
			overlapStart, overlapEnd = start, end
		}
		cursor = start + 1
	}
	if overlapEnd > overlapStart {
		return overlapStart, overlapEnd, true
	}

	if start := strings.Index(text[fromByte:], fragment); start >= 0 {
		start += fromByte
		return start, start + len(fragment), true
	}
	return 0, 0, false
}

func findNormalizedEvidenceMatch(source, fragment string, fromByte int) (int, bool) {
	if source == "" || fragment == "" {
		return 0, false
	}

	// Search only the small window that can overlap the previous match. This
	// keeps repeated chunks aligned without rescanning unrelated earlier text.
	searchFrom := fromByte - len(fragment) + 1
	if searchFrom < 0 {
		searchFrom = 0
	}
	overlapStart := -1
	for cursor := searchFrom; cursor < fromByte; {
		relative := strings.Index(source[cursor:], fragment)
		if relative < 0 {
			break
		}
		start := cursor + relative
		if start >= fromByte {
			break
		}
		if start+len(fragment) > fromByte {
			overlapStart = start
		}
		cursor = start + 1
	}
	if overlapStart >= 0 {
		return overlapStart, true
	}

	if start := strings.Index(source[fromByte:], fragment); start >= 0 {
		return start + fromByte, true
	}
	return 0, false
}

func (index evidenceTextIndex) locate(fragment string, fromByte int) (charStart, charEnd, lineStart, lineEnd, nextByte int) {
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return 0, 0, 0, 0, fromByte
	}
	if fromByte < 0 || fromByte > len(index.text) {
		fromByte = 0
	}

	byteStart, byteEnd, found := findExactEvidenceMatch(index.text, fragment, fromByte)
	if !found {
		normalizedFragment := normalizeEvidenceText(fragment)
		normalizedStart := findNormalizedEvidenceStart(index, fromByte)
		normalizedByte, normalizedFound := findNormalizedEvidenceMatch(index.normalized, normalizedFragment, normalizedStart)
		if !normalizedFound {
			return 0, 0, 0, 0, fromByte
		}
		byteStart, byteEnd, found = index.sourceRangeForNormalizedByte(normalizedByte, len(normalizedFragment))
		if !found {
			return 0, 0, 0, 0, fromByte
		}
	}

	charStart = utf8.RuneCountInString(index.text[:byteStart])
	charEnd = utf8.RuneCountInString(index.text[:byteEnd])
	lineStart = 1 + strings.Count(index.text[:byteStart], "\n")
	lineEnd = lineStart + strings.Count(index.text[byteStart:byteEnd], "\n")
	return charStart, charEnd, lineStart, lineEnd, maxInt(byteEnd, fromByte)
}

func findNormalizedEvidenceStart(index evidenceTextIndex, fromByte int) int {
	return index.normalizedByteFromSourceByte(fromByte)
}

// locateEvidenceRange returns rune-based offsets and line numbers for a chunk.
// The final value is a byte offset used only to find the next repeated chunk.
func locateEvidenceRange(documentText, fragment string, fromByte int) (charStart, charEnd, lineStart, lineEnd, nextByte int) {
	return newEvidenceTextIndex(documentText).locate(fragment, fromByte)
}

func evidenceIDForChunk(chunk DocumentChunk) string {
	if strings.TrimSpace(chunk.EvidenceID) != "" {
		return strings.TrimSpace(chunk.EvidenceID)
	}
	seed := strings.Join([]string{
		chunk.KnowledgeBaseID,
		chunk.DocumentID,
		chunk.ID,
		chunk.Kind,
		strconv.Itoa(chunk.Index),
		strconv.Itoa(chunk.CharStart),
		strconv.Itoa(chunk.CharEnd),
	}, "\x00")
	digest := sha256.Sum256([]byte(seed))
	return "ev-" + hex.EncodeToString(digest[:12])
}

func evidenceMetadata(chunk RetrievedChunk) map[string]string {
	metadata := map[string]string{
		"evidenceId": evidenceIDForChunk(chunk.DocumentChunk),
	}
	if chunk.CharEnd > chunk.CharStart {
		metadata["charStart"] = strconv.Itoa(chunk.CharStart)
		metadata["charEnd"] = strconv.Itoa(chunk.CharEnd)
	}
	if chunk.LineStart > 0 {
		metadata["lineStart"] = strconv.Itoa(chunk.LineStart)
	}
	if chunk.LineEnd > 0 {
		metadata["lineEnd"] = strconv.Itoa(chunk.LineEnd)
	}
	if chunk.TableRow > 0 {
		metadata["tableRow"] = strconv.Itoa(chunk.TableRow)
	}
	if len(chunk.TableColumns) > 0 {
		metadata["tableColumns"] = strings.Join(chunk.TableColumns, ",")
	}
	return metadata
}

func evidenceDebugFields(chunk RetrievedChunk) (string, int, int, int, int, int, []string) {
	return evidenceIDForChunk(chunk.DocumentChunk), chunk.CharStart, chunk.CharEnd, chunk.LineStart, chunk.LineEnd, chunk.TableRow, append([]string(nil), chunk.TableColumns...)
}

func formatEvidenceLocation(chunk RetrievedChunk) string {
	parts := make([]string, 0, 2)
	if chunk.LineStart > 0 {
		parts = append(parts, fmt.Sprintf("行 %d-%d", chunk.LineStart, maxInt(chunk.LineEnd, chunk.LineStart)))
	} else if chunk.CharEnd > chunk.CharStart {
		parts = append(parts, fmt.Sprintf("字符 %d-%d", chunk.CharStart, chunk.CharEnd))
	}
	if chunk.TableRow > 0 {
		parts = append(parts, fmt.Sprintf("表格第 %d 行", chunk.TableRow))
	}
	return strings.Join(parts, "，")
}
