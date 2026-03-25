// spa/src/components/ThemeInjector.tsx
import { useEffect } from 'react'
import { useThemeStore } from '../stores/useThemeStore'
import { tokensToCss } from '../lib/theme-tokens'

export function ThemeInjector() {
  const activeThemeId = useThemeStore((s) => s.activeThemeId)
  const customThemes = useThemeStore((s) => s.customThemes)
  const custom = customThemes[activeThemeId]

  useEffect(() => {
    if (!custom) return
    const style = document.createElement('style')
    style.dataset.themeId = custom.id
    style.textContent = `[data-theme="${custom.id}"] { ${tokensToCss(custom.tokens)} }`
    document.head.appendChild(style)
    return () => { style.remove() }
  }, [custom])

  return null
}
