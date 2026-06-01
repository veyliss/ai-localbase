import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { DirectoryUploadTask, DocumentItem, KnowledgeBase } from '../../App'
import type {
  DocumentDetailResponse,
  EvalDatasetDetail,
  EvalGroundTruthCase,
  EvalDatasetSummary,
  GenerateEvalDatasetResponse,
  KnowledgeBaseHealthResponse,
  RetrievalDebugResponse,
  UpdateEvalDatasetItemResponse,
  DeleteEvalDatasetItemResponse,
} from '../../services/api'
import CreateKnowledgeBaseDialog from './CreateKnowledgeBaseDialog'
import DirectoryUploadTaskPanel from './DirectoryUploadTaskPanel'
import DocumentDetailDialog from './DocumentDetailDialog'
import EvalDatasetHistoryPanel from './EvalDatasetHistoryPanel'
import DocumentList from './DocumentList'
import EvalDatasetDialog from './EvalDatasetDialog'
import KnowledgeBaseRail from './KnowledgeBaseRail'
import KnowledgeHealthPanel from './KnowledgeHealthPanel'
import RetrievalDebugPanel from './RetrievalDebugPanel'

interface KnowledgePanelProps {
  open: boolean
  knowledgeBases: KnowledgeBase[]
  collapsedKnowledgeBases: Record<string, boolean>
  onToggleCollapse: (knowledgeBaseId: string) => void
  selectedKnowledgeBaseId: string | null
  selectedDocumentId: string | null
  onSelectKnowledgeBase: (knowledgeBaseId: string) => void
  onSelectDocument: (knowledgeBaseId: string, documentId: string | null) => void
  onCreateKnowledgeBase: (name: string, description: string) => void
  onDeleteKnowledgeBase: (knowledgeBaseId: string) => void
  onUploadFiles: (knowledgeBaseId: string, files: FileList | null) => void
  onUploadDirectory: (knowledgeBaseId: string, files: FileList | null) => void
  onGenerateEvalDataset: (knowledgeBaseId: string) => Promise<GenerateEvalDatasetResponse>
  onListEvalDatasets: (knowledgeBaseId: string) => Promise<EvalDatasetSummary[]>
  onFetchEvalDataset: (datasetId: string) => Promise<EvalDatasetDetail>
  onDeleteEvalDataset: (datasetId: string) => Promise<void>
  onAddEvalDatasetCandidate: (
    knowledgeBaseId: string,
    documentId: string | null,
    item: EvalGroundTruthCase,
  ) => Promise<EvalDatasetSummary>
  onUpdateEvalDatasetItem: (
    datasetId: string,
    itemId: string,
    item: EvalGroundTruthCase,
  ) => Promise<UpdateEvalDatasetItemResponse>
  onDeleteEvalDatasetItem: (
    datasetId: string,
    itemId: string,
  ) => Promise<DeleteEvalDatasetItemResponse>
  directoryUploadTask: DirectoryUploadTask
  onCancelDirectoryUpload: () => void
  onContinueDirectoryUpload: () => void
  onRemoveDocument: (knowledgeBaseId: string, documentId: string) => void
  onFetchKnowledgeBaseHealth: (knowledgeBaseId: string) => Promise<KnowledgeBaseHealthResponse>
  onFetchDocumentDetail: (
    knowledgeBaseId: string,
    documentId: string,
  ) => Promise<DocumentDetailResponse>
  onReindexDocument: (knowledgeBaseId: string, documentId: string) => Promise<DocumentItem>
  onDebugRetrieval: (
    knowledgeBaseId: string,
    query: string,
    documentId: string | null,
  ) => Promise<RetrievalDebugResponse>
  onClose: () => void
}

const KnowledgePanel: React.FC<KnowledgePanelProps> = ({
  open,
  knowledgeBases,
  selectedKnowledgeBaseId,
  selectedDocumentId,
  onSelectKnowledgeBase,
  onSelectDocument,
  onCreateKnowledgeBase,
  onDeleteKnowledgeBase,
  onUploadFiles,
  onUploadDirectory,
  onGenerateEvalDataset,
  onListEvalDatasets,
  onFetchEvalDataset,
  onDeleteEvalDataset,
  onAddEvalDatasetCandidate,
  onUpdateEvalDatasetItem,
  onDeleteEvalDatasetItem,
  directoryUploadTask,
  onCancelDirectoryUpload,
  onContinueDirectoryUpload,
  onRemoveDocument,
  onFetchKnowledgeBaseHealth,
  onFetchDocumentDetail,
  onReindexDocument,
  onDebugRetrieval,
  onClose,
}) => {
  const [showCreateModal, setShowCreateModal] = useState(false)
  const [newName, setNewName] = useState('')
  const [newDescription, setNewDescription] = useState('')
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
  const [showUploadTaskDetails, setShowUploadTaskDetails] = useState(false)
  const [showFailedItems, setShowFailedItems] = useState(false)
  const [showSkippedItems, setShowSkippedItems] = useState(false)
  const [generatingEvalKnowledgeBaseId, setGeneratingEvalKnowledgeBaseId] = useState<string | null>(null)
  const [documentDetail, setDocumentDetail] = useState<DocumentDetailResponse | null>(null)
  const [evalDataset, setEvalDataset] = useState<GenerateEvalDatasetResponse | null>(null)
  const [evalDatasetScopeName, setEvalDatasetScopeName] = useState('')
  const [evalDatasetSummaries, setEvalDatasetSummaries] = useState<EvalDatasetSummary[]>([])
  const [evalDatasetHistoryLoading, setEvalDatasetHistoryLoading] = useState(false)
  const [evalDatasetHistoryError, setEvalDatasetHistoryError] = useState('')
  const [openingEvalDatasetId, setOpeningEvalDatasetId] = useState<string | null>(null)
  const [deletingEvalDatasetId, setDeletingEvalDatasetId] = useState<string | null>(null)
  const [documentDetailLoadingId, setDocumentDetailLoadingId] = useState<string | null>(null)
  const [documentDetailError, setDocumentDetailError] = useState('')
  const [healthByKnowledgeBase, setHealthByKnowledgeBase] = useState<Record<string, KnowledgeBaseHealthResponse>>({})
  const [healthLoadingId, setHealthLoadingId] = useState<string | null>(null)
  const [healthError, setHealthError] = useState('')
  const [reindexingDocumentId, setReindexingDocumentId] = useState<string | null>(null)
  const [retrievalQuery, setRetrievalQuery] = useState('')
  const [retrievalDebugKnowledgeBaseId, setRetrievalDebugKnowledgeBaseId] = useState<string | null>(null)
  const [retrievalDebugResult, setRetrievalDebugResult] = useState<RetrievalDebugResponse | null>(null)
  const [retrievalDebugError, setRetrievalDebugError] = useState('')
  const [savingEvalCandidate, setSavingEvalCandidate] = useState(false)
  const [evalCandidateSaveMessage, setEvalCandidateSaveMessage] = useState('')
  const directoryInputRefs = useRef<Record<string, HTMLInputElement | null>>({})
  const evalDatasetLoadSeq = useRef(0)

  const selectedKnowledgeBase = useMemo(
    () => knowledgeBases.find((item) => item.id === selectedKnowledgeBaseId) ?? knowledgeBases[0] ?? null,
    [knowledgeBases, selectedKnowledgeBaseId],
  )
  const activeKnowledgeBaseId = selectedKnowledgeBase?.id ?? null

  useEffect(() => {
    if (open && !selectedKnowledgeBaseId && selectedKnowledgeBase) {
      onSelectKnowledgeBase(selectedKnowledgeBase.id)
    }
  }, [open, selectedKnowledgeBaseId, selectedKnowledgeBase, onSelectKnowledgeBase])

  useEffect(() => {
    setRetrievalDebugResult(null)
    setRetrievalDebugError('')
    setEvalCandidateSaveMessage('')
  }, [selectedKnowledgeBaseId, selectedDocumentId])

  const loadEvalDatasets = useCallback(async (knowledgeBaseId: string) => {
    const requestSeq = evalDatasetLoadSeq.current + 1
    evalDatasetLoadSeq.current = requestSeq
    setEvalDatasetHistoryLoading(true)
    setEvalDatasetHistoryError('')
    try {
      const items = await onListEvalDatasets(knowledgeBaseId)
      if (evalDatasetLoadSeq.current !== requestSeq) return
      setEvalDatasetSummaries(items)
    } catch (error) {
      if (evalDatasetLoadSeq.current !== requestSeq) return
      setEvalDatasetHistoryError(error instanceof Error ? error.message : '加载评估集历史失败')
    } finally {
      if (evalDatasetLoadSeq.current === requestSeq) {
        setEvalDatasetHistoryLoading(false)
      }
    }
  }, [onListEvalDatasets])

  useEffect(() => {
    if (!open || !activeKnowledgeBaseId) {
      evalDatasetLoadSeq.current += 1
      setEvalDatasetSummaries([])
      setEvalDatasetHistoryError('')
      setEvalDatasetHistoryLoading(false)
      return
    }

    void loadEvalDatasets(activeKnowledgeBaseId)
  }, [open, activeKnowledgeBaseId, loadEvalDatasets])

  const selectedKnowledgeBaseHealthKey = useMemo(() => {
    if (!activeKnowledgeBaseId || !selectedKnowledgeBase) return ''
    return selectedKnowledgeBase.documents
      .map((document) => [
        document.id,
        document.status,
        document.chunkCount ?? 0,
        document.indexedAt ?? '',
        document.indexError ?? '',
      ].join(':'))
      .join('|')
  }, [activeKnowledgeBaseId, selectedKnowledgeBase])

  useEffect(() => {
    if (!open || !activeKnowledgeBaseId) {
      return
    }

    let canceled = false
    setHealthLoadingId(activeKnowledgeBaseId)
    setHealthError('')
    onFetchKnowledgeBaseHealth(activeKnowledgeBaseId)
      .then((health) => {
        if (canceled) return
        setHealthByKnowledgeBase((prev) => ({
          ...prev,
          [activeKnowledgeBaseId]: health,
        }))
      })
      .catch((error) => {
        if (canceled) return
        setHealthError(error instanceof Error ? error.message : '加载知识库健康度失败')
      })
      .finally(() => {
        if (!canceled) {
          setHealthLoadingId(null)
        }
      })

    return () => {
      canceled = true
    }
  }, [open, activeKnowledgeBaseId, selectedKnowledgeBaseHealthKey, onFetchKnowledgeBaseHealth])

  const handleFileChange = (knowledgeBaseId: string, event: React.ChangeEvent<HTMLInputElement>) => {
    onUploadFiles(knowledgeBaseId, event.target.files)
    event.target.value = ''
  }

  const handleDirectoryChange = (knowledgeBaseId: string, event: React.ChangeEvent<HTMLInputElement>) => {
    onUploadDirectory(knowledgeBaseId, event.target.files)
    event.target.value = ''
  }

  const handleGenerateEvalDataset = async (knowledgeBaseId: string) => {
    setGeneratingEvalKnowledgeBaseId(knowledgeBaseId)
    try {
      const dataset = await onGenerateEvalDataset(knowledgeBaseId)
      setEvalDataset(dataset)
      setEvalDatasetScopeName(
        knowledgeBases.find((knowledgeBase) => knowledgeBase.id === knowledgeBaseId)?.name ?? knowledgeBaseId,
      )
      void loadEvalDatasets(knowledgeBaseId)
    } catch (error) {
      const message = error instanceof Error ? error.message : '生成评估集失败，请稍后重试。'
      window.alert(`生成评估集失败：${message}`)
    } finally {
      setGeneratingEvalKnowledgeBaseId(null)
    }
  }

  const handleOpenSavedEvalDataset = async (datasetId: string) => {
    setOpeningEvalDatasetId(datasetId)
    try {
      const detail = await onFetchEvalDataset(datasetId)
      setEvalDataset({
        datasetId: detail.id,
        knowledgeBaseId: detail.knowledgeBaseId,
        documentId: detail.documentId,
        count: detail.count,
        documentCount: detail.documentCount,
        createdAt: detail.createdAt,
        items: detail.items,
      })
      setEvalDatasetScopeName(detail.name || selectedKnowledgeBase?.name || detail.knowledgeBaseId || '评估集')
    } catch (error) {
      const message = error instanceof Error ? error.message : '加载评估集失败'
      window.alert(`加载评估集失败：${message}`)
    } finally {
      setOpeningEvalDatasetId(null)
    }
  }

  const handleDeleteSavedEvalDataset = async (datasetId: string) => {
    const target = evalDatasetSummaries.find((dataset) => dataset.id === datasetId)
    if (!window.confirm(`确认删除「${target?.name || datasetId}」？`)) {
      return
    }

    setDeletingEvalDatasetId(datasetId)
    try {
      await onDeleteEvalDataset(datasetId)
      setEvalDatasetSummaries((prev) => prev.filter((dataset) => dataset.id !== datasetId))
    } catch (error) {
      const message = error instanceof Error ? error.message : '删除评估集失败'
      window.alert(`删除评估集失败：${message}`)
    } finally {
      setDeletingEvalDatasetId(null)
    }
  }

  const handleUpdateEvalDatasetItem = async (
    datasetId: string,
    itemId: string,
    item: EvalGroundTruthCase,
  ) => {
    const response = await onUpdateEvalDatasetItem(datasetId, itemId, item)
    setEvalDataset((prev) => {
      if (!prev) return prev
      return {
        ...prev,
        count: response.dataset.count,
        items: prev.items.map((existing) => existing.id === itemId ? response.item : existing),
      }
    })
    if (activeKnowledgeBaseId) {
      void loadEvalDatasets(activeKnowledgeBaseId)
    }
    return response.item
  }

  const handleDeleteEvalDatasetItem = async (datasetId: string, itemId: string) => {
    const response = await onDeleteEvalDatasetItem(datasetId, itemId)
    setEvalDataset((prev) => {
      if (!prev) return prev
      return {
        ...prev,
        count: response.dataset.count,
        items: prev.items.filter((item) => item.id !== itemId),
      }
    })
    if (activeKnowledgeBaseId) {
      void loadEvalDatasets(activeKnowledgeBaseId)
    }
  }

  const handleOpenDocumentDetail = async (knowledgeBaseId: string, documentId: string) => {
    setDocumentDetail(null)
    setDocumentDetailError('')
    setDocumentDetailLoadingId(documentId)
    try {
      const detail = await onFetchDocumentDetail(knowledgeBaseId, documentId)
      setDocumentDetail(detail)
    } catch (error) {
      setDocumentDetailError(error instanceof Error ? error.message : '加载文档详情失败')
    } finally {
      setDocumentDetailLoadingId(null)
    }
  }

  const handleReindexDocument = async (knowledgeBaseId: string, documentId: string) => {
    setReindexingDocumentId(documentId)
    try {
      const updatedDocument = await onReindexDocument(knowledgeBaseId, documentId)
      if (documentDetail?.document.id === documentId) {
        const detail = await onFetchDocumentDetail(knowledgeBaseId, documentId)
        setDocumentDetail({
          ...detail,
          document: {
            ...detail.document,
            ...updatedDocument,
          },
        })
      }
      const health = await onFetchKnowledgeBaseHealth(knowledgeBaseId)
      setHealthByKnowledgeBase((prev) => ({
        ...prev,
        [knowledgeBaseId]: health,
      }))
    } finally {
      setReindexingDocumentId(null)
    }
  }

  const handleRunRetrievalDebug = async (knowledgeBaseId: string) => {
    const query = retrievalQuery.trim()
    if (!query) {
      setRetrievalDebugError('请输入要调试的问题')
      return
    }

    setRetrievalDebugKnowledgeBaseId(knowledgeBaseId)
    setRetrievalDebugError('')
    setEvalCandidateSaveMessage('')
    try {
      const result = await onDebugRetrieval(knowledgeBaseId, query, selectedDocumentId)
      setRetrievalDebugResult(result)
    } catch (error) {
      setRetrievalDebugResult(null)
      setRetrievalDebugError(error instanceof Error ? error.message : '检索调试失败')
    } finally {
      setRetrievalDebugKnowledgeBaseId(null)
    }
  }

  const handleDownloadRetrievalEvalCandidate = () => {
    if (!retrievalDebugResult?.evalCandidate) {
      return
    }

    const blob = new Blob([JSON.stringify([retrievalDebugResult.evalCandidate], null, 2)], {
      type: 'application/json;charset=utf-8',
    })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    const timestamp = new Date().toISOString().slice(0, 19).replace(/[-:T]/g, '')
    const scope = retrievalDebugResult.documentId || retrievalDebugResult.knowledgeBaseId || 'all'
    link.href = url
    link.download = `retrieval_debug_eval_${scope}_${timestamp}.json`
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
  }

  const handleAddRetrievalEvalCandidate = async (knowledgeBaseId: string) => {
    if (!retrievalDebugResult?.evalCandidate) {
      return
    }

    setSavingEvalCandidate(true)
    setEvalCandidateSaveMessage('')
    try {
      const dataset = await onAddEvalDatasetCandidate(
        knowledgeBaseId,
        selectedDocumentId,
        retrievalDebugResult.evalCandidate,
      )
      setEvalCandidateSaveMessage(`已加入「${dataset.name}」`)
      await loadEvalDatasets(knowledgeBaseId)
    } catch (error) {
      setEvalCandidateSaveMessage(error instanceof Error ? error.message : '加入待审核评估集失败')
    } finally {
      setSavingEvalCandidate(false)
    }
  }

  const registerDirectoryInput = (knowledgeBaseId: string, element: HTMLInputElement | null) => {
    directoryInputRefs.current[knowledgeBaseId] = element
    if (element) {
      element.setAttribute('webkitdirectory', '')
      element.setAttribute('directory', '')
    }
  }

  const handleOpenCreate = () => {
    setNewName('')
    setNewDescription('')
    setShowCreateModal(true)
  }

  const handleConfirmCreate = () => {
    const trimmedName = newName.trim()
    if (!trimmedName) return
    onCreateKnowledgeBase(trimmedName, newDescription.trim())
    setShowCreateModal(false)
    setNewName('')
    setNewDescription('')
  }

  const closeDocumentDetail = () => {
    setDocumentDetail(null)
    setDocumentDetailError('')
  }

  const selectedScopeLabel =
    selectedDocumentId
      ? selectedKnowledgeBase?.documents.find((document) => document.id === selectedDocumentId)?.name ?? '当前文档'
      : '全部文档'

  const uploadProgressPercent =
    directoryUploadTask.eligibleFiles > 0
      ? Math.round((directoryUploadTask.processedFiles / directoryUploadTask.eligibleFiles) * 100)
      : 0

  const isTaskVisible = directoryUploadTask.status !== 'idle'
  const canCancelUpload =
    directoryUploadTask.status === 'uploading' || directoryUploadTask.status === 'canceling'
  const canContinueUpload =
    (directoryUploadTask.status === 'canceled' || directoryUploadTask.status === 'partial-failed') &&
    directoryUploadTask.pendingFiles > 0
  const isTaskActive =
    directoryUploadTask.status === 'scanning' ||
    directoryUploadTask.status === 'uploading' ||
    directoryUploadTask.status === 'canceling'

  useEffect(() => {
    if (isTaskActive) {
      setShowUploadTaskDetails(true)
    }
  }, [isTaskActive])

  useEffect(() => {
    setShowFailedItems(false)
    setShowSkippedItems(false)
  }, [directoryUploadTask.knowledgeBaseId, directoryUploadTask.status])

  if (!open) return null

  const totalDocuments = knowledgeBases.reduce((sum, knowledgeBase) => sum + knowledgeBase.documents.length, 0)
  const activeHealth = activeKnowledgeBaseId ? healthByKnowledgeBase[activeKnowledgeBaseId] : undefined

  return (
    <>
      <div className="kb-backdrop" onClick={onClose}>
        <div className="kb-modal kb-modal--workspace" onClick={(event) => event.stopPropagation()}>
          <header className="kb-header">
            <div className="kb-header-left">
              <div>
                <h2 className="kb-header-title">知识库管理</h2>
                <p className="kb-header-sub">
                  共 {knowledgeBases.length} 个知识库 · {totalDocuments} 份文档
                </p>
              </div>
            </div>
            <div className="kb-header-actions">
              <button className="kb-create-btn" onClick={handleOpenCreate}>
                <span>+</span> 新建知识库
              </button>
              <button className="kb-close-btn" onClick={onClose} title="关闭">x</button>
            </div>
          </header>

          {knowledgeBases.length === 0 ? (
            <div className="kb-empty">
              <p className="kb-empty-title">暂无知识库</p>
              <p className="kb-empty-sub">创建第一个知识库，开始管理本地文档</p>
              <button className="kb-create-btn" onClick={handleOpenCreate}>
                <span>+</span> 新建知识库
              </button>
            </div>
          ) : (
            <div className="kb-workbench">
              <KnowledgeBaseRail
                knowledgeBases={knowledgeBases}
                selectedKnowledgeBaseId={activeKnowledgeBaseId}
                healthByKnowledgeBase={healthByKnowledgeBase}
                deleteConfirmId={deleteConfirmId}
                onSelectKnowledgeBase={onSelectKnowledgeBase}
                onDeleteKnowledgeBase={onDeleteKnowledgeBase}
                onSetDeleteConfirmId={setDeleteConfirmId}
                onCreate={handleOpenCreate}
              />

              <main className="kb-workspace">
                {selectedKnowledgeBase && activeKnowledgeBaseId ? (
                  <>
                    <section className="kb-workspace-hero">
                      <div className="kb-workspace-title">
                        <span className="kb-workspace-kicker">当前知识库</span>
                        <h3>{selectedKnowledgeBase.name}</h3>
                        <p>{selectedKnowledgeBase.description || '未填写描述'}</p>
                      </div>
                      <div className="kb-workspace-actions">
                        <label className="kb-upload-btn" title="上传文档">
                          <span>+</span> 上传文件
                          <input
                            type="file"
                            multiple
                            accept=".txt,.md,.pdf,.csv,.xlsx"
                            className="hidden-input"
                            onChange={(event) => handleFileChange(activeKnowledgeBaseId, event)}
                          />
                        </label>
                        <label className="kb-upload-btn kb-upload-btn--secondary" title="上传目录">
                          <span>+</span> 上传目录
                          <input
                            ref={(element) => registerDirectoryInput(activeKnowledgeBaseId, element)}
                            type="file"
                            multiple
                            className="hidden-input"
                            onChange={(event) => handleDirectoryChange(activeKnowledgeBaseId, event)}
                          />
                        </label>
                        <button
                          className="kb-eval-btn"
                          onClick={() => void handleGenerateEvalDataset(activeKnowledgeBaseId)}
                          disabled={selectedKnowledgeBase.documents.length === 0 || generatingEvalKnowledgeBaseId === activeKnowledgeBaseId}
                        >
                          {generatingEvalKnowledgeBaseId === activeKnowledgeBaseId ? '生成中' : '评估集'}
                        </button>
                      </div>
                    </section>

                    {isTaskVisible && directoryUploadTask.knowledgeBaseId === activeKnowledgeBaseId && (
                      <DirectoryUploadTaskPanel
                        task={directoryUploadTask}
                        progressPercent={uploadProgressPercent}
                        showDetails={showUploadTaskDetails}
                        showFailedItems={showFailedItems}
                        showSkippedItems={showSkippedItems}
                        canCancelUpload={canCancelUpload}
                        canContinueUpload={canContinueUpload}
                        onToggleDetails={() => setShowUploadTaskDetails((prev) => !prev)}
                        onToggleFailedItems={() => setShowFailedItems((prev) => !prev)}
                        onToggleSkippedItems={() => setShowSkippedItems((prev) => !prev)}
                        onCancel={onCancelDirectoryUpload}
                        onContinue={onContinueDirectoryUpload}
                      />
                    )}

                    <div className="kb-workspace-grid">
                      <KnowledgeHealthPanel
                        health={activeHealth}
                        loading={healthLoadingId === activeKnowledgeBaseId}
                        error={healthError}
                        onReindexDocument={(documentId) => void handleReindexDocument(activeKnowledgeBaseId, documentId)}
                        reindexingDocumentId={reindexingDocumentId}
                      />

                      <RetrievalDebugPanel
                        scopeLabel={selectedScopeLabel}
                        query={retrievalQuery}
                        result={retrievalDebugResult}
                        error={retrievalDebugError}
                        loading={retrievalDebugKnowledgeBaseId === activeKnowledgeBaseId}
                        savingEvalCandidate={savingEvalCandidate}
                        evalCandidateSaveMessage={evalCandidateSaveMessage}
                        onQueryChange={setRetrievalQuery}
                        onRun={() => void handleRunRetrievalDebug(activeKnowledgeBaseId)}
                        onDownloadEvalCandidate={handleDownloadRetrievalEvalCandidate}
                        onAddEvalCandidate={() => void handleAddRetrievalEvalCandidate(activeKnowledgeBaseId)}
                      />
                    </div>

                    <EvalDatasetHistoryPanel
                      datasets={evalDatasetSummaries}
                      loading={evalDatasetHistoryLoading}
                      error={evalDatasetHistoryError}
                      openingDatasetId={openingEvalDatasetId}
                      deletingDatasetId={deletingEvalDatasetId}
                      onRefresh={() => void loadEvalDatasets(activeKnowledgeBaseId)}
                      onOpen={(datasetId) => void handleOpenSavedEvalDataset(datasetId)}
                      onDelete={(datasetId) => void handleDeleteSavedEvalDataset(datasetId)}
                    />

                    <DocumentList
                      documents={selectedKnowledgeBase.documents}
                      selectedDocumentId={selectedDocumentId}
                      documentDetailLoadingId={documentDetailLoadingId}
                      reindexingDocumentId={reindexingDocumentId}
                      onSelectDocument={(documentId) => onSelectDocument(activeKnowledgeBaseId, documentId)}
                      onOpenDocumentDetail={(documentId) => void handleOpenDocumentDetail(activeKnowledgeBaseId, documentId)}
                      onReindexDocument={(documentId) => void handleReindexDocument(activeKnowledgeBaseId, documentId)}
                      onRemoveDocument={(documentId) => onRemoveDocument(activeKnowledgeBaseId, documentId)}
                    />
                  </>
                ) : (
                  <div className="kb-empty kb-empty--workspace">
                    <p className="kb-empty-title">选择一个知识库</p>
                    <p className="kb-empty-sub">查看索引健康度、文档列表和检索调试结果</p>
                  </div>
                )}
              </main>
            </div>
          )}
        </div>
      </div>

      {showCreateModal && (
        <CreateKnowledgeBaseDialog
          name={newName}
          description={newDescription}
          onNameChange={setNewName}
          onDescriptionChange={setNewDescription}
          onCancel={() => {
            setShowCreateModal(false)
            setNewName('')
            setNewDescription('')
          }}
          onConfirm={handleConfirmCreate}
        />
      )}

      {(documentDetail || documentDetailError) && (
        <DocumentDetailDialog
          detail={documentDetail}
          error={documentDetailError}
          onClose={closeDocumentDetail}
        />
      )}

      {evalDataset && (
        <EvalDatasetDialog
          dataset={evalDataset}
          scopeName={evalDatasetScopeName}
          onClose={() => setEvalDataset(null)}
          onUpdateItem={handleUpdateEvalDatasetItem}
          onDeleteItem={handleDeleteEvalDatasetItem}
        />
      )}
    </>
  )
}

export default KnowledgePanel
