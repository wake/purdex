import type { IconWeight, PaneContent, PaneLayout, Tab } from '../types/tab'
import type { HostConfig } from '../stores/useHostStore'
import { rgbaString } from './color-space'
import { isValidDaemonId } from './daemon-id'
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
 * Returns a cleaned `colors` map, or `undefined` when nothing valid survives.
 * `same` is true when the input can be kept by reference.
 */
function sanitizeHostColors(v: unknown): { value: HostConfig['colors']; same: boolean } {
  if (!isPlainObject(v)) return { value: undefined, same: false }
  let same = true
  const out: Partial<Record<HostColorMode, HostColorSet>> = {}
  for (const [mode, rawSet] of Object.entries(v)) {
    if (!isHostColorMode(mode) || !isPlainObject(rawSet) || !isHostColorLayer(rawSet.main, { requireColor: true })) {
      same = false
      continue
    }
    const cleaned: HostColorSet = { main: rawSet.main as HostColorLayer & { color: string } }
    let setSame = true
    for (const layer of ['middle', 'light'] as const) {
      if (!(layer in rawSet)) continue
      if (isHostColorLayer(rawSet[layer])) cleaned[layer] = rawSet[layer]
      else setSame = false
    }
    if (Object.keys(rawSet).some((k) => k !== 'main' && k !== 'middle' && k !== 'light')) setSame = false
    out[mode] = setSame ? (rawSet as unknown as HostColorSet) : cleaned
    if (!setSame) same = false
  }
  if (Object.keys(out).length === 0) return { value: undefined, same: false }
  return { value: out, same }
}

/**
 * Drops present-but-invalid identity keys (`color`, `colors`, `icon`, `iconWeight`,
 * and `daemonId` — judged by the shared `isValidDaemonId`) from an untrusted host
 * config (sync payload, persisted state). Returns the same object when every
 * present key is valid.
 */
export function sanitizeHostConfig(host: HostConfig): HostConfig {
  const badColor = 'color' in host && !isValidHostColor(host.color)
  const badIcon = 'icon' in host && !isPhosphorIconName(host.icon)
  const badWeight = 'iconWeight' in host && !isIconWeight(host.iconWeight)
  const colors = 'colors' in host ? sanitizeHostColors(host.colors) : null
  const badColors = colors !== null && !colors.same
  const badDaemonId = 'daemonId' in host && !isValidDaemonId(host.daemonId)
  if (!badColor && !badIcon && !badWeight && !badColors && !badDaemonId) return host

  const cleaned = { ...host }
  if (badColor) delete cleaned.color
  if (badIcon) delete cleaned.icon
  if (badWeight) delete cleaned.iconWeight
  if (badDaemonId) delete cleaned.daemonId
  if (badColors) {
    if (colors.value === undefined) delete cleaned.colors
    else cleaned.colors = colors.value
  }
  return cleaned
}

/**
 * Whether a tab's resolved host identity is worth a badge at all.
 *
 * "沒設定就沒有": a host that picked neither a color nor an icon renders nothing —
 * a grey default box on every row is noise for a single-host setup. Either half
 * alone is enough (colors only → default icon; icon only → neutral box).
 *
 * Structurally typed so `lib/` need not depend on the hook that owns `TabHostBadge`.
 */
export function hasHostBadge<T extends { colors: ResolvedHostColors | null; icon?: string | undefined }>(
  badge: T | null,
): badge is T {
  return badge !== null && (badge.colors !== null || badge.icon !== undefined)
}

function findFirstTmuxPane(layout: PaneLayout): Extract<PaneContent, { kind: 'tmux-session' }> | null {
  if (layout.type === 'leaf') {
    return layout.pane.content.kind === 'tmux-session' ? layout.pane.content : null
  }
  for (const child of layout.children) {
    const found = findFirstTmuxPane(child)
    if (found) return found
  }
  return null
}

/**
 * Content of the first tmux-session pane in pre-order (the pane the host badge
 * represents), or null.
 *
 * `getTabHostId` derives from this so host and mode are always read from the same
 * pane — resolving them independently (e.g. host from pre-order, mode from
 * `getPrimaryPane`) let a split tab whose first leaf isn't a tmux pane pick one
 * host's colors under a different pane's mode (P2 Task 1 review finding, PR #1153).
 */
export function getTabBadgePane(tab: Tab): Extract<PaneContent, { kind: 'tmux-session' }> | null {
  return findFirstTmuxPane(tab.layout)
}

/** hostId of the first tmux-session pane in pre-order, or null. */
export function getTabHostId(tab: Tab): string | null {
  return getTabBadgePane(tab)?.hostId ?? null
}

/* ─── Per-mode tri-color (spec 2026-09-18 host-color-modes §4.1) ─── */

export type HostColorMode = 'console' | 'terminal' | 'execution'
export const HOST_COLOR_MODES: readonly HostColorMode[] = ['console', 'terminal', 'execution']

export type HostColorLayerName = 'main' | 'middle' | 'light'
export const HOST_COLOR_LAYER_NAMES: readonly HostColorLayerName[] = ['main', 'middle', 'light']

export interface HostColorLayer {
  /** `#rrggbb`. Absent on middle/light = inherit the mode's main color. Required on main. */
  color?: string
  /** Integer 0–100. */
  alpha: number
}

export interface HostColorSet {
  main: HostColorLayer & { color: string }
  middle?: HostColorLayer
  light?: HostColorLayer
}

/** Alpha used when a layer is absent (light 22 = the alpha.362 background default). */
export const HOST_COLOR_ALPHA_DEFAULTS: Readonly<Record<HostColorLayerName, number>> = {
  main: 100,
  middle: 60,
  light: 22,
}

export function isHostColorMode(v: unknown): v is HostColorMode {
  return typeof v === 'string' && (HOST_COLOR_MODES as readonly string[]).includes(v)
}

/** Rounds and bounds to an integer 0–100. NaN → 0. */
export function clampHostAlpha(n: number): number {
  if (Number.isNaN(n)) return 0
  return Math.min(100, Math.max(0, Math.round(n)))
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/**
 * Structural guard for a stored layer: `alpha` must already be an integer 0–100
 * (sanitize drops, it does not clamp); `color`, when present, must be `#rrggbb`.
 */
export function isHostColorLayer(v: unknown, opts?: { requireColor?: boolean }): v is HostColorLayer {
  if (!isPlainObject(v)) return false
  const { alpha, color } = v
  if (typeof alpha !== 'number' || !Number.isInteger(alpha) || alpha < 0 || alpha > 100) return false
  if ('color' in v && !isValidHostColor(color)) return false
  if (opts?.requireColor && !('color' in v)) return false
  return true
}

export function isHostColorSet(v: unknown): v is HostColorSet {
  if (!isPlainObject(v)) return false
  if (!isHostColorLayer(v.main, { requireColor: true })) return false
  if ('middle' in v && !isHostColorLayer(v.middle)) return false
  if ('light' in v && !isHostColorLayer(v.light)) return false
  return true
}

/* ─── Resolver (spec §4.3) ─── */

export type HostColorSource = Pick<HostConfig, 'colors' | 'color'>

export interface ResolvedHostColorLayer { color: string; alpha: number }
export interface ResolvedHostColorSet {
  main: ResolvedHostColorLayer
  middle: ResolvedHostColorLayer
  light: ResolvedHostColorLayer
}

/**
 * Hex-level resolution with every inheritance applied:
 * `colors[mode]` → `colors.console` → legacy `color` → null.
 * Within the chosen set, middle/light take main's color when their own is absent
 * and the §4.1 default alpha when the layer is absent.
 */
export function resolveHostColorSet(host: HostColorSource | undefined, mode: HostColorMode): ResolvedHostColorSet | null {
  if (!host) return null
  const set = host.colors?.[mode] ?? host.colors?.console
  if (set) {
    if (!isHostColorSet(set)) return null
    const main = set.main.color
    return {
      main: { color: main, alpha: set.main.alpha },
      middle: { color: set.middle?.color ?? main, alpha: set.middle?.alpha ?? HOST_COLOR_ALPHA_DEFAULTS.middle },
      light: { color: set.light?.color ?? main, alpha: set.light?.alpha ?? HOST_COLOR_ALPHA_DEFAULTS.light },
    }
  }
  if (!isValidHostColor(host.color)) return null
  return {
    main: { color: host.color, alpha: HOST_COLOR_ALPHA_DEFAULTS.main },
    middle: { color: host.color, alpha: HOST_COLOR_ALPHA_DEFAULTS.middle },
    light: { color: host.color, alpha: HOST_COLOR_ALPHA_DEFAULTS.light },
  }
}

export interface ResolvedHostColors { main: string; middle: string; light: string }

/** `resolveHostColorSet` rendered to `rgba()` strings, ready for inline CSS. */
export function resolveHostColors(host: HostColorSource | undefined, mode: HostColorMode): ResolvedHostColors | null {
  const set = resolveHostColorSet(host, mode)
  if (!set) return null
  const main = rgbaString(set.main.color, set.main.alpha)
  const middle = rgbaString(set.middle.color, set.middle.alpha)
  const light = rgbaString(set.light.color, set.light.alpha)
  if (!main || !middle || !light) return null
  return { main, middle, light }
}
