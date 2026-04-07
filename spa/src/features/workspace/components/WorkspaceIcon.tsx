import { Suspense, lazy, useMemo } from 'react'
import type { Icon } from '@phosphor-icons/react'
import type { IconWeight } from '../../../types/tab'
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
  weight?: IconWeight
  className?: string
}

export function WorkspaceIcon({ icon, name, size, weight = 'bold', className }: Props) {
  const fallbackChar = name.charAt(0) || '?'
  const phosphorName = icon && isPhosphorName(icon) ? icon : null
  const LazyIcon = useMemo(() => phosphorName ? getLazyIcon(phosphorName) : null, [phosphorName])
  const textStyle = { fontSize: size * 0.75 }

  if (!icon) {
    return <span className={className} style={textStyle}>{fallbackChar}</span>
  }

  if (!phosphorName) {
    return <span className={className} style={textStyle}>{icon}</span>
  }

  if (!LazyIcon) {
    return <span className={className} style={textStyle}>{fallbackChar}</span>
  }

  return (
    <Suspense fallback={<span className={className} style={textStyle}>{fallbackChar}</span>}>
      <LazyIcon size={size} weight={weight} className={className} />
    </Suspense>
  )
}
