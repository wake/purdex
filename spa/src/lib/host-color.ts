import type { IconWeight, Tab } from '../types/tab'
import type { HostConfig } from '../stores/useHostStore'
import { collectTmuxSessionHostIds } from './infer-workspace-host-id'
// Static import on purpose: `CommandIconPicker` already pulls icon-meta into the
// main chunk, so the catalog costs nothing extra here.
import iconMetaData from '../features/workspace/generated/icon-meta.json'

/** Phosphor icon used for a host that has not picked one. Must itself pass `isPhosphorIconName`. */
export const DEFAULT_HOST_ICON = 'Desktop'

/**
 * Cheap bounded shape check run *before* the catalog lookup, so a megabyte-long
 * hostile string is rejected without hashing it. Phosphor names are PascalCase
 * alphanumerics; the longest real one is well under 64 chars.
 */
const PHOSPHOR_NAME_RE = /^[A-Z][A-Za-z0-9]{0,63}$/

let phosphorNames: Set<string> | null = null

/**
 * Whether a value is a real Phosphor icon name from the generated catalog.
 *
 * `WorkspaceIcon` renders an unknown name as literal *text*, so any untrusted
 * value (sync payload, persisted state) that reaches it would paint
 * attacker-controlled text into every affected tab row. Membership in the
 * catalog — not just "non-empty string" — is the gate.
 *
 * Exact match only: a padded `' Laptop '` is invalid, never trimmed and accepted.
 */
export function isPhosphorIconName(v: unknown): v is string {
  if (typeof v !== 'string' || !PHOSPHOR_NAME_RE.test(v)) return false
  phosphorNames ??= new Set((iconMetaData as { n: string }[]).map((m) => m.n))
  return phosphorNames.has(v)
}

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
  const badIcon = 'icon' in host && !isPhosphorIconName(host.icon)
  const badWeight = 'iconWeight' in host && !isIconWeight(host.iconWeight)
  if (!badColor && !badIcon && !badWeight) return host

  const cleaned = { ...host }
  if (badColor) delete cleaned.color
  if (badIcon) delete cleaned.icon
  if (badWeight) delete cleaned.iconWeight
  return cleaned
}

/**
 * Whether a tab's resolved host identity is worth a badge at all.
 *
 * "沒設定就沒有": a host that picked neither a color nor an icon renders nothing —
 * a grey default box on every row is noise for a single-host setup. Either half
 * alone is enough (color only → default icon; icon only → neutral box).
 *
 * Structurally typed so `lib/` need not depend on the hook that owns `TabHostBadge`.
 */
export function hasHostBadge<T extends { color: string | null; icon?: string | undefined }>(
  badge: T | null,
): badge is T {
  return badge !== null && (badge.color !== null || badge.icon !== undefined)
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
