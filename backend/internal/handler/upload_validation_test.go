package handler

import (
	"errors"
	"mime/multipart"
	"testing"

	"ai-localbase/internal/model"
)

func TestValidateUploadFileRejectsOversizedFile(t *testing.T) {
	file := &multipart.FileHeader{
		Filename: "large.md",
		Size:     1025,
	}

	err := validateUploadFile(file, model.AppConfig{}, 1024)
	if err == nil {
		t.Fatal("expected oversized upload to be rejected")
	}

	var sizeErr *uploadSizeError
	if !errors.As(err, &sizeErr) {
		t.Fatalf("expected uploadSizeError, got %T: %v", err, err)
	}
}

func TestValidateUploadFileAllowsFileWithinLimit(t *testing.T) {
	file := &multipart.FileHeader{
		Filename: "small.md",
		Size:     1024,
	}

	if err := validateUploadFile(file, model.AppConfig{}, 1024); err != nil {
		t.Fatalf("expected upload within limit, got %v", err)
	}
}
