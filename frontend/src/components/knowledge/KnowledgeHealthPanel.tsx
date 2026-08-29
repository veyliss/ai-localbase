import React from 'react'
import type { IndexedDocumentVerification, KnowledgeBaseHealthResponse } from '../../services/api'
import AppIcon from '../common/AppIcon'
import { healthStatusLabel } from './knowledgeLabels'

interface KnowledgeHealthPanelProps {
  health?: KnowledgeBaseHealthResponse
  loading: boolean
  error: string
  onReindexDocument: (documentId: string) => void
  reindexingDocumentId: string | null
  verificationByDocument: Record<string, IndexedDocumentVerification>
  verificationLoadingKey: string | null
  verificationError: string
  onVerifyDocument: (documentId: string) => void
}

const KnowledgeHealthPanel: React.FC<KnowledgeHealthPanelProps> = ({
  health,
  loading,
  error,
  onReindexDocument,
  reindexingDocumentId,
  verificationByDocument,
  verificationLoadingKey,
  verificationError,
  onVerifyDocument,
}) => {
  const badge = health ? healthStatusLabel(health.status) : null
  const needsReindexDocuments = health?.documents.filter((item) => item.needsReindex) ?? []
  const recentIndexRuns = health?.indexHistory?.slice(0, 5) ?? []

  const indexErrorLabel = (code?: string) => {
    switch (code) {
      case 'source_missing': return '原文缺失'
      case 'source_changed': return '原文已变化'
      case 'source_unreadable': return '原文不可读'
      case 'vector_dimension_mismatch': return '向量维度不一致'
      case 'index_rule_outdated': return '索引规则过期'
      case 'index_failed': return '索引失败'
      default: return code || '未分类'
    }
  }

  const verificationIssueLabel = (issue: string) => {
    const [code, expected, actual] = issue.split(':')
    const labels: Record<string, string> = {
      indexed_content_snapshot_missing: '索引快照缺失',
      indexed_content_flag_without_snapshot: '索引状态与快照不一致',
      indexed_content_snapshot_unreadable: '索引快照不可读',
      content_unavailable: '内容不可用',
      content_empty: '内容为空',
      evidence_location_missing: '证据定位缺失',
      snapshot_character_count_mismatch: '快照字符数不一致',
      structured_snapshot_unavailable: '结构化快照不可用',
      structured_table_count_mismatch: '表格数量不一致',
      indexed_table_count_mismatch: '索引表格数量不一致',
      index_version_outdated: '索引版本过期',
    }
    const label = labels[code] || code
    return expected && actual ? `${label}（${expected} / ${actual}）` : label
  }

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
              <small className="kb-health-version">索引版本 v{health.currentIndexVersion || 1}</small>
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

          <div className="kb-health-verification">
            <div className="kb-health-verification-head">
              <div>
                <h4>索引校验</h4>
                <span>验证已保存快照、chunk 数量与证据定位</span>
              </div>
              <AppIcon name="shield" size={16} />
            </div>
            {verificationError && <div className="kb-health-error" role="alert">{verificationError}</div>}
            {health.documents.length === 0 ? (
              <p className="kb-health-verification-empty">暂无文档可校验</p>
            ) : (
              <div className="kb-health-verification-list">
                {health.documents.map((item) => {
                  const verificationKey = `${health.knowledgeBaseId}:${item.documentId}`
                  const verification = verificationByDocument[verificationKey]
                  const isLoading = verificationLoadingKey === verificationKey
                  const statusLabel = verification
                    ? verification.valid ? '通过' : '需处理'
                    : '未校验'
                  const statusClass = verification
                    ? verification.valid ? 'is-valid' : 'is-invalid'
                    : 'is-pending'

                  return (
                    <div className="kb-health-verification-item" key={item.documentId}>
                      <div className="kb-health-verification-copy">
                        <div className="kb-health-verification-title">
                          <strong>{item.documentName}</strong>
                          <span className={`kb-health-verification-status ${statusClass}`}>{statusLabel}</span>
                        </div>
                        {verification ? (
                          verification.valid ? (
                            <span>
                              {verification.snapshotAvailable ? '已保存快照' : '使用原文'} · {verification.chunkCount} chunks · 证据 {verification.evidenceLocatedCount}/{verification.evidenceLocatedCount + verification.evidenceMissingCount}
                            </span>
                          ) : (
                            <span>{(verification.issues ?? []).map(verificationIssueLabel).join('、') || '发现索引一致性问题'}</span>
                          )
                        ) : (
                          <span>尚未执行完整校验</span>
                        )}
                      </div>
                      <button
                        aria-label={`校验 ${item.documentName} 的索引`}
                        disabled={isLoading}
                        onClick={() => onVerifyDocument(item.documentId)}
                        title="校验索引快照与证据定位"
                        type="button"
                      >
                        <AppIcon className={isLoading ? 'spin' : undefined} name="shield" size={14} />
                        {isLoading ? '校验中' : '校验'}
                      </button>
                    </div>
                  )
                })}
              </div>
            )}
          </div>

          {needsReindexDocuments.length > 0 && (
            <div className="kb-health-docs">
              <h4>需要处理的文档</h4>
              <div className="kb-health-doc-list">
                {needsReindexDocuments.map((item) => (
                  <div className="kb-health-doc-item" key={item.documentId}>
                    <div>
                      <strong>{item.documentName}</strong>
                      <span>
                        {item.recommendation || '建议检查索引状态'}
                        {item.indexVersion ? ` · 当前索引 v${item.indexVersion}` : ''}
                      </span>
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

          {recentIndexRuns.length > 0 && (
            <div className="kb-health-history">
              <div className="kb-health-history-head">
                <h4>最近索引记录</h4>
                <span>显示最近 {recentIndexRuns.length} 次</span>
              </div>
              <div className="kb-health-history-list">
                {recentIndexRuns.map((run) => (
                  <div className="kb-health-history-item" key={run.id}>
                    <div>
                      <strong>{run.documentName || '知识库批量任务'}</strong>
                      <span>
                        {run.status === 'succeeded' ? '成功' : '失败'} · {run.trigger} · v{run.indexVersion}
                      </span>
                    </div>
                    {run.status === 'succeeded' ? (
                      <span className="kb-health-history-status is-success">已完成</span>
                    ) : (
                      <span className="kb-health-history-status is-error" title={run.error || undefined}>
                        {indexErrorLabel(run.errorCode)}
                      </span>
                    )}
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
