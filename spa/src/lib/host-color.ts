import type { IconWeight, Tab } from '../types/tab'
import type { HostConfig } from '../stores/useHostStore'
import { collectTmuxSessionHostIds } from './infer-workspace-host-id'

/** Phosphor icon used for a host that has not picked one. */
export const DEFAULT_HOST_ICON = 'Desktop'

const ICON_WEIGHTS: readonly string[] = ['bold', 'regular', 'thin', 'light', 'fill', 'duotone']

/** Guards the `IconWeight` union for untrusted values (sync payload, persisted state). */
export function isIconWeight(v: unknown): v is IconWeight {
  return typeof v === 'string' && ICON_WEIGHTS.includes(v)
}

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

/**
 * Drops present-but-invalid identity keys (`color`, `icon`, `iconWeight`) from an
 * untrusted host config (sync payload, persisted state). Returns the same object
 * when every present key is valid.
 */
export function sanitizeHostConfig(host: HostConfig): HostConfig {
  const badColor = 'color' in host && !isValidHostColor(host.color)
  const badIcon = 'icon' in host && !(typeof host.icon === 'string' && host.icon.trim() !== '')
  const badWeight = 'iconWeight' in host && !isIconWeight(host.iconWeight)
  if (!badColor && !badIcon && !badWeight) return host

  const cleaned = { ...host }
  if (badColor) delete cleaned.color
  if (badIcon) delete cleaned.icon
  if (badWeight) delete cleaned.iconWeight
  return cleaned
}

/** hostId of the first tmux-session pane in pre-order, or null. */
export function getTabHostId(tab: Tab): string | null {
  return collectTmuxSessionHostIds(tab.layout)[0] ?? null
}

/** Validated color of the tab's host, or null when unresolvable / invalid. */
export function resolveTabHostColor(tab: Tab, hosts: Record<string, HostConfig>): string | null {
  const hostId = getTabHostId(tab)
  if (!hostId) return null
  const color = hosts[hostId]?.color
  return isValidHostColor(color) ? color : null
}
