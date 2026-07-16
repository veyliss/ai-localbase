import React, { useEffect, useMemo, useRef, useState } from 'react'
import type { DocumentDetailResponse } from '../../services/api'
import { useModalFocusTrap } from '../../hooks/useModalFocusTrap'
import { formatDocumentPreviewText, shouldUseRawDocumentPreview } from './documentPreviewText'
import { chunkKindLabel } from './knowledgeLabels'

interface DocumentDetailDialogProps {
  detail: DocumentDetailResponse | null
  error: string
  focusChunkId?: string | null
  onClose: () => void
}

const DocumentDetailDialog: React.FC<DocumentDetailDialogProps> = ({
  detail,
  error,
  focusChunkId,
  onClose,
}) => {
  const backdropRef = useRef<HTMLDivElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const focusChunkRef = useRef<HTMLDivElement | null>(null)
  const [rawContentView, setRawContentView] = useState<'readable' | 'raw'>('readable')

  useModalFocusTrap(backdropRef, {
    initialFocusRef: closeButtonRef,
    onClose,
  })

  useEffect(() => {
    focusChunkRef.current?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }, [detail?.document.id, focusChunkId])

  useEffect(() => {
    setRawContentView(shouldUseRawDocumentPreview(detail?.document.name ?? '') ? 'raw' : 'readable')
  }, [detail?.document.id, detail?.document.name])

  const hasFocusedChunk = Boolean(
    detail?.chunks.some((chunk) => focusChunkId && chunk.id === focusChunkId),
  )
  const readableRawContent = useMemo(
    () => formatDocumentPreviewText(detail?.rawContent ?? ''),
    [detail?.rawContent],
  )

  return (
    <div className="kb-detail-backdrop" onClick={onClose} ref={backdropRef}>
      <div
        aria-labelledby="kb-detail-title"
        aria-modal="true"
        className="kb-detail-dialog"
        onClick={(event) => event.stopPropagation()}
        role="dialog"
      >
        <div className="kb-detail-header">
          <div>
            <h3 id="kb-detail-title">{detail?.document.name ?? '文档详情'}</h3>
            {detail?.document.indexedAt && (
              <p>最近索引：{new Date(detail.document.indexedAt).toLocaleString('zh-CN')}</p>
            )}
          </div>
          <button
            className="kb-close-btn"
            onClick={onClose}
            ref={closeButtonRef}
            type="button"
            aria-label="关闭文档详情"
            title="关闭文档详情"
          >
            x
          </button>
        </div>

        {error ? (
          <div className="kb-detail-error">{error}</div>
        ) : detail && (
          <div className="kb-detail-body">
            <section className="kb-detail-section">
              <div className="kb-detail-stats">
                <div><span>原文字符</span><strong>{detail.diagnostics.rawContentChars}</strong></div>
                <div><span>分块数量</span><strong>{detail.diagnostics.chunkCount}</strong></div>
                <div><span>向量数量</span><strong>{detail.diagnostics.vectorCount}</strong></div>
                <div><span>摘要块</span><strong>{detail.diagnostics.summaryChunkCount}</strong></div>
                <div><span>数据行块</span><strong>{detail.diagnostics.structuredRowCount}</strong></div>
                <div><span>Qdrant</span><strong>{detail.diagnostics.qdrantEnabled ? '启用' : '未启用'}</strong></div>
              </div>
              {detail.document.indexError && (
                <div className="kb-detail-error">索引错误：{detail.document.indexError}</div>
              )}
            </section>

            <section className="kb-detail-section">
              <h4>摘要预览</h4>
              <pre className="kb-detail-pre">{detail.summary || '暂无摘要'}</pre>
            </section>

            <section className="kb-detail-section">
              <div className="kb-detail-section-head">
                <h4>
                  原文预览
                  {detail.diagnostics.rawContentTruncated && <span className="kb-detail-muted">已截断</span>}
                </h4>
                <div className="kb-detail-view-toggle" role="group" aria-label="原文显示方式">
                  <button
                    aria-pressed={rawContentView === 'readable'}
                    className={rawContentView === 'readable' ? 'active' : ''}
                    onClick={() => setRawContentView('readable')}
                    type="button"
                  >
                    整理排版
                  </button>
                  <button
                    aria-pressed={rawContentView === 'raw'}
                    className={rawContentView === 'raw' ? 'active' : ''}
                    onClick={() => setRawContentView('raw')}
                    type="button"
                  >
                    原始文本
                  </button>
                </div>
              </div>
              <pre className={`kb-detail-pre kb-detail-pre--${rawContentView}`}>
                {rawContentView === 'readable'
                  ? readableRawContent || '暂无可读取原文'
                  : detail.rawContent || '暂无可读取原文'}
              </pre>
            </section>

            <section className="kb-detail-section">
              <h4>
                Chunk 预览
                {detail.diagnostics.chunkPreviewTruncated && (
                  <span className="kb-detail-muted">
                    {hasFocusedChunk ? '已包含引用定位片段' : `仅显示前 ${detail.chunks.length} 个`}
                  </span>
                )}
              </h4>
              <div className="kb-detail-chunks">
                {detail.chunks.length === 0 ? (
                  <div className="kb-docs-empty">暂无 chunk</div>
                ) : (
                  detail.chunks.map((chunk) => {
                    const isFocused = Boolean(focusChunkId && chunk.id === focusChunkId)
                    return (
                      <div
                        key={chunk.id}
                        ref={isFocused ? focusChunkRef : undefined}
                        className={`kb-detail-chunk${isFocused ? ' kb-detail-chunk--focused' : ''}`}
                      >
                        <div className="kb-detail-chunk-head">
                          <span>#{chunk.index + 1}</span>
                          <span>{chunkKindLabel(chunk.kind)}</span>
                          {isFocused && <span>引用定位</span>}
                          <code>{chunk.id}</code>
                        </div>
                        <pre>{chunk.text}</pre>
                      </div>
                    )
                  })
                )}
              </div>
            </section>
          </div>
        )}
      </div>
    </div>
  )
}

export default DocumentDetailDialog
