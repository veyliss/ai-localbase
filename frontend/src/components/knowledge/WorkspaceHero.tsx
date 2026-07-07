import React from 'react'
import type { KnowledgeBase } from '../../App'
import type { KnowledgeBaseHealthResponse } from '../../services/api'
import { healthStatusLabel } from './knowledgeLabels'
import KnowledgeIcon from './KnowledgeIcon'

interface WorkspaceHeroProps {
  knowledgeBase: KnowledgeBase
  health: KnowledgeBaseHealthResponse | undefined
  selectedScopeLabel: string
  generatingEvalDataset: boolean
  onUploadFiles: (e: React.ChangeEvent<HTMLInputElement>) => void
  onUploadDirectory: (e: React.ChangeEvent<HTMLInputElement>) => void
  onGenerateEvalDataset: () => void
  registerDirectoryInput: (element: HTMLInputElement | null) => void
}

const WorkspaceHero: React.FC<WorkspaceHeroProps> = ({
  knowledgeBase,
  health,
  selectedScopeLabel,
  generatingEvalDataset,
  onUploadFiles,
  onUploadDirectory,
  onGenerateEvalDataset,
  registerDirectoryInput,
}) => {
  const healthBadge = health ? healthStatusLabel(health.status) : null
  const metrics = health?.metrics
  const indexedCount = metrics?.indexedCount ?? knowledgeBase.documents.filter(d => d.status === 'indexed').length ?? 0

  return (
    <section className="kb-workspace-hero">
      <div className="kb-workspace-overview">
        <div className="kb-workspace-title">
          <span className="kb-workspace-kicker">当前知识库</span>
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
            <span>向量</span>
            <strong>{metrics?.vectorCount ?? '-'}</strong>
          </div>
          <div>
            <span>结构化</span>
            <strong>{metrics?.structuredRowCount ?? '-'}</strong>
          </div>
          <div>
            <span>范围</span>
            <strong>{selectedScopeLabel}</strong>
          </div>
        </div>
      </div>
      <div className="kb-workspace-actions">
        <label className="kb-upload-btn kb-upload-btn--primary" title="上传文档">
          <KnowledgeIcon name="upload" />
          <span>上传文件</span>
          <input
            type="file"
            multiple
            accept=".txt,.md,.pdf,.csv,.xlsx"
            className="hidden-input"
            onChange={onUploadFiles}
          />
        </label>
        <label className="kb-upload-btn kb-upload-btn--secondary" title="上传目录">
          <KnowledgeIcon name="folderPlus" />
          <span>上传目录</span>
          <input
            ref={registerDirectoryInput}
            type="file"
            multiple
            className="hidden-input"
            onChange={onUploadDirectory}
          />
        </label>
        <button
          className="kb-eval-btn"
          onClick={onGenerateEvalDataset}
          disabled={knowledgeBase.documents.length === 0 || generatingEvalDataset}
        >
          {generatingEvalDataset ? '生成中' : '生成评估集'}
        </button>
      </div>
    </section>
  )
}

export default WorkspaceHero
