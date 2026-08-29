import React, { createContext, useContext, useState, useCallback, useMemo, useRef, type ReactNode } from 'react'
import type {
  IndexedDocumentVerification,
  KnowledgeBaseHealthResponse,
  RetrievalDebugResponse,
  RetrievalSearchMode,
} from '../../../services/api'
import {
  fetchKnowledgeBaseHealth,
  debugKnowledgeBaseRetrieval,
  extractErrorMessage,
  verifyKnowledgeBaseDocumentIndex,
} from '../../../services/api'

interface HealthContextValue {
  // Health Monitoring
  healthByKnowledgeBase: Record<string, KnowledgeBaseHealthResponse>
  healthLoadingId: string | null
  healthError: string
  fetchHealth: (knowledgeBaseId: string) => Promise<void>
  clearHealthError: () => void

  // Index verification
  indexVerificationByDocument: Record<string, IndexedDocumentVerification>
  indexVerificationLoadingKey: string | null
  indexVerificationError: string
  verifyDocumentIndex: (knowledgeBaseId: string, documentId: string) => Promise<void>

  // Retrieval Debug
  retrievalQuery: string
  retrievalSearchMode: RetrievalSearchMode
  retrievalDebugKnowledgeBaseId: string | null
  retrievalDebugResult: RetrievalDebugResponse | null
  retrievalDebugError: string
  retrievalDebugLoading: boolean

  setRetrievalQuery: (query: string) => void
  setRetrievalSearchMode: (mode: RetrievalSearchMode) => void
  runRetrievalDebug: (
    knowledgeBaseId: string,
    documentId?: string | null
  ) => Promise<void>
  clearRetrievalDebug: () => void
}

const HealthContext = createContext<HealthContextValue | null>(null)

export const useHealth = () => {
  const context = useContext(HealthContext)
  if (!context) {
    throw new Error('useHealth must be used within HealthProvider')
  }
  return context
}

interface HealthProviderProps {
  children: ReactNode
}

const getErrorMessage = async (err: unknown, fallback: string) => {
  if (err instanceof Error) {
    return err.message
  }
  if (err instanceof Response) {
    return extractErrorMessage(err)
  }
  return fallback
}

export const HealthProvider: React.FC<HealthProviderProps> = ({ children }) => {
  // Health Monitoring
  const [healthByKnowledgeBase, setHealthByKnowledgeBase] = useState<
    Record<string, KnowledgeBaseHealthResponse>
  >({})
  const [healthLoadingId, setHealthLoadingId] = useState<string | null>(null)
  const [healthError, setHealthError] = useState('')
  const healthRequestIdRef = useRef(0)

  // Verification requests are keyed by knowledge base and document so a
  // response from an older selection cannot overwrite the current result.
  const [indexVerificationByDocument, setIndexVerificationByDocument] = useState<
    Record<string, IndexedDocumentVerification>
  >({})
  const [indexVerificationLoadingKey, setIndexVerificationLoadingKey] = useState<string | null>(null)
  const [indexVerificationError, setIndexVerificationError] = useState('')
  const indexVerificationRequestIdRef = useRef(0)

  // Retrieval Debug
  const [retrievalQuery, setRetrievalQuery] = useState('')
  const [retrievalSearchMode, setRetrievalSearchMode] = useState<RetrievalSearchMode>('auto')
  const [retrievalDebugKnowledgeBaseId, setRetrievalDebugKnowledgeBaseId] = useState<string | null>(null)
  const [retrievalDebugResult, setRetrievalDebugResult] = useState<RetrievalDebugResponse | null>(null)
  const [retrievalDebugError, setRetrievalDebugError] = useState('')
  const [retrievalDebugLoading, setRetrievalDebugLoading] = useState(false)

  // Fetch Health
  const fetchHealth = useCallback(async (knowledgeBaseId: string) => {
    const requestId = healthRequestIdRef.current + 1
    healthRequestIdRef.current = requestId
    setHealthLoadingId(knowledgeBaseId)
    setHealthError('')

    try {
      const health = await fetchKnowledgeBaseHealth(knowledgeBaseId)

      if (requestId !== healthRequestIdRef.current) {
        return
      }

      setHealthByKnowledgeBase(prev => ({
        ...prev,
        [knowledgeBaseId]: health,
      }))
    } catch (err) {
      if (requestId !== healthRequestIdRef.current) {
        return
      }
      setHealthError(await getErrorMessage(err, '加载知识库健康度失败'))
    } finally {
      if (requestId === healthRequestIdRef.current) {
        setHealthLoadingId(null)
      }
    }
  }, [])

  const clearHealthError = useCallback(() => {
    setHealthError('')
  }, [])

  const verifyDocumentIndex = useCallback(async (knowledgeBaseId: string, documentId: string) => {
    const key = `${knowledgeBaseId}:${documentId}`
    const requestId = indexVerificationRequestIdRef.current + 1
    indexVerificationRequestIdRef.current = requestId
    setIndexVerificationLoadingKey(key)
    setIndexVerificationError('')

    try {
      const verification = await verifyKnowledgeBaseDocumentIndex(knowledgeBaseId, documentId)
      if (requestId !== indexVerificationRequestIdRef.current) {
        return
      }
      setIndexVerificationByDocument((previous) => ({
        ...previous,
        [key]: verification,
      }))
    } catch (err) {
      if (requestId !== indexVerificationRequestIdRef.current) {
        return
      }
      setIndexVerificationError(await getErrorMessage(err, '索引校验失败'))
    } finally {
      if (requestId === indexVerificationRequestIdRef.current) {
        setIndexVerificationLoadingKey(null)
      }
    }
  }, [])

  // Run Retrieval Debug
  const runRetrievalDebug = useCallback(async (
    knowledgeBaseId: string,
    documentId: string | null = null
  ) => {
    if (!retrievalQuery.trim()) {
      setRetrievalDebugError('请输入查询内容')
      return
    }

    setRetrievalDebugLoading(true)
    setRetrievalDebugError('')
    setRetrievalDebugKnowledgeBaseId(knowledgeBaseId)

    try {
      const result = await debugKnowledgeBaseRetrieval(
        knowledgeBaseId,
        retrievalQuery,
        documentId,
        retrievalSearchMode
      )

      setRetrievalDebugResult(result)
    } catch (err) {
      setRetrievalDebugError(await getErrorMessage(err, '检索调试失败'))
      setRetrievalDebugResult(null)
    } finally {
      setRetrievalDebugLoading(false)
      setRetrievalDebugKnowledgeBaseId(null)
    }
  }, [retrievalQuery, retrievalSearchMode])

  const clearRetrievalDebug = useCallback(() => {
    setRetrievalDebugResult(null)
    setRetrievalDebugError('')
    setRetrievalDebugKnowledgeBaseId(null)
  }, [])

  // Memoize context value
  const value = useMemo<HealthContextValue>(
    () => ({
      healthByKnowledgeBase,
      healthLoadingId,
      healthError,
      fetchHealth,
      clearHealthError,

      indexVerificationByDocument,
      indexVerificationLoadingKey,
      indexVerificationError,
      verifyDocumentIndex,

      retrievalQuery,
      retrievalSearchMode,
      retrievalDebugKnowledgeBaseId,
      retrievalDebugResult,
      retrievalDebugError,
      retrievalDebugLoading,

      setRetrievalQuery,
      setRetrievalSearchMode,
      runRetrievalDebug,
      clearRetrievalDebug,
    }),
    [
      healthByKnowledgeBase,
      healthLoadingId,
      healthError,
      fetchHealth,
      clearHealthError,
      indexVerificationByDocument,
      indexVerificationLoadingKey,
      indexVerificationError,
      verifyDocumentIndex,
      retrievalQuery,
      retrievalSearchMode,
      retrievalDebugKnowledgeBaseId,
      retrievalDebugResult,
      retrievalDebugError,
      retrievalDebugLoading,
      runRetrievalDebug,
      clearRetrievalDebug,
    ]
  )

  return <HealthContext.Provider value={value}>{children}</HealthContext.Provider>
}
