import React, { useMemo, useState } from 'react'
import type { EvalDatasetDetail, EvalGroundTruthCase, GenerateEvalDatasetResponse } from '../../services/api'

type EvalDatasetDialogDataset = GenerateEvalDatasetResponse | EvalDatasetDetail

interface EvalDatasetDialogProps {
  dataset: EvalDatasetDialogDataset
  scopeName: string
  onClose: () => void
  onUpdateItem?: (
    datasetId: string,
    itemId: string,
    item: EvalGroundTruthCase,
  ) => Promise<EvalGroundTruthCase>
  onDeleteItem?: (datasetId: string, itemId: string) => Promise<void>
}

interface EvalItemDraft {
  question: string
  answer: string
  snippets: string
  answerType: string
  difficulty: string
  reviewStatus: string
  disabled: boolean
  notes: string
}

const difficultyLabel: Record<string, string> = {
  easy: '简单',
  medium: '中等',
  hard: '困难',
}

const answerTypeLabel: Record<string, string> = {
  numeric: '数值',
  listing: '列表',
  process: '流程',
  extractive: '摘录',
  'retrieval-debug-candidate': '调试候选',
}

const reviewStatusLabel: Record<string, string> = {
  pending: '待审核',
  approved: '已审核',
}

const countBy = (items: EvalGroundTruthCase[], key: keyof EvalGroundTruthCase) =>
  items.reduce<Record<string, number>>((acc, item) => {
    const value = String(item[key] || 'unknown')
    acc[value] = (acc[value] ?? 0) + 1
    return acc
  }, {})

const formatStats = (stats: Record<string, number>, labels: Record<string, string>) =>
  Object.entries(stats)
    .sort((left, right) => right[1] - left[1])
    .map(([key, count]) => `${labels[key] ?? key} ${count}`)
    .join(' · ')

type LooseEvalGroundTruthCase = EvalGroundTruthCase & {
  Question?: string
  Answer?: string
  answerSnippets?: string[]
  AnswerSnippets?: string[]
}

const getEvalQuestion = (item: EvalGroundTruthCase) => {
  const looseItem = item as LooseEvalGroundTruthCase
  return looseItem.question || looseItem.Question || '（问题为空）'
}

const getEvalAnswer = (item: EvalGroundTruthCase) => {
  const looseItem = item as LooseEvalGroundTruthCase
  return looseItem.answer || looseItem.Answer || '（答案为空）'
}

const getEvalSnippets = (item: EvalGroundTruthCase) => {
  const looseItem = item as LooseEvalGroundTruthCase
  return looseItem.answer_snippets || looseItem.answerSnippets || looseItem.AnswerSnippets || []
}

const getSavedDatasetId = (dataset: EvalDatasetDialogDataset) => (
  ('datasetId' in dataset && dataset.datasetId) || ('id' in dataset ? dataset.id : '')
)

const lineList = (value: string) => (
  value
    .split('\n')
    .map((item) => item.trim())
    .filter(Boolean)
)

const draftFromItem = (item: EvalGroundTruthCase): EvalItemDraft => ({
  question: getEvalQuestion(item),
  answer: getEvalAnswer(item),
  snippets: getEvalSnippets(item).join('\n'),
  answerType: item.answer_type || 'extractive',
  difficulty: item.difficulty || 'medium',
  reviewStatus: item.review_status || 'approved',
  disabled: Boolean(item.disabled),
  notes: item.notes || '',
})

const itemFromDraft = (item: EvalGroundTruthCase, draft: EvalItemDraft): EvalGroundTruthCase => ({
  ...item,
  question: draft.question.trim(),
  answer: draft.answer.trim(),
  answer_snippets: lineList(draft.snippets),
  answer_type: draft.answerType,
  difficulty: draft.difficulty,
  review_status: draft.reviewStatus,
  disabled: draft.disabled,
  notes: draft.notes.trim() || undefined,
})

const downloadEvalDataset = (dataset: EvalDatasetDialogDataset, enabledOnly: boolean) => {
  const items = enabledOnly ? dataset.items.filter((item) => !item.disabled) : dataset.items
  const blob = new Blob([JSON.stringify(items, null, 2)], {
    type: 'application/json;charset=utf-8',
  })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  const timestamp = new Date().toISOString().slice(0, 19).replace(/[-:T]/g, '')
  const scope = dataset.documentId || dataset.knowledgeBaseId || 'all'
  const suffix = enabledOnly ? 'enabled' : 'all'
  link.href = url
  link.download = `ground_truth_${scope}_${suffix}_${timestamp}.json`
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

const EvalDatasetDialog: React.FC<EvalDatasetDialogProps> = ({
  dataset,
  scopeName,
  onClose,
  onUpdateItem,
  onDeleteItem,
}) => {
  const [editingItemId, setEditingItemId] = useState<string | null>(null)
  const [draft, setDraft] = useState<EvalItemDraft | null>(null)
  const [savingItemId, setSavingItemId] = useState<string | null>(null)
  const [deletingItemId, setDeletingItemId] = useState<string | null>(null)
  const [actionError, setActionError] = useState('')

  const datasetId = getSavedDatasetId(dataset)
  const editable = Boolean(datasetId && onUpdateItem && onDeleteItem)
  const previewItems = dataset.items
  const enabledCount = dataset.items.filter((item) => !item.disabled).length
  const answerTypeStats = useMemo(
    () => formatStats(countBy(dataset.items, 'answer_type'), answerTypeLabel),
    [dataset.items],
  )
  const difficultyStats = useMemo(
    () => formatStats(countBy(dataset.items, 'difficulty'), difficultyLabel),
    [dataset.items],
  )

  const startEditing = (item: EvalGroundTruthCase) => {
    setActionError('')
    setEditingItemId(item.id)
    setDraft(draftFromItem(item))
  }

  const updateDraft = <K extends keyof EvalItemDraft>(key: K, value: EvalItemDraft[K]) => {
    setDraft((prev) => prev ? { ...prev, [key]: value } : prev)
  }

  const saveItem = async (item: EvalGroundTruthCase, nextDraft: EvalItemDraft) => {
    if (!datasetId || !onUpdateItem) return
    const nextItem = itemFromDraft(item, nextDraft)
    setSavingItemId(item.id)
    setActionError('')
    try {
      await onUpdateItem(datasetId, item.id, nextItem)
      setEditingItemId(null)
      setDraft(null)
    } catch (error) {
      setActionError(error instanceof Error ? error.message : '更新评估样本失败')
    } finally {
      setSavingItemId(null)
    }
  }

  const quickUpdateItem = async (item: EvalGroundTruthCase, patch: Partial<EvalGroundTruthCase>) => {
    await saveItem(item, draftFromItem({ ...item, ...patch }))
  }

  const deleteItem = async (item: EvalGroundTruthCase) => {
    if (!datasetId || !onDeleteItem) return
    if (!window.confirm(`确认删除样本「${getEvalQuestion(item)}」？`)) return
    setDeletingItemId(item.id)
    setActionError('')
    try {
      await onDeleteItem(datasetId, item.id)
      if (editingItemId === item.id) {
        setEditingItemId(null)
        setDraft(null)
      }
    } catch (error) {
      setActionError(error instanceof Error ? error.message : '删除评估样本失败')
    } finally {
      setDeletingItemId(null)
    }
  }

  return (
    <div className="kb-dialog-backdrop" onClick={onClose}>
      <div className="kb-eval-dialog" onClick={(event) => event.stopPropagation()}>
        <header className="kb-eval-dialog-head">
          <div>
            <span>评估集预览</span>
            <h3>{scopeName || dataset.knowledgeBaseId || '当前知识库'}</h3>
          </div>
          <button className="kb-close-btn" onClick={onClose} title="关闭">x</button>
        </header>

        <section className="kb-eval-summary-grid">
          <div>
            <strong>{dataset.count}</strong>
            <span>评估用例</span>
          </div>
          <div>
            <strong>{enabledCount}</strong>
            <span>已启用</span>
          </div>
          <div>
            <strong>{answerTypeStats || '-'}</strong>
            <span>题型分布</span>
          </div>
          <div>
            <strong>{difficultyStats || '-'}</strong>
            <span>难度分布</span>
          </div>
        </section>

        {actionError && <div className="kb-eval-dialog-error">{actionError}</div>}

        <div className="kb-eval-preview-list">
          {previewItems.map((item, index) => {
            const isEditing = editingItemId === item.id
            const itemDraft = isEditing ? draft : null
            return (
              <article className="kb-eval-preview-item" key={item.id || index}>
                <div className="kb-eval-preview-head">
                  <div className="kb-eval-preview-tags">
                    <span>#{index + 1}</span>
                    <span>{answerTypeLabel[item.answer_type] ?? item.answer_type}</span>
                    <span>{difficultyLabel[item.difficulty] ?? item.difficulty}</span>
                    {item.review_status && <span>{reviewStatusLabel[item.review_status] ?? item.review_status}</span>}
                    {item.disabled && <span>未启用</span>}
                  </div>
                  {editable && !isEditing && (
                    <div className="kb-eval-item-actions kb-eval-item-actions--head">
                      {item.review_status !== 'approved' && (
                        <button
                          onClick={() => void quickUpdateItem(item, { review_status: 'approved', disabled: false })}
                          disabled={savingItemId === item.id}
                        >
                          审核通过
                        </button>
                      )}
                      <button
                        onClick={() => void quickUpdateItem(item, { disabled: !item.disabled })}
                        disabled={savingItemId === item.id}
                      >
                        {item.disabled ? '启用' : '禁用'}
                      </button>
                      <button onClick={() => startEditing(item)}>编辑</button>
                      <button
                        className="kb-eval-danger-btn"
                        onClick={() => void deleteItem(item)}
                        disabled={deletingItemId === item.id}
                      >
                        {deletingItemId === item.id ? '删除中' : '删除'}
                      </button>
                    </div>
                  )}
                </div>

                {isEditing && itemDraft ? (
                  <form
                    className="kb-eval-edit-form"
                    onSubmit={(event) => {
                      event.preventDefault()
                      void saveItem(item, itemDraft)
                    }}
                  >
                    <label>
                      <span>问题</span>
                      <input
                        value={itemDraft.question}
                        onChange={(event) => updateDraft('question', event.currentTarget.value)}
                      />
                    </label>
                    <label>
                      <span>答案</span>
                      <textarea
                        value={itemDraft.answer}
                        onChange={(event) => updateDraft('answer', event.currentTarget.value)}
                      />
                    </label>
                    <label>
                      <span>证据片段</span>
                      <textarea
                        value={itemDraft.snippets}
                        onChange={(event) => updateDraft('snippets', event.currentTarget.value)}
                      />
                    </label>
                    <div className="kb-eval-edit-row">
                      <label>
                        <span>题型</span>
                        <select
                          value={itemDraft.answerType}
                          onChange={(event) => updateDraft('answerType', event.currentTarget.value)}
                        >
                          <option value="extractive">摘录</option>
                          <option value="numeric">数值</option>
                          <option value="listing">列表</option>
                          <option value="process">流程</option>
                          <option value="retrieval-debug-candidate">调试候选</option>
                        </select>
                      </label>
                      <label>
                        <span>难度</span>
                        <select
                          value={itemDraft.difficulty}
                          onChange={(event) => updateDraft('difficulty', event.currentTarget.value)}
                        >
                          <option value="easy">简单</option>
                          <option value="medium">中等</option>
                          <option value="hard">困难</option>
                        </select>
                      </label>
                      <label>
                        <span>审核</span>
                        <select
                          value={itemDraft.reviewStatus}
                          onChange={(event) => updateDraft('reviewStatus', event.currentTarget.value)}
                        >
                          <option value="pending">待审核</option>
                          <option value="approved">已审核</option>
                        </select>
                      </label>
                    </div>
                    <label className="kb-eval-edit-check">
                      <input
                        type="checkbox"
                        checked={!itemDraft.disabled}
                        onChange={(event) => updateDraft('disabled', !event.currentTarget.checked)}
                      />
                      <span>参与评估</span>
                    </label>
                    <label>
                      <span>备注</span>
                      <textarea
                        value={itemDraft.notes}
                        onChange={(event) => updateDraft('notes', event.currentTarget.value)}
                      />
                    </label>
                    <div className="kb-eval-item-actions">
                      <button type="submit" disabled={savingItemId === item.id}>
                        {savingItemId === item.id ? '保存中' : '保存'}
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          setEditingItemId(null)
                          setDraft(null)
                        }}
                      >
                        取消
                      </button>
                    </div>
                  </form>
                ) : (
                  <>
                    <div className="kb-eval-preview-body">
                      <div className="kb-eval-preview-question">{getEvalQuestion(item)}</div>
                      <div className="kb-eval-preview-answer">{getEvalAnswer(item)}</div>
                    </div>
                    {getEvalSnippets(item).length > 0 && (
                      <div className="kb-eval-preview-evidence">
                        <span>证据片段</span>
                        <pre>{getEvalSnippets(item).join('\n\n')}</pre>
                      </div>
                    )}
                  </>
                )}
              </article>
            )
          })}
        </div>

        <footer className="kb-eval-dialog-actions">
          <span>
            共 {dataset.items.length} 条，已启用 {enabledCount} 条。
            {datasetId ? ` 已保存为 ${datasetId}。` : ''}
          </span>
          <div>
            <button onClick={() => downloadEvalDataset(dataset, true)}>下载启用 JSON</button>
            <button onClick={() => downloadEvalDataset(dataset, false)}>下载全部 JSON</button>
          </div>
        </footer>
      </div>
    </div>
  )
}

export default EvalDatasetDialog
