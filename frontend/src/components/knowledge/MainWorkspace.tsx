import React from 'react'
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

          <KnowledgeHealthPanel
            health={activeHealth}
            loading={healthLoadingId === knowledgeBaseId}
            error={healthError}
            onReindexDocument={onReindexDocument}
            reindexingDocumentId={docContext.reindexingDocumentId}
          />
        </aside>
      </div>

      <div className="kb-eval-grid">
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

        <EvalRunTrendPanel
          runs={evalContext.evalRunSummaries}
          loading={evalContext.evalRunHistoryLoading}
          error={evalContext.evalRunHistoryError}
          onRefresh={onLoadEvalRuns}
        />
      </div>
    </>
  )
}

export default MainWorkspace
