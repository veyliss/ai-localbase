import React, { useMemo, useState } from 'react'
import type { MCPConfig } from '../../../App'

interface MCPSettingsProps {
  config: MCPConfig
  onCopyMcpToken: () => Promise<void>
  onResetMcpToken: () => Promise<void>
}

const MCPSettings: React.FC<MCPSettingsProps> = ({ config, onCopyMcpToken, onResetMcpToken }) => {
  const [tokenFeedback, setTokenFeedback] = useState('')
  const [templateFeedback, setTemplateFeedback] = useState('')
  const [isMcpTokenVisible, setIsMcpTokenVisible] = useState(false)
  const [copiedTemplateId, setCopiedTemplateId] = useState('')

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
        description: 'HTTP MCP 服务配置',
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
        description: 'HTTP MCP 客户端配置',
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
        description: '通用 JSON-RPC MCP 配置',
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

  const handleCopyToken = async () => {
    try {
      await onCopyMcpToken()
      setTokenFeedback('Token 已复制')
    } catch {
      setTokenFeedback('复制失败')
    }
  }

  const handleResetToken = async () => {
    try {
      await onResetMcpToken()
      setTokenFeedback('Token 已重置')
    } catch {
      setTokenFeedback('重置失败')
    }
  }

  const handleCopyTemplate = async (templateId: string, content: string) => {
    if (typeof navigator === 'undefined' || !navigator.clipboard) {
      setTemplateFeedback('当前环境不支持复制')
      return
    }

    try {
      await navigator.clipboard.writeText(content)
      setCopiedTemplateId(templateId)
      setTemplateFeedback('模板已复制')
    } catch {
      setTemplateFeedback('复制失败')
    }
  }

  return (
    <div className="settings-tab-content">
      <section className="settings-card">
        <div className="settings-card-header">
          <div className="settings-card-header-copy">
            <h3>MCP 配置</h3>
            <p>MCP 默认关闭；服务器启用时必须同时开启认证。旧 Token 已废弃，仅用于迁移。</p>
          </div>
          <span className={`settings-status-pill ${config.enabled ? 'enabled' : 'disabled'}`}>
            {config.enabled ? '已启用' : '未启用'}
          </span>
        </div>
        <div className="settings-card-body">
          <div className="settings-readonly-grid">
            <div className="settings-readonly-field">
              <span>状态</span>
              <strong>{config.enabled ? '已启用' : '未启用'}</strong>
            </div>
            <div className="settings-readonly-field">
              <span>Base Path</span>
              <strong>{config.basePath || '未配置'}</strong>
            </div>
            <div className="settings-readonly-field">
              <span>旧 Token 迁移</span>
              <strong>{config.legacyTokenEnabled ? '已启用' : '已关闭'}</strong>
            </div>
          </div>

          <div className="settings-form-grid settings-form-grid-dense">
            <div className="settings-form-group settings-form-group-full">
              <label className="settings-form-label">迁移 Token</label>
              <div className="settings-token-wrapper">
                <input
                  type={isMcpTokenVisible ? 'text' : 'password'}
                  value={config.token}
                  placeholder={config.tokenConfigured ? 'Token 已生成，明文不在配置接口返回' : 'Token 未生成'}
                  readOnly
                  className="settings-token-input"
                />
                <div className="settings-token-actions">
                  <button
                    className="settings-action-btn"
                    onClick={() => setIsMcpTokenVisible((v) => !v)}
                    title={isMcpTokenVisible ? '隐藏 Token' : '显示 Token'}
                    disabled={!config.token}
                  >
                    {isMcpTokenVisible ? '隐藏' : '显示'}
                  </button>
                  <button className="settings-action-btn" onClick={() => void handleCopyToken()} disabled={!config.token}>
                    复制
                  </button>
                  <button
                    className="settings-action-btn"
                    onClick={() => void handleResetToken()}
                    disabled={!config.legacyTokenEnabled}
                  >
                    重置
                  </button>
                </div>
              </div>
              <small>旧版 MCP Bearer Token 等价 MCP 全权限，已废弃且默认关闭；新接入请使用带 MCP scope 的 API Key。</small>
              {tokenFeedback && <small className="settings-feedback">{tokenFeedback}</small>}
            </div>
          </div>
        </div>
      </section>

      <section className="settings-card">
        <div className="settings-card-header">
          <div className="settings-card-header-copy">
            <h3>客户端模板</h3>
            <p>模板默认使用 API Key，并按客户端场景标注所需 MCP scope。</p>
          </div>
        </div>
        <div className="settings-card-body">
          <div className="settings-readonly-grid">
            <div className="settings-readonly-field">
              <span>MCP Endpoint</span>
              <strong>{mcpEndpoint}</strong>
            </div>
            <div className="settings-readonly-field">
              <span>认证方式</span>
              <strong>Authorization Bearer</strong>
              <small>使用带 MCP scope 的 API Key</small>
            </div>
          </div>

          <div className="settings-mcp-template-grid">
            {templates.map((template) => (
              <article className="settings-mcp-template" key={template.id}>
                <div className="settings-mcp-template-head">
                  <div>
                    <h4>{template.name}</h4>
                    <p>{template.description}</p>
                  </div>
                  <button
                    className="settings-action-btn"
                    onClick={() => void handleCopyTemplate(template.id, template.content)}
                    type="button"
                  >
                    {copiedTemplateId === template.id ? '已复制' : '复制'}
                  </button>
                </div>
                <div className="settings-mcp-scope-row">
                  {template.scopes.map((scope) => (
                    <span key={scope}>{scope}</span>
                  ))}
                </div>
                <pre className="settings-mcp-template-code">
                  <code>{template.content}</code>
                </pre>
              </article>
            ))}
          </div>
          {templateFeedback && <small className="settings-feedback">{templateFeedback}</small>}
        </div>
      </section>
    </div>
  )
}

export default MCPSettings
