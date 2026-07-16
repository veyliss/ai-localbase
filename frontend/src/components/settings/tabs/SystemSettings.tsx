import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { useAuth } from '../../../contexts/AuthContext'
import {
  fetchAuthSessions,
  fetchSecurityEvents,
  type AuthSessionInfo,
  type SecurityEventInfo,
} from '../../../services/api'
import AppIcon from '../../common/AppIcon'
import ConfirmDialog from '../../common/ConfirmDialog'

interface SystemSettingsProps {
  onLogout: () => void | Promise<void>
}

type SecurityEventFilter = 'all' | 'account' | 'api-key' | 'mcp' | 'failures'

const securityEventFilters: Array<{ id: SecurityEventFilter; label: string }> = [
  { id: 'all', label: '全部' },
  { id: 'account', label: '账户' },
  { id: 'api-key', label: 'API Key' },
  { id: 'mcp', label: 'MCP' },
  { id: 'failures', label: '失败与提醒' },
]

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
  mcp_danger_succeeded: 'MCP 危险操作成功',
  mcp_danger_failed: 'MCP 危险操作失败',
  root_password_reset_from_env: '环境变量重置密码',
  weak_env_password: '弱密码提醒',
  weak_env_reset_password: '弱重置密码提醒',
}

const normalizeVersion = (value?: string) => {
  if (!value) return ''
  return value.replace(/_/g, '.').split('.').slice(0, 2).join('.')
}

const describeUserAgent = (userAgent?: string) => {
  if (!userAgent) {
    return { browser: '未知浏览器', system: '未知系统' }
  }

  const browserPatterns: Array<[RegExp, string]> = [
    [/EdgA?\/([\d.]+)/, 'Microsoft Edge'],
    [/OPR\/([\d.]+)/, 'Opera'],
    [/CriOS\/([\d.]+)/, 'Chrome'],
    [/Chrome\/([\d.]+)/, 'Chrome'],
    [/FxiOS\/([\d.]+)/, 'Firefox'],
    [/Firefox\/([\d.]+)/, 'Firefox'],
    [/Version\/([\d.]+).*Safari/, 'Safari'],
    [/Electron\/([\d.]+)/, 'Electron'],
    [/PostmanRuntime\/([\d.]+)/, 'Postman'],
    [/curl\/([\d.]+)/, 'curl'],
  ]
  const browserMatch = browserPatterns.find(([pattern]) => pattern.test(userAgent))
  const browserVersion = browserMatch?.[0].exec(userAgent)?.[1]
  const browser = browserMatch
    ? `${browserMatch[1]}${browserVersion ? ` ${normalizeVersion(browserVersion)}` : ''}`
    : '其它客户端'

  let system = '未知系统'
  const iosMatch = /(?:iPhone|CPU(?: iPhone)? OS) ([\d_]+)/.exec(userAgent)
  const androidMatch = /Android ([\d.]+)/.exec(userAgent)
  const windowsMatch = /Windows NT ([\d.]+)/.exec(userAgent)
  const macMatch = /Mac OS X ([\d_]+)/.exec(userAgent)

  if (/iPad/.test(userAgent) || (/Macintosh/.test(userAgent) && /Mobile/.test(userAgent))) {
    system = `iPadOS${iosMatch?.[1] ? ` ${normalizeVersion(iosMatch[1])}` : ''}`
  } else if (/iPhone/.test(userAgent)) {
    system = `iOS${iosMatch?.[1] ? ` ${normalizeVersion(iosMatch[1])}` : ''}`
  } else if (androidMatch) {
    system = `Android ${normalizeVersion(androidMatch[1])}`
  } else if (windowsMatch) {
    const windowsVersions: Record<string, string> = {
      '10.0': 'Windows 10/11',
      '6.3': 'Windows 8.1',
      '6.2': 'Windows 8',
      '6.1': 'Windows 7',
    }
    system = windowsVersions[windowsMatch[1]] ?? `Windows ${windowsMatch[1]}`
  } else if (macMatch) {
    system = `macOS ${normalizeVersion(macMatch[1])}`
  } else if (/Linux/.test(userAgent)) {
    system = 'Linux'
  }

  return { browser, system }
}

const isSessionExpired = (session: AuthSessionInfo) => {
  const expiresAt = new Date(session.expiresAt).getTime()
  return Number.isFinite(expiresAt) && expiresAt <= Date.now()
}

const isSessionInactive = (session: AuthSessionInfo) => Boolean(session.revokedAt) || isSessionExpired(session)

const isMCPEvent = (event: SecurityEventInfo) => event.type.startsWith('mcp_')
const isAPIKeyEvent = (event: SecurityEventInfo) => event.type.startsWith('api_key_')
const isFailureEvent = (event: SecurityEventInfo) => (
  event.type.includes('_failed') || event.type.startsWith('weak_')
)

const matchesEventFilter = (event: SecurityEventInfo, filter: SecurityEventFilter) => {
  switch (filter) {
    case 'account':
      return !isMCPEvent(event) && !isAPIKeyEvent(event)
    case 'api-key':
      return isAPIKeyEvent(event)
    case 'mcp':
      return isMCPEvent(event)
    case 'failures':
      return isFailureEvent(event)
    default:
      return true
  }
}

const SystemSettings: React.FC<SystemSettingsProps> = ({ onLogout }) => {
  const { username, expiresAt, logoutAll, changePassword } = useAuth()
  const [sessions, setSessions] = useState<AuthSessionInfo[]>([])
  const [events, setEvents] = useState<SecurityEventInfo[]>([])
  const [eventFilter, setEventFilter] = useState<SecurityEventFilter>('all')
  const [loading, setLoading] = useState(true)
  const [feedback, setFeedback] = useState('')
  const [error, setError] = useState('')
  const [showLogoutConfirm, setShowLogoutConfirm] = useState(false)
  const [showLogoutAllConfirm, setShowLogoutAllConfirm] = useState(false)
  const [showPasswordForm, setShowPasswordForm] = useState(false)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [busyAction, setBusyAction] = useState('')

  const activeSessions = useMemo(
    () => sessions.filter((session) => !isSessionInactive(session)),
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
  const inactiveSessions = useMemo(
    () => sessions.filter(isSessionInactive).slice(0, 4),
    [sessions],
  )
  const eventCounts = useMemo(
    () => Object.fromEntries(securityEventFilters.map((filter) => [
      filter.id,
      events.filter((event) => matchesEventFilter(event, filter.id)).length,
    ])) as Record<SecurityEventFilter, number>,
    [events],
  )
  const filteredEvents = useMemo(
    () => events.filter((event) => matchesEventFilter(event, eventFilter)).slice(0, 20),
    [eventFilter, events],
  )
  const passwordStrength = useMemo(() => getPasswordStrength(newPassword), [newPassword])

  const loadSecurityData = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [nextSessions, nextEvents] = await Promise.all([
        fetchAuthSessions(),
        fetchSecurityEvents(50),
      ])
      setSessions(nextSessions)
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

  const renderSessionRow = (session: AuthSessionInfo) => {
    const client = describeUserAgent(session.userAgent)
    const inactive = isSessionInactive(session)
    const statusLabel = inactive ? '历史会话' : session.current ? '当前设备' : '登录设备'
    const inactiveAt = session.revokedAt || session.expiresAt

    return (
      <details className={`settings-security-entry ${inactive ? 'is-muted' : ''}`} key={session.id}>
        <summary className="settings-security-entry-summary">
          <span className="settings-security-entry-icon" aria-hidden="true">
            <AppIcon name="user" size={17} />
          </span>
          <span className="settings-security-entry-main">
            <strong>{client.browser}</strong>
            <span>{client.system} · {statusLabel}</span>
          </span>
          <span className="settings-security-entry-meta">
            <strong>{inactive ? '已失效' : formatTimeRemaining(session.expiresAt)}</strong>
            <small>{inactive ? `失效 ${formatDateTime(inactiveAt)}` : `到期 ${formatDateTime(session.expiresAt)}`}</small>
          </span>
          <AppIcon className="settings-security-entry-chevron" name="chevronDown" size={16} />
        </summary>
        <div className="settings-security-entry-details">
          <div>
            <span>IP 地址</span>
            <strong>{session.ip || '未知'}</strong>
          </div>
          <div>
            <span>创建时间</span>
            <strong>{formatDateTime(session.createdAt)}</strong>
          </div>
          <div>
            <span>最近活动</span>
            <strong>{formatDateTime(session.lastSeenAt)}</strong>
          </div>
          <div className="settings-security-detail-wide">
            <span>User-Agent</span>
            <code>{session.userAgent || '未知客户端'}</code>
          </div>
        </div>
      </details>
    )
  }

  const renderEventRow = (event: SecurityEventInfo, index: number) => {
    const failed = isFailureEvent(event)
    const category = isMCPEvent(event) ? 'MCP' : isAPIKeyEvent(event) ? 'API Key' : '账户'

    return (
      <details
        className={`settings-security-entry settings-event-entry ${failed ? 'is-failure' : ''}`}
        key={`${event.id}-${event.createdAt}-${index}`}
      >
        <summary className="settings-security-entry-summary">
          <span className="settings-security-entry-icon" aria-hidden="true">
            <AppIcon name={failed ? 'alert' : 'shield'} size={17} />
          </span>
          <span className="settings-security-entry-main">
            <strong>{eventLabelMap[event.type] || event.type}</strong>
            <span>{event.message || `${category}事件已记录`}</span>
          </span>
          <span className="settings-security-entry-meta">
            <strong>{category}</strong>
            <small>{formatDateTime(event.createdAt)}</small>
          </span>
          <AppIcon className="settings-security-entry-chevron" name="chevronDown" size={16} />
        </summary>
        <div className="settings-security-entry-details">
          <div>
            <span>事件类型</span>
            <code>{event.type}</code>
          </div>
          <div>
            <span>账户</span>
            <strong>{event.username || username || 'root'}</strong>
          </div>
          <div>
            <span>IP 地址</span>
            <strong>{event.ip || '未知'}</strong>
          </div>
          <div>
            <span>发生时间</span>
            <strong>{formatDateTime(event.createdAt)}</strong>
          </div>
          <div className="settings-security-detail-wide">
            <span>User-Agent</span>
            <code>{event.userAgent || '未记录'}</code>
          </div>
        </div>
      </details>
    )
  }

  return (
    <>
      <div className="settings-tab-content settings-security-content">
        <section className="settings-security-overview-panel" aria-label="账户概览">
          <div className="settings-security-identity">
            <div>
              <span>Root 账户</span>
              <strong>{username || 'root'}</strong>
              <p>管理登录密码、设备会话和安全记录。</p>
            </div>
            <span className="settings-status-pill enabled">已认证</span>
          </div>

          <div className="settings-security-metrics">
            <div>
              <span>当前会话</span>
              <strong>{formatTimeRemaining(expiresAt)}</strong>
              <small>{formatDateTime(expiresAt)}</small>
            </div>
            <div>
              <span>活跃设备</span>
              <strong>{activeSessions.length}</strong>
              <small>含当前浏览器</small>
            </div>
            <div>
              <span>最近记录</span>
              <strong>{events[0] ? formatDateTime(events[0].createdAt) : '暂无'}</strong>
              <small>{events[0] ? eventLabelMap[events[0].type] || events[0].type : '尚无安全事件'}</small>
            </div>
          </div>

          {(loading || feedback || error) && (
            <div className="settings-security-notices">
              {loading && <div className="settings-inline-note">正在加载账户安全状态...</div>}
              {feedback && <div className="settings-inline-note success">{feedback}</div>}
              {error && <div className="settings-inline-note error">{error}</div>}
            </div>
          )}
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
                      autoComplete="current-password"
                      onChange={(event) => setCurrentPassword(event.target.value)}
                      type="password"
                      value={currentPassword}
                    />
                  </div>
                  <div className="settings-form-group">
                    <label className="settings-form-label">新密码</label>
                    <input
                      autoComplete="new-password"
                      onChange={(event) => setNewPassword(event.target.value)}
                      type="password"
                      value={newPassword}
                    />
                    <div className={`settings-password-meter ${passwordStrength.tone}`}>
                      <span>{passwordStrength.label}</span>
                      <small>{passwordStrength.hint}</small>
                    </div>
                  </div>
                  <div className="settings-form-group">
                    <label className="settings-form-label">确认新密码</label>
                    <input
                      autoComplete="new-password"
                      onChange={(event) => setConfirmPassword(event.target.value)}
                      type="password"
                      value={confirmPassword}
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
              <h3>设备会话</h3>
              <p>按浏览器和系统识别登录设备，展开后可查看完整连接信息。</p>
            </div>
            <button
              aria-label="刷新设备会话"
              className="settings-action-btn settings-icon-action"
              onClick={() => void loadSecurityData()}
              title="刷新设备会话"
              type="button"
            >
              <AppIcon name="refresh" size={16} />
            </button>
          </div>
          <div className="settings-security-list settings-security-entry-list">
            {!loading && sessions.length === 0 && <div className="settings-empty-row">暂无会话记录</div>}
            {currentSessions.length > 0 && <div className="settings-list-group-label">当前设备</div>}
            {currentSessions.map(renderSessionRow)}
            {otherSessions.length > 0 && <div className="settings-list-group-label">其它设备</div>}
            {otherSessions.map(renderSessionRow)}
            {inactiveSessions.length > 0 && <div className="settings-list-group-label">最近失效</div>}
            {inactiveSessions.map(renderSessionRow)}
          </div>
        </section>

        <section className="settings-setting-section">
          <div className="settings-setting-section-header">
            <div>
              <h3>安全记录</h3>
              <p>统一查看登录、密码、API Key 和 MCP 调用事件。</p>
            </div>
          </div>

          <div className="settings-record-filters" role="group" aria-label="安全记录筛选">
            {securityEventFilters.map((filter) => (
              <button
                aria-pressed={eventFilter === filter.id}
                className={eventFilter === filter.id ? 'active' : ''}
                key={filter.id}
                onClick={() => setEventFilter(filter.id)}
                type="button"
              >
                <span>{filter.label}</span>
                <strong>{eventCounts[filter.id]}</strong>
              </button>
            ))}
          </div>

          <div className="settings-security-list settings-security-entry-list settings-event-list">
            {!loading && filteredEvents.length === 0 && <div className="settings-empty-row">当前筛选下暂无安全记录</div>}
            {filteredEvents.map(renderEventRow)}
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

      <ConfirmDialog
        cancelText="取消"
        confirmText="确认退出"
        message="退出后需要重新输入 root 密码。"
        onCancel={() => setShowLogoutConfirm(false)}
        onConfirm={() => void handleLogout()}
        open={showLogoutConfirm}
        title="退出当前设备？"
      />

      <ConfirmDialog
        cancelText="取消"
        confirmText="退出所有设备"
        message="所有浏览器会话都会立即失效，需要重新登录。"
        onCancel={() => setShowLogoutAllConfirm(false)}
        onConfirm={() => void handleLogoutAll()}
        open={showLogoutAllConfirm}
        title="撤销所有会话？"
      />
    </>
  )
}

export default SystemSettings
