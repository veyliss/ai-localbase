import React from 'react'

type KnowledgeIconName = 'chevronDown' | 'chevronUp' | 'file' | 'folderPlus' | 'plus' | 'trash' | 'upload' | 'x'

interface KnowledgeIconProps {
  name: KnowledgeIconName
}

const KnowledgeIcon: React.FC<KnowledgeIconProps> = ({ name }) => {
  const commonProps = {
    viewBox: '0 0 24 24',
    fill: 'none',
    'aria-hidden': true,
  }

  if (name === 'upload') {
    return (
      <svg {...commonProps}>
        <path d="M12 15.5V4.75M12 4.75L7.75 9M12 4.75L16.25 9" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M5 15.25V17.25C5 18.35 5.9 19.25 7 19.25H17C18.1 19.25 19 18.35 19 17.25V15.25" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    )
  }

  if (name === 'folderPlus') {
    return (
      <svg {...commonProps}>
        <path d="M4.75 7.25C4.75 6.15 5.65 5.25 6.75 5.25H10.15L12.15 7.25H17.25C18.35 7.25 19.25 8.15 19.25 9.25V17.25C19.25 18.35 18.35 19.25 17.25 19.25H6.75C5.65 19.25 4.75 18.35 4.75 17.25V7.25Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M12 10.75V15.75M9.5 13.25H14.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    )
  }

  if (name === 'file') {
    return (
      <svg {...commonProps}>
        <path d="M7 3.75H13.25L18 8.5V20.25H7V3.75Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M13 3.75V8.75H18" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M9.5 13H14.5M9.5 16H13" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
      </svg>
    )
  }

  if (name === 'trash') {
    return (
      <svg {...commonProps}>
        <path d="M6 7.25H18" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        <path d="M9.25 7.25V5.75C9.25 4.92 9.92 4.25 10.75 4.25H13.25C14.08 4.25 14.75 4.92 14.75 5.75V7.25" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        <path d="M8.25 9.75L8.8 18.25C8.86 19.1 9.56 19.75 10.41 19.75H13.59C14.44 19.75 15.14 19.1 15.2 18.25L15.75 9.75" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
      </svg>
    )
  }

  if (name === 'x') {
    return (
      <svg {...commonProps}>
        <path d="M6.5 6.5L17.5 17.5M17.5 6.5L6.5 17.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      </svg>
    )
  }

  if (name === 'chevronUp' || name === 'chevronDown') {
    return (
      <svg {...commonProps}>
        <path
          d={name === 'chevronUp' ? 'M7 14L12 9L17 14' : 'M7 10L12 15L17 10'}
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    )
  }

  return (
    <svg {...commonProps}>
      <path d="M12 5V19M5 12H19" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  )
}

export default KnowledgeIcon
