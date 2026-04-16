package config

import (
	"os"
	"strconv"
	"strings"

	"ai-localbase/internal/model"
)

func LoadServerConfig() model.ServerConfig {
	return model.ServerConfig{
		Port:                     getEnv("PORT", "8080"),
		UploadDir:                getEnv("UPLOAD_DIR", "data/uploads"),
		StateFile:                getEnv("STATE_FILE", "data/app-state.json"),
		ChatHistoryFile:          getEnv("CHAT_HISTORY_FILE", "data/chat-history.db"),
		QdrantURL:                getEnv("QDRANT_URL", "http://localhost:6333"),
		QdrantAPIKey:             getEnv("QDRANT_API_KEY", ""),
		QdrantCollectionPrefix:   getEnv("QDRANT_COLLECTION_PREFIX", "kb_"),
		QdrantVectorSize:         getEnvAsInt("QDRANT_VECTOR_SIZE", 1024),
		QdrantDistance:           getEnv("QDRANT_DISTANCE", "Cosine"),
		QdrantTimeoutSeconds:     getEnvAsInt("QDRANT_TIMEOUT_SECONDS", 5),
		EnableHybridSearch:       getEnvAsBool("ENABLE_HYBRID_SEARCH", false),
		EnableSemanticReranker:   getEnvAsBool("ENABLE_SEMANTIC_RERANKER", false),
		EnableQueryRewrite:       getEnvAsBool("ENABLE_QUERY_REWRITE", false),
		EnableSemanticCache:      getEnvAsBool("ENABLE_SEMANTIC_CACHE", false),
		EnableContextCompression: getEnvAsBool("ENABLE_CONTEXT_COMPRESSION", false),
		OllamaBaseURL:            getEnv("OLLAMA_BASE_URL", "http://localhost:11434"),
		EnableMCP:                getEnvAsBool("ENABLE_MCP", true),
		MCPBasePath:              getEnv("MCP_BASE_PATH", "/mcp"),
		MCPRequestTimeoutSeconds: getEnvAsInt("MCP_REQUEST_TIMEOUT_SECONDS", 15),
		MCPRequestsPerMinute:     getEnvAsInt("MCP_REQUESTS_PER_MINUTE", 120),
	}
}

func getEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func getEnvAsBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}

	switch value {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return fallback
	}
}
