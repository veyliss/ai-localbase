import React, { useCallback, useEffect, useMemo, useState } from 'react'
import type { AppConfig } from '../../../App'
import { useAuth } from '../../../contexts/AuthContext'
import {
  createAuthAPIKey,
  fetchAuthAPIKeys,
  fetchAuthSessions,
  fetchSecurityEvents,
  revokeAuthAPIKey,
  type AuthAPIKeyInfo,
  type AuthSessionInfo,
  type SecurityEventInfo,
} from '../../../services/api'

interface SystemSettingsProps {
  config: AppConfig
  onLogout: () => void | Promise<void>
}

const formatDateTime = (value?: string | number | null) => {
  if (!value) return '未知'
  const date = typeof value === 'number' ? new Date(value * 1000) : new Date(value)
  if (Number.isNaN(date.getTime())) return '未知'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}

const formatTimeRemaining = (value?: string | number | null) => {
  if (!value) return '未知'
  const date = typeof value === 'number' ? new Date(value * 1000) : new Date(value)
  const diffMs = date.getTime() - Date.now()
  if (!Number.isFinite(diffMs) || diffMs <= 0) return '已到期'
  const days = Math.floor(diffMs / 86400000)
  if (days >= 1) return `约 ${days} 天后`
  const hours = Math.floor(diffMs / 3600000)
  if (hours >= 1) return `约 ${hours} 小时后`
  const minutes = Math.max(1, Math.floor(diffMs / 60000))
  return `约 ${minutes} 分钟后`
}

const getPasswordStrength = (password: string) => {
  if (!password) return { label: '等待输入', tone: 'idle', hint: '建议使用 16 位以上密码' }
  let score = 0
  if (password.length >= 8) score += 1
  if (password.length >= 16) score += 1
  if (/[a-z]/.test(password) && /[A-Z]/.test(password)) score += 1
  if (/\d/.test(password)) score += 1
  if (/[^a-zA-Z0-9]/.test(password)) score += 1
  if (score >= 4) return { label: '强', tone: 'strong', hint: '适合服务器部署' }
  if (score >= 2) return { label: '可用', tone: 'medium', hint: '生产环境建议再增强' }
  return { label: '偏弱', tone: 'weak', hint: '至少 8 位，推荐 16 位以上' }
}

const eventLabelMap: Record<string, string> = {
  root_bootstrapped_from_env: '环境变量初始化',
  root_setup_completed: '首次初始化',
  login_succeeded: '登录成功',
  login_failed: '登录失败',
  logout: '退出登录',
  logout_all: '退出所有设备',
  password_changed: '修改密码',
  password_change_failed: '改密失败',
  api_key_created: '创建 API Key',
  api_key_revoked: '撤销 API Key',
  mcp_call_succeeded: 'MCP 调用成功',
  mcp_call_failed: 'MCP 调用失败',
  mcp_danger_succeeded: 'MCP 危险成功',
  mcp_danger_failed: 'MCP 危险失败',
  root_password_reset_from_env: '环境变量重置密码',
  weak_env_password: '弱密码提醒',
  weak_env_reset_password: '弱重置密码提醒',
}

const apiKeyScopeOptions = [
  {
    value: 'openai:chat',
    label: '聊天接口',
    description: '/v1/chat/completions',
  },
  {
    value: 'knowledge:read',
    label: '读取知识库',
    description: '预留给知识库读取 API',
  },
  {
    value: 'knowledge:write',
    label: '写入知识库',
    description: '预留给知识库变更 API',
  },
  {
    value: 'config:read',
    label: '读取配置',
    description: '预留给配置读取 API',
  },
  {
    value: 'mcp:read',
    label: 'MCP 读取',
    description: '工具发现、检索、列表和只读查询',
  },
  {
    value: 'mcp:write',
    label: 'MCP 写入',
    description: '创建知识库、保存会话和重建索引',
  },
  {
    value: 'mcp:upload',
    label: 'MCP 上传',
    description: '上传文档和注册暂存文件',
  },
  {
    value: 'mcp:eval',
    label: 'MCP 评估',
    description: '生成评估数据集',
  },
  {
    value: 'mcp:danger',
    label: 'MCP 危险',
    description: '删除知识库、文档或会话',
  },
  {
    value: 'mcp:admin',
    label: 'MCP 管理',
    description: '允许调用全部 MCP 工具',
  },
]

const defaultAPIKeyScopes = ['openai:chat']

const formatAPIKeyScopes = (scopes: string[] = []) => {
  if (scopes.length === 0) return '未设置权限'
  return scopes.map((scope) => (
    apiKeyScopeOptions.find((option) => option.value === scope)?.label || scope
  )).join(' / ')
}

const isMCPEvent = (event: SecurityEventInfo) => event.type.startsWith('mcp_')
const isMCPFailureEvent = (event: SecurityEventInfo) => event.type.includes('_failed')
const isMCPDangerEvent = (event: SecurityEventInfo) => event.type.includes('_danger_')

const SystemSettings: React.FC<SystemSettingsProps> = ({ config, onLogout }) => {
  const { username, expiresAt, logoutAll, changePassword } = useAuth()
  const [sessions, setSessions] = useState<AuthSessionInfo[]>([])
  const [apiKeys, setApiKeys] = useState<AuthAPIKeyInfo[]>([])
  const [events, setEvents] = useState<SecurityEventInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [feedback, setFeedback] = useState('')
  const [error, setError] = useState('')
  const [showLogoutConfirm, setShowLogoutConfirm] = useState(false)
  const [showLogoutAllConfirm, setShowLogoutAllConfirm] = useState(false)
  const [showPasswordForm, setShowPasswordForm] = useState(false)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [keyName, setKeyName] = useState('')
  const [selectedScopes, setSelectedScopes] = useState<string[]>(defaultAPIKeyScopes)
  const [createdToken, setCreatedToken] = useState('')
  const [createdTokenSaved, setCreatedTokenSaved] = useState(false)
  const [busyAction, setBusyAction] = useState('')

  const activeSessions = useMemo(
    () => sessions.filter((session) => !session.revokedAt),
    [sessions],
  )
  const currentSessions = useMemo(
    () => activeSessions.filter((session) => session.current),
    [activeSessions],
  )
  const otherSessions = useMemo(
    () => activeSessions.filter((session) => !session.current),
    [activeSessions],
  )
  const revokedSessions = useMemo(
    () => sessions.filter((session) => session.revokedAt).slice(0, 4),
    [sessions],
  )
  const mcpEvents = useMemo(
    () => events.filter(isMCPEvent),
    [events],
  )
  const mcpFailureEvents = useMemo(
    () => mcpEvents.filter(isMCPFailureEvent),
    [mcpEvents],
  )
  const mcpDangerEvents = useMemo(
    () => mcpEvents.filter(isMCPDangerEvent),
    [mcpEvents],
  )
  const passwordStrength = useMemo(() => getPasswordStrength(newPassword), [newPassword])
  const deploymentWarnings = config.mcp.deploymentWarnings ?? []

  const loadSecurityData = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [nextSessions, nextAPIKeys, nextEvents] = await Promise.all([
        fetchAuthSessions(),
        fetchAuthAPIKeys(),
        fetchSecurityEvents(50),
      ])
      setSessions(nextSessions)
      setApiKeys(nextAPIKeys)
      setEvents(nextEvents)
    } catch (err) {
      setError(err instanceof Error ? err.message : '安全设置加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadSecurityData()
  }, [loadSecurityData])

  const handleLogout = async () => {
    setShowLogoutConfirm(false)
    await onLogout()
  }

  const handleLogoutAll = async () => {
    setBusyAction('logout-all')
    setShowLogoutAllConfirm(false)
    try {
      await logoutAll()
    } finally {
      setBusyAction('')
    }
  }

  const handleChangePassword = async (event: React.FormEvent) => {
    event.preventDefault()
    setFeedback('')
    setError('')
    if (newPassword.length < 8) {
      setError('新密码至少需要 8 个字符')
      return
    }
    if (newPassword !== confirmPassword) {
      setError('两次输入的新密码不一致')
      return
    }

    setBusyAction('change-password')
    try {
      await changePassword(currentPassword, newPassword)
      setFeedback('密码已更新，请重新登录')
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
      setShowPasswordForm(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : '修改密码失败')
    } finally {
      setBusyAction('')
    }
  }

  const handleCreateAPIKey = async (event: React.FormEvent) => {
    event.preventDefault()
    setFeedback('')
    setError('')
    if (createdToken && !createdTokenSaved) {
      setError('请先确认已经保存当前 API Key')
      return
    }
    setCreatedToken('')
    setCreatedTokenSaved(false)
    if (!keyName.trim()) {
      setError('请输入 API Key 名称')
      return
    }
    if (selectedScopes.length === 0) {
      setError('至少选择一个 API Key 权限')
      return
    }

    setBusyAction('create-key')
    try {
      const created = await createAuthAPIKey({ name: keyName.trim(), scopes: selectedScopes })
      setCreatedToken(created.token)
      setCreatedTokenSaved(false)
      setKeyName('')
      setFeedback('API Key 已创建，请立即复制保存')
      await loadSecurityData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建 API Key 失败')
    } finally {
      setBusyAction('')
    }
  }

  const handleCopyCreatedToken = async () => {
    if (!createdToken) return
    try {
      await navigator.clipboard.writeText(createdToken)
      setFeedback('API Key 已复制')
    } catch {
      setError('复制失败')
    }
  }

  const handleConfirmCreatedTokenSaved = () => {
    setCreatedToken('')
    setCreatedTokenSaved(true)
    setFeedback('API Key 已确认保存')
  }

  const handleToggleScope = (scope: string) => {
    setSelectedScopes((current) => {
      if (current.includes(scope)) {
        return current.filter((item) => item !== scope)
      }
      return [...current, scope]
    })
  }

  const handleRevokeAPIKey = async (id: string) => {
    setBusyAction(id)
    setFeedback('')
    setError('')
    try {
      await revokeAuthAPIKey(id)
      setFeedback('API Key 已撤销')
      await loadSecurityData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '撤销 API Key 失败')
    } finally {
      setBusyAction('')
    }
  }

  const renderSessionRow = (session: AuthSessionInfo) => (
    <div className="settings-security-row" key={session.id}>
      <div>
        <strong>{session.current ? '当前设备' : session.revokedAt ? '历史会话' : '其它设备'}</strong>
        <span>{session.ip || '未知 IP'} · {session.userAgent || '未知客户端'}</span>
      </div>
      <div className="settings-security-row-meta">
        <span>{session.revokedAt ? `失效 ${formatDateTime(session.revokedAt)}` : `到期 ${formatDateTime(session.expiresAt)}`}</span>
        {session.current && <em>当前</em>}
      </div>
    </div>
  )

  return (
    <>
      <div className="settings-tab-content settings-security-content">
        <section className="settings-security-overview-panel" aria-label="账户概览">
          <div className="settings-security-identity">
            <div>
              <span>Root 账户</span>
              <strong>{username || 'root'}</strong>
              <p>管理网页登录、服务端会话和 OpenAI-compatible API Key。</p>
            </div>
            <span className="settings-status-pill enabled">已认证</span>
          </div>

          {deploymentWarnings.length > 0 && (
            <div className="settings-security-warning-panel" role="status" aria-live="polite">
              <div className="settings-security-warning-copy">
                <strong>部署提醒</strong>
                <span>{deploymentWarnings.join('；')}</span>
              </div>
              <div className="settings-security-warning-meta">
                <span className="settings-status-pill warning">需处理</span>
                <small>{config.mcp.recommendedAuthMode === 'api_key_scopes' ? 'MCP 建议使用 API Key Scope，危险工具使用 confirmNonce' : '请检查认证配置'}</small>
              </div>
            </div>
          )}

          <div className="settings-security-metrics">
            <div>
              <span>会话到期</span>
              <strong>{formatTimeRemaining(expiresAt)}</strong>
              <small>{formatDateTime(expiresAt)}</small>
            </div>
            <div>
              <span>活跃会话</span>
              <strong>{activeSessions.length}</strong>
              <small>含当前设备</small>
            </div>
            <div>
              <span>API Key</span>
              <strong>{apiKeys.filter((key) => !key.revokedAt).length}</strong>
              <small>未撤销</small>
            </div>
          </div>

          {(loading || feedback || error) && (
            <div className="settings-security-notices">
              {loading && <div className="settings-inline-note">正在加载安全状态...</div>}
              {feedback && <div className="settings-inline-note success">{feedback}</div>}
              {error && <div className="settings-inline-note error">{error}</div>}
            </div>
          )}
        </section>

        <section className="settings-setting-section">
          <div className="settings-setting-section-header">
            <div>
              <h3>MCP 审计</h3>
              <p>最近 MCP 工具访问、失败和危险调用记录。</p>
            </div>
          </div>
          <div className="settings-security-metrics settings-security-metrics-compact">
            <div>
              <span>最近访问</span>
              <strong>{mcpEvents.length > 0 ? formatDateTime(mcpEvents[0].createdAt) : '暂无'}</strong>
              <small>{mcpEvents.length} 条记录</small>
            </div>
            <div>
              <span>最近失败</span>
              <strong>{mcpFailureEvents.length}</strong>
              <small>{mcpFailureEvents[0] ? formatDateTime(mcpFailureEvents[0].createdAt) : '暂无'}</small>
            </div>
            <div>
              <span>危险调用</span>
              <strong>{mcpDangerEvents.length}</strong>
              <small>{mcpDangerEvents[0] ? formatDateTime(mcpDangerEvents[0].createdAt) : '暂无'}</small>
            </div>
          </div>
          <div className="settings-security-list">
            {mcpEvents.length === 0 && <div className="settings-empty-row">暂无 MCP 调用记录</div>}
            {mcpEvents.slice(0, 6).map((event, index) => (
              <div
                className="settings-security-row"
                key={`${event.id}-${event.createdAt}-${index}`}
              >
                <div>
                  <strong>{eventLabelMap[event.type] || event.type}</strong>
                  <span>{event.message || '无详情'}</span>
                </div>
                <div className="settings-security-row-meta">
                  <span>{formatDateTime(event.createdAt)}</span>
                </div>
              </div>
            ))}
          </div>
        </section>

        <section className="settings-setting-section">
          <div className="settings-setting-section-header">
            <div>
              <h3>密码</h3>
              <p>更新 root 密码后，所有已登录会话会立即失效。</p>
            </div>
          </div>
          <div className="settings-setting-list">
            <div className="settings-setting-row">
              <div className="settings-setting-row-main">
                <strong>登录密码</strong>
                <span>需要输入当前密码才能完成变更。</span>
              </div>
              <div className="settings-setting-row-action">
                <span className="settings-status-pill warning">会吊销会话</span>
                <button
                  className="settings-action-btn"
                  onClick={() => setShowPasswordForm((visible) => !visible)}
                  type="button"
                >
                  {showPasswordForm ? '收起' : '修改'}
                </button>
              </div>
            </div>
            {showPasswordForm && (
              <div className="settings-inline-panel">
                <form className="settings-form-grid settings-form-grid-dense settings-password-form" onSubmit={handleChangePassword}>
                  <div className="settings-form-group">
                    <label className="settings-form-label">当前密码</label>
                    <input
                      type="password"
                      value={currentPassword}
                      onChange={(event) => setCurrentPassword(event.target.value)}
                      autoComplete="current-password"
                    />
                  </div>
                  <div className="settings-form-group">
                    <label className="settings-form-label">新密码</label>
                    <input
                      type="password"
                      value={newPassword}
                      onChange={(event) => setNewPassword(event.target.value)}
                      autoComplete="new-password"
                    />
                    <div className={`settings-password-meter ${passwordStrength.tone}`}>
                      <span>{passwordStrength.label}</span>
                      <small>{passwordStrength.hint}</small>
                    </div>
                  </div>
                  <div className="settings-form-group">
                    <label className="settings-form-label">确认新密码</label>
                    <input
                      type="password"
                      value={confirmPassword}
                      onChange={(event) => setConfirmPassword(event.target.value)}
                      autoComplete="new-password"
                    />
                  </div>
                  <div className="settings-form-group settings-security-action-cell">
                    <button
                      className="settings-action-btn settings-action-btn-primary"
                      disabled={busyAction === 'change-password'}
                      type="submit"
                    >
                      {busyAction === 'change-password' ? '更新中...' : '更新密码'}
                    </button>
                  </div>
                </form>
              </div>
            )}
          </div>
        </section>

        <section className="settings-setting-section">
          <div className="settings-setting-section-header">
            <div>
              <h3>服务端会话</h3>
              <p>查看当前设备和其它浏览器登录态。</p>
            </div>
            <button className="settings-action-btn" onClick={() => void loadSecurityData()} type="button">
              刷新
            </button>
          </div>
          <div className="settings-security-list">
            {sessions.length === 0 && <div className="settings-empty-row">暂无会话记录</div>}
            {currentSessions.length > 0 && <div className="settings-list-group-label">当前设备</div>}
            {currentSessions.map(renderSessionRow)}
            {otherSessions.length > 0 && <div className="settings-list-group-label">其它设备</div>}
            {otherSessions.map(renderSessionRow)}
            {revokedSessions.length > 0 && <div className="settings-list-group-label">最近失效</div>}
            {revokedSessions.map(renderSessionRow)}
          </div>
        </section>

        <section className="settings-setting-section">
          <div className="settings-setting-section-header">
            <div>
              <h3>API Key</h3>
              <p>用于外部客户端调用 OpenAI-compatible API 或 MCP 工具，和网页登录会话分离。</p>
            </div>
          </div>
          <div className="settings-inline-panel settings-inline-panel-quiet">
            <form className="settings-token-create-row" onSubmit={handleCreateAPIKey}>
              <input
                value={keyName}
                onChange={(event) => setKeyName(event.target.value)}
                placeholder="例如：server-deploy"
              />
              <button
                className="settings-action-btn settings-action-btn-primary"
                disabled={busyAction === 'create-key'}
                type="submit"
              >
                {busyAction === 'create-key' ? '创建中...' : '创建'}
              </button>
            </form>
            <div className="settings-scope-options" role="group" aria-label="API Key 权限">
              {apiKeyScopeOptions.map((option) => (
                <label className="settings-scope-option" key={option.value}>
                  <input
                    checked={selectedScopes.includes(option.value)}
                    onChange={() => handleToggleScope(option.value)}
                    type="checkbox"
                  />
                  <span>
                    <strong>{option.label}</strong>
                    <small>{option.description}</small>
                  </span>
                </label>
              ))}
            </div>

            {createdToken && (
              <div className="settings-created-token">
                <div className="settings-created-token-copy">
                  <span>只显示一次</span>
                  <strong>请现在复制保存，关闭后无法再次查看。</strong>
                </div>
                <code>{createdToken}</code>
                <div className="settings-created-token-actions">
                  <button className="settings-action-btn" onClick={() => void handleCopyCreatedToken()} type="button">
                    复制
                  </button>
                  <button className="settings-action-btn settings-action-btn-primary" onClick={handleConfirmCreatedTokenSaved} type="button">
                    我已保存
                  </button>
                </div>
              </div>
            )}
          </div>

          <div className="settings-security-list settings-api-key-list">
            {apiKeys.length === 0 && <div className="settings-empty-row">暂无 API Key</div>}
            {apiKeys.map((apiKey) => (
              <div className="settings-security-row" key={apiKey.id}>
                <div>
                  <strong>{apiKey.name}</strong>
                  <span>{apiKey.prefix}... · {formatAPIKeyScopes(apiKey.scopes)} · 创建 {formatDateTime(apiKey.createdAt)}</span>
                </div>
                <div className="settings-security-row-meta">
                  <span>{apiKey.revokedAt ? '已撤销' : apiKey.lastUsedAt ? `最近使用 ${formatDateTime(apiKey.lastUsedAt)}` : '未使用'}</span>
                  {!apiKey.revokedAt && (
                    <button
                      className="settings-action-btn settings-action-btn-danger"
                      disabled={busyAction === apiKey.id}
                      onClick={() => void handleRevokeAPIKey(apiKey.id)}
                      type="button"
                    >
                      撤销
                    </button>
                  )}
                </div>
              </div>
            ))}
          </div>
        </section>

        <section className="settings-setting-section">
          <div className="settings-setting-section-header">
            <div>
              <h3>安全事件</h3>
              <p>记录登录、改密、API Key 变更等关键动作。</p>
            </div>
          </div>
          <div className="settings-security-list settings-event-list">
            {events.length === 0 && <div className="settings-empty-row">暂无安全事件</div>}
            {events.slice(0, 10).map((event, index) => (
              <div
                className="settings-security-row"
                key={`${event.id}-${event.createdAt}-${index}`}
              >
                <div>
                  <strong>{eventLabelMap[event.type] || event.type}</strong>
                  <span>{event.message || '已记录'}{event.ip ? ` · ${event.ip}` : ''}</span>
                </div>
                <div className="settings-security-row-meta">
                  <span>{formatDateTime(event.createdAt)}</span>
                </div>
              </div>
            ))}
          </div>
        </section>

        <section className="settings-setting-section settings-setting-section-danger">
          <div className="settings-setting-list">
            <div className="settings-setting-row">
              <div className="settings-setting-row-main">
                <strong>会话操作</strong>
                <span>退出当前设备，或撤销所有已登录设备。</span>
              </div>
              <div className="settings-setting-row-action">
                <button className="btn-danger settings-logout-btn-full" onClick={() => setShowLogoutConfirm(true)} type="button">
                  退出登录
                </button>
                <button
                  className="btn-danger settings-logout-btn-full"
                  disabled={busyAction === 'logout-all'}
                  onClick={() => setShowLogoutAllConfirm(true)}
                  type="button"
                >
                  退出所有设备
                </button>
              </div>
            </div>
          </div>
        </section>
      </div>

      {showLogoutConfirm && (
        <div className="logout-confirm-overlay" onClick={() => setShowLogoutConfirm(false)}>
          <div className="logout-confirm-dialog" onClick={(event) => event.stopPropagation()}>
            <div className="logout-confirm-icon">
              <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="2"/>
                <path d="M12 9V13" stroke="currentColor" strokeWidth="2" strokeLinecap="round"/>
                <circle cx="12" cy="16" r="0.5" fill="currentColor"/>
              </svg>
            </div>
            <h4>确认退出登录</h4>
            <p>退出后需要重新输入 root 密码。</p>
            <div className="logout-confirm-actions">
              <button className="btn-secondary" onClick={() => setShowLogoutConfirm(false)} type="button">
                取消
              </button>
              <button className="btn-danger" onClick={() => void handleLogout()} type="button">
                确认退出
              </button>
            </div>
          </div>
        </div>
      )}

      {showLogoutAllConfirm && (
        <div className="logout-confirm-overlay" onClick={() => setShowLogoutAllConfirm(false)}>
          <div className="logout-confirm-dialog" onClick={(event) => event.stopPropagation()}>
            <div className="logout-confirm-icon">
              <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="2"/>
                <path d="M12 8V12" stroke="currentColor" strokeWidth="2" strokeLinecap="round"/>
                <path d="M12 16H12.01" stroke="currentColor" strokeWidth="2" strokeLinecap="round"/>
              </svg>
            </div>
            <h4>退出所有设备</h4>
            <p>所有浏览器会话都会失效，需要重新登录。</p>
            <div className="logout-confirm-actions">
              <button className="btn-secondary" onClick={() => setShowLogoutAllConfirm(false)} type="button">
                取消
              </button>
              <button className="btn-danger" onClick={() => void handleLogoutAll()} type="button">
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
