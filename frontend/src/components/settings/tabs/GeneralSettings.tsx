import React from 'react'
import type { AppConfig } from '../../../App'

interface GeneralSettingsProps {
  config: AppConfig
}

const GeneralSettings: React.FC<GeneralSettingsProps> = ({ config }) => {
  const chatProviderLabel = config.chat.provider === 'ollama' ? 'Ollama' : 'OpenAI Compatible'
  const embeddingProviderLabel = config.embedding.provider === 'ollama' ? 'Ollama' : 'OpenAI Compatible'

  return (
    <div className="settings-tab-content">
      <section className="settings-card">
        <div className="settings-card-header">
          <h3>应用信息</h3>
          <p>查看当前系统的基本配置状态</p>
        </div>
        <div className="settings-card-body">
          <div className="settings-info-row">
            <span className="settings-info-label">聊天 Provider</span>
            <span className="settings-info-value">{chatProviderLabel}</span>
          </div>
          <div className="settings-info-row">
            <span className="settings-info-label">Embedding Provider</span>
            <span className="settings-info-value">{embeddingProviderLabel}</span>
          </div>
          <div className="settings-info-row">
            <span className="settings-info-label">检索策略</span>
            <span className="settings-info-value">{config.retrieval.rerankStrategy === 'semantic' ? '语义重排' : '关键词融合'}</span>
          </div>
          <div className="settings-info-row">
            <span className="settings-info-label">MCP 状态</span>
            <span className={`settings-mcp-status ${config.mcp.enabled ? 'enabled' : 'disabled'}`}>
              {config.mcp.enabled ? '已启用' : '未启用'}
            </span>
          </div>
        </div>
      </section>
    </div>
  )
}

export default GeneralSettings
