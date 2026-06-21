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

const evalTrendInsights = (
  latest: EvalRunSummary | null,
  previous: EvalRunSummary | null,
) => {
  if (!latest) return []

  const insights: string[] = []
  const metrics = latest.metrics
  const hitRateDelta = previous ? metrics.hitRate - previous.metrics.hitRate : null
  const mrrDelta = previous ? metrics.mrr - previous.metrics.mrr : null

  if (metrics.hitRate < 0.7) {
    insights.push('Hit Rate 偏低，优先检查召回范围、切分质量和检索模式。')
  } else if (hitRateDelta !== null && hitRateDelta < -0.05) {
    insights.push('Hit Rate 较上次下降，建议对比最近一次检索策略或索引变更。')
  } else {
    insights.push('Hit Rate 表现稳定，继续关注低置信与证据支撑。')
  }

  if (metrics.mrr < 0.45) {
    insights.push('MRR 偏低，命中文档排序靠后，可尝试语义重排或混合检索。')
  } else if (mrrDelta !== null && mrrDelta > 0.02) {
    insights.push('MRR 有提升，当前排序策略较上一轮更有效。')
  }

  if (metrics.lowConfidence > 0) {
    insights.push(`低置信 ${metrics.lowConfidence} 条，建议沉淀为评估样本并复核答案证据。`)
  }

  if (metrics.evidenceSupportRate < 0.85) {
    insights.push('证据支撑不足，关注上下文压缩、引用片段和答案支撑关系。')
  }

  if (metrics.latencyP95Ms > 8000) {
    insights.push('P95 延迟较高，建议检查改写、重排和模型调用耗时。')
  }

  if (metrics.citationMismatchCount > 0) {
    insights.push(`引用不准 ${metrics.citationMismatchCount} 条，建议检查引用定位和 chunk 边界。`)
  }

  return insights.slice(0, 4)
}

const EvalRunTrendPanel: React.FC<EvalRunTrendPanelProps> = ({
  runs = [],
  loading,
  error,
  onRefresh,
}) => {
  const safeRuns = Array.isArray(runs) ? runs : []
  const latest = safeRuns[0] ?? null
  const previous = safeRuns[1] ?? null
  const hitRateDelta = latest && previous ? latest.metrics.hitRate - previous.metrics.hitRate : null
  const mrrDelta = latest && previous ? latest.metrics.mrr - previous.metrics.mrr : null
  const insights = evalTrendInsights(latest, previous)
  const visibleRuns = safeRuns.slice(0, 6)

  return (
    <section className={`kb-eval-trend-panel${safeRuns.length === 0 ? ' kb-eval-trend-panel--empty' : ''}`}>
      <div className="kb-panel-section-head">
        <div>
          <h3>质量趋势</h3>
          <p>{safeRuns.length} 次评估运行 · 关注 Hit Rate、MRR、低置信和证据支撑变化</p>
        </div>
        <button className="kb-panel-mini-btn" onClick={onRefresh} disabled={loading}>
          {loading ? '刷新中' : '刷新'}
        </button>
      </div>

      {error && <div className="kb-eval-history-error">{error}</div>}

      {loading && safeRuns.length === 0 ? (
        <div className="kb-eval-trend-empty">正在加载评估趋势</div>
      ) : safeRuns.length === 0 ? (
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
          {insights.length > 0 && (
            <div className="kb-eval-trend-insights" aria-label="质量趋势诊断建议">
              {insights.map((insight) => (
                <span key={insight}>{insight}</span>
              ))}
            </div>
          )}
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
