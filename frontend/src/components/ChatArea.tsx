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
import MarkdownRenderer from './chat/MarkdownRenderer'
import { chunkKindLabel } from './knowledge/knowledgeLabels'
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

const sourceIdentity = (source: ChatSourceMetadata, index: number) =>
  [
    source.toolName,
    source.knowledgeBaseId,
    source.documentId,
    source.chunkId,
    source.chunkIndex,
  ].filter(Boolean).join(':') || `source-${index}`

const normalizeSources = (sources?: ChatSourceMetadata[]) => {
  if (!sources || sources.length === 0) return []
  const seen = new Set<string>()
  return sources.filter((source, index) => {
    const key = sourceIdentity(source, index)
    if (seen.has(key)) return false
    seen.add(key)
    return Boolean(source.documentName || source.toolName || source.snippet)
  })
}

const sourceTypeLabel = (source: ChatSourceMetadata) => {
  if (source.toolName) return `工具：${source.toolName}`
  if (source.sourceType === 'structured-data') return '结构化数据'
  if (source.chunkKind) return chunkKindLabel(source.chunkKind)
  return '来源'
}

const sourceRankLabel = (source: ChatSourceMetadata, index: number) => {
  if (source.chunkIndex) return `#${source.chunkIndex}`
  return `#${index + 1}`
}

const scoreLabel = (score?: string) => {
  if (!score) return ''
  const value = Number(score)
  if (!Number.isFinite(value)) return ''
  return `分数 ${value.toFixed(4)}`
}

const citationConfidenceLabel = (value?: string) => {
  switch (value) {
    case 'high':
      return '强证据'
    case 'medium':
      return '中证据'
    case 'low':
      return '弱证据'
    default:
      return ''
  }
}

const MessageCitations: React.FC<{
  sources: ChatSourceMetadata[]
  onOpenCitationSource?: (source: ChatSourceMetadata) => void
}> = ({ sources, onOpenCitationSource }) => {
  const visibleSources = normalizeSources(sources).slice(0, 6)
  if (visibleSources.length === 0) return null

  return (
    <details className="message-citations">
      <summary>
        <span>引用来源</span>
        <strong>{visibleSources.length}</strong>
      </summary>
      <div className="message-citation-list">
        {visibleSources.map((source, index) => (
          <article className="message-citation" key={sourceIdentity(source, index)}>
            <div className="message-citation-head">
              <strong>{source.documentName || source.toolName || '未知来源'}</strong>
              <span>{sourceTypeLabel(source)}</span>
              <span>{sourceRankLabel(source, index)}</span>
              {citationConfidenceLabel(source.citationConfidence) && (
                <span>{citationConfidenceLabel(source.citationConfidence)}</span>
              )}
              {scoreLabel(source.score) && <span>{scoreLabel(source.score)}</span>}
              {source.documentId && (
                <button
                  type="button"
                  onClick={() => onOpenCitationSource?.(source)}
                  disabled={!onOpenCitationSource}
                >
                  定位
                </button>
              )}
            </div>
            {source.snippet && <p>{source.snippet}</p>}
          </article>
        ))}
      </div>
    </details>
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
  onOpenCitationSource,
}) => {
  const [inputValue, setInputValue] = useState('')
  const [copiedMessageId, setCopiedMessageId] = useState<string | null>(null)
  const messagesEndRef = useRef<HTMLDivElement | null>(null)

  const canSend = inputValue.trim().length > 0 && !(enforceSingleFlight && isGlobalGenerating)

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

  const activeModeModel =
    chatMode === 'think'
      ? chatModeSettings.thinkModel || config.chat.model
      : chatModeSettings.fastModel || config.chat.model

  const toolbarItems = [
    {
      icon: '📚',
      text: scopeText,
    },
    {
      icon: chatMode === 'think' ? '🧠' : '⚡',
      text: `${chatMode === 'think' ? '思考模式' : '快速模式'} · ${activeModeModel}`,
    },
    {
      icon: '💬',
      text: `${conversationStats.totalCount} 条消息`,
    },
  ]

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

  return (
    <main className={`chat-area ${sidebarOpen ? 'sidebar-open' : 'sidebar-closed'}`}>
      <div className="chat-topbar">
        <div className="chat-topbar-left">
          <span className="chat-topbar-title">AI Assistant</span>
          <span className="chat-topbar-sep">·</span>
          <span className="chat-topbar-hint">{activeConversation.title}</span>
          <span className="chat-topbar-sep">·</span>
          <span className="chat-topbar-hint">{formatTime(activeConversation.updatedAt)}</span>
        </div>

        <div className="chat-topbar-pills">
          {toolbarItems.map((item) => (
            <div key={item.text} className="topbar-pill" title={item.text}>
              <span className="topbar-pill-icon">{item.icon}</span>
              <span className="topbar-pill-text">{item.text}</span>
            </div>
          ))}
        </div>

        <div className="chat-topbar-right">
          {enforceSingleFlight && isGlobalGenerating && (
            <span className="chat-topbar-hint" aria-live="polite">
              正在后台生成：{generatingConversationTitle}
            </span>
          )}
          <button
            type="button"
            className="chat-clear-btn"
            onClick={onClearConversation}
            disabled={isLoading}
          >
            清空对话
          </button>
        </div>
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
            const degradedMetadata =
              message.role === 'assistant' && message.metadata?.degraded
                ? message.metadata
                : null

            return (
              <div key={message.id} className={`message ${message.role}`}>
                {!isStreamingPlaceholder && message.content.trim() && (
                  <button
                    type="button"
                    className="message-copy-btn"
                    onClick={() => {
                      void handleCopyMessage(message.id, message.content)
                    }}
                    aria-label="复制消息"
                    title={copiedMessageId === message.id ? '已复制' : '复制消息'}
                  >
                    {copiedMessageId === message.id ? '✓' : '⧉'}
                  </button>
                )}
                <div
                  className={`message-content ${
                    isStreamingPlaceholder ? 'message-content-thinking' : ''
                  } ${message.role === 'assistant' ? 'message-content-markdown' : ''}`}
                >
                  {degradedMetadata && (
                    <div className="message-degraded-banner" role="status" aria-live="polite">
                      <div className="message-degraded-title">
                        ⚠ 当前回答为降级回复，模型或检索链路出现异常
                      </div>
                      {degradedMetadata.fallbackStrategy && (
                        <div className="message-degraded-detail">
                          策略：{degradedMetadata.fallbackStrategy}
                        </div>
                      )}
                      {degradedMetadata.upstreamError && (
                        <div className="message-degraded-subtle">
                          上游错误：{degradedMetadata.upstreamError}
                        </div>
                      )}
                    </div>
                  )}
                  {isStreamingPlaceholder ? (
                    <div className="thinking-indicator" aria-label="AI 正在思考">
                      <span className="thinking-dot" />
                      <span className="thinking-dot" />
                      <span className="thinking-dot" />
                    </div>
                  ) : message.role === 'assistant' ? (
                    <MarkdownRenderer content={message.content} />
                  ) : (
                    message.content
                  )}
                </div>
                {message.role === 'assistant' && message.metadata?.sources && (
                  <MessageCitations
                    sources={message.metadata.sources}
                    onOpenCitationSource={onOpenCitationSource}
                  />
                )}
                <div className="message-time">{formatTime(message.timestamp)}</div>
              </div>
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

      <div className="input-area">
        <div className="input-mode-bar">
          <div className="input-mode-group" role="tablist" aria-label="回答模式选择">
            <button
              type="button"
              className={`input-mode-btn ${chatMode === 'fast' ? 'active' : ''}`}
              onClick={() => onChatModeChange('fast')}
              disabled={isLoading}
            >
              ⚡ 快速模式
            </button>
            <button
              type="button"
              className={`input-mode-btn ${chatMode === 'think' ? 'active' : ''}`}
              onClick={() => onChatModeChange('think')}
              disabled={isLoading}
            >
              🧠 思考模式
            </button>
          </div>
          <div className="input-mode-hint">
            <div>
              当前使用：{chatMode === 'think' ? '思考模式' : '快速模式'}
              {' · '}
              模型：{activeModeModel}
            </div>
            <div className="input-mode-description">
              {chatMode === 'think'
                ? '质量优先，适合复杂分析与推理，响应会更慢。'
                : '速度优先，适合日常问答与知识库检索。'}
            </div>
          </div>
        </div>
        <div className="input-container">
          <textarea
            value={inputValue}
            onChange={(event) => setInputValue(event.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={
              enforceSingleFlight && isGlobalGenerating
                ? `当前正在后台生成「${generatingConversationTitle}」，请等待完成后再发送`
                : '输入您的问题，Enter 发送，Shift + Enter 换行'
            }
            rows={3}
          />
          <button
            type="button"
            onClick={() => {
              void handleSubmit()
            }}
            disabled={!canSend}
            className="send-btn"
          >
            {isLoading ? '发送中...' : enforceSingleFlight && isGlobalGenerating ? '排队中' : '发送'}
          </button>
        </div>
      </div>
    </main>
  )
}

export default ChatArea
