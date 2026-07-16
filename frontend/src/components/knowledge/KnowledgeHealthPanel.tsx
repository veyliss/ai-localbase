import React from 'react'
import type { KnowledgeBaseHealthResponse } from '../../services/api'
import AppIcon from '../common/AppIcon'
import { healthStatusLabel } from './knowledgeLabels'

interface KnowledgeHealthPanelProps {
  health?: KnowledgeBaseHealthResponse
  loading: boolean
  error: string
  onReindexDocument: (documentId: string) => void
  reindexingDocumentId: string | null
}

const KnowledgeHealthPanel: React.FC<KnowledgeHealthPanelProps> = ({
  health,
  loading,
  error,
  onReindexDocument,
  reindexingDocumentId,
}) => {
  const badge = health ? healthStatusLabel(health.status) : null
  const needsReindexDocuments = health?.documents.filter((item) => item.needsReindex) ?? []

  return (
    <section className="kb-health-panel">
      <div className="kb-panel-section-head">
        <div>
          <h3>索引健康</h3>
          <p>
            {health?.metrics.lastIndexedAt
              ? `最近索引 ${new Date(health.metrics.lastIndexedAt).toLocaleString('zh-CN')}`
              : '检查文档索引、向量和结构化数据状态'}
          </p>
        </div>
      </div>

      {error && !loading && <div className="kb-health-error">{error}</div>}

      {!health && !error && (
        <div className="kb-health-loading">
          <AppIcon className={loading ? 'spin' : undefined} name={loading ? 'loader' : 'info'} size={22} />
          <strong>{loading ? '正在检查索引状态' : '暂无健康数据'}</strong>
        </div>
      )}

      {health && (
        <>
          <div className="kb-health-overview">
            <div className="kb-health-score">
              <span>健康评分</span>
              <strong>{health.score}</strong>
            </div>
            <div>
              <span className="kb-health-badge" style={{ color: badge?.color, background: badge?.bg }}>
                {badge?.text}
              </span>
              <p>
                {needsReindexDocuments.length > 0
                  ? `${needsReindexDocuments.length} 份文档需要处理`
                  : '所有文档索引状态正常'}
              </p>
            </div>
          </div>

          <dl className="kb-health-stats">
            <div><dt>文档</dt><dd>{health.metrics.documentCount}</dd></div>
            <div><dt>已索引</dt><dd>{health.metrics.indexedCount}</dd></div>
            <div data-status={health.metrics.failedCount > 0 ? 'error' : 'normal'}>
              <dt>失败</dt><dd>{health.metrics.failedCount}</dd>
            </div>
            <div><dt>Chunks</dt><dd>{health.metrics.chunkCount}</dd></div>
            <div><dt>向量</dt><dd>{health.metrics.vectorCount}</dd></div>
            <div><dt>结构化行</dt><dd>{health.metrics.structuredRowCount}</dd></div>
          </dl>

          {health.recommendations.length > 0 && (
            <div className="kb-health-recommendations">
              <h4>检查建议</h4>
              {health.recommendations.map((item) => (
                <div className="kb-health-recommendation" key={item}>
                  <AppIcon name={needsReindexDocuments.length > 0 ? 'alert' : 'check'} size={16} />
                  <span>{item}</span>
                </div>
              ))}
            </div>
          )}

          {needsReindexDocuments.length > 0 && (
            <div className="kb-health-docs">
              <h4>需要处理的文档</h4>
              <div className="kb-health-doc-list">
                {needsReindexDocuments.map((item) => (
                  <div className="kb-health-doc-item" key={item.documentId}>
                    <div>
                      <strong>{item.documentName}</strong>
                      <span>{item.recommendation || '建议检查索引状态'}</span>
                    </div>
                    <button
                      disabled={reindexingDocumentId === item.documentId}
                      onClick={() => onReindexDocument(item.documentId)}
                      type="button"
                    >
                      <AppIcon className={reindexingDocumentId === item.documentId ? 'spin' : undefined} name="refresh" size={15} />
                      {reindexingDocumentId === item.documentId ? '重建中' : '重新索引'}
                    </button>
                  </div>
                ))}
              </div>
            </div>
          )}
        </>
      )}
    </section>
  )
}

export default KnowledgeHealthPanel
