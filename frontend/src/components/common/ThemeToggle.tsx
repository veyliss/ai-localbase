import { useEffect, useState } from 'react'
import AppIcon from './AppIcon'

const ThemeToggle = () => {
  const [theme, setTheme] = useState<'light' | 'dark'>(() => {
    if (typeof window === 'undefined') return 'light'
    const saved = localStorage.getItem('theme')
    if (saved === 'dark' || saved === 'light') return saved
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  })

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
    localStorage.setItem('theme', theme)
  }, [theme])

  const toggleTheme = () => {
    setTheme(prev => prev === 'light' ? 'dark' : 'light')
  }

  return (
    <button
      type="button"
      className="theme-toggle"
      onClick={toggleTheme}
      aria-label={`切换到${theme === 'light' ? '深色' : '浅色'}模式`}
      title={`切换到${theme === 'light' ? '深色' : '浅色'}模式`}
    >
      <AppIcon name={theme === 'light' ? 'moon' : 'sun'} size={20} />
    </button>
  )
}

export default ThemeToggle
