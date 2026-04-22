package model

import "sync"

type ServerConfig struct {
	Port                     string
	UploadDir                string
	StateFile                string
	ChatHistoryFile          string
	QdrantURL                string
	QdrantAPIKey             string
	QdrantCollectionPrefix   string
	QdrantVectorSize         int
	QdrantDistance           string
	QdrantTimeoutSeconds     int
	EnableHybridSearch       bool
	EnableSemanticReranker   bool
	EnableQueryRewrite       bool
	EnableSemanticCache      bool
	EnableContextCompression bool
	OllamaBaseURL            string
	EnableMCP                bool
	MCPBasePath              string
	MCPRequestTimeoutSeconds int
	MCPRequestsPerMinute     int
}

type AppState struct {
	Mu             sync.RWMutex
	Config         AppConfig
	KnowledgeBases map[string]KnowledgeBase
}

type HealthResponse struct {
	Status string            `json:"status"`
	Name   string            `json:"name"`
	Config map[string]string `json:"config"`
}

type ChatConfig struct {
	Provider            string  `json:"provider"`
	BaseURL             string  `json:"baseUrl"`
	Model               string  `json:"model"`
	APIKey              string  `json:"apiKey"`
	Temperature         float64 `json:"temperature"`
	ContextMessageLimit int     `json:"contextMessageLimit"`
}

type EmbeddingConfig struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
}

type MCPConfig struct {
	Enabled  bool   `json:"enabled"`
	BasePath string `json:"basePath"`
	Token    string `json:"token"`
}

type AppConfig struct {
	Chat      ChatConfig      `json:"chat"`
	Embedding EmbeddingConfig `json:"embedding"`
	MCP       MCPConfig       `json:"mcp"`
}

type KnowledgeBase struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Documents   []Document `json:"documents"`
	CreatedAt   string     `json:"createdAt"`
}

type KnowledgeBaseInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Document struct {
	ID              string `json:"id"`
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	Name            string `json:"name"`
	Size            int64  `json:"size"`
	SizeLabel       string `json:"sizeLabel"`
	UploadedAt      string `json:"uploadedAt"`
	Status          string `json:"status"`
	Path            string `json:"path"`
	ContentPreview  string `json:"contentPreview"`
}

type UploadResponse struct {
	Message       string   `json:"message"`
	KnowledgeBase string   `json:"knowledgeBaseId"`
	Uploaded      Document `json:"uploaded"`
}

type StagedUpload struct {
	ID         string `json:"id"`
	FileName   string `json:"fileName"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	SizeLabel  string `json:"sizeLabel"`
	SHA256     string `json:"sha256"`
	CreatedAt  string `json:"createdAt"`
	ExpiresAt  string `json:"expiresAt"`
	Status     string `json:"status"`
	Source     string `json:"source,omitempty"`
	ConsumedAt string `json:"consumedAt,omitempty"`
}

type StageUploadResponse struct {
	Message  string       `json:"message"`
	Staged   StagedUpload `json:"staged"`
	UploadID string       `json:"uploadId"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatModelConfig struct {
	Provider            string  `json:"provider"`
	BaseURL             string  `json:"baseUrl"`
	Model               string  `json:"model"`
	APIKey              string  `json:"apiKey"`
	Temperature         float64 `json:"temperature"`
	ContextMessageLimit int     `json:"contextMessageLimit"`
}

type EmbeddingModelConfig struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
}

type ChatCompletionRequest struct {
	ConversationID  string               `json:"conversationId"`
	Model           string               `json:"model"`
	Messages        []ChatMessage        `json:"messages"`
	KnowledgeBaseID string               `json:"knowledgeBaseId"`
	DocumentID      string               `json:"documentId"`
	Config          ChatModelConfig      `json:"config"`
	Embedding       EmbeddingModelConfig `json:"embedding"`
}

type ChatCompletionChoice struct {
	Index   int         `json:"index"`
	Message ChatMessage `json:"message"`
}

type ChatCompletionResponse struct {
	ID       string                 `json:"id"`
	Object   string                 `json:"object"`
	Created  int64                  `json:"created"`
	Model    string                 `json:"model"`
	Choices  []ChatCompletionChoice `json:"choices"`
	Metadata map[string]any         `json:"metadata"`
}

type ToolUseMetadata struct {
	ToolName        string         `json:"toolName"`
	Reason          string         `json:"reason"`
	PermissionLevel string         `json:"permissionLevel"`
	Arguments       map[string]any `json:"arguments,omitempty"`
	Data            map[string]any `json:"data,omitempty"`
	IsError         bool           `json:"isError,omitempty"`
	Error           string         `json:"error,omitempty"`
}

type ConfigUpdateRequest struct {
	Chat      ChatConfig      `json:"chat"`
	Embedding EmbeddingConfig `json:"embedding"`
	MCP       MCPConfig       `json:"mcp"`
}

type Conversation struct {
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	KnowledgeBaseID string              `json:"knowledgeBaseId"`
	DocumentID      string              `json:"documentId"`
	CreatedAt       string              `json:"createdAt"`
	UpdatedAt       string              `json:"updatedAt"`
	Messages        []StoredChatMessage `json:"messages"`
}

type StoredChatMessage struct {
	ID        string         `json:"id"`
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	CreatedAt string         `json:"createdAt"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type ConversationListItem struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	DocumentID      string `json:"documentId"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	MessageCount    int    `json:"messageCount"`
}

type SaveConversationRequest struct {
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	KnowledgeBaseID string              `json:"knowledgeBaseId"`
	DocumentID      string              `json:"documentId"`
	Messages        []StoredChatMessage `json:"messages"`
}

type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

type APIError struct {
	Error ErrorDetail `json:"error"`
}
