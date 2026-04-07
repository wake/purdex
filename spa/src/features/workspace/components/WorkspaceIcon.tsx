import { Suspense, lazy, useMemo } from 'react'
import type { Icon } from '@phosphor-icons/react'
import { iconLoaders } from '../generated/icon-loader'

/** Cache of resolved lazy components to avoid re-creating on every render */
const lazyCache = new Map<string, React.LazyExoticComponent<Icon>>()

function getLazyIcon(name: string): React.LazyExoticComponent<Icon> | null {
  if (lazyCache.has(name)) return lazyCache.get(name)!
  const loader = iconLoaders[name]
  if (!loader) return null
  const LazyComponent = lazy(() => loader().then((comp) => ({ default: comp })))
  lazyCache.set(name, LazyComponent)
  return LazyComponent
}

function isPhosphorName(icon: string): boolean {
  return icon.length > 1 && /^[A-Z]/.test(icon)
}

interface Props {
  icon: string | undefined
  name: string
  size: number
  className?: string
}

export function WorkspaceIcon({ icon, name, size, className }: Props) {
  const fallbackChar = name.charAt(0) || '?'

  // No icon → first char
  if (!icon) {
    return <span className={className} style={{ fontSize: size * 0.85 }}>{fallbackChar}</span>
  }

  // Legacy single-char or emoji → render as text
  if (!isPhosphorName(icon)) {
    return <span className={className} style={{ fontSize: size * 0.85 }}>{icon}</span>
  }

  // Phosphor icon name → lazy load (useMemo to satisfy react-hooks/static-components)
  // eslint-disable-next-line react-hooks/rules-of-hooks -- icon is stable per render path (early returns above guarantee it's a Phosphor name)
  const LazyIcon = useMemo(() => getLazyIcon(icon), [icon])
  if (!LazyIcon) {
    return <span className={className} style={{ fontSize: size * 0.85 }}>{fallbackChar}</span>
  }

  return (
    <Suspense fallback={<span className={className} style={{ fontSize: size * 0.85 }}>{fallbackChar}</span>}>
      <LazyIcon size={size} className={className} />
    </Suspense>
  )
}
