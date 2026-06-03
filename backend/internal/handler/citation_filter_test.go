package handler

import "testing"

func TestCalibrateCitationSourcesFiltersUnrelatedContextCandidates(t *testing.T) {
	sources := []map[string]string{
		{
			"documentId":   "doc-unrelated",
			"documentName": "deploy.md",
			"chunkId":      "chunk-unrelated",
			"score":        "0.9400",
			"snippet":      "这是一段关于部署参数、缓存策略和服务端口的文档。",
		},
		{
			"documentId":   "doc-related",
			"documentName": "school.md",
			"chunkId":      "chunk-related",
			"score":        "0.8200",
			"snippet":      "武汉大学校长为张三，学校治理结构稳定。",
		},
	}

	filtered := calibrateCitationSources("武汉大学校长是谁", "武汉大学校长是张三。", sources)
	if len(filtered) != 1 {
		t.Fatalf("expected one calibrated source, got %#v", filtered)
	}
	if filtered[0]["documentId"] != "doc-related" {
		t.Fatalf("expected related citation source, got %#v", filtered[0])
	}
	if filtered[0]["citationConfidence"] == "" {
		t.Fatalf("expected citation confidence marker, got %#v", filtered[0])
	}
}

func TestCalibrateCitationSourcesDropsSourcesWhenAnswerHasNoEvidenceOverlap(t *testing.T) {
	sources := []map[string]string{{
		"documentId":   "doc-unrelated",
		"documentName": "deploy.md",
		"chunkId":      "chunk-unrelated",
		"score":        "0.9800",
		"snippet":      "系统部署参数、缓存策略和服务端口。",
	}}

	filtered := calibrateCitationSources("武汉大学校长是谁", "未找到可靠证据说明武汉大学校长是谁。", sources)
	if len(filtered) != 0 {
		t.Fatalf("expected no calibrated sources, got %#v", filtered)
	}
}

func TestCalibrateCitationSourcesKeepsStructuredDataSources(t *testing.T) {
	sources := []map[string]string{{
		"sourceType":   "structured-data",
		"documentId":   "doc-table",
		"documentName": "teachers.csv",
	}}

	filtered := calibrateCitationSources("谁的薪资最高", "张三的薪资最高，为 24000。", sources)
	if len(filtered) != 1 {
		t.Fatalf("expected structured data source to be kept, got %#v", filtered)
	}
	if filtered[0]["citationConfidence"] != "high" {
		t.Fatalf("expected high confidence structured source, got %#v", filtered[0])
	}
}
