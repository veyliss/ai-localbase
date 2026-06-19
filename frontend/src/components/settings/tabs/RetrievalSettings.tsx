import React from 'react'
import type { RetrievalConfig } from '../../../App'

interface RetrievalSettingsProps {
  config: RetrievalConfig
  onRetrievalConfigChange: <K extends keyof RetrievalConfig>(
    key: K,
    value: RetrievalConfig[K],
  ) => void
}

const RetrievalSettings: React.FC<RetrievalSettingsProps> = ({
  config,
  onRetrievalConfigChange,
}) => {
  return (
    <div className="settings-tab-content">
      <section className="settings-card">
        <div className="settings-card-header">
          <h3>召回策略</h3>
          <p>决定先用什么方式召回，再如何排序</p>
        </div>
        <div className="settings-card-body">
          <div className="settings-form-grid">
            <div className="settings-form-group">
              <label className="settings-form-label">默认模式</label>
              <select
                value={config.defaultSearchMode}
                onChange={(event) => onRetrievalConfigChange('defaultSearchMode', event.target.value as RetrievalConfig['defaultSearchMode'])}
              >
                <option value="dense">向量检索</option>
                <option value="hybrid">混合检索</option>
              </select>
            </div>
            <div className="settings-form-group settings-form-group-checkbox">
              <label className="settings-checkbox-label">
                <input
                  type="checkbox"
                  checked={config.hybridSearchEnabled}
                  onChange={(event) => onRetrievalConfigChange('hybridSearchEnabled', event.target.checked)}
                />
                <span>启用混合检索</span>
              </label>
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">重排策略</label>
              <select
                value={config.rerankStrategy}
                onChange={(event) => onRetrievalConfigChange('rerankStrategy', event.target.value as RetrievalConfig['rerankStrategy'])}
              >
                <option value="keyword">关键词融合</option>
                <option value="semantic">语义重排</option>
              </select>
            </div>
            <div className="settings-form-group settings-form-group-checkbox">
              <label className="settings-checkbox-label">
                <input
                  type="checkbox"
                  checked={config.enableQueryRewrite}
                  onChange={(event) => onRetrievalConfigChange('enableQueryRewrite', event.target.checked)}
                />
                <span>启用问题改写</span>
              </label>
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">改写数量</label>
              <input
                type="number"
                min="1"
                max="5"
                value={config.queryRewriteMaxVariants}
                onChange={(event) => onRetrievalConfigChange('queryRewriteMaxVariants', Number(event.target.value))}
              />
            </div>
          </div>
        </div>
      </section>

      <section className="settings-card">
        <div className="settings-card-header">
          <h3>召回规模</h3>
          <p>控制候选集大小和最终进入上下文的片段数量</p>
        </div>
        <div className="settings-card-body">
          <div className="settings-form-grid">
            <div className="settings-form-group">
              <label className="settings-form-label">文档 TopK</label>
              <input
                type="number"
                min="1"
                max="30"
                value={config.topKDocument}
                onChange={(event) => onRetrievalConfigChange('topKDocument', Number(event.target.value))}
              />
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">文档候选 TopK</label>
              <input
                type="number"
                min={config.topKDocument}
                max="80"
                value={config.candidateTopKDocument}
                onChange={(event) => onRetrievalConfigChange('candidateTopKDocument', Number(event.target.value))}
              />
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">知识库 TopK</label>
              <input
                type="number"
                min="1"
                max="40"
                value={config.topKKnowledgeBase}
                onChange={(event) => onRetrievalConfigChange('topKKnowledgeBase', Number(event.target.value))}
              />
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">知识库候选 TopK</label>
              <input
                type="number"
                min={config.topKKnowledgeBase}
                max="120"
                value={config.candidateTopKAllDocs}
                onChange={(event) => onRetrievalConfigChange('candidateTopKAllDocs', Number(event.target.value))}
              />
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">每文档片段数</label>
              <input
                type="number"
                min="1"
                max="10"
                value={config.maxChunksPerDocument}
                onChange={(event) => onRetrievalConfigChange('maxChunksPerDocument', Number(event.target.value))}
              />
            </div>
          </div>
        </div>
      </section>

      <section className="settings-card">
        <div className="settings-card-header">
          <h3>上下文与补强</h3>
          <p>控制进入回答前的证据长度和低置信兜底</p>
        </div>
        <div className="settings-card-body">
          <div className="settings-form-grid">
            <div className="settings-form-group">
              <label className="settings-form-label">上下文字符</label>
              <input
                type="number"
                min="800"
                max="20000"
                step="100"
                value={config.maxContextChars}
                onChange={(event) => onRetrievalConfigChange('maxContextChars', Number(event.target.value))}
              />
            </div>
            <div className="settings-form-group settings-form-group-checkbox">
              <label className="settings-checkbox-label">
                <input
                  type="checkbox"
                  checked={config.enableLowConfidenceBoost}
                  onChange={(event) => onRetrievalConfigChange('enableLowConfidenceBoost', event.target.checked)}
                />
                <span>低置信自动扩展</span>
              </label>
              <small>当知识库范围召回置信偏低时，扩大候选并尝试补充更多片段。</small>
            </div>
          </div>
        </div>
      </section>
    </div>
  )
}

export default RetrievalSettings