import { useEffect, useState } from 'react'
import type { IconWeight } from '../../../types/tab'
import { getIconPath, isWeightLoaded, prefetchWeight, type PathData } from '../lib/icon-path-cache'

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
  const textStyle = { fontSize: size * 0.75 }
  const phosphorName = icon && isPhosphorName(icon) ? icon : null

  // Hooks must be called before any conditional returns (Rules of Hooks)
  const [, setTick] = useState(0)
  useEffect(() => {
    if (phosphorName && !isWeightLoaded(weight)) {
      prefetchWeight(weight).then(() => setTick((t) => t + 1)).catch(() => {})
    }
  }, [phosphorName, weight])

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

function renderPaths(data: PathData) {
  if (typeof data === 'string') return <path d={data} />
  return data.map((p, i) =>
    typeof p === 'string'
      ? <path key={i} d={p} />
      : <path key={i} d={p.d} opacity={p.o} />,
  )
}
