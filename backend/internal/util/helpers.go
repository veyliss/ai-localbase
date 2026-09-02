package util

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

var idCounter atomic.Uint64

var ErrUnsafeFilename = errors.New("unsafe file name")

func NextID(prefix string) string {
	var randomBytes [12]byte
	if _, err := rand.Read(randomBytes[:]); err == nil {
		return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(randomBytes[:]))
	}
	id := idCounter.Add(1)
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), id)
}

func NextRequestID() string {
	return NextID("req")
}

func NowUnixNano() int64 {
	return time.Now().UnixNano()
}

func NowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func FormatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}

	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}

	return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
}

func ExtractContentPreview(path string) string {
	content, err := ExtractDocumentText(path)
	if err != nil {
		return "暂未生成摘要"
	}

	return BuildContentPreviewFromText(content)
}

func SanitizeFilename(name string) string {
	name = safeFilenameBase(name)
	name = strings.ReplaceAll(name, " ", "_")
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, name)
}

// NormalizeFilename keeps the user-visible name to one safe basename. Relative
// directory components are tolerated for compatibility with multipart clients,
// but absolute paths, traversal components, and control characters are rejected.
func NormalizeFilename(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("file name is required")
	}
	for _, character := range trimmed {
		if unicode.IsControl(character) {
			return "", fmt.Errorf("%w: control characters are not allowed", ErrUnsafeFilename)
		}
	}

	portable := strings.ReplaceAll(trimmed, `\`, "/")
	if strings.HasPrefix(portable, "/") || isWindowsAbsolutePath(portable) {
		return "", fmt.Errorf("%w: absolute paths are not allowed", ErrUnsafeFilename)
	}
	parts := strings.Split(portable, "/")
	for _, part := range parts {
		if part == "." || part == ".." {
			return "", fmt.Errorf("%w: path traversal is not allowed", ErrUnsafeFilename)
		}
	}
	base := strings.TrimSpace(parts[len(parts)-1])
	if base == "" || base == "." || base == ".." {
		return "", fmt.Errorf("%w: file name is empty", ErrUnsafeFilename)
	}
	return base, nil
}

func safeFilenameBase(name string) string {
	portable := strings.ReplaceAll(strings.TrimSpace(name), `\`, "/")
	if index := strings.LastIndex(portable, "/"); index >= 0 {
		portable = portable[index+1:]
	}
	return portable
}

func isWindowsAbsolutePath(path string) bool {
	if len(path) < 3 {
		return false
	}
	letter := path[0]
	return ((letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z')) && path[1] == ':' && path[2] == '/'
}
