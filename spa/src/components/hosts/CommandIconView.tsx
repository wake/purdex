import { useEffect, useState } from 'react'
import { Terminal } from '@phosphor-icons/react'
import type { AgentIconComponent } from '../../lib/agent-icons'
import { AGENT_ICON_VALUES, agentIconComponent } from '../../lib/command-icons'
import type { AgentIconValue, CommandIcon } from '../../lib/host-config-api'
import { getIconPath, isWeightLoaded, prefetchWeight } from '../../features/workspace/lib/icon-path-cache'
import { renderPaths } from '../../features/workspace/lib/render-paths'

const WEIGHT = 'regular'

// Resolved once at module level so render only indexes a static map.
const AGENT_ICONS = Object.fromEntries(AGENT_ICON_VALUES.map((v) => [v, agentIconComponent(v)])) as Record<
  AgentIconValue,
  AgentIconComponent
>

/**
 * A stored command icon. Phosphor icons render from the lazily fetched
 * `/icons/regular.json` path data (same pipeline as workspace icons), so no
 * icon component set enters a JS chunk. Unknown names or data still loading
 * render the `Terminal` fallback.
 */
export function CommandIconView({ icon, size = 16, className }: { icon: CommandIcon; size?: number; className?: string }) {
  const [, setTick] = useState(0)
  const needsPaths = icon.kind === 'phosphor' && !isWeightLoaded(WEIGHT)

  useEffect(() => {
    if (!needsPaths) return
    let cancelled = false
    prefetchWeight(WEIGHT).then(() => { if (!cancelled) setTick((n) => n + 1) }).catch(() => {})
    return () => { cancelled = true }
  }, [needsPaths])

  const cls = `inline-flex ${className ?? ''}`.trim()

  if (icon.kind === 'agent') {
    const Agent = AGENT_ICONS[icon.value]
    return (
      <span data-testid="command-icon" data-kind="agent" data-value={icon.value} className={cls}>
        <Agent size={size} />
      </span>
    )
  }

  const path = getIconPath(icon.value, WEIGHT)
  return (
    <span
      data-testid="command-icon"
      data-kind="phosphor"
      data-value={icon.value}
      {...(path ? {} : { 'data-fallback': 'true' })}
      className={cls}
    >
      {path ? (
        <svg width={size} height={size} viewBox="0 0 256 256" fill="currentColor" aria-hidden="true">
          {renderPaths(path)}
        </svg>
      ) : (
        <Terminal size={size} aria-hidden="true" />
      )}
    </span>
  )
}
