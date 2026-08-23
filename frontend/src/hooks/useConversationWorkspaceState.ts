import { useState } from 'react'
import type { CitationNavigationTarget, Conversation, WorkspaceView } from '../App'

export const useConversationWorkspaceState = (
  createInitialConversation: () => Conversation,
) => {
  const [conversations, setConversations] = useState<Conversation[]>(() => [
    createInitialConversation(),
  ])
  const [activeConversationId, setActiveConversationId] = useState<string | null>(null)
  const [activeWorkspace, setActiveWorkspace] = useState<WorkspaceView>('chat')
  const [citationNavigationTarget, setCitationNavigationTarget] =
    useState<CitationNavigationTarget | null>(null)
  const [streamingConversationId, setStreamingConversationId] = useState<string | null>(null)

  return {
    conversations,
    setConversations,
    activeConversationId,
    setActiveConversationId,
    activeWorkspace,
    setActiveWorkspace,
    citationNavigationTarget,
    setCitationNavigationTarget,
    streamingConversationId,
    setStreamingConversationId,
  }
}
