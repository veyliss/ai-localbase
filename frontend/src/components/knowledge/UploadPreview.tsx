import React from 'react'
import KnowledgeIcon from './KnowledgeIcon'

interface UploadPreviewProps {
  files: File[]
  onRemoveFile: (index: number) => void
}

const formatFileSize = (bytes: number): string => {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

const UploadPreview: React.FC<UploadPreviewProps> = ({ files, onRemoveFile }) => {
  const totalSize = files.reduce((sum, file) => sum + file.size, 0)

  return (
    <div className="upload-preview">
      <div className="upload-preview-header">
        <span className="upload-preview-count">{files.length} 个文件</span>
        <span className="upload-preview-size">总大小: {formatFileSize(totalSize)}</span>
      </div>
      <div className="upload-preview-list">
        {files.map((file, index) => (
          <div key={`${file.name}-${index}`} className="upload-preview-item">
            <span className="upload-preview-icon">
              <KnowledgeIcon name="file" />
            </span>
            <div className="upload-preview-info">
              <span className="upload-preview-name">{file.name}</span>
              <span className="upload-preview-meta">
                {formatFileSize(file.size)} · {file.type || '未知类型'}
              </span>
            </div>
            <button
              className="upload-preview-remove"
              onClick={() => onRemoveFile(index)}
              title="移除文件"
            >
              <KnowledgeIcon name="x" />
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}

export default UploadPreview
