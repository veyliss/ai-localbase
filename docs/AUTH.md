# 认证系统使用文档

## 环境变量配置

```bash
# 启用认证（默认 false）
ENABLE_AUTH=true

# 设置登录密码（必填，当 ENABLE_AUTH=true）
AUTH_PASSWORD=your-secure-password

# JWT 签名密钥（可选，建议生产环境设置）
JWT_SECRET=your-random-secret-key-min-32-chars
```

## API 端点

### 1. 登录
```bash
POST /api/auth/login
Content-Type: application/json

{
  "password": "your-secure-password"
}
```

**响应**:
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires_at": 1717776000
}
```

### 2. 验证状态
```bash
GET /api/auth/status
Authorization: Bearer <token>
```

**响应**:
```json
{
  "authenticated": true,
  "username": "admin"
}
```

## 前端集成

### 1. 登录流程
```typescript
// 登录
const response = await fetch('/api/auth/login', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ password: 'your-password' })
})

const { token, expires_at } = await response.json()

// 保存 token
localStorage.setItem('auth_token', token)
localStorage.setItem('token_expires_at', expires_at.toString())
```

### 2. API 请求携带 Token
```typescript
// 每次请求自动添加 Authorization 头
const token = localStorage.getItem('auth_token')

fetch('/api/knowledge-bases', {
  headers: {
    'Authorization': `Bearer ${token}`
  }
})
```

### 3. 拦截器示例 (Axios)
```typescript
import axios from 'axios'

axios.interceptors.request.use(config => {
  const token = localStorage.getItem('auth_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

axios.interceptors.response.use(
  response => response,
  error => {
    if (error.response?.status === 401) {
      // Token 过期或无效，跳转登录页
      localStorage.removeItem('auth_token')
      window.location.href = '/login'
    }
    return Promise.reject(error)
  }
)
```

## 开发模式

```bash
# 关闭认证（开发/本地使用）
ENABLE_AUTH=false

# 或者不设置 ENABLE_AUTH（默认 false）
```

## 安全建议

1. **生产环境必须使用 HTTPS**，否则 Token 可能被中间人窃取
2. **密码强度**: 建议 16+ 字符，包含大小写字母、数字、特殊符号
3. **JWT_SECRET**: 建议使用随机 32+ 字符，可通过以下命令生成:
   ```bash
   openssl rand -base64 32
   ```
4. **Token 有效期**: 默认 7 天，可在 `auth_handler.go` 修改
5. **前端存储**: Token 存储在 `localStorage`，注意防范 XSS 攻击

## 错误处理

| HTTP 状态码 | 错误信息 | 说明 |
|------------|---------|------|
| 400 | invalid request | 请求格式错误 |
| 401 | invalid password | 密码错误 |
| 401 | missing authorization header | 缺少 Authorization 头 |
| 401 | invalid authorization format | 格式错误（应为 `Bearer <token>`）|
| 401 | invalid signature | Token 签名无效 |
| 401 | token expired | Token 已过期 |

## 示例：完整登录页面

```typescript
import { useState } from 'react'

export default function LoginPage() {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    try {
      const response = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password })
      })

      if (!response.ok) {
        const data = await response.json()
        throw new Error(data.error || 'Login failed')
      }

      const { token, expires_at } = await response.json()
      localStorage.setItem('auth_token', token)
      localStorage.setItem('token_expires_at', expires_at.toString())
      
      // 跳转到主页
      window.location.href = '/'
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed')
    }
  }

  return (
    <div className="login-container">
      <form onSubmit={handleLogin}>
        <h1>AI LocalBase Login</h1>
        <input
          type="password"
          placeholder="Enter password"
          value={password}
          onChange={e => setPassword(e.target.value)}
          required
        />
        <button type="submit">Login</button>
        {error && <div className="error">{error}</div>}
      </form>
    </div>
  )
}
```
