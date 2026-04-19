import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

export type TerminalRenderer = 'webgl' | 'dom'
export type TabIndicatorStyle = 'icon' | 'dot' | 'iconDot' | 'badge'
export type CcIconVariant = 'bot' | 'star'
export type CodexIconVariant = 'openai' | 'codex'

export const KEEPALIVE_MAX_WEBGL = 6
export const KEEPALIVE_MAX_DOM = 10

export function clampKeepAlive(renderer: TerminalRenderer, count: number): number {
  const max = renderer === 'webgl' ? KEEPALIVE_MAX_WEBGL : KEEPALIVE_MAX_DOM
  return Math.min(count, max)
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
  showOscTitle: boolean
  setShowOscTitle: (show: boolean) => void
}

export const useUISettingsStore = create<UISettings>()(
  persist(
    (set) => ({
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
      linkDetectRelativeSlash: false,
      setLinkDetectRelativeSlash: (v) => set({ linkDetectRelativeSlash: v }),
      linkDetectBareFilename: false,
      setLinkDetectBareFilename: (v) => set({ linkDetectBareFilename: v }),

      tabIndicatorStyle: 'badge' as TabIndicatorStyle,
      setTabIndicatorStyle: (style) => set({ tabIndicatorStyle: style }),
      ccIconVariant: 'bot' as CcIconVariant,
      setCcIconVariant: (variant) => set({ ccIconVariant: variant }),
      codexIconVariant: 'openai' as CodexIconVariant,
      setCodexIconVariant: (variant) => set({ codexIconVariant: variant }),
      showOscTitle: false,
      setShowOscTitle: (show) => set({ showOscTitle: show }),
    }),
    {
      name: STORAGE_KEYS.UI_SETTINGS,
      storage: purdexStorage,
      version: 2,
      migrate: (persisted: unknown, fromVersion: number): unknown => {
        const base = (persisted ?? {}) as Record<string, unknown>
        if (fromVersion >= 2) return base

        // Import UI prefs that used to live in useAgentStore's persist slice (v4 or v5).
        // Keep it best-effort: any parse/shape error just falls back to defaults.
        try {
          if (typeof window === 'undefined') return base
          const raw = window.localStorage.getItem(STORAGE_KEYS.AGENT)
          if (!raw) return base
          const parsed = JSON.parse(raw)
          const oldState = parsed?.state as Record<string, unknown> | undefined
          if (!oldState) return base
          const imported: Record<string, unknown> = {}
          if (typeof oldState.tabIndicatorStyle === 'string') imported.tabIndicatorStyle = oldState.tabIndicatorStyle
          if (typeof oldState.ccIconVariant === 'string') imported.ccIconVariant = oldState.ccIconVariant
          if (typeof oldState.codexIconVariant === 'string') imported.codexIconVariant = oldState.codexIconVariant
          if (typeof oldState.showOscTitle === 'boolean') imported.showOscTitle = oldState.showOscTitle
          return { ...base, ...imported }
        } catch {
          return base
        }
      },
      onRehydrateStorage: () => (state) => {
        if (!state) return
        const clamped = clampKeepAlive(state.terminalRenderer, state.keepAliveCount)
        if (clamped !== state.keepAliveCount) {
          useUISettingsStore.setState({ keepAliveCount: clamped })
        }
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.UI_SETTINGS, useUISettingsStore)
