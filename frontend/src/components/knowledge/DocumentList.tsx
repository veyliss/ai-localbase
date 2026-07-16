import React, { useEffect, useMemo, useState } from 'react'
import type { DocumentItem } from '../../App'
import type { KnowledgeBaseDocumentHealth } from '../../services/api'
import AppIcon, { type AppIconName } from '../common/AppIcon'
import { documentStatusLabel } from './knowledgeLabels'

interface DocumentListProps {
  documents: DocumentItem[]
  healthDocuments?: KnowledgeBaseDocumentHealth[]
  selectedDocumentId: string | null
  documentDetailLoadingId: string | null
  reindexingDocumentId: string | null
  onSelectDocument: (documentId: string | null) => void
  onOpenDocumentDetail: (documentId: string) => void
  onReindexDocument: (documentId: string) => void
  onRemoveDocument: (documentId: string) => void
}

type SortField = 'name' | 'size' | 'uploadedAt'
type SortOrder = 'asc' | 'desc'

const documentIconName = (name: string): AppIconName => {
  const extension = name.split('.').pop()?.toLowerCase()
  return extension === 'csv' || extension === 'xlsx' ? 'database' : 'file'
}

const documentIconKind = (name: string) => {
  const extension = name.split('.').pop()?.toLowerCase()
  if (extension === 'csv' || extension === 'xlsx') return 'data'
  if (extension === 'pdf') return 'pdf'
  return 'text'
}

const DocumentList: React.FC<DocumentListProps> = ({
  documents,
  healthDocuments = [],
  selectedDocumentId,
  documentDetailLoadingId,
  reindexingDocumentId,
  onSelectDocument,
  onOpenDocumentDetail,
  onReindexDocument,
  onRemoveDocument,
}) => {
  const [sortField, setSortField] = useState<SortField>('uploadedAt')
  const [sortOrder, setSortOrder] = useState<SortOrder>('desc')
  const [searchQuery, setSearchQuery] = useState('')
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [showBulkConfirm, setShowBulkConfirm] = useState(false)
  const [actionMenuId, setActionMenuId] = useState<string | null>(null)
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
  const documentIdsKey = documents.map((document) => document.id).join('|')

  const healthByDocumentId = useMemo(
    () => new Map(healthDocuments.map((item) => [item.documentId, item])),
    [healthDocuments],
  )

  useEffect(() => {
    setSelectedIds(new Set())
    setShowBulkConfirm(false)
    setActionMenuId(null)
    setDeleteConfirmId(null)
  }, [documentIdsKey])

  useEffect(() => {
    if (!actionMenuId) return

    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Element && target.closest(`[data-document-actions="${actionMenuId}"]`)) {
        return
      }
      setActionMenuId(null)
      setDeleteConfirmId(null)
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setActionMenuId(null)
    }

    document.addEventListener('pointerdown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [actionMenuId])

  const filteredAndSortedDocuments = useMemo(() => {
    const query = searchQuery.trim().toLowerCase()
    const filtered = query
      ? documents.filter((document) => (
        document.name.toLowerCase().includes(query) ||
        document.contentPreview?.toLowerCase().includes(query)
      ))
      : documents

    return [...filtered].sort((a, b) => {
      let comparison = 0
      if (sortField === 'name') comparison = a.name.localeCompare(b.name)
      if (sortField === 'size') comparison = (a.size || 0) - (b.size || 0)
      if (sortField === 'uploadedAt') {
        comparison = new Date(a.uploadedAt).getTime() - new Date(b.uploadedAt).getTime()
      }
      return sortOrder === 'asc' ? comparison : -comparison
    })
  }, [documents, searchQuery, sortField, sortOrder])

  const handleSortChange = (value: string) => {
    const [field, order] = value.split(':') as [SortField, SortOrder]
    setSortField(field)
    setSortOrder(order)
  }

  const handleToggleSelectAll = () => {
    if (selectedIds.size === filteredAndSortedDocuments.length) {
      setSelectedIds(new Set())
      return
    }
    setSelectedIds(new Set(filteredAndSortedDocuments.map((document) => document.id)))
  }

  const handleToggleSelect = (documentId: string) => {
    setSelectedIds((current) => {
      const next = new Set(current)
      if (next.has(documentId)) next.delete(documentId)
      else next.add(documentId)
      return next
    })
    setShowBulkConfirm(false)
  }

  const handleBulkDelete = () => {
    selectedIds.forEach(onRemoveDocument)
    setSelectedIds(new Set())
    setShowBulkConfirm(false)
  }

  const allSelected = filteredAndSortedDocuments.length > 0 && selectedIds.size === filteredAndSortedDocuments.length

  return (
    <section className="kb-docs-panel">
      <div className="kb-panel-section-head">
        <div>
          <h3>文档</h3>
          <p>{documents.length} 份文档 · 管理资料、索引状态和检索范围</p>
        </div>
      </div>

      <div className="kb-document-controls">
        <div className="kb-docs-search-wrapper">
          <AppIcon name="search" size={16} />
          <input
            aria-label="搜索文档"
            className="kb-docs-search"
            onChange={(event) => setSearchQuery(event.target.value)}
            placeholder="搜索文档名称或内容"
            type="search"
            value={searchQuery}
          />
        </div>

        <label className="kb-document-select">
          <span>检索范围</span>
          <select
            aria-label="检索范围"
            onChange={(event) => onSelectDocument(event.target.value || null)}
            value={selectedDocumentId ?? ''}
          >
            <option value="">全部文档</option>
            {documents.map((document) => (
              <option key={document.id} value={document.id}>{document.name}</option>
            ))}
          </select>
        </label>

        <label className="kb-document-select kb-document-select--sort">
          <span>排序</span>
          <select
            aria-label="文档排序"
            onChange={(event) => handleSortChange(event.target.value)}
            value={`${sortField}:${sortOrder}`}
          >
            <option value="uploadedAt:desc">最近上传</option>
            <option value="uploadedAt:asc">最早上传</option>
            <option value="name:asc">名称升序</option>
            <option value="name:desc">名称降序</option>
            <option value="size:desc">文件较大</option>
            <option value="size:asc">文件较小</option>
          </select>
        </label>

        {filteredAndSortedDocuments.length > 0 && (
          <label className="kb-bulk-checkbox">
            <input checked={allSelected} onChange={handleToggleSelectAll} type="checkbox" />
            <span>全选</span>
          </label>
        )}
      </div>

      {selectedIds.size > 0 && (
        <div className="kb-bulk-selection-bar">
          <span>已选择 <strong>{selectedIds.size}</strong> 份文档</span>
          {!showBulkConfirm ? (
            <button onClick={() => setShowBulkConfirm(true)} type="button">
              <AppIcon name="trash" size={15} />
              删除
            </button>
          ) : (
            <div className="kb-bulk-confirm">
              <span>确认删除所选文档？</span>
              <button className="kb-bulk-yes" onClick={handleBulkDelete} type="button">确认</button>
              <button className="kb-bulk-no" onClick={() => setShowBulkConfirm(false)} type="button">取消</button>
            </div>
          )}
        </div>
      )}

      <div className="kb-docs">
        {filteredAndSortedDocuments.length === 0 ? (
          <div className="kb-docs-empty">
            <AppIcon name={searchQuery ? 'search' : 'file'} size={22} />
            <strong>{searchQuery ? '没有匹配的文档' : '还没有文档'}</strong>
            <span>{searchQuery ? '尝试调整搜索关键词' : '使用右上角上传入口添加资料'}</span>
          </div>
        ) : filteredAndSortedDocuments.map((document) => {
          const badge = documentStatusLabel(document.status)
          const health = healthByDocumentId.get(document.id)
          const needsReindex = Boolean(health?.needsReindex || document.status === 'failed')
          const isSelected = selectedIds.has(document.id)
          const menuOpen = actionMenuId === document.id
          const confirmingDelete = deleteConfirmId === document.id

          return (
            <article
              className={`kb-doc-item${selectedDocumentId === document.id ? ' kb-doc-item--active' : ''}${needsReindex ? ' kb-doc-item--attention' : ''}${isSelected ? ' kb-doc-item--selected' : ''}`}
              key={document.id}
            >
              <label className="kb-doc-checkbox-wrapper" title={`选择 ${document.name}`}>
                <input
                  checked={isSelected}
                  className="kb-doc-checkbox"
                  onChange={() => handleToggleSelect(document.id)}
                  type="checkbox"
                />
              </label>

              <div className="kb-doc-file-icon" data-kind={documentIconKind(document.name)}>
                <AppIcon name={documentIconName(document.name)} size={18} />
              </div>

              <button className="kb-doc-main" onClick={() => onSelectDocument(document.id)} type="button">
                <div className="kb-doc-top">
                  <span className="kb-doc-name">{document.name}</span>
                  <span className="kb-doc-badge" style={{ color: badge.color, background: badge.bg }}>
                    {badge.text}
                  </span>
                </div>
                {document.contentPreview && <p className="kb-doc-preview">{document.contentPreview}</p>}
                <div className="kb-doc-health-row">
                  <span>{health?.rawContentAvailable ? '原文可用' : '原文缺失'}</span>
                  <span>{health?.vectorCount ?? 0} 向量</span>
                  {typeof health?.structuredRowCount === 'number' && health.structuredRowCount > 0 && (
                    <span>{health.structuredRowCount} 数据行</span>
                  )}
                  {needsReindex && <span className="kb-doc-health-warning">需要处理</span>}
                </div>
                {(document.indexError || health?.recommendation) && (
                  <p className="kb-doc-issue">
                    {document.indexError ? `索引失败：${document.indexError}` : health?.recommendation}
                  </p>
                )}
              </button>

              <div className="kb-doc-side-meta">
                <strong>{document.sizeLabel}</strong>
                <span>{document.chunkCount ?? 0} chunks</span>
                <span>{new Date(document.uploadedAt).toLocaleDateString('zh-CN')}</span>
              </div>

              <div className="kb-doc-actions" data-document-actions={document.id}>
                {needsReindex && (
                  <button
                    aria-label={`重新解析并重建 ${document.name}`}
                    className="kb-doc-attention-action"
                    disabled={reindexingDocumentId === document.id}
                    onClick={() => onReindexDocument(document.id)}
                    title="重新解析并重建索引"
                    type="button"
                  >
                    <AppIcon className={reindexingDocumentId === document.id ? 'spin' : undefined} name="refresh" size={16} />
                  </button>
                )}
                <button
                  aria-expanded={menuOpen}
                  aria-haspopup="menu"
                  aria-label={`打开 ${document.name} 的操作菜单`}
                  className="kb-doc-more"
                  onClick={() => setActionMenuId((current) => current === document.id ? null : document.id)}
                  title="更多操作"
                  type="button"
                >
                  <AppIcon name="more" size={17} />
                </button>
                {menuOpen && (
                  <div
                    aria-label={confirmingDelete ? '确认删除文档' : undefined}
                    className="kb-doc-menu"
                    role={confirmingDelete ? 'alertdialog' : 'menu'}
                  >
                    {confirmingDelete ? (
                      <div className="kb-doc-menu-confirm">
                        <strong>删除这份文档？</strong>
                        <span>该操作会移除文档及其索引。</span>
                        <div>
                          <button
                            className="kb-doc-menu-confirm-delete"
                            onClick={() => {
                              setActionMenuId(null)
                              setDeleteConfirmId(null)
                              onRemoveDocument(document.id)
                            }}
                            type="button"
                          >
                            确认删除
                          </button>
                          <button onClick={() => setDeleteConfirmId(null)} type="button">取消</button>
                        </div>
                      </div>
                    ) : (
                      <>
                        <button
                          disabled={documentDetailLoadingId === document.id}
                          onClick={() => {
                            setActionMenuId(null)
                            onOpenDocumentDetail(document.id)
                          }}
                          role="menuitem"
                          type="button"
                        >
                          <AppIcon name="eye" size={16} />
                          <span>{documentDetailLoadingId === document.id ? '加载中' : '查看详情'}</span>
                        </button>
                        <button
                          disabled={reindexingDocumentId === document.id}
                          onClick={() => {
                            setActionMenuId(null)
                            onReindexDocument(document.id)
                          }}
                          role="menuitem"
                          type="button"
                        >
                          <AppIcon className={reindexingDocumentId === document.id ? 'spin' : undefined} name="refresh" size={16} />
                          <span>{reindexingDocumentId === document.id ? '重建中' : '重新建立索引'}</span>
                        </button>
                        <button
                          className="kb-doc-menu-danger"
                          onClick={() => setDeleteConfirmId(document.id)}
                          role="menuitem"
                          type="button"
                        >
                          <AppIcon name="trash" size={16} />
                          <span>删除文档</span>
                        </button>
                      </>
                    )}
                  </div>
                )}
              </div>
            </article>
          )
        })}
      </div>
    </section>
  )
}

export default DocumentList
