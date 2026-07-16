import React from 'react'
import { APP_VERSION_LABEL, IS_RELEASE_BUILD } from '../../../utils/appInfo'

const repositoryUrl = 'https://github.com/veyliss/ai-localbase'
const releaseUrl = `${repositoryUrl}/releases`
const licenseUrl = `${repositoryUrl}/blob/main/LICENSE`

interface AboutSettingsProps {
  embedded?: boolean
}

const AboutSettings: React.FC<AboutSettingsProps> = ({ embedded = false }) => {
  const buildStatus = IS_RELEASE_BUILD ? 'Release 构建' : '本地开发'
  const projectName = 'AI LocalBase'

  return (
    <div className={embedded ? 'settings-about-page settings-about-embedded' : 'settings-tab-content settings-about-page'}>
      <section className="settings-setting-section">
        <div className="settings-setting-section-header">
          <div>
            <h3>关于</h3>
            <p>项目版本和常用链接。</p>
          </div>
        </div>
        <div className="settings-about-list">
          <div>
            <span>项目</span>
            <strong>{projectName}</strong>
          </div>
          <div>
            <span>版本</span>
            <strong>{APP_VERSION_LABEL}</strong>
          </div>
          <div>
            <span>构建</span>
            <strong>{buildStatus}</strong>
          </div>
          <div>
            <span>项目地址</span>
            <a href={repositoryUrl} target="_blank" rel="noreferrer">{repositoryUrl}</a>
          </div>
          <div>
            <span>发布页</span>
            <a href={releaseUrl} target="_blank" rel="noreferrer">{releaseUrl}</a>
          </div>
          <div>
            <span>许可证</span>
            <a href={licenseUrl} target="_blank" rel="noreferrer">LICENSE</a>
          </div>
        </div>
      </section>
    </div>
  )
}

export default AboutSettings
