import { useEffect, useState } from 'react'
import type { IconWeight } from '../../../types/tab'
import { getIconPath, isWeightLoaded, prefetchWeight } from '../lib/icon-path-cache'
import { renderPaths } from '../lib/render-paths'

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

const RETRY_DELAY = 3000

export function WorkspaceIcon({ icon, name, size, weight = 'bold', className }: Props) {
  const fallbackChar = name.charAt(0) || '?'
  const textStyle = { fontSize: size * 0.75 }
  const phosphorName = icon && isPhosphorName(icon) ? icon : null

  // Hooks must be called before any conditional returns (Rules of Hooks)
  const [, setTick] = useState(0)
  const [retryCount, setRetryCount] = useState(0)

  useEffect(() => {
    if (phosphorName && !isWeightLoaded(weight)) {
      let cancelled = false
      prefetchWeight(weight)
        .then(() => { if (!cancelled) setTick((t) => t + 1) })
        .catch(() => {
          if (!cancelled) {
            // Schedule a retry after delay
            const timer = setTimeout(() => {
              if (!cancelled) setRetryCount((c) => c + 1)
            }, RETRY_DELAY)
            return () => clearTimeout(timer)
          }
        })
      return () => { cancelled = true }
    }
  }, [phosphorName, weight, retryCount])

  if (!phosphorName) {
    return <span className={className} style={textStyle}>{icon || fallbackChar}</span>
  }

  const pathData = getIconPath(phosphorName, weight)
  if (!pathData) {
    return <span className={className} style={textStyle}>{fallbackChar}</span>
  }

  return (
    <svg width={size} height={size} viewBox="0 0 256 256" fill="currentColor" className={className}>
      {renderPaths(pathData)}
    </svg>
  )
}
