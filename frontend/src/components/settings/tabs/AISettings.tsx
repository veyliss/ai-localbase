import React from 'react'
import type { AppConfig, ChatConfig, ChatModeSettings, EmbeddingConfig } from '../../../App'
import { ModelConfigTest } from '../ModelConfigProbe'

interface AISettingsProps {
  config: AppConfig
  onChatConfigChange: <K extends keyof ChatConfig>(key: K, value: ChatConfig[K]) => void
  onEmbeddingConfigChange: <K extends keyof EmbeddingConfig>(
    key: K,
    value: EmbeddingConfig[K],
  ) => void
  chatModeSettings: ChatModeSettings
  onThinkModelChange: (value: string) => void
}

const AISettings: React.FC<AISettingsProps> = ({
  config,
  onChatConfigChange,
  onEmbeddingConfigChange,
  chatModeSettings,
  onThinkModelChange,
}) => {
  return (
    <div className="settings-tab-content">
      <section className="settings-card">
        <div className="settings-card-header">
          <h3>聊天模型</h3>
          <p>配置对话使用的 AI 模型参数</p>
        </div>
        <div className="settings-card-body">
          <div className="settings-form-grid">
            <div className="settings-form-group">
              <label className="settings-form-label">Provider</label>
              <select
                value={config.chat.provider}
                onChange={(event) => onChatConfigChange('provider', event.target.value as ChatConfig['provider'])}
              >
                <option value="ollama">Ollama</option>
                <option value="openai-compatible">OpenAI Compatible</option>
              </select>
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">Base URL</label>
              <input
                type="text"
                value={config.chat.baseUrl}
                onChange={(event) => onChatConfigChange('baseUrl', event.target.value)}
                placeholder={config.chat.provider === 'ollama' ? 'http://localhost:11434' : 'http://localhost:11434/v1'}
              />
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">Model</label>
              <input
                type="text"
                value={config.chat.model}
                onChange={(event) => onChatConfigChange('model', event.target.value)}
                placeholder="llama3.2"
              />
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">API Key</label>
              <input
                type="password"
                value={config.chat.apiKey}
                onChange={(event) => onChatConfigChange('apiKey', event.target.value)}
                placeholder="选填"
              />
            </div>
            <div className="settings-form-group settings-form-group-full">
              <label className="settings-form-label">Temperature: {config.chat.temperature.toFixed(1)}</label>
              <input
                type="range"
                min="0"
                max="1"
                step="0.1"
                value={config.chat.temperature}
                onChange={(event) => onChatConfigChange('temperature', Number(event.target.value))}
              />
            </div>
            <div className="settings-form-group settings-form-group-full">
              <label className="settings-form-label">上下文消息数量</label>
              <input
                type="number"
                min="1"
                max="100"
                value={config.chat.contextMessageLimit}
                onChange={(event) => onChatConfigChange('contextMessageLimit', Number(event.target.value))}
                placeholder="12"
              />
              <small>限制每次发送给模型的最近消息条数，范围 1-100。</small>
            </div>
            <div className="settings-form-group settings-form-group-full">
              <ModelConfigTest
                type="chat"
                provider={config.chat.provider}
                baseUrl={config.chat.baseUrl}
                modelName={config.chat.model}
                apiKey={config.chat.apiKey}
                temperature={config.chat.temperature}
              />
            </div>
            <div className="settings-form-group settings-form-group-full">
              <label className="settings-form-label">思考模式模型</label>
              <input
                type="text"
                value={chatModeSettings.thinkModel}
                onChange={(event) => onThinkModelChange(event.target.value)}
                placeholder="deepseek-r1:8b"
              />
              <small>用于"思考模式"的专用模型，建议填写推理更强但更慢的模型。</small>
            </div>
          </div>
        </div>
      </section>

      <section className="settings-card">
        <div className="settings-card-header">
          <h3>Embedding 模型</h3>
          <p>配置文档索引和语义召回使用的向量模型</p>
        </div>
        <div className="settings-card-body">
          <div className="settings-form-grid">
            <div className="settings-form-group">
              <label className="settings-form-label">Provider</label>
              <select
                value={config.embedding.provider}
                onChange={(event) => onEmbeddingConfigChange('provider', event.target.value as EmbeddingConfig['provider'])}
              >
                <option value="ollama">Ollama</option>
                <option value="openai-compatible">OpenAI Compatible</option>
              </select>
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">Base URL</label>
              <input
                type="text"
                value={config.embedding.baseUrl}
                onChange={(event) => onEmbeddingConfigChange('baseUrl', event.target.value)}
                placeholder={config.embedding.provider === 'ollama' ? 'http://localhost:11434' : 'http://localhost:11434/v1'}
              />
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">Model</label>
              <input
                type="text"
                value={config.embedding.model}
                onChange={(event) => onEmbeddingConfigChange('model', event.target.value)}
                placeholder="nomic-embed-text"
              />
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">API Key</label>
              <input
                type="password"
                value={config.embedding.apiKey}
                onChange={(event) => onEmbeddingConfigChange('apiKey', event.target.value)}
                placeholder="选填"
              />
            </div>
            <div className="settings-form-group settings-form-group-full">
              <ModelConfigTest
                type="embedding"
                provider={config.embedding.provider}
                baseUrl={config.embedding.baseUrl}
                modelName={config.embedding.model}
                apiKey={config.embedding.apiKey}
              />
            </div>
          </div>
        </div>
      </section>
    </div>
  )
}

export default AISettings