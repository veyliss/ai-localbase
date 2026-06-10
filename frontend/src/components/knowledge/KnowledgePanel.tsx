import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { CitationNavigationTarget, DirectoryUploadTask, DocumentItem, KnowledgeBase } from '../../App'
import type {
  DocumentDetailResponse,
  EvalDatasetDetail,
  EvalGroundTruthCase,
  EvalDatasetSummary,
  EvalRunOptions,
  EvalRunSummary,
  GenerateEvalDatasetResponse,
  KnowledgeBaseHealthResponse,
  RetrievalDebugResponse,
  RetrievalSearchMode,
  RunEvalDatasetResponse,
  UpdateEvalDatasetItemResponse,
  DeleteEvalDatasetItemResponse,
} from '../../services/api'
import CreateKnowledgeBaseDialog from './CreateKnowledgeBaseDialog'
import DocumentDetailDialog from './DocumentDetailDialog'
import EvalDatasetDialog from './EvalDatasetDialog'
import KnowledgeBaseRail from './KnowledgeBaseRail'
import WorkspaceHero from './WorkspaceHero'
import MainWorkspace from './MainWorkspace'
import ConfirmDialog from '../common/ConfirmDialog'
import UploadDropZone from '../common/UploadDropZone'
import { useKnowledgeBase } from './contexts/KnowledgeBaseContext'
import { useDocument } from './contexts/DocumentContext'
import { useEvalDataset } from './contexts/EvalDatasetContext'

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
  onListEvalRuns: (knowledgeBaseId: string) => Promise<EvalRunSummary[]>
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
  onRunEvalDataset: (
    datasetId: string,
    options?: RetrievalSearchMode | EvalRunOptions,
  ) => Promise<RunEvalDatasetResponse>
  directoryUploadTask: DirectoryUploadTask
  onCancelDirectoryUpload: () => void
  onContinueDirectoryUpload: () => void
  onRemoveDocument: (knowledgeBaseId: string, documentId: string) => void
  onFetchKnowledgeBaseHealth: (knowledgeBaseId: string) => Promise<KnowledgeBaseHealthResponse>
  onFetchDocumentDetail: (
    knowledgeBaseId: string,
    documentId: string,
    focusChunkId?: string,
  ) => Promise<DocumentDetailResponse>
  onReindexDocument: (knowledgeBaseId: string, documentId: string) => Promise<DocumentItem>
  onDebugRetrieval: (
    knowledgeBaseId: string,
    query: string,
    documentId: string | null,
    searchMode?: RetrievalSearchMode,
  ) => Promise<RetrievalDebugResponse>
  citationNavigationTarget: CitationNavigationTarget | null
  onCitationNavigationHandled: () => void
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
  onListEvalRuns,
  onFetchEvalDataset,
  onDeleteEvalDataset,
  onAddEvalDatasetCandidate,
  onUpdateEvalDatasetItem,
  onDeleteEvalDatasetItem,
  onRunEvalDataset,
  directoryUploadTask,
  onCancelDirectoryUpload,
  onContinueDirectoryUpload,
  onRemoveDocument,
  onFetchKnowledgeBaseHealth,
  onFetchDocumentDetail,
  onReindexDocument,
  onDebugRetrieval,
  citationNavigationTarget,
  onCitationNavigationHandled,
  onClose,
}) => {
  // Use Context
  const kbContext = useKnowledgeBase()
  const docContext = useDocument()
  const evalContext = useEvalDataset()

  // UI state (non-business logic)
  const [showCreateModal, setShowCreateModal] = useState(false)
  const [newName, setNewName] = useState('')
  const [newDescription, setNewDescription] = useState('')
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
  const [showUploadTaskDetails, setShowUploadTaskDetails] = useState(false)
  const [showFailedItems, setShowFailedItems] = useState(false)
  const [showSkippedItems, setShowSkippedItems] = useState(false)
  const [healthByKnowledgeBase, setHealthByKnowledgeBase] = useState<Record<string, KnowledgeBaseHealthResponse>>({})
  const [healthLoadingId, setHealthLoadingId] = useState<string | null>(null)
  const [healthError, setHealthError] = useState('')
  const [retrievalQuery, setRetrievalQuery] = useState('')
  const [retrievalSearchMode, setRetrievalSearchMode] = useState<RetrievalSearchMode>('auto')
  const [retrievalDebugKnowledgeBaseId, setRetrievalDebugKnowledgeBaseId] = useState<string | null>(null)
  const [retrievalDebugResult, setRetrievalDebugResult] = useState<RetrievalDebugResponse | null>(null)
  const [retrievalDebugError, setRetrievalDebugError] = useState('')

  // 确认对话框状态
  const [confirmDialog, setConfirmDialog] = useState<{
    open: boolean
    title: string
    message: string
    onConfirm: () => void
  }>({
    open: false,
    title: '',
    message: '',
    onConfirm: () => {},
  })
  const directoryInputRefs = useRef<Record<string, HTMLInputElement | null>>({})

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
  }, [selectedKnowledgeBaseId, selectedDocumentId])

  useEffect(() => {
    if (!open || !activeKnowledgeBaseId) return
    void evalContext.loadEvalDatasets(activeKnowledgeBaseId)
    void evalContext.loadEvalRuns(activeKnowledgeBaseId)
  }, [open, activeKnowledgeBaseId, evalContext])

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
    await evalContext.generateEvalDataset(knowledgeBaseId)
  }

  const handleOpenSavedEvalDataset = async (datasetId: string) => {
    await evalContext.openEvalDataset(datasetId)
  }

  const handleDeleteSavedEvalDataset = async (datasetId: string) => {
    const target = evalContext.evalDatasetSummaries.find((dataset) => dataset.id === datasetId)

    setConfirmDialog({
      open: true,
      title: '删除评估集',
      message: `确认删除「${target?.name || datasetId}」？`,
      onConfirm: async () => {
        setConfirmDialog({ ...confirmDialog, open: false })
        await evalContext.deleteEvalDataset(datasetId)
      }
    })
  }

  const handleUpdateEvalDatasetItem = async (
    datasetId: string,
    itemId: string,
    item: EvalGroundTruthCase,
  ) => {
    const response = await evalContext.updateDatasetItem(datasetId, itemId, item)
    if (activeKnowledgeBaseId) {
      void evalContext.loadEvalDatasets(activeKnowledgeBaseId)
    }
    return response.item
  }

  const handleDeleteEvalDatasetItem = async (datasetId: string, itemId: string) => {
    await evalContext.deleteDatasetItem(datasetId, itemId)
    if (activeKnowledgeBaseId) {
      void evalContext.loadEvalDatasets(activeKnowledgeBaseId)
    }
  }

  const handleRunEvalDataset = async (
    datasetId: string,
    options?: RetrievalSearchMode | EvalRunOptions,
  ) => {
    const report = await evalContext.runEvalDataset(datasetId, options)
    if (activeKnowledgeBaseId) {
      void evalContext.loadEvalRuns(activeKnowledgeBaseId)
    }
    return report
  }

  const handleOpenDocumentDetail = useCallback(async (knowledgeBaseId: string, documentId: string, chunkId?: string) => {
    await docContext.openDocumentDetail(knowledgeBaseId, documentId, chunkId)
  }, [docContext])

  useEffect(() => {
    if (!open || !citationNavigationTarget) {
      return
    }
    const { knowledgeBaseId, documentId, chunkId } = citationNavigationTarget
    onSelectKnowledgeBase(knowledgeBaseId)
    onSelectDocument(knowledgeBaseId, documentId)
    void handleOpenDocumentDetail(knowledgeBaseId, documentId, chunkId)
    onCitationNavigationHandled()
  }, [
    open,
    citationNavigationTarget,
    handleOpenDocumentDetail,
    onCitationNavigationHandled,
    onSelectDocument,
    onSelectKnowledgeBase,
  ])

  const handleReindexDocument = async (knowledgeBaseId: string, documentId: string) => {
    await docContext.reindexDocument(knowledgeBaseId, documentId)
    // Refresh health after reindex
    const health = await onFetchKnowledgeBaseHealth(knowledgeBaseId)
    setHealthByKnowledgeBase((prev) => ({
      ...prev,
      [knowledgeBaseId]: health,
    }))
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
      const result = await onDebugRetrieval(knowledgeBaseId, query, selectedDocumentId, retrievalSearchMode)
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
    if (!retrievalDebugResult?.evalCandidate) return
    const { query, groundTruth } = retrievalDebugResult.evalCandidate
    await evalContext.saveEvalCandidate(knowledgeBaseId, selectedDocumentId, query, groundTruth)
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
                <UploadDropZone onFilesSelected={(files) => onUploadDirectory(activeKnowledgeBaseId!, files)}>
                {selectedKnowledgeBase && activeKnowledgeBaseId ? (
                  <>
                    <WorkspaceHero
                      knowledgeBase={selectedKnowledgeBase}
                      health={activeHealth}
                      selectedScopeLabel={selectedScopeLabel}
                      generatingEvalDataset={evalContext.generatingKnowledgeBaseId === activeKnowledgeBaseId}
                      onUploadFiles={(e) => handleFileChange(activeKnowledgeBaseId, e)}
                      onUploadDirectory={(e) => handleDirectoryChange(activeKnowledgeBaseId, e)}
                      onGenerateEvalDataset={() => void handleGenerateEvalDataset(activeKnowledgeBaseId)}
                      registerDirectoryInput={(el) => registerDirectoryInput(activeKnowledgeBaseId, el)}
                    />

                    <MainWorkspace
                      knowledgeBase={selectedKnowledgeBase}
                      knowledgeBaseId={activeKnowledgeBaseId}
                      selectedDocumentId={selectedDocumentId}
                      directoryUploadTask={directoryUploadTask}
                      uploadProgressPercent={uploadProgressPercent}
                      showUploadTaskDetails={showUploadTaskDetails}
                      showFailedItems={showFailedItems}
                      showSkippedItems={showSkippedItems}
                      canCancelUpload={canCancelUpload}
                      canContinueUpload={canContinueUpload}
                      isTaskVisible={isTaskVisible}
                      activeHealth={activeHealth}
                      healthLoadingId={healthLoadingId}
                      healthError={healthError}
                      selectedScopeLabel={selectedScopeLabel}
                      retrievalQuery={retrievalQuery}
                      retrievalSearchMode={retrievalSearchMode}
                      retrievalDebugResult={retrievalDebugResult}
                      retrievalDebugError={retrievalDebugError}
                      retrievalDebugKnowledgeBaseId={retrievalDebugKnowledgeBaseId}
                      onToggleUploadTaskDetails={() => setShowUploadTaskDetails(prev => !prev)}
                      onToggleFailedItems={() => setShowFailedItems(prev => !prev)}
                      onToggleSkippedItems={() => setShowSkippedItems(prev => !prev)}
                      onCancelDirectoryUpload={onCancelDirectoryUpload}
                      onContinueDirectoryUpload={onContinueDirectoryUpload}
                      onReindexDocument={(documentId) => void handleReindexDocument(activeKnowledgeBaseId, documentId)}
                      onSetRetrievalQuery={setRetrievalQuery}
                      onSetRetrievalSearchMode={setRetrievalSearchMode}
                      onRunRetrievalDebug={() => void handleRunRetrievalDebug(activeKnowledgeBaseId)}
                      onDownloadRetrievalEvalCandidate={handleDownloadRetrievalEvalCandidate}
                      onAddRetrievalEvalCandidate={() => void handleAddRetrievalEvalCandidate(activeKnowledgeBaseId)}
                      onLoadEvalDatasets={() => void loadEvalDatasets(activeKnowledgeBaseId)}
                      onOpenSavedEvalDataset={(datasetId) => void handleOpenSavedEvalDataset(datasetId)}
                      onDeleteSavedEvalDataset={(datasetId) => void handleDeleteSavedEvalDataset(datasetId)}
                      onLoadEvalRuns={() => void loadEvalRuns(activeKnowledgeBaseId)}
                      onSelectDocument={(documentId) => onSelectDocument(activeKnowledgeBaseId, documentId)}
                      onOpenDocumentDetail={(documentId) => void handleOpenDocumentDetail(activeKnowledgeBaseId, documentId)}
                      onRemoveDocument={(documentId) => onRemoveDocument(activeKnowledgeBaseId, documentId)}
                    />
                  </>
                ) : (
                  <div className="kb-empty kb-empty--workspace">
                    <p className="kb-empty-title">选择一个知识库</p>
                    <p className="kb-empty-sub">查看索引健康度、文档列表和检索调试结果</p>
                  </div>
                )}
                </UploadDropZone>
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

      {(docContext.documentDetail || docContext.documentDetailError) && (
        <DocumentDetailDialog
          detail={docContext.documentDetail}
          error={docContext.documentDetailError}
          focusChunkId={docContext.documentDetailFocusChunkId}
          onClose={docContext.closeDocumentDetail}
        />
      )}

      {evalContext.evalDataset && (
        <EvalDatasetDialog
          dataset={evalContext.evalDataset}
          scopeName={evalContext.evalDatasetScopeName}
          onClose={() => evalContext.closeEvalDataset()}
          onUpdateItem={handleUpdateEvalDatasetItem}
          onDeleteItem={handleDeleteEvalDatasetItem}
          onRun={handleRunEvalDataset}
        />
      )}

      <ConfirmDialog
        open={confirmDialog.open}
        title={confirmDialog.title}
        message={confirmDialog.message}
        onConfirm={confirmDialog.onConfirm}
        onCancel={() => setConfirmDialog({ ...confirmDialog, open: false })}
      />
    </>
  )
}

export default KnowledgePanel
