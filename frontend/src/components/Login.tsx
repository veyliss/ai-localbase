import React, { useState, useEffect, useRef } from 'react'
import { useAuth } from '../contexts/AuthContext'
import '../styles/Login.css'

const Login: React.FC = () => {
  const [password, setPassword] = useState('')
  const { login, authError } = useAuth()
  const [isLoading, setIsLoading] = useState(false)
  const [isFocused, setIsFocused] = useState(false)
  const passwordRef = useRef<HTMLInputElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    passwordRef.current?.focus()
  }, [])

  // 粒子背景动画
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    const ctx = canvas.getContext('2d', { alpha: true })
    if (!ctx) return

    let animationFrameId: number
    let particles: Particle[] = []
    let mouse: { x: number; y: number } = { x: -1000, y: -1000 }

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

        // 鼠标交互
        const dx = mouse.x - this.x
        const dy = mouse.y - this.y
        const dist = Math.sqrt(dx * dx + dy * dy)
        if (dist < 150) {
          const force = (150 - dist) / 150
          this.vx -= (dx / dist) * force * 0.02
          this.vy -= (dy / dist) * force * 0.02
        }

        // 速度衰减
        this.vx *= 0.99
        this.vy *= 0.99

        // 边界环绕
        if (this.x < 0) this.x = this.canvasWidth
        if (this.x > this.canvasWidth) this.x = 0
        if (this.y < 0) this.y = this.canvasHeight
        if (this.y > this.canvasHeight) this.y = 0

        // 动态大小
        this.size = this.baseSize + Math.sin(Date.now() * 0.001 + this.x) * 0.5
      }

      draw(ctx: CanvasRenderingContext2D) {
        ctx.beginPath()
        ctx.arc(this.x, this.y, this.size, 0, Math.PI * 2)
        ctx.fillStyle = `rgba(37, 99, 235, ${this.opacity})`
        ctx.fill()
      }
    }

    // 初始化粒子
    const particleCount = Math.min(Math.floor((canvas.width * canvas.height) / 15000), 100)
    for (let i = 0; i < particleCount; i++) {
      particles.push(new Particle(canvas.width, canvas.height))
    }

    const drawConnections = () => {
      for (let i = 0; i < particles.length; i++) {
        for (let j = i + 1; j < particles.length; j++) {
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
      
      particles.forEach(p => {
        p.update()
        p.draw(ctx)
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
    setIsLoading(true)
    try {
      await login(password)
    } catch {
      // Error handled by context
    } finally {
      setIsLoading(false)
    }
  }

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
          {/* Logo Section */}
          <div className="login-header">
            <div className="login-logo-wrapper">
              <div className="login-logo-icon">
                <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
                  <path d="M12 2L2 7L12 12L22 7L12 2Z" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                  <path d="M2 17L12 22L22 17" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                  <path d="M2 12L12 17L22 12" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
                </svg>
              </div>
            </div>
            <h1 className="login-title">AI LocalBase</h1>
            <p className="login-description">本地知识库智能管理系统</p>
          </div>

          {/* Form Section */}
          <form onSubmit={handleSubmit} className="login-form">
            <div className={`input-wrapper ${isFocused ? 'focused' : ''}`}>
              <svg className="input-icon" viewBox="0 0 24 24" fill="none">
                <rect x="3" y="11" width="18" height="11" rx="2" stroke="currentColor" strokeWidth="1.5"/>
                <path d="M7 11V7C7 4.23858 9.23858 2 12 2C14.7614 2 17 4.23858 17 7V11" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
              </svg>
              <input
                ref={passwordRef}
                type="password"
                placeholder="请输入访问密码"
                value={password}
                onChange={e => setPassword(e.target.value)}
                onFocus={() => setIsFocused(true)}
                onBlur={() => setIsFocused(false)}
                disabled={isLoading}
                className="login-input password-field"
                aria-label="访问密码"
                required
              />
              <button
                type="button"
                className="password-toggle"
                onClick={() => {
                  const input = passwordRef.current
                  if (input) {
                    input.type = input.type === 'password' ? 'text' : 'password'
                  }
                }}
                disabled={isLoading}
                aria-label="切换密码可见性"
              >
                <svg className="eye-icon eye-off" viewBox="0 0 24 24" fill="none">
                  <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20C7.78 20 3.4 17.12 1 12C3.4 6.88 7.78 4 12 4C13.42 4 14.8 4.3 16.04 4.84" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                  <path d="M9.29 6.29A3.01 3.01 0 0 1 12 4C16.22 4 20.6 6.88 23 12C21.4 15.38 19.08 17.7 16.5 18.94" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                  <path d="M1 1L23 23" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                  <path d="M6.5 6.5C8.15 8.15 9.6 9.6 12 12C14.2 14.2 15.65 15.65 17.5 17.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                </svg>
                <svg className="eye-icon eye-on" viewBox="0 0 24 24" fill="none">
                  <path d="M1 12C1 12 5 4 12 4C19 4 23 12 23 12C23 12 19 20 12 20C5 20 1 12 1 12Z" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                  <circle cx="12" cy="12" r="3" stroke="currentColor" strokeWidth="1.5"/>
                </svg>
              </button>
            </div>

            {authError && (
              <div className="login-error" role="alert">
                <svg viewBox="0 0 24 24" fill="none">
                  <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="1.5"/>
                  <path d="M12 8V12" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
                  <circle cx="12" cy="16" r="0.5" fill="currentColor"/>
                </svg>
                <span>{authError}</span>
              </div>
            )}

            <button 
              type="submit" 
              className="login-submit-btn" 
              disabled={isLoading || !password}
            >
              {isLoading ? (
                <>
                  <span className="submit-spinner"></span>
                  <span>验证中...</span>
                </>
              ) : (
                '安全登录'
              )}
            </button>
          </form>

          {/* Footer */}
          <div className="login-footer">
            <p>本地部署 · 安全可控 · 数据隐私</p>
          </div>
        </div>

        {/* Version Badge */}
        <div className="login-version">
          v1.3.0
        </div>
      </div>
    </div>
  )
}

export default Login
