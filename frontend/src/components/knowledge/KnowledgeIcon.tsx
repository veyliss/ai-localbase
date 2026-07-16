import React from 'react'
import AppIcon, { type AppIconName } from '../common/AppIcon'

type KnowledgeIconName = 'chevronDown' | 'chevronUp' | 'file' | 'folderPlus' | 'plus' | 'trash' | 'upload' | 'x'

interface KnowledgeIconProps {
  name: KnowledgeIconName
}

const KnowledgeIcon: React.FC<KnowledgeIconProps> = ({ name }) => {
  const iconMap: Record<KnowledgeIconName, AppIconName> = {
    chevronDown: 'chevronDown',
    chevronUp: 'chevronUp',
    file: 'file',
    folderPlus: 'folderPlus',
    plus: 'plus',
    trash: 'trash',
    upload: 'upload',
    x: 'x',
  }

  return <AppIcon name={iconMap[name]} />
}

export default KnowledgeIcon
