import React from 'react'
import type { DirectoryUploadTask } from '../../App'

interface DirectoryUploadTaskPanelProps {
  task: DirectoryUploadTask
  progressPercent: number
  showDetails: boolean
  showFailedItems: boolean
  showSkippedItems: boolean
  canCancelUpload: boolean
  canContinueUpload: boolean
  onToggleDetails: () => void
  onToggleFailedItems: () => void
  onToggleSkippedItems: () => void
  onCancel: () => void
  onContinue: () => void
}

const taskStatusText = (status: DirectoryUploadTask['status']) => {
  if (status === 'scanning') return '扫描中'
  if (status === 'uploading') return '上传中'
  if (status === 'indexing') return '索引中'
  if (status === 'polling-index') return '确认索引'
  if (status === 'canceling') return '取消中'
  if (status === 'canceled') return '已取消'
  if (status === 'done') return '已完成'
  if (status === 'partial-failed') return '部分完成'
  if (status === 'failed') return '失败'
  return '待开始'
}

const DirectoryUploadTaskPanel: React.FC<DirectoryUploadTaskPanelProps> = ({
  task,
  progressPercent,
  showDetails,
  showFailedItems,
  showSkippedItems,
  canCancelUpload,
  canContinueUpload,
  onToggleDetails,
  onToggleFailedItems,
  onToggleSkippedItems,
  onCancel,
  onContinue,
}) => (
  <div className="kb-upload-task-shell">
    <div className="kb-upload-task-compact">
      <div className="kb-upload-task-compact-main">
        <span className={`kb-upload-task-pill kb-upload-task-pill--${task.status}`}>
          {taskStatusText(task.status)}
        </span>
        <div className="kb-upload-task-compact-text">
          <div className="kb-upload-task-compact-title">文档导入任务</div>
          <div className="kb-upload-task-compact-summary">
            {task.processedFiles}/{task.eligibleFiles} · 成功 {task.successFiles} · 失败 {task.failedFiles} · 跳过 {task.skippedFiles}
          </div>
        </div>
      </div>
      <div className="kb-upload-task-actions">
        <button className="kb-upload-task-btn kb-upload-task-btn--ghost" onClick={onToggleDetails}>
          {showDetails ? '收起' : '详情'}
        </button>
        {canContinueUpload && (
          <button className="kb-upload-task-btn" onClick={onContinue}>继续导入</button>
        )}
        {canCancelUpload && (
          <button
            className="kb-upload-task-btn kb-upload-task-btn--danger"
            onClick={onCancel}
            disabled={task.status === 'canceling'}
          >
            {task.status === 'canceling' ? '取消中' : '取消导入'}
          </button>
        )}
      </div>
    </div>

    {showDetails && (
      <div className="kb-upload-task">
        <div className="kb-upload-progress-meta">
          <span>已处理 {task.processedFiles} / {task.eligibleFiles}</span>
          <span>{progressPercent}%</span>
        </div>
        <div className="kb-upload-progress-track">
          <div className="kb-upload-progress-fill" style={{ width: `${progressPercent}%` }} />
        </div>

        <div className="kb-upload-stats-grid">
          <div className="kb-upload-stat-card"><span className="kb-upload-stat-label">总文件</span><strong>{task.totalFiles}</strong></div>
          <div className="kb-upload-stat-card"><span className="kb-upload-stat-label">可上传</span><strong>{task.eligibleFiles}</strong></div>
          <div className="kb-upload-stat-card"><span className="kb-upload-stat-label">成功</span><strong>{task.successFiles}</strong></div>
          <div className="kb-upload-stat-card"><span className="kb-upload-stat-label">失败</span><strong>{task.failedFiles}</strong></div>
          <div className="kb-upload-stat-card"><span className="kb-upload-stat-label">跳过</span><strong>{task.skippedFiles}</strong></div>
          <div className="kb-upload-stat-card"><span className="kb-upload-stat-label">未执行</span><strong>{task.pendingFiles}</strong></div>
        </div>

        {task.currentFilePath && (
          <div className="kb-upload-current-file">当前处理：{task.currentFilePath}</div>
        )}
        {task.summaryMessage && (
          <div className="kb-upload-summary">{task.summaryMessage}</div>
        )}

        {task.failedItems.length > 0 && (
          <div className="kb-upload-issues-toggle-row">
            <button className="kb-upload-task-btn kb-upload-task-btn--ghost" onClick={onToggleFailedItems}>
              {showFailedItems ? '隐藏失败文件' : `查看失败文件（${task.failedItems.length}）`}
            </button>
          </div>
        )}
        {showFailedItems && task.failedItems.length > 0 && (
          <div className="kb-upload-issues">
            <div className="kb-upload-issues-title">失败文件</div>
            {task.failedItems.map((item) => (
              <div key={`${item.path}-${item.reason}`} className="kb-upload-issue-item">
                <span className="kb-upload-issue-path">{item.path}</span>
                <span className="kb-upload-issue-reason">{item.reason}</span>
              </div>
            ))}
          </div>
        )}

        {task.skippedItems.length > 0 && (
          <div className="kb-upload-issues-toggle-row">
            <button className="kb-upload-task-btn kb-upload-task-btn--ghost" onClick={onToggleSkippedItems}>
              {showSkippedItems ? '隐藏已跳过文件' : `查看已跳过文件（${task.skippedItems.length}）`}
            </button>
          </div>
        )}
        {showSkippedItems && task.skippedItems.length > 0 && (
          <div className="kb-upload-issues kb-upload-issues--muted">
            <div className="kb-upload-issues-title">已跳过文件</div>
            {task.skippedItems.map((item) => (
              <div key={`${item.path}-${item.reason}`} className="kb-upload-issue-item">
                <span className="kb-upload-issue-path">{item.path}</span>
                <span className="kb-upload-issue-reason">{item.reason}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    )}
  </div>
)

export default DirectoryUploadTaskPanel
