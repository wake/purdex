import { useState, useRef, useCallback, useEffect } from 'react'
import { CaretUp, CircleNotch, CheckCircle, XCircle, LockSimple } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { compositeKey } from '../lib/composite-key'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  activeTab: Tab | null
  onViewModeChange?: (tabId: string, paneId: string, mode: 'terminal' | 'stream') => void
  onNavigateToHost?: (hostId: string) => void
}

const VIEW_MODE_COLORS: Record<string, string> = {
  terminal: 'bg-green-900/40 text-green-400 border-green-700/50',
  stream: 'bg-blue-900/40 text-blue-400 border-blue-700/50',
}

function UploadStatus({ hostId, sessionCode, t }: { hostId: string | null; sessionCode: string | null; t: (key: string, params?: Record<string, string | number>) => string }) {
  const ck = hostId && sessionCode ? compositeKey(hostId, sessionCode) : null
  const uploadState = useUploadStore((s) => ck ? s.sessions[ck] : undefined)
  const setDone = useUploadStore((s) => s.setDone)
  const dismiss = useUploadStore((s) => s.dismiss)
  const uploadStatus = uploadState?.status

  // Auto-transition "typing" → "done" after 1.5 seconds
  useEffect(() => {
    if (uploadStatus !== 'typing' || !hostId || !sessionCode) return
    const timer = setTimeout(() => setDone(hostId, sessionCode), 1500)
    return () => clearTimeout(timer)
  }, [uploadStatus, hostId, sessionCode, setDone])

  // Auto-dismiss "done" after 3 seconds
  useEffect(() => {
    if (uploadStatus !== 'done' || !hostId || !sessionCode) return
    const timer = setTimeout(() => dismiss(hostId, sessionCode), 3000)
    return () => clearTimeout(timer)
  }, [uploadStatus, hostId, sessionCode, dismiss])

  // Auto-dismiss "error" after 30 seconds
  useEffect(() => {
    if (uploadStatus !== 'error' || !hostId || !sessionCode) return
    const timer = setTimeout(() => dismiss(hostId, sessionCode), 30000)
    return () => clearTimeout(timer)
  }, [uploadStatus, hostId, sessionCode, dismiss])

  if (!uploadState || !hostId || !sessionCode) return null

  if (uploadState.status === 'uploading') {
    return (
      <span className="flex items-center gap-1 text-yellow-400" data-testid="upload-status">
        <CircleNotch size={12} className="animate-spin" />
        <span>{t('upload.uploading', { file: uploadState.currentFile, current: uploadState.completed + 1, total: uploadState.total })}</span>
      </span>
    )
  }

  if (uploadState.status === 'typing') {
    return (
      <span className="flex items-center gap-1 text-blue-400" data-testid="upload-status">
        <CircleNotch size={12} className="animate-spin" />
        <span>{t('upload.typing')}</span>
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
        onClick={() => dismiss(hostId!, sessionCode!)}
      >
        <XCircle size={12} />
        <span>{message}</span>
      </span>
    )
  }

  return null
}

export function StatusBar({ activeTab, onViewModeChange, onNavigateToHost }: Props) {
  const t = useI18nStore((s) => s.t)
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  // Read agent event for the active session (hooks must be called unconditionally)
  const primaryContent = activeTab?.layout
    ? getPrimaryPane(activeTab.layout).content
    : null
  const agentHostId = primaryContent && primaryContent.kind === 'tmux-session' ? primaryContent.hostId : null
  const agentSessionCode = primaryContent && 'sessionCode' in primaryContent ? primaryContent.sessionCode : null
  const agentCk = agentHostId && agentSessionCode ? compositeKey(agentHostId, agentSessionCode) : null

  const session = useSessionStore((s) =>
    agentHostId && agentSessionCode
      ? (s.sessions[agentHostId] ?? []).find((sess) => sess.code === agentSessionCode) ?? null
      : null,
  )
  const hostConfig = useHostStore((s) => agentHostId ? s.hosts[agentHostId] : null)
  const hostRuntime = useHostStore((s) => agentHostId ? s.runtime[agentHostId] : null)
  const agentLabel = useAgentStore((s) => agentCk ? s.models[agentCk] ?? null : null)
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

  if (content.kind !== 'tmux-session') {
    return (
      <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0">
        <span>{content.kind}</span>
      </div>
    )
  }

  // Session pane — show host, session name, status, viewMode toggle
  const sessionName = session?.name ?? content.sessionCode
  const hostName = hostConfig?.name ?? 'Unknown'
  const status = hostRuntime?.status ?? 'disconnected'

  const viewMode = content.mode
  const viewModes: ('terminal' | 'stream')[] = ['terminal', 'stream']

  return (
    <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted gap-3 flex-shrink-0 relative z-10">
      <span className="text-text-secondary">{hostName}</span>
      <span className="text-text-secondary">{sessionName}</span>
      <span
        className={
          status === 'auth-error' ? 'text-red-400 cursor-pointer flex items-center gap-1'
            : status === 'connected' && hostRuntime?.tmuxState === 'unavailable' ? 'text-yellow-400'
            : status === 'connected' ? 'text-green-500'
            : status === 'reconnecting' ? 'text-yellow-400'
            : 'text-red-400'
        }
        onClick={status === 'auth-error' && agentHostId ? () => onNavigateToHost?.(agentHostId) : undefined}
      >
        {status === 'auth-error' && <LockSimple size={10} weight="fill" />}
        {status === 'auth-error' ? t('hosts.auth_error')
          : status === 'connected' && hostRuntime?.tmuxState === 'unavailable'
            ? t('hosts.error_tmux_down')
            : status}
      </span>
      {agentLabel && (
        <span className="px-[7px] rounded-[3px] border text-[10px] leading-4 bg-[rgba(154,96,56,0.15)] text-[#e8956a] border-[rgba(180,110,65,0.3)]" data-testid="agent-label">
          {agentLabel}
        </span>
      )}
      <UploadStatus hostId={agentHostId} sessionCode={agentSessionCode} t={t} />
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
