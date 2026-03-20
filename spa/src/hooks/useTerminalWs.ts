import { useEffect, useRef } from 'react'
import type { Terminal } from '@xterm/xterm'
import type { FitAddon } from '@xterm/addon-fit'
import { connectTerminal } from '../lib/ws'
import { useUISettingsStore } from '../stores/useUISettingsStore'

interface UseTerminalWsOpts {
  wsUrl: string
  termRef: React.RefObject<Terminal | null>
  fitAddonRef: React.RefObject<FitAddon | null>
  containerRef: React.RefObject<HTMLDivElement | null>
  onReady: () => void
  onDisconnect: () => void
  onReconnect: () => void
}

export function useTerminalWs({ wsUrl, termRef, fitAddonRef, containerRef, onReady, onDisconnect, onReconnect }: UseTerminalWsOpts) {
  const connRef = useRef<ReturnType<typeof connectTerminal> | null>(null)
  const revealDelayRef = useRef(useUISettingsStore.getState().terminalRevealDelay)

  useEffect(() => {
    return useUISettingsStore.subscribe((s) => { revealDelayRef.current = s.terminalRevealDelay })
  }, [])

  useEffect(() => {
    const term = termRef.current
    const container = containerRef.current
    if (!term || !container) return

    let revealed = false
    const reveal = () => {
      if (revealed) return
      revealed = true
      onReady()
      term.focus()
    }

    const conn = connectTerminal(
      wsUrl,
      (data) => {
        term.write(new Uint8Array(data))
        if (!revealed) setTimeout(reveal, revealDelayRef.current)
      },
      () => onDisconnect(),
      () => {
        onReconnect()
        if (revealed) onReady()
        fitAddonRef.current?.fit()
        conn.resize(term.cols, term.rows)
      },
    )
    connRef.current = conn

    const ta = container.querySelector('.xterm-helper-textarea')

    // --- Shift+Enter: send \n (line feed) instead of \r (carriage return) ---
    let shiftEnterHandled = false
    const handleShiftEnter = (ev: Event) => {
      const ke = ev as KeyboardEvent
      if (ke.key === 'Enter' && ke.shiftKey && !ke.ctrlKey && !ke.metaKey) {
        ke.stopPropagation()
        ke.preventDefault()
        shiftEnterHandled = true
        conn.send('\n')
      }
    }
    container.addEventListener('keydown', handleShiftEnter, true)

    // --- IME duplicate guard ---
    let lastComposedSent = ''
    const handleCompositionStart = () => { lastComposedSent = '' }
    ta?.addEventListener('compositionstart', handleCompositionStart)

    term.onData((data) => {
      if (shiftEnterHandled && data === '\r') { shiftEnterHandled = false; return }
      shiftEnterHandled = false
      const isComposed = data.length > 1 && data.charCodeAt(0) !== 0x1b
      if (isComposed && data === lastComposedSent) return
      if (isComposed) lastComposedSent = data
      else lastComposedSent = ''
      conn.send(data)
    })
    term.onResize(({ cols, rows }) => conn.resize(cols, rows))

    return () => {
      container.removeEventListener('keydown', handleShiftEnter, true)
      ta?.removeEventListener('compositionstart', handleCompositionStart)
      conn.close()
      connRef.current = null
    }
  }, [wsUrl, termRef, fitAddonRef, containerRef, onReady, onDisconnect, onReconnect])

  return connRef
}
