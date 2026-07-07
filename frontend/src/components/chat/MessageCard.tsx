import React, { useState } from 'react'
import type { ChatMessage, ChatSourceMetadata } from '../../App'
import MarkdownRenderer from './MarkdownRenderer'
import MessageCitations from './MessageCitations'

interface MessageCardProps {
  message: ChatMessage
  isLoading: boolean
  isStreamingPlaceholder: boolean
  onCopyMessage: (messageId: string, content: string) => Promise<void>
  onEditMessage?: (messageId: string, newContent: string) => Promise<void>
  onDeleteMessage?: (messageId: string) => Promise<void>
  onRegenerateMessage?: (messageId: string) => Promise<void>
  onOpenCitationSource?: (source: ChatSourceMetadata) => void
  copiedMessageId: string | null
}

const formatTime = (value: string) =>
  new Date(value).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
  })

type MessageActionIconName = 'alert' | 'check' | 'copy' | 'edit' | 'refresh' | 'spinner' | 'x'

const MessageActionIcon: React.FC<{ name: MessageActionIconName }> = ({ name }) => {
  const commonProps = {
    viewBox: '0 0 24 24',
    fill: 'none',
    'aria-hidden': true,
  }

  if (name === 'copy') {
    return (
      <svg {...commonProps}>
        <path d="M8.5 8H6.75C5.78 8 5 8.78 5 9.75V18.25C5 19.22 5.78 20 6.75 20H15.25C16.22 20 17 19.22 17 18.25V16.5" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M10.75 4H17.25C18.22 4 19 4.78 19 5.75V12.25C19 13.22 18.22 14 17.25 14H10.75C9.78 14 9 13.22 9 12.25V5.75C9 4.78 9.78 4 10.75 4Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
      </svg>
    )
  }

  if (name === 'check') {
    return (
      <svg {...commonProps}>
        <path d="M5 12.5L9.25 16.75L19 7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    )
  }

  if (name === 'edit') {
    return (
      <svg {...commonProps}>
        <path d="M5 19H9.25L18.5 9.75C19.33 8.92 19.33 7.58 18.5 6.75L17.25 5.5C16.42 4.67 15.08 4.67 14.25 5.5L5 14.75V19Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M13 6.75L17.25 11" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    )
  }

  if (name === 'refresh' || name === 'spinner') {
    return (
      <svg {...commonProps} className={name === 'spinner' ? 'message-action-spin' : undefined}>
        <path d="M19 12A7 7 0 0 1 7.1 17" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        <path d="M5 12A7 7 0 0 1 16.9 7" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        <path d="M16.75 3.75V7.25H20.25" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M7.25 20.25V16.75H3.75" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    )
  }

  if (name === 'alert') {
    return (
      <svg {...commonProps}>
        <path d="M12 4.25L21 19.75H3L12 4.25Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M12 9.5V13.25M12 16.5H12.01" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      </svg>
    )
  }

  return (
    <svg {...commonProps}>
      <path d="M6.5 6.5L17.5 17.5M17.5 6.5L6.5 17.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  )
}

const MessageCard: React.FC<MessageCardProps> = ({
  message,
  isLoading,
  isStreamingPlaceholder,
  onCopyMessage,
  onEditMessage,
  onDeleteMessage,
  onRegenerateMessage,
  onOpenCitationSource,
  copiedMessageId,
}) => {
  const [isEditing, setIsEditing] = useState(false)
  const [editedContent, setEditedContent] = useState(message.content)
  const [isRegenerating, setIsRegenerating] = useState(false)

  const degradedMetadata =
    message.role === 'assistant' && message.metadata?.degraded
      ? message.metadata
      : null
  const hasMessageContent = message.content.trim().length > 0
  const hasRenderableContent =
    hasMessageContent || Boolean(degradedMetadata) || isStreamingPlaceholder

  if (!hasRenderableContent) {
    return null
  }

  const handleSaveEdit = async () => {
    const trimmedContent = editedContent.trim()
    if (!trimmedContent || trimmedContent === message.content) {
      setIsEditing(false)
      setEditedContent(message.content)
      return
    }

    if (onEditMessage) {
      await onEditMessage(message.id, trimmedContent)
    }
    setIsEditing(false)
  }

  const handleCancelEdit = () => {
    setIsEditing(false)
    setEditedContent(message.content)
  }

  const handleRegenerateClick = async () => {
    if (!onRegenerateMessage || isRegenerating) return
    setIsRegenerating(true)
    try {
      await onRegenerateMessage(message.id)
    } finally {
      setIsRegenerating(false)
    }
  }

  const handleDeleteClick = async () => {
    if (!onDeleteMessage) return
    const confirmed = window.confirm('确定要删除这条消息吗？')
    if (confirmed) {
      await onDeleteMessage(message.id)
    }
  }

  return (
    <div className={`message ${message.role}`}>
      {!isStreamingPlaceholder && hasMessageContent && !isEditing && (
        <div className="message-actions">
          <button
            type="button"
            className="message-action-btn"
            onClick={() => {
              void onCopyMessage(message.id, message.content)
            }}
            aria-label="复制消息"
            title={copiedMessageId === message.id ? '已复制' : '复制消息'}
          >
            <MessageActionIcon name={copiedMessageId === message.id ? 'check' : 'copy'} />
          </button>
          {message.role === 'user' && onEditMessage && (
            <button
              type="button"
              className="message-action-btn"
              onClick={() => {
                setIsEditing(true)
                setEditedContent(message.content)
              }}
            aria-label="编辑消息"
            title="编辑消息"
          >
              <MessageActionIcon name="edit" />
            </button>
          )}
          {message.role === 'assistant' && onRegenerateMessage && !isLoading && (
            <button
              type="button"
              className="message-action-btn"
              onClick={() => {
                void handleRegenerateClick()
              }}
              aria-label="重新生成"
            title="重新生成"
            disabled={isRegenerating}
          >
              <MessageActionIcon name={isRegenerating ? 'spinner' : 'refresh'} />
            </button>
          )}
          {onDeleteMessage && (
            <button
              type="button"
              className="message-action-btn message-action-delete"
              onClick={() => {
                void handleDeleteClick()
              }}
            aria-label="删除消息"
            title="删除消息"
          >
              <MessageActionIcon name="x" />
            </button>
          )}
        </div>
      )}

      {isEditing ? (
        <div className="message-edit-container">
          <textarea
            className="message-edit-textarea"
            value={editedContent}
            onChange={(e) => setEditedContent(e.target.value)}
            rows={5}
            autoFocus
          />
          <div className="message-edit-actions">
            <button
              type="button"
              className="message-edit-btn message-edit-save"
              onClick={() => {
                void handleSaveEdit()
              }}
            >
              保存
            </button>
            <button
              type="button"
              className="message-edit-btn message-edit-cancel"
              onClick={handleCancelEdit}
            >
              取消
            </button>
          </div>
        </div>
      ) : (
        <div
          className={`message-content ${
            isStreamingPlaceholder ? 'message-content-thinking' : ''
          } ${message.role === 'assistant' ? 'message-content-markdown' : ''}`}
        >
          {degradedMetadata && (
            <div className="message-degraded-banner" role="status" aria-live="polite">
              <div className="message-degraded-title">
                <span className="message-degraded-title-icon">
                  <MessageActionIcon name="alert" />
                </span>
                <span>当前回答为降级回复，模型或检索链路出现异常</span>
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
      )}

      {message.role === 'assistant' && message.metadata?.sources && !isEditing && (
        <MessageCitations
          sources={message.metadata.sources}
          onOpenCitationSource={onOpenCitationSource}
        />
      )}

      {!isEditing && <div className="message-time">{formatTime(message.timestamp)}</div>}
    </div>
  )
}

export default MessageCard
