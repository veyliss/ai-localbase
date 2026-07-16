import React, { useState, useEffect, useMemo, useRef } from 'react'
import { useAuth } from '../contexts/AuthContext'
import { APP_VERSION } from '../utils/appInfo'
import AppIcon from './common/AppIcon'
import '../styles/Login.css'

const getPasswordStrength = (password: string) => {
  if (!password) {
    return { label: '等待输入', tone: 'idle', hint: '建议使用 16 位以上密码' }
  }

  let score = 0
  if (password.length >= 8) score += 1
  if (password.length >= 16) score += 1
  if (/[a-z]/.test(password) && /[A-Z]/.test(password)) score += 1
  if (/\d/.test(password)) score += 1
  if (/[^a-zA-Z0-9]/.test(password)) score += 1

  if (score >= 4) {
    return { label: '强', tone: 'strong', hint: '适合服务器部署' }
  }
  if (score >= 2) {
    return { label: '可用', tone: 'medium', hint: '生产环境建议再增强' }
  }
  return { label: '偏弱', tone: 'weak', hint: '至少 8 位，推荐 16 位以上' }
}

const Login: React.FC = () => {
  const {
    login,
    setup,
    authError,
    authReady,
    setupRequired,
    setupTokenRequired,
    username: configuredUsername,
  } = useAuth()
  const [username, setUsername] = useState(configuredUsername || 'root')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [setupToken, setSetupToken] = useState('')
  const [localError, setLocalError] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [focusedField, setFocusedField] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [showResetHelp, setShowResetHelp] = useState(false)
  const usernameRef = useRef<HTMLInputElement>(null)
  const passwordRef = useRef<HTMLInputElement>(null)
  const passwordStrength = useMemo(() => getPasswordStrength(password), [password])

  useEffect(() => {
    setUsername(configuredUsername || 'root')
  }, [configuredUsername])

  useEffect(() => {
    if (setupRequired) {
      usernameRef.current?.focus()
      return
    }
    passwordRef.current?.focus()
  }, [setupRequired])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setLocalError('')

    if (setupRequired) {
      if (password.length < 8) {
        setLocalError('密码至少需要 8 个字符')
        return
      }
      if (password !== confirmPassword) {
        setLocalError('两次输入的密码不一致')
        return
      }
      if (setupTokenRequired && !setupToken.trim()) {
        setLocalError('请输入初始化 Token')
        return
      }
    }

    setIsLoading(true)
    try {
      if (setupRequired) {
        await setup({ username: username.trim() || 'root', password, setupToken: setupToken.trim() || undefined })
      } else {
        await login(username.trim() || 'root', password)
      }
    } catch {
      // Error handled by context.
    } finally {
      setIsLoading(false)
    }
  }

  const displayError = localError || authError
  const submitDisabled = isLoading || !authReady || !password || !username.trim()
    || (setupRequired && (!confirmPassword || (setupTokenRequired && !setupToken.trim())))

  return (
    <main className="login-page">
      <div className="login-content">
        <div className="login-card">
          <div className="login-header">
            <div className="login-logo-wrapper">
              <div className="login-logo-icon">
                <AppIcon name="database" size={24} />
              </div>
            </div>
            <span className={`login-mode-pill ${setupRequired ? 'setup' : ''}`}>
              {setupRequired ? '首次初始化' : 'Root 登录'}
            </span>
            <h1 className="login-title">AI LocalBase</h1>
            <p className="login-description">
              {setupRequired ? '创建本机 root 管理账户' : '使用 root 账户进入本地知识库'}
            </p>
          </div>

          <form onSubmit={handleSubmit} className="login-form">
            {!authReady && (
              <div className="login-status-note">
                <span className="login-status-dot"></span>
                <span>正在检查认证状态...</span>
              </div>
            )}

            <div className={`input-wrapper ${focusedField === 'username' ? 'focused' : ''}`}>
              <AppIcon className="input-icon" name="user" size={18} />
              <input
                ref={usernameRef}
                type="text"
                placeholder="root"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                onFocus={() => setFocusedField('username')}
                onBlur={() => setFocusedField('')}
                disabled={isLoading || !authReady}
                className="login-input username-field"
                aria-label="用户名"
                autoComplete="username"
                required
              />
            </div>

            <div className={`input-wrapper ${focusedField === 'password' ? 'focused' : ''}`}>
              <AppIcon className="input-icon" name="lock" size={18} />
              <input
                ref={passwordRef}
                type={showPassword ? 'text' : 'password'}
                placeholder={setupRequired ? '设置 root 密码' : '输入访问密码'}
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                onFocus={() => setFocusedField('password')}
                onBlur={() => setFocusedField('')}
                disabled={isLoading || !authReady}
                className="login-input password-field"
                aria-label="密码"
                autoComplete={setupRequired ? 'new-password' : 'current-password'}
                required
              />
              <button
                type="button"
                className="password-toggle"
                onClick={() => setShowPassword((visible) => !visible)}
                disabled={isLoading}
                aria-label={showPassword ? '隐藏密码' : '显示密码'}
              >
                <AppIcon className="eye-icon" name={showPassword ? 'eyeOff' : 'eye'} size={17} />
              </button>
            </div>

            {setupRequired && (
              <>
                <div className={`login-password-strength ${passwordStrength.tone}`}>
                  <div>
                    <span>密码强度</span>
                    <strong>{passwordStrength.label}</strong>
                  </div>
                  <p>{passwordStrength.hint}</p>
                </div>

                <div className={`input-wrapper ${focusedField === 'confirm' ? 'focused' : ''}`}>
                  <AppIcon className="input-icon" name="check" size={18} />
                  <input
                    type={showPassword ? 'text' : 'password'}
                    placeholder="确认 root 密码"
                    value={confirmPassword}
                    onChange={(event) => setConfirmPassword(event.target.value)}
                    onFocus={() => setFocusedField('confirm')}
                    onBlur={() => setFocusedField('')}
                    disabled={isLoading || !authReady}
                    className="login-input"
                    aria-label="确认密码"
                    autoComplete="new-password"
                    required
                  />
                </div>

                {setupTokenRequired && (
                  <div className={`input-wrapper ${focusedField === 'setupToken' ? 'focused' : ''}`}>
                    <AppIcon className="input-icon" name="key" size={18} />
                    <input
                      type="password"
                      placeholder="初始化 Token"
                      value={setupToken}
                      onChange={(event) => setSetupToken(event.target.value)}
                      onFocus={() => setFocusedField('setupToken')}
                      onBlur={() => setFocusedField('')}
                      disabled={isLoading || !authReady}
                      className="login-input"
                      aria-label="初始化 Token"
                      required
                    />
                  </div>
                )}
              </>
            )}

            {displayError && (
              <div className="login-error" role="alert">
                <AppIcon name="alert" size={17} />
                <span>{displayError}</span>
              </div>
            )}

            <button
              type="submit"
              className="login-submit-btn"
              disabled={submitDisabled}
            >
              {isLoading ? (
                <>
                  <span className="submit-spinner"></span>
                  <span>{setupRequired ? '初始化中...' : '验证中...'}</span>
                </>
              ) : (
                setupRequired ? '创建 root 账户' : '安全登录'
              )}
            </button>
          </form>

          <div className="login-footer">
            <p>{setupRequired ? '首次创建后，后续登录将使用 root 密码' : '本地部署 · 会话可吊销 · API Key 独立管理'}</p>
            {!setupRequired && (
              <button className="login-help-link" type="button" onClick={() => setShowResetHelp(true)}>
                忘记 root 密码？
              </button>
            )}
          </div>
        </div>

        {APP_VERSION && (
          <div className="login-version">
            {APP_VERSION}
          </div>
        )}
      </div>

      {showResetHelp && (
        <div className="login-help-overlay" onClick={() => setShowResetHelp(false)}>
          <div className="login-help-dialog" onClick={(event) => event.stopPropagation()} role="dialog" aria-modal="true" aria-labelledby="login-reset-title">
            <div className="login-help-icon" aria-hidden="true">
              <AppIcon name="shield" size={24} />
            </div>
            <h2 id="login-reset-title">重置 root 密码</h2>
            <p>自部署环境需要在服务器侧重置密码。设置一次性重置变量并重启后端，登录成功后删除变量再重启。</p>
            <div className="login-reset-steps">
              <div>
                <span>1</span>
                <strong>生成重置 Token</strong>
                <code>openssl rand -base64 32</code>
              </div>
              <div>
                <span>2</span>
                <strong>设置环境变量</strong>
                <code>AUTH_RESET_TOKEN / AUTH_RESET_PASSWORD</code>
              </div>
              <div>
                <span>3</span>
                <strong>重启并登录</strong>
                <code>确认成功后移除重置变量</code>
              </div>
            </div>
            <button className="login-help-close" type="button" onClick={() => setShowResetHelp(false)}>
              我知道了
            </button>
          </div>
        </div>
      )}
    </main>
  )
}

export default Login
