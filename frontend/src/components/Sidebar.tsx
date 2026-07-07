import React, { useMemo, useState } from 'react'
import type {
  AppConfig,
  ChatConfig,
  ChatModeSettings,
  CitationNavigationTarget,
  Conversation,
  DocumentItem,
  DirectoryUploadTask,
  EmbeddingConfig,
  KnowledgeBase,
  RetrievalConfig,
} from '../App'
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
} from '../services/api'
import KnowledgePanelWrapper from './knowledge/KnowledgePanelWrapper'
import SettingsPanel from './settings/SettingsPanel'
import ThemeToggle from './common/ThemeToggle'

interface SidebarProps {
  isOpen: boolean
  onToggle: () => void
  knowledgeBases: KnowledgeBase[]
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
  conversations: Conversation[]
  activeConversationId: string | null
  onSelectConversation: (conversationId: string) => void
  onCreateConversation: () => void
  onRenameConversation: (conversationId: string, title: string) => void
  onDeleteConversation: (conversationId: string) => void
  config: AppConfig
  isSettingsOpen: boolean
  isKnowledgePanelOpen: boolean
  citationNavigationTarget: CitationNavigationTarget | null
  onToggleSettings: () => void
  onToggleKnowledgePanel: () => void
  onCitationNavigationHandled: () => void
  onChatConfigChange: <K extends keyof ChatConfig>(key: K, value: ChatConfig[K]) => void
  onEmbeddingConfigChange: <K extends keyof EmbeddingConfig>(
    key: K,
    value: EmbeddingConfig[K],
  ) => void
  onRetrievalConfigChange: <K extends keyof RetrievalConfig>(
    key: K,
    value: RetrievalConfig[K],
  ) => void
  chatModeSettings: ChatModeSettings
  onThinkModelChange: (value: string) => void
  onCopyMcpToken: () => Promise<void>
  onResetMcpToken: () => Promise<void>
  onLogout: () => void | Promise<void>
}

const formatDateTime = (value: string) =>
  new Date(value).toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })

const SidebarIcon: React.FC<{
  name: 'settings' | 'knowledge' | 'chevronLeft' | 'chevronRight' | 'more' | 'plus'
}> = ({ name }) => {
  if (name === 'settings') {
    return (
      <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <path d="M12 8.25A3.75 3.75 0 1 0 12 15.75A3.75 3.75 0 0 0 12 8.25Z" stroke="currentColor" strokeWidth="1.8" />
        <path d="M19.05 13.6C19.16 13.08 19.22 12.55 19.22 12C19.22 11.45 19.16 10.92 19.05 10.4L20.95 8.92L18.95 5.48L16.64 6.4C15.8 5.75 14.83 5.25 13.78 4.97L13.45 2.5H10.55L10.22 4.97C9.17 5.25 8.2 5.75 7.36 6.4L5.05 5.48L3.05 8.92L4.95 10.4C4.84 10.92 4.78 11.45 4.78 12C4.78 12.55 4.84 13.08 4.95 13.6L3.05 15.08L5.05 18.52L7.36 17.6C8.2 18.25 9.17 18.75 10.22 19.03L10.55 21.5H13.45L13.78 19.03C14.83 18.75 15.8 18.25 16.64 17.6L18.95 18.52L20.95 15.08L19.05 13.6Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
      </svg>
    )
  }

  if (name === 'knowledge') {
    return (
      <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <path d="M5.5 4.75H10C11.1 4.75 12 5.65 12 6.75V19.25C12 18.15 11.1 17.25 10 17.25H5.5C4.67 17.25 4 16.58 4 15.75V6.25C4 5.42 4.67 4.75 5.5 4.75Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M18.5 4.75H14C12.9 4.75 12 5.65 12 6.75V19.25C12 18.15 12.9 17.25 14 17.25H18.5C19.33 17.25 20 16.58 20 15.75V6.25C20 5.42 19.33 4.75 18.5 4.75Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M7 8.25H9.25M14.75 8.25H17" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    )
  }

  if (name === 'plus') {
    return (
      <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <path d="M12 5V19M5 12H19" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      </svg>
    )
  }

  if (name === 'more') {
    return (
      <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <path d="M6.75 12H6.76M12 12H12.01M17.25 12H17.26" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
      </svg>
    )
  }

  return (
    <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path
        d={name === 'chevronLeft' ? 'M15 6L9 12L15 18' : 'M9 6L15 12L9 18'}
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

const Sidebar: React.FC<SidebarProps> = ({
  isOpen,
  onToggle,
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
  conversations,
  activeConversationId,
  onSelectConversation,
  onCreateConversation,
  onRenameConversation,
  onDeleteConversation,
  config,
  isSettingsOpen,
  isKnowledgePanelOpen,
  citationNavigationTarget,
  onToggleSettings,
  onToggleKnowledgePanel,
  onCitationNavigationHandled,
  onChatConfigChange,
  onEmbeddingConfigChange,
  onRetrievalConfigChange,
  chatModeSettings,
  onThinkModelChange,
  onCopyMcpToken,
  onResetMcpToken,
  onLogout,
}) => {
  const [collapsedKnowledgeBases, setCollapsedKnowledgeBases] = useState<
    Record<string, boolean>
  >({})
  const [menuConversationId, setMenuConversationId] = useState<string | null>(null)
  const [editingConversationId, setEditingConversationId] = useState<string | null>(null)
  const [editingTitle, setEditingTitle] = useState('')
  const [isComposingTitle, setIsComposingTitle] = useState(false)
  const [conversationFilter, setConversationFilter] = useState('')

  const sortedKnowledgeBases = useMemo(() => knowledgeBases, [knowledgeBases])

  const filteredConversations = useMemo(() => {
    if (!conversationFilter.trim()) return conversations
    const q = conversationFilter.toLowerCase()
    return conversations.filter((c) => c.title.toLowerCase().includes(q))
  }, [conversations, conversationFilter])

  const toggleKnowledgeBaseCollapse = (knowledgeBaseId: string) => {
    setCollapsedKnowledgeBases((prev) => ({
      ...prev,
      [knowledgeBaseId]: !prev[knowledgeBaseId],
    }))
  }

  return (
    <>
      <aside className={`sidebar ${isOpen ? 'open' : 'closed'}`} aria-hidden={!isOpen}>
        <div className="sidebar-header">
          <button
            onClick={onToggle}
            className="toggle-btn"
            type="button"
            aria-label={isOpen ? '收起侧边栏' : '展开侧边栏'}
            title={isOpen ? '收起侧边栏' : '展开侧边栏'}
          >
            <SidebarIcon name={isOpen ? 'chevronLeft' : 'chevronRight'} />
          </button>
          <h2>AI LocalBase</h2>
        </div>

        <div className="sidebar-body">
          <section className="section section-conversations">
            <div className="section-title-row">
              <h3>会话</h3>
              <button
                type="button"
                className="ghost-btn"
                onClick={onCreateConversation}
                aria-label="新建会话"
                title="新建会话"
              >
                <SidebarIcon name="plus" />
                <span>新建</span>
              </button>
            </div>

            <div className="sidebar-search-wrap">
              <input
                type="text"
                className="sidebar-search-input"
                placeholder="搜索会话…"
                value={conversationFilter}
                onChange={(e) => setConversationFilter(e.target.value)}
              />
            </div>

            <div className="conversation-list">
              {filteredConversations.map((conversation) => {
                const isMenuOpen = menuConversationId === conversation.id
                const isEditing = editingConversationId === conversation.id

                return (
                  <div
                    key={conversation.id}
                    className={`conversation-item-row ${isMenuOpen ? 'menu-open' : ''}`}
                  >
                    {isEditing ? (
                      <div
                        className={`conversation-item conversation-item-editing ${
                          activeConversationId === conversation.id ? 'active' : ''
                        }`}
                      >
                        <input
                          className="conversation-title-input"
                          type="text"
                          value={editingTitle}
                          autoFocus
                          onFocus={(event) => {
                            event.currentTarget.select()
                          }}
                          onClick={(event) => event.stopPropagation()}
                          onChange={(event) => setEditingTitle(event.currentTarget.value)}
                          onCompositionStart={() => setIsComposingTitle(true)}
                          onCompositionEnd={(event) => {
                            setIsComposingTitle(false)
                            setEditingTitle(event.currentTarget.value)
                          }}
                          onKeyDown={(event) => {
                            event.stopPropagation()

                            if (isComposingTitle || event.nativeEvent.isComposing) {
                              return
                            }

                            if (event.key === 'Enter') {
                              const nextTitle = editingTitle.trim()
                              setEditingConversationId(null)
                              if (!nextTitle || nextTitle === conversation.title.trim()) {
                                return
                              }
                              onRenameConversation(conversation.id, nextTitle)
                            }

                            if (event.key === 'Escape') {
                              setEditingConversationId(null)
                              setEditingTitle(conversation.title)
                            }
                          }}
                          onKeyUp={(event) => {
                            event.stopPropagation()
                          }}
                          onBlur={() => {
                            if (isComposingTitle) {
                              return
                            }

                            const nextTitle = editingTitle.trim()
                            setEditingConversationId(null)
                            if (!nextTitle || nextTitle === conversation.title.trim()) {
                              return
                            }
                            onRenameConversation(conversation.id, nextTitle)
                          }}
                        />
                        <span className="conversation-meta">
                          {conversation.messages.length} 条消息 · {formatDateTime(conversation.updatedAt)}
                        </span>
                      </div>
                    ) : (
                      <button
                        type="button"
                        className={`conversation-item ${
                          activeConversationId === conversation.id ? 'active' : ''
                        }`}
                        onClick={() => {
                          setMenuConversationId(null)
                          setEditingConversationId(null)
                          onSelectConversation(conversation.id)
                        }}
                      >
                        <span className="conversation-title">{conversation.title}</span>
                        <span className="conversation-meta">
                          {conversation.messages.length} 条消息 · {formatDateTime(conversation.updatedAt)}
                        </span>
                      </button>
                    )}

                    <div className="conversation-item-actions">
                      <button
                        type="button"
                        className="conversation-menu-trigger"
                        aria-label="打开会话菜单"
                        onClick={(event) => {
                          event.stopPropagation()
                          setEditingConversationId(null)
                          setMenuConversationId((current) =>
                            current === conversation.id ? null : conversation.id,
                          )
                        }}
                      >
                        <SidebarIcon name="more" />
                      </button>

                      {isMenuOpen && (
                        <div className="conversation-menu" onClick={(event) => event.stopPropagation()}>
                          <button
                            type="button"
                            className="conversation-menu-item"
                            onMouseDown={(event) => event.preventDefault()}
                            onClick={() => {
                              setMenuConversationId(null)
                              setEditingConversationId(conversation.id)
                              setEditingTitle(conversation.title)
                              setIsComposingTitle(false)
                            }}
                          >
                            重命名
                          </button>
                          <button
                            type="button"
                            className="conversation-menu-item danger"
                            onMouseDown={(event) => event.preventDefault()}
                            onClick={() => {
                              const confirmed = window.confirm(`确定删除会话“${conversation.title}”吗？`)
                              setMenuConversationId(null)
                              setEditingConversationId(null)
                              if (!confirmed) {
                                return
                              }
                              onDeleteConversation(conversation.id)
                            }}
                          >
                            删除
                          </button>
                        </div>
                      )}
                    </div>
                  </div>
                )
              })}
            </div>
          </section>

          <div className="sidebar-footer sidebar-footer-icons">
            <button
              type="button"
              className={`sidebar-icon-btn ${isSettingsOpen ? 'active' : ''}`}
              onClick={onToggleSettings}
              aria-label="打开设置"
              title="设置"
            >
              <span className="sidebar-icon-glyph">
                <SidebarIcon name="settings" />
              </span>
              <span>设置</span>
            </button>
            <ThemeToggle />
            <button
              type="button"
              className={`sidebar-icon-btn ${isKnowledgePanelOpen ? 'active' : ''}`}
              onClick={onToggleKnowledgePanel}
              aria-label="打开知识库"
              title="知识库"
            >
              <span className="sidebar-icon-glyph">
                <SidebarIcon name="knowledge" />
              </span>
              <span>知识库</span>
            </button>
          </div>
        </div>
      </aside>

      {isSettingsOpen && (
        <SettingsPanel
          config={config}
          onClose={onToggleSettings}
          onChatConfigChange={onChatConfigChange}
          onEmbeddingConfigChange={onEmbeddingConfigChange}
          onRetrievalConfigChange={onRetrievalConfigChange}
          chatModeSettings={chatModeSettings}
          onThinkModelChange={onThinkModelChange}
          onCopyMcpToken={onCopyMcpToken}
          onResetMcpToken={onResetMcpToken}
          onLogout={onLogout}
        />
      )}

      <KnowledgePanelWrapper
        open={isKnowledgePanelOpen}
        knowledgeBases={sortedKnowledgeBases}
        collapsedKnowledgeBases={collapsedKnowledgeBases}
        onToggleCollapse={toggleKnowledgeBaseCollapse}
        selectedKnowledgeBaseId={selectedKnowledgeBaseId}
        selectedDocumentId={selectedDocumentId}
        onSelectKnowledgeBase={onSelectKnowledgeBase}
        onSelectDocument={onSelectDocument}
        onCreateKnowledgeBase={onCreateKnowledgeBase}
        onDeleteKnowledgeBase={onDeleteKnowledgeBase}
        onUploadFiles={onUploadFiles}
        onUploadDirectory={onUploadDirectory}
        onGenerateEvalDataset={onGenerateEvalDataset}
        onListEvalDatasets={onListEvalDatasets}
        onListEvalRuns={onListEvalRuns}
        onFetchEvalDataset={onFetchEvalDataset}
        onDeleteEvalDataset={onDeleteEvalDataset}
        onAddEvalDatasetCandidate={onAddEvalDatasetCandidate}
        onUpdateEvalDatasetItem={onUpdateEvalDatasetItem}
        onDeleteEvalDatasetItem={onDeleteEvalDatasetItem}
        onRunEvalDataset={onRunEvalDataset}
        directoryUploadTask={directoryUploadTask}
        onCancelDirectoryUpload={onCancelDirectoryUpload}
        onContinueDirectoryUpload={onContinueDirectoryUpload}
        onRemoveDocument={onRemoveDocument}
        onFetchKnowledgeBaseHealth={onFetchKnowledgeBaseHealth}
        onFetchDocumentDetail={onFetchDocumentDetail}
        onReindexDocument={onReindexDocument}
        onDebugRetrieval={onDebugRetrieval}
        citationNavigationTarget={citationNavigationTarget}
        onCitationNavigationHandled={onCitationNavigationHandled}
        onClose={onToggleKnowledgePanel}
      />

    </>
  )
}

export default Sidebar
