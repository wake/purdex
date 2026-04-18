import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { createXtermLinkProvider, terminalLinkRegistry } from '../lib/terminal-link'
import type { LinkContext } from '../lib/terminal-link'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useThemeStore } from '../stores/useThemeStore'

export interface UseTerminalResult {
  termRef: React.RefObject<Terminal | null>
  fitAddonRef: React.RefObject<FitAddon | null>
  containerRef: React.RefObject<HTMLDivElement | null>
}

function getTerminalTheme() {
  const style = getComputedStyle(document.documentElement)
  return {
    background: style.getPropertyValue('--terminal-bg').trim() || '#0a0a1a',
    foreground: style.getPropertyValue('--terminal-fg').trim() || '#e0e0e0',
    cursor: style.getPropertyValue('--terminal-cursor').trim() || '#e0e0e0',
  }
}

/**
 * xterm instance 的生命週期綁定 component mount/unmount。
 * wsUrl 變更或 settings 變更導致的重建由 TabContent 的 pool key
 * (`key={id}-${poolVersion}`) 觸發 remount，不在此 hook 處理。
 */
export interface UseTerminalOptions {
  linkContext?: LinkContext
  onTitle?: (title: string) => void
}

export function useTerminal(options: UseTerminalOptions = {}): UseTerminalResult {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)

  // Keep latest onTitle in a ref so the mount effect (empty deps) always
  // calls the current handler without re-binding the terminal lifecycle.
  const onTitleRef = useRef(options.onTitle)
  // eslint-disable-next-line react-hooks/refs -- latest-value pattern for mount-only effect closure
  onTitleRef.current = options.onTitle

  // Keep latest linkContext in a ref so the provider closure always reads
  // the current hostId/sessionCode without re-binding the terminal lifecycle.
  const linkCtxRef = useRef<LinkContext>(options.linkContext ?? {})
  // eslint-disable-next-line react-hooks/refs -- latest-value pattern for mount-only effect closure
  linkCtxRef.current = options.linkContext ?? {}

  useEffect(() => {
    if (!containerRef.current) return

    const term = new Terminal({
      theme: getTerminalTheme(),
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, monospace',
      cursorBlink: true,
      macOptionClickForcesSelection: true,
      rightClickSelectsWord: true,
    })

    const titleDisposable = term.onTitleChange((title) => {
      onTitleRef.current?.(title)
    })

    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(containerRef.current)
    fitAddonRef.current = fitAddon
    termRef.current = term

    const renderer = useUISettingsStore.getState().terminalRenderer
    if (renderer === 'webgl') {
      try {
        const webglAddon = new WebglAddon()
        webglAddon.onContextLoss(() => {
          webglAddon.dispose()
          // xterm auto-creates DomRenderer on WebglAddon dispose.
          // Re-fit to recalculate dimensions with the new renderer.
          requestAnimationFrame(() => { if (fitAddonRef.current) fitAddonRef.current.fit() })
        })
        term.loadAddon(webglAddon)
      } catch { /* fallback to DOM */ }
    }
    // DOM renderer is the default — no addon needed
    try {
      term.loadAddon(new Unicode11Addon())
      term.unicode.activeVersion = '11'
    } catch { /* fallback to unicode 6 */ }
    let linkProviderDisp: { dispose: () => void } | undefined
    try {
      linkProviderDisp = term.registerLinkProvider(
        createXtermLinkProvider(terminalLinkRegistry, () => linkCtxRef.current, term),
      )
    } catch { /* non-critical */ }

    const container = containerRef.current
    // Guard: skip initial fit if container is hidden (visibility:hidden in keep-alive pool)
    requestAnimationFrame(() => {
      const { width, height } = container.getBoundingClientRect()
      if (width && height) fitAddon.fit()
    })

    let rafId = 0
    const observer = new ResizeObserver((entries) => {
      // Guard against hidden tabs (visibility:hidden) sending stale resize
      const { width, height } = entries[0]?.contentRect ?? {}
      if (!width || !height) return
      cancelAnimationFrame(rafId)
      rafId = requestAnimationFrame(() => fitAddon.fit())
    })
    observer.observe(container)

    const handleContextMenu = (e: MouseEvent) => e.preventDefault()
    container.addEventListener('contextmenu', handleContextMenu)

    return () => {
      cancelAnimationFrame(rafId)
      observer.disconnect()
      container.removeEventListener('contextmenu', handleContextMenu)
      linkProviderDisp?.dispose()
      titleDisposable.dispose()
      term.dispose()
      fitAddonRef.current = null
      termRef.current = null
    }
  }, [])

  useEffect(() => {
    let prevThemeId = useThemeStore.getState().activeThemeId
    const unsub = useThemeStore.subscribe((state) => {
      if (state.activeThemeId === prevThemeId) return
      prevThemeId = state.activeThemeId
      if (!termRef.current) return
      requestAnimationFrame(() => {
        if (termRef.current) {
          termRef.current.options.theme = getTerminalTheme()
        }
      })
    })
    return unsub
  }, [])

  return { termRef, fitAddonRef, containerRef }
}
