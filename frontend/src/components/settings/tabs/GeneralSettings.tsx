import React from 'react'
import type { AppConfig } from '../../../App'

interface GeneralSettingsProps {
  config: AppConfig
}

interface PreferenceRow {
  title: string
  description: string
  value: string
  meta?: string
  status?: 'enabled' | 'disabled' | 'neutral'
}

const GeneralSettings: React.FC<GeneralSettingsProps> = ({ config }) => {
  const chatProviderLabel = config.chat.provider === 'ollama' ? 'Ollama' : 'OpenAI Compatible'
  const embeddingProviderLabel = config.embedding.provider === 'ollama' ? 'Ollama' : 'OpenAI Compatible'
  const searchModeLabel = config.retrieval.defaultSearchMode === 'hybrid' ? '混合检索' : '向量检索'
  const rerankLabel = config.retrieval.rerankStrategy === 'semantic' ? '语义重排' : '关键词融合'
  const queryRewriteLabel = config.retrieval.enableQueryRewrite ? '已启用' : '未启用'
  const lowConfidenceBoostLabel = config.retrieval.enableLowConfidenceBoost ? '已启用' : '未启用'
  const mcpWarnings = config.mcp.deploymentWarnings ?? []

  const preferenceRows: PreferenceRow[] = [
    {
      title: '聊天模型',
      description: `${chatProviderLabel} · ${config.chat.baseUrl || '未配置 Base URL'}`,
      value: config.chat.model || '未配置',
      meta: `上下文 ${config.chat.contextMessageLimit} 条`,
      status: config.chat.model ? 'neutral' : 'disabled',
    },
    {
      title: 'Embedding 模型',
      description: `${embeddingProviderLabel} · ${config.embedding.baseUrl || '未配置 Base URL'}`,
      value: config.embedding.model || '未配置',
      status: config.embedding.model ? 'neutral' : 'disabled',
    },
    {
      title: '默认检索模式',
      description: `${rerankLabel} · ${queryRewriteLabel}问题改写 · ${lowConfidenceBoostLabel}低置信补强`,
      value: searchModeLabel,
      meta: config.retrieval.hybridSearchEnabled ? '混合召回可用' : '向量召回优先',
      status: config.retrieval.hybridSearchEnabled ? 'enabled' : 'neutral',
    },
    {
      title: '召回规模',
      description: `文档 TopK ${config.retrieval.topKDocument}，知识库 TopK ${config.retrieval.topKKnowledgeBase}，每文档 ${config.retrieval.maxChunksPerDocument} 个片段。`,
      value: `${config.retrieval.maxContextChars}`,
      meta: '上下文字符',
    },
    {
      title: 'MCP 服务',
      description: config.mcp.basePath || '未配置 MCP 路径',
      value: config.mcp.enabled ? '已启用' : '未启用',
      meta: config.mcp.tokenConfigured ? '迁移 Token 已生成' : '迁移 Token 未生成',
      status: config.mcp.enabled ? 'enabled' : 'disabled',
    },
  ]

  return (
    <div className="settings-tab-content settings-preferences-page">
      {mcpWarnings.length > 0 && (
        <section className="settings-preference-row settings-preference-warning" aria-label="部署提醒">
          <div className="settings-preference-copy">
            <h3>部署提醒</h3>
            <p>{mcpWarnings.join('；')}</p>
          </div>
          <div className="settings-preference-control">
            <span className="settings-select-like settings-status-like disabled">需处理</span>
            <small>{config.mcp.recommendedAuthMode === 'api_key_scopes' ? 'MCP 建议继续使用 API Key Scope' : '检查鉴权配置'}</small>
          </div>
        </section>
      )}
      {preferenceRows.map((row) => (
        <section className="settings-preference-row" key={row.title}>
          <div className="settings-preference-copy">
            <h3>{row.title}</h3>
            <p>{row.description}</p>
          </div>
          <div className="settings-preference-control">
            {row.status ? (
              <span className={`settings-select-like settings-status-like ${row.status}`}>
                {row.value}
              </span>
            ) : (
              <span className="settings-select-like">{row.value}</span>
            )}
            {row.meta ? <small>{row.meta}</small> : null}
          </div>
        </section>
      ))}
    </div>
  )
}

export default GeneralSettings
