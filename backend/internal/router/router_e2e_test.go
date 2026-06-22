package router

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"ai-localbase/internal/auth"
	"ai-localbase/internal/handler"
	"ai-localbase/internal/mcp"
	"ai-localbase/internal/model"
	"ai-localbase/internal/service"
)

type qdrantCollectionState struct {
	points []service.QdrantPoint
}

type qdrantTestServer struct {
	mu          sync.Mutex
	collections map[string]*qdrantCollectionState
}

type embeddingTestResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

type chatTestResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func TestRouterConfigEndpoints(t *testing.T) {
	engine, _, cleanup := newTestRouter(t)
	defer cleanup()

	updatePayload := map[string]any{
		"chat": map[string]any{
			"provider":    "ollama",
			"baseUrl":     "http://chat.example.invalid/v1",
			"model":       "chat-model-a",
			"apiKey":      "",
			"temperature": 0.4,
		},
		"embedding": map[string]any{
			"provider": "openai-compatible",
			"baseUrl":  "http://embed.example.invalid/v1",
			"model":    "embed-model-a",
			"apiKey":   "test-embed-key",
		},
	}

	resp := performJSONRequest(t, engine, http.MethodPut, "/api/config", updatePayload)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	var updated model.AppConfig
	decodeJSONResponse(t, resp.Body.Bytes(), &updated)
	if updated.Chat.BaseURL != "http://chat.example.invalid/v1" {
		t.Fatalf("expected chat baseUrl to be updated, got %s", updated.Chat.BaseURL)
	}
	if updated.Embedding.Model != "embed-model-a" {
		t.Fatalf("expected embedding model to be updated, got %s", updated.Embedding.Model)
	}

	resp = performRequest(t, engine, http.MethodGet, "/api/config", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	var fetched model.AppConfig
	decodeJSONResponse(t, resp.Body.Bytes(), &fetched)
	if fetched.Chat.Temperature != 0.4 {
		t.Fatalf("expected persisted chat temperature 0.4, got %v", fetched.Chat.Temperature)
	}
	if fetched.Embedding.APIKey != "test-embed-key" {
		t.Fatalf("expected persisted embedding apiKey, got %s", fetched.Embedding.APIKey)
	}
}

func TestMCPRequiresAuthorizationHeader(t *testing.T) {
	engine, _, _, cleanup := newAuthenticatedTestRouter(t)
	defer cleanup()

	resp := performRequest(t, engine, http.MethodGet, "/mcp", nil, "")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d, body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "missing authorization header") {
		t.Fatalf("expected missing authorization error, got %s", resp.Body.String())
	}
}

func TestMCPDisabledReturnsExplicitStatus(t *testing.T) {
	engine, _, cleanup := newTestRouterWithServerConfig(t, func(serverConfig *model.ServerConfig) {
		serverConfig.EnableMCP = false
	})
	defer cleanup()

	resp := performRequest(t, engine, http.MethodGet, "/mcp", nil, "")
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d, body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "mcp is disabled") {
		t.Fatalf("expected disabled mcp error, got %s", resp.Body.String())
	}
}

func TestMCPRejectsWhenAuthDisabled(t *testing.T) {
	engine, _, cleanup := newTestRouterWithServerConfig(t, func(serverConfig *model.ServerConfig) {
		serverConfig.EnableMCP = true
		serverConfig.EnableAuth = false
	})
	defer cleanup()

	configResp := performRequest(t, engine, http.MethodGet, "/api/config", nil, "")
	if configResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", configResp.Code, configResp.Body.String())
	}
	var cfg model.AppConfig
	decodeJSONResponse(t, configResp.Body.Bytes(), &cfg)

	resp := performRequestWithHeaders(t, engine, http.MethodGet, "/mcp", nil, "", map[string]string{
		"Authorization": "Bearer " + cfg.MCP.Token,
	})
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d, body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "ENABLE_AUTH=true") {
		t.Fatalf("expected auth required error, got %s", resp.Body.String())
	}
}

func TestMCPCompatibleTokenStillWorks(t *testing.T) {
	engine, _, sessionHeaders, cleanup := newAuthenticatedTestRouter(t)
	defer cleanup()

	cfg := getTestConfig(t, engine, sessionHeaders)
	if strings.TrimSpace(cfg.MCP.Token) == "" {
		t.Fatal("expected compatible mcp token")
	}

	resp := performRequestWithHeaders(t, engine, http.MethodGet, "/mcp", nil, "", map[string]string{
		"Authorization": "Bearer " + cfg.MCP.Token,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("expected compatible token to authorize mcp info, got %d, body=%s", resp.Code, resp.Body.String())
	}
}

func TestMCPAPIKeyScopeEnforcement(t *testing.T) {
	engine, _, sessionHeaders, cleanup := newAuthenticatedTestRouter(t)
	defer cleanup()

	readHeaders := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-read-only", []string{"mcp:read"})
	infoResp := performRequestWithHeaders(t, engine, http.MethodGet, "/mcp", nil, "", readHeaders)
	if infoResp.Code != http.StatusOK {
		t.Fatalf("expected mcp:read to authorize info, got %d, body=%s", infoResp.Code, infoResp.Body.String())
	}

	writeWithReadResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      200,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "create_knowledge_base",
				"arguments": map[string]any{
					"name": "should-not-create",
				},
			},
		})),
		"application/json",
		readHeaders,
	)
	if writeWithReadResp.Code != http.StatusForbidden {
		t.Fatalf("expected mcp:read to reject write tool, got %d, body=%s", writeWithReadResp.Code, writeWithReadResp.Body.String())
	}
	if !strings.Contains(writeWithReadResp.Body.String(), "mcp:write") {
		t.Fatalf("expected required mcp:write scope, got %s", writeWithReadResp.Body.String())
	}

	evalWithReadResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      201,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "generate_eval_dataset",
				"arguments": map[string]any{},
			},
		})),
		"application/json",
		readHeaders,
	)
	if evalWithReadResp.Code != http.StatusForbidden {
		t.Fatalf("expected mcp:read to reject eval tool, got %d, body=%s", evalWithReadResp.Code, evalWithReadResp.Body.String())
	}
	if !strings.Contains(evalWithReadResp.Body.String(), "mcp:eval") {
		t.Fatalf("expected required mcp:eval scope, got %s", evalWithReadResp.Body.String())
	}

	writeHeaders := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-write-only", []string{"mcp:write"})
	uploadWithWriteResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      202,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "upload_text_document",
				"arguments": map[string]any{},
			},
		})),
		"application/json",
		writeHeaders,
	)
	if uploadWithWriteResp.Code != http.StatusForbidden {
		t.Fatalf("expected mcp:write to reject upload tool, got %d, body=%s", uploadWithWriteResp.Code, uploadWithWriteResp.Body.String())
	}
	if !strings.Contains(uploadWithWriteResp.Body.String(), "mcp:upload") {
		t.Fatalf("expected required mcp:upload scope, got %s", uploadWithWriteResp.Body.String())
	}

	dangerWithWriteResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      203,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "delete_conversation",
				"arguments": map[string]any{
					"id": "conv-does-not-matter",
				},
			},
		})),
		"application/json",
		writeHeaders,
	)
	if dangerWithWriteResp.Code != http.StatusForbidden {
		t.Fatalf("expected mcp:write to reject danger tool, got %d, body=%s", dangerWithWriteResp.Code, dangerWithWriteResp.Body.String())
	}
	if !strings.Contains(dangerWithWriteResp.Body.String(), "mcp:danger") {
		t.Fatalf("expected required mcp:danger scope, got %s", dangerWithWriteResp.Body.String())
	}
}

func TestMCPToolsListAndCreateKnowledgeBase(t *testing.T) {
	engine, _, sessionHeaders, cleanup := newAuthenticatedTestRouter(t)
	defer cleanup()

	headers := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-admin-tools", []string{"mcp:admin"})

	infoResp := performRequestWithHeaders(t, engine, http.MethodGet, "/mcp", nil, "", headers)
	if infoResp.Code != http.StatusOK {
		t.Fatalf("expected info status 200, got %d, body=%s", infoResp.Code, infoResp.Body.String())
	}
	if !strings.Contains(infoResp.Body.String(), `"version":"0.2.0"`) {
		t.Fatalf("expected mcp version in info response, got %s", infoResp.Body.String())
	}

	initResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      100,
			"method":  "initialize",
			"params":  map[string]any{},
		})),
		"application/json",
		headers,
	)
	if initResp.Code != http.StatusOK {
		t.Fatalf("expected initialize status 200, got %d, body=%s", initResp.Code, initResp.Body.String())
	}
	if !strings.Contains(initResp.Body.String(), `"version":"0.2.0"`) {
		t.Fatalf("expected mcp version in initialize response, got %s", initResp.Body.String())
	}

	listResp := performRequestWithHeaders(t, engine, http.MethodGet, "/mcp/tools", nil, "", headers)
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", listResp.Code, listResp.Body.String())
	}
	var listPayload struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	decodeJSONResponse(t, listResp.Body.Bytes(), &listPayload)

	toolNames := make([]string, 0, len(listPayload.Tools))
	for _, tool := range listPayload.Tools {
		toolNames = append(toolNames, tool.Name)
		requiredValue, ok := tool.InputSchema["required"]
		if !ok {
			t.Fatalf("expected tool %s to include inputSchema.required", tool.Name)
		}
		requiredList, ok := requiredValue.([]any)
		if !ok {
			t.Fatalf("expected tool %s inputSchema.required to be array, got %T (%v)", tool.Name, requiredValue, requiredValue)
		}
		if requiredList == nil {
			t.Fatalf("expected tool %s inputSchema.required to be non-nil array", tool.Name)
		}
		assertArraySchemaHasItems(t, tool.Name, tool.InputSchema)
	}
	if !containsString(toolNames, "create_knowledge_base") {
		t.Fatalf("expected create_knowledge_base in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "get_mcp_capabilities") {
		t.Fatalf("expected get_mcp_capabilities in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "delete_knowledge_base") {
		t.Fatalf("expected delete_knowledge_base in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "delete_document") {
		t.Fatalf("expected delete_document in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "upload_text_document") {
		t.Fatalf("expected upload_text_document in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "register_staged_upload") {
		t.Fatalf("expected register_staged_upload in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "search_document") {
		t.Fatalf("expected search_document in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "get_document_detail") {
		t.Fatalf("expected get_document_detail in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "debug_retrieval") {
		t.Fatalf("expected debug_retrieval in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "reindex_document") {
		t.Fatalf("expected reindex_document in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "query_structured_data") {
		t.Fatalf("expected query_structured_data in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "generate_eval_dataset") {
		t.Fatalf("expected generate_eval_dataset in tools list, got %v", toolNames)
	}

	capabilitiesResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      101,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "get_mcp_capabilities",
				"arguments": map[string]any{},
			},
		})),
		"application/json",
		headers,
	)
	if capabilitiesResp.Code != http.StatusOK {
		t.Fatalf("expected capabilities status 200, got %d, body=%s", capabilitiesResp.Code, capabilitiesResp.Body.String())
	}
	if !strings.Contains(capabilitiesResp.Body.String(), `"toolCount":21`) ||
		!strings.Contains(capabilitiesResp.Body.String(), `"version":"0.2.0"`) ||
		!strings.Contains(capabilitiesResp.Body.String(), `"permissionCounts"`) {
		t.Fatalf("expected mcp capability summary, got %s", capabilitiesResp.Body.String())
	}

	callResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "create_knowledge_base",
				"arguments": map[string]any{
					"name":        "MCP 新建知识库",
					"description": "通过 MCP 创建",
				},
			},
		})),
		"application/json",
		headers,
	)
	if callResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", callResp.Code, callResp.Body.String())
	}

	var rpcResp struct {
		Result struct {
			Data struct {
				KnowledgeBase model.KnowledgeBase `json:"knowledgeBase"`
			} `json:"data"`
		} `json:"result"`
	}
	decodeJSONResponse(t, callResp.Body.Bytes(), &rpcResp)
	if rpcResp.Result.Data.KnowledgeBase.Name != "MCP 新建知识库" {
		t.Fatalf("expected created knowledge base name, got %+v", rpcResp.Result.Data.KnowledgeBase)
	}

	kbListResp := performRequestWithHeaders(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "", sessionHeaders)
	if kbListResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", kbListResp.Code, kbListResp.Body.String())
	}
	var kbList struct {
		Items []model.KnowledgeBase `json:"items"`
	}
	decodeJSONResponse(t, kbListResp.Body.Bytes(), &kbList)
	if len(kbList.Items) < 2 {
		t.Fatalf("expected at least 2 knowledge bases after MCP create, got %d", len(kbList.Items))
	}
}

func TestMCPUploadTextDocument(t *testing.T) {
	engine, _, sessionHeaders, cleanup := newAuthenticatedTestRouter(t)
	defer cleanup()

	headers := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-admin-upload-text", []string{"mcp:admin"})

	kbListResp := performRequestWithHeaders(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "", sessionHeaders)
	if kbListResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", kbListResp.Code, kbListResp.Body.String())
	}
	var kbList struct {
		Items []model.KnowledgeBase `json:"items"`
	}
	decodeJSONResponse(t, kbListResp.Body.Bytes(), &kbList)
	if len(kbList.Items) == 0 {
		t.Fatal("expected default knowledge base")
	}
	knowledgeBaseID := kbList.Items[0].ID

	resp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      11,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "upload_text_document",
				"arguments": map[string]any{
					"knowledgeBaseId": knowledgeBaseID,
					"fileName":        "sample-intro.txt",
					"content":         "示例机构A是一所综合性院校。",
				},
			},
		})),
		"application/json",
		headers,
	)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	var rpcResp struct {
		Result struct {
			Data struct {
				Uploaded model.Document `json:"uploaded"`
			} `json:"data"`
		} `json:"result"`
	}
	decodeJSONResponse(t, resp.Body.Bytes(), &rpcResp)
	if rpcResp.Result.Data.Uploaded.Name != "sample-intro.txt" {
		t.Fatalf("expected uploaded text document, got %+v", rpcResp.Result.Data.Uploaded)
	}
}

func TestMCPStructuredDataQueryAndEvalDataset(t *testing.T) {
	engine, _, sessionHeaders, cleanup := newAuthenticatedTestRouter(t)
	defer cleanup()

	headers := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-admin-structured", []string{"mcp:admin"})

	kbListResp := performRequestWithHeaders(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "", sessionHeaders)
	if kbListResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", kbListResp.Code, kbListResp.Body.String())
	}
	var kbList struct {
		Items []model.KnowledgeBase `json:"items"`
	}
	decodeJSONResponse(t, kbListResp.Body.Bytes(), &kbList)
	if len(kbList.Items) == 0 {
		t.Fatal("expected default knowledge base")
	}
	knowledgeBaseID := kbList.Items[0].ID

	uploadResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      21,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "upload_text_document",
				"arguments": map[string]any{
					"knowledgeBaseId": knowledgeBaseID,
					"fileName":        "mcp-users.csv",
					"content":         "姓名,城市,薪资\n张三,上海,24000\n李四,北京,18000\n王五,上海,7000\n",
				},
			},
		})),
		"application/json",
		headers,
	)
	if uploadResp.Code != http.StatusOK {
		t.Fatalf("expected upload status 200, got %d, body=%s", uploadResp.Code, uploadResp.Body.String())
	}
	var uploadRPC struct {
		Result struct {
			Data struct {
				Uploaded model.Document `json:"uploaded"`
			} `json:"data"`
		} `json:"result"`
	}
	decodeJSONResponse(t, uploadResp.Body.Bytes(), &uploadRPC)
	documentID := uploadRPC.Result.Data.Uploaded.ID
	if documentID == "" {
		t.Fatalf("expected uploaded document id, got %+v", uploadRPC.Result.Data.Uploaded)
	}

	queryResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      22,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "query_structured_data",
				"arguments": map[string]any{
					"documentId": documentID,
					"query":      "筛选城市是上海的数据",
				},
			},
		})),
		"application/json",
		headers,
	)
	if queryResp.Code != http.StatusOK {
		t.Fatalf("expected query status 200, got %d, body=%s", queryResp.Code, queryResp.Body.String())
	}
	if !strings.Contains(queryResp.Body.String(), "|张三|上海|24000|") || !strings.Contains(queryResp.Body.String(), "|王五|上海|7000|") {
		t.Fatalf("expected structured rows in MCP result, got %s", queryResp.Body.String())
	}

	detailResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      24,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "get_document_detail",
				"arguments": map[string]any{
					"knowledgeBaseId": knowledgeBaseID,
					"documentId":      documentID,
				},
			},
		})),
		"application/json",
		headers,
	)
	if detailResp.Code != http.StatusOK {
		t.Fatalf("expected detail status 200, got %d, body=%s", detailResp.Code, detailResp.Body.String())
	}
	if !strings.Contains(detailResp.Body.String(), `"diagnostics"`) || !strings.Contains(detailResp.Body.String(), `"chunks"`) {
		t.Fatalf("expected document detail diagnostics and chunks, got %s", detailResp.Body.String())
	}

	debugResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      25,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "debug_retrieval",
				"arguments": map[string]any{
					"documentId": documentID,
					"query":      "薪资最高是谁",
					"topK":       5,
				},
			},
		})),
		"application/json",
		headers,
	)
	if debugResp.Code != http.StatusOK {
		t.Fatalf("expected debug status 200, got %d, body=%s", debugResp.Code, debugResp.Body.String())
	}
	if !strings.Contains(debugResp.Body.String(), `"deterministicUsed":true`) || !strings.Contains(debugResp.Body.String(), `"structuredIntent":"max"`) {
		t.Fatalf("expected retrieval debug deterministic metadata, got %s", debugResp.Body.String())
	}

	reindexResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      26,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "reindex_document",
				"arguments": map[string]any{
					"knowledgeBaseId": knowledgeBaseID,
					"documentId":      documentID,
				},
			},
		})),
		"application/json",
		headers,
	)
	if reindexResp.Code != http.StatusOK {
		t.Fatalf("expected reindex status 200, got %d, body=%s", reindexResp.Code, reindexResp.Body.String())
	}
	if !strings.Contains(reindexResp.Body.String(), `"status":"indexed"`) {
		t.Fatalf("expected reindexed document status, got %s", reindexResp.Body.String())
	}

	evalResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      23,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "generate_eval_dataset",
				"arguments": map[string]any{
					"documentId":     documentID,
					"maxPerDocument": 2,
				},
			},
		})),
		"application/json",
		headers,
	)
	if evalResp.Code != http.StatusOK {
		t.Fatalf("expected eval status 200, got %d, body=%s", evalResp.Code, evalResp.Body.String())
	}
	var evalRPC struct {
		Result struct {
			Data struct {
				Dataset model.GenerateEvalDatasetResponse `json:"dataset"`
			} `json:"data"`
		} `json:"result"`
	}
	decodeJSONResponse(t, evalResp.Body.Bytes(), &evalRPC)
	if evalRPC.Result.Data.Dataset.Count == 0 {
		t.Fatalf("expected generated eval cases, got %+v", evalRPC.Result.Data.Dataset)
	}
}

func TestHTTPStageUploadAndMCPRegisterStagedUpload(t *testing.T) {
	engine, _, sessionHeaders, cleanup := newAuthenticatedTestRouter(t)
	defer cleanup()

	headers := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-admin-register", []string{"mcp:admin"})

	kbListResp := performRequestWithHeaders(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "", sessionHeaders)
	if kbListResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", kbListResp.Code, kbListResp.Body.String())
	}
	var kbList struct {
		Items []model.KnowledgeBase `json:"items"`
	}
	decodeJSONResponse(t, kbListResp.Body.Bytes(), &kbList)
	if len(kbList.Items) == 0 {
		t.Fatal("expected default knowledge base")
	}
	knowledgeBaseID := kbList.Items[0].ID

	stageResp := performMultipartUploadWithHeaders(t, engine, http.MethodPost, "/api/uploads", "sample-stage.md", "# Sample Stage\n\n这是通过 staging + MCP register 导入的示例文档。", sessionHeaders)
	if stageResp.Code != http.StatusOK {
		t.Fatalf("expected stage upload status 200, got %d, body=%s", stageResp.Code, stageResp.Body.String())
	}
	var stagedResult model.StageUploadResponse
	decodeJSONResponse(t, stageResp.Body.Bytes(), &stagedResult)
	if strings.TrimSpace(stagedResult.UploadID) == "" {
		t.Fatalf("expected uploadId, got %+v", stagedResult)
	}

	registerResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      12,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "register_staged_upload",
				"arguments": map[string]any{
					"uploadId":        stagedResult.UploadID,
					"knowledgeBaseId": knowledgeBaseID,
					"fileName":        "registered-sample.md",
				},
			},
		})),
		"application/json",
		headers,
	)
	if registerResp.Code != http.StatusOK {
		t.Fatalf("expected register status 200, got %d, body=%s", registerResp.Code, registerResp.Body.String())
	}
	var rpcResp struct {
		Result struct {
			Data struct {
				Uploaded model.Document `json:"uploaded"`
			} `json:"data"`
		} `json:"result"`
	}
	decodeJSONResponse(t, registerResp.Body.Bytes(), &rpcResp)
	if rpcResp.Result.Data.Uploaded.Name != "registered-sample.md" {
		t.Fatalf("expected registered staged upload name, got %+v", rpcResp.Result.Data.Uploaded)
	}
	if rpcResp.Result.Data.Uploaded.Status != "indexed" {
		t.Fatalf("expected indexed staged upload, got %+v", rpcResp.Result.Data.Uploaded)
	}
}

func TestMCPInlineUploadTooLargeReturnsGuidance(t *testing.T) {
	engine, _, sessionHeaders, cleanup := newAuthenticatedTestRouter(t)
	defer cleanup()

	headers := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-admin-large-upload", []string{"mcp:admin"})

	kbListResp := performRequestWithHeaders(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "", sessionHeaders)
	if kbListResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", kbListResp.Code, kbListResp.Body.String())
	}
	var kbList struct {
		Items []model.KnowledgeBase `json:"items"`
	}
	decodeJSONResponse(t, kbListResp.Body.Bytes(), &kbList)
	if len(kbList.Items) == 0 {
		t.Fatal("expected default knowledge base")
	}

	largeBase64 := make([]byte, 256*1024+1)
	for i := range largeBase64 {
		largeBase64[i] = 'a'
	}
	resp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      13,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "upload_document",
				"arguments": map[string]any{
					"knowledgeBaseId": kbList.Items[0].ID,
					"fileName":        "large.pdf",
					"contentBase64":   base64.StdEncoding.EncodeToString(largeBase64),
				},
			},
		})),
		"application/json",
		headers,
	)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "register_staged_upload") || !strings.Contains(resp.Body.String(), "/api/uploads") {
		t.Fatalf("expected staged upload guidance, got %s", resp.Body.String())
	}
}

func TestChatCompletionsIncludesToolUseMetadata(t *testing.T) {
	engine, modelBaseURL, cleanup := newTestRouter(t)
	defer cleanup()

	listResp := performRequest(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "")
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", listResp.Code, listResp.Body.String())
	}

	var kbList struct {
		Items []model.KnowledgeBase `json:"items"`
	}
	decodeJSONResponse(t, listResp.Body.Bytes(), &kbList)
	if len(kbList.Items) == 0 {
		t.Fatal("expected default knowledge base")
	}
	knowledgeBaseID := kbList.Items[0].ID

	uploadResp := performMultipartUpload(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/knowledge-bases/%s/documents", knowledgeBaseID),
		"tooluse-sample.md",
		"# 示例组件\n\n示例组件A支持缓存、队列与持久化能力。",
	)
	if uploadResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", uploadResp.Code, uploadResp.Body.String())
	}

	chatPayload := map[string]any{
		"conversationId":  "conv-tooluse-1",
		"model":           "chat-test-model",
		"knowledgeBaseId": knowledgeBaseID,
		"documentId":      "",
		"config": map[string]any{
			"provider":    "ollama",
			"baseUrl":     modelBaseURL,
			"model":       "chat-test-model",
			"apiKey":      "",
			"temperature": 0.2,
		},
		"messages": []map[string]string{{
			"role":    "user",
			"content": "请介绍示例组件A的主要用途",
		}},
	}

	resp := performJSONRequest(t, engine, http.MethodPost, "/v1/chat/completions", chatPayload)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	var chatResult model.ChatCompletionResponse
	decodeJSONResponse(t, resp.Body.Bytes(), &chatResult)
	toolUseRaw, ok := chatResult.Metadata["toolUse"].([]any)
	if !ok || len(toolUseRaw) == 0 {
		t.Fatalf("expected toolUse metadata, got %#v", chatResult.Metadata["toolUse"])
	}
	firstToolUse, ok := toolUseRaw[0].(map[string]any)
	if !ok {
		t.Fatalf("expected toolUse item object, got %#v", toolUseRaw[0])
	}
	if firstToolUse["toolName"] != "search_knowledge_base" {
		t.Fatalf("expected search_knowledge_base tool use, got %#v", firstToolUse)
	}
}

func TestOpenAICompatibleAPIAuthRejectsMissingTokenAndAllowsAPIKey(t *testing.T) {
	engine, modelBaseURL, cleanup := newTestRouterWithServerConfig(t, func(serverConfig *model.ServerConfig) {
		serverConfig.EnableAuth = true
		serverConfig.AuthUsername = "root"
		serverConfig.AuthPassword = "correct-password"
	})
	defer cleanup()

	chatPayload := map[string]any{
		"conversationId": "conv-auth-openai-compatible",
		"model":          "chat-test-model",
		"config": map[string]any{
			"provider":    "ollama",
			"baseUrl":     modelBaseURL,
			"model":       "chat-test-model",
			"apiKey":      "",
			"temperature": 0.2,
		},
		"messages": []map[string]string{{
			"role":    "user",
			"content": "Redis 是什么",
		}},
	}

	missingAuthResp := performJSONRequest(t, engine, http.MethodPost, "/v1/chat/completions", chatPayload)
	if missingAuthResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing auth status 401, got %d, body=%s", missingAuthResp.Code, missingAuthResp.Body.String())
	}

	loginResp := performJSONRequest(t, engine, http.MethodPost, "/api/auth/login", map[string]string{
		"username": "root",
		"password": "correct-password",
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("expected login status 200, got %d, body=%s", loginResp.Code, loginResp.Body.String())
	}
	sessionCookie := findResponseCookie(loginResp.Result().Cookies(), auth.SessionCookieName)
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("expected %s cookie after login", auth.SessionCookieName)
	}

	apiKeyResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/api/auth/api-keys",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"name":   "OpenAI compatible test key",
			"scopes": []string{"openai:chat"},
		})),
		"application/json",
		map[string]string{"Cookie": auth.SessionCookieName + "=" + sessionCookie.Value},
	)
	if apiKeyResp.Code != http.StatusCreated {
		t.Fatalf("expected api key status 201, got %d, body=%s", apiKeyResp.Code, apiKeyResp.Body.String())
	}
	var createdKey service.CreatedAPIKey
	decodeJSONResponse(t, apiKeyResp.Body.Bytes(), &createdKey)
	if !strings.HasPrefix(createdKey.Token, "ailb_sk_") {
		t.Fatalf("expected api key token in response, got %+v", createdKey)
	}

	allowedResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(mustMarshalJSON(t, chatPayload)),
		"application/json",
		map[string]string{"Authorization": "Bearer " + createdKey.Token},
	)
	if allowedResp.Code != http.StatusOK {
		t.Fatalf("expected api key to authorize chat completions, got %d, body=%s", allowedResp.Code, allowedResp.Body.String())
	}
}

func TestMCPDangerConfirmationNonceFlow(t *testing.T) {
	engine, _, sessionHeaders, cleanup := newAuthenticatedTestRouter(t)
	defer cleanup()

	headers := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-danger-nonce", []string{"mcp:danger"})
	dangerArgs := map[string]any{"id": "conv-danger-nonce"}

	missingNonceResp := performMCPToolCall(t, engine, headers, 301, "delete_conversation", dangerArgs)
	if missingNonceResp.Code != http.StatusForbidden {
		t.Fatalf("expected missing nonce status 403, got %d, body=%s", missingNonceResp.Code, missingNonceResp.Body.String())
	}
	if !strings.Contains(missingNonceResp.Body.String(), "confirmNonce") {
		t.Fatalf("expected confirmNonce error, got %s", missingNonceResp.Body.String())
	}

	wrongNonceArgs := map[string]any{"id": "conv-danger-nonce", "confirmNonce": "mcp_confirm_wrong"}
	wrongNonceResp := performMCPToolCall(t, engine, headers, 302, "delete_conversation", wrongNonceArgs)
	if wrongNonceResp.Code != http.StatusForbidden {
		t.Fatalf("expected wrong nonce status 403, got %d, body=%s", wrongNonceResp.Code, wrongNonceResp.Body.String())
	}

	confirmation := createMCPDangerConfirmation(t, engine, sessionHeaders, "delete_conversation", dangerArgs, 0)
	mismatchResp := performMCPToolCall(t, engine, headers, 303, "delete_conversation", map[string]any{
		"id":           "conv-other",
		"confirmNonce": confirmation.ConfirmNonce,
	})
	if mismatchResp.Code != http.StatusForbidden {
		t.Fatalf("expected mismatched nonce status 403, got %d, body=%s", mismatchResp.Code, mismatchResp.Body.String())
	}
	if !strings.Contains(mismatchResp.Body.String(), "invalid danger confirmation nonce") {
		t.Fatalf("expected mismatch error, got %s", mismatchResp.Body.String())
	}

	validConfirmation := createMCPDangerConfirmation(t, engine, sessionHeaders, "delete_conversation", dangerArgs, 0)
	validArgs := map[string]any{"id": "conv-danger-nonce", "confirmNonce": validConfirmation.ConfirmNonce}
	validResp := performMCPToolCall(t, engine, headers, 304, "delete_conversation", validArgs)
	if validResp.Code != http.StatusOK {
		t.Fatalf("expected valid nonce to reach tool handler, got %d, body=%s", validResp.Code, validResp.Body.String())
	}

	reusedResp := performMCPToolCall(t, engine, headers, 305, "delete_conversation", validArgs)
	if reusedResp.Code != http.StatusForbidden {
		t.Fatalf("expected reused nonce status 403, got %d, body=%s", reusedResp.Code, reusedResp.Body.String())
	}
	if !strings.Contains(reusedResp.Body.String(), "invalid or used") {
		t.Fatalf("expected reused nonce error, got %s", reusedResp.Body.String())
	}

	expiredConfirmation := createMCPDangerConfirmation(t, engine, sessionHeaders, "delete_conversation", dangerArgs, 1)
	time.Sleep(1100 * time.Millisecond)
	expiredResp := performMCPToolCall(t, engine, headers, 306, "delete_conversation", map[string]any{
		"id":           "conv-danger-nonce",
		"confirmNonce": expiredConfirmation.ConfirmNonce,
	})
	if expiredResp.Code != http.StatusForbidden {
		t.Fatalf("expected expired nonce status 403, got %d, body=%s", expiredResp.Code, expiredResp.Body.String())
	}
	if !strings.Contains(expiredResp.Body.String(), "expired danger confirmation nonce") {
		t.Fatalf("expected expired nonce error, got %s", expiredResp.Body.String())
	}
}

func TestMCPAuditEventsRecorded(t *testing.T) {
	engine, _, sessionHeaders, cleanup := newAuthenticatedTestRouter(t)
	defer cleanup()

	adminHeaders := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-audit-admin", []string{"mcp:admin"})
	successResp := performMCPToolCall(t, engine, adminHeaders, 401, "list_knowledge_bases", map[string]any{})
	if successResp.Code != http.StatusOK {
		t.Fatalf("expected success status 200, got %d, body=%s", successResp.Code, successResp.Body.String())
	}

	readHeaders := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-audit-read", []string{"mcp:read"})
	failedResp := performMCPToolCall(t, engine, readHeaders, 402, "create_knowledge_base", map[string]any{"name": "forbidden"})
	if failedResp.Code != http.StatusForbidden {
		t.Fatalf("expected scope failure status 403, got %d, body=%s", failedResp.Code, failedResp.Body.String())
	}

	dangerHeaders := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-audit-danger", []string{"mcp:danger"})
	dangerArgs := map[string]any{"id": "conv-audit-missing"}
	dangerConfirmation := createMCPDangerConfirmation(t, engine, sessionHeaders, "delete_conversation", dangerArgs, 0)
	dangerArgs["confirmNonce"] = dangerConfirmation.ConfirmNonce
	dangerResp := performMCPToolCall(t, engine, dangerHeaders, 403, "delete_conversation", dangerArgs)
	if dangerResp.Code != http.StatusOK {
		t.Fatalf("expected danger call to reach handler, got %d, body=%s", dangerResp.Code, dangerResp.Body.String())
	}

	eventsResp := performRequestWithHeaders(t, engine, http.MethodGet, "/api/auth/security-events?limit=20", nil, "", sessionHeaders)
	if eventsResp.Code != http.StatusOK {
		t.Fatalf("expected events status 200, got %d, body=%s", eventsResp.Code, eventsResp.Body.String())
	}
	var eventsPayload struct {
		Items []model.SecurityEvent `json:"items"`
	}
	decodeJSONResponse(t, eventsResp.Body.Bytes(), &eventsPayload)
	if !hasSecurityEvent(eventsPayload.Items, "mcp_call_succeeded", "tool=list_knowledge_bases") {
		t.Fatalf("expected mcp success audit event, got %+v", eventsPayload.Items)
	}
	if !hasSecurityEvent(eventsPayload.Items, "mcp_call_failed", "tool=create_knowledge_base") {
		t.Fatalf("expected mcp failed audit event, got %+v", eventsPayload.Items)
	}
	if !hasSecurityEvent(eventsPayload.Items, "mcp_danger_succeeded", "tool=delete_conversation") {
		t.Fatalf("expected mcp danger audit event, got %+v", eventsPayload.Items)
	}
}

func TestMCPDangerToolsDeleteKnowledgeBaseAndDocument(t *testing.T) {
	engine, _, sessionHeaders, cleanup := newAuthenticatedTestRouter(t)
	defer cleanup()

	headers := createTestAPIKeyHeaders(t, engine, sessionHeaders, "mcp-admin-danger", []string{"mcp:admin"})
	dangerHeaders := headers

	createKBResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]any{
				"name": "create_knowledge_base",
				"arguments": map[string]any{
					"name":        "删除工具测试知识库",
					"description": "用于通用删除流程测试",
				},
			},
		})),
		"application/json",
		headers,
	)
	if createKBResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", createKBResp.Code, createKBResp.Body.String())
	}

	var createKBRPC struct {
		Result struct {
			Data struct {
				KnowledgeBase model.KnowledgeBase `json:"knowledgeBase"`
			} `json:"data"`
		} `json:"result"`
	}
	decodeJSONResponse(t, createKBResp.Body.Bytes(), &createKBRPC)
	knowledgeBaseID := createKBRPC.Result.Data.KnowledgeBase.ID
	if knowledgeBaseID == "" {
		t.Fatal("expected created knowledge base id")
	}

	uploadResp := performMultipartUploadWithHeaders(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/knowledge-bases/%s/documents", knowledgeBaseID),
		"delete-tool-sample.md",
		"# 通用删除测试\n\n用于验证 MCP 删除文档工具。",
		sessionHeaders,
	)
	if uploadResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", uploadResp.Code, uploadResp.Body.String())
	}
	var uploadResult model.UploadResponse
	decodeJSONResponse(t, uploadResp.Body.Bytes(), &uploadResult)

	deleteDocArgs := map[string]any{
		"knowledgeBaseId": knowledgeBaseID,
		"documentId":      uploadResult.Uploaded.ID,
	}
	deleteDocConfirmation := createMCPDangerConfirmation(t, engine, sessionHeaders, "delete_document", deleteDocArgs, 0)
	deleteDocArgs["confirmNonce"] = deleteDocConfirmation.ConfirmNonce
	delDocResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      3,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "delete_document",
				"arguments": deleteDocArgs,
			},
		})),
		"application/json",
		dangerHeaders,
	)
	if delDocResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", delDocResp.Code, delDocResp.Body.String())
	}

	docListResp := performRequestWithHeaders(t, engine, http.MethodGet, fmt.Sprintf("/api/knowledge-bases/%s/documents", knowledgeBaseID), nil, "", sessionHeaders)
	if docListResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", docListResp.Code, docListResp.Body.String())
	}
	var docList struct {
		Items []model.Document `json:"items"`
	}
	decodeJSONResponse(t, docListResp.Body.Bytes(), &docList)
	if len(docList.Items) != 0 {
		t.Fatalf("expected 0 documents after delete, got %d", len(docList.Items))
	}

	deleteKBArgs := map[string]any{
		"knowledgeBaseId": knowledgeBaseID,
	}
	deleteKBConfirmation := createMCPDangerConfirmation(t, engine, sessionHeaders, "delete_knowledge_base", deleteKBArgs, 0)
	deleteKBArgs["confirmNonce"] = deleteKBConfirmation.ConfirmNonce
	delKBResp := performRequestWithHeaders(
		t,
		engine,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      4,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "delete_knowledge_base",
				"arguments": deleteKBArgs,
			},
		})),
		"application/json",
		dangerHeaders,
	)
	if delKBResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", delKBResp.Code, delKBResp.Body.String())
	}

	kbListResp := performRequestWithHeaders(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "", sessionHeaders)
	if kbListResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", kbListResp.Code, kbListResp.Body.String())
	}
	var kbList struct {
		Items []model.KnowledgeBase `json:"items"`
	}
	decodeJSONResponse(t, kbListResp.Body.Bytes(), &kbList)
	for _, item := range kbList.Items {
		if item.ID == knowledgeBaseID {
			t.Fatalf("expected knowledge base %s to be deleted", knowledgeBaseID)
		}
	}
}

func TestRouterRejectSensitiveStructuredUploadWithoutLocalOllama(t *testing.T) {
	engine, _, cleanup := newTestRouter(t)
	defer cleanup()

	updatePayload := map[string]any{
		"chat": map[string]any{
			"provider":    "openai-compatible",
			"baseUrl":     "http://chat.example.invalid/v1",
			"model":       "chat-model-b",
			"apiKey":      "test-chat-key",
			"temperature": 0.4,
		},
		"embedding": map[string]any{
			"provider": "openai-compatible",
			"baseUrl":  "http://embed.example.invalid/v1",
			"model":    "embed-model-b",
			"apiKey":   "test-embed-key-2",
		},
	}
	resp := performJSONRequest(t, engine, http.MethodPut, "/api/config", updatePayload)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	listResp := performRequest(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "")
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", listResp.Code, listResp.Body.String())
	}
	var kbList struct {
		Items []model.KnowledgeBase `json:"items"`
	}
	decodeJSONResponse(t, listResp.Body.Bytes(), &kbList)
	if len(kbList.Items) == 0 {
		t.Fatal("expected default knowledge base")
	}

	uploadResp := performMultipartUpload(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/knowledge-bases/%s/documents", kbList.Items[0].ID),
		"structured-sensitive.csv",
		"字段A,字段B\n值1,值2\n",
	)
	if uploadResp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d, body=%s", uploadResp.Code, uploadResp.Body.String())
	}
	if !strings.Contains(uploadResp.Body.String(), "requires local ollama") {
		t.Fatalf("expected local ollama policy error, got %s", uploadResp.Body.String())
	}
}

func TestRouterUploadRetrievalAndChatE2E(t *testing.T) {
	engine, modelBaseURL, cleanup := newTestRouter(t)
	defer cleanup()

	listResp := performRequest(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "")
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", listResp.Code, listResp.Body.String())
	}

	var kbList struct {
		Items []model.KnowledgeBase `json:"items"`
	}
	decodeJSONResponse(t, listResp.Body.Bytes(), &kbList)
	if len(kbList.Items) == 0 {
		t.Fatal("expected default knowledge base")
	}
	knowledgeBaseID := kbList.Items[0].ID

	documentContent := `# Redis 核心特点

Redis 是一个开源的内存数据结构存储系统，可用作数据库、缓存和消息代理。

## 主要特性

Redis 支持字符串、哈希、列表、集合、有序集合等多种数据结构。
Redis 具有极高的读写性能，单机每秒可处理数十万次请求。
Redis 支持数据持久化，可将内存中的数据保存到磁盘，重启后恢复。
Redis 支持主从复制，可实现读写分离与高可用部署。
Redis 提供发布订阅功能，支持消息传递模式。
Redis 支持 Lua 脚本，可实现原子性复杂操作。
Redis 内置事务支持，通过 MULTI/EXEC 命令实现。
Redis 支持过期时间设置，适合用作会话缓存或临时数据存储。

## 常见应用场景

缓存加速：将热点数据存入 Redis，减少数据库压力，提升响应速度。
计数器：利用 INCR 命令实现高并发下的精确计数，如页面浏览量统计。
排行榜：使用有序集合实现实时排行榜功能，支持按分数快速查询。
分布式锁：通过 SET NX 命令实现分布式锁，保证多节点下的互斥访问。
消息队列：使用列表结构实现简单的消息队列，支持生产者消费者模式。
会话管理：将用户会话数据存入 Redis，实现跨服务器的会话共享。
`
	uploadResp := performMultipartUpload(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/knowledge-bases/%s/documents", knowledgeBaseID),
		"redis-notes.md",
		documentContent,
	)
	if uploadResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", uploadResp.Code, uploadResp.Body.String())
	}

	var uploadResult model.UploadResponse
	decodeJSONResponse(t, uploadResp.Body.Bytes(), &uploadResult)
	if uploadResult.Uploaded.Status != "indexed" {
		t.Fatalf("expected uploaded document status indexed, got %s", uploadResult.Uploaded.Status)
	}
	if !strings.Contains(uploadResult.Uploaded.ContentPreview, "Redis") {
		t.Fatalf("expected content preview to contain indexed text, got %q", uploadResult.Uploaded.ContentPreview)
	}

	chatPayload := map[string]any{
		"conversationId":  "conv-e2e-1",
		"model":           "chat-test-model",
		"knowledgeBaseId": knowledgeBaseID,
		"documentId":      uploadResult.Uploaded.ID,
		"config": map[string]any{
			"provider":    "ollama",
			"baseUrl":     modelBaseURL,
			"model":       "chat-test-model",
			"apiKey":      "",
			"temperature": 0.2,
		},
		"messages": []map[string]string{{
			"role":    "user",
			"content": "请说明 Redis 的核心特点",
		}},
	}

	resp := performJSONRequest(t, engine, http.MethodPost, "/v1/chat/completions", chatPayload)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	var chatResult model.ChatCompletionResponse
	decodeJSONResponse(t, resp.Body.Bytes(), &chatResult)
	if len(chatResult.Choices) == 0 {
		t.Fatal("expected chat choices")
	}
	answer := chatResult.Choices[0].Message.Content
	if !strings.Contains(answer, "Redis") {
		t.Fatalf("expected answer to mention Redis, got %q", answer)
	}

	sources, ok := chatResult.Metadata["sources"].([]any)
	if !ok || len(sources) == 0 {
		t.Fatalf("expected retrieval sources in metadata, got %#v", chatResult.Metadata["sources"])
	}
	firstSource, ok := sources[0].(map[string]any)
	if !ok {
		t.Fatalf("expected source metadata object, got %#v", sources[0])
	}
	if strings.TrimSpace(fmt.Sprint(firstSource["chunkId"])) == "" {
		t.Fatalf("expected chunk id in source metadata, got %#v", firstSource)
	}
	if strings.TrimSpace(fmt.Sprint(firstSource["chunkIndex"])) == "" {
		t.Fatalf("expected chunk index in source metadata, got %#v", firstSource)
	}
	if strings.TrimSpace(fmt.Sprint(firstSource["snippet"])) == "" {
		t.Fatalf("expected snippet in source metadata, got %#v", firstSource)
	}
}

func TestRouterStructuredCSVCountQuestionUsesCondensedAnswerRules(t *testing.T) {
	engine, modelBaseURL, cleanup := newTestRouter(t)
	defer cleanup()

	listResp := performRequest(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "")
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", listResp.Code, listResp.Body.String())
	}

	var kbList struct {
		Items []model.KnowledgeBase `json:"items"`
	}
	decodeJSONResponse(t, listResp.Body.Bytes(), &kbList)
	if len(kbList.Items) == 0 {
		t.Fatal("expected default knowledge base")
	}
	knowledgeBaseID := kbList.Items[0].ID

	csvContent := "姓名,性别,职称,教龄\n张三,男,高级职称,20\n李四,女,中级职称,8\n王五,男,无职称,4\n赵六,女,助教,1\n"
	uploadResp := performMultipartUpload(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/knowledge-bases/%s/documents", knowledgeBaseID),
		"employees.csv",
		csvContent,
	)
	if uploadResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", uploadResp.Code, uploadResp.Body.String())
	}

	var uploadResult model.UploadResponse
	decodeJSONResponse(t, uploadResp.Body.Bytes(), &uploadResult)

	chatPayload := map[string]any{
		"conversationId":  "conv-e2e-csv-count",
		"model":           "chat-test-model",
		"knowledgeBaseId": knowledgeBaseID,
		"documentId":      uploadResult.Uploaded.ID,
		"config": map[string]any{
			"provider":    "ollama",
			"baseUrl":     modelBaseURL,
			"model":       "chat-test-model",
			"apiKey":      "",
			"temperature": 0.2,
		},
		"messages": []map[string]string{{
			"role":    "user",
			"content": "这个文档有多少名员工",
		}},
	}

	resp := performJSONRequest(t, engine, http.MethodPost, "/v1/chat/completions", chatPayload)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	var chatResult model.ChatCompletionResponse
	decodeJSONResponse(t, resp.Body.Bytes(), &chatResult)
	if len(chatResult.Choices) == 0 {
		t.Fatal("expected chat choices")
	}
	answer := chatResult.Choices[0].Message.Content
	if !strings.Contains(answer, "该文档中共有 4 名员工") {
		t.Fatalf("expected concise count answer, got %q", answer)
	}
	if strings.Count(answer, "4 名员工") != 1 {
		t.Fatalf("expected count conclusion to appear once, got %q", answer)
	}
	if strings.Contains(answer, "字段：") {
		t.Fatalf("expected field list to be omitted for count question, got %q", answer)
	}
}

func newTestRouter(t *testing.T) (*http.ServeMux, string, func()) {
	return newTestRouterWithServerConfig(t, nil)
}

func newAuthenticatedTestRouter(t *testing.T) (*http.ServeMux, string, map[string]string, func()) {
	t.Helper()
	engine, modelBaseURL, cleanup := newTestRouterWithServerConfig(t, func(serverConfig *model.ServerConfig) {
		serverConfig.EnableAuth = true
		serverConfig.AuthUsername = "root"
		serverConfig.AuthPassword = "correct-password"
	})
	return engine, modelBaseURL, loginTestSessionHeaders(t, engine), cleanup
}

func newTestRouterWithServerConfig(t *testing.T, configure func(*model.ServerConfig)) (*http.ServeMux, string, func()) {
	t.Helper()

	uploadDir := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "app-state.json")
	chatHistoryPath := filepath.Join(t.TempDir(), "chat-history.db")
	chatHistoryStore, err := service.NewSQLiteChatHistoryStore(chatHistoryPath)
	if err != nil {
		t.Fatalf("create chat history store: %v", err)
	}
	qdrantState := &qdrantTestServer{collections: map[string]*qdrantCollectionState{}}
	qdrantHTTP := httptest.NewServer(http.HandlerFunc(qdrantState.handle))
	modelHTTP := httptest.NewServer(http.HandlerFunc(handleModelAPI))

	serverConfig := model.ServerConfig{
		Port:                   "0",
		UploadDir:              uploadDir,
		StateFile:              statePath,
		QdrantURL:              qdrantHTTP.URL,
		QdrantCollectionPrefix: "kb_",
		QdrantVectorSize:       8,
		QdrantDistance:         "Cosine",
		QdrantTimeoutSeconds:   5,
		EnableMCP:              true,
		MCPBasePath:            "/mcp",
	}
	if configure != nil {
		configure(&serverConfig)
	}

	qdrantService := service.NewQdrantService(serverConfig)
	appService := service.NewAppService(qdrantService, service.NewAppStateStore(serverConfig.StateFile), chatHistoryStore, serverConfig)
	initialConfig := appService.GetConfig()
	_, err = appService.UpdateConfig(model.ConfigUpdateRequest{
		Chat: model.ChatConfig{
			Provider:    "ollama",
			BaseURL:     modelHTTP.URL,
			Model:       "chat-test-model",
			APIKey:      "",
			Temperature: 0.2,
		},
		Embedding: model.EmbeddingConfig{
			Provider: "ollama",
			BaseURL:  modelHTTP.URL,
			Model:    "embedding-test-model",
			APIKey:   "",
		},
		MCP: model.MCPConfig{
			Enabled:  serverConfig.EnableMCP,
			BasePath: serverConfig.MCPBasePath,
			Token:    initialConfig.MCP.Token,
		},
	})
	if err != nil {
		t.Fatalf("update config: %v", err)
	}

	mcpRegistry := mcp.DefaultRegistry(appService)
	toolPlanner := mcp.NewToolUsePlanner(mcpRegistry)
	appHandler := handler.NewAppHandler(serverConfig, appService, service.NewLLMService(), toolPlanner)
	configHandler := handler.NewConfigHandler(appService, qdrantService)
	authService, err := service.NewAuthService(appService, serverConfig)
	if err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	authHandler := handler.NewAuthHandler(authService, serverConfig.EnableAuth)
	mcpServer := mcp.NewServer(mcpRegistry, appService, authService, serverConfig)
	ginEngine := NewRouter(appHandler, configHandler, authHandler, authService, serverConfig, mcpServer, fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>test</body></html>")},
	})

	mux := http.NewServeMux()
	mux.Handle("/", ginEngine)

	cleanup := func() {
		_ = chatHistoryStore.Close()
		modelHTTP.Close()
		qdrantHTTP.Close()
		_ = os.RemoveAll(uploadDir)
	}
	return mux, modelHTTP.URL, cleanup
}

func loginTestSessionHeaders(t *testing.T, handler http.Handler) map[string]string {
	t.Helper()
	loginResp := performJSONRequest(t, handler, http.MethodPost, "/api/auth/login", map[string]string{
		"username": "root",
		"password": "correct-password",
	})
	if loginResp.Code != http.StatusOK {
		t.Fatalf("expected login status 200, got %d, body=%s", loginResp.Code, loginResp.Body.String())
	}
	sessionCookie := findResponseCookie(loginResp.Result().Cookies(), auth.SessionCookieName)
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("expected %s cookie after login", auth.SessionCookieName)
	}
	return map[string]string{"Cookie": auth.SessionCookieName + "=" + sessionCookie.Value}
}

func getTestConfig(t *testing.T, handler http.Handler, headers map[string]string) model.AppConfig {
	t.Helper()
	configResp := performRequestWithHeaders(t, handler, http.MethodGet, "/api/config", nil, "", headers)
	if configResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", configResp.Code, configResp.Body.String())
	}
	var cfg model.AppConfig
	decodeJSONResponse(t, configResp.Body.Bytes(), &cfg)
	return cfg
}

func createTestAPIKeyHeaders(t *testing.T, handler http.Handler, sessionHeaders map[string]string, name string, scopes []string) map[string]string {
	t.Helper()
	resp := performRequestWithHeaders(
		t,
		handler,
		http.MethodPost,
		"/api/auth/api-keys",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"name":   name,
			"scopes": scopes,
		})),
		"application/json",
		sessionHeaders,
	)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected api key status 201, got %d, body=%s", resp.Code, resp.Body.String())
	}
	var created service.CreatedAPIKey
	decodeJSONResponse(t, resp.Body.Bytes(), &created)
	if !strings.HasPrefix(created.Token, "ailb_sk_") {
		t.Fatalf("expected api key token, got %+v", created)
	}
	return map[string]string{"Authorization": "Bearer " + created.Token}
}

func createMCPDangerConfirmation(t *testing.T, handler http.Handler, sessionHeaders map[string]string, toolName string, args map[string]any, ttlSeconds int) model.MCPDangerConfirmationResponse {
	t.Helper()
	payload := map[string]any{
		"toolName":  toolName,
		"arguments": args,
	}
	if ttlSeconds > 0 {
		payload["ttlSeconds"] = ttlSeconds
	}
	resp := performRequestWithHeaders(
		t,
		handler,
		http.MethodPost,
		"/api/config/mcp/danger-confirmations",
		bytes.NewReader(mustMarshalJSON(t, payload)),
		"application/json",
		sessionHeaders,
	)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected danger confirmation status 201, got %d, body=%s", resp.Code, resp.Body.String())
	}
	var confirmation model.MCPDangerConfirmationResponse
	decodeJSONResponse(t, resp.Body.Bytes(), &confirmation)
	if strings.TrimSpace(confirmation.ConfirmNonce) == "" {
		t.Fatalf("expected confirmNonce, got %+v", confirmation)
	}
	return confirmation
}

func performMCPToolCall(t *testing.T, handler http.Handler, headers map[string]string, id int, toolName string, args map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return performRequestWithHeaders(
		t,
		handler,
		http.MethodPost,
		"/mcp",
		bytes.NewReader(mustMarshalJSON(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      toolName,
				"arguments": args,
			},
		})),
		"application/json",
		headers,
	)
}

func hasSecurityEvent(items []model.SecurityEvent, eventType, messagePart string) bool {
	for _, item := range items {
		if item.Type == eventType && strings.Contains(item.Message, messagePart) {
			return true
		}
	}
	return false
}

func (s *qdrantTestServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	writeJSON := func(status int, payload any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}

	requestPath := strings.TrimPrefix(r.URL.Path, "/")
	segments := strings.Split(requestPath, "/")
	if len(segments) == 0 || segments[0] != "collections" {
		writeJSON(http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}

	if r.Method == http.MethodGet && len(segments) == 1 {
		writeJSON(http.StatusOK, map[string]any{"result": []any{}})
		return
	}

	if len(segments) < 2 {
		writeJSON(http.StatusNotFound, map[string]any{"error": "missing collection"})
		return
	}

	collectionName := segments[1]
	if _, ok := s.collections[collectionName]; !ok {
		s.collections[collectionName] = &qdrantCollectionState{}
	}
	collection := s.collections[collectionName]

	switch {
	case r.Method == http.MethodPut && len(segments) == 2:
		writeJSON(http.StatusOK, map[string]any{"result": true})
		return
	case r.Method == http.MethodDelete && len(segments) == 2:
		delete(s.collections, collectionName)
		writeJSON(http.StatusOK, map[string]any{"result": true})
		return
	case r.Method == http.MethodPut && len(segments) == 3 && segments[2] == "points":
		var req struct {
			Points []service.QdrantPoint `json:"points"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		collection.points = append([]service.QdrantPoint(nil), req.Points...)
		writeJSON(http.StatusOK, map[string]any{"result": map[string]any{"status": "acknowledged"}})
		return
	case r.Method == http.MethodPost && len(segments) == 4 && segments[2] == "points" && segments[3] == "search":
		var req struct {
			Filter map[string]any `json:"filter"`
			Limit  int            `json:"limit"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		limit := req.Limit
		if limit <= 0 {
			limit = 5
		}

		results := make([]map[string]any, 0, len(collection.points))
		for index, point := range collection.points {
			if !matchesFilter(point.Payload, req.Filter) {
				continue
			}
			results = append(results, map[string]any{
				"id":      point.ID,
				"score":   0.99 - float64(index)*0.01,
				"payload": point.Payload,
			})
			if len(results) >= limit {
				break
			}
		}
		writeJSON(http.StatusOK, map[string]any{"result": results})
		return
	default:
		writeJSON(http.StatusNotFound, map[string]any{"error": "unsupported path"})
		return
	}
}

func matchesFilter(payload map[string]any, filter map[string]any) bool {
	if len(filter) == 0 {
		return true
	}
	must, ok := filter["must"].([]any)
	if !ok {
		if typed, ok := filter["must"].([]map[string]any); ok {
			for _, condition := range typed {
				if !matchCondition(payload, condition) {
					return false
				}
			}
			return true
		}
		return true
	}

	for _, item := range must {
		condition, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if !matchCondition(payload, condition) {
			return false
		}
	}
	return true
}

func matchCondition(payload map[string]any, condition map[string]any) bool {
	key, _ := condition["key"].(string)
	match, _ := condition["match"].(map[string]any)
	value := fmt.Sprint(match["value"])
	return fmt.Sprint(payload[key]) == value
}

func handleModelAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.URL.Path {
	case "/embeddings":
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		response := embeddingTestResponse{}
		for index := range req.Input {
			item := struct {
				Embedding []float64 `json:"embedding"`
				Index     int       `json:"index"`
			}{
				Embedding: []float64{1, 0, 0, 0, 0, 0, 0, 0},
				Index:     index,
			}
			response.Data = append(response.Data, item)
		}
		_ = json.NewEncoder(w).Encode(response)
	// Ollama native embedding
	case "/api/embed":
		var embedReq struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&embedReq)
		embeddings := make([][]float64, len(embedReq.Input))
		for i := range embedReq.Input {
			embeddings[i] = []float64{1, 0, 0, 0, 0, 0, 0, 0}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": embeddings})
	// OpenAI-compatible chat
	case "/chat/completions":
		body, _ := io.ReadAll(r.Body)
		content := "已基于检索上下文回答：Redis 是高性能内存数据库。"
		if bytes.Contains(body, []byte("这个文档有多少名员工")) && bytes.Contains(body, []byte("数据行数：4")) {
			content = "该文档中共有 4 名员工。\n统计依据：按表头下方的数据行统计，共 4 条员工记录。"
		} else if !bytes.Contains(body, []byte("Redis")) {
			content = "已收到请求，但未检测到上下文。"
		}
		response := chatTestResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 1,
			Model:   "chat-test-model",
			Choices: []struct {
				Index   int `json:"index"`
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
			}{
				{
					Index: 0,
					Message: struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					}{
						Role:    "assistant",
						Content: content,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(response)
	// Ollama native chat
	case "/api/chat":
		body, _ := io.ReadAll(r.Body)
		content := "已基于检索上下文回答：Redis 是高性能内存数据库。"
		if bytes.Contains(body, []byte("这个文档有多少名员工")) && bytes.Contains(body, []byte("数据行数：4")) {
			content = "该文档中共有 4 名员工。\n统计依据：按表头下方的数据行统计，共 4 条员工记录。"
		} else if !bytes.Contains(body, []byte("Redis")) {
			content = "已收到请求，但未检测到上下文。"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "chat-test-model",
			"message": map[string]any{
				"role":    "assistant",
				"content": content,
			},
			"done": true,
		})
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "not found"}})
	}
}

func performJSONRequest(t *testing.T, handler http.Handler, method, target string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal json request: %v", err)
	}
	return performRequestWithHeaders(t, handler, method, target, bytes.NewReader(body), "application/json", nil)
}

func performMultipartUpload(t *testing.T, handler http.Handler, method, target, filename, content string) *httptest.ResponseRecorder {
	t.Helper()
	return performMultipartUploadWithHeaders(t, handler, method, target, filename, content, nil)
}

func performMultipartUploadWithHeaders(t *testing.T, handler http.Handler, method, target, filename, content string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fileWriter, err := writer.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := fileWriter.Write([]byte(content)); err != nil {
		t.Fatalf("write multipart content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return performRequestWithHeaders(t, handler, method, target, body, writer.FormDataContentType(), headers)
}

func performRequest(t *testing.T, handler http.Handler, method, target string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	return performRequestWithHeaders(t, handler, method, target, body, contentType, nil)
}

func performRequestWithHeaders(t *testing.T, handler http.Handler, method, target string, body io.Reader, contentType string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func findResponseCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func mustMarshalJSON(t *testing.T, payload any) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal json payload: %v", err)
	}
	return body
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func decodeJSONResponse(t *testing.T, body []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode json response: %v, body=%s", err, string(body))
	}
}

func assertArraySchemaHasItems(t *testing.T, schemaName string, schema map[string]any) {
	t.Helper()

	schemaType, _ := schema["type"].(string)
	if schemaType == "array" {
		items, ok := schema["items"]
		if !ok {
			t.Fatalf("expected schema %s to include items for array type", schemaName)
		}
		if items == nil {
			t.Fatalf("expected schema %s items to be non-nil", schemaName)
		}
		itemSchema, ok := items.(map[string]any)
		if !ok {
			t.Fatalf("expected schema %s items to be object schema, got %T", schemaName, items)
		}
		assertArraySchemaHasItems(t, schemaName+"[]", itemSchema)
	}

	properties, _ := schema["properties"].(map[string]any)
	for propertyName, propertyValue := range properties {
		propertySchema, ok := propertyValue.(map[string]any)
		if !ok {
			continue
		}
		assertArraySchemaHasItems(t, schemaName+"."+propertyName, propertySchema)
	}
}
