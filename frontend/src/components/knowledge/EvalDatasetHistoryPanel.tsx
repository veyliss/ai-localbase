import React from 'react'
import type { EvalDatasetSummary } from '../../services/api'

interface EvalDatasetHistoryPanelProps {
  datasets: EvalDatasetSummary[]
  loading: boolean
  error: string
  openingDatasetId: string | null
  deletingDatasetId: string | null
  onRefresh: () => void
  onOpen: (datasetId: string) => void
  onDelete: (datasetId: string) => void
}

const formatDateTime = (value: string) => {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return '-'
  }
  return date.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

const datasetKindLabel = (kind?: string) => {
  if (kind === 'review') return '待审核'
  if (kind === 'generated') return '自动生成'
  return '评估集'
}

const EvalDatasetHistoryPanel: React.FC<EvalDatasetHistoryPanelProps> = ({
  datasets = [],
  loading,
  error,
  openingDatasetId,
  deletingDatasetId,
  onRefresh,
  onOpen,
  onDelete,
}) => {
  const safeDatasets = Array.isArray(datasets) ? datasets : []

  return (
    <section className="kb-eval-history-panel">
      <div className="kb-panel-section-head">
        <div>
          <h3>评估集历史</h3>
          <p>{safeDatasets.length} 份已保存评估集 · 可再次查看和导出</p>
        </div>
        <button className="kb-panel-mini-btn" onClick={onRefresh} disabled={loading}>
          {loading ? '刷新中' : '刷新'}
        </button>
      </div>

      {error && <div className="kb-eval-history-error">{error}</div>}

      {loading && safeDatasets.length === 0 ? (
        <div className="kb-eval-history-empty">正在加载评估集历史</div>
      ) : safeDatasets.length === 0 ? (
        <div className="kb-eval-history-empty">暂无已保存评估集，点击上方“评估集”生成第一份。</div>
      ) : (
        <div className="kb-eval-history-list">
          {safeDatasets.map((dataset) => (
            <article className="kb-eval-history-item" key={dataset.id}>
              <button className="kb-eval-history-main" onClick={() => onOpen(dataset.id)}>
                <span className="kb-eval-history-name">{dataset.name || dataset.id}</span>
                <span className="kb-eval-history-meta">
                  {datasetKindLabel(dataset.kind)} · {dataset.count} 条用例 · {dataset.documentCount} 份文档 · {formatDateTime(dataset.updatedAt || dataset.createdAt)}
                </span>
              </button>
              <div className="kb-eval-history-actions">
                <button
                  className="kb-doc-action"
                  onClick={() => onOpen(dataset.id)}
                  disabled={openingDatasetId === dataset.id}
                >
                  {openingDatasetId === dataset.id ? '打开中' : '查看'}
                </button>
                <button
                  className="kb-doc-remove"
                  onClick={() => onDelete(dataset.id)}
                  disabled={deletingDatasetId === dataset.id}
                  title="删除评估集"
                >
                  {deletingDatasetId === dataset.id ? '...' : 'x'}
                </button>
              </div>
            </article>
          ))}
        </div>
      )}
    </section>
  )
}

export default EvalDatasetHistoryPanel
