import React from 'react'
import type { EvalRunSummary } from '../../services/api'

interface EvalRunTrendPanelProps {
  runs: EvalRunSummary[]
  loading: boolean
  error: string
  onRefresh: () => void
}

const formatPercent = (value: number) => `${(value * 100).toFixed(1)}%`

const searchModeLabel = (mode?: string) => {
  if (mode === 'hybrid') return '混合'
  if (mode === 'dense') return '向量'
  return '自动'
}

const rerankStrategyLabel = (strategy?: string) => {
  if (strategy === 'semantic') return '语义'
  return '关键词'
}

const evalStrategyLabel = (run?: EvalRunSummary | null) => {
  if (!run) return '-'
  return `${rerankStrategyLabel(run.rerankStrategy)} / ${run.queryRewriteUsed ? '改写' : '不改写'}`
}

const evalRunModeLabel = (run: EvalRunSummary) => (
  `${searchModeLabel(run.searchMode)} · ${evalStrategyLabel(run)}`
)

const formatDateTime = (value: string) => {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

const EvalRunTrendPanel: React.FC<EvalRunTrendPanelProps> = ({
  runs,
  loading,
  error,
  onRefresh,
}) => {
  const latest = runs[0] ?? null
  const previous = runs[1] ?? null
  const hitRateDelta = latest && previous ? latest.metrics.hitRate - previous.metrics.hitRate : null
  const mrrDelta = latest && previous ? latest.metrics.mrr - previous.metrics.mrr : null
  const visibleRuns = runs.slice(0, 6)

  return (
    <section className={`kb-eval-trend-panel${runs.length === 0 ? ' kb-eval-trend-panel--empty' : ''}`}>
      <div className="kb-panel-section-head">
        <div>
          <h3>质量趋势</h3>
          <p>{runs.length} 次评估运行 · 关注 Hit Rate、MRR、低置信和证据支撑变化</p>
        </div>
        <button className="kb-panel-mini-btn" onClick={onRefresh} disabled={loading}>
          {loading ? '刷新中' : '刷新'}
        </button>
      </div>

      {error && <div className="kb-eval-history-error">{error}</div>}

      {loading && runs.length === 0 ? (
        <div className="kb-eval-trend-empty">正在加载评估趋势</div>
      ) : runs.length === 0 ? (
        <div className="kb-eval-trend-empty">暂无运行记录，打开评估集并点击“运行评估”后会生成趋势。</div>
      ) : (
        <>
          <div className="kb-eval-trend-summary">
            <div>
              <strong>{formatPercent(latest?.metrics.hitRate ?? 0)}</strong>
              <span>最新 Hit Rate</span>
              {hitRateDelta !== null && <em>{hitRateDelta >= 0 ? '+' : ''}{formatPercent(hitRateDelta)}</em>}
            </div>
            <div>
              <strong>{(latest?.metrics.mrr ?? 0).toFixed(3)}</strong>
              <span>最新 MRR</span>
              {mrrDelta !== null && <em>{mrrDelta >= 0 ? '+' : ''}{mrrDelta.toFixed(3)}</em>}
            </div>
            <div>
              <strong>{latest?.metrics.lowConfidence ?? 0}</strong>
              <span>低置信</span>
            </div>
            <div>
              <strong>{formatPercent(latest?.metrics.evidenceSupportRate ?? 0)}</strong>
              <span>证据支撑</span>
            </div>
            <div>
              <strong>{latest?.metrics.latencyP95Ms ?? 0}ms</strong>
              <span>检索 P95</span>
            </div>
            <div>
              <strong>{searchModeLabel(latest?.searchMode)}</strong>
              <span>最新模式</span>
            </div>
            <div>
              <strong>{evalStrategyLabel(latest)}</strong>
              <span>最新策略</span>
            </div>
          </div>
          <div className="kb-eval-trend-list">
            {visibleRuns.map((run) => (
              <article key={run.runId} className="kb-eval-trend-item">
                <div>
                  <strong>{run.datasetName || run.datasetId}</strong>
                  <span>{formatDateTime(run.startedAt)} · {evalRunModeLabel(run)} · {run.metrics.totalCases} 条用例</span>
                </div>
                <div className="kb-eval-trend-metrics">
                  <span>Hit {formatPercent(run.metrics.hitRate)}</span>
                  <span>MRR {run.metrics.mrr.toFixed(3)}</span>
                  <span>低置信 {run.metrics.lowConfidence}</span>
                  <span>证据 {formatPercent(run.metrics.evidenceSupportRate ?? 0)}</span>
                  <span>引用不准 {run.metrics.citationMismatchCount ?? 0}</span>
                  <span>P95 {run.metrics.latencyP95Ms}ms</span>
                </div>
              </article>
            ))}
          </div>
        </>
      )}
    </section>
  )
}

export default EvalRunTrendPanel
