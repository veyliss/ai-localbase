package offline

import "testing"

func TestEvaluateFaithfulnessRequiresMatchingLiterals(t *testing.T) {
	evidence := []RetrievedChunkInfo{{Text: "示例机构成立于1898年，位于示例城市。"}}

	supported := EvaluateFaithfulness("示例机构成立于1898年。", evidence)
	if !supported.Evaluated || supported.ClaimCount != 1 || supported.SupportedClaimCount != 1 || supported.UnsupportedClaimCount != 0 {
		t.Fatalf("expected supported claim, got %#v", supported)
	}

	unsupported := EvaluateFaithfulness("示例机构成立于1900年。", evidence)
	if unsupported.SupportedClaimCount != 0 || unsupported.UnsupportedClaimCount != 1 || unsupported.Score != 0 {
		t.Fatalf("expected changed year to be unsupported, got %#v", unsupported)
	}
}

func TestEvaluateFaithfulnessCountsMixedClaimsAndIgnoresRefusal(t *testing.T) {
	evidence := []RetrievedChunkInfo{{Text: "成员甲是负责人。机构位于示例城市。"}}
	result := EvaluateFaithfulness("成员甲是负责人。机构位于示例城市。机构成立于1900年。", evidence)
	if result.ClaimCount != 3 || result.SupportedClaimCount != 2 || result.UnsupportedClaimCount != 1 {
		t.Fatalf("unexpected mixed claim analysis: %#v", result)
	}

	refusal := EvaluateFaithfulness("无法确认该问题，暂无相关可靠证据。", nil)
	if refusal.Evaluated || refusal.ClaimCount != 0 {
		t.Fatalf("expected refusal to be excluded from faithfulness denominator, got %#v", refusal)
	}
}

func TestClassifyFailureDetectsUnsupportedAnswerAfterRetrievalHit(t *testing.T) {
	gt := GroundTruthCase{SourceDocuments: []SourceDocument{{DocumentID: "doc-1", ChunkID: "chunk-1"}}}
	result := CaseResult{
		RetrievedChunks: []RetrievedChunkInfo{{DocumentID: "doc-1", ChunkID: "chunk-1", Text: "成员甲是负责人。"}},
		LLMAnswer:       "成员甲是负责人。成员乙是负责人。",
	}
	ApplyFaithfulness(&result)
	classification := ClassifyFailure(result, gt, 0.5)
	if classification.Category != FailureCategoryUnsupportedAnswer {
		t.Fatalf("expected unsupported answer category, got %#v", classification)
	}
}
