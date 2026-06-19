import React, { useState } from 'react'

interface SystemSettingsProps {
  onLogout: () => void
}

const SystemSettings: React.FC<SystemSettingsProps> = ({ onLogout }) => {
  const [showLogoutConfirm, setShowLogoutConfirm] = useState(false)

  const handleLogout = () => {
    setShowLogoutConfirm(false)
    onLogout()
  }

  return (
    <>
      <div className="settings-tab-content">
        <section className="settings-card settings-card-danger">
          <div className="settings-card-header">
            <h3>会话管理</h3>
            <p>安全相关操作，需谨慎执行</p>
          </div>
          <div className="settings-card-body">
            <div className="settings-danger-actions">
              <button className="btn-danger settings-logout-btn-full" onClick={() => setShowLogoutConfirm(true)}>
                <svg viewBox="0 0 24 24" fill="none" width="18" height="18">
                  <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
                  <polyline points="16 17 21 12 16 7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
                  <line x1="21" y1="12" x2="9" y2="12" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
                </svg>
                退出登录
              </button>
              <p className="settings-danger-hint">退出后需要重新输入密码才能继续使用。</p>
            </div>
          </div>
        </section>
      </div>

      {/* 退出确认对话框 */}
      {showLogoutConfirm && (
        <div className="logout-confirm-overlay" onClick={() => setShowLogoutConfirm(false)}>
          <div className="logout-confirm-dialog" onClick={(e) => e.stopPropagation()}>
            <div className="logout-confirm-icon">
              <svg viewBox="0 0 24 24" fill="none">
                <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="2"/>
                <path d="M12 9V13" stroke="currentColor" strokeWidth="2" strokeLinecap="round"/>
                <circle cx="12" cy="16" r="0.5" fill="currentColor"/>
              </svg>
            </div>
            <h4>确认退出登录</h4>
            <p>退出后需要重新输入密码才能继续使用。</p>
            <div className="logout-confirm-actions">
              <button className="btn-secondary" onClick={() => setShowLogoutConfirm(false)}>
                取消
              </button>
              <button className="btn-danger" onClick={handleLogout}>
                确认退出
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}

export default SystemSettings
