import React, { useCallback, useMemo, useState } from 'react'
import type { AppConfig, ChatConfig, ChatModeSettings, EmbeddingConfig, RetrievalConfig } from '../../App'
import GeneralSettings from './tabs/GeneralSettings'
import AISettings from './tabs/AISettings'
import RetrievalSettings from './tabs/RetrievalSettings'
import MCPSettings from './tabs/MCPSettings'
import SystemSettings from './tabs/SystemSettings'

type SettingsTab = 'general' | 'ai' | 'retrieval' | 'mcp' | 'system'

interface SettingsPanelProps {
  config: AppConfig
  onClose: () => void
  onChatConfigChange: <K extends keyof ChatConfig>(key: K, value: ChatConfig[K]) => void
  onEmbeddingConfigChange: <K extends keyof EmbeddingConfig>(
    key: K,
    value: EmbeddingConfig[K],
  ) => void
  onRetrievalConfigChange: <K extends keyof RetrievalConfig>(
    key: K,
    value: RetrievalConfig[K],
  ) => void
  chatModeSettings: ChatModeSettings
  onThinkModelChange: (value: string) => void
  onCopyMcpToken: () => Promise<void>
  onResetMcpToken: () => Promise<void>
  onLogout: () => void | Promise<void>
}

interface SettingsNavItem {
  id: SettingsTab
  label: string
  description: string
  icon: string
}

const navItems: SettingsNavItem[] = [
  { id: 'general', label: '通用设置', description: '应用状态与基础信息', icon: '总' },
  { id: 'ai', label: 'AI 配置', description: '模型、接口与推理参数', icon: 'AI' },
  { id: 'retrieval', label: '检索策略', description: '召回、重排与上下文规模', icon: '检' },
  { id: 'mcp', label: 'MCP 设置', description: '工具调用与访问令牌', icon: 'M' },
  { id: 'system', label: '账户与安全', description: '会话、密码与访问密钥', icon: '安' },
]

const getTabButtonId = (tabId: SettingsTab) => `settings-tab-${tabId}`
const getTabPanelId = (tabId: SettingsTab) => `settings-panel-${tabId}`

const SettingsPanel: React.FC<SettingsPanelProps> = ({
  config,
  onClose,
  onChatConfigChange,
  onEmbeddingConfigChange,
  onRetrievalConfigChange,
  chatModeSettings,
  onThinkModelChange,
  onCopyMcpToken,
  onResetMcpToken,
  onLogout,
}) => {
  const [activeTab, setActiveTab] = useState<SettingsTab>('general')

  const activeTabIndex = useMemo(
    () => navItems.findIndex((item) => item.id === activeTab),
    [activeTab],
  )

  const activeNavItem = useMemo(
    () => navItems[activeTabIndex] ?? navItems[0],
    [activeTabIndex],
  )

  const handleTabChange = useCallback((nextTab: SettingsTab) => {
    setActiveTab((currentTab) => (currentTab === nextTab ? currentTab : nextTab))
  }, [])

  const focusTab = useCallback((nextIndex: number) => {
    const nextItem = navItems[(nextIndex + navItems.length) % navItems.length]
    window.requestAnimationFrame(() => {
      document.getElementById(getTabButtonId(nextItem.id))?.focus()
    })
    handleTabChange(nextItem.id)
  }, [handleTabChange])

  const handleNavKeyDown = useCallback((event: React.KeyboardEvent<HTMLElement>) => {
    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowRight':
        event.preventDefault()
        focusTab(activeTabIndex + 1)
        break
      case 'ArrowUp':
      case 'ArrowLeft':
        event.preventDefault()
        focusTab(activeTabIndex - 1)
        break
      case 'Home':
        event.preventDefault()
        focusTab(0)
        break
      case 'End':
        event.preventDefault()
        focusTab(navItems.length - 1)
        break
      default:
        break
    }
  }, [activeTabIndex, focusTab])

  const handleDialogKeyDown = useCallback((event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') {
      event.stopPropagation()
      onClose()
    }
  }, [onClose])

  const activePanel = useMemo(() => {
    switch (activeTab) {
      case 'general':
        return <GeneralSettings config={config} />
      case 'ai':
        return (
          <AISettings
            config={config}
            onChatConfigChange={onChatConfigChange}
            onEmbeddingConfigChange={onEmbeddingConfigChange}
            chatModeSettings={chatModeSettings}
            onThinkModelChange={onThinkModelChange}
          />
        )
      case 'retrieval':
        return (
          <RetrievalSettings
            config={config.retrieval}
            onRetrievalConfigChange={onRetrievalConfigChange}
          />
        )
      case 'mcp':
        return (
          <MCPSettings
            config={config.mcp}
            onCopyMcpToken={onCopyMcpToken}
            onResetMcpToken={onResetMcpToken}
          />
        )
      case 'system':
        return <SystemSettings onLogout={onLogout} />
      default:
        return null
    }
  }, [
    activeTab,
    chatModeSettings,
    config,
    onChatConfigChange,
    onCopyMcpToken,
    onEmbeddingConfigChange,
    onLogout,
    onResetMcpToken,
    onRetrievalConfigChange,
    onThinkModelChange,
  ])

  return (
    <div className="settings-modal-backdrop" onClick={onClose}>
      <div
        aria-describedby="settings-dialog-description"
        aria-labelledby="settings-dialog-title"
        aria-modal="true"
        className="settings-modal settings-modal-settings"
        onClick={(event) => event.stopPropagation()}
        onKeyDown={handleDialogKeyDown}
        role="dialog"
      >
        <aside className="settings-sidebar" aria-label="设置分类">
          <div className="settings-sidebar-header">
            <div>
              <h2 id="settings-dialog-title">设置管理</h2>
              <p id="settings-dialog-description">调整模型、检索、MCP 与账户安全</p>
            </div>
            <button className="settings-sidebar-close" onClick={onClose} aria-label="关闭设置">
              <svg viewBox="0 0 24 24" fill="none" width="20" height="20" aria-hidden="true">
                <path d="M18 6L6 18M6 6l12 12" stroke="currentColor" strokeWidth="2" strokeLinecap="round"/>
              </svg>
            </button>
          </div>

          <nav
            aria-label="设置分类"
            className="settings-nav"
            onKeyDown={handleNavKeyDown}
            role="tablist"
          >
            {navItems.map((item) => {
              const isActive = activeTab === item.id

              return (
                <button
                  aria-controls={getTabPanelId(item.id)}
                  aria-selected={isActive}
                  className={`settings-nav-item ${isActive ? 'active' : ''}`}
                  id={getTabButtonId(item.id)}
                  key={item.id}
                  onClick={() => handleTabChange(item.id)}
                  role="tab"
                  tabIndex={isActive ? 0 : -1}
                  type="button"
                >
                  <span className="settings-nav-icon" aria-hidden="true">{item.icon}</span>
                  <span className="settings-nav-text">
                    <span className="settings-nav-label">{item.label}</span>
                    <span className="settings-nav-description">{item.description}</span>
                  </span>
                </button>
              )
            })}
          </nav>
        </aside>

        <main className="settings-main">
          <header className="settings-main-header">
            <div>
              <h3>{activeNavItem.label}</h3>
              <p>{activeNavItem.description}</p>
            </div>
          </header>

          <div className="settings-summary-bar" aria-label="当前配置摘要">
            <div className="settings-summary-item">
              <span>聊天模型</span>
              <strong>{config.chat.model || '未配置'}</strong>
            </div>
            <div className="settings-summary-item">
              <span>Embedding</span>
              <strong>{config.embedding.model || '未配置'}</strong>
            </div>
            <div className="settings-summary-item">
              <span>检索模式</span>
              <strong>{config.retrieval.defaultSearchMode === 'hybrid' ? '混合' : '向量'}</strong>
            </div>
            <div className="settings-summary-item">
              <span>MCP</span>
              <strong>{config.mcp.enabled ? '已启用' : '未启用'}</strong>
            </div>
          </div>

          <section
            aria-labelledby={getTabButtonId(activeTab)}
            className="settings-content-scroll"
            id={getTabPanelId(activeTab)}
            key={activeTab}
            role="tabpanel"
            tabIndex={0}
          >
            {activePanel}
          </section>
        </main>
      </div>
    </div>
  )
}

export default SettingsPanel
