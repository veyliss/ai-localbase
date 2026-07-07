import React, { useState, useMemo } from 'react'
import type { DocumentItem } from '../../App'
import type { KnowledgeBaseDocumentHealth } from '../../services/api'
import KnowledgeIcon from './KnowledgeIcon'
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

const SortIndicator: React.FC<{ active: boolean; order: SortOrder }> = ({ active, order }) => {
  if (!active) return null
  return (
    <span className="kb-sort-indicator">
      <KnowledgeIcon name={order === 'asc' ? 'chevronUp' : 'chevronDown'} />
    </span>
  )
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

  const healthByDocumentId = new Map(healthDocuments.map((item) => [item.documentId, item]))

  const handleSort = (field: SortField) => {
    if (sortField === field) {
      setSortOrder(sortOrder === 'asc' ? 'desc' : 'asc')
    } else {
      setSortField(field)
      setSortOrder('asc')
    }
  }

  const filteredAndSortedDocuments = useMemo(() => {
    let filtered = documents

    if (searchQuery.trim()) {
      const query = searchQuery.toLowerCase()
      filtered = documents.filter(
        (doc) =>
          doc.name.toLowerCase().includes(query) ||
          (doc.contentPreview && doc.contentPreview.toLowerCase().includes(query))
      )
    }

    const sorted = [...filtered].sort((a, b) => {
      let comparison = 0

      switch (sortField) {
        case 'name':
          comparison = a.name.localeCompare(b.name)
          break
        case 'size':
          comparison = (a.size || 0) - (b.size || 0)
          break
        case 'uploadedAt':
          comparison = new Date(a.uploadedAt).getTime() - new Date(b.uploadedAt).getTime()
          break
      }

      return sortOrder === 'asc' ? comparison : -comparison
    })

    return sorted
  }, [documents, searchQuery, sortField, sortOrder])

  const handleToggleSelectAll = () => {
    if (selectedIds.size === filteredAndSortedDocuments.length) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(filteredAndSortedDocuments.map((doc) => doc.id)))
    }
  }

  const handleToggleSelect = (documentId: string) => {
    const newSelected = new Set(selectedIds)
    if (newSelected.has(documentId)) {
      newSelected.delete(documentId)
    } else {
      newSelected.add(documentId)
    }
    setSelectedIds(newSelected)
  }

  const handleBulkDelete = () => {
    selectedIds.forEach((id) => {
      onRemoveDocument(id)
    })
    setSelectedIds(new Set())
    setShowBulkConfirm(false)
  }

  const allSelected = filteredAndSortedDocuments.length > 0 && selectedIds.size === filteredAndSortedDocuments.length

  return (
    <section className="kb-docs-panel">
      <div className="kb-panel-section-head">
        <div>
          <h3>文档</h3>
          <p>{documents.length} 份文档 · 查看索引状态和查询范围</p>
        </div>
      </div>

      <div className="kb-scope-bar" aria-label="检索范围">
        <span className="kb-scope-label">范围</span>
        <button
          className={`kb-scope-btn${selectedDocumentId === null ? ' kb-scope-btn--active' : ''}`}
          type="button"
          onClick={() => onSelectDocument(null)}
          aria-pressed={selectedDocumentId === null}
        >
          全部文档
        </button>
        {documents.map((document) => (
          <button
            key={document.id}
            className={`kb-scope-btn${selectedDocumentId === document.id ? ' kb-scope-btn--active' : ''}`}
            type="button"
            onClick={() => onSelectDocument(document.id)}
            aria-pressed={selectedDocumentId === document.id}
          >
            {document.name}
          </button>
        ))}
      </div>

      <div className="kb-docs-toolbar">
        <div className="kb-docs-search-wrapper">
          <input
            type="text"
            className="kb-docs-search"
            placeholder="搜索文档名称或内容..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
        </div>

        <div className="kb-docs-sort">
          <button
            className={`kb-sort-btn${sortField === 'name' ? ' kb-sort-btn--active' : ''}`}
            type="button"
            onClick={() => handleSort('name')}
            aria-pressed={sortField === 'name'}
            title="按名称排序"
          >
            <span>名称</span>
            <SortIndicator active={sortField === 'name'} order={sortOrder} />
          </button>
          <button
            className={`kb-sort-btn${sortField === 'size' ? ' kb-sort-btn--active' : ''}`}
            type="button"
            onClick={() => handleSort('size')}
            aria-pressed={sortField === 'size'}
            title="按大小排序"
          >
            <span>大小</span>
            <SortIndicator active={sortField === 'size'} order={sortOrder} />
          </button>
          <button
            className={`kb-sort-btn${sortField === 'uploadedAt' ? ' kb-sort-btn--active' : ''}`}
            type="button"
            onClick={() => handleSort('uploadedAt')}
            aria-pressed={sortField === 'uploadedAt'}
            title="按上传时间排序"
          >
            <span>时间</span>
            <SortIndicator active={sortField === 'uploadedAt'} order={sortOrder} />
          </button>
        </div>

        {filteredAndSortedDocuments.length > 0 && (
          <div className="kb-docs-bulk">
            <label className="kb-bulk-checkbox">
              <input
                type="checkbox"
                checked={allSelected}
                onChange={handleToggleSelectAll}
              />
              <span>全选</span>
            </label>
            {selectedIds.size > 0 && (
              <>
                {!showBulkConfirm ? (
                  <button
                    className="kb-bulk-delete-btn"
                    onClick={() => setShowBulkConfirm(true)}
                  >
                    删除 ({selectedIds.size})
                  </button>
                ) : (
                  <div className="kb-bulk-confirm">
                    <span>确认删除 {selectedIds.size} 份文档?</span>
                    <button className="kb-bulk-yes" onClick={handleBulkDelete}>
                      确认
                    </button>
                    <button className="kb-bulk-no" onClick={() => setShowBulkConfirm(false)}>
                      取消
                    </button>
                  </div>
                )}
              </>
            )}
          </div>
        )}
      </div>

      <div className="kb-docs">
        {filteredAndSortedDocuments.length === 0 ? (
          <div className="kb-docs-empty">
            <span>{searchQuery ? '未找到匹配的文档' : '暂无文档，点击上传添加文件'}</span>
          </div>
        ) : (
          filteredAndSortedDocuments.map((document) => {
            const badge = documentStatusLabel(document.status)
            const health = healthByDocumentId.get(document.id)
            const needsReindex = Boolean(health?.needsReindex || document.status === 'failed')
            const isSelected = selectedIds.has(document.id)

            return (
              <div
                key={document.id}
                className={`kb-doc-item${selectedDocumentId === document.id ? ' kb-doc-item--active' : ''}${needsReindex ? ' kb-doc-item--attention' : ''}${isSelected ? ' kb-doc-item--selected' : ''}`}
              >
                <div className="kb-doc-checkbox-wrapper">
                  <input
                    type="checkbox"
                    className="kb-doc-checkbox"
                    checked={isSelected}
                    onChange={() => handleToggleSelect(document.id)}
                    onClick={(e) => e.stopPropagation()}
                  />
                </div>
                <button className="kb-doc-main" onClick={() => onSelectDocument(document.id)}>
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
                    <span>{health?.structuredRowCount ?? 0} 数据行</span>
                    {needsReindex && <span className="kb-doc-health-warning">需处理</span>}
                  </div>
                  {(document.indexError || health?.recommendation) && (
                    <p className="kb-doc-issue">
                      {document.indexError ? `索引失败：${document.indexError}` : health?.recommendation}
                    </p>
                  )}
                  <div className="kb-doc-meta">
                    <span>{document.sizeLabel}</span>
                    {typeof document.chunkCount === 'number' && (
                      <>
                        <span>·</span>
                        <span>{document.chunkCount} chunks</span>
                      </>
                    )}
                    <span>·</span>
                    <span>{new Date(document.uploadedAt).toLocaleDateString('zh-CN')}</span>
                  </div>
                </button>
                <div className="kb-doc-actions">
                  <button
                    className="kb-doc-action"
                    type="button"
                    onClick={() => onOpenDocumentDetail(document.id)}
                    disabled={documentDetailLoadingId === document.id}
                    aria-label={`查看 ${document.name} 的索引详情`}
                    title="查看索引详情"
                  >
                    {documentDetailLoadingId === document.id ? '加载' : '详情'}
                  </button>
                  <button
                    className="kb-doc-action"
                    type="button"
                    onClick={() => onReindexDocument(document.id)}
                    disabled={reindexingDocumentId === document.id}
                    aria-label={`重新解析并重建 ${document.name}`}
                    title="重新解析并重建索引"
                  >
                    {reindexingDocumentId === document.id ? '重建中' : '重建'}
                  </button>
                  <button
                    className="kb-doc-remove"
                    type="button"
                    onClick={() => onRemoveDocument(document.id)}
                    aria-label={`删除文档 ${document.name}`}
                    title="删除文档"
                  >
                    <KnowledgeIcon name="trash" />
                  </button>
                </div>
              </div>
            )
          })
        )}
      </div>
    </section>
  )
}

export default DocumentList
