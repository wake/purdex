// spa/src/components/BackgroundSymbol.tsx — the corner symbol of a tab's agent icon for background work (spec N6).
//
// One small, static symbol at the top-left of the agent icon (or of the dot in the `dot` style): TreeStructure for a
// workflow, Eye for a monitor, Clock for a scheduled wake-up. The colour is the icon's (`currentColor`); background
// SHELL commands draw nothing (the daemon never sends them). The caller decides WHERE it is drawn and that it is not
// drawn at all when the lights are off.
import { Clock, Eye, TreeStructure } from '@phosphor-icons/react'
import type { BackgroundKind } from '../stores/useAgentStore'
import { useI18nStore } from '../stores/useI18nStore'

const ICONS = { workflow: TreeStructure, monitor: Eye, schedule: Clock } as const

interface Props {
  kind: BackgroundKind
  /** Offsets from the top-left of the positioned box it sits in (px). */
  top?: number
  left?: number
  size?: number
}

export function BackgroundSymbol({ kind, top = -3, left = -3, size = 8 }: Props) {
  const t = useI18nStore((s) => s.t)
  const Icon = ICONS[kind]
  const label = t(`tab.background.${kind}`)
  return (
    <span
      data-testid="tab-background-symbol"
      data-kind={kind}
      role="img"
      aria-label={label}
      title={label}
      className="absolute z-20 inline-flex"
      style={{ top, left, lineHeight: 0, color: 'currentColor' }}
    >
      <Icon size={size} weight="bold" />
    </span>
  )
}
