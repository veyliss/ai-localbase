import React, { useEffect, useMemo, useRef, useState } from 'react'
import type { DocumentDetailResponse } from '../../services/api'
import { useModalFocusTrap } from '../../hooks/useModalFocusTrap'
import AppIcon from '../common/AppIcon'
import { formatDocumentPreviewText, shouldUseRawDocumentPreview } from './documentPreviewText'
import { chunkKindLabel } from './knowledgeLabels'

interface DocumentDetailDialogProps {
  detail: DocumentDetailResponse | null
  error: string
  focusChunkId?: string | null
  onClose: () => void
}

const DETAIL_VIEWS = [
  { value: 'overview', label: '概览' },
  { value: 'source', label: '原文' },
  { value: 'chunks', label: 'Chunks' },
] as const

type DetailView = (typeof DETAIL_VIEWS)[number]['value']

const DocumentDetailDialog: React.FC<DocumentDetailDialogProps> = ({
  detail,
  error,
  focusChunkId,
  onClose,
}) => {
  const backdropRef = useRef<HTMLDivElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const focusChunkRef = useRef<HTMLDivElement | null>(null)
  const [activeView, setActiveView] = useState<DetailView>(focusChunkId ? 'chunks' : 'overview')
  const [rawContentView, setRawContentView] = useState<'readable' | 'raw'>('readable')

  useModalFocusTrap(backdropRef, {
    initialFocusRef: closeButtonRef,
    onClose,
  })

  useEffect(() => {
    setActiveView(focusChunkId ? 'chunks' : 'overview')
  }, [detail?.document.id, focusChunkId])

  useEffect(() => {
    if (activeView === 'chunks') {
      focusChunkRef.current?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }
  }, [activeView, detail?.document.id, focusChunkId])

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
            <span>文档详情</span>
            <h3 id="kb-detail-title">{detail?.document.name ?? '文档详情'}</h3>
            {detail?.document.indexedAt && (
              <p>最近索引 {new Date(detail.document.indexedAt).toLocaleString('zh-CN')}</p>
            )}
          </div>
          <button
            aria-label="关闭文档详情"
            className="kb-close-btn"
            onClick={onClose}
            ref={closeButtonRef}
            title="关闭文档详情"
            type="button"
          >
            <AppIcon name="x" size={18} />
          </button>
        </div>

        {!error && detail && (
          <div aria-label="文档详情视图" className="kb-detail-tabs" role="tablist">
            {DETAIL_VIEWS.map((view) => (
              <button
                aria-controls="kb-detail-panel"
                aria-selected={activeView === view.value}
                className={activeView === view.value ? 'active' : ''}
                id={`kb-detail-${view.value}-tab`}
                key={view.value}
                onClick={() => setActiveView(view.value)}
                role="tab"
                tabIndex={activeView === view.value ? 0 : -1}
                type="button"
              >
                {view.label}
                {view.value === 'chunks' && <span>{detail.chunks.length}</span>}
              </button>
            ))}
          </div>
        )}

        {error ? (
          <div className="kb-detail-error kb-detail-error--drawer">{error}</div>
        ) : detail && (
          <div
            aria-labelledby={`kb-detail-${activeView}-tab`}
            className="kb-detail-body"
            id="kb-detail-panel"
            role="tabpanel"
          >
            {detail.document.indexError && (
              <div className="kb-detail-error">索引错误：{detail.document.indexError}</div>
            )}

            {activeView === 'overview' && (
              <>
                <section className="kb-detail-section">
                  <h4>索引诊断</h4>
                  <dl className="kb-detail-facts">
                    <div><dt>原文字符</dt><dd>{detail.diagnostics.rawContentChars}</dd></div>
                    <div><dt>分块数量</dt><dd>{detail.diagnostics.chunkCount}</dd></div>
                    <div><dt>向量数量</dt><dd>{detail.diagnostics.vectorCount}</dd></div>
                    <div><dt>摘要块</dt><dd>{detail.diagnostics.summaryChunkCount}</dd></div>
                    <div><dt>数据行块</dt><dd>{detail.diagnostics.structuredRowCount}</dd></div>
                    <div><dt>向量服务</dt><dd>{detail.diagnostics.qdrantEnabled ? '已启用' : '未启用'}</dd></div>
                  </dl>
                </section>

                <section className="kb-detail-section">
                  <div className="kb-detail-section-head">
                    <h4>摘要预览</h4>
                    <button className="kb-detail-text-link" onClick={() => setActiveView('source')} type="button">
                      查看原文
                      <AppIcon name="chevronRight" size={14} />
                    </button>
                  </div>
                  <div className="kb-detail-summary">{detail.summary || '暂无摘要'}</div>
                </section>
              </>
            )}

            {activeView === 'source' && (
              <section className="kb-detail-section kb-detail-section--fill">
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
            )}

            {activeView === 'chunks' && (
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
                    <div className="kb-docs-empty">暂无 Chunk</div>
                  ) : detail.chunks.map((chunk) => {
                    const isFocused = Boolean(focusChunkId && chunk.id === focusChunkId)
                    return (
                      <div
                        className={`kb-detail-chunk${isFocused ? ' kb-detail-chunk--focused' : ''}`}
                        key={chunk.id}
                        ref={isFocused ? focusChunkRef : undefined}
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
                  })}
                </div>
              </section>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

export default DocumentDetailDialog
