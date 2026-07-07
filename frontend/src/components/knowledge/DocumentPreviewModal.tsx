import React from 'react'

interface DocumentPreviewModalProps {
  documentName: string
  documentSize: string
  uploadedAt: string
  contentPreview?: string
  chunkCount?: number
  vectorCount?: number
  healthStatus: 'healthy' | 'warning' | 'attention'
  healthScore?: number
  indexedAt?: string
  onClose: () => void
}

const DocumentPreviewModal: React.FC<DocumentPreviewModalProps> = ({
  documentName,
  documentSize,
  uploadedAt,
  contentPreview,
  chunkCount,
  vectorCount,
  healthStatus,
  healthScore,
  indexedAt,
  onClose,
}) => {
  const formatDate = (dateString: string) => {
    return new Date(dateString).toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  }

  const getHealthBadgeStyle = () => {
    switch (healthStatus) {
      case 'healthy':
        return { background: 'var(--color-success-light)', color: 'var(--color-success)' }
      case 'warning':
        return { background: 'var(--color-warning-light)', color: 'var(--color-warning)' }
      case 'attention':
        return { background: 'var(--color-error-light)', color: 'var(--color-error)' }
      default:
        return { background: 'var(--surface-muted)', color: 'var(--text-secondary)' }
    }
  }

  const getHealthLabel = () => {
    switch (healthStatus) {
      case 'healthy':
        return '健康'
      case 'warning':
        return '警告'
      case 'attention':
        return '需处理'
      default:
        return '未知'
    }
  }

  const badgeStyle = getHealthBadgeStyle()
  const truncatedPreview = contentPreview
    ? contentPreview.length > 500
      ? `${contentPreview.substring(0, 500)}...`
      : contentPreview
    : '暂无内容预览'

  return (
    <div className="settings-modal-backdrop" onClick={onClose}>
      <div
        className="settings-modal settings-modal-single"
        onClick={(event) => event.stopPropagation()}
        style={{ maxHeight: 'min(88vh, 800px)' }}
      >
        <div className="settings-modal-header">
          <div>
            <h3>文档预览</h3>
            <p>查看文档基本信息、内容预览和索引统计</p>
          </div>
          <button type="button" className="ghost-btn settings-close-btn" onClick={onClose}>
            关闭
          </button>
        </div>

        <div className="settings-modal-scroll">
          <section className="settings-panel-block single-column">
            <div className="settings-section-head" style={{ marginBottom: '16px' }}>
              <div>
                <h3 style={{ margin: '0 0 6px', fontSize: '16px', color: 'var(--text-primary)' }}>基本信息</h3>
                <p style={{ margin: 0, fontSize: '13px', color: 'var(--text-secondary)' }}>
                  文档名称、大小和上传时间
                </p>
              </div>
            </div>

            <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                <span style={{ fontSize: '13px', color: 'var(--text-secondary)', minWidth: '80px' }}>
                  文件名
                </span>
                <strong
                  style={{
                    fontSize: '14px',
                    color: 'var(--text-primary)',
                    wordBreak: 'break-word',
                    flex: 1,
                  }}
                >
                  {documentName}
                </strong>
              </div>

              <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                <span style={{ fontSize: '13px', color: 'var(--text-secondary)', minWidth: '80px' }}>大小</span>
                <strong style={{ fontSize: '14px', color: 'var(--text-primary)' }}>{documentSize}</strong>
              </div>

              <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                <span style={{ fontSize: '13px', color: 'var(--text-secondary)', minWidth: '80px' }}>
                  上传时间
                </span>
                <strong style={{ fontSize: '14px', color: 'var(--text-primary)' }}>
                  {formatDate(uploadedAt)}
                </strong>
              </div>

              {indexedAt && (
                <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                  <span style={{ fontSize: '13px', color: 'var(--text-secondary)', minWidth: '80px' }}>
                    索引时间
                  </span>
                  <strong style={{ fontSize: '14px', color: 'var(--text-primary)' }}>
                    {formatDate(indexedAt)}
                  </strong>
                </div>
              )}
            </div>
          </section>

          <section className="settings-panel-block single-column">
            <div className="settings-section-head" style={{ marginBottom: '16px' }}>
              <div>
                <h3 style={{ margin: '0 0 6px', fontSize: '16px', color: 'var(--text-primary)' }}>索引统计</h3>
                <p style={{ margin: 0, fontSize: '13px', color: 'var(--text-secondary)' }}>
                  文档分块数量、向量数量和健康度
                </p>
              </div>
            </div>

            <div
              style={{
                display: 'grid',
                gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
                gap: '12px',
              }}
            >
              <div
                style={{
                  padding: '12px 14px',
                  border: '1px solid var(--border-color)',
                  borderRadius: '10px',
                  background: 'var(--surface-secondary)',
                }}
              >
                <span style={{ display: 'block', fontSize: '12px', color: 'var(--text-secondary)' }}>
                  分块数量
                </span>
                <strong
                  style={{
                    display: 'block',
                    marginTop: '6px',
                    fontSize: '20px',
                    color: 'var(--text-primary)',
                  }}
                >
                  {chunkCount ?? 0}
                </strong>
              </div>

              <div
                style={{
                  padding: '12px 14px',
                  border: '1px solid var(--border-color)',
                  borderRadius: '10px',
                  background: 'var(--surface-secondary)',
                }}
              >
                <span style={{ display: 'block', fontSize: '12px', color: 'var(--text-secondary)' }}>
                  向量数量
                </span>
                <strong
                  style={{
                    display: 'block',
                    marginTop: '6px',
                    fontSize: '20px',
                    color: 'var(--text-primary)',
                  }}
                >
                  {vectorCount ?? 0}
                </strong>
              </div>
            </div>

            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                gap: '12px',
                marginTop: '12px',
                padding: '12px 14px',
                border: '1px solid var(--border-color)',
                borderRadius: '10px',
                background: 'var(--surface-primary)',
              }}
            >
              <div>
                <span style={{ display: 'block', fontSize: '12px', color: 'var(--text-secondary)' }}>
                  健康度状态
                </span>
                <strong
                  style={{
                    display: 'block',
                    marginTop: '4px',
                    fontSize: '14px',
                    color: 'var(--text-primary)',
                  }}
                >
                  {getHealthLabel()}
                  {healthScore !== undefined && (
                    <span style={{ marginLeft: '8px', fontSize: '13px', color: 'var(--text-secondary)' }}>
                      {healthScore.toFixed(1)} 分
                    </span>
                  )}
                </strong>
              </div>
              <div
                style={{
                  padding: '6px 12px',
                  borderRadius: '999px',
                  fontSize: '12px',
                  fontWeight: 700,
                  ...badgeStyle,
                }}
              >
                {getHealthLabel()}
              </div>
            </div>
          </section>

          <section className="settings-panel-block single-column">
            <div className="settings-section-head" style={{ marginBottom: '16px' }}>
              <div>
                <h3 style={{ margin: '0 0 6px', fontSize: '16px', color: 'var(--text-primary)' }}>内容预览</h3>
                <p style={{ margin: 0, fontSize: '13px', color: 'var(--text-secondary)' }}>
                  文档内容前 500 字符
                </p>
              </div>
            </div>

            <pre
              style={{
                margin: 0,
                padding: '14px',
                maxHeight: '300px',
                overflow: 'auto',
                border: '1px solid var(--border-color)',
                borderRadius: '10px',
                background: 'var(--surface-secondary)',
                color: 'var(--text-secondary)',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
                fontSize: '13px',
                lineHeight: 1.6,
              }}
            >
              {truncatedPreview}
            </pre>
          </section>
        </div>
      </div>
    </div>
  )
}

export default DocumentPreviewModal
