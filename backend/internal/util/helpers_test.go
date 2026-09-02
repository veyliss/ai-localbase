package util

import (
	"errors"
	"strings"
	"testing"
)

func TestNextIDUsesUniqueProcessIndependentValues(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := NextID("doc")
		if !strings.HasPrefix(id, "doc-") {
			t.Fatalf("expected doc prefix, got %q", id)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("generated duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNormalizeFilenameRejectsPathsAndPreservesBasename(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      string
		wantError bool
	}{
		{name: "plain name", input: "guide.md", want: "guide.md"},
		{name: "relative directory", input: "documents/guide.md", want: "guide.md"},
		{name: "windows relative directory", input: `documents\\guide.md`, want: "guide.md"},
		{name: "unix absolute path", input: "/tmp/guide.md", wantError: true},
		{name: "windows absolute path", input: `C:\\Users\\test\\guide.md`, wantError: true},
		{name: "windows unc path", input: `\\\\server\\share\\guide.md`, wantError: true},
		{name: "parent traversal", input: "../guide.md", wantError: true},
		{name: "nested traversal", input: "documents/../guide.md", wantError: true},
		{name: "control character", input: "guide\x00.md", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeFilename(tt.input)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected unsafe filename to fail, got %q", got)
				}
				if !errors.Is(err, ErrUnsafeFilename) && !strings.Contains(err.Error(), "required") {
					t.Fatalf("expected unsafe filename error, got %v", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("NormalizeFilename(%q) = %q, %v; want %q", tt.input, got, err, tt.want)
			}
		})
	}
}
