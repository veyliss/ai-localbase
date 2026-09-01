import { useCallback, useEffect, useState, type Dispatch, type SetStateAction } from 'react'
import {
  API_BASE_PATH,
  fetchBackendHealth,
  fetchConversationDetail,
  fetchInitialAppData,
} from '../services/api'
import { createEmptyConversation } from './appHelpers'
import { normalizeAppConfig } from './appConfig'
import type { AppConfig, Conversation, KnowledgeBase } from '../App'

interface UseAppBootstrapOptions {
  isAuthenticated: boolean
  logout: () => Promise<void>
  setKnowledgeBases: Dispatch<SetStateAction<KnowledgeBase[]>>
  setConfig: Dispatch<SetStateAction<AppConfig>>
  setConversations: Dispatch<SetStateAction<Conversation[]>>
  setActiveConversationId: Dispatch<SetStateAction<string | null>>
  setSelectedKnowledgeBaseId: Dispatch<SetStateAction<string | null>>
  setSelectedDocumentId: Dispatch<SetStateAction<string | null>>
}

export interface AppBootstrapState {
  authCheckDone: boolean
  authRequired: boolean
  backendReady: boolean
  backendWarmupRequired: boolean
  setBackendReady: Dispatch<SetStateAction<boolean>>
  setBackendWarmupRequired: Dispatch<SetStateAction<boolean>>
  waitForBackendReady: (attempts?: number, delayMs?: number) => Promise<boolean>
}

const sleep = (delayMs: number) => new Promise((resolve) => {
  window.setTimeout(resolve, delayMs)
})

export const useAppBootstrap = ({
  isAuthenticated,
  logout,
  setKnowledgeBases,
  setConfig,
  setConversations,
  setActiveConversationId,
  setSelectedKnowledgeBaseId,
  setSelectedDocumentId,
}: UseAppBootstrapOptions): AppBootstrapState => {
  const [authCheckDone, setAuthCheckDone] = useState(false)
  const [authRequired, setAuthRequired] = useState(false)
  const [backendReady, setBackendReady] = useState(false)
  const [backendWarmupRequired, setBackendWarmupRequired] = useState(true)

  const waitForBackendReady = useCallback(async (attempts = 12, delayMs = 1500) => {
    for (let index = 0; index < attempts; index += 1) {
      const health = await fetchBackendHealth()
      if ((health?.status ?? '').toLowerCase() === 'ok') {
        setBackendReady(true)
        setBackendWarmupRequired(true)
        return true
      }

      if (index < attempts - 1) {
        await sleep(delayMs)
      }
    }

    setBackendReady(false)
    return false
  }, [])

  useEffect(() => {
    let canceled = false

    const checkAuth = async () => {
      try {
        const health = await fetchBackendHealth()
        if (!health) {
          throw new Error('health check unavailable')
        }
        const authEnabled = health.config?.auth_enabled === 'true'
        if (!authEnabled) {
          setAuthRequired(false)
          return
        }

        setAuthRequired(true)
        if (!isAuthenticated) {
          return
        }

        const response = await fetch(`${API_BASE_PATH}/api/auth/status`, {
          credentials: 'same-origin',
        })
        if (response.status === 401) {
          setAuthRequired(true)
          void logout()
          return
        }
        if (!response.ok) {
          throw new Error('auth status check failed')
        }
      } catch {
        try {
          const response = await fetch(`${API_BASE_PATH}/api/knowledge-bases`, {
            credentials: 'same-origin',
          })
          if (response.status === 401) {
            setAuthRequired(true)
            if (isAuthenticated) {
              void logout()
            }
            return
          }
          setAuthRequired(!response.ok)
        } catch {
          setAuthRequired(true)
        }
      } finally {
        if (!canceled) {
          setAuthCheckDone(true)
        }
      }
    }

    void checkAuth()

    return () => {
      canceled = true
    }
  }, [isAuthenticated, logout])

  useEffect(() => {
    if (!authCheckDone || (authRequired && !isAuthenticated)) {
      return
    }

    let canceled = false

    const bootstrapApp = async () => {
      while (!canceled) {
        try {
          const isReady = await waitForBackendReady()
          if (!isReady) {
            throw new Error('backend is not ready')
          }

          const initialData = await fetchInitialAppData()

          if (canceled) {
            return
          }

          setKnowledgeBases(initialData.knowledgeBases)
          setConfig((prev) => normalizeAppConfig(initialData.config, prev))
          const conversationItems = initialData.conversations
          if (conversationItems.length > 0) {
            const firstConversation = await fetchConversationDetail(conversationItems[0].id)
            const restConversations = conversationItems.slice(1).map((conversation) => ({
              id: conversation.id,
              title: conversation.title,
              knowledgeBaseId: conversation.knowledgeBaseId,
              documentId: conversation.documentId,
              scopeVersion: conversation.scopeVersion ?? 0,
              createdAt: conversation.createdAt,
              updatedAt: conversation.updatedAt,
              messages: [],
            }))

            if (canceled) {
              return
            }

            if (firstConversation.scopeVersion < 1) {
              const safeConversation = createEmptyConversation(firstConversation.knowledgeBaseId)
              setConversations([safeConversation, firstConversation, ...restConversations])
              setActiveConversationId(safeConversation.id)
              setSelectedKnowledgeBaseId(safeConversation.knowledgeBaseId || null)
              setSelectedDocumentId(null)
            } else {
              setConversations([firstConversation, ...restConversations])
              setActiveConversationId(firstConversation.id)
              setSelectedKnowledgeBaseId(firstConversation.knowledgeBaseId || null)
              setSelectedDocumentId(firstConversation.documentId || null)
            }
          } else {
            const initialKnowledgeBaseId = initialData.knowledgeBases[0]?.id ?? ''
            const initialConversation = createEmptyConversation(initialKnowledgeBaseId)
            setConversations([initialConversation])
            setActiveConversationId(initialConversation.id)
            setSelectedKnowledgeBaseId(initialKnowledgeBaseId || null)
            setSelectedDocumentId(null)
          }

          setBackendReady(true)
          return
        } catch (error) {
          if (canceled) {
            return
          }

          setBackendReady(false)
          console.warn('bootstrap app failed, retrying after backend warmup', error)
          await sleep(2000)
        }
      }
    }

    void bootstrapApp()

    return () => {
      canceled = true
    }
  }, [
    authCheckDone,
    authRequired,
    isAuthenticated,
    setActiveConversationId,
    setConfig,
    setConversations,
    setKnowledgeBases,
    setSelectedDocumentId,
    setSelectedKnowledgeBaseId,
    waitForBackendReady,
  ])

  return {
    authCheckDone,
    authRequired,
    backendReady,
    backendWarmupRequired,
    setBackendReady,
    setBackendWarmupRequired,
    waitForBackendReady,
  }
}
