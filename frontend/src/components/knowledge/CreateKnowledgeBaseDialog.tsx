import React, { useEffect } from 'react'

interface CreateKnowledgeBaseDialogProps {
  name: string
  description: string
  onNameChange: (value: string) => void
  onDescriptionChange: (value: string) => void
  onCancel: () => void
  onConfirm: () => void
}

const CreateKnowledgeBaseDialog: React.FC<CreateKnowledgeBaseDialogProps> = ({
  name,
  description,
  onNameChange,
  onDescriptionChange,
  onCancel,
  onConfirm,
}) => {
  // ESC 键关闭
  useEffect(() => {
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onCancel()
      }
    }

    document.addEventListener('keydown', handleEscape)
    return () => {
      document.removeEventListener('keydown', handleEscape)
    }
  }, [onCancel])

  return (
    <div className="kb-create-backdrop" onClick={onCancel}>
      <div
        className="kb-create-dialog"
        onClick={(event) => event.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby="create-kb-title"
      >
        <div className="kb-create-dialog-header">
          <h3 id="create-kb-title">新建知识库</h3>
          <button
            className="kb-close-btn"
            onClick={onCancel}
            aria-label="关闭对话框"
            type="button"
          >
            ×
          </button>
        </div>
        <div className="kb-create-dialog-body">
          <div className="kb-form-field">
            <label className="kb-form-label" htmlFor="kb-name-input">
              知识库名称 <span className="kb-required">*</span>
            </label>
            <input
              id="kb-name-input"
              className="kb-form-input"
              type="text"
              placeholder="例如：产品文档、技术手册"
              value={name}
              onChange={(event) => onNameChange(event.target.value)}
              onKeyDown={(event) => event.key === 'Enter' && name.trim() && onConfirm()}
              autoFocus
              maxLength={50}
              aria-required="true"
            />
          </div>
          <div className="kb-form-field">
            <label className="kb-form-label" htmlFor="kb-desc-input">
              描述（可选）
            </label>
            <textarea
              id="kb-desc-input"
              className="kb-form-textarea"
              placeholder="简要描述该知识库的用途"
              value={description}
              onChange={(event) => onDescriptionChange(event.target.value)}
              rows={3}
              maxLength={200}
            />
          </div>
        </div>
        <div className="kb-create-dialog-footer">
          <button className="kb-cancel-btn" onClick={onCancel} type="button">
            取消
          </button>
          <button
            className="kb-confirm-btn"
            onClick={onConfirm}
            disabled={!name.trim()}
            type="button"
          >
            创建知识库
          </button>
        </div>
      </div>
    </div>
  )
}

export default CreateKnowledgeBaseDialog
