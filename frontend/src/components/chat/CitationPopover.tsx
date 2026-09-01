import React, { useState } from 'react'
import type { ChatSourceMetadata } from '../../App'

interface CitationPopoverProps {
  source: ChatSourceMetadata
  isOpen: boolean
  onClose: () => void
  onNavigateToDocument?: (knowledgeBaseId: string, documentId: string, chunkId?: string) => void
}

const CitationPopover: React.FC<CitationPopoverProps> = ({
  source,
  isOpen,
  onClose,
  onNavigateToDocument,
}) => {
  const [copied, setCopied] = useState(false)
  const citationText = source.citationSnippet || source.snippet || ''

  if (!isOpen) return null

  const handleCopySnippet = async () => {
    if (!citationText) return
    try {
      await navigator.clipboard.writeText(citationText)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // 忽略复制失败
    }
  }

  const handleNavigate = () => {
    if (source.knowledgeBaseId && source.documentId && onNavigateToDocument) {
      onNavigateToDocument(source.knowledgeBaseId, source.documentId, source.chunkId)
      onClose()
    }
  }

  return (
    <div className="citation-popover-overlay" onClick={onClose}>
      <div className="citation-popover" onClick={(e) => e.stopPropagation()}>
        <div className="citation-popover-header">
          <h3>引用来源详情</h3>
          <button
            type="button"
            className="citation-popover-close"
            onClick={onClose}
            aria-label="关闭"
          >
            ✕
          </button>
        </div>

        <div className="citation-popover-body">
          <div className="citation-field">
            <label>文档名称</label>
            <div className="citation-value">
              {source.documentName || '未知来源'}
            </div>
          </div>

          {source.chunkKind && (
            <div className="citation-field">
              <label>块类型</label>
              <div className="citation-value">{source.chunkKind}</div>
            </div>
          )}

          {source.chunkIndex && (
            <div className="citation-field">
              <label>块索引</label>
              <div className="citation-value">#{source.chunkIndex}</div>
            </div>
          )}

          {source.evidenceId && (
            <div className="citation-field">
              <label>证据 ID</label>
              <div className="citation-value">{source.evidenceId}</div>
            </div>
          )}

          {(source.lineStart || source.charStart || source.tableRow) && (
            <div className="citation-field">
              <label>原文位置</label>
              <div className="citation-value">
                {source.lineStart
                  ? `第 ${source.lineStart}-${source.lineEnd || source.lineStart} 行`
                  : source.charStart
                    ? `字符 ${source.charStart}-${source.charEnd || source.charStart}`
                    : ''}
                {source.tableRow ? `，表格第 ${source.tableRow} 行` : ''}
              </div>
            </div>
          )}

          {source.tableColumns && (
            <div className="citation-field">
              <label>表格字段</label>
              <div className="citation-value">{source.tableColumns}</div>
            </div>
          )}

          {source.score && (
            <div className="citation-field">
              <label>相关度分数</label>
              <div className="citation-value">{Number(source.score).toFixed(4)}</div>
            </div>
          )}

          {citationText && (
            <div className="citation-field">
              <label>{source.citationSnippet ? '支撑证据' : '内容片段'}</label>
              <div className="citation-snippet">
                {citationText}
                <button
                  type="button"
                  className="citation-copy-btn"
                  onClick={() => {
                    void handleCopySnippet()
                  }}
                  title={copied ? '已复制' : '复制内容'}
                >
                  {copied ? '✓' : '⧉'}
                </button>
              </div>
            </div>
          )}
          {source.citationSnippet && source.snippet && source.citationSnippet !== source.snippet && (
            <details className="citation-field citation-full-snippet">
              <summary>查看完整召回片段</summary>
              <div className="citation-value">{source.snippet}</div>
            </details>
          )}
        </div>

        <div className="citation-popover-footer">
          {source.documentId && onNavigateToDocument && (
            <button
              type="button"
              className="citation-navigate-btn"
              onClick={handleNavigate}
            >
              跳转到文档详情
            </button>
          )}
          <button
            type="button"
            className="citation-close-btn"
            onClick={onClose}
          >
            关闭
          </button>
        </div>
      </div>
    </div>
  )
}

export default CitationPopover
