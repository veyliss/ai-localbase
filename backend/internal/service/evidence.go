package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// locateEvidenceRange returns rune-based offsets and line numbers for a chunk.
// The final value is a byte offset used only to find the next repeated chunk.
func locateEvidenceRange(documentText, fragment string, fromByte int) (charStart, charEnd, lineStart, lineEnd, nextByte int) {
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return 0, 0, 0, 0, fromByte
	}
	if fromByte < 0 || fromByte > len(documentText) {
		fromByte = 0
	}

	byteStart := strings.Index(documentText[fromByte:], fragment)
	if byteStart < 0 {
		byteStart = strings.Index(documentText, fragment)
		if byteStart < 0 {
			return 0, 0, 0, 0, fromByte
		}
	} else {
		byteStart += fromByte
	}
	byteEnd := byteStart + len(fragment)
	charStart = len([]rune(documentText[:byteStart]))
	charEnd = len([]rune(documentText[:byteEnd]))
	lineStart = 1 + strings.Count(documentText[:byteStart], "\n")
	lineEnd = lineStart + strings.Count(documentText[byteStart:byteEnd], "\n")
	return charStart, charEnd, lineStart, lineEnd, byteStart + maxInt(len(fragment), 1)
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
