import React, { useState } from 'react'
import type { MCPConfig } from '../../../App'

interface MCPSettingsProps {
  config: MCPConfig
  onCopyMcpToken: () => Promise<void>
  onResetMcpToken: () => Promise<void>
}

const MCPSettings: React.FC<MCPSettingsProps> = ({ config, onCopyMcpToken, onResetMcpToken }) => {
  const [mcpFeedback, setMcpFeedback] = useState('')
  const [isMcpTokenVisible, setIsMcpTokenVisible] = useState(false)

  const handleCopyToken = async () => {
    try {
      await onCopyMcpToken()
      setMcpFeedback('Token 已复制')
    } catch {
      setMcpFeedback('复制失败')
    }
  }

  const handleResetToken = async () => {
    try {
      await onResetMcpToken()
      setMcpFeedback('Token 已重置')
    } catch {
      setMcpFeedback('重置失败')
    }
  }

  return (
    <div className="settings-tab-content">
      <section className="settings-card">
        <div className="settings-card-header">
          <h3>MCP 配置</h3>
          <p>管理外部工具调用入口和访问 Token</p>
        </div>
        <div className="settings-card-body">
          <div className="settings-form-grid">
            <div className="settings-form-group">
              <label className="settings-form-label">状态</label>
              <input value={config.enabled ? '已启用' : '未启用'} readOnly className="settings-input-readonly" />
            </div>
            <div className="settings-form-group">
              <label className="settings-form-label">Base Path</label>
              <input value={config.basePath} readOnly className="settings-input-readonly" />
            </div>
            <div className="settings-form-group settings-form-group-full">
              <label className="settings-form-label">Token</label>
              <div className="settings-token-wrapper">
                <input
                  type={isMcpTokenVisible ? 'text' : 'password'}
                  value={config.token}
                  readOnly
                  className="settings-token-input"
                />
                <div className="settings-token-actions">
                  <button
                    className="settings-action-btn"
                    onClick={() => setIsMcpTokenVisible((v) => !v)}
                    title={isMcpTokenVisible ? '隐藏 Token' : '显示 Token'}
                  >
                    {isMcpTokenVisible ? '隐藏' : '显示'}
                  </button>
                  <button className="settings-action-btn" onClick={() => void handleCopyToken()}>
                    复制
                  </button>
                  <button className="settings-action-btn" onClick={() => void handleResetToken()}>
                    重置
                  </button>
                </div>
              </div>
              <small>用于访问 MCP 接口的 Bearer Token。重置后旧 Token 会立刻失效。</small>
              {mcpFeedback && <small className="settings-feedback">{mcpFeedback}</small>}
            </div>
          </div>
        </div>
      </section>
    </div>
  )
}

export default MCPSettings
