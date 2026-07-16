import React from 'react'
import type { EvalRunSummary } from '../../services/api'
import AppIcon from '../common/AppIcon'

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

const rerankStrategyLabel = (strategy?: string) => (
  strategy === 'semantic' ? '语义' : '关键词'
)

const evalStrategyLabel = (run?: EvalRunSummary | null) => (
  run ? `${rerankStrategyLabel(run.rerankStrategy)} / ${run.queryRewriteUsed ? '改写' : '不改写'}` : '-'
)

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

const formatChartTime = (value: string) => {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

const evalTrendInsights = (latest: EvalRunSummary | null, previous: EvalRunSummary | null) => {
  if (!latest) return []

  const insights: string[] = []
  const metrics = latest.metrics
  const hitRateDelta = previous ? metrics.hitRate - previous.metrics.hitRate : null
  const mrrDelta = previous ? metrics.mrr - previous.metrics.mrr : null

  if (metrics.hitRate < 0.7) insights.push('Hit Rate 偏低，优先检查召回范围、切分质量和检索模式。')
  else if (hitRateDelta !== null && hitRateDelta < -0.05) insights.push('Hit Rate 较上次下降，建议对比最近的索引或检索策略变更。')

  if (metrics.mrr < 0.45) insights.push('MRR 偏低，命中文档排序靠后，可尝试语义重排或混合检索。')
  else if (mrrDelta !== null && mrrDelta > 0.02) insights.push('MRR 有提升，当前排序策略较上一轮更有效。')

  if (metrics.lowConfidence > 0) insights.push(`存在 ${metrics.lowConfidence} 条低置信结果，建议复核答案证据。`)
  if (metrics.evidenceSupportRate < 0.85) insights.push('证据支撑不足，关注上下文压缩、引用片段和答案支撑关系。')
  if (metrics.latencyP95Ms > 8000) insights.push('P95 延迟较高，建议检查改写、重排和模型调用耗时。')
  if (metrics.citationMismatchCount > 0) insights.push(`存在 ${metrics.citationMismatchCount} 条引用不准结果。`)

  return insights.slice(0, 4)
}

const TrendChart: React.FC<{ runs: EvalRunSummary[] }> = ({ runs }) => {
  const points = runs.slice(0, 6).reverse()
  const width = 640
  const height = 190
  const paddingX = 38
  const paddingTop = 18
  const paddingBottom = 32
  const plotHeight = height - paddingTop - paddingBottom
  const plotWidth = width - paddingX * 2
  const xForIndex = (index: number) => (
    points.length <= 1 ? width / 2 : paddingX + (plotWidth * index) / (points.length - 1)
  )
  const yForValue = (value: number) => paddingTop + (1 - Math.max(0, Math.min(1, value))) * plotHeight
  const hitRatePoints = points.map((run, index) => `${xForIndex(index)},${yForValue(run.metrics.hitRate)}`).join(' ')
  const mrrPoints = points.map((run, index) => `${xForIndex(index)},${yForValue(run.metrics.mrr)}`).join(' ')

  return (
    <div className="kb-eval-chart">
      <div className="kb-eval-chart-head">
        <div>
          <h4>指标趋势</h4>
          <p>最近 {points.length} 次运行</p>
        </div>
        <div className="kb-eval-chart-legend">
          <span data-series="hit">Hit Rate</span>
          <span data-series="mrr">MRR</span>
        </div>
      </div>
      <svg aria-label="Hit Rate 与 MRR 趋势" role="img" viewBox={`0 0 ${width} ${height}`}>
        {[0, 0.5, 1].map((value) => (
          <g key={value}>
            <line className="kb-eval-chart-grid" x1={paddingX} x2={width - paddingX} y1={yForValue(value)} y2={yForValue(value)} />
            <text className="kb-eval-chart-axis-label" x={paddingX - 8} y={yForValue(value) + 4}>{Math.round(value * 100)}%</text>
          </g>
        ))}
        <polyline className="kb-eval-chart-line kb-eval-chart-line--hit" points={hitRatePoints} />
        <polyline className="kb-eval-chart-line kb-eval-chart-line--mrr" points={mrrPoints} />
        {points.map((run, index) => (
          <g key={run.runId}>
            <circle className="kb-eval-chart-point kb-eval-chart-point--hit" cx={xForIndex(index)} cy={yForValue(run.metrics.hitRate)} r="4" />
            <circle className="kb-eval-chart-point kb-eval-chart-point--mrr" cx={xForIndex(index)} cy={yForValue(run.metrics.mrr)} r="4" />
            <text className="kb-eval-chart-date" x={xForIndex(index)} y={height - 8}>{formatChartTime(run.startedAt)}</text>
          </g>
        ))}
      </svg>
    </div>
  )
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
  const visibleRuns = safeRuns.slice(0, 8)

  return (
    <section className={`kb-eval-trend-panel${safeRuns.length === 0 ? ' kb-eval-trend-panel--empty' : ''}`}>
      <div className="kb-panel-section-head">
        <div>
          <h3>质量趋势</h3>
          <p>{safeRuns.length} 次评估运行 · 对比检索质量、证据支撑和延迟变化</p>
        </div>
        <button
          aria-label="刷新质量趋势"
          className="kb-panel-icon-btn"
          disabled={loading}
          onClick={onRefresh}
          title="刷新质量趋势"
          type="button"
        >
          <AppIcon className={loading ? 'spin' : undefined} name="refresh" size={16} />
        </button>
      </div>

      {error && <div className="kb-eval-history-error">{error}</div>}

      {loading && safeRuns.length === 0 ? (
        <div className="kb-eval-trend-empty">正在加载评估趋势</div>
      ) : safeRuns.length === 0 ? (
        <div className="kb-eval-trend-empty">暂无运行记录，打开评估集并运行后会生成趋势。</div>
      ) : (
        <>
          <div className="kb-eval-latest-context">
            <span>最新策略</span>
            <strong>{searchModeLabel(latest?.searchMode)} · {evalStrategyLabel(latest)}</strong>
            <span>{formatDateTime(latest?.startedAt ?? '')}</span>
          </div>

          <dl className="kb-eval-trend-summary">
            <div>
              <dt>Hit Rate</dt>
              <dd>{formatPercent(latest?.metrics.hitRate ?? 0)}</dd>
              {hitRateDelta !== null && <span data-direction={hitRateDelta >= 0 ? 'up' : 'down'}>{hitRateDelta >= 0 ? '+' : ''}{formatPercent(hitRateDelta)}</span>}
            </div>
            <div>
              <dt>MRR</dt>
              <dd>{(latest?.metrics.mrr ?? 0).toFixed(3)}</dd>
              {mrrDelta !== null && <span data-direction={mrrDelta >= 0 ? 'up' : 'down'}>{mrrDelta >= 0 ? '+' : ''}{mrrDelta.toFixed(3)}</span>}
            </div>
            <div data-status={(latest?.metrics.evidenceSupportRate ?? 0) < 0.85 ? 'warning' : 'normal'}>
              <dt>证据支撑</dt>
              <dd>{formatPercent(latest?.metrics.evidenceSupportRate ?? 0)}</dd>
              <span>{latest?.metrics.citationMismatchCount ?? 0} 条引用不准</span>
            </div>
            <div data-status={(latest?.metrics.latencyP95Ms ?? 0) > 8000 ? 'warning' : 'normal'}>
              <dt>检索 P95</dt>
              <dd>{latest?.metrics.latencyP95Ms ?? 0}ms</dd>
              <span>{latest?.metrics.lowConfidence ?? 0} 条低置信</span>
            </div>
          </dl>

          <TrendChart runs={safeRuns} />

          {insights.length > 0 && (
            <div aria-label="质量趋势诊断建议" className="kb-eval-trend-insights">
              <div>
                <AppIcon name="alert" size={17} />
                <strong>诊断建议</strong>
              </div>
              <ul>
                {insights.map((insight) => <li key={insight}>{insight}</li>)}
              </ul>
            </div>
          )}

          <div className="kb-eval-run-history">
            <div className="kb-eval-run-history-head">
              <div>
                <h4>运行历史</h4>
                <p>按时间倒序显示最近 {visibleRuns.length} 次运行</p>
              </div>
            </div>
            <div className="kb-eval-run-table-wrap">
              <table className="kb-eval-run-table">
                <thead>
                  <tr>
                    <th>运行时间</th>
                    <th>检索策略</th>
                    <th>Hit Rate</th>
                    <th>MRR</th>
                    <th>证据</th>
                    <th>低置信</th>
                    <th>P95</th>
                  </tr>
                </thead>
                <tbody>
                  {visibleRuns.map((run) => (
                    <tr key={run.runId}>
                      <td>
                        <strong>{run.datasetName || run.datasetId}</strong>
                        <span>{formatDateTime(run.startedAt)}</span>
                      </td>
                      <td>{evalRunModeLabel(run)}</td>
                      <td>{formatPercent(run.metrics.hitRate)}</td>
                      <td>{run.metrics.mrr.toFixed(3)}</td>
                      <td>{formatPercent(run.metrics.evidenceSupportRate ?? 0)}</td>
                      <td data-status={run.metrics.lowConfidence > 0 ? 'warning' : 'normal'}>{run.metrics.lowConfidence}</td>
                      <td data-status={run.metrics.latencyP95Ms > 8000 ? 'warning' : 'normal'}>{run.metrics.latencyP95Ms}ms</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </>
      )}
    </section>
  )
}

export default EvalRunTrendPanel
