import React, { useCallback, useMemo, useState } from 'react'
import type { AppConfig, ChatConfig, ChatModeSettings, EmbeddingConfig, RetrievalConfig } from '../../App'
import AppIcon, { type AppIconName } from '../common/AppIcon'
import GeneralSettings from './tabs/GeneralSettings'
import AISettings from './tabs/AISettings'
import RetrievalSettings from './tabs/RetrievalSettings'
import MCPSettings from './tabs/MCPSettings'
import SystemSettings from './tabs/SystemSettings'
import AboutSettings from './tabs/AboutSettings'

type SettingsTab = 'general' | 'ai' | 'retrieval' | 'mcp' | 'system' | 'about'

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
  icon: AppIconName
}

const navItems: SettingsNavItem[] = [
  { id: 'system', label: '账户管理', description: '会话、密码与访问密钥', icon: 'user' },
  { id: 'general', label: '系统设置', description: '当前模型、检索与运行状态', icon: 'settings' },
  { id: 'mcp', label: '系统授权', description: 'MCP 工具调用与访问令牌', icon: 'shield' },
  { id: 'ai', label: '模型', description: '接口与推理参数', icon: 'box' },
  { id: 'retrieval', label: '检索策略', description: '召回、重排与上下文规模', icon: 'database' },
  { id: 'about', label: '关于', description: '版本、发布与项目资源', icon: 'info' },
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
    <section className="settings-workspace app-workspace" aria-labelledby="settings-workspace-title">
      <header className="workspace-page-header settings-workspace-header">
        <div>
          <span className="workspace-page-kicker">AI LocalBase</span>
          <h2 id="settings-workspace-title">设置</h2>
          <p>管理本地模型、检索策略、系统授权与账户安全。</p>
        </div>
        <button
          className="workspace-page-back"
          onClick={onClose}
          aria-label="返回聊天"
          title="返回聊天"
          type="button"
        >
          <AppIcon name="chevronLeft" size={20} />
        </button>
      </header>

      <div className="settings-layout">
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
                    <AppIcon name={item.icon} size={18} />
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
              <h3>{activeNavItem.label}</h3>
              <p className="settings-main-visible-description">{activeNavItem.description}</p>
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
    </section>
  )
}

export default SettingsPanel
