import React, { useState } from 'react'
import type { KnowledgeBase, DirectoryUploadTask } from '../../App'
import type {
  KnowledgeBaseHealthResponse,
  RetrievalDebugResponse,
  RetrievalSearchMode,
} from '../../services/api'
import DirectoryUploadTaskPanel from './DirectoryUploadTaskPanel'
import KnowledgeHealthPanel from './KnowledgeHealthPanel'
import RetrievalDebugPanel from './RetrievalDebugPanel'
import EvalDatasetHistoryPanel from './EvalDatasetHistoryPanel'
import EvalRunTrendPanel from './EvalRunTrendPanel'
import DocumentList from './DocumentList'
import { useDocument } from './contexts/DocumentContext'
import { useEvalDataset } from './contexts/EvalDatasetContext'

interface MainWorkspaceProps {
  knowledgeBase: KnowledgeBase
  knowledgeBaseId: string
  selectedDocumentId: string | null
  directoryUploadTask: DirectoryUploadTask
  uploadProgressPercent: number
  showUploadTaskDetails: boolean
  showFailedItems: boolean
  showSkippedItems: boolean
  canCancelUpload: boolean
  canContinueUpload: boolean
  isTaskVisible: boolean
  activeHealth: KnowledgeBaseHealthResponse | undefined
  healthLoadingId: string | null
  healthError: string
  selectedScopeLabel: string
  retrievalQuery: string
  retrievalSearchMode: RetrievalSearchMode
  retrievalDebugResult: RetrievalDebugResponse | null
  retrievalDebugError: string
  retrievalDebugKnowledgeBaseId: string | null
  onToggleUploadTaskDetails: () => void
  onToggleFailedItems: () => void
  onToggleSkippedItems: () => void
  onCancelDirectoryUpload: () => void
  onContinueDirectoryUpload: () => void
  onReindexDocument: (documentId: string) => void
  onSetRetrievalQuery: (query: string) => void
  onSetRetrievalSearchMode: (mode: RetrievalSearchMode) => void
  onRunRetrievalDebug: () => void
  onDownloadRetrievalEvalCandidate: () => void
  onAddRetrievalEvalCandidate: () => void
  onLoadEvalDatasets: () => void
  onOpenSavedEvalDataset: (datasetId: string) => void
  onDeleteSavedEvalDataset: (datasetId: string) => void
  onLoadEvalRuns: () => void
  onSelectDocument: (documentId: string | null) => void
  onOpenDocumentDetail: (documentId: string) => void
  onRemoveDocument: (documentId: string) => void
}

const INSPECTION_VIEW_OPTIONS = [
  { value: 'retrieval', label: '检索调试' },
  { value: 'health', label: '索引健康' },
] as const

const EVALUATION_VIEW_OPTIONS = [
  { value: 'datasets', label: '评估集' },
  { value: 'trend', label: '质量趋势' },
] as const

interface WorkspaceViewTabsProps<T extends string> {
  activeView: T
  ariaLabel: string
  idPrefix: string
  onChange: (view: T) => void
  options: ReadonlyArray<{ value: T; label: string }>
  panelId: string
}

function WorkspaceViewTabs<T extends string>({
  activeView,
  ariaLabel,
  idPrefix,
  onChange,
  options,
  panelId,
}: WorkspaceViewTabsProps<T>) {
  const handleKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return

    event.preventDefault()
    const currentIndex = options.findIndex((option) => option.value === activeView)
    const nextIndex = event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? options.length - 1
        : (currentIndex + (event.key === 'ArrowRight' ? 1 : -1) + options.length) % options.length
    const nextView = options[nextIndex]?.value
    if (!nextView) return

    onChange(nextView)
    window.requestAnimationFrame(() => {
      document.getElementById(`${idPrefix}-${nextView}-tab`)?.focus()
    })
  }

  return (
    <div
      aria-label={ariaLabel}
      className="kb-workspace-view-tabs"
      onKeyDown={handleKeyDown}
      role="tablist"
    >
      {options.map((option) => {
        const isActive = option.value === activeView
        return (
          <button
            aria-controls={panelId}
            aria-selected={isActive}
            className={isActive ? 'active' : ''}
            id={`${idPrefix}-${option.value}-tab`}
            key={option.value}
            onClick={() => onChange(option.value)}
            role="tab"
            tabIndex={isActive ? 0 : -1}
            type="button"
          >
            {option.label}
          </button>
        )
      })}
    </div>
  )
}

const MainWorkspace: React.FC<MainWorkspaceProps> = ({
  knowledgeBase,
  knowledgeBaseId,
  selectedDocumentId,
  directoryUploadTask,
  uploadProgressPercent,
  showUploadTaskDetails,
  showFailedItems,
  showSkippedItems,
  canCancelUpload,
  canContinueUpload,
  isTaskVisible,
  activeHealth,
  healthLoadingId,
  healthError,
  selectedScopeLabel,
  retrievalQuery,
  retrievalSearchMode,
  retrievalDebugResult,
  retrievalDebugError,
  retrievalDebugKnowledgeBaseId,
  onToggleUploadTaskDetails,
  onToggleFailedItems,
  onToggleSkippedItems,
  onCancelDirectoryUpload,
  onContinueDirectoryUpload,
  onReindexDocument,
  onSetRetrievalQuery,
  onSetRetrievalSearchMode,
  onRunRetrievalDebug,
  onDownloadRetrievalEvalCandidate,
  onAddRetrievalEvalCandidate,
  onLoadEvalDatasets,
  onOpenSavedEvalDataset,
  onDeleteSavedEvalDataset,
  onLoadEvalRuns,
  onSelectDocument,
  onOpenDocumentDetail,
  onRemoveDocument,
}) => {
  const docContext = useDocument()
  const evalContext = useEvalDataset()
  const [inspectionView, setInspectionView] = useState<(typeof INSPECTION_VIEW_OPTIONS)[number]['value']>('retrieval')
  const [evaluationView, setEvaluationView] = useState<(typeof EVALUATION_VIEW_OPTIONS)[number]['value']>('datasets')

  return (
    <>
      {isTaskVisible && directoryUploadTask.knowledgeBaseId === knowledgeBaseId && (
        <DirectoryUploadTaskPanel
          task={directoryUploadTask}
          progressPercent={uploadProgressPercent}
          showDetails={showUploadTaskDetails}
          showFailedItems={showFailedItems}
          showSkippedItems={showSkippedItems}
          canCancelUpload={canCancelUpload}
          canContinueUpload={canContinueUpload}
          onToggleDetails={onToggleUploadTaskDetails}
          onToggleFailedItems={onToggleFailedItems}
          onToggleSkippedItems={onToggleSkippedItems}
          onCancel={onCancelDirectoryUpload}
          onContinue={onContinueDirectoryUpload}
        />
      )}

      {docContext.reindexError && (
        <div className="kb-inline-error">
          重建索引失败：{docContext.reindexError}
        </div>
      )}

      <div className="kb-workspace-layout">
        <div className="kb-workspace-primary">
          <DocumentList
            documents={knowledgeBase.documents}
            healthDocuments={activeHealth?.documents}
            selectedDocumentId={selectedDocumentId}
            documentDetailLoadingId={docContext.documentDetailLoading ? selectedDocumentId : null}
            reindexingDocumentId={docContext.reindexingDocumentId}
            onSelectDocument={onSelectDocument}
            onOpenDocumentDetail={onOpenDocumentDetail}
            onReindexDocument={onReindexDocument}
            onRemoveDocument={onRemoveDocument}
          />
        </div>

        <aside className="kb-workspace-side" aria-label="知识库检查与调试">
          <WorkspaceViewTabs
            activeView={inspectionView}
            ariaLabel="知识库检查工具"
            idPrefix="kb-inspection"
            onChange={setInspectionView}
            options={INSPECTION_VIEW_OPTIONS}
            panelId="kb-inspection-panel"
          />

          <div
            className="kb-workspace-view-panel"
            id="kb-inspection-panel"
            aria-labelledby={`kb-inspection-${inspectionView}-tab`}
            role="tabpanel"
          >
            {inspectionView === 'retrieval' ? (
              <RetrievalDebugPanel
                scopeLabel={selectedScopeLabel}
                query={retrievalQuery}
                searchMode={retrievalSearchMode}
                result={retrievalDebugResult}
                error={retrievalDebugError}
                loading={retrievalDebugKnowledgeBaseId === knowledgeBaseId}
                savingEvalCandidate={evalContext.savingEvalCandidate}
                evalCandidateSaveMessage={evalContext.evalCandidateSaveMessage}
                onQueryChange={onSetRetrievalQuery}
                onSearchModeChange={onSetRetrievalSearchMode}
                onRun={onRunRetrievalDebug}
                onDownloadEvalCandidate={onDownloadRetrievalEvalCandidate}
                onAddEvalCandidate={onAddRetrievalEvalCandidate}
              />
            ) : (
              <KnowledgeHealthPanel
                health={activeHealth}
                loading={healthLoadingId === knowledgeBaseId}
                error={healthError}
                onReindexDocument={onReindexDocument}
                reindexingDocumentId={docContext.reindexingDocumentId}
              />
            )}
          </div>
        </aside>
      </div>

      <div className="kb-evaluation-shell">
        <WorkspaceViewTabs
          activeView={evaluationView}
          ariaLabel="质量评估视图"
          idPrefix="kb-evaluation"
          onChange={setEvaluationView}
          options={EVALUATION_VIEW_OPTIONS}
          panelId="kb-evaluation-panel"
        />
        <div
          className="kb-evaluation-panel"
          id="kb-evaluation-panel"
          aria-labelledby={`kb-evaluation-${evaluationView}-tab`}
          role="tabpanel"
        >
          {evaluationView === 'datasets' ? (
            <EvalDatasetHistoryPanel
              datasets={evalContext.evalDatasetSummaries}
              loading={evalContext.evalDatasetHistoryLoading}
              error={evalContext.evalDatasetHistoryError}
              openingDatasetId={evalContext.openingEvalDatasetId}
              deletingDatasetId={evalContext.deletingEvalDatasetId}
              onRefresh={onLoadEvalDatasets}
              onOpen={onOpenSavedEvalDataset}
              onDelete={onDeleteSavedEvalDataset}
            />
          ) : (
            <EvalRunTrendPanel
              runs={evalContext.evalRunSummaries}
              loading={evalContext.evalRunHistoryLoading}
              error={evalContext.evalRunHistoryError}
              onRefresh={onLoadEvalRuns}
            />
          )}
        </div>
      </div>
    </>
  )
}

export default MainWorkspace
