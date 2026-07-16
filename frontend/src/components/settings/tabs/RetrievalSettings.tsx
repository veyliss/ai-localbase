import React, { useMemo, useState } from 'react'
import type { RetrievalConfig } from '../../../App'
import AppIcon, { type AppIconName } from '../../common/AppIcon'

interface RetrievalSettingsProps {
  config: RetrievalConfig
  onRetrievalConfigChange: <K extends keyof RetrievalConfig>(
    key: K,
    value: RetrievalConfig[K],
  ) => void
  onRetrievalConfigPatch: (patch: Partial<RetrievalConfig>) => void
}

type RetrievalPresetId = 'fast' | 'balanced' | 'quality' | 'custom'

interface RetrievalPreset {
  id: Exclude<RetrievalPresetId, 'custom'>
  label: string
  description: string
  icon: AppIconName
  config: RetrievalConfig
}

const retrievalPresets: RetrievalPreset[] = [
  {
    id: 'fast',
    label: '快速',
    description: '低延迟，适合短文档和明确问题',
    icon: 'zap',
    config: {
      defaultSearchMode: 'dense',
      hybridSearchEnabled: false,
      rerankStrategy: 'keyword',
      enableQueryRewrite: false,
      queryRewriteMaxVariants: 2,
      topKDocument: 4,
      candidateTopKDocument: 8,
      topKKnowledgeBase: 6,
      candidateTopKAllDocs: 16,
      maxChunksPerDocument: 2,
      maxContextChars: 1800,
      enableLowConfidenceBoost: false,
    },
  },
  {
    id: 'balanced',
    label: '均衡',
    description: '兼顾速度与跨文档召回质量',
    icon: 'sliders',
    config: {
      defaultSearchMode: 'hybrid',
      hybridSearchEnabled: true,
      rerankStrategy: 'keyword',
      enableQueryRewrite: true,
      queryRewriteMaxVariants: 3,
      topKDocument: 6,
      candidateTopKDocument: 12,
      topKKnowledgeBase: 10,
      candidateTopKAllDocs: 32,
      maxChunksPerDocument: 2,
      maxContextChars: 2400,
      enableLowConfidenceBoost: false,
    },
  },
  {
    id: 'quality',
    label: '高质量',
    description: '扩大候选并启用语义补强',
    icon: 'sparkles',
    config: {
      defaultSearchMode: 'hybrid',
      hybridSearchEnabled: true,
      rerankStrategy: 'semantic',
      enableQueryRewrite: true,
      queryRewriteMaxVariants: 4,
      topKDocument: 8,
      candidateTopKDocument: 20,
      topKKnowledgeBase: 14,
      candidateTopKAllDocs: 48,
      maxChunksPerDocument: 3,
      maxContextChars: 4200,
      enableLowConfidenceBoost: true,
    },
  },
]

const retrievalConfigKeys = Object.keys(retrievalPresets[0].config) as Array<keyof RetrievalConfig>

const matchesPreset = (config: RetrievalConfig, preset: RetrievalPreset) =>
  retrievalConfigKeys.every((key) => config[key] === preset.config[key])

const RetrievalSettings: React.FC<RetrievalSettingsProps> = ({
  config,
  onRetrievalConfigChange,
  onRetrievalConfigPatch,
}) => {
  const [showAdvanced, setShowAdvanced] = useState(false)

  const activePreset = useMemo<RetrievalPresetId>(
    () => retrievalPresets.find((preset) => matchesPreset(config, preset))?.id ?? 'custom',
    [config],
  )

  const handlePresetChange = (presetId: RetrievalPresetId) => {
    if (presetId === 'custom') {
      setShowAdvanced(true)
      return
    }
    const preset = retrievalPresets.find((item) => item.id === presetId)
    if (preset) {
      onRetrievalConfigPatch(preset.config)
    }
  }

  const handleSearchModeChange = (mode: RetrievalConfig['defaultSearchMode']) => {
    onRetrievalConfigPatch({
      defaultSearchMode: mode,
      ...(mode === 'hybrid' ? { hybridSearchEnabled: true } : {}),
    })
  }

  return (
    <div className="settings-tab-content settings-retrieval-page">
      <section className="settings-preset-section">
        <header>
          <h3>检索预设</h3>
          <p>先选择目标，再按需要微调参数。</p>
        </header>
        <div className="settings-preset-options" aria-label="检索预设">
          {retrievalPresets.map((preset) => (
            <button
              aria-pressed={activePreset === preset.id}
              className={activePreset === preset.id ? 'active' : ''}
              key={preset.id}
              onClick={() => handlePresetChange(preset.id)}
              type="button"
            >
              <span aria-hidden="true"><AppIcon name={preset.icon} size={17} /></span>
              <strong>{preset.label}</strong>
              <small>{preset.description}</small>
            </button>
          ))}
          <button
            aria-pressed={activePreset === 'custom'}
            className={activePreset === 'custom' ? 'active' : ''}
            onClick={() => handlePresetChange('custom')}
            type="button"
          >
            <span aria-hidden="true"><AppIcon name="settings" size={17} /></span>
            <strong>自定义</strong>
            <small>保留当前配置并展开高级参数</small>
          </button>
        </div>
      </section>

      <section className="settings-form-section settings-retrieval-core">
        <header>
          <h4>核心策略</h4>
          <p>决定召回方式、排序方法和回答证据长度。</p>
        </header>
        <div className="settings-form-grid settings-form-grid-dense">
          <div className="settings-form-group">
            <label className="settings-form-label" htmlFor="retrieval-search-mode">默认模式</label>
            <select
              id="retrieval-search-mode"
              value={config.defaultSearchMode}
              onChange={(event) => handleSearchModeChange(event.target.value as RetrievalConfig['defaultSearchMode'])}
            >
              <option value="dense">向量检索</option>
              <option value="hybrid">混合检索</option>
            </select>
          </div>
          <div className="settings-form-group">
            <label className="settings-form-label" htmlFor="retrieval-rerank">重排策略</label>
            <select
              id="retrieval-rerank"
              value={config.rerankStrategy}
              onChange={(event) => onRetrievalConfigChange('rerankStrategy', event.target.value as RetrievalConfig['rerankStrategy'])}
            >
              <option value="keyword">关键词融合</option>
              <option value="semantic">语义重排</option>
            </select>
          </div>
          <div className="settings-form-group settings-form-group-full">
            <label className="settings-form-label settings-form-label-inline" htmlFor="retrieval-context-chars">
              <span>回答上下文字符</span>
              <strong>{config.maxContextChars.toLocaleString()}</strong>
            </label>
            <input
              id="retrieval-context-chars"
              type="range"
              min="800"
              max="20000"
              step="100"
              value={config.maxContextChars}
              onChange={(event) => onRetrievalConfigChange('maxContextChars', Number(event.target.value))}
            />
            <small>更高的值会提供更多证据，同时增加模型上下文消耗。</small>
          </div>
          <label className="settings-toggle-row settings-form-group-full" htmlFor="retrieval-hybrid-enabled">
            <span>
              <strong>启用混合召回</strong>
              <small>同时使用向量和关键词信号。</small>
            </span>
            <input
              id="retrieval-hybrid-enabled"
              type="checkbox"
              checked={config.hybridSearchEnabled}
              onChange={(event) => onRetrievalConfigChange('hybridSearchEnabled', event.target.checked)}
            />
          </label>
        </div>
      </section>

      <details
        className="settings-advanced-section"
        onToggle={(event) => setShowAdvanced(event.currentTarget.open)}
        open={showAdvanced}
      >
        <summary>
          <span>
            <strong>高级参数</strong>
            <small>问题改写、候选规模与低置信补强</small>
          </span>
          <span className="settings-advanced-summary">
            最终 {config.topKKnowledgeBase} · 候选 {config.candidateTopKAllDocs}
          </span>
          <AppIcon name="chevronDown" size={17} />
        </summary>

        <div className="settings-advanced-content">
          <section className="settings-form-section">
            <header>
              <h4>问题改写</h4>
              <p>为模糊问题生成多个检索表达。</p>
            </header>
            <div className="settings-form-grid settings-form-grid-dense">
              <label className="settings-toggle-row settings-form-group-full" htmlFor="retrieval-query-rewrite">
                <span>
                  <strong>启用问题改写</strong>
                  <small>复杂问题通常能获得更完整的召回。</small>
                </span>
                <input
                  id="retrieval-query-rewrite"
                  type="checkbox"
                  checked={config.enableQueryRewrite}
                  onChange={(event) => onRetrievalConfigChange('enableQueryRewrite', event.target.checked)}
                />
              </label>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="retrieval-query-variants">改写数量</label>
                <input
                  disabled={!config.enableQueryRewrite}
                  id="retrieval-query-variants"
                  type="number"
                  min="1"
                  max="5"
                  value={config.queryRewriteMaxVariants}
                  onChange={(event) => onRetrievalConfigChange('queryRewriteMaxVariants', Number(event.target.value))}
                />
              </div>
            </div>
          </section>

          <section className="settings-form-section">
            <header>
              <h4>召回规模</h4>
              <p>控制初始候选和最终进入上下文的片段数量。</p>
            </header>
            <div className="settings-form-grid settings-form-grid-dense">
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="retrieval-document-top-k">文档 TopK</label>
                <input
                  id="retrieval-document-top-k"
                  type="number"
                  min="1"
                  max="30"
                  value={config.topKDocument}
                  onChange={(event) => onRetrievalConfigChange('topKDocument', Number(event.target.value))}
                />
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="retrieval-document-candidates">文档候选</label>
                <input
                  id="retrieval-document-candidates"
                  type="number"
                  min={config.topKDocument}
                  max="80"
                  value={config.candidateTopKDocument}
                  onChange={(event) => onRetrievalConfigChange('candidateTopKDocument', Number(event.target.value))}
                />
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="retrieval-kb-top-k">知识库 TopK</label>
                <input
                  id="retrieval-kb-top-k"
                  type="number"
                  min="1"
                  max="40"
                  value={config.topKKnowledgeBase}
                  onChange={(event) => onRetrievalConfigChange('topKKnowledgeBase', Number(event.target.value))}
                />
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="retrieval-kb-candidates">知识库候选</label>
                <input
                  id="retrieval-kb-candidates"
                  type="number"
                  min={config.topKKnowledgeBase}
                  max="120"
                  value={config.candidateTopKAllDocs}
                  onChange={(event) => onRetrievalConfigChange('candidateTopKAllDocs', Number(event.target.value))}
                />
              </div>
              <div className="settings-form-group">
                <label className="settings-form-label" htmlFor="retrieval-chunks-per-document">每文档片段数</label>
                <input
                  id="retrieval-chunks-per-document"
                  type="number"
                  min="1"
                  max="10"
                  value={config.maxChunksPerDocument}
                  onChange={(event) => onRetrievalConfigChange('maxChunksPerDocument', Number(event.target.value))}
                />
              </div>
            </div>
          </section>

          <label className="settings-toggle-row" htmlFor="retrieval-confidence-boost">
            <span>
              <strong>低置信自动补强</strong>
              <small>召回置信偏低时扩大候选并补充片段。</small>
            </span>
            <input
              id="retrieval-confidence-boost"
              type="checkbox"
              checked={config.enableLowConfidenceBoost}
              onChange={(event) => onRetrievalConfigChange('enableLowConfidenceBoost', event.target.checked)}
            />
          </label>
        </div>
      </details>
    </div>
  )
}

export default RetrievalSettings
