import { useState, useRef, useCallback, useEffect } from 'react'
import { CaretUp, CircleNotch, CheckCircle, XCircle } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore, getAgentLabel } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  activeTab: Tab | null
  onViewModeChange?: (tabId: string, paneId: string, mode: 'terminal' | 'stream') => void
}

const VIEW_MODE_COLORS: Record<string, string> = {
  terminal: 'bg-green-900/40 text-green-400 border-green-700/50',
  stream: 'bg-blue-900/40 text-blue-400 border-blue-700/50',
}

function UploadStatus({ sessionCode, t }: { sessionCode: string | null; t: (key: string, params?: Record<string, string | number>) => string }) {
  const uploadState = useUploadStore((s) => sessionCode ? s.sessions[sessionCode] : undefined)
  const dismiss = useUploadStore((s) => s.dismiss)
  const uploadStatus = uploadState?.status

  // Auto-dismiss "done" after 3 seconds
  useEffect(() => {
    if (uploadStatus !== 'done' || !sessionCode) return
    const timer = setTimeout(() => dismiss(sessionCode), 3000)
    return () => clearTimeout(timer)
  }, [uploadStatus, sessionCode, dismiss])

  // Auto-dismiss "error" after 30 seconds
  useEffect(() => {
    if (uploadStatus !== 'error' || !sessionCode) return
    const timer = setTimeout(() => dismiss(sessionCode), 30000)
    return () => clearTimeout(timer)
  }, [uploadStatus, sessionCode, dismiss])

  if (!uploadState || !sessionCode) return null

  if (uploadState.status === 'uploading') {
    return (
      <span className="flex items-center gap-1 text-yellow-400" data-testid="upload-status">
        <CircleNotch size={12} className="animate-spin" />
        <span>{t('upload.uploading', { file: uploadState.currentFile, current: uploadState.completed + 1, total: uploadState.total })}</span>
      </span>
    )
  }

  if (uploadState.status === 'done') {
    const key = uploadState.total === 1 ? 'upload.done_one' : 'upload.done_many'
    return (
      <span className="flex items-center gap-1 text-green-400" data-testid="upload-status">
        <CheckCircle size={12} />
        <span>{t(key, { count: uploadState.total })}</span>
      </span>
    )
  }

  if (uploadState.status === 'error') {
    const message = uploadState.completed > 0
      ? t('upload.partial', { uploaded: uploadState.completed, failed: uploadState.failed })
      : t('upload.failed', { file: uploadState.error ?? '' })
    return (
      <span
        className="flex items-center gap-1 text-red-400 cursor-pointer"
        data-testid="upload-status"
        onClick={() => dismiss(sessionCode)}
      >
        <XCircle size={12} />
        <span>{message}</span>
      </span>
    )
  }

  return null
}

export function StatusBar({ activeTab, onViewModeChange }: Props) {
  const t = useI18nStore((s) => s.t)
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  const sessions = useSessionStore((s) => s.sessions)
  const defaultHost = useHostStore((s) => s.defaultHost)

  // Read agent event for the active session (hooks must be called unconditionally)
  const sessionCode = activeTab?.layout
    ? getPrimaryPane(activeTab.layout).content
    : null
  const agentSessionCode = sessionCode && 'sessionCode' in sessionCode ? sessionCode.sessionCode : null
  const agentEvent = useAgentStore((s) => agentSessionCode ? s.events[agentSessionCode] : undefined)

  const closeMenu = useCallback(() => setMenuOpen(false), [])
  useClickOutside(menuRef, closeMenu)

  if (!activeTab) {
    return (
      <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0">
        {t('status.no_active')}
      </div>
    )
  }

  const primary = getPrimaryPane(activeTab.layout)
  const { content } = primary

  if (content.kind !== 'session') {
    return (
      <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0">
        <span>{content.kind}</span>
      </div>
    )
  }

  // Session pane — show host, session name, status, viewMode toggle
  const session = sessions.find((s) => s.code === content.sessionCode)
  const sessionName = session?.name ?? content.sessionCode
  const hostName = defaultHost.name
  const status = defaultHost.status

  const viewMode = content.mode
  const viewModes: ('terminal' | 'stream')[] = ['terminal', 'stream']

  return (
    <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted gap-3 flex-shrink-0 relative z-10">
      <span>{hostName}</span>
      <span>{sessionName}</span>
      <span className={status === 'connected' ? 'text-green-500' : 'text-text-muted'}>
        {status}
      </span>
      {getAgentLabel(agentEvent) && (() => {
        const label = getAgentLabel(agentEvent)!
        const hasModelName = label !== 'Agent'
        const badgeClass = hasModelName
          ? 'bg-[rgba(154,96,56,0.15)] text-[#e8956a] border-[rgba(180,110,65,0.3)]'
          : 'bg-white/8 text-white/70 border-white/15'
        return (
          <span className={`px-[7px] rounded-[3px] border text-[10px] leading-4 ${badgeClass}`} data-testid="agent-label">
            {label}
          </span>
        )
      })()}
      <UploadStatus sessionCode={agentSessionCode} t={t} />
      <span className="ml-auto flex items-center">
        <div className="relative" ref={menuRef}>
          <button
            title={t('nav.toggle_view')}
            onClick={() => setMenuOpen((v) => !v)}
            className={`flex items-center gap-1 px-2 py-0.5 rounded border text-[10px] cursor-pointer transition-colors ${VIEW_MODE_COLORS[viewMode] ?? 'bg-surface-secondary text-text-secondary border-border-default'}`}
          >
            {viewMode}
            <CaretUp size={10} className={`transition-transform ${menuOpen ? '' : 'rotate-180'}`} />
          </button>
          {menuOpen && (
            <div className="absolute bottom-full right-0 mb-1 bg-surface-elevated border border-border-default rounded-md shadow-lg py-1 min-w-[100px]">
              {viewModes.map((vm) => (
                <button
                  key={vm}
                  onClick={() => {
                    onViewModeChange?.(activeTab.id, primary.id, vm)
                    setMenuOpen(false)
                  }}
                  className={`w-full px-3 py-1 text-left text-[10px] cursor-pointer transition-colors hover:bg-surface-hover ${vm === viewMode ? 'text-white' : 'text-text-secondary'}`}
                >
                  {vm} {vm === viewMode && '\u2713'}
                </button>
              ))}
            </div>
          )}
        </div>
      </span>
    </div>
  )
}
