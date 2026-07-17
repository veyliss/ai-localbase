import React, { createContext, useContext, useState, useCallback, useMemo, useEffect, type ReactNode } from 'react'
import {
  AUTH_UNAUTHORIZED_EVENT,
  changeAuthPassword,
  fetchAuthBootstrap,
  loginAuth,
  logoutAllAuth,
  logoutAuth,
  setupAuth,
  type AuthBootstrapResponse,
  type AuthLoginResponse,
} from '../services/api'

interface AuthContextValue {
  username: string
  expiresAt: number | null
  authEnabled: boolean
  authReady: boolean
  setupRequired: boolean
  setupTokenRequired: boolean
  isAuthenticated: boolean
  refreshAuthBootstrap: () => Promise<AuthBootstrapResponse | null>
  setup: (payload: { username: string; password: string; setupToken?: string }) => Promise<void>
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
  logoutAll: () => Promise<void>
  changePassword: (currentPassword: string, newPassword: string) => Promise<void>
  authError: string
  authConnectionError: string
  clearAuthError: () => void
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

interface StoredAuth {
  username: string
  expiresAt: number | null
}

const readStoredAuth = (): StoredAuth => {
  localStorage.removeItem('auth_token')
  localStorage.removeItem('token_expires_at')
  const expiresAt = Number(localStorage.getItem('auth_expires_at') || '0')
  if (!Number.isFinite(expiresAt) || expiresAt <= Math.floor(Date.now() / 1000)) {
    localStorage.removeItem('auth_expires_at')
    return { username: localStorage.getItem('auth_username') || 'root', expiresAt: null }
  }

  return {
    username: localStorage.getItem('auth_username') || 'root',
    expiresAt,
  }
}

export const AuthProvider: React.FC<AuthProviderProps> = ({ children }) => {
  const initialAuth = readStoredAuth()
  const [username, setUsername] = useState(initialAuth.username)
  const [expiresAt, setExpiresAt] = useState<number | null>(initialAuth.expiresAt)
  const [bootstrap, setBootstrap] = useState<AuthBootstrapResponse | null>(null)
  const [authReady, setAuthReady] = useState(false)
  const [authError, setAuthError] = useState('')
  const [authConnectionError, setAuthConnectionError] = useState('')

  const clearLocalAuth = useCallback(() => {
    localStorage.removeItem('auth_token')
    localStorage.removeItem('token_expires_at')
    localStorage.removeItem('auth_expires_at')
    setAuthError('')
    setAuthConnectionError('')
    setExpiresAt(null)
  }, [])

  const storeAuthResult = useCallback((result: AuthLoginResponse) => {
    localStorage.removeItem('auth_token')
    localStorage.removeItem('token_expires_at')
    localStorage.setItem('auth_expires_at', result.expires_at.toString())
    localStorage.setItem('auth_username', result.username || 'root')
    setUsername(result.username || 'root')
    setExpiresAt(result.expires_at)
  }, [])

  const refreshAuthBootstrap = useCallback(async () => {
    setAuthReady(false)
    setAuthConnectionError('')
    try {
      const nextBootstrap = await fetchAuthBootstrap()
      setBootstrap(nextBootstrap)
      setUsername((current) => nextBootstrap.username || current || 'root')
      return nextBootstrap
    } catch {
      setBootstrap(null)
      setAuthConnectionError('无法连接认证服务，请确认后端已启动并重试。')
      return null
    } finally {
      setAuthReady(true)
    }
  }, [])

  const clearAuthError = useCallback(() => {
    setAuthError('')
  }, [])

  const login = useCallback(async (nextUsername: string, password: string) => {
    setAuthError('')
    try {
      const result = await loginAuth({ username: nextUsername || username || 'root', password })
      storeAuthResult(result)
      await refreshAuthBootstrap()
    } catch (err) {
      const message = err instanceof Error ? err.message : '登录失败'
      setAuthError(message)
      throw err
    }
  }, [refreshAuthBootstrap, storeAuthResult, username])

  const setup = useCallback(async (payload: { username: string; password: string; setupToken?: string }) => {
    setAuthError('')
    try {
      const result = await setupAuth(payload)
      storeAuthResult(result)
      await refreshAuthBootstrap()
    } catch (err) {
      const message = err instanceof Error ? err.message : '初始化失败'
      setAuthError(message)
      throw err
    }
  }, [refreshAuthBootstrap, storeAuthResult])

  const logout = useCallback(async () => {
    if (expiresAt) {
      try {
        await logoutAuth()
      } catch {
        // 本地退出优先，远端会话可能已经过期。
      }
    }
    clearLocalAuth()
  }, [clearLocalAuth, expiresAt])

  const logoutAll = useCallback(async () => {
    try {
      await logoutAllAuth()
    } finally {
      clearLocalAuth()
    }
  }, [clearLocalAuth])

  const changePassword = useCallback(async (currentPassword: string, newPassword: string) => {
    setAuthError('')
    try {
      await changeAuthPassword({ currentPassword, newPassword })
      clearLocalAuth()
    } catch (err) {
      const message = err instanceof Error ? err.message : '修改密码失败'
      setAuthError(message)
      throw err
    }
  }, [clearLocalAuth])

  useEffect(() => {
    void refreshAuthBootstrap()
  }, [refreshAuthBootstrap])

  useEffect(() => {
    const handleUnauthorized = () => {
      clearLocalAuth()
    }
    const handleStorage = (event: StorageEvent) => {
      if (
        event.key === 'auth_expires_at'
        || event.key === 'auth_username'
        || event.key === 'auth_token'
        || event.key === 'token_expires_at'
      ) {
        const nextAuth = readStoredAuth()
        setUsername(nextAuth.username)
        setExpiresAt(nextAuth.expiresAt)
      }
    }

    window.addEventListener(AUTH_UNAUTHORIZED_EVENT, handleUnauthorized)
    window.addEventListener('storage', handleStorage)
    return () => {
      window.removeEventListener(AUTH_UNAUTHORIZED_EVENT, handleUnauthorized)
      window.removeEventListener('storage', handleStorage)
    }
  }, [clearLocalAuth])

  const value = useMemo<AuthContextValue>(
    () => ({
      username,
      expiresAt,
      authEnabled: bootstrap?.auth_enabled ?? false,
      authReady,
      setupRequired: bootstrap?.setup_required ?? false,
      setupTokenRequired: bootstrap?.setup_token_required ?? false,
      isAuthenticated: !!expiresAt && expiresAt > Math.floor(Date.now() / 1000),
      refreshAuthBootstrap,
      setup,
      login,
      logout,
      logoutAll,
      changePassword,
      authError,
      authConnectionError,
      clearAuthError,
    }),
    [
      authError,
      authConnectionError,
      authReady,
      bootstrap?.auth_enabled,
      bootstrap?.setup_required,
      bootstrap?.setup_token_required,
      changePassword,
      clearAuthError,
      expiresAt,
      login,
      logout,
      logoutAll,
      refreshAuthBootstrap,
      setup,
      username,
    ],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
