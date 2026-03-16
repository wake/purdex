import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import { connectTerminal } from '../lib/ws'
import '@xterm/xterm/css/xterm.css'

interface Props {
  wsUrl: string
}

export default function TerminalView({ wsUrl }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!containerRef.current) return

    const term = new Terminal({
      theme: { background: '#0a0a1a', foreground: '#e0e0e0' },
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, monospace',
      cursorBlink: true,
    })

    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(containerRef.current)

    try { term.loadAddon(new WebglAddon()) } catch { /* fallback to canvas */ }

    // Delay initial fit to next frame — flex layout may not have final dimensions yet
    requestAnimationFrame(() => fitAddon.fit())

    const conn = connectTerminal(
      wsUrl,
      (data) => term.write(new Uint8Array(data)),
      () => term.write('\r\n\x1b[31m[disconnected]\x1b[0m\r\n'),
      () => {
        // WebSocket opened — send initial size so tmux renders at correct dimensions
        fitAddon.fit()
        conn.resize(term.cols, term.rows)
      },
    )

    term.onData((data) => conn.send(data))
    term.onResize(({ cols, rows }) => conn.resize(cols, rows))

    let rafId = 0
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(rafId)
      rafId = requestAnimationFrame(() => fitAddon.fit())
    })
    observer.observe(containerRef.current)

    return () => {
      cancelAnimationFrame(rafId)
      observer.disconnect()
      conn.close()
      term.dispose()
    }
  }, [wsUrl])

  return <div ref={containerRef} className="w-full h-full" />
}
