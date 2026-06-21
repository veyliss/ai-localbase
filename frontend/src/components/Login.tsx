import React, { useState, useEffect, useMemo, useRef } from 'react'
import { useAuth } from '../contexts/AuthContext'
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
  const canvasRef = useRef<HTMLCanvasElement>(null)
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

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    const ctx = canvas.getContext('2d', { alpha: true })
    if (!ctx) return

    let animationFrameId: number
    const particles: Particle[] = []
    const mouse: { x: number; y: number } = { x: -1000, y: -1000 }

    const resize = () => {
      canvas.width = window.innerWidth
      canvas.height = window.innerHeight
    }
    resize()
    window.addEventListener('resize', resize)

    const handleMouseMove = (e: MouseEvent) => {
      mouse.x = e.clientX
      mouse.y = e.clientY
    }
    window.addEventListener('mousemove', handleMouseMove)

    class Particle {
      x: number
      y: number
      vx: number
      vy: number
      size: number
      baseSize: number
      opacity: number
      canvasWidth: number
      canvasHeight: number

      constructor(width: number, height: number) {
        this.canvasWidth = width
        this.canvasHeight = height
        this.x = Math.random() * width
        this.y = Math.random() * height
        this.vx = (Math.random() - 0.5) * 0.5
        this.vy = (Math.random() - 0.5) * 0.5
        this.baseSize = Math.random() * 2 + 1
        this.size = this.baseSize
        this.opacity = Math.random() * 0.5 + 0.2
      }

      update() {
        this.x += this.vx
        this.y += this.vy

        const dx = mouse.x - this.x
        const dy = mouse.y - this.y
        const dist = Math.sqrt(dx * dx + dy * dy)
        if (dist > 0 && dist < 150) {
          const force = (150 - dist) / 150
          this.vx -= (dx / dist) * force * 0.02
          this.vy -= (dy / dist) * force * 0.02
        }

        this.vx *= 0.99
        this.vy *= 0.99

        if (this.x < 0) this.x = this.canvasWidth
        if (this.x > this.canvasWidth) this.x = 0
        if (this.y < 0) this.y = this.canvasHeight
        if (this.y > this.canvasHeight) this.y = 0

        this.size = this.baseSize + Math.sin(Date.now() * 0.001 + this.x) * 0.5
      }

      draw(nextCtx: CanvasRenderingContext2D) {
        nextCtx.beginPath()
        nextCtx.arc(this.x, this.y, this.size, 0, Math.PI * 2)
        nextCtx.fillStyle = `rgba(37, 99, 235, ${this.opacity})`
        nextCtx.fill()
      }
    }

    const particleCount = Math.min(Math.floor((canvas.width * canvas.height) / 15000), 100)
    for (let i = 0; i < particleCount; i += 1) {
      particles.push(new Particle(canvas.width, canvas.height))
    }

    const drawConnections = () => {
      for (let i = 0; i < particles.length; i += 1) {
        for (let j = i + 1; j < particles.length; j += 1) {
          const dx = particles[i].x - particles[j].x
          const dy = particles[i].y - particles[j].y
          const dist = Math.sqrt(dx * dx + dy * dy)

          if (dist < 120) {
            const opacity = (1 - dist / 120) * 0.15
            ctx.beginPath()
            ctx.moveTo(particles[i].x, particles[i].y)
            ctx.lineTo(particles[j].x, particles[j].y)
            ctx.strokeStyle = `rgba(37, 99, 235, ${opacity})`
            ctx.lineWidth = 0.5
            ctx.stroke()
          }
        }
      }
    }

    const animate = () => {
      ctx.clearRect(0, 0, canvas.width, canvas.height)

      particles.forEach((particle) => {
        particle.update()
        particle.draw(ctx)
      })

      drawConnections()
      animationFrameId = requestAnimationFrame(animate)
    }

    animate()

    return () => {
      cancelAnimationFrame(animationFrameId)
      window.removeEventListener('resize', resize)
      window.removeEventListener('mousemove', handleMouseMove)
    }
  }, [])

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
    <div className="login-page">
      <canvas
        ref={canvasRef}
        className="login-canvas"
        aria-hidden="true"
      />

      <div className="login-overlay"></div>

      <div className="login-content">
        <div className="login-card">
          <div className="login-header">
            <div className="login-logo-wrapper">
              <div className="login-logo-icon">
                <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
                  <path d="M12 2L2 7L12 12L22 7L12 2Z" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                  <path d="M2 17L12 22L22 17" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                  <path d="M2 12L12 17L22 12" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                </svg>
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
              <svg className="input-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <path d="M20 21a8 8 0 0 0-16 0" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                <circle cx="12" cy="7" r="4" stroke="currentColor" strokeWidth="1.5"/>
              </svg>
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
              <svg className="input-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                <rect x="3" y="11" width="18" height="11" rx="2" stroke="currentColor" strokeWidth="1.5"/>
                <path d="M7 11V7C7 4.23858 9.23858 2 12 2C14.7614 2 17 4.23858 17 7V11" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
              </svg>
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
                {showPassword ? (
                  <svg className="eye-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                    <path d="M1 12C1 12 5 4 12 4C19 4 23 12 23 12C23 12 19 20 12 20C5 20 1 12 1 12Z" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                    <circle cx="12" cy="12" r="3" stroke="currentColor" strokeWidth="1.5"/>
                  </svg>
                ) : (
                  <svg className="eye-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                    <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20C7.78 20 3.4 17.12 1 12C3.4 6.88 7.78 4 12 4C13.42 4 14.8 4.3 16.04 4.84" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                    <path d="M1 1L23 23" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                  </svg>
                )}
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
                  <svg className="input-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                    <path d="M20 6L9 17L4 12" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"/>
                  </svg>
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
                    <svg className="input-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                      <path d="M15 7h.01M11 11h.01M7 15h.01M9.4 21H5a2 2 0 0 1-2-2v-4.4a2 2 0 0 1 .59-1.42L13.17 3.6a2 2 0 0 1 2.82 0l4.41 4.41a2 2 0 0 1 0 2.82l-9.58 9.58A2 2 0 0 1 9.4 21Z" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                    </svg>
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
                <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
                  <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="1.5"/>
                  <path d="M12 8V12" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                  <circle cx="12" cy="16" r="0.5" fill="currentColor"/>
                </svg>
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

        <div className="login-version">
          v1.3.0
        </div>
      </div>

      {showResetHelp && (
        <div className="login-help-overlay" onClick={() => setShowResetHelp(false)}>
          <div className="login-help-dialog" onClick={(event) => event.stopPropagation()} role="dialog" aria-modal="true" aria-labelledby="login-reset-title">
            <div className="login-help-icon" aria-hidden="true">
              <svg viewBox="0 0 24 24" fill="none">
                <path d="M12 3L4 6.5V12C4 16.4 7.4 20.1 12 21C16.6 20.1 20 16.4 20 12V6.5L12 3Z" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round"/>
                <path d="M9.2 12.2L11.1 14.1L15.2 9.9" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"/>
              </svg>
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
    </div>
  )
}

export default Login
