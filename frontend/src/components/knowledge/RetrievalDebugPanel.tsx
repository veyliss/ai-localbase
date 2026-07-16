import React from 'react'
import type { RetrievalDebugResponse, RetrievalSearchMode } from '../../services/api'
import AppIcon from '../common/AppIcon'
import { chunkKindLabel, structuredIntentLabel } from './knowledgeLabels'

interface RetrievalDebugPanelProps {
  scopeLabel: string
  query: string
  searchMode: RetrievalSearchMode
  result: RetrievalDebugResponse | null
  error: string
  loading: boolean
  savingEvalCandidate: boolean
  evalCandidateSaveMessage: string
  onQueryChange: (value: string) => void
  onSearchModeChange: (value: RetrievalSearchMode) => void
  onRun: () => void
  onDownloadEvalCandidate: () => void
  onAddEvalCandidate: () => void
}

const searchModeOptions: Array<{ value: RetrievalSearchMode; label: string }> = [
  { value: 'auto', label: '自动' },
  { value: 'dense', label: '向量' },
  { value: 'hybrid', label: '混合' },
]

const resolvedSearchModeLabel = (mode?: string) => {
  if (mode === 'hybrid') return '混合检索'
  if (mode === 'dense') return '向量检索'
  return '自动检索'
}

const rerankStrategyLabel = (strategy?: string) => (
  strategy === 'semantic' ? '语义重排' : '关键词重排'
)

const retrievalChannelLabel = (channel: string) => {
  if (channel === 'dense') return '向量'
  if (channel === 'sparse') return '关键词'
  if (channel === 'lexical') return '词法'
  return channel
}

const retrievalContributionSummary = (result: RetrievalDebugResponse) => {
  const counts = result.items.reduce(
    (acc, item) => {
      const channels = item.retrievalChannels ?? []
      const hasDense = channels.includes('dense')
      const hasSparse = channels.includes('sparse')
      const hasLexical = channels.includes('lexical')
      if (hasDense && hasSparse) acc.both += 1
      else if (hasSparse) acc.sparseOnly += 1
      else if (hasDense) acc.denseOnly += 1
      else if (hasLexical) acc.lexicalOnly += 1
      else acc.unknown += 1
      return acc
    },
    { both: 0, denseOnly: 0, sparseOnly: 0, lexicalOnly: 0, unknown: 0 },
  )

  return [
    `双路命中 ${counts.both}`,
    `向量独有 ${counts.denseOnly}`,
    `关键词独有 ${counts.sparseOnly}`,
    counts.lexicalOnly > 0 ? `词法兜底 ${counts.lexicalOnly}` : '',
    counts.unknown > 0 ? `未标记 ${counts.unknown}` : '',
  ].filter(Boolean)
}

const retrievalChannelRankLabel = (item: RetrievalDebugResponse['items'][number]) => {
  const parts = []
  if (item.denseRank) parts.push(`向量 #${item.denseRank}`)
  if (item.sparseRank) parts.push(`关键词 #${item.sparseRank}`)
  return parts.join(' · ')
}

const formatDiagnosticScore = (value?: number) => (
  typeof value === 'number' && Number.isFinite(value) ? value.toFixed(4) : '-'
)

const formatDiagnosticPercent = (value?: number) => (
  typeof value === 'number' && Number.isFinite(value) ? `${(value * 100).toFixed(1)}%` : '-'
)

const highlightMatches = (text: string, query: string) => {
  const term = query.trim()
  if (term.length < 2) return text

  const matchIndex = text.toLocaleLowerCase().indexOf(term.toLocaleLowerCase())
  if (matchIndex < 0) return text

  return (
    <>
      {text.slice(0, matchIndex)}
      <mark>{text.slice(matchIndex, matchIndex + term.length)}</mark>
      {text.slice(matchIndex + term.length)}
    </>
  )
}

const RetrievalDebugPanel: React.FC<RetrievalDebugPanelProps> = ({
  scopeLabel,
  query,
  searchMode,
  result,
  error,
  loading,
  savingEvalCandidate,
  evalCandidateSaveMessage,
  onQueryChange,
  onSearchModeChange,
  onRun,
  onDownloadEvalCandidate,
  onAddEvalCandidate,
}) => (
  <section className="kb-retrieval-debug">
    <div className="kb-panel-section-head kb-retrieval-heading">
      <div>
        <h3>检索测试</h3>
        <p>验证问题会命中哪些文档和 Chunk，当前范围：{scopeLabel}</p>
      </div>
      {result && <span className="kb-retrieval-resolved-mode">{resolvedSearchModeLabel(result.searchMode)}</span>}
    </div>

    <div className="kb-retrieval-composer">
      <div className="kb-retrieval-input-row">
        <AppIcon name="search" size={17} />
        <input
          aria-label="检索测试问题"
          className="kb-retrieval-input"
          onChange={(event) => onQueryChange(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') onRun()
          }}
          placeholder="输入一个问题，例如：武汉大学创办于哪一年？"
          value={query}
        />
        <button className="kb-retrieval-run" disabled={loading || !query.trim()} onClick={onRun} type="button">
          {loading ? '检索中' : '运行检索'}
        </button>
      </div>
      <div className="kb-retrieval-options">
        <span>检索模式</span>
        <div aria-label="检索模式" className="kb-retrieval-mode-tabs" role="group">
          {searchModeOptions.map((option) => (
            <button
              aria-pressed={searchMode === option.value}
              className={searchMode === option.value ? 'active' : ''}
              disabled={loading}
              key={option.value}
              onClick={() => onSearchModeChange(option.value)}
              type="button"
            >
              {option.label}
            </button>
          ))}
        </div>
      </div>
    </div>

    {error && <div className="kb-retrieval-error">{error}</div>}

    {!result && !error && (
      <div className="kb-retrieval-idle">
        <AppIcon name="search" size={24} />
        <strong>{loading ? '正在检索' : '等待检索问题'}</strong>
        <span>{loading ? '正在召回和重排候选 Chunk' : '运行后将在这里显示命中排名和诊断信息'}</span>
      </div>
    )}

    {result && (
      <div className="kb-retrieval-result">
        <dl className="kb-retrieval-summary-grid">
          <div><dt>命中数量</dt><dd>{result.count}</dd></div>
          <div><dt>检索耗时</dt><dd>{result.elapsedMs} ms</dd></div>
          <div data-status={result.lowConfidence ? 'warning' : 'normal'}>
            <dt>置信状态</dt><dd>{result.lowConfidence ? '需要复核' : '正常'}</dd>
          </div>
          <div><dt>排序策略</dt><dd>{rerankStrategyLabel(result.rerankStrategy)}</dd></div>
        </dl>

        {result.confidence && (
          <div className={`kb-retrieval-confidence kb-retrieval-confidence--${result.confidence.status === 'low' ? 'low' : 'normal'}`}>
            <div>
              <strong>{result.confidence.status === 'low' ? '结果需要复核' : '置信诊断正常'}</strong>
              <p>{result.confidence.summary}</p>
            </div>
            <div className="kb-retrieval-confidence-metrics">
              <span>最高分 {formatDiagnosticScore(result.confidence.topScore)}</span>
              <span>平均分 {formatDiagnosticScore(result.confidence.averageScore)}</span>
              <span>证据覆盖 {formatDiagnosticPercent(result.confidence.evidenceCoverage)}</span>
            </div>
            {result.confidence.reasons && result.confidence.reasons.length > 0 && (
              <ul>
                {result.confidence.reasons.map((reason) => <li key={reason}>{reason}</li>)}
              </ul>
            )}
          </div>
        )}

        <div className="kb-retrieval-results-head">
          <div>
            <h4>命中结果</h4>
            <p>按最终排序分数展示，共 {result.items.length} 个 Chunk</p>
          </div>
          <span>{result.queryRewriteUsed ? '查询已改写' : '原始查询'} · {result.deterministicUsed ? '确定性补全' : '向量优先'}</span>
        </div>

        <div className="kb-retrieval-hits">
          {result.items.length === 0 ? (
            <div className="kb-docs-empty">没有命中 Chunk</div>
          ) : result.items.map((item, index) => (
            <article className="kb-retrieval-hit" key={item.id}>
              <div className="kb-retrieval-hit-head">
                <span className="kb-retrieval-rank">{index + 1}</span>
                <div>
                  <strong>{item.documentName}</strong>
                  <span>{chunkKindLabel(item.kind)} · Chunk #{item.index + 1}</span>
                </div>
                <div className="kb-retrieval-score">
                  <strong>{item.score.toFixed(4)}</strong>
                  <span>排序分数</span>
                </div>
              </div>
              <div className="kb-retrieval-hit-tags">
                {item.retrievalChannels?.map((channel) => (
                  <span key={channel}>{retrievalChannelLabel(channel)}</span>
                ))}
                {retrievalChannelRankLabel(item) && <span>{retrievalChannelRankLabel(item)}</span>}
                {item.matchReasons?.map((reason) => <span key={reason}>{reason}</span>)}
              </div>
              <p className="kb-retrieval-hit-text">{highlightMatches(item.text, query)}</p>
            </article>
          ))}
        </div>

        {result.evalCandidate && (
          <div className="kb-retrieval-eval">
            <div>
              <strong>可沉淀为评估样本</strong>
              <p>当前问题置信度较低，建议加入待审核评估集并人工复核答案证据。</p>
              {evalCandidateSaveMessage && <span>{evalCandidateSaveMessage}</span>}
            </div>
            <div className="kb-retrieval-eval-actions">
              <button disabled={savingEvalCandidate} onClick={onAddEvalCandidate} type="button">
                <AppIcon name="plus" size={15} />
                {savingEvalCandidate ? '加入中' : '加入待审核'}
              </button>
              <button onClick={onDownloadEvalCandidate} type="button">
                <AppIcon name="download" size={15} />
                下载样本
              </button>
            </div>
          </div>
        )}

        <details className="kb-retrieval-advanced">
          <summary>查看高级诊断</summary>
          <div className="kb-retrieval-advanced-body">
            <section>
              <h4>召回贡献</h4>
              <div className="kb-retrieval-contribution-list">
                {retrievalContributionSummary(result).map((item) => <span key={item}>{item}</span>)}
              </div>
            </section>

            {structuredIntentLabel(result.structuredIntent) && (
              <section>
                <h4>结构化意图</h4>
                <p>{structuredIntentLabel(result.structuredIntent)}{result.targetField ? `：${result.targetField}` : ''}</p>
              </section>
            )}

            {result.evidenceGate && (
              <section>
                <h4>证据门控</h4>
                <p>{result.evidenceGate.reason || (result.evidenceGate.enabled ? '已启用' : '未启用')}</p>
                {result.evidenceGate.enabled && (
                  <div className="kb-retrieval-gate-metrics">
                    <span>候选 {result.evidenceGate.candidateCount}</span>
                    <span>保留 {result.evidenceGate.selectedCount}</span>
                    <span>直接证据 {result.evidenceGate.directEvidenceCount}</span>
                    <span>过滤 {result.evidenceGate.removedCount}</span>
                  </div>
                )}
              </section>
            )}

            {result.contextPreview && (
              <section>
                <h4>上下文预览</h4>
                <pre>{result.contextPreview}</pre>
              </section>
            )}

            {result.trace && result.trace.length > 0 && (
              <section>
                <h4>处理说明</h4>
                <div className="kb-retrieval-trace-list">
                  {result.trace.map((step, index) => (
                    <span key={`${step.stage}-${index}`}>
                      {step.stage}：{step.reason || step.status}
                      {(step.inputCount || step.outputCount) ? `（${step.inputCount ?? '-'} -> ${step.outputCount ?? '-'}）` : ''}
                    </span>
                  ))}
                </div>
              </section>
            )}
          </div>
        </details>
      </div>
    )}
  </section>
)

export default RetrievalDebugPanel
