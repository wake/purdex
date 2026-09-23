import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

export type TerminalRenderer = 'webgl' | 'dom'
export type TabIndicatorStyle = 'icon' | 'dot' | 'iconDot' | 'badge'
export type CcIconVariant = 'bot' | 'star'
export type CodexIconVariant = 'openai' | 'codex'
export type TabNameTooltipMode = 'none' | 'top' | 'left' | 'both'

export const KEEPALIVE_MAX_WEBGL = 6
export const KEEPALIVE_MAX_DOM = 10

export function clampKeepAlive(renderer: TerminalRenderer, count: number): number {
  const max = renderer === 'webgl' ? KEEPALIVE_MAX_WEBGL : KEEPALIVE_MAX_DOM
  return Math.min(count, max)
}

// ---------------------------------------------------------------------------
// Host badge (tinted box + host icon shown on sidebar rows / top tabs)
// ---------------------------------------------------------------------------

export type HostBadgeLineColor = 'host' | 'neutral'

const HOST_BADGE_LINE_COLORS: readonly HostBadgeLineColor[] = ['host', 'neutral']

export function isHostBadgeLineColor(v: unknown): v is HostBadgeLineColor {
  return typeof v === 'string' && (HOST_BADGE_LINE_COLORS as readonly string[]).includes(v)
}

export const HOST_BADGE_BOX_MIN = 12
export const HOST_BADGE_BOX_MAX = 24
export const HOST_BADGE_BOX_DEFAULT = 16
export const HOST_BADGE_INSET_MIN = 0
export const HOST_BADGE_INSET_MAX = 5
export const HOST_BADGE_INSET_DEFAULT = 2
export const HOST_BADGE_RADIUS_MIN = 0
export const HOST_BADGE_RADIUS_MAX = 8
export const HOST_BADGE_RADIUS_DEFAULT = 4

/** Non-finite → the field's default; otherwise round then clamp to [min, max]. */
function clampRounded(n: number, min: number, max: number, fallback: number): number {
  if (!Number.isFinite(n)) return fallback
  return Math.min(max, Math.max(min, Math.round(n)))
}

export const clampHostBadgeBox = (n: number): number =>
  clampRounded(n, HOST_BADGE_BOX_MIN, HOST_BADGE_BOX_MAX, HOST_BADGE_BOX_DEFAULT)
export const clampHostBadgeInset = (n: number): number =>
  clampRounded(n, HOST_BADGE_INSET_MIN, HOST_BADGE_INSET_MAX, HOST_BADGE_INSET_DEFAULT)
export const clampHostBadgeRadius = (n: number): number =>
  clampRounded(n, HOST_BADGE_RADIUS_MIN, HOST_BADGE_RADIUS_MAX, HOST_BADGE_RADIUS_DEFAULT)

const HOST_BADGE_SURFACES = ['Sidebar', 'TabBar'] as const

const HOST_BADGE_BOOL_FIELDS = HOST_BADGE_SURFACES.map((s) => `hostBadge${s}Enabled`)
const HOST_BADGE_ENUM_FIELDS = HOST_BADGE_SURFACES.map((s) => `hostBadge${s}LineColor`)
const HOST_BADGE_NUMERIC_CLAMPS: Record<string, (n: number) => number> = Object.fromEntries(
  HOST_BADGE_SURFACES.flatMap((s) => [
    [`hostBadge${s}Box`, clampHostBadgeBox],
    [`hostBadge${s}Inset`, clampHostBadgeInset],
    [`hostBadge${s}Radius`, clampHostBadgeRadius],
  ]),
)

/**
 * Per-surface opacity fields removed in P2 (§4.4): host badge tint is now
 * driven by the host color mode, not independent line/background opacity
 * sliders. Kept as a named list so stale persisted/synced keys can be
 * stripped rather than silently resurrected.
 */
export const HOST_BADGE_REMOVED_FIELDS = HOST_BADGE_SURFACES.flatMap((s) => [
  `hostBadge${s}LineOpacity`,
  `hostBadge${s}BgOpacity`,
]) as readonly string[]

/** Initial values for the 10 host badge fields; also the rehydrate fallback. */
export const HOST_BADGE_DEFAULTS = {
  hostBadgeSidebarEnabled: true,
  hostBadgeSidebarLineColor: 'host' as HostBadgeLineColor,
  hostBadgeSidebarBox: HOST_BADGE_BOX_DEFAULT,
  hostBadgeSidebarInset: HOST_BADGE_INSET_DEFAULT,
  hostBadgeSidebarRadius: HOST_BADGE_RADIUS_DEFAULT,
  hostBadgeTabBarEnabled: true,
  hostBadgeTabBarLineColor: 'host' as HostBadgeLineColor,
  hostBadgeTabBarBox: HOST_BADGE_BOX_DEFAULT,
  hostBadgeTabBarInset: HOST_BADGE_INSET_DEFAULT,
  hostBadgeTabBarRadius: HOST_BADGE_RADIUS_DEFAULT,
}

/**
 * Validates host badge fields for paths that bypass store setters (sync,
 * persist rehydrate): non-boolean toggles, invalid line colors and non-number /
 * non-finite metrics are dropped; finite metrics are rounded + clamped. Other
 * fields pass through untouched.
 */
export function sanitizeHostBadgePrefs<T extends object>(data: T): T {
  const out = { ...data } as Record<string, unknown>
  for (const field of HOST_BADGE_REMOVED_FIELDS) {
    delete out[field]
  }
  for (const field of HOST_BADGE_BOOL_FIELDS) {
    if (field in out && typeof out[field] !== 'boolean') delete out[field]
  }
  for (const field of HOST_BADGE_ENUM_FIELDS) {
    if (field in out && !isHostBadgeLineColor(out[field])) delete out[field]
  }
  for (const [field, clamp] of Object.entries(HOST_BADGE_NUMERIC_CLAMPS)) {
    if (!(field in out)) continue
    const v = out[field]
    if (typeof v !== 'number' || !Number.isFinite(v)) delete out[field]
    else out[field] = clamp(v)
  }
  return out as T
}

interface UISettings {
  /**
   * 收到第一筆 terminal data 後，延遲多久才移除 overlay 顯示畫面（ms）。
   *
   * 預設 300ms。這段延遲讓 overlay 遮住以下瞬態現象：
   * - 0ms（onOpen reveal）：tmux attach 完成但尚未送出畫面，會看到 resize 彈跳
   * - 立即 reveal（首筆 data 無 delay）：daemon batcher 16ms 批次 + Claude Code
   *   自動捲動會產生可見的捲動閃爍
   * - 300ms：足夠 batcher 送完初始畫面 + tmux 渲染穩定，視覺上平滑過渡
   *
   * overlay 只在收到第一筆 data 後才開始倒數，無 fallback timer。
   */
  terminalRevealDelay: number
  setTerminalRevealDelay: (ms: number) => void

  /**
   * Terminal 渲染器類型。
   *
   * - webgl：效能最佳，適合 Claude Code 大量高速輸出，但每個實例佔一個 WebGL context
   *   （瀏覽器限制通常 8-16 個）
   * - dom：DOM 渲染，效能較低但相容性最好，無 WebGL context 限制，適合 Electron 或低負載場景
   *
   * 變更後需重啟 terminal 連線（Settings Terminal section 切換時自動 bump version）。
   */
  terminalRenderer: TerminalRenderer
  setTerminalRenderer: (renderer: TerminalRenderer) => void

  keepAliveCount: number
  setKeepAliveCount: (n: number) => void
  keepAlivePinned: boolean
  setKeepAlivePinned: (v: boolean) => void
  terminalSettingsVersion: number
  bumpTerminalSettingsVersion: () => void

  linkDetectAbsolute: boolean
  setLinkDetectAbsolute: (v: boolean) => void
  linkDetectTilde: boolean
  setLinkDetectTilde: (v: boolean) => void
  linkDetectRelativeSlash: boolean
  setLinkDetectRelativeSlash: (v: boolean) => void
  linkDetectBareFilename: boolean
  setLinkDetectBareFilename: (v: boolean) => void

  tabIndicatorStyle: TabIndicatorStyle
  setTabIndicatorStyle: (style: TabIndicatorStyle) => void
  ccIconVariant: CcIconVariant
  setCcIconVariant: (variant: CcIconVariant) => void
  codexIconVariant: CodexIconVariant
  setCodexIconVariant: (variant: CodexIconVariant) => void
  dynamicTabName: boolean
  setDynamicTabName: (show: boolean) => void
  tabNameTooltipMode: TabNameTooltipMode
  setTabNameTooltipMode: (mode: TabNameTooltipMode) => void
  showAgentTitleInStatusBar: boolean
  setShowAgentTitleInStatusBar: (show: boolean) => void
  /** Strip the marker an agent writes at the start of its pane title (e.g. Claude Code's `✳`). */
  stripAgentTitleMarker: boolean
  setStripAgentTitleMarker: (v: boolean) => void


  hostBadgeSidebarEnabled: boolean
  setHostBadgeSidebarEnabled: (v: boolean) => void
  hostBadgeSidebarLineColor: HostBadgeLineColor
  setHostBadgeSidebarLineColor: (v: HostBadgeLineColor) => void
  hostBadgeSidebarBox: number
  setHostBadgeSidebarBox: (px: number) => void
  hostBadgeSidebarInset: number
  setHostBadgeSidebarInset: (px: number) => void
  hostBadgeSidebarRadius: number
  setHostBadgeSidebarRadius: (px: number) => void

  hostBadgeTabBarEnabled: boolean
  setHostBadgeTabBarEnabled: (v: boolean) => void
  hostBadgeTabBarLineColor: HostBadgeLineColor
  setHostBadgeTabBarLineColor: (v: HostBadgeLineColor) => void
  hostBadgeTabBarBox: number
  setHostBadgeTabBarBox: (px: number) => void
  hostBadgeTabBarInset: number
  setHostBadgeTabBarInset: (px: number) => void
  hostBadgeTabBarRadius: number
  setHostBadgeTabBarRadius: (px: number) => void
}

// persist hydrates synchronously INSIDE `create()` (purdexStorage is synchronous),
// while `useUISettingsStore` is still in its TDZ — so `onRehydrateStorage` repairs
// through the creator's captured `set`, never the store constant (#1385).
let setUISettingsState: (partial: Partial<UISettings>) => void = () => {}

export const useUISettingsStore = create<UISettings>()(
  persist(
    (set) => {
      setUISettingsState = set
      return {
        terminalRevealDelay: 300,
        setTerminalRevealDelay: (ms) => set({ terminalRevealDelay: ms }),
        terminalRenderer: 'webgl' as TerminalRenderer,
        setTerminalRenderer: (renderer) => set({ terminalRenderer: renderer }),

        keepAliveCount: 0,
        setKeepAliveCount: (n) => set({ keepAliveCount: n }),
        keepAlivePinned: false,
        setKeepAlivePinned: (v) => set({ keepAlivePinned: v }),
        terminalSettingsVersion: 0,
        bumpTerminalSettingsVersion: () => set((s) => ({ terminalSettingsVersion: s.terminalSettingsVersion + 1 })),

        linkDetectAbsolute: true,
        setLinkDetectAbsolute: (v) => set({ linkDetectAbsolute: v }),
        linkDetectTilde: true,
        setLinkDetectTilde: (v) => set({ linkDetectTilde: v }),
        linkDetectRelativeSlash: true,
        setLinkDetectRelativeSlash: (v) => set({ linkDetectRelativeSlash: v }),
        linkDetectBareFilename: false,
        setLinkDetectBareFilename: (v) => set({ linkDetectBareFilename: v }),

        tabIndicatorStyle: 'badge' as TabIndicatorStyle,
        setTabIndicatorStyle: (style) => set({ tabIndicatorStyle: style }),
        ccIconVariant: 'bot' as CcIconVariant,
        setCcIconVariant: (variant) => set({ ccIconVariant: variant }),
        codexIconVariant: 'openai' as CodexIconVariant,
        setCodexIconVariant: (variant) => set({ codexIconVariant: variant }),
        dynamicTabName: false,
        setDynamicTabName: (show) => set({ dynamicTabName: show }),
        tabNameTooltipMode: 'both' as TabNameTooltipMode,
        setTabNameTooltipMode: (mode) => set({ tabNameTooltipMode: mode }),
        showAgentTitleInStatusBar: false,
        setShowAgentTitleInStatusBar: (show) => set({ showAgentTitleInStatusBar: show }),
        stripAgentTitleMarker: true,
        setStripAgentTitleMarker: (v) => set({ stripAgentTitleMarker: v }),


        ...HOST_BADGE_DEFAULTS,
        setHostBadgeSidebarEnabled: (v) => set({ hostBadgeSidebarEnabled: v }),
        setHostBadgeSidebarLineColor: (v) => {
          if (isHostBadgeLineColor(v)) set({ hostBadgeSidebarLineColor: v })
        },
        setHostBadgeSidebarBox: (px) => set({ hostBadgeSidebarBox: clampHostBadgeBox(px) }),
        setHostBadgeSidebarInset: (px) => set({ hostBadgeSidebarInset: clampHostBadgeInset(px) }),
        setHostBadgeSidebarRadius: (px) => set({ hostBadgeSidebarRadius: clampHostBadgeRadius(px) }),

        setHostBadgeTabBarEnabled: (v) => set({ hostBadgeTabBarEnabled: v }),
        setHostBadgeTabBarLineColor: (v) => {
          if (isHostBadgeLineColor(v)) set({ hostBadgeTabBarLineColor: v })
        },
        setHostBadgeTabBarBox: (px) => set({ hostBadgeTabBarBox: clampHostBadgeBox(px) }),
        setHostBadgeTabBarInset: (px) => set({ hostBadgeTabBarInset: clampHostBadgeInset(px) }),
        setHostBadgeTabBarRadius: (px) => set({ hostBadgeTabBarRadius: clampHostBadgeRadius(px) }),
      }
    },
    {
      name: STORAGE_KEYS.UI_SETTINGS,
      storage: purdexStorage,
      version: 4,
      migrate: (persisted: unknown, fromVersion: number): unknown => {
        const base = (persisted ?? {}) as Record<string, unknown>

        const migrateShowOscTitle = (state: Record<string, unknown>): Record<string, unknown> => {
          const { showOscTitle, ...rest } = state
          if (typeof showOscTitle === 'boolean') {
            return { ...rest, dynamicTabName: showOscTitle, showAgentTitleInStatusBar: showOscTitle }
          }
          return rest
        }

        // v4 dropped the four per-surface host badge opacity fields (§4.4): a
        // migrate step is the only hook that can remove keys (onRehydrateStorage
        // + setState can only add/overwrite), so strip them here regardless of
        // which branch below produced the migrated state.
        const stripRemovedHostBadgeFields = (state: Record<string, unknown>): Record<string, unknown> => {
          const out = { ...state }
          for (const k of HOST_BADGE_REMOVED_FIELDS) delete out[k]
          return out
        }

        const migrated = ((): Record<string, unknown> => {
          if (fromVersion >= 3) return base

          if (fromVersion >= 2) return migrateShowOscTitle(base)

          // Import UI prefs that used to live in useAgentStore's persist slice (v4 or v5).
          // Keep it best-effort: any parse/shape error just falls back to defaults.
          try {
            if (typeof window === 'undefined') return migrateShowOscTitle(base)
            const raw = window.localStorage.getItem(STORAGE_KEYS.AGENT)
            if (!raw) return migrateShowOscTitle(base)
            const parsed = JSON.parse(raw)
            const oldState = parsed?.state as Record<string, unknown> | undefined
            if (!oldState) return migrateShowOscTitle(base)
            const imported: Record<string, unknown> = {}
            if (typeof oldState.tabIndicatorStyle === 'string') imported.tabIndicatorStyle = oldState.tabIndicatorStyle
            if (typeof oldState.ccIconVariant === 'string') imported.ccIconVariant = oldState.ccIconVariant
            if (typeof oldState.codexIconVariant === 'string') imported.codexIconVariant = oldState.codexIconVariant
            if (typeof oldState.showOscTitle === 'boolean') imported.showOscTitle = oldState.showOscTitle
            return migrateShowOscTitle({ ...base, ...imported })
          } catch {
            return migrateShowOscTitle(base)
          }
        })()

        return fromVersion < 4 ? stripRemovedHostBadgeFields(migrated) : migrated
      },
      onRehydrateStorage: () => (state) => {
        if (!state) return
        const clamped = clampKeepAlive(state.terminalRenderer, state.keepAliveCount)
        if (clamped !== state.keepAliveCount) {
          setUISettingsState({ keepAliveCount: clamped })
        }

        // migrate() passes v3 data through untouched, so corrupt local host badge
        // prefs must be repaired here: a dropped field falls back to its default,
        // an out-of-range number is clamped.
        const badgeState = state as unknown as Record<string, unknown>
        const badgeKeys = Object.keys(HOST_BADGE_DEFAULTS)
        const badgeCurrent: Record<string, unknown> = {}
        for (const k of badgeKeys) badgeCurrent[k] = badgeState[k]
        const badgeSanitized: Record<string, unknown> = {
          ...HOST_BADGE_DEFAULTS,
          ...sanitizeHostBadgePrefs(badgeCurrent),
        }
        const badgeChanged = badgeKeys.some((k) => badgeSanitized[k] !== badgeCurrent[k])
        if (badgeChanged) setUISettingsState(badgeSanitized as Partial<UISettings>)
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.UI_SETTINGS, useUISettingsStore)
