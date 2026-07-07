import React, { useState, useMemo, useRef } from 'react'
import type { Conversation } from '../../App'
import { useModalFocusTrap } from '../../hooks/useModalFocusTrap'

interface ConversationExportDialogProps {
  conversation: Conversation
  isOpen: boolean
  onClose: () => void
  onExport: (conversationId: string, format: 'markdown') => Promise<string>
}

const formatTimestamp = (timestamp: string) => {
  return new Date(timestamp).toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

const generateMarkdown = (conversation: Conversation): string => {
  const lines: string[] = []

  lines.push(`# ${conversation.title}`)
  lines.push('')
  lines.push(`**创建时间**: ${formatTimestamp(conversation.createdAt)}`)
  lines.push(`**更新时间**: ${formatTimestamp(conversation.updatedAt)}`)
  lines.push(`**消息数量**: ${conversation.messages.length}`)
  lines.push('')
  lines.push('---')
  lines.push('')

  for (const message of conversation.messages) {
    const role = message.role === 'user' ? '👤 用户' : '🤖 助手'
    lines.push(`## ${role}`)
    lines.push('')
    lines.push(`*${formatTimestamp(message.timestamp)}*`)
    lines.push('')
    lines.push(message.content)
    lines.push('')

    if (message.metadata?.sources && message.metadata.sources.length > 0) {
      lines.push('### 引用来源')
      lines.push('')
      message.metadata.sources.forEach((source, index) => {
        const sourceName = source.documentName || source.toolName || '未知来源'
        lines.push(`${index + 1}. **${sourceName}**`)
        if (source.snippet) {
          lines.push(`   > ${source.snippet}`)
        }
        lines.push('')
      })
    }

    lines.push('---')
    lines.push('')
  }

  return lines.join('\n')
}

const ConversationExportDialog: React.FC<ConversationExportDialogProps> = ({
  conversation,
  isOpen,
  onClose,
  onExport,
}) => {
  const [format, setFormat] = useState<'markdown'>('markdown')
  const [isExporting, setIsExporting] = useState(false)
  const [showPreview, setShowPreview] = useState(false)
  const backdropRef = useRef<HTMLDivElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)

  useModalFocusTrap(backdropRef, {
    enabled: isOpen,
    initialFocusRef: closeButtonRef,
    onClose,
  })

  const previewContent = useMemo(() => {
    return generateMarkdown(conversation)
  }, [conversation])

  if (!isOpen) return null

  const handleExport = async () => {
    setIsExporting(true)
    try {
      const content = await onExport(conversation.id, format)

      const blob = new Blob([content], { type: 'text/markdown;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `${conversation.title.replace(/[^\w一-龥-]/g, '_')}_${Date.now()}.md`
      document.body.appendChild(link)
      link.click()
      document.body.removeChild(link)
      URL.revokeObjectURL(url)

      onClose()
    } catch (error) {
      const message = error instanceof Error ? error.message : '导出失败'
      window.alert(`导出失败：${message}`)
    } finally {
      setIsExporting(false)
    }
  }

  return (
    <div className="export-dialog-overlay" onClick={onClose} ref={backdropRef}>
      <div
        aria-labelledby="export-dialog-title"
        aria-modal="true"
        className="export-dialog"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
      >
        <div className="export-dialog-header">
          <h3 id="export-dialog-title">导出对话</h3>
          <button
            type="button"
            className="export-dialog-close"
            onClick={onClose}
            aria-label="关闭"
            ref={closeButtonRef}
          >
            ✕
          </button>
        </div>

        <div className="export-dialog-body">
          <div className="export-info">
            <div className="export-info-item">
              <label>对话标题</label>
              <div>{conversation.title}</div>
            </div>
            <div className="export-info-item">
              <label>消息数量</label>
              <div>{conversation.messages.length} 条</div>
            </div>
            <div className="export-info-item">
              <label>创建时间</label>
              <div>{formatTimestamp(conversation.createdAt)}</div>
            </div>
          </div>

          <div className="export-format-section">
            <label>导出格式</label>
            <div className="export-format-options">
              <label className="export-format-option">
                <input
                  type="radio"
                  name="export-format"
                  value="markdown"
                  checked={format === 'markdown'}
                  onChange={(e) => setFormat(e.target.value as 'markdown')}
                />
                <span>Markdown (.md)</span>
              </label>
            </div>
          </div>

          <div className="export-preview-section">
            <button
              type="button"
              className="export-preview-toggle"
              onClick={() => setShowPreview(!showPreview)}
            >
              {showPreview ? '隐藏预览' : '显示预览'}
            </button>
            {showPreview && (
              <div className="export-preview-content">
                <pre>{previewContent}</pre>
              </div>
            )}
          </div>
        </div>

        <div className="export-dialog-footer">
          <button
            type="button"
            className="export-btn export-btn-primary"
            onClick={() => {
              void handleExport()
            }}
            disabled={isExporting}
          >
            {isExporting ? '导出中...' : '导出'}
          </button>
          <button
            type="button"
            className="export-btn export-btn-secondary"
            onClick={onClose}
            disabled={isExporting}
          >
            取消
          </button>
        </div>
      </div>
    </div>
  )
}

export default ConversationExportDialog
