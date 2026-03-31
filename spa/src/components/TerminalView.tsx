import { useEffect, useState, useRef, useCallback } from 'react'
import { UploadSimple } from '@phosphor-icons/react'
import { useTerminal } from '../hooks/useTerminal'
import { useTerminalWs } from '../hooks/useTerminalWs'
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { compositeKey } from '../lib/composite-key'
import { agentUpload } from '../lib/api'
import '@xterm/xterm/css/xterm.css'

interface Props {
  wsUrl: string
  visible?: boolean
  connectingMessage?: string
  hostId?: string
  sessionCode?: string
}

export default function TerminalView({ wsUrl, visible = true, connectingMessage, hostId, sessionCode }: Props) {
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

  // Drag-drop state
  const [isDragging, setIsDragging] = useState(false)
  const dragCounter = useRef(0)
  const ck = hostId && sessionCode ? compositeKey(hostId, sessionCode) : undefined
  const agentStatus = useAgentStore((s) => ck ? s.statuses[ck] : undefined)
  const agentActive = agentStatus != null
  const daemonBase = useHostStore((s) => s.getDaemonBase(hostId ?? s.hostOrder[0] ?? ''))
  const t = useI18nStore((s) => s.t)

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    if (!agentActive) return
    dragCounter.current++
    if (dragCounter.current === 1) setIsDragging(true)
  }, [agentActive])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault()
  }, [])

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    dragCounter.current--
    if (dragCounter.current <= 0) {
      dragCounter.current = 0
      setIsDragging(false)
    }
  }, [])

  const handleDrop = useCallback(async (e: React.DragEvent) => {
    e.preventDefault()
    setIsDragging(false)
    dragCounter.current = 0
    if (!agentActive || !hostId || !sessionCode) return

    const files = Array.from(e.dataTransfer.files)
    if (files.length === 0) return

    const { startUpload, fileCompleted, fileFailed, nextFile } = useUploadStore.getState()
    startUpload(hostId, sessionCode, files.length, files[0].name) // overwrites any previous done/error state

    for (let i = 0; i < files.length; i++) {
      if (i > 0) nextFile(hostId, sessionCode, files[i].name)
      try {
        await agentUpload(daemonBase, files[i], sessionCode)
        fileCompleted(hostId, sessionCode)
      } catch {
        fileFailed(hostId, sessionCode, files[i].name)
      }
    }
  }, [agentActive, hostId, sessionCode, daemonBase])

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
    <div
      className="w-full h-full relative bg-terminal-bg"
      onDragEnter={handleDragEnter}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
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
      {isDragging && (
        <div
          data-testid="drop-overlay"
          className="absolute inset-0 flex flex-col items-center justify-center gap-2 pointer-events-none z-20"
          style={{ background: 'rgba(0, 0, 0, 0.6)' }}
        >
          <UploadSimple size={32} className="text-text-secondary" />
          <span className="text-text-secondary text-sm">{t('upload.drop_files')}</span>
        </div>
      )}
    </div>
  )
}
