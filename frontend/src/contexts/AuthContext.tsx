import React, { createContext, useContext, useState, useCallback, useMemo, type ReactNode } from 'react'
import { parseJsonResponse } from '../services/api'

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

export const AuthProvider: React.FC<AuthProviderProps> = ({ children }) => {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('auth_token'))
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
