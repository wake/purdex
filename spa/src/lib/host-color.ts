import type { Tab } from '../types/tab'
import type { HostConfig } from '../stores/useHostStore'
import { collectTmuxSessionHostIds } from './infer-workspace-host-id'

/** Preset host colors, legible on both dark and light themes. */
export const HOST_COLOR_PRESETS: readonly string[] = [
  '#ef4444',
  '#f97316',
  '#eab308',
  '#22c55e',
  '#14b8a6',
  '#3b82f6',
  '#8b5cf6',
  '#ec4899',
]

const HOST_COLOR_RE = /^#[0-9a-f]{6}$/i

/** Strict `#rrggbb` check. Guards every value that reaches inline CSS. */
export function isValidHostColor(v: unknown): v is string {
  return typeof v === 'string' && HOST_COLOR_RE.test(v)
}

/** Trims, accepts `rrggbb` or `#rrggbb`, lowercases. Anything else → null. */
export function normalizeHostColor(v: string): string | null {
  const trimmed = v.trim()
  const withHash = trimmed.startsWith('#') ? trimmed : `#${trimmed}`
  return isValidHostColor(withHash) ? withHash.toLowerCase() : null
}

/** hostId of the first tmux-session pane in pre-order, or null. */
export function getTabHostId(tab: Tab): string | null {
  return collectTmuxSessionHostIds(tab.layout)[0] ?? null
}

/** Validated color of the tab's host, or null when unresolvable / invalid. */
export function resolveTabHostColor(tab: Tab, hosts: Record<string, HostConfig>): string | null {
  const hostId = getTabHostId(tab)
  if (!hostId) return null
  const color = (hosts[hostId] as { color?: unknown } | undefined)?.color
  return isValidHostColor(color) ? color : null
}
