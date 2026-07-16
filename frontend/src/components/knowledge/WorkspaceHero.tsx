import React, { useEffect, useRef, useState } from 'react'
import type { KnowledgeBase } from '../../App'
import type { KnowledgeBaseHealthResponse } from '../../services/api'
import { healthStatusLabel } from './knowledgeLabels'
import KnowledgeIcon from './KnowledgeIcon'

interface WorkspaceHeroProps {
  knowledgeBase: KnowledgeBase
  health: KnowledgeBaseHealthResponse | undefined
  selectedScopeLabel: string
  onUploadFiles: (e: React.ChangeEvent<HTMLInputElement>) => void
  onUploadDirectory: (e: React.ChangeEvent<HTMLInputElement>) => void
  registerDirectoryInput: (element: HTMLInputElement | null) => void
}

const WorkspaceHero: React.FC<WorkspaceHeroProps> = ({
  knowledgeBase,
  health,
  selectedScopeLabel,
  onUploadFiles,
  onUploadDirectory,
  registerDirectoryInput,
}) => {
  const [uploadMenuOpen, setUploadMenuOpen] = useState(false)
  const uploadMenuRef = useRef<HTMLDivElement | null>(null)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const directoryInputRef = useRef<HTMLInputElement | null>(null)
  const healthBadge = health ? healthStatusLabel(health.status) : null
  const metrics = health?.metrics
  const indexedCount = metrics?.indexedCount ?? knowledgeBase.documents.filter(d => d.status === 'indexed').length ?? 0

  useEffect(() => {
    if (!uploadMenuOpen) return

    const handlePointerDown = (event: PointerEvent) => {
      if (!uploadMenuRef.current?.contains(event.target as Node)) {
        setUploadMenuOpen(false)
      }
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setUploadMenuOpen(false)
      }
    }

    document.addEventListener('pointerdown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [uploadMenuOpen])

  const registerDirectory = (element: HTMLInputElement | null) => {
    directoryInputRef.current = element
    registerDirectoryInput(element)
  }

  return (
    <section className="kb-workspace-hero">
      <div className="kb-workspace-overview">
        <div className="kb-workspace-title">
          <div className="kb-workspace-title-row">
            <h3>{knowledgeBase.name}</h3>
            {healthBadge && (
              <span
                className="kb-workspace-health"
                style={{ color: healthBadge.color, background: healthBadge.bg }}
              >
                {healthBadge.text} · {health?.score}
              </span>
            )}
          </div>
          <p>{knowledgeBase.description || '未填写描述'}</p>
        </div>
        <div className="kb-workspace-metrics" aria-label="知识库索引概览">
          <div>
            <span>文档</span>
            <strong>{metrics?.documentCount ?? knowledgeBase.documents.length}</strong>
          </div>
          <div>
            <span>已索引</span>
            <strong>{indexedCount}</strong>
          </div>
          <div>
            <span>Chunks</span>
            <strong>{metrics?.chunkCount ?? '-'}</strong>
          </div>
          <div>
            <span>范围</span>
            <strong>{selectedScopeLabel}</strong>
          </div>
        </div>
      </div>
      <div className="kb-workspace-actions" ref={uploadMenuRef}>
        <button
          aria-expanded={uploadMenuOpen}
          aria-haspopup="menu"
          className="kb-upload-menu-trigger"
          onClick={() => setUploadMenuOpen((current) => !current)}
          type="button"
        >
          <KnowledgeIcon name="upload" />
          <span>上传</span>
          <KnowledgeIcon name="chevronDown" />
        </button>
        {uploadMenuOpen && (
          <div className="kb-upload-menu" role="menu">
            <button
              className="kb-upload-menu-item"
              onClick={() => {
                setUploadMenuOpen(false)
                fileInputRef.current?.click()
              }}
              role="menuitem"
              type="button"
            >
              <KnowledgeIcon name="file" />
              <span>
                <strong>上传文件</strong>
                <small>选择 PDF、文本或表格文件</small>
              </span>
            </button>
            <button
              className="kb-upload-menu-item"
              onClick={() => {
                setUploadMenuOpen(false)
                directoryInputRef.current?.click()
              }}
              role="menuitem"
              type="button"
            >
              <KnowledgeIcon name="folderPlus" />
              <span>
                <strong>上传目录</strong>
                <small>批量扫描并导入目录内容</small>
              </span>
            </button>
          </div>
        )}
        <input
          ref={fileInputRef}
          type="file"
          multiple
          accept=".txt,.md,.pdf,.csv,.xlsx"
          className="hidden-input"
          onChange={onUploadFiles}
        />
        <input
          ref={registerDirectory}
          type="file"
          multiple
          className="hidden-input"
          onChange={onUploadDirectory}
        />
      </div>
    </section>
  )
}

export default WorkspaceHero
