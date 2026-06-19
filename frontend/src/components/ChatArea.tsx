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
  onOpenCitationSource,
}) => {
  const [inputValue, setInputValue] = useState('')
  const [copiedMessageId, setCopiedMessageId] = useState<string | null>(null)
  const [showModeMenu, setShowModeMenu] = useState(false)
  const [showTopbarSub, setShowTopbarSub] = useState(false)
  const [showClearConfirm, setShowClearConfirm] = useState(false)
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
              📚 {knowledgeBaseBadgeText}
            </span>
            <span className="topbar-badge topbar-badge-scope" title={retrievalScopeTitle}>
              📄 {retrievalScopeText}
            </span>
            <span className="topbar-badge" title={`${chatMode === 'think' ? '思考模式' : '快速模式'} · ${activeModeModel}`}>
              {chatMode === 'think' ? '🧠' : '⚡'} {activeModeModel}
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
                className="chat-topbar-info-btn"
                onClick={() => setShowTopbarSub(prev => !prev)}
                title="消息统计"
                aria-label="消息统计"
              >
                <span className="chat-topbar-info-icon" aria-hidden="true" />
              </button>
              <button
                type="button"
                className="chat-topbar-clear-btn"
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

        {showTopbarSub && (
          <div className="chat-topbar-sub">
            <span className="topbar-sub-stat">💬 {conversationStats.totalCount} 条消息</span>
            <span className="topbar-sub-stat">👤 {conversationStats.userCount} 条提问</span>
            <span className="topbar-sub-stat">🕐 {formatTime(activeConversation.updatedAt)}</span>
          </div>
        )}
      </div>

      <div className="messages-container">
        {activeConversation.messages.length === 0 ? (
          <div className="welcome-message">
            <h2>欢迎使用 AI LocalBase</h2>
            <p>先选择知识库，或者指定知识库中的单个文档后再进行问答</p>
          </div>
        ) : (
          activeConversation.messages.map((message) => {
            const isStreamingPlaceholder =
              isLoading &&
              message.role === 'assistant' &&
              message.id === activeConversation.messages.at(-1)?.id &&
              !message.content.trim()

            return (
              <MessageCard
                key={message.id}
                message={message}
                isLoading={isLoading}
                isStreamingPlaceholder={isStreamingPlaceholder}
                onCopyMessage={handleCopyMessage}
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
          <span className="input-context-icon">📚</span>
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
            >
              {chatMode === 'think' ? '🧠' : '⚡'}
            </button>
            {showModeMenu && (
              <div className="input-mode-dropdown">
                <button
                  type="button"
                  className={`input-mode-dropdown-item ${chatMode === 'fast' ? 'active' : ''}`}
                  onClick={() => { onChatModeChange('fast'); setShowModeMenu(false) }}
                >
                  <span>⚡</span>
                  <span className="input-mode-dropdown-label">
                    <strong>快速模式</strong>
                    <small>速度优先，日常问答</small>
                  </span>
                </button>
                <button
                  type="button"
                  className={`input-mode-dropdown-item ${chatMode === 'think' ? 'active' : ''}`}
                  onClick={() => { onChatModeChange('think'); setShowModeMenu(false) }}
                >
                  <span>🧠</span>
                  <span className="input-mode-dropdown-label">
                    <strong>思考模式</strong>
                    <small>质量优先，复杂分析</small>
                  </span>
                </button>
                <div className="input-mode-dropdown-model">
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
            {isLoading ? '…' : '↑'}
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
    </main>
  )
}

export default ChatArea
