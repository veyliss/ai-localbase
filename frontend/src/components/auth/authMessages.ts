export type AuthMode = 'login' | 'setup'

export const formatAuthError = (message: string, mode: AuthMode) => {
  const normalized = message.trim().toLowerCase()

  if (!normalized) return mode === 'setup' ? '初始化失败，请重试。' : '登录失败，请重试。'
  if (normalized.includes('invalid username or password') || normalized.includes('invalid credentials')) {
    return '用户名或密码不正确。'
  }
  if (normalized.includes('too many') || normalized.includes('rate limit') || normalized.includes('429')) {
    return '登录尝试过于频繁，请稍后再试。'
  }
  if (normalized.includes('setup token') && (normalized.includes('invalid') || normalized.includes('incorrect'))) {
    return '初始化 Token 无效，请检查后重试。'
  }
  if (normalized.includes('setup token') && normalized.includes('required')) {
    return '请输入初始化 Token。'
  }
  if (normalized.includes('failed to fetch') || normalized.includes('network')) {
    return '无法连接认证服务，请检查后端状态。'
  }

  return message
}
