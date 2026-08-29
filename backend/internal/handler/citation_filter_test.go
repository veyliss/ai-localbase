package handler

import "testing"

func TestCalibrateCitationSourcesFiltersUnrelatedContextCandidates(t *testing.T) {
	sources := []map[string]string{
		{
			"knowledgeBaseId": "kb-1",
			"documentId":      "doc-unrelated",
			"documentName":    "deploy.md",
			"chunkId":         "chunk-unrelated",
			"score":           "0.9400",
			"snippet":         "这是一段关于部署参数、缓存策略和服务端口的文档。",
		},
		{
			"knowledgeBaseId": "kb-1",
			"documentId":      "doc-related",
			"documentName":    "school.md",
			"chunkId":         "chunk-related",
			"score":           "0.8200",
			"snippet":         "示例机构负责人为成员甲，组织结构稳定。",
		},
	}

	filtered := calibrateCitationSources("示例机构负责人是谁", "示例机构负责人是成员甲。", sources, "kb-1", "")
	if len(filtered) != 1 {
		t.Fatalf("expected one calibrated source, got %#v", filtered)
	}
	if filtered[0]["documentId"] != "doc-related" {
		t.Fatalf("expected related citation source, got %#v", filtered[0])
	}
	if filtered[0]["citationConfidence"] != "" {
		t.Fatalf("expected no synthetic confidence marker, got %#v", filtered[0])
	}
}

func TestCalibrateCitationSourcesDropsSourcesWhenAnswerHasNoEvidenceOverlap(t *testing.T) {
	sources := []map[string]string{{
		"knowledgeBaseId": "kb-1",
		"documentId":      "doc-unrelated",
		"documentName":    "deploy.md",
		"chunkId":         "chunk-unrelated",
		"score":           "0.9800",
		"snippet":         "系统部署参数、缓存策略和服务端口。",
	}}

	filtered := calibrateCitationSources("示例机构负责人是谁", "未找到可靠证据说明示例机构负责人是谁。", sources, "kb-1", "")
	if len(filtered) != 0 {
		t.Fatalf("expected no calibrated sources, got %#v", filtered)
	}
}

func TestCalibrateCitationSourcesDropsNonDocumentAndIncompleteSources(t *testing.T) {
	sources := []map[string]string{
		{
			"toolName": "search_knowledge_base",
			"status":   "ok",
		},
		{
			"knowledgeBaseId": "kb-1",
			"documentId":      "doc-table",
			"documentName":    "records.csv",
			"chunkId":         "chunk-table",
		},
	}

	filtered := calibrateCitationSources("谁的薪资最高", "成员甲的薪资最高，为 300。", sources, "kb-1", "doc-table")
	if len(filtered) != 0 {
		t.Fatalf("expected non-document and incomplete sources to be dropped, got %#v", filtered)
	}
}

func TestCalibrateCitationSourcesDropsWrongScopeAndGenericOverlap(t *testing.T) {
	sources := []map[string]string{
		{
			"knowledgeBaseId": "kb-other",
			"documentId":      "doc-novel",
			"documentName":    "作品大纲.md",
			"chunkId":         "chunk-novel",
			"score":           "0.9900",
			"snippet":         "林译是破晓小队的核心成员。",
		},
		{
			"knowledgeBaseId": "kb-school",
			"documentId":      "doc-school",
			"documentName":    "机构简介.pdf",
			"chunkId":         "chunk-school",
			"score":           "0.2660",
			"snippet":         "示例机构与多个地区的机构建立了合作关系。",
		},
	}

	answer := "《示例故事》的主角成员乙是示例小队的领袖，拥有读取心理意图的能力，并在危机中成长。"
	filtered := calibrateCitationSources("详细介绍", answer, sources, "kb-school", "")
	if len(filtered) != 0 {
		t.Fatalf("expected no citation for an answer unsupported by the selected knowledge base, got %#v", filtered)
	}
}

func TestCalibrateCitationSourcesKeepsShortChineseEntityEvidence(t *testing.T) {
	sources := []map[string]string{{
		"knowledgeBaseId": "kb-school",
		"documentId":      "doc-school",
		"documentName":    "学校简介.pdf",
		"chunkId":         "chunk-school",
		"score":           "0.8200",
		"snippet":         "机构现任负责人为成员甲，负责日常管理工作。",
	}}

	filtered := calibrateCitationSources("负责人是谁", "负责人是成员甲。", sources, "kb-school", "")
	if len(filtered) != 1 || filtered[0]["documentId"] != "doc-school" {
		t.Fatalf("expected short Chinese entity evidence to remain citable, got %#v", filtered)
	}
}

func TestCalibrateCitationSourcesDoesNotTreatDocumentMetadataAsEvidence(t *testing.T) {
	sources := []map[string]string{{
		"knowledgeBaseId": "kb-school",
		"documentId":      "doc-school",
		"documentName":    "成员甲负责人资料.md",
		"chunkId":         "chunk-school",
		"score":           "0.9900",
		"snippet":         "该文档只介绍资料归档流程，不包含人员信息。",
	}}

	filtered := calibrateCitationSources("负责人是谁", "负责人是成员甲。", sources, "kb-school", "")
	if len(filtered) != 0 {
		t.Fatalf("expected metadata-only overlap to be rejected, got %#v", filtered)
	}
}

func TestCalibrateCitationSourcesRejectsConflictingAnswerValue(t *testing.T) {
	sources := []map[string]string{{
		"knowledgeBaseId": "kb-school",
		"documentId":      "doc-school",
		"documentName":    "学校简介.md",
		"chunkId":         "chunk-school",
		"score":           "0.9900",
		"snippet":         "示例机构现任负责人为成员乙，负责日常管理工作。",
	}}

	filtered := calibrateCitationSources("示例机构负责人是谁", "示例机构负责人是成员甲。", sources, "kb-school", "")
	if len(filtered) != 0 {
		t.Fatalf("expected conflicting answer value to be rejected, got %#v", filtered)
	}
}

func TestCalibrateCitationSourcesNeverCitesAbstention(t *testing.T) {
	sources := []map[string]string{{
		"knowledgeBaseId": "kb-school",
		"documentId":      "doc-school",
		"documentName":    "学校简介.md",
		"chunkId":         "chunk-school",
		"score":           "0.9900",
		"snippet":         "示例机构现任负责人为成员甲。",
	}}

	filtered := calibrateCitationSources("负责人是谁", "无法确认该问题，暂无可靠证据。", sources, "kb-school", "")
	if len(filtered) != 0 {
		t.Fatalf("expected abstention answer to have no citations, got %#v", filtered)
	}
}
