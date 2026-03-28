import { useEffect, useState, useRef, useCallback } from 'react'
import { useTerminal } from '../hooks/useTerminal'
import { useTerminalWs } from '../hooks/useTerminalWs'
import '@xterm/xterm/css/xterm.css'

interface Props {
  wsUrl: string
  visible?: boolean
  connectingMessage?: string
}

export default function TerminalView({ wsUrl, visible = true, connectingMessage }: Props) {
  const { termRef, fitAddonRef, containerRef } = useTerminal()
  const [ready, setReady] = useState(false)
  const [disconnected, setDisconnected] = useState(false)
  const prevVisible = useRef(visible)

  const handleReady = useCallback(() => { setReady(true) }, [])
  const handleDisconnect = useCallback(() => { setDisconnected(true) }, [])
  const handleReconnect = useCallback(() => { setDisconnected(false) }, [])

  const connRef = useTerminalWs({
    wsUrl,
    termRef,
    fitAddonRef,
    containerRef,
    onReady: handleReady,
    onDisconnect: handleDisconnect,
    onReconnect: handleReconnect,
  })

  // Reset state on wsUrl change. React guarantees effects fire in declaration
  // order, so useTerminal (mount) → useTerminalWs (connect) → this reset.
  useEffect(() => {
    setReady(false)
    setDisconnected(false)
  }, [wsUrl])

  // Refit + focus when becoming visible after being hidden (keep-alive).
  // With offscreen positioning (left: -9999em) the terminal kept correct
  // dimensions the whole time, so no overlay or delay is needed.
  // Force ready=true to suppress any lingering connecting overlay —
  // the terminal was alive and connected the whole time.
  useEffect(() => {
    if (visible && !prevVisible.current) {
      setReady(true)
      requestAnimationFrame(() => {
        fitAddonRef.current?.fit()
        const term = termRef.current
        const conn = connRef.current
        if (term && conn) conn.resize(term.cols, term.rows)
        termRef.current?.focus()
      })
    }
    prevVisible.current = visible
  }, [visible, termRef, fitAddonRef, connRef])

  const showOverlay = !ready || disconnected

  return (
    <div className="w-full h-full relative bg-terminal-bg">
      <div ref={containerRef} className="w-full h-full" />
      <div
        data-testid="terminal-overlay"
        className="absolute inset-0 flex items-center justify-center pointer-events-none"
        style={{
          background: disconnected ? 'color-mix(in srgb, var(--terminal-bg) 50%, transparent)' : 'var(--terminal-bg)',
          opacity: showOverlay ? 1 : 0,
          transition: 'opacity 0.3s ease-out',
        }}
      >
        <span className="text-text-muted text-sm" style={{ animation: 'breathing 2s ease-in-out infinite' }}>
          {disconnected ? 'reconnecting...' : (connectingMessage || 'connecting...')}
        </span>
        <style>{`@keyframes breathing { 0%, 100% { opacity: 0.3; } 50% { opacity: 1; } }`}</style>
      </div>
    </div>
  )
}
