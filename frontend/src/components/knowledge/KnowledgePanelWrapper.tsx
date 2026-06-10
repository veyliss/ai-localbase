import React from 'react'
import { DocumentProvider } from './contexts/DocumentContext'
import { KnowledgeBaseProvider } from './contexts/KnowledgeBaseContext'
import { EvalDatasetProvider } from './contexts/EvalDatasetContext'
import KnowledgePanel from './KnowledgePanel'
import type { CitationNavigationTarget, DirectoryUploadTask, KnowledgeBase } from '../../App'
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

interface KnowledgePanelWrapperProps {
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
  onReindexDocument: (knowledgeBaseId: string, documentId: string) => Promise<any>
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

const KnowledgePanelWrapper: React.FC<KnowledgePanelWrapperProps> = (props) => {
  return (
    <KnowledgeBaseProvider
      initialKnowledgeBases={props.knowledgeBases}
      initialSelectedId={props.selectedKnowledgeBaseId}
      initialCollapsed={props.collapsedKnowledgeBases}
    >
      <DocumentProvider>
        <EvalDatasetProvider>
          <KnowledgePanel {...props} />
        </EvalDatasetProvider>
      </DocumentProvider>
    </KnowledgeBaseProvider>
  )
}

export default KnowledgePanelWrapper
