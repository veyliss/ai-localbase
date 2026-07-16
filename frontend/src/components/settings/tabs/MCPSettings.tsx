import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { MCPConfig } from '../../../App'
import { useModalFocusTrap } from '../../../hooks/useModalFocusTrap'
import {
  createAuthAPIKey,
  fetchAuthAPIKeys,
  revokeAuthAPIKey,
  type AuthAPIKeyInfo,
} from '../../../services/api'
import AppIcon from '../../common/AppIcon'
import ConfirmDialog from '../../common/ConfirmDialog'

interface MCPSettingsProps {
  config: MCPConfig
  onCopyMcpToken: () => Promise<void>
  onResetMcpToken: () => Promise<void>
  onPendingTokenChange: (pending: boolean) => void
}

interface ScopeOption {
  value: string
  label: string
  description: string
}

const apiKeyScopeGroups: Array<{ label: string; options: ScopeOption[] }> = [
  {
    label: '接口与知识库',
    options: [
      {
        value: 'openai:chat',
        label: '聊天接口',
        description: '/v1/chat/completions',
      },
      {
        value: 'knowledge:read',
        label: '读取知识库',
        description: '预留给知识库读取 API',
      },
      {
        value: 'knowledge:write',
        label: '写入知识库',
        description: '预留给知识库变更 API',
      },
      {
        value: 'config:read',
        label: '读取配置',
        description: '预留给配置读取 API',
      },
    ],
  },
  {
    label: 'MCP 工具',
    options: [
      {
        value: 'mcp:read',
        label: '读取',
        description: '工具发现、检索、列表和只读查询',
      },
      {
        value: 'mcp:write',
        label: '写入',
        description: '创建知识库、保存会话和重建索引',
      },
      {
        value: 'mcp:upload',
        label: '上传',
        description: '上传文档和注册暂存文件',
      },
      {
        value: 'mcp:eval',
        label: '评估',
        description: '生成评估数据集',
      },
      {
        value: 'mcp:danger',
        label: '危险操作',
        description: '删除知识库、文档或会话',
      },
      {
        value: 'mcp:admin',
        label: '管理',
        description: '允许调用全部 MCP 工具',
      },
    ],
  },
]

const apiKeyScopeOptions = apiKeyScopeGroups.flatMap((group) => group.options)
const defaultAPIKeyScopes = ['openai:chat']

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

const formatAPIKeyScopes = (scopes: string[] = []) => {
  if (scopes.length === 0) return '未设置权限'
  return scopes.map((scope) => (
    apiKeyScopeOptions.find((option) => option.value === scope)?.label || scope
  )).join(' / ')
}

const MCPSettings: React.FC<MCPSettingsProps> = ({
  config,
  onCopyMcpToken,
  onResetMcpToken,
  onPendingTokenChange,
}) => {
  const [apiKeys, setApiKeys] = useState<AuthAPIKeyInfo[]>([])
  const [keyName, setKeyName] = useState('')
  const [selectedScopes, setSelectedScopes] = useState<string[]>(defaultAPIKeyScopes)
  const [createdToken, setCreatedToken] = useState('')
  const [loadingKeys, setLoadingKeys] = useState(true)
  const [busyAction, setBusyAction] = useState('')
  const [accessFeedback, setAccessFeedback] = useState('')
  const [accessError, setAccessError] = useState('')
  const [tokenFeedback, setTokenFeedback] = useState('')
  const [templateFeedback, setTemplateFeedback] = useState('')
  const [isMcpTokenVisible, setIsMcpTokenVisible] = useState(false)
  const [selectedTemplateId, setSelectedTemplateId] = useState('cherry-studio')
  const [copiedTemplateId, setCopiedTemplateId] = useState('')
  const [apiKeyToRevoke, setApiKeyToRevoke] = useState<AuthAPIKeyInfo | null>(null)
  const createdTokenBackdropRef = useRef<HTMLDivElement | null>(null)
  const createdTokenCopyButtonRef = useRef<HTMLButtonElement | null>(null)

  useModalFocusTrap(createdTokenBackdropRef, {
    enabled: Boolean(createdToken),
    initialFocusRef: createdTokenCopyButtonRef,
  })

  const mcpEndpoint = useMemo(() => {
    const origin = typeof window === 'undefined' ? 'http://localhost:3000' : window.location.origin
    return `${origin}${config.basePath || '/mcp'}`
  }, [config.basePath])

  const templates = useMemo(() => {
    const apiKeyPlaceholder = '<API_KEY_WITH_MCP_SCOPE>'
    const authHeader = `Bearer ${apiKeyPlaceholder}`

    return [
      {
        id: 'cherry-studio',
        name: 'Cherry Studio',
        description: 'Streamable HTTP 服务配置',
        scopes: ['mcp:read', 'mcp:upload', 'mcp:eval'],
        content: JSON.stringify(
          {
            name: 'AI LocalBase',
            type: 'streamable-http',
            url: mcpEndpoint,
            headers: {
              Authorization: authHeader,
            },
          },
          null,
          2,
        ),
      },
      {
        id: 'claude-desktop',
        name: 'Claude Desktop',
        description: '桌面端 HTTP MCP 配置',
        scopes: ['mcp:read', 'mcp:eval'],
        content: JSON.stringify(
          {
            mcpServers: {
              'ai-localbase': {
                type: 'http',
                url: mcpEndpoint,
                headers: {
                  Authorization: authHeader,
                },
              },
            },
          },
          null,
          2,
        ),
      },
      {
        id: 'cursor-http',
        name: 'Cursor / 通用 HTTP',
        description: '通用 JSON-RPC HTTP 配置',
        scopes: ['mcp:read', 'mcp:write', 'mcp:upload', 'mcp:eval'],
        content: JSON.stringify(
          {
            server: 'ai-localbase',
            transport: 'http',
            endpoint: mcpEndpoint,
            headers: {
              Authorization: authHeader,
            },
          },
          null,
          2,
        ),
      },
    ]
  }, [mcpEndpoint])

  const selectedTemplate = useMemo(
    () => templates.find((template) => template.id === selectedTemplateId) ?? templates[0],
    [selectedTemplateId, templates],
  )
  const activeAPIKeys = useMemo(
    () => apiKeys.filter((apiKey) => !apiKey.revokedAt),
    [apiKeys],
  )
  const deploymentWarnings = config.deploymentWarnings ?? []

  const loadAPIKeys = useCallback(async () => {
    setLoadingKeys(true)
    setAccessError('')
    try {
      setApiKeys(await fetchAuthAPIKeys())
    } catch (error) {
      setAccessError(error instanceof Error ? error.message : 'API Key 加载失败')
    } finally {
      setLoadingKeys(false)
    }
  }, [])

  useEffect(() => {
    void loadAPIKeys()
  }, [loadAPIKeys])

  useEffect(() => {
    onPendingTokenChange(Boolean(createdToken))
  }, [createdToken, onPendingTokenChange])

  useEffect(() => {
    if (!createdToken) return undefined

    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', handleBeforeUnload)
    return () => window.removeEventListener('beforeunload', handleBeforeUnload)
  }, [createdToken])

  const handleCreateAPIKey = async (event: React.FormEvent) => {
    event.preventDefault()
    setAccessFeedback('')
    setAccessError('')
    if (createdToken) {
      setAccessError('请先确认已经安全保存当前 API Key')
      return
    }
    if (!keyName.trim()) {
      setAccessError('请输入可识别的 API Key 名称')
      return
    }
    if (selectedScopes.length === 0) {
      setAccessError('至少选择一个 API Key 权限')
      return
    }

    setBusyAction('create-key')
    try {
      const created = await createAuthAPIKey({ name: keyName.trim(), scopes: selectedScopes })
      setCreatedToken(created.token)
      setKeyName('')
      setAccessFeedback('API Key 已创建，请立即复制并确认保存')
      await loadAPIKeys()
    } catch (error) {
      setAccessError(error instanceof Error ? error.message : '创建 API Key 失败')
    } finally {
      setBusyAction('')
    }
  }

  const handleCopyCreatedToken = async () => {
    if (!createdToken) return
    try {
      await navigator.clipboard.writeText(createdToken)
      setAccessFeedback('API Key 已复制到剪贴板')
      setAccessError('')
    } catch {
      setAccessError('复制失败，请手动选择密钥内容')
    }
  }

  const handleConfirmCreatedTokenSaved = () => {
    setCreatedToken('')
    setAccessFeedback('API Key 已确认保存')
    setAccessError('')
  }

  const handleToggleScope = (scope: string) => {
    setSelectedScopes((current) => {
      if (current.includes(scope)) {
        return current.filter((item) => item !== scope)
      }
      return [...current, scope]
    })
  }

  const handleRevokeAPIKey = async (id: string) => {
    setBusyAction(id)
    setAccessFeedback('')
    setAccessError('')
    try {
      await revokeAuthAPIKey(id)
      setAccessFeedback('API Key 已撤销')
      await loadAPIKeys()
    } catch (error) {
      setAccessError(error instanceof Error ? error.message : '撤销 API Key 失败')
    } finally {
      setBusyAction('')
    }
  }

  const handleCopyToken = async () => {
    try {
      await onCopyMcpToken()
      setTokenFeedback('旧版 Token 已复制')
    } catch {
      setTokenFeedback('复制失败')
    }
  }

  const handleResetToken = async () => {
    try {
      await onResetMcpToken()
      setTokenFeedback('旧版 Token 已重置')
    } catch {
      setTokenFeedback('重置失败')
    }
  }

  const handleCopyTemplate = async () => {
    if (typeof navigator === 'undefined' || !navigator.clipboard) {
      setTemplateFeedback('当前环境不支持复制')
      return
    }

    try {
      await navigator.clipboard.writeText(selectedTemplate.content)
      setCopiedTemplateId(selectedTemplate.id)
      setTemplateFeedback(`${selectedTemplate.name} 模板已复制`)
    } catch {
      setTemplateFeedback('复制失败')
    }
  }

  return (
    <>
      <div className="settings-tab-content settings-access-content">
      <section className="settings-setting-section">
        <div className="settings-setting-section-header">
          <div>
            <h3>MCP 服务</h3>
            <p>确认服务地址和认证模式，再为具体客户端创建最小权限 API Key。</p>
          </div>
          <span className={`settings-status-pill ${config.enabled ? 'enabled' : 'disabled'}`}>
            {config.enabled ? '已启用' : '未启用'}
          </span>
        </div>

        {deploymentWarnings.length > 0 && (
          <div className="settings-security-warning-panel" role="status" aria-live="polite">
            <div className="settings-security-warning-copy">
              <strong>部署提醒</strong>
              <span>{deploymentWarnings.join('；')}</span>
            </div>
            <div className="settings-security-warning-meta">
              <span className="settings-status-pill warning">需处理</span>
              <small>{config.recommendedAuthMode === 'api_key_scopes' ? '建议使用 API Key Scope，危险工具使用 confirmNonce' : '请检查认证配置'}</small>
            </div>
          </div>
        )}

        <div className="settings-readonly-grid settings-access-summary">
          <div className="settings-readonly-field">
            <span>服务地址</span>
            <strong title={mcpEndpoint}>{mcpEndpoint}</strong>
          </div>
          <div className="settings-readonly-field">
            <span>认证方式</span>
            <strong>Authorization Bearer</strong>
            <small>使用带明确 Scope 的 API Key</small>
          </div>
          <div className="settings-readonly-field">
            <span>有效 API Key</span>
            <strong>{loadingKeys ? '读取中' : activeAPIKeys.length}</strong>
            <small>已撤销密钥不会计入</small>
          </div>
        </div>
      </section>

      <section className="settings-setting-section">
        <div className="settings-setting-section-header">
          <div>
            <h3>API Key</h3>
            <p>为每个外部客户端单独命名并分配权限，密钥只在创建后显示一次。</p>
          </div>
          <button
            aria-label="刷新 API Key"
            className="settings-action-btn settings-icon-action"
            onClick={() => void loadAPIKeys()}
            title="刷新 API Key"
            type="button"
          >
            <AppIcon name="refresh" size={16} />
          </button>
        </div>

        {(loadingKeys || accessFeedback || accessError) && (
          <div className="settings-security-notices">
            {loadingKeys && <div className="settings-inline-note">正在读取 API Key...</div>}
            {accessFeedback && <div className="settings-inline-note success">{accessFeedback}</div>}
            {accessError && <div className="settings-inline-note error">{accessError}</div>}
          </div>
        )}

        <form className="settings-access-key-form" onSubmit={handleCreateAPIKey}>
          <div className="settings-form-group settings-form-group-full">
            <label className="settings-form-label" htmlFor="settings-api-key-name">密钥名称</label>
            <input
              autoComplete="off"
              id="settings-api-key-name"
              onChange={(event) => setKeyName(event.target.value)}
              placeholder="例如：cherry-studio-mac"
              value={keyName}
            />
            <small>使用客户端和设备名称，便于之后识别与撤销。</small>
          </div>

          <fieldset className="settings-scope-fieldset">
            <legend>
              <span>权限范围</span>
              <small>已选择 {selectedScopes.length} 项</small>
            </legend>
            <div className="settings-scope-groups">
              {apiKeyScopeGroups.map((group) => (
                <div className="settings-scope-group" key={group.label}>
                  <strong className="settings-scope-group-label">{group.label}</strong>
                  <div className="settings-scope-options">
                    {group.options.map((option) => (
                      <label className="settings-scope-option" key={option.value}>
                        <input
                          checked={selectedScopes.includes(option.value)}
                          onChange={() => handleToggleScope(option.value)}
                          type="checkbox"
                        />
                        <span>
                          <strong>{option.label}</strong>
                          <small>{option.description}</small>
                        </span>
                      </label>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </fieldset>

          <div className="settings-access-key-actions">
            <span>按实际用途选择最小权限；危险操作与管理权限应单独创建密钥。</span>
            <button
              className="settings-action-btn settings-action-btn-primary"
              disabled={busyAction === 'create-key' || Boolean(createdToken)}
              type="submit"
            >
              <AppIcon name={busyAction === 'create-key' ? 'loader' : 'plus'} size={16} />
              {busyAction === 'create-key' ? '创建中...' : '创建 API Key'}
            </button>
          </div>
        </form>

        <div className="settings-security-list settings-api-key-list">
          {!loadingKeys && apiKeys.length === 0 && <div className="settings-empty-row">暂无 API Key</div>}
          {apiKeys.map((apiKey) => (
            <div className="settings-security-row" key={apiKey.id}>
              <div>
                <strong>{apiKey.name}</strong>
                <span>{apiKey.prefix}... · {formatAPIKeyScopes(apiKey.scopes)} · 创建 {formatDateTime(apiKey.createdAt)}</span>
              </div>
              <div className="settings-security-row-meta">
                <span>{apiKey.revokedAt ? '已撤销' : apiKey.lastUsedAt ? `最近使用 ${formatDateTime(apiKey.lastUsedAt)}` : '未使用'}</span>
                {!apiKey.revokedAt && (
                  <button
                    className="settings-action-btn settings-action-btn-danger"
                    disabled={busyAction === apiKey.id}
                    onClick={() => setApiKeyToRevoke(apiKey)}
                    type="button"
                  >
                    撤销
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      </section>

      <section className="settings-setting-section">
        <div className="settings-setting-section-header">
          <div>
            <h3>客户端模板</h3>
            <p>选择客户端后复制对应配置，密钥位置保留为安全占位符。</p>
          </div>
        </div>

        <div className="settings-template-switcher" role="group" aria-label="MCP 客户端模板">
          {templates.map((template) => (
            <button
              aria-pressed={selectedTemplate.id === template.id}
              className={selectedTemplate.id === template.id ? 'active' : ''}
              key={template.id}
              onClick={() => {
                setSelectedTemplateId(template.id)
                setTemplateFeedback('')
              }}
              type="button"
            >
              {template.name}
            </button>
          ))}
        </div>

        <article
          aria-live="polite"
          className="settings-mcp-template settings-mcp-template-single"
        >
          <div className="settings-mcp-template-head">
            <div>
              <h4>{selectedTemplate.name}</h4>
              <p>{selectedTemplate.description}</p>
            </div>
            <button className="settings-action-btn" onClick={() => void handleCopyTemplate()} type="button">
              <AppIcon name={copiedTemplateId === selectedTemplate.id ? 'check' : 'copy'} size={16} />
              {copiedTemplateId === selectedTemplate.id ? '已复制' : '复制模板'}
            </button>
          </div>
          <div className="settings-mcp-scope-row" aria-label="模板所需权限">
            {selectedTemplate.scopes.map((scope) => (
              <span key={scope}>{scope}</span>
            ))}
          </div>
          <pre className="settings-mcp-template-code">
            <code>{selectedTemplate.content}</code>
          </pre>
        </article>
        {templateFeedback && <small className="settings-feedback">{templateFeedback}</small>}
      </section>

      <section className="settings-setting-section settings-legacy-section">
        <details className="settings-legacy-access">
          <summary>
            <span>
              <strong>旧版兼容</strong>
              <small>仅用于迁移旧 MCP Bearer Token，新接入无需配置。</small>
            </span>
            <span className={`settings-status-pill ${config.legacyTokenEnabled ? 'warning' : 'disabled'}`}>
              {config.legacyTokenEnabled ? '迁移已启用' : '已关闭'}
            </span>
            <AppIcon className="settings-legacy-chevron" name="chevronDown" size={17} />
          </summary>
          <div className="settings-legacy-access-body">
            <div className="settings-form-group settings-form-group-full">
              <label className="settings-form-label">迁移 Token</label>
              <div className="settings-token-wrapper">
                <input
                  className="settings-token-input"
                  placeholder={config.tokenConfigured ? 'Token 已生成，明文不在配置接口返回' : 'Token 未生成'}
                  readOnly
                  type={isMcpTokenVisible ? 'text' : 'password'}
                  value={config.token}
                />
                <div className="settings-token-actions">
                  <button
                    aria-label={isMcpTokenVisible ? '隐藏旧版 Token' : '显示旧版 Token'}
                    className="settings-action-btn settings-icon-action"
                    disabled={!config.token}
                    onClick={() => setIsMcpTokenVisible((visible) => !visible)}
                    title={isMcpTokenVisible ? '隐藏旧版 Token' : '显示旧版 Token'}
                    type="button"
                  >
                    <AppIcon name={isMcpTokenVisible ? 'eyeOff' : 'eye'} size={16} />
                  </button>
                  <button className="settings-action-btn" disabled={!config.token} onClick={() => void handleCopyToken()} type="button">
                    <AppIcon name="copy" size={16} />
                    复制
                  </button>
                  <button
                    className="settings-action-btn"
                    disabled={!config.legacyTokenEnabled}
                    onClick={() => void handleResetToken()}
                    type="button"
                  >
                    <AppIcon name="refresh" size={16} />
                    重置
                  </button>
                </div>
              </div>
              <small>旧版 Token 等价 MCP 全权限；完成迁移后应关闭兼容模式。</small>
              {tokenFeedback && <small className="settings-feedback">{tokenFeedback}</small>}
            </div>
          </div>
        </details>
      </section>

      {createdToken && (
        <div className="settings-created-token-backdrop" ref={createdTokenBackdropRef}>
          <div
            aria-describedby="settings-created-token-description"
            aria-labelledby="settings-created-token-title"
            aria-modal="true"
            className="settings-created-token-dialog"
            role="alertdialog"
          >
            <div className="settings-created-token-dialog-icon" aria-hidden="true">
              <AppIcon name="key" size={22} />
            </div>
            <span>只显示一次</span>
            <h4 id="settings-created-token-title">保存新的 API Key</h4>
            <p id="settings-created-token-description">请立即复制到密码管理器或安全配置中。确认保存后，密钥会从页面清除且无法再次查看。</p>
            <code>{createdToken}</code>
            <div className="settings-created-token-dialog-actions">
              <button
                className="settings-action-btn"
                onClick={() => void handleCopyCreatedToken()}
                ref={createdTokenCopyButtonRef}
                type="button"
              >
                <AppIcon name="copy" size={16} />
                复制密钥
              </button>
              <button className="settings-action-btn settings-action-btn-primary" onClick={handleConfirmCreatedTokenSaved} type="button">
                <AppIcon name="check" size={16} />
                已安全保存
              </button>
            </div>
          </div>
        </div>
      )}
    </div>

      <ConfirmDialog
        cancelText="保留密钥"
        confirmText="确认撤销"
        message={apiKeyToRevoke ? `撤销 ${apiKeyToRevoke.name} 后，使用该密钥的客户端会立即失去访问权限，且无法恢复。` : ''}
        onCancel={() => setApiKeyToRevoke(null)}
        onConfirm={() => {
          if (!apiKeyToRevoke) return
          const apiKeyId = apiKeyToRevoke.id
          setApiKeyToRevoke(null)
          void handleRevokeAPIKey(apiKeyId)
        }}
        open={apiKeyToRevoke !== null}
        title="撤销 API Key？"
      />
    </>
  )
}

export default MCPSettings
