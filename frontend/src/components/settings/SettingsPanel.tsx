import React, { useCallback, useEffect, useMemo, useState } from 'react'
import type { AppConfig, ChatConfig, ChatModeSettings, EmbeddingConfig, RetrievalConfig } from '../../App'
import AppIcon, { type AppIconName } from '../common/AppIcon'
import ConfirmDialog from '../common/ConfirmDialog'
import GeneralSettings from './tabs/GeneralSettings'
import AISettings from './tabs/AISettings'
import RetrievalSettings from './tabs/RetrievalSettings'
import MCPSettings from './tabs/MCPSettings'
import SystemSettings from './tabs/SystemSettings'

type SettingsTab = 'overview' | 'models' | 'retrieval' | 'access' | 'account'

interface SettingsPanelProps {
  config: AppConfig
  onClose: () => void
  chatModeSettings: ChatModeSettings
  onSave: (config: AppConfig, thinkModel: string) => Promise<AppConfig>
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
  { id: 'overview', label: '概览', description: '运行状态与当前配置', icon: 'settings' },
  { id: 'models', label: '模型', description: '聊天、推理与 Embedding', icon: 'brain' },
  { id: 'retrieval', label: '检索', description: '召回质量与上下文规模', icon: 'database' },
  { id: 'access', label: '接入与密钥', description: 'MCP、API Key 与客户端', icon: 'key' },
  { id: 'account', label: '账户与安全', description: '密码、会话与安全记录', icon: 'shield' },
]

const getTabButtonId = (tabId: SettingsTab) => `settings-tab-${tabId}`
const getTabPanelId = (tabId: SettingsTab) => `settings-panel-${tabId}`

const getConfigFingerprint = (config: AppConfig, thinkModel: string) =>
  JSON.stringify([config, thinkModel])

const validateConfig = (config: AppConfig) => {
  const requiredFields = [
    [config.chat.baseUrl, '请填写聊天模型 Base URL'],
    [config.chat.model, '请填写聊天模型名称'],
    [config.embedding.baseUrl, '请填写 Embedding 模型 Base URL'],
    [config.embedding.model, '请填写 Embedding 模型名称'],
  ] as const

  const missingField = requiredFields.find(([value]) => !value.trim())
  if (missingField) {
    return missingField[1]
  }

  if (config.chat.temperature < 0 || config.chat.temperature > 1) {
    return 'Temperature 需要在 0 到 1 之间'
  }
  if (config.chat.contextMessageLimit < 1 || config.chat.contextMessageLimit > 100) {
    return '上下文消息数量需要在 1 到 100 之间'
  }
  if (config.retrieval.topKDocument < 1 || config.retrieval.topKDocument > 30) {
    return '文档 TopK 需要在 1 到 30 之间'
  }
  if (config.retrieval.candidateTopKDocument < 1 || config.retrieval.candidateTopKDocument > 80) {
    return '文档候选 TopK 需要在 1 到 80 之间'
  }
  if (config.retrieval.topKKnowledgeBase < 1 || config.retrieval.topKKnowledgeBase > 40) {
    return '知识库 TopK 需要在 1 到 40 之间'
  }
  if (config.retrieval.candidateTopKAllDocs < 1 || config.retrieval.candidateTopKAllDocs > 120) {
    return '知识库候选 TopK 需要在 1 到 120 之间'
  }
  if (config.retrieval.maxChunksPerDocument < 1 || config.retrieval.maxChunksPerDocument > 10) {
    return '每文档片段数需要在 1 到 10 之间'
  }
  if (config.retrieval.maxContextChars < 800 || config.retrieval.maxContextChars > 20000) {
    return '上下文字符数需要在 800 到 20000 之间'
  }
  if (config.retrieval.queryRewriteMaxVariants < 1 || config.retrieval.queryRewriteMaxVariants > 5) {
    return '问题改写数量需要在 1 到 5 之间'
  }
  if (config.retrieval.candidateTopKDocument < config.retrieval.topKDocument) {
    return '文档候选 TopK 不能小于文档 TopK'
  }
  if (config.retrieval.candidateTopKAllDocs < config.retrieval.topKKnowledgeBase) {
    return '知识库候选 TopK 不能小于知识库 TopK'
  }

  return null
}

const SettingsPanel: React.FC<SettingsPanelProps> = ({
  config,
  onClose,
  chatModeSettings,
  onSave,
  onCopyMcpToken,
  onResetMcpToken,
  onLogout,
}) => {
  const [activeTab, setActiveTab] = useState<SettingsTab>('overview')
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false)
  const [baselineConfig, setBaselineConfig] = useState(config)
  const [draftConfig, setDraftConfig] = useState(config)
  const [baselineThinkModel, setBaselineThinkModel] = useState(chatModeSettings.thinkModel)
  const [draftThinkModel, setDraftThinkModel] = useState(chatModeSettings.thinkModel)
  const [isSaving, setIsSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [showDiscardDialog, setShowDiscardDialog] = useState(false)
  const [hasPendingAccessToken, setHasPendingAccessToken] = useState(false)
  const [pendingCredentialDestination, setPendingCredentialDestination] = useState<SettingsTab | 'close' | null>(null)

  const isDirty = useMemo(
    () => getConfigFingerprint(draftConfig, draftThinkModel) !==
      getConfigFingerprint(baselineConfig, baselineThinkModel),
    [baselineConfig, baselineThinkModel, draftConfig, draftThinkModel],
  )

  useEffect(() => {
    if (isDirty || isSaving) {
      return
    }
    setBaselineConfig(config)
    setDraftConfig(config)
    setBaselineThinkModel(chatModeSettings.thinkModel)
    setDraftThinkModel(chatModeSettings.thinkModel)
  }, [chatModeSettings.thinkModel, config, isDirty, isSaving])

  const markDraftChanged = useCallback(() => {
    setSaveError(null)
  }, [])

  const handleChatConfigChange = useCallback(<K extends keyof ChatConfig>(
    key: K,
    value: ChatConfig[K],
  ) => {
    markDraftChanged()
    setDraftConfig((current) => {
      const nextChat = { ...current.chat, [key]: value }
      if (key === 'apiKey') {
        nextChat.clearApiKey = false
      } else if (key === 'clearApiKey' && value === true) {
        nextChat.apiKey = ''
      }
      return { ...current, chat: nextChat }
    })
  }, [markDraftChanged])

  const handleEmbeddingConfigChange = useCallback(<K extends keyof EmbeddingConfig>(
    key: K,
    value: EmbeddingConfig[K],
  ) => {
    markDraftChanged()
    setDraftConfig((current) => {
      const nextEmbedding = { ...current.embedding, [key]: value }
      if (key === 'apiKey') {
        nextEmbedding.clearApiKey = false
      } else if (key === 'clearApiKey' && value === true) {
        nextEmbedding.apiKey = ''
      }
      return { ...current, embedding: nextEmbedding }
    })
  }, [markDraftChanged])

  const handleRetrievalConfigChange = useCallback(<K extends keyof RetrievalConfig>(
    key: K,
    value: RetrievalConfig[K],
  ) => {
    markDraftChanged()
    setDraftConfig((current) => ({
      ...current,
      retrieval: { ...current.retrieval, [key]: value },
    }))
  }, [markDraftChanged])

  const handleRetrievalConfigPatch = useCallback((patch: Partial<RetrievalConfig>) => {
    markDraftChanged()
    setDraftConfig((current) => ({
      ...current,
      retrieval: { ...current.retrieval, ...patch },
    }))
  }, [markDraftChanged])

  const handleThinkModelChange = useCallback((value: string) => {
    markDraftChanged()
    setDraftThinkModel(value)
  }, [markDraftChanged])

  const handleDiscard = useCallback(() => {
    setDraftConfig(baselineConfig)
    setDraftThinkModel(baselineThinkModel)
    setSaveError(null)
  }, [baselineConfig, baselineThinkModel])

  const handleSave = useCallback(async () => {
    const validationError = validateConfig(draftConfig)
    if (validationError) {
      setSaveError(validationError)
      return
    }

    setIsSaving(true)
    setSaveError(null)
    try {
      const normalizedThinkModel = draftThinkModel.trim()
      const savedConfig = await onSave(draftConfig, normalizedThinkModel)
      setBaselineConfig(savedConfig)
      setDraftConfig(savedConfig)
      setBaselineThinkModel(normalizedThinkModel)
      setDraftThinkModel(normalizedThinkModel)
    } catch (error) {
      setSaveError(error instanceof Error ? error.message : '设置保存失败，请稍后重试')
    } finally {
      setIsSaving(false)
    }
  }, [draftConfig, draftThinkModel, onSave])

  const handleClose = useCallback(() => {
    if (hasPendingAccessToken) {
      setPendingCredentialDestination('close')
      return
    }
    if (isDirty) {
      setShowDiscardDialog(true)
      return
    }
    onClose()
  }, [hasPendingAccessToken, isDirty, onClose])

  const activeTabIndex = useMemo(
    () => navItems.findIndex((item) => item.id === activeTab),
    [activeTab],
  )

  const activeNavItem = useMemo(
    () => navItems[activeTabIndex] ?? navItems[0],
    [activeTabIndex],
  )

  const handleTabChange = useCallback((nextTab: SettingsTab) => {
    if (activeTab === 'access' && hasPendingAccessToken && nextTab !== 'access') {
      setPendingCredentialDestination(nextTab)
      return
    }
    setActiveTab((currentTab) => (currentTab === nextTab ? currentTab : nextTab))
    setMobileDetailOpen(true)
  }, [activeTab, hasPendingAccessToken])

  const handleCredentialNavigationConfirm = useCallback(() => {
    const destination = pendingCredentialDestination
    setPendingCredentialDestination(null)
    setHasPendingAccessToken(false)

    if (destination === 'close') {
      if (isDirty) {
        setShowDiscardDialog(true)
      } else {
        onClose()
      }
      return
    }

    if (destination) {
      setActiveTab(destination)
      setMobileDetailOpen(true)
    }
  }, [isDirty, onClose, pendingCredentialDestination])

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
      case 'overview':
        return <GeneralSettings config={draftConfig} />
      case 'models':
        return (
          <AISettings
            config={draftConfig}
            onChatConfigChange={handleChatConfigChange}
            onEmbeddingConfigChange={handleEmbeddingConfigChange}
            chatModeSettings={{ ...chatModeSettings, thinkModel: draftThinkModel }}
            onThinkModelChange={handleThinkModelChange}
          />
        )
      case 'retrieval':
        return (
          <RetrievalSettings
            config={draftConfig.retrieval}
            onRetrievalConfigChange={handleRetrievalConfigChange}
            onRetrievalConfigPatch={handleRetrievalConfigPatch}
          />
        )
      case 'access':
        return (
          <MCPSettings
            config={draftConfig.mcp}
            onCopyMcpToken={onCopyMcpToken}
            onPendingTokenChange={setHasPendingAccessToken}
            onResetMcpToken={onResetMcpToken}
          />
        )
      case 'account':
        return <SystemSettings onLogout={onLogout} />
      default:
        return null
    }
  }, [
    activeTab,
    chatModeSettings.fastModel,
    draftConfig,
    draftThinkModel,
    handleChatConfigChange,
    handleEmbeddingConfigChange,
    handleRetrievalConfigChange,
    handleRetrievalConfigPatch,
    handleThinkModelChange,
    onCopyMcpToken,
    onLogout,
    onResetMcpToken,
    setHasPendingAccessToken,
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
          onClick={handleClose}
          aria-label="返回聊天"
          title="返回聊天"
          type="button"
        >
          <AppIcon name="chevronLeft" size={20} />
        </button>
      </header>

      <div className={`settings-layout ${mobileDetailOpen ? 'mobile-detail-open' : ''}`}>
        <aside className="settings-sidebar" aria-label="设置分类">
          <div className="settings-mobile-list-header">
            <strong>设置分类</strong>
            <span>选择一项进行查看和修改</span>
          </div>
          <nav
            aria-label="设置分类"
            className="settings-nav"
            onKeyDown={handleNavKeyDown}
          >
            {navItems.map((item) => {
              const isActive = activeTab === item.id

              return (
                <button
                  aria-controls={getTabPanelId(item.id)}
                  aria-current={isActive ? 'page' : undefined}
                  className={`settings-nav-item ${isActive ? 'active' : ''}`}
                  id={getTabButtonId(item.id)}
                  key={item.id}
                  onClick={() => handleTabChange(item.id)}
                  tabIndex={0}
                  type="button"
                >
                  <span className="settings-nav-icon" aria-hidden="true">
                    <AppIcon name={item.icon} size={18} />
                  </span>
                  <span className="settings-nav-text">
                    <span className="settings-nav-label">{item.label}</span>
                    <span className="settings-nav-description">{item.description}</span>
                  </span>
                  <span className="settings-nav-trailing" aria-hidden="true">
                    <AppIcon name="chevronRight" size={16} />
                  </span>
                </button>
              )
            })}
          </nav>
        </aside>

        <main className="settings-main">
          <header className="settings-main-header">
            <button
              aria-label="返回设置分类"
              className="settings-mobile-back"
              onClick={() => setMobileDetailOpen(false)}
              title="返回设置分类"
              type="button"
            >
              <AppIcon name="chevronLeft" size={18} />
            </button>
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
            role="region"
            tabIndex={0}
          >
            {activePanel}
          </section>

          <footer className={`settings-save-bar ${saveError ? 'has-error' : ''}`}>
            <div className="settings-save-state" role="status" aria-live="polite">
              <span className="settings-save-state-icon" aria-hidden="true">
                <AppIcon
                  className={isSaving ? 'settings-save-spinner' : undefined}
                  name={saveError ? 'alert' : isSaving ? 'loader' : 'check'}
                  size={16}
                />
              </span>
              <span>
                <strong>
                  {saveError
                    ? '保存失败'
                    : isSaving
                      ? '正在保存'
                      : isDirty
                        ? '有未保存的更改'
                        : '所有更改已保存'}
                </strong>
                {saveError && <small>{saveError}</small>}
              </span>
            </div>
            <div className="settings-save-actions">
              <button
                className="settings-action-btn"
                disabled={!isDirty || isSaving}
                onClick={handleDiscard}
                type="button"
              >
                放弃更改
              </button>
              <button
                className="settings-action-btn settings-action-btn-primary"
                disabled={!isDirty || isSaving}
                onClick={() => void handleSave()}
                type="button"
              >
                保存
              </button>
            </div>
          </footer>
        </main>
      </div>

      <ConfirmDialog
        cancelText="继续编辑"
        confirmText="放弃并返回"
        message="当前设置尚未保存，返回后这些更改会丢失。"
        onCancel={() => setShowDiscardDialog(false)}
        onConfirm={() => {
          setShowDiscardDialog(false)
          onClose()
        }}
        open={showDiscardDialog}
        title="放弃未保存的更改？"
      />

      <ConfirmDialog
        cancelText="留在当前页"
        confirmText="已保存并继续"
        message="新创建的 API Key 只显示一次。请确认已经复制并安全保存，再离开接入与密钥页面。"
        onCancel={() => setPendingCredentialDestination(null)}
        onConfirm={handleCredentialNavigationConfirm}
        open={pendingCredentialDestination !== null}
        title="确认已经保存 API Key？"
      />
    </section>
  )
}

export default SettingsPanel
