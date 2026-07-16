import React, { useState } from 'react'
import type { AppConfig, ChatConfig, ChatModeSettings, EmbeddingConfig } from '../../../App'
import AppIcon from '../../common/AppIcon'
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

type ModelSection = 'chat' | 'embedding'

const AISettings: React.FC<AISettingsProps> = ({
  config,
  onChatConfigChange,
  onEmbeddingConfigChange,
  chatModeSettings,
  onThinkModelChange,
}) => {
  const [activeSection, setActiveSection] = useState<ModelSection>('chat')

  return (
    <div className="settings-tab-content settings-models-page">
      <div className="settings-subnav" aria-label="模型类型" role="tablist">
        <button
          aria-controls="settings-model-panel-chat"
          aria-selected={activeSection === 'chat'}
          className={activeSection === 'chat' ? 'active' : ''}
          id="settings-model-tab-chat"
          onClick={() => setActiveSection('chat')}
          role="tab"
          type="button"
        >
          <AppIcon name="message" size={16} />
          <span>聊天模型</span>
        </button>
        <button
          aria-controls="settings-model-panel-embedding"
          aria-selected={activeSection === 'embedding'}
          className={activeSection === 'embedding' ? 'active' : ''}
          id="settings-model-tab-embedding"
          onClick={() => setActiveSection('embedding')}
          role="tab"
          type="button"
        >
          <AppIcon name="database" size={16} />
          <span>Embedding</span>
        </button>
      </div>

      {activeSection === 'chat' ? (
        <section
          aria-labelledby="settings-model-tab-chat"
          className="settings-config-panel"
          id="settings-model-panel-chat"
          role="tabpanel"
        >
          <header className="settings-config-panel-header">
            <div>
              <span className="settings-config-panel-icon" aria-hidden="true">
                <AppIcon name="brain" size={18} />
              </span>
              <div>
                <h3>聊天与推理</h3>
                <p>配置对话生成、上下文窗口和思考模式模型。</p>
              </div>
            </div>
            <span className={`settings-compact-status ${config.chat.model ? 'enabled' : 'disabled'}`}>
              {config.chat.model ? '已配置' : '待配置'}
            </span>
          </header>

          <section className="settings-form-section">
            <header>
              <h4>连接</h4>
              <p>模型服务地址和身份凭据。</p>
            </header>
            <div className="settings-form-grid settings-form-grid-dense">
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="chat-provider">Provider</label>
                <select
                  id="chat-provider"
                  value={config.chat.provider}
                  onChange={(event) => onChatConfigChange('provider', event.target.value as ChatConfig['provider'])}
                >
                  <option value="ollama">Ollama</option>
                  <option value="openai-compatible">OpenAI Compatible</option>
                </select>
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="chat-base-url">Base URL</label>
                <input
                  id="chat-base-url"
                  type="text"
                  value={config.chat.baseUrl}
                  onChange={(event) => onChatConfigChange('baseUrl', event.target.value)}
                  placeholder={config.chat.provider === 'ollama' ? 'http://localhost:11434' : 'http://localhost:11434/v1'}
                />
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="chat-model">Model</label>
                <input
                  id="chat-model"
                  type="text"
                  value={config.chat.model}
                  onChange={(event) => onChatConfigChange('model', event.target.value)}
                  placeholder="llama3.2"
                />
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="chat-api-key">API Key</label>
                <div className="settings-secret-input-row">
                  <input
                    id="chat-api-key"
                    type="password"
                    value={config.chat.apiKey}
                    onChange={(event) => onChatConfigChange('apiKey', event.target.value)}
                    placeholder={config.chat.apiKeyConfigured ? '已配置，输入新密钥覆盖' : '选填'}
                  />
                  {(config.chat.apiKeyConfigured || config.chat.apiKey) && (
                    <button
                      type="button"
                      className="settings-action-btn"
                      onClick={() => onChatConfigChange('clearApiKey', true)}
                    >
                      清除
                    </button>
                  )}
                </div>
                {config.chat.apiKeyConfigured && !config.chat.apiKey && (
                  <small>密钥已保存在后端，页面不会显示明文。</small>
                )}
              </div>
            </div>
          </section>

          <section className="settings-form-section">
            <header>
              <h4>生成</h4>
              <p>控制回答随机性、上下文规模和思考模式。</p>
            </header>
            <div className="settings-form-grid settings-form-grid-dense">
              <div className="settings-form-group settings-form-group-full">
                <label className="settings-form-label settings-form-label-inline" htmlFor="chat-temperature">
                  <span>Temperature</span>
                  <strong>{config.chat.temperature.toFixed(1)}</strong>
                </label>
                <input
                  id="chat-temperature"
                  type="range"
                  min="0"
                  max="1"
                  step="0.1"
                  value={config.chat.temperature}
                  onChange={(event) => onChatConfigChange('temperature', Number(event.target.value))}
                />
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="chat-context-limit">上下文消息数量</label>
                <input
                  id="chat-context-limit"
                  type="number"
                  min="1"
                  max="100"
                  value={config.chat.contextMessageLimit}
                  onChange={(event) => onChatConfigChange('contextMessageLimit', Number(event.target.value))}
                />
                <small>每次发送给模型的最近消息条数，范围 1-100。</small>
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="think-model">思考模式模型</label>
                <input
                  id="think-model"
                  type="text"
                  value={chatModeSettings.thinkModel}
                  onChange={(event) => onThinkModelChange(event.target.value)}
                  placeholder="deepseek-r1:8b"
                />
                <small>留空时使用聊天模型。</small>
              </div>
            </div>
          </section>

          <div className="settings-test-row">
            <div>
              <strong>连接测试</strong>
              <span>使用当前草稿验证地址、模型和凭据。</span>
            </div>
            <ModelConfigTest
              type="chat"
              provider={config.chat.provider}
              baseUrl={config.chat.baseUrl}
              modelName={config.chat.model}
              apiKey={config.chat.apiKey}
              temperature={config.chat.temperature}
            />
          </div>
        </section>
      ) : (
        <section
          aria-labelledby="settings-model-tab-embedding"
          className="settings-config-panel"
          id="settings-model-panel-embedding"
          role="tabpanel"
        >
          <header className="settings-config-panel-header">
            <div>
              <span className="settings-config-panel-icon" aria-hidden="true">
                <AppIcon name="database" size={18} />
              </span>
              <div>
                <h3>Embedding</h3>
                <p>配置文档索引和语义召回使用的向量模型。</p>
              </div>
            </div>
            <span className={`settings-compact-status ${config.embedding.model ? 'enabled' : 'disabled'}`}>
              {config.embedding.model ? '已配置' : '待配置'}
            </span>
          </header>

          <section className="settings-form-section">
            <header>
              <h4>连接</h4>
              <p>Embedding 服务地址、模型和凭据。</p>
            </header>
            <div className="settings-form-grid settings-form-grid-dense">
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="embedding-provider">Provider</label>
                <select
                  id="embedding-provider"
                  value={config.embedding.provider}
                  onChange={(event) => onEmbeddingConfigChange('provider', event.target.value as EmbeddingConfig['provider'])}
                >
                  <option value="ollama">Ollama</option>
                  <option value="openai-compatible">OpenAI Compatible</option>
                </select>
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="embedding-base-url">Base URL</label>
                <input
                  id="embedding-base-url"
                  type="text"
                  value={config.embedding.baseUrl}
                  onChange={(event) => onEmbeddingConfigChange('baseUrl', event.target.value)}
                  placeholder={config.embedding.provider === 'ollama' ? 'http://localhost:11434' : 'http://localhost:11434/v1'}
                />
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="embedding-model">Model</label>
                <input
                  id="embedding-model"
                  type="text"
                  value={config.embedding.model}
                  onChange={(event) => onEmbeddingConfigChange('model', event.target.value)}
                  placeholder="nomic-embed-text"
                />
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="embedding-api-key">API Key</label>
                <div className="settings-secret-input-row">
                  <input
                    id="embedding-api-key"
                    type="password"
                    value={config.embedding.apiKey}
                    onChange={(event) => onEmbeddingConfigChange('apiKey', event.target.value)}
                    placeholder={config.embedding.apiKeyConfigured ? '已配置，输入新密钥覆盖' : '选填'}
                  />
                  {(config.embedding.apiKeyConfigured || config.embedding.apiKey) && (
                    <button
                      type="button"
                      className="settings-action-btn"
                      onClick={() => onEmbeddingConfigChange('clearApiKey', true)}
                    >
                      清除
                    </button>
                  )}
                </div>
                {config.embedding.apiKeyConfigured && !config.embedding.apiKey && (
                  <small>密钥已保存在后端，页面不会显示明文。</small>
                )}
              </div>
            </div>
          </section>

          <div className="settings-test-row">
            <div>
              <strong>连接测试</strong>
              <span>确认当前草稿可以完成向量请求。</span>
            </div>
            <ModelConfigTest
              type="embedding"
              provider={config.embedding.provider}
              baseUrl={config.embedding.baseUrl}
              modelName={config.embedding.model}
              apiKey={config.embedding.apiKey}
            />
          </div>
        </section>
      )}
    </div>
  )
}

export default AISettings
