package service

import "testing"

func TestAssessCitationSupportRequiresAnswerSpecificEvidence(t *testing.T) {
	sources := []map[string]string{
		{
			"knowledgeBaseId": "kb-1",
			"documentId":      "doc-unrelated",
			"documentName":    "部署说明.md",
			"chunkId":         "chunk-unrelated",
			"snippet":         "系统部署参数、缓存策略和服务端口。",
		},
		{
			"knowledgeBaseId": "kb-1",
			"documentId":      "doc-related",
			"documentName":    "学校简介.md",
			"chunkId":         "chunk-related",
			"evidenceId":      "ev-related",
			"snippet":         "示例学校成立于 1893 年，现有 12 个校区。",
		},
	}

	report := AssessCitationSupport(
		"示例学校的成立时间是什么？",
		"示例学校成立于 1893 年。",
		sources,
		"kb-1",
		"",
	)
	if report.Status != "supported" || report.Coverage != 1 {
		t.Fatalf("expected fully supported answer, got %+v", report)
	}
	if len(report.SupportedSources) != 1 || report.SupportedSources[0]["documentId"] != "doc-related" {
		t.Fatalf("expected only related evidence source, got %#v", report.SupportedSources)
	}
}

func TestAssessCitationSupportDetectsPartialClaimsAndNumericConflicts(t *testing.T) {
	sources := []map[string]string{{
		"knowledgeBaseId": "kb-1",
		"documentId":      "doc-1",
		"documentName":    "学校简介.md",
		"chunkId":         "chunk-1",
		"evidenceId":      "ev-1",
		"snippet":         "示例学校成立于 1893 年。",
	}}

	report := AssessCitationSupport(
		"示例学校的成立时间和校长是谁？",
		"示例学校成立于 1893 年。校长是成员甲。",
		sources,
		"kb-1",
		"",
	)
	if report.Status != "partial" || report.ClaimCount != 2 || report.SupportedClaimCount != 1 {
		t.Fatalf("expected one of two claims to be supported, got %+v", report)
	}

	conflict := AssessCitationSupport(
		"示例学校的成立时间是什么？",
		"示例学校成立于 1994 年。",
		sources,
		"kb-1",
		"",
	)
	if conflict.Status != "unsupported" || len(conflict.SupportedSources) != 0 {
		t.Fatalf("expected conflicting numeric claim to be rejected, got %+v", conflict)
	}
}

func TestAssessCitationSupportMarksAbstentionAndScopesSources(t *testing.T) {
	sources := []map[string]string{{
		"knowledgeBaseId": "kb-other",
		"documentId":      "doc-other",
		"documentName":    "其他文档.md",
		"chunkId":         "chunk-other",
		"snippet":         "负责人是成员甲。",
	}}

	report := AssessCitationSupport("负责人是谁？", "无法确认该问题，暂无可靠证据。", sources, "kb-1", "")
	if report.Status != "abstained" || report.ClaimCount != 0 || len(report.SupportedSources) != 0 {
		t.Fatalf("expected abstention without citations, got %+v", report)
	}

	report = AssessCitationSupport("负责人是谁？", "负责人是成员甲。", sources, "kb-1", "")
	if report.Status != "unsupported" || len(report.SupportedSources) != 0 {
		t.Fatalf("expected out-of-scope source to be rejected, got %+v", report)
	}
}
