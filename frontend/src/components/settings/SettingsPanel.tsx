import React, { useCallback, useMemo, useState } from 'react'
import type { AppConfig, ChatConfig, ChatModeSettings, EmbeddingConfig, RetrievalConfig } from '../../App'
import GeneralSettings from './tabs/GeneralSettings'
import AISettings from './tabs/AISettings'
import RetrievalSettings from './tabs/RetrievalSettings'
import MCPSettings from './tabs/MCPSettings'
import SystemSettings from './tabs/SystemSettings'
import AboutSettings from './tabs/AboutSettings'

type SettingsTab = 'general' | 'ai' | 'retrieval' | 'mcp' | 'system' | 'about'
type SettingsNavIconName = 'user' | 'settings' | 'shield' | 'cube' | 'database' | 'info'

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
  icon: SettingsNavIconName
}

const navItems: SettingsNavItem[] = [
  { id: 'system', label: '账户管理', description: '会话、密码与访问密钥', icon: 'user' },
  { id: 'general', label: '系统设置', description: '当前模型、检索与运行状态', icon: 'settings' },
  { id: 'mcp', label: '系统授权', description: 'MCP 工具调用与访问令牌', icon: 'shield' },
  { id: 'ai', label: '模型', description: '模型、接口与推理参数', icon: 'cube' },
  { id: 'retrieval', label: '检索策略', description: '召回、重排与上下文规模', icon: 'database' },
  { id: 'about', label: '关于', description: '版本、发布与项目资源', icon: 'info' },
]

const getTabButtonId = (tabId: SettingsTab) => `settings-tab-${tabId}`
const getTabPanelId = (tabId: SettingsTab) => `settings-panel-${tabId}`

const SettingsNavIcon: React.FC<{ name: SettingsNavIconName }> = ({ name }) => {
  switch (name) {
    case 'user':
      return (
        <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <circle cx="12" cy="8" r="4" stroke="currentColor" strokeWidth="1.8" />
          <path d="M4.8 20C6 16.8 8.4 15.2 12 15.2C15.6 15.2 18 16.8 19.2 20" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        </svg>
      )
    case 'settings':
      return (
        <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M12 8.4A3.6 3.6 0 1 0 12 15.6A3.6 3.6 0 0 0 12 8.4Z" stroke="currentColor" strokeWidth="1.8" />
          <path d="M19.1 13.6C19.2 13.1 19.2 12.6 19.2 12C19.2 11.4 19.2 10.9 19.1 10.4L21 8.9L19 5.5L16.7 6.4C15.8 5.7 14.9 5.2 13.8 4.9L13.5 2.5H10.5L10.2 4.9C9.1 5.2 8.2 5.7 7.3 6.4L5 5.5L3 8.9L4.9 10.4C4.8 10.9 4.8 11.4 4.8 12C4.8 12.6 4.8 13.1 4.9 13.6L3 15.1L5 18.5L7.3 17.6C8.2 18.3 9.1 18.8 10.2 19.1L10.5 21.5H13.5L13.8 19.1C14.9 18.8 15.8 18.3 16.7 17.6L19 18.5L21 15.1L19.1 13.6Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        </svg>
      )
    case 'shield':
      return (
        <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M12 3L19 6V11.4C19 15.8 16.1 19.2 12 21C7.9 19.2 5 15.8 5 11.4V6L12 3Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
          <path d="M9 12L11.1 14.1L15.4 9.8" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      )
    case 'cube':
      return (
        <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M12 3.5L19.5 7.7V16.3L12 20.5L4.5 16.3V7.7L12 3.5Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
          <path d="M4.8 8L12 12.1L19.2 8M12 20.2V12.1" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        </svg>
      )
    case 'database':
      return (
        <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <ellipse cx="12" cy="6" rx="7" ry="3.2" stroke="currentColor" strokeWidth="1.8" />
          <path d="M5 6V12C5 13.8 8.1 15.2 12 15.2C15.9 15.2 19 13.8 19 12V6" stroke="currentColor" strokeWidth="1.8" />
          <path d="M5 12V18C5 19.8 8.1 21.2 12 21.2C15.9 21.2 19 19.8 19 18V12" stroke="currentColor" strokeWidth="1.8" />
        </svg>
      )
    case 'info':
      return (
        <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <circle cx="12" cy="12" r="8.5" stroke="currentColor" strokeWidth="1.8" />
          <path d="M12 10.8V16.2" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
          <path d="M12 7.6H12.01" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" />
        </svg>
      )
    default:
      return null
  }
}

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
        return <SystemSettings config={config} onLogout={onLogout} />
      case 'about':
        return <AboutSettings />
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
        <button className="settings-modal-close" onClick={onClose} aria-label="关闭设置" type="button">
          <svg viewBox="0 0 24 24" fill="none" width="28" height="28" aria-hidden="true">
            <path d="M18 6L6 18M6 6l12 12" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round"/>
          </svg>
        </button>

        <aside className="settings-sidebar" aria-label="设置分类">
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
                  <span className="settings-nav-icon" aria-hidden="true">
                    <SettingsNavIcon name={item.icon} />
                  </span>
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
              <h3 id="settings-dialog-title">设置</h3>
              <p id="settings-dialog-description" className="settings-main-description">
                {activeNavItem.label} · {activeNavItem.description}
              </p>
            </div>
          </header>

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
