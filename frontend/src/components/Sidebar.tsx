import React, { useMemo, useState } from 'react'
import {
  AppConfig,
  ChatConfig,
  ChatModeSettings,
  Conversation,
  DirectoryUploadTask,
  EmbeddingConfig,
  KnowledgeBase,
} from '../App'
import KnowledgePanel from './knowledge/KnowledgePanel'
import SettingsPanel from './settings/SettingsPanel'

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
  directoryUploadTask: DirectoryUploadTask
  onCancelDirectoryUpload: () => void
  onContinueDirectoryUpload: () => void
  onRemoveDocument: (knowledgeBaseId: string, documentId: string) => void
  conversations: Conversation[]
  activeConversationId: string | null
  onSelectConversation: (conversationId: string) => void
  onCreateConversation: () => void
  onRenameConversation: (conversationId: string, title: string) => void
  onDeleteConversation: (conversationId: string) => void
  config: AppConfig
  isSettingsOpen: boolean
  isKnowledgePanelOpen: boolean
  onToggleSettings: () => void
  onToggleKnowledgePanel: () => void
  onChatConfigChange: <K extends keyof ChatConfig>(key: K, value: ChatConfig[K]) => void
  onEmbeddingConfigChange: <K extends keyof EmbeddingConfig>(
    key: K,
    value: EmbeddingConfig[K],
  ) => void
  chatModeSettings: ChatModeSettings
  onThinkModelChange: (value: string) => void
  onCopyMcpToken: () => Promise<void>
  onResetMcpToken: () => Promise<void>
}

const formatDateTime = (value: string) =>
  new Date(value).toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })

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
  directoryUploadTask,
  onCancelDirectoryUpload,
  onContinueDirectoryUpload,
  onRemoveDocument,
  conversations,
  activeConversationId,
  onSelectConversation,
  onCreateConversation,
  onRenameConversation,
  onDeleteConversation,
  config,
  isSettingsOpen,
  isKnowledgePanelOpen,
  onToggleSettings,
  onToggleKnowledgePanel,
  onChatConfigChange,
  onEmbeddingConfigChange,
  chatModeSettings,
  onThinkModelChange,
  onCopyMcpToken,
  onResetMcpToken,
}) => {
  const [collapsedKnowledgeBases, setCollapsedKnowledgeBases] = useState<
    Record<string, boolean>
  >({})
  const [menuConversationId, setMenuConversationId] = useState<string | null>(null)
  const [editingConversationId, setEditingConversationId] = useState<string | null>(null)
  const [editingTitle, setEditingTitle] = useState('')
  const [isComposingTitle, setIsComposingTitle] = useState(false)

  const sortedKnowledgeBases = useMemo(() => knowledgeBases, [knowledgeBases])

  const toggleKnowledgeBaseCollapse = (knowledgeBaseId: string) => {
    setCollapsedKnowledgeBases((prev) => ({
      ...prev,
      [knowledgeBaseId]: !prev[knowledgeBaseId],
    }))
  }

  return (
    <>
      <aside className={`sidebar ${isOpen ? 'open' : 'closed'}`}>
        <div className="sidebar-header">
          <button onClick={onToggle} className="toggle-btn" type="button">
            {isOpen ? '◁' : '▷'}
          </button>
          <h2>AI LocalBase</h2>
        </div>

        <div className="sidebar-body">
          <section className="section section-conversations">
            <div className="section-title-row">
              <h3>会话</h3>
              <button type="button" className="ghost-btn" onClick={onCreateConversation}>
                ＋ 新建
              </button>
            </div>

            <div className="conversation-list">
              {conversations.map((conversation) => {
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
                        ⋯
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
              title="设置"
            >
              <span className="sidebar-icon-glyph">⚙️</span>
              <span>设置</span>
            </button>
            <button
              type="button"
              className={`sidebar-icon-btn ${isKnowledgePanelOpen ? 'active' : ''}`}
              onClick={onToggleKnowledgePanel}
              title="知识库"
            >
              <span className="sidebar-icon-glyph">📘</span>
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
          chatModeSettings={chatModeSettings}
          onThinkModelChange={onThinkModelChange}
          onCopyMcpToken={onCopyMcpToken}
          onResetMcpToken={onResetMcpToken}
        />
      )}

      <KnowledgePanel
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
        directoryUploadTask={directoryUploadTask}
        onCancelDirectoryUpload={onCancelDirectoryUpload}
        onContinueDirectoryUpload={onContinueDirectoryUpload}
        onRemoveDocument={onRemoveDocument}
        onClose={onToggleKnowledgePanel}
      />
    </>
  )
}

export default Sidebar
