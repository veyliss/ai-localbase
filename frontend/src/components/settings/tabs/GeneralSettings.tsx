import React from 'react'
import type { AppConfig } from '../../../App'

interface GeneralSettingsProps {
  config: AppConfig
}

interface OverviewRow {
  label: string
  value: string
  detail?: string
  status?: 'enabled' | 'disabled'
}

interface OverviewGroup {
  title: string
  rows: OverviewRow[]
}

const GeneralSettings: React.FC<GeneralSettingsProps> = ({ config }) => {
  const chatProviderLabel = config.chat.provider === 'ollama' ? 'Ollama' : 'OpenAI Compatible'
  const embeddingProviderLabel = config.embedding.provider === 'ollama' ? 'Ollama' : 'OpenAI Compatible'
  const searchModeLabel = config.retrieval.defaultSearchMode === 'hybrid' ? '混合检索' : '向量检索'
  const rerankLabel = config.retrieval.rerankStrategy === 'semantic' ? '语义重排' : '关键词融合'
  const queryRewriteLabel = config.retrieval.enableQueryRewrite ? '已启用' : '未启用'
  const lowConfidenceBoostLabel = config.retrieval.enableLowConfidenceBoost ? '已启用' : '未启用'

  const overviewGroups: OverviewGroup[] = [
    {
      title: '模型链路',
      rows: [
        { label: '聊天 Provider', value: chatProviderLabel, detail: config.chat.baseUrl },
        { label: '聊天模型', value: config.chat.model || '未配置', detail: `上下文 ${config.chat.contextMessageLimit} 条` },
        { label: 'Embedding Provider', value: embeddingProviderLabel, detail: config.embedding.baseUrl },
        { label: 'Embedding 模型', value: config.embedding.model || '未配置' },
      ],
    },
    {
      title: '检索链路',
      rows: [
        { label: '默认模式', value: searchModeLabel, detail: config.retrieval.hybridSearchEnabled ? '混合召回可用' : '仅默认召回' },
        { label: '重排策略', value: rerankLabel },
        { label: '问题改写', value: queryRewriteLabel, detail: `${config.retrieval.queryRewriteMaxVariants} 个变体` },
        { label: '低置信补强', value: lowConfidenceBoostLabel },
      ],
    },
    {
      title: '召回规模',
      rows: [
        { label: '文档 TopK', value: String(config.retrieval.topKDocument), detail: `候选 ${config.retrieval.candidateTopKDocument}` },
        { label: '知识库 TopK', value: String(config.retrieval.topKKnowledgeBase), detail: `候选 ${config.retrieval.candidateTopKAllDocs}` },
        { label: '每文档片段', value: String(config.retrieval.maxChunksPerDocument) },
        { label: '上下文字符', value: String(config.retrieval.maxContextChars) },
      ],
    },
    {
      title: '集成状态',
      rows: [
        { label: 'MCP 状态', value: config.mcp.enabled ? '已启用' : '未启用', status: config.mcp.enabled ? 'enabled' : 'disabled' },
        { label: 'MCP 路径', value: config.mcp.basePath || '未配置' },
        { label: '访问 Token', value: config.mcp.token ? '已生成' : '未生成', status: config.mcp.token ? 'enabled' : 'disabled' },
      ],
    },
  ]

  return (
    <div className="settings-tab-content settings-tab-content-overview">
      <section className="settings-card">
        <div className="settings-card-header">
          <div className="settings-card-header-copy">
            <h3>应用信息</h3>
            <p>集中查看模型、检索与 MCP 的当前运行配置。</p>
          </div>
        </div>
        <div className="settings-card-body">
          <div className="settings-overview-grid">
            {overviewGroups.map((group) => (
              <div className="settings-overview-group" key={group.title}>
                <h4>{group.title}</h4>
                <div className="settings-overview-rows">
                  {group.rows.map((row) => (
                    <div className="settings-info-row" key={`${group.title}-${row.label}`}>
                      <span className="settings-info-label">{row.label}</span>
                      <span className="settings-info-content">
                        {row.status ? (
                          <span className={`settings-status-pill ${row.status}`}>{row.value}</span>
                        ) : (
                          <span className="settings-info-value">{row.value}</span>
                        )}
                        {row.detail ? <small>{row.detail}</small> : null}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
      </section>
    </div>
  )
}

export default GeneralSettings
