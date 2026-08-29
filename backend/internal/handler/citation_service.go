package handler

import "ai-localbase/internal/service"

// calibrateCitationSources is kept as a small handler-facing compatibility
// wrapper. The actual claim-level assessment lives in service so chat, eval
// and MCP callers can share the same evidence contract.
func calibrateCitationSources(question, answer string, sources []map[string]string, knowledgeBaseID, documentID string) []map[string]string {
	report := service.AssessCitationSupport(question, answer, sources, knowledgeBaseID, documentID)
	return report.SupportedSources
}
