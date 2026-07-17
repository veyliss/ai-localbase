import { describe, expect, it } from 'vitest'
import { formatAuthError } from './authMessages'

describe('formatAuthError', () => {
  it('translates invalid credential errors', () => {
    expect(formatAuthError('Invalid username or password.', 'login')).toBe('用户名或密码不正确。')
  })

  it('translates rate limit errors', () => {
    expect(formatAuthError('Too many login attempts', 'login')).toBe('登录尝试过于频繁，请稍后再试。')
  })

  it('provides a mode-specific fallback', () => {
    expect(formatAuthError('', 'setup')).toBe('初始化失败，请重试。')
  })
})
