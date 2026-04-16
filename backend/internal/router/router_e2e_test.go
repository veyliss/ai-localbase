package router

import (
	"bytes"
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
			"baseUrl":     "http://chat.local/v1",
			"model":       "llama3.2",
			"apiKey":      "",
			"temperature": 0.4,
		},
		"embedding": map[string]any{
			"provider": "openai-compatible",
			"baseUrl":  "http://embed.local/v1",
			"model":    "bge-m3",
			"apiKey":   "embed-key",
		},
	}

	resp := performJSONRequest(t, engine, http.MethodPut, "/api/config", updatePayload)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", resp.Code, resp.Body.String())
	}

	var updated model.AppConfig
	decodeJSONResponse(t, resp.Body.Bytes(), &updated)
	if updated.Chat.BaseURL != "http://chat.local/v1" {
		t.Fatalf("expected chat baseUrl to be updated, got %s", updated.Chat.BaseURL)
	}
	if updated.Embedding.Model != "bge-m3" {
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
	if fetched.Embedding.APIKey != "embed-key" {
		t.Fatalf("expected persisted embedding apiKey, got %s", fetched.Embedding.APIKey)
	}
}

func TestMCPRequiresAuthorizationHeader(t *testing.T) {
	engine, _, cleanup := newTestRouter(t)
	defer cleanup()

	resp := performRequest(t, engine, http.MethodGet, "/mcp", nil, "")
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d, body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "missing authorization header") {
		t.Fatalf("expected missing authorization error, got %s", resp.Body.String())
	}
}

func TestMCPToolsListAndCreateKnowledgeBase(t *testing.T) {
	engine, _, cleanup := newTestRouter(t)
	defer cleanup()

	configResp := performRequest(t, engine, http.MethodGet, "/api/config", nil, "")
	if configResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", configResp.Code, configResp.Body.String())
	}
	var cfg model.AppConfig
	decodeJSONResponse(t, configResp.Body.Bytes(), &cfg)
	if strings.TrimSpace(cfg.MCP.Token) == "" {
		t.Fatal("expected mcp token to be populated")
	}

	headers := map[string]string{"Authorization": "Bearer " + cfg.MCP.Token}

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
	if !containsString(toolNames, "delete_knowledge_base") {
		t.Fatalf("expected delete_knowledge_base in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "delete_document") {
		t.Fatalf("expected delete_document in tools list, got %v", toolNames)
	}
	if !containsString(toolNames, "upload_text_document") {
		t.Fatalf("expected upload_text_document in tools list, got %v", toolNames)
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

	kbListResp := performRequest(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "")
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
	engine, _, cleanup := newTestRouter(t)
	defer cleanup()

	configResp := performRequest(t, engine, http.MethodGet, "/api/config", nil, "")
	if configResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", configResp.Code, configResp.Body.String())
	}
	var cfg model.AppConfig
	decodeJSONResponse(t, configResp.Body.Bytes(), &cfg)
	headers := map[string]string{"Authorization": "Bearer " + cfg.MCP.Token}

	kbListResp := performRequest(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "")
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
					"fileName":        "whu-intro.txt",
					"content":         "武汉大学是教育部直属重点综合性大学。",
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
	if rpcResp.Result.Data.Uploaded.Name != "whu-intro.txt" {
		t.Fatalf("expected uploaded text document, got %+v", rpcResp.Result.Data.Uploaded)
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
		"redis-tooluse.md",
		"# Redis\n\nRedis 是高性能内存数据库，支持缓存、消息队列与持久化。",
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
			"content": "请介绍 Redis 的主要用途",
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

func TestMCPDangerToolsDeleteKnowledgeBaseAndDocument(t *testing.T) {
	engine, _, cleanup := newTestRouter(t)
	defer cleanup()

	configResp := performRequest(t, engine, http.MethodGet, "/api/config", nil, "")
	if configResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", configResp.Code, configResp.Body.String())
	}
	var cfg model.AppConfig
	decodeJSONResponse(t, configResp.Body.Bytes(), &cfg)
	headers := map[string]string{"Authorization": "Bearer " + cfg.MCP.Token}
	dangerHeaders := map[string]string{
		"Authorization": "Bearer " + cfg.MCP.Token,
		"X-MCP-Confirm": cfg.MCP.Token,
	}

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
					"name":        "危险工具测试知识库",
					"description": "用于删除测试",
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

	uploadResp := performMultipartUpload(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/knowledge-bases/%s/documents", knowledgeBaseID),
		"danger-tool-doc.md",
		"# 删除测试\n\n用于验证 MCP 删除文档工具。",
	)
	if uploadResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", uploadResp.Code, uploadResp.Body.String())
	}
	var uploadResult model.UploadResponse
	decodeJSONResponse(t, uploadResp.Body.Bytes(), &uploadResult)

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
				"name": "delete_document",
				"arguments": map[string]any{
					"knowledgeBaseId": knowledgeBaseID,
					"documentId":      uploadResult.Uploaded.ID,
				},
			},
		})),
		"application/json",
		dangerHeaders,
	)
	if delDocResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", delDocResp.Code, delDocResp.Body.String())
	}

	docListResp := performRequest(t, engine, http.MethodGet, fmt.Sprintf("/api/knowledge-bases/%s/documents", knowledgeBaseID), nil, "")
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
				"name": "delete_knowledge_base",
				"arguments": map[string]any{
					"knowledgeBaseId": knowledgeBaseID,
				},
			},
		})),
		"application/json",
		dangerHeaders,
	)
	if delKBResp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", delKBResp.Code, delKBResp.Body.String())
	}

	kbListResp := performRequest(t, engine, http.MethodGet, "/api/knowledge-bases", nil, "")
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
			"baseUrl":     "http://chat.remote/v1",
			"model":       "gpt-test",
			"apiKey":      "chat-key",
			"temperature": 0.4,
		},
		"embedding": map[string]any{
			"provider": "openai-compatible",
			"baseUrl":  "http://embed.remote/v1",
			"model":    "bge-m3",
			"apiKey":   "embed-key",
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
		"sensitive.csv",
		"姓名,部门\n张三,销售部\n",
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
	t.Helper()

	uploadDir := t.TempDir()
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
		QdrantURL:              qdrantHTTP.URL,
		QdrantCollectionPrefix: "kb_",
		QdrantVectorSize:       8,
		QdrantDistance:         "Cosine",
		QdrantTimeoutSeconds:   5,
		EnableMCP:              true,
		MCPBasePath:            "/mcp",
	}

	qdrantService := service.NewQdrantService(serverConfig)
	appService := service.NewAppService(qdrantService, service.NewAppStateStore(""), chatHistoryStore, serverConfig)
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
	})
	if err != nil {
		t.Fatalf("update config: %v", err)
	}

	mcpRegistry := mcp.DefaultRegistry(appService)
	toolPlanner := mcp.NewToolUsePlanner(mcpRegistry)
	appHandler := handler.NewAppHandler(serverConfig, appService, service.NewLLMService(), toolPlanner)
	mcpServer := mcp.NewServer(mcpRegistry, appService, serverConfig)
	ginEngine := NewRouter(appHandler, serverConfig, mcpServer)

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
	return performRequest(t, handler, method, target, body, writer.FormDataContentType())
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
