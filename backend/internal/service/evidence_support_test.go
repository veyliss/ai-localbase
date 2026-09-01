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

func TestAssessCitationSupportReturnsMinimalClaimEvidence(t *testing.T) {
	sources := []map[string]string{{
		"knowledgeBaseId": "kb-1",
		"documentId":      "doc-school",
		"documentName":    "学校简介.md",
		"chunkId":         "chunk-school",
		"evidenceId":      "ev-school",
		"snippet":         "文档说明：该文件还包含校园交通和招生流程。\n示例学校成立于 1893 年。\n附录介绍其他学校的历史。",
	}}

	report := AssessCitationSupport(
		"示例学校的成立时间是什么？",
		"示例学校成立于 1893 年。",
		sources,
		"kb-1",
		"",
	)
	if report.Status != "supported" || len(report.SupportedSources) != 1 {
		t.Fatalf("expected supported claim with one source, got %+v", report)
	}

	excerpt := report.SupportedSources[0]["citationSnippet"]
	if excerpt != "示例学校成立于 1893 年。" {
		t.Fatalf("expected minimal citation snippet, got %q", excerpt)
	}
	if len(report.Claims) != 1 || len(report.Claims[0].EvidenceSnippets) != 1 || report.Claims[0].EvidenceSnippets[0] != excerpt {
		t.Fatalf("expected claim-level evidence snippet, got %+v", report.Claims)
	}
}

func TestAssessCitationSupportSelectsTableRowsAndCrossSentenceEvidence(t *testing.T) {
	sources := []map[string]string{
		{
			"knowledgeBaseId": "kb-1",
			"documentId":      "doc-table",
			"documentName":    "教师信息.csv",
			"chunkId":         "chunk-table",
			"evidenceId":      "ev-table",
			"snippet":         "|姓名|职称|薪资|\n|---|---|---|\n|张三|高级职称|24000|\n|李四|中级职称|18000|",
		},
		{
			"knowledgeBaseId": "kb-1",
			"documentId":      "doc-text",
			"documentName":    "学校简介.md",
			"chunkId":         "chunk-text",
			"evidenceId":      "ev-text",
			"snippet":         "学校位于示例城市。\n学校创建于 1893 年。",
		},
	}

	tableReport := AssessCitationSupport(
		"谁的薪资最高？",
		"张三的薪资最高，为 24000。",
		sources,
		"kb-1",
		"doc-table",
	)
	if tableReport.Status != "supported" || len(tableReport.SupportedSources) != 1 {
		t.Fatalf("expected table claim to be supported by one source, got %+v", tableReport)
	}
	if got := tableReport.SupportedSources[0]["citationSnippet"]; got != "|张三|高级职称|24000|" {
		t.Fatalf("expected table row citation, got %q", got)
	}

	textReport := AssessCitationSupport(
		"学校的创建时间和所在城市是什么？",
		"学校创建于 1893 年，位于示例城市。",
		sources,
		"kb-1",
		"doc-text",
	)
	if textReport.Status != "supported" || len(textReport.Claims) != 1 {
		t.Fatalf("expected cross-sentence claim to be supported, got %+v", textReport)
	}
	if got := textReport.SupportedSources[0]["citationSnippet"]; got != "学校位于示例城市。\n学校创建于 1893 年。" {
		t.Fatalf("expected both supporting sentences in source order, got %q", got)
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
