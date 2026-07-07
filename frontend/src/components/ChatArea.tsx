import React, { useEffect, useMemo, useRef, useState } from 'react'
import type {
  AppConfig,
  ChatMode,
  ChatModeSettings,
  Conversation,
  DocumentItem,
  ChatSourceMetadata,
  KnowledgeBase,
} from '../App'
import MessageCard from './chat/MessageCard'
import ConversationExportDialog from './chat/ConversationExportDialog'
import ConfirmDialog from './common/ConfirmDialog'

interface ChatAreaProps {
  sidebarOpen: boolean
  activeConversation: Conversation
  selectedKnowledgeBase: KnowledgeBase | null
  selectedDocument: DocumentItem | null
  config: AppConfig
  chatMode: ChatMode
  chatModeSettings: ChatModeSettings
  isLoading: boolean
  isGlobalGenerating: boolean
  generatingConversationTitle: string
  enforceSingleFlight: boolean
  onChatModeChange: (mode: ChatMode) => void
  onSendMessage: (content: string) => Promise<void>
  onClearConversation: () => void
  onEditMessage?: (messageId: string, newContent: string) => Promise<void>
  onDeleteMessage?: (messageId: string) => Promise<void>
  onRegenerateMessage?: (messageId: string) => Promise<void>
  onExportConversation?: (conversationId: string, format: 'markdown') => Promise<string>
  onOpenCitationSource?: (source: ChatSourceMetadata) => void
}

const suggestedPrompts = [
  '请总结当前知识库的核心观点',
  '请列出这个知识库中最关键的结论',
  '如果基于当前资料开始实现，下一步建议是什么？',
]

const formatTime = (value: string) =>
  new Date(value).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
  })

type ChatIconName = 'bolt' | 'brain' | 'clock' | 'database' | 'file' | 'message' | 'send' | 'user'

const ChatIcon: React.FC<{ name: ChatIconName }> = ({ name }) => {
  const commonProps = {
    viewBox: '0 0 24 24',
    fill: 'none',
    'aria-hidden': true,
  }

  if (name === 'database') {
    return (
      <svg {...commonProps}>
        <ellipse cx="12" cy="5.75" rx="6.5" ry="2.75" stroke="currentColor" strokeWidth="1.8" />
        <path d="M5.5 5.75V12C5.5 13.52 8.41 14.75 12 14.75C15.59 14.75 18.5 13.52 18.5 12V5.75" stroke="currentColor" strokeWidth="1.8" />
        <path d="M5.5 12V18.25C5.5 19.77 8.41 21 12 21C15.59 21 18.5 19.77 18.5 18.25V12" stroke="currentColor" strokeWidth="1.8" />
      </svg>
    )
  }

  if (name === 'file') {
    return (
      <svg {...commonProps}>
        <path d="M7 3.75H13.25L18 8.5V20.25H7V3.75Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M13 3.75V8.75H18" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M9.5 13H14.5M9.5 16H13" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    )
  }

  if (name === 'brain') {
    return (
      <svg {...commonProps}>
        <path d="M8.4 8.2A3 3 0 0 1 11.2 4A3 3 0 0 1 14 6.1A3.2 3.2 0 0 1 18.4 9.1A3 3 0 0 1 18 14.9A3.2 3.2 0 0 1 14 18.7A3 3 0 0 1 9.8 18.1A3.1 3.1 0 0 1 6 14.8A3.2 3.2 0 0 1 5.6 8.7A3 3 0 0 1 8.4 8.2Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M12 6.25V18M8.5 10.25H12M12 13.75H15.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    )
  }

  if (name === 'bolt') {
    return (
      <svg {...commonProps}>
        <path d="M13.25 3.75L5.75 13H11.25L10.75 20.25L18.25 10.75H12.75L13.25 3.75Z" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    )
  }

  if (name === 'message') {
    return (
      <svg {...commonProps}>
        <path d="M5 6.5C5 5.4 5.9 4.5 7 4.5H17C18.1 4.5 19 5.4 19 6.5V13.5C19 14.6 18.1 15.5 17 15.5H11L6.75 19V15.5H7C5.9 15.5 5 14.6 5 13.5V6.5Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
      </svg>
    )
  }

  if (name === 'user') {
    return (
      <svg {...commonProps}>
        <path d="M12 12.25A3.75 3.75 0 1 0 12 4.75A3.75 3.75 0 0 0 12 12.25Z" stroke="currentColor" strokeWidth="1.8" />
        <path d="M5.75 20C6.55 16.95 8.75 15.25 12 15.25C15.25 15.25 17.45 16.95 18.25 20" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    )
  }

  if (name === 'clock') {
    return (
      <svg {...commonProps}>
        <path d="M12 21A9 9 0 1 0 12 3A9 9 0 0 0 12 21Z" stroke="currentColor" strokeWidth="1.8" />
        <path d="M12 7.5V12L15 14" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    )
  }

  return (
    <svg {...commonProps}>
      <path d="M12 5V19M12 5L6.75 10.25M12 5L17.25 10.25" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

const ChatArea: React.FC<ChatAreaProps> = ({
  sidebarOpen,
  activeConversation,
  selectedKnowledgeBase,
  selectedDocument,
  config,
  chatMode,
  chatModeSettings,
  isLoading,
  isGlobalGenerating,
  generatingConversationTitle,
  enforceSingleFlight,
  onChatModeChange,
  onSendMessage,
  onClearConversation,
  onEditMessage,
  onDeleteMessage,
  onRegenerateMessage,
  onExportConversation,
  onOpenCitationSource,
}) => {
  const [inputValue, setInputValue] = useState('')
  const [copiedMessageId, setCopiedMessageId] = useState<string | null>(null)
  const [showModeMenu, setShowModeMenu] = useState(false)
  const [showClearConfirm, setShowClearConfirm] = useState(false)
  const [showExportDialog, setShowExportDialog] = useState(false)
  const [editingTitle, setEditingTitle] = useState(false)
  const [titleDraft, setTitleDraft] = useState(activeConversation.title)
  const messagesEndRef = useRef<HTMLDivElement | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement | null>(null)

  const canSend = inputValue.trim().length > 0 && !(enforceSingleFlight && isGlobalGenerating)

  const hasMessages = activeConversation.messages.length > 0

  // Auto-resize textarea
  useEffect(() => {
    const textarea = textareaRef.current
    if (!textarea) return
    textarea.style.height = 'auto'
    const lineHeight = 22
    const maxHeight = lineHeight * 6
    textarea.style.height = `${Math.min(textarea.scrollHeight, maxHeight)}px`
  }, [inputValue])

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [activeConversation.messages, isLoading])

  const conversationStats = useMemo(() => {
    const userCount = activeConversation.messages.filter(
      (message) => message.role === 'user',
    ).length

    return {
      userCount,
      totalCount: activeConversation.messages.length,
    }
  }, [activeConversation.messages])

  const hasUserMessages = conversationStats.userCount > 0
  const canUseMessageActions = !isGlobalGenerating
  const canExportConversation = Boolean(onExportConversation) && hasUserMessages && !isGlobalGenerating

  const scopeText = selectedDocument
    ? `文档问答：${selectedDocument.name}`
    : selectedKnowledgeBase
      ? `知识库问答：${selectedKnowledgeBase.name}`
      : '未选择知识库'
  const knowledgeBaseBadgeText = selectedKnowledgeBase?.name ?? '未选择知识库'
  const retrievalScopeText = selectedDocument
    ? `文档：${selectedDocument.name}`
    : selectedKnowledgeBase
      ? '全部文档'
      : '未选择范围'
  const retrievalScopeTitle = selectedDocument
    ? `当前检索范围：单独文档「${selectedDocument.name}」`
    : selectedKnowledgeBase
      ? `当前检索范围：知识库「${selectedKnowledgeBase.name}」的全部文档`
      : '当前检索范围：未选择'

  const activeModeModel =
    chatMode === 'think'
      ? chatModeSettings.thinkModel || config.chat.model
      : chatModeSettings.fastModel || config.chat.model

  const handleSubmit = async () => {
    const content = inputValue.trim()
    if (!content || isLoading) {
      return
    }

    setInputValue('')
    await onSendMessage(content)
  }

  const handleKeyDown = async (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      await handleSubmit()
    }
  }

  const handleModeMenuKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    const menuItems = Array.from(
      event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]'),
    )

    if (event.key === 'Escape') {
      event.preventDefault()
      setShowModeMenu(false)
      return
    }

    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key) || menuItems.length === 0) {
      return
    }

    event.preventDefault()
    const activeIndex = Math.max(0, menuItems.indexOf(document.activeElement as HTMLButtonElement))
    const nextIndex =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? menuItems.length - 1
          : event.key === 'ArrowUp'
            ? (activeIndex - 1 + menuItems.length) % menuItems.length
            : (activeIndex + 1) % menuItems.length

    menuItems[nextIndex]?.focus()
  }

  const handleCopyMessage = async (messageId: string, content: string) => {
    try {
      await navigator.clipboard.writeText(content)
      setCopiedMessageId(messageId)
      window.setTimeout(() => {
        setCopiedMessageId((prev) => (prev === messageId ? null : prev))
      }, 1500)
    } catch {
      // 忽略复制异常，避免影响主流程
    }
  }

  const handleConfirmClearConversation = () => {
    setShowClearConfirm(false)
    onClearConversation()
  }

  return (
    <main className={`chat-area ${sidebarOpen ? 'sidebar-open' : 'sidebar-closed'}`}>
      <div className="chat-topbar">
        <div className="chat-topbar-main">
          <div className="chat-topbar-left">
            {editingTitle ? (
              <input
                className="chat-topbar-title-input"
                value={titleDraft}
                onChange={(e) => setTitleDraft(e.target.value)}
                onBlur={() => setEditingTitle(false)}
                onKeyDown={(e) => { if (e.key === 'Enter') setEditingTitle(false) }}
                autoFocus
              />
            ) : (
              <span
                className="chat-topbar-title"
                onDoubleClick={() => { setTitleDraft(activeConversation.title); setEditingTitle(true) }}
                title="双击编辑标题"
              >
                {activeConversation.title}
              </span>
            )}
          </div>

          <div className="chat-topbar-badges">
            <span className="topbar-badge topbar-badge-kb" title={`当前知识库：${knowledgeBaseBadgeText}`}>
              <ChatIcon name="database" />
              <span>{knowledgeBaseBadgeText}</span>
            </span>
            <span className="topbar-badge topbar-badge-scope" title={retrievalScopeTitle}>
              <ChatIcon name="file" />
              <span>{retrievalScopeText}</span>
            </span>
            <span className="topbar-badge" title={`${chatMode === 'think' ? '思考模式' : '快速模式'} · ${activeModeModel}`}>
              <ChatIcon name={chatMode === 'think' ? 'brain' : 'bolt'} />
              <span>{activeModeModel}</span>
            </span>
          </div>

          <div className="chat-topbar-right">
            {enforceSingleFlight && isGlobalGenerating && (
              <span className="chat-topbar-hint" aria-live="polite">
                生成中：{generatingConversationTitle}
              </span>
            )}
            <div className="chat-topbar-actions" aria-label="对话操作">
              <button
                type="button"
                className="chat-topbar-action-btn chat-topbar-export-btn"
                onClick={() => setShowExportDialog(true)}
                disabled={!canExportConversation}
                title={canExportConversation ? '导出对话' : '暂无可导出的对话'}
                aria-label="导出对话"
              >
                <span className="chat-topbar-export-icon" aria-hidden="true" />
              </button>
              <button
                type="button"
                className="chat-topbar-action-btn chat-topbar-clear-btn"
                onClick={() => setShowClearConfirm(true)}
                disabled={isLoading}
                title="清空对话"
                aria-label="清空对话"
              >
                <span className="chat-topbar-clear-icon" aria-hidden="true" />
              </button>
            </div>
          </div>
        </div>

        <div className="chat-topbar-sub" aria-label="对话统计">
          <span className="topbar-sub-stat">
            <ChatIcon name="message" />
            {conversationStats.totalCount} 条消息
          </span>
          <span className="topbar-sub-stat">
            <ChatIcon name="user" />
            {conversationStats.userCount} 条提问
          </span>
          <span className="topbar-sub-stat">
            <ChatIcon name="clock" />
            {formatTime(activeConversation.updatedAt)}
          </span>
        </div>
      </div>

      <div className="messages-container">
        {activeConversation.messages.length === 0 ? (
          <div className="welcome-message">
            <h2>欢迎使用 AI LocalBase</h2>
            <p>先选择知识库，或者指定知识库中的单个文档后再进行问答</p>
          </div>
        ) : (
          activeConversation.messages.map((message, index) => {
            const isStreamingPlaceholder =
              isLoading &&
              message.role === 'assistant' &&
              message.id === activeConversation.messages.at(-1)?.id &&
              !message.content.trim()
            const previousMessage = activeConversation.messages[index - 1]
            const canDeleteMessage =
              canUseMessageActions && activeConversation.messages.length > 1
            const canRegenerateMessage =
              canUseMessageActions &&
              message.role === 'assistant' &&
              previousMessage?.role === 'user'

            return (
              <MessageCard
                key={message.id}
                message={message}
                isLoading={isLoading}
                isStreamingPlaceholder={isStreamingPlaceholder}
                onCopyMessage={handleCopyMessage}
                onEditMessage={canUseMessageActions ? onEditMessage : undefined}
                onDeleteMessage={canDeleteMessage ? onDeleteMessage : undefined}
                onRegenerateMessage={
                  canRegenerateMessage ? onRegenerateMessage : undefined
                }
                onOpenCitationSource={onOpenCitationSource}
                copiedMessageId={copiedMessageId}
              />
            )
          })
        )}

        {isLoading && activeConversation.messages.at(-1)?.role !== 'assistant' && (
          <div className="message assistant loading">
            <div className="message-content">AI 正在生成回答...</div>
          </div>
        )}

        <div ref={messagesEndRef} />
      </div>

      {!hasMessages && (
        <div className="prompt-list">
          {suggestedPrompts.map((prompt) => (
            <button
              key={prompt}
              type="button"
              className="prompt-chip"
              disabled={enforceSingleFlight && isGlobalGenerating}
              onClick={() => {
                void onSendMessage(prompt)
              }}
            >
              {prompt}
            </button>
          ))}
        </div>
      )}

      <div className="input-area">
        <div className="input-context-row">
          <span className="input-context-icon">
            <ChatIcon name="database" />
          </span>
          <span className="input-context-text">{scopeText}</span>
          {selectedDocument && (
            <span className="input-context-badge">文档</span>
          )}
        </div>
        <div className="input-container">
          <div className="input-mode-compact">
            <button
              type="button"
              className="input-mode-toggle"
              onClick={() => setShowModeMenu((prev) => !prev)}
              title={`${chatMode === 'think' ? '思考模式' : '快速模式'} · ${activeModeModel}`}
              aria-label={`切换输入模式，当前为${chatMode === 'think' ? '思考模式' : '快速模式'}，模型 ${activeModeModel}`}
              aria-controls="input-mode-menu"
              aria-expanded={showModeMenu}
              aria-haspopup="menu"
            >
              <ChatIcon name={chatMode === 'think' ? 'brain' : 'bolt'} />
            </button>
            {showModeMenu && (
              <div
                className="input-mode-dropdown"
                id="input-mode-menu"
                role="menu"
                aria-label="输入模式选项"
                onKeyDown={handleModeMenuKeyDown}
              >
                <button
                  type="button"
                  className={`input-mode-dropdown-item ${chatMode === 'fast' ? 'active' : ''}`}
                  onClick={() => { onChatModeChange('fast'); setShowModeMenu(false) }}
                  role="menuitemradio"
                  aria-checked={chatMode === 'fast'}
                  tabIndex={chatMode === 'fast' ? 0 : -1}
                >
                  <span className="input-mode-dropdown-icon">
                    <ChatIcon name="bolt" />
                  </span>
                  <span className="input-mode-dropdown-label">
                    <strong>快速模式</strong>
                    <small>速度优先，日常问答</small>
                  </span>
                </button>
                <button
                  type="button"
                  className={`input-mode-dropdown-item ${chatMode === 'think' ? 'active' : ''}`}
                  onClick={() => { onChatModeChange('think'); setShowModeMenu(false) }}
                  role="menuitemradio"
                  aria-checked={chatMode === 'think'}
                  tabIndex={chatMode === 'think' ? 0 : -1}
                >
                  <span className="input-mode-dropdown-icon">
                    <ChatIcon name="brain" />
                  </span>
                  <span className="input-mode-dropdown-label">
                    <strong>思考模式</strong>
                    <small>质量优先，复杂分析</small>
                  </span>
                </button>
                <div className="input-mode-dropdown-model" role="presentation">
                  模型：{activeModeModel}
                </div>
              </div>
            )}
          </div>
          <textarea
            ref={textareaRef}
            value={inputValue}
            onChange={(event) => setInputValue(event.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={
              enforceSingleFlight && isGlobalGenerating
                ? `当前正在后台生成「${generatingConversationTitle}」，请等待完成后再发送`
                : '输入您的问题，Enter 发送，Shift + Enter 换行'
            }
            rows={1}
          />
          <button
            type="button"
            onClick={() => {
              void handleSubmit()
            }}
            disabled={!canSend}
            className={`send-btn ${canSend ? 'send-btn-active' : ''}`}
            aria-label="发送消息"
          >
            {isLoading ? <span className="send-loading-dot" /> : <ChatIcon name="send" />}
          </button>
        </div>
      </div>

      <ConfirmDialog
        open={showClearConfirm}
        title="清空对话"
        message="确认清空当前对话的全部消息吗？此操作不可撤销。"
        confirmText="清空"
        cancelText="取消"
        onConfirm={handleConfirmClearConversation}
        onCancel={() => setShowClearConfirm(false)}
      />

      {onExportConversation && (
        <ConversationExportDialog
          conversation={activeConversation}
          isOpen={showExportDialog}
          onClose={() => setShowExportDialog(false)}
          onExport={onExportConversation}
        />
      )}
    </main>
  )
}

export default ChatArea
