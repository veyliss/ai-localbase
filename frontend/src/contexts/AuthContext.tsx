import React, { createContext, useContext, useState, useCallback, useMemo, useEffect, type ReactNode } from 'react'
import { AUTH_UNAUTHORIZED_EVENT, parseJsonResponse } from '../services/api'

interface AuthContextValue {
  token: string | null
  isAuthenticated: boolean
  login: (password: string) => Promise<void>
  logout: () => void
  authError: string
}

const AuthContext = createContext<AuthContextValue | null>(null)

export const useAuth = () => {
  const context = useContext(AuthContext)
  if (!context) {
    throw new Error('useAuth must be used within AuthProvider')
  }
  return context
}

interface AuthProviderProps {
  children: ReactNode
}

const readStoredToken = () => {
  const storedToken = localStorage.getItem('auth_token')
  if (!storedToken) {
    return null
  }

  const expiresAt = Number(localStorage.getItem('token_expires_at') || '0')
  if (!Number.isFinite(expiresAt) || expiresAt <= Math.floor(Date.now() / 1000)) {
    localStorage.removeItem('auth_token')
    localStorage.removeItem('token_expires_at')
    return null
  }

  return storedToken
}

export const AuthProvider: React.FC<AuthProviderProps> = ({ children }) => {
  const [token, setToken] = useState<string | null>(readStoredToken)
  const [authError, setAuthError] = useState('')

  const login = useCallback(async (password: string) => {
    setAuthError('')
    try {
      const response = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password })
      })

      const data = await parseJsonResponse<{
        token?: string
        expires_at?: number
        error?: string
      }>(response)

      if (!response.ok) {
        throw new Error(data?.error || response.statusText || '登录失败')
      }

      if (!data?.token || !data.expires_at) {
        throw new Error('登录接口返回空响应')
      }

      const { token: newToken, expires_at } = data
      localStorage.setItem('auth_token', newToken)
      localStorage.setItem('token_expires_at', expires_at.toString())
      setToken(newToken)
    } catch (err) {
      const message = err instanceof Error ? err.message : '登录失败'
      setAuthError(message)
      throw err
    }
  }, [])

  const logout = useCallback(() => {
    localStorage.removeItem('auth_token')
    localStorage.removeItem('token_expires_at')
    setToken(null)
  }, [])

  useEffect(() => {
    const handleUnauthorized = () => {
      logout()
    }
    const handleStorage = (event: StorageEvent) => {
      if (event.key === 'auth_token' || event.key === 'token_expires_at') {
        setToken(readStoredToken())
      }
    }

    window.addEventListener(AUTH_UNAUTHORIZED_EVENT, handleUnauthorized)
    window.addEventListener('storage', handleStorage)
    return () => {
      window.removeEventListener(AUTH_UNAUTHORIZED_EVENT, handleUnauthorized)
      window.removeEventListener('storage', handleStorage)
    }
  }, [logout])

  const value = useMemo<AuthContextValue>(
    () => ({
      token,
      isAuthenticated: !!token,
      login,
      logout,
      authError,
    }),
    [token, login, logout, authError]
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
