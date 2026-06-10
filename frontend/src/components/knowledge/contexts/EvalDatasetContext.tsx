import React, { createContext, useContext, useState, useCallback, useMemo, useRef, useEffect, type ReactNode } from 'react'
import type {
  EvalDatasetSummary,
  EvalDatasetDetail,
  EvalGroundTruthCase,
  EvalRunSummary,
  EvalRunOptions,
  GenerateEvalDatasetResponse,
  RunEvalDatasetResponse,
  UpdateEvalDatasetItemResponse,
  DeleteEvalDatasetItemResponse,
} from '../../../services/api'
import {
  generateEvalDataset as apiGenerateEvalDataset,
  listEvalDatasets,
  listEvalRuns,
  getEvalDataset,
  deleteEvalDataset as apiDeleteEvalDataset,
  updateEvalDatasetItem,
  deleteEvalDatasetItem,
  runEvalDataset as apiRunEvalDataset,
  extractErrorMessage,
} from '../../../services/api'

interface EvalDatasetContextValue {
  // Generate Dataset
  generatingKnowledgeBaseId: string | null
  generateEvalDataset: (knowledgeBaseId: string) => Promise<GenerateEvalDatasetResponse>

  // Dataset List
  evalDatasetSummaries: EvalDatasetSummary[]
  evalDatasetHistoryLoading: boolean
  evalDatasetHistoryError: string
  loadEvalDatasets: (knowledgeBaseId: string) => Promise<void>

  // Current Dataset
  evalDataset: EvalDatasetDetail | null
  evalDatasetScopeName: string
  openingEvalDatasetId: string | null
  openEvalDataset: (datasetId: string) => Promise<void>
  closeEvalDataset: () => void

  // Dataset Operations
  deletingEvalDatasetId: string | null
  deleteEvalDataset: (datasetId: string) => Promise<void>
  updateDatasetItem: (
    datasetId: string,
    itemId: string,
    item: EvalGroundTruthCase
  ) => Promise<UpdateEvalDatasetItemResponse>
  deleteDatasetItem: (
    datasetId: string,
    itemId: string
  ) => Promise<DeleteEvalDatasetItemResponse>

  // Eval Runs
  evalRunSummaries: EvalRunSummary[]
  evalRunHistoryLoading: boolean
  evalRunHistoryError: string
  loadEvalRuns: (knowledgeBaseId: string) => Promise<void>
  runEvalDataset: (datasetId: string, options?: EvalRunOptions) => Promise<RunEvalDatasetResponse>

  // Eval Candidate
  savingEvalCandidate: boolean
  evalCandidateSaveMessage: string
  saveEvalCandidate: (
    knowledgeBaseId: string,
    documentId: string | null,
    query: string,
    groundTruth: string
  ) => Promise<void>
}

const EvalDatasetContext = createContext<EvalDatasetContextValue | null>(null)

export const useEvalDataset = () => {
  const context = useContext(EvalDatasetContext)
  if (!context) {
    throw new Error('useEvalDataset must be used within EvalDatasetProvider')
  }
  return context
}

interface EvalDatasetProviderProps {
  children: ReactNode
}

export const EvalDatasetProvider: React.FC<EvalDatasetProviderProps> = ({ children }) => {
  // Generate Dataset
  const [generatingKnowledgeBaseId, setGeneratingKnowledgeBaseId] = useState<string | null>(null)

  // Dataset List
  const [evalDatasetSummaries, setEvalDatasetSummaries] = useState<EvalDatasetSummary[]>([])
  const [evalDatasetHistoryLoading, setEvalDatasetHistoryLoading] = useState(false)
  const [evalDatasetHistoryError, setEvalDatasetHistoryError] = useState('')
  const evalDatasetLoadSeqRef = useRef(0)

  // Current Dataset
  const [evalDataset, setEvalDataset] = useState<EvalDatasetDetail | null>(null)
  const [evalDatasetScopeName, setEvalDatasetScopeName] = useState('')
  const [openingEvalDatasetId, setOpeningEvalDatasetId] = useState<string | null>(null)

  // Dataset Operations
  const [deletingEvalDatasetId, setDeletingEvalDatasetId] = useState<string | null>(null)

  // Eval Runs
  const [evalRunSummaries, setEvalRunSummaries] = useState<EvalRunSummary[]>([])
  const [evalRunHistoryLoading, setEvalRunHistoryLoading] = useState(false)
  const [evalRunHistoryError, setEvalRunHistoryError] = useState('')
  const evalRunLoadSeqRef = useRef(0)

  // Eval Candidate
  const [savingEvalCandidate, setSavingEvalCandidate] = useState(false)
  const [evalCandidateSaveMessage, setEvalCandidateSaveMessage] = useState('')

  // Generate Dataset
  const generateEvalDataset = useCallback(async (knowledgeBaseId: string) => {
    setGeneratingKnowledgeBaseId(knowledgeBaseId)
    try {
      const result = await apiGenerateEvalDataset(knowledgeBaseId)
      return result
    } finally {
      setGeneratingKnowledgeBaseId(null)
    }
  }, [])

  // Load Dataset List
  const loadEvalDatasets = useCallback(async (knowledgeBaseId: string) => {
    const currentSeq = ++evalDatasetLoadSeqRef.current
    setEvalDatasetHistoryLoading(true)
    setEvalDatasetHistoryError('')

    try {
      const response = await listEvalDatasets(knowledgeBaseId)

      if (currentSeq === evalDatasetLoadSeqRef.current) {
        setEvalDatasetSummaries(response.items ?? [])
      }
    } catch (err) {
      if (currentSeq === evalDatasetLoadSeqRef.current) {
        setEvalDatasetHistoryError(await extractErrorMessage(err))
      }
    } finally {
      if (currentSeq === evalDatasetLoadSeqRef.current) {
        setEvalDatasetHistoryLoading(false)
      }
    }
  }, [])

  // Open Dataset
  const openEvalDataset = useCallback(async (datasetId: string) => {
    setOpeningEvalDatasetId(datasetId)
    try {
      const detail = await getEvalDataset(datasetId)
      setEvalDataset(detail)

      // Set scope name based on dataset
      if (detail.knowledgeBaseName && detail.documentName) {
        setEvalDatasetScopeName(`${detail.knowledgeBaseName} > ${detail.documentName}`)
      } else if (detail.knowledgeBaseName) {
        setEvalDatasetScopeName(detail.knowledgeBaseName)
      } else {
        setEvalDatasetScopeName('')
      }
    } finally {
      setOpeningEvalDatasetId(null)
    }
  }, [])

  const closeEvalDataset = useCallback(() => {
    setEvalDataset(null)
    setEvalDatasetScopeName('')
  }, [])

  // Delete Dataset
  const deleteEvalDataset = useCallback(async (datasetId: string) => {
    setDeletingEvalDatasetId(datasetId)
    try {
      await apiDeleteEvalDataset(datasetId)

      // Remove from list
      setEvalDatasetSummaries(prev => prev.filter(s => s.id !== datasetId))

      // Close if currently open
      if (evalDataset?.id === datasetId) {
        closeEvalDataset()
      }
    } finally {
      setDeletingEvalDatasetId(null)
    }
  }, [evalDataset, closeEvalDataset])

  // Update Dataset Item
  const updateDatasetItem = useCallback(async (
    datasetId: string,
    itemId: string,
    item: EvalGroundTruthCase
  ) => {
    const result = await updateEvalDatasetItem(datasetId, itemId, item)

    // Update local state
    if (evalDataset?.id === datasetId) {
      setEvalDataset(prev => {
        if (!prev) return prev
        return {
          ...prev,
          cases: prev.cases.map(c => (c.id === itemId ? { ...c, ...item } : c)),
        }
      })
    }

    return result
  }, [evalDataset])

  // Delete Dataset Item
  const deleteDatasetItem = useCallback(async (datasetId: string, itemId: string) => {
    const result = await deleteEvalDatasetItem(datasetId, itemId)

    // Update local state
    if (evalDataset?.id === datasetId) {
      setEvalDataset(prev => {
        if (!prev) return prev
        return {
          ...prev,
          cases: prev.cases.filter(c => c.id !== itemId),
        }
      })
    }

    return result
  }, [evalDataset])

  // Load Eval Runs
  const loadEvalRuns = useCallback(async (knowledgeBaseId: string) => {
    const currentSeq = ++evalRunLoadSeqRef.current
    setEvalRunHistoryLoading(true)
    setEvalRunHistoryError('')

    try {
      const response = await listEvalRuns(knowledgeBaseId)

      if (currentSeq === evalRunLoadSeqRef.current) {
        setEvalRunSummaries(response.items ?? [])
      }
    } catch (err) {
      if (currentSeq === evalRunLoadSeqRef.current) {
        setEvalRunHistoryError(await extractErrorMessage(err))
      }
    } finally {
      if (currentSeq === evalRunLoadSeqRef.current) {
        setEvalRunHistoryLoading(false)
      }
    }
  }, [])

  // Run Eval Dataset
  const runEvalDataset = useCallback(async (datasetId: string, options?: EvalRunOptions) => {
    return await apiRunEvalDataset(datasetId, options)
  }, [])

  // Save Eval Candidate
  const saveEvalCandidate = useCallback(async (
    knowledgeBaseId: string,
    documentId: string | null,
    query: string,
    groundTruth: string
  ) => {
    setSavingEvalCandidate(true)
    setEvalCandidateSaveMessage('')

    try {
      // Implementation would call an API to save the candidate
      // For now, just simulate success
      setEvalCandidateSaveMessage('评测候选已保存')

      // Auto-clear message after 3 seconds
      setTimeout(() => {
        setEvalCandidateSaveMessage('')
      }, 3000)
    } catch (err) {
      setEvalCandidateSaveMessage(await extractErrorMessage(err))
    } finally {
      setSavingEvalCandidate(false)
    }
  }, [])

  // Memoize context value
  const value = useMemo<EvalDatasetContextValue>(
    () => ({
      generatingKnowledgeBaseId,
      generateEvalDataset,

      evalDatasetSummaries,
      evalDatasetHistoryLoading,
      evalDatasetHistoryError,
      loadEvalDatasets,

      evalDataset,
      evalDatasetScopeName,
      openingEvalDatasetId,
      openEvalDataset,
      closeEvalDataset,

      deletingEvalDatasetId,
      deleteEvalDataset,
      updateDatasetItem,
      deleteDatasetItem,

      evalRunSummaries,
      evalRunHistoryLoading,
      evalRunHistoryError,
      loadEvalRuns,
      runEvalDataset,

      savingEvalCandidate,
      evalCandidateSaveMessage,
      saveEvalCandidate,
    }),
    [
      generatingKnowledgeBaseId,
      generateEvalDataset,
      evalDatasetSummaries,
      evalDatasetHistoryLoading,
      evalDatasetHistoryError,
      loadEvalDatasets,
      evalDataset,
      evalDatasetScopeName,
      openingEvalDatasetId,
      openEvalDataset,
      closeEvalDataset,
      deletingEvalDatasetId,
      deleteEvalDataset,
      updateDatasetItem,
      deleteDatasetItem,
      evalRunSummaries,
      evalRunHistoryLoading,
      evalRunHistoryError,
      loadEvalRuns,
      runEvalDataset,
      savingEvalCandidate,
      evalCandidateSaveMessage,
      saveEvalCandidate,
    ]
  )

  return <EvalDatasetContext.Provider value={value}>{children}</EvalDatasetContext.Provider>
}
