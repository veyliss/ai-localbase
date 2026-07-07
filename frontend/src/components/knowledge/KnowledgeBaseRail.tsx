import React from 'react'
import type { KnowledgeBase } from '../../App'
import type { KnowledgeBaseHealthResponse } from '../../services/api'
import { healthStatusLabel } from './knowledgeLabels'
import KnowledgeIcon from './KnowledgeIcon'

interface KnowledgeBaseRailProps {
  knowledgeBases: KnowledgeBase[]
  selectedKnowledgeBaseId: string | null
  healthByKnowledgeBase: Record<string, KnowledgeBaseHealthResponse>
  deleteConfirmId: string | null
  onSelectKnowledgeBase: (knowledgeBaseId: string) => void
  onDeleteKnowledgeBase: (knowledgeBaseId: string) => void
  onSetDeleteConfirmId: (knowledgeBaseId: string | null) => void
  onCreate: () => void
}

const KnowledgeBaseRail: React.FC<KnowledgeBaseRailProps> = ({
  knowledgeBases,
  selectedKnowledgeBaseId,
  healthByKnowledgeBase,
  deleteConfirmId,
  onSelectKnowledgeBase,
  onDeleteKnowledgeBase,
  onSetDeleteConfirmId,
  onCreate,
}) => (
  <aside className="kb-rail">
    <div className="kb-rail-head">
      <div>
        <h3>知识库</h3>
        <p>{knowledgeBases.length} 个空间</p>
      </div>
      <button className="kb-rail-create" onClick={onCreate} title="新建知识库" aria-label="新建知识库">
        <KnowledgeIcon name="plus" />
      </button>
    </div>

    <div className="kb-rail-list">
      {knowledgeBases.map((knowledgeBase) => {
        const health = healthByKnowledgeBase[knowledgeBase.id]
        const badge = health ? healthStatusLabel(health.status) : null
        const isSelected = selectedKnowledgeBaseId === knowledgeBase.id
        return (
          <div
            key={knowledgeBase.id}
            className={`kb-rail-item${isSelected ? ' kb-rail-item--active' : ''}`}
          >
            <button className="kb-rail-main" onClick={() => onSelectKnowledgeBase(knowledgeBase.id)}>
              <span className="kb-rail-name">{knowledgeBase.name}</span>
              <span className="kb-rail-meta">
                {knowledgeBase.documents.length} 份文档
                {badge && (
                  <span className="kb-rail-health" style={{ color: badge.color, background: badge.bg }}>
                    {badge.text}
                  </span>
                )}
              </span>
            </button>
            {deleteConfirmId === knowledgeBase.id ? (
              <div className="kb-rail-delete-confirm">
                <button
                  className="kb-delete-yes"
                  type="button"
                  onClick={() => {
                    onDeleteKnowledgeBase(knowledgeBase.id)
                    onSetDeleteConfirmId(null)
                  }}
                  aria-label={`确认删除知识库 ${knowledgeBase.name}`}
                >
                  删除
                </button>
                <button
                  className="kb-delete-no"
                  type="button"
                  onClick={() => onSetDeleteConfirmId(null)}
                  aria-label={`取消删除知识库 ${knowledgeBase.name}`}
                >
                  取消
                </button>
              </div>
            ) : (
              <button
                className="kb-rail-delete"
                type="button"
                onClick={() => onSetDeleteConfirmId(knowledgeBase.id)}
                aria-label={`删除知识库 ${knowledgeBase.name}`}
                title="删除知识库"
              >
                <KnowledgeIcon name="trash" />
              </button>
            )}
          </div>
        )
      })}
    </div>
  </aside>
)

export default KnowledgeBaseRail
