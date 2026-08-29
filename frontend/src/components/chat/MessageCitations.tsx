import React from 'react'
import type { ChatSourceMetadata, CitationSupportMetadata } from '../../App'
import { chunkKindLabel } from '../knowledge/knowledgeLabels'
import { filterDocumentCitationSources } from './citationSources'

interface MessageCitationsProps {
  sources: ChatSourceMetadata[]
  citationSupport?: CitationSupportMetadata
  onOpenCitationSource?: (source: ChatSourceMetadata) => void
}

const sourceIdentity = (source: ChatSourceMetadata, index: number) =>
  [
    source.knowledgeBaseId,
    source.documentId,
    source.chunkId,
    source.chunkIndex,
  ].filter(Boolean).join(':') || `source-${index}`

const normalizeSources = (sources?: ChatSourceMetadata[]) => {
  if (!sources || sources.length === 0) return []
  const seen = new Set<string>()
  return filterDocumentCitationSources(sources).filter((source, index) => {
    const key = sourceIdentity(source, index)
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

const sourceTypeLabel = (source: ChatSourceMetadata) => {
  if (source.sourceType === 'structured-data') return '结构化数据'
  if (source.chunkKind) return chunkKindLabel(source.chunkKind)
  return '来源'
}

const sourceRankLabel = (source: ChatSourceMetadata, index: number) => {
  if (source.chunkIndex) return `#${source.chunkIndex}`
  return `#${index + 1}`
}

const scoreLabel = (score?: string) => {
  if (!score) return ''
  const value = Number(score)
  if (!Number.isFinite(value)) return ''
  return `分数 ${value.toFixed(4)}`
}

const supportLabel = (status?: CitationSupportMetadata['status']) => {
  switch (status) {
    case 'supported':
      return '答案已被引用片段完整支撑'
    case 'partial':
      return '仅部分答案被引用片段支撑'
    case 'abstained':
      return '回答未声明有确定证据'
    default:
      return '引用片段未能支撑答案'
  }
}

const MessageCitations: React.FC<MessageCitationsProps> = ({
  sources,
  citationSupport,
  onOpenCitationSource,
}) => {
  const visibleSources = normalizeSources(sources).slice(0, 6)
  if (visibleSources.length === 0) return null

  return (
    <details className="message-citations">
      <summary>
        <span>引用来源</span>
        <strong>{visibleSources.length}</strong>
      </summary>
      {citationSupport && (
        <div className={`message-citation-support message-citation-support-${citationSupport.status}`}>
          <strong>{supportLabel(citationSupport.status)}</strong>
          <span>
            {citationSupport.supportedClaimCount}/{citationSupport.claimCount} 条陈述已核对
          </span>
          {citationSupport.status === 'partial' && <span>{citationSupport.summary}</span>}
        </div>
      )}
      <div className="message-citation-list">
        {visibleSources.map((source, index) => (
          <article className="message-citation" key={sourceIdentity(source, index)}>
            <div className="message-citation-head">
              <strong>{source.documentName || '未知来源'}</strong>
              <span>{sourceTypeLabel(source)}</span>
              <span>{sourceRankLabel(source, index)}</span>
              {scoreLabel(source.score) && <span>{scoreLabel(source.score)}</span>}
              {source.documentId && (
                <button
                  type="button"
                  onClick={() => onOpenCitationSource?.(source)}
                  disabled={!onOpenCitationSource}
                >
                  定位
                </button>
              )}
            </div>
            {source.snippet && <p>{source.snippet}</p>}
          </article>
        ))}
      </div>
    </details>
  )
}

export default MessageCitations
