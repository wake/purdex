import { useState } from 'react'
import { Plus, Play, Trash, PencilSimple, Check, X } from '@phosphor-icons/react'
import { useSessionStore } from '../../stores/useSessionStore'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useAgentStore, type AgentStatus } from '../../stores/useAgentStore'
import { hostFetch, renameSession } from '../../lib/host-api'
import { compositeKey } from '../../lib/composite-key'
import { connectionErrorMessage } from '../../lib/host-utils'
import { SessionLauncher } from '../session-launcher/SessionLauncher'
import type { Session } from '../../lib/host-api'

interface Props {
  hostId: string
}

// Shared fallback so the selector returns a stable reference when the host has
// no sessions entry yet — a fresh `[]` per call makes useSyncExternalStore loop.
const EMPTY_SESSIONS: Session[] = []

// The store casts the wire value to AgentStatus, so an unknown one can still
// arrive; the badge falls back to showing it raw.
const AGENT_STATUS_LABEL_KEYS: Record<AgentStatus, string> = {
  running: 'hosts.agent_status.running',
  waiting: 'hosts.agent_status.waiting',
  idle: 'hosts.agent_status.idle',
  error: 'hosts.agent_status.error',
}

/* ─── Inline Rename ─── */

function InlineRename({ hostId, session, onDone }: { hostId: string; session: Session; onDone: () => void }) {
  const [draft, setDraft] = useState(session.name)

  const handleSave = async () => {
    if (draft.trim() && draft !== session.name) {
      await renameSession(hostId, session.code, draft.trim())
    }
    onDone()
  }

  return (
    <span className="inline-flex items-center gap-1">
      <input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        className="bg-surface-primary border border-border-default rounded px-1 py-0.5 text-sm w-32"
        autoFocus
        onBlur={handleSave}
        onKeyDown={(e) => {
          if (e.key === 'Enter') handleSave()
          if (e.key === 'Escape') onDone()
        }}
      />
      <button onClick={handleSave} className="text-green-400 cursor-pointer"><Check size={14} /></button>
      <button onClick={onDone} className="text-text-muted cursor-pointer"><X size={14} /></button>
    </span>
  )
}

/* ─── Main component ─── */

export function SessionsSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const sessions = useSessionStore((s) => s.sessions[hostId] ?? EMPTY_SESSIONS)
  const runtime = useHostStore((s) => s.runtime[hostId])
  const isOffline = !runtime || runtime.status !== 'connected' || runtime.tmuxState === 'unavailable'
  const [showNew, setShowNew] = useState(false)
  const [renamingCode, setRenamingCode] = useState<string | null>(null)
  const [deletingCode, setDeletingCode] = useState<string | null>(null)
  const agentStatuses = useAgentStore((s) => s.statuses)

  const handleOpen = (session: Session) => {
    const tabId = useTabStore.getState().openSingletonTab({
      kind: 'tmux-session',
      hostId,
      sessionCode: session.code,
      mode: 'terminal',
      cachedName: session.name,
      // Generation from the session payload we are opening, never from
      // ambient host state (spec §4.5).
      tmuxInstance: session.tmux_instance ?? '',
    })
    useWorkspaceStore.getState().insertTab(tabId)
    useTabStore.getState().setActiveTab(tabId)
  }

  const handleDelete = async (code: string) => {
    try {
      await hostFetch(hostId, `/api/sessions/${code}`, { method: 'DELETE' })
    } catch { /* will be removed by next WS sync */ }
    setDeletingCode(null)
  }

  return (
    <div className="max-w-2xl">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">{t('hosts.sessions')}</h2>
        <button
          onClick={() => setShowNew((v) => !v)}
          disabled={isOffline}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
        >
          <Plus size={14} />
          {t('hosts.new_session')}
        </button>
      </div>

      {/* Host page semantics: creating only. The new session lands in the table
          below through the next sessions payload; no tab is opened. */}
      {showNew && (
        <div className="mb-4">
          <SessionLauncher
            hostId={hostId}
            disabled={isOffline}
            onLaunched={() => setShowNew(false)}
            onCancel={() => setShowNew(false)}
          />
        </div>
      )}

      {(() => {
        const errorMsg = isOffline ? connectionErrorMessage(runtime, t) : null
        return errorMsg && (
          <div className="text-xs text-red-400 px-3 py-2 mb-2">
            {errorMsg}
          </div>
        )
      })()}

      {sessions.length === 0 ? (
        <p className="text-sm text-text-muted">{t('hosts.no_sessions')}</p>
      ) : (
        <div className="border border-border-subtle rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-surface-tertiary text-text-secondary text-xs">
                <th className="text-left px-3 py-2">{t('hosts.session_name')}</th>
                <th className="text-left px-3 py-2">{t('hosts.agent')}</th>
                <th className="text-left px-3 py-2">{t('hosts.cwd')}</th>
                <th className="text-right px-3 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((session) => {
                const agent = agentStatuses[compositeKey(hostId, session.code)]
                return (
                  <tr key={session.code} className="border-t border-border-subtle hover:bg-surface-secondary/30">
                    <td className="px-3 py-2">
                      {renamingCode === session.code ? (
                        <InlineRename hostId={hostId} session={session} onDone={() => setRenamingCode(null)} />
                      ) : (
                        <span className="text-text-primary">{session.name}</span>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      {agent ? (
                        <span className={`text-xs px-1.5 py-0.5 rounded ${
                          agent === 'running' ? 'bg-green-500/20 text-green-400'
                            : agent === 'error' ? 'bg-red-500/20 text-red-400'
                            : 'bg-surface-tertiary text-text-muted'
                        }`}>
                          {AGENT_STATUS_LABEL_KEYS[agent] ? t(AGENT_STATUS_LABEL_KEYS[agent]) : agent}
                        </span>
                      ) : (
                        <span className="text-text-muted">—</span>
                      )}
                    </td>
                    <td className="px-3 py-2 text-text-muted font-mono text-xs truncate max-w-[200px]">{session.cwd}</td>
                    <td className="px-3 py-2 text-right">
                      <div className="flex items-center justify-end gap-1">
                        <button
                          onClick={() => handleOpen(session)}
                          disabled={isOffline}
                          title={t('hosts.open')}
                          className="p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-accent cursor-pointer disabled:opacity-50"
                        >
                          <Play size={14} />
                        </button>
                        <button
                          onClick={() => setRenamingCode(session.code)}
                          disabled={isOffline}
                          title={t('hosts.rename')}
                          className="p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-50"
                        >
                          <PencilSimple size={14} />
                        </button>
                        {deletingCode === session.code ? (
                          <span className="flex items-center gap-1 text-xs">
                            <button
                              onClick={() => handleDelete(session.code)}
                              className="text-red-400 cursor-pointer"
                            >
                              <Check size={14} />
                            </button>
                            <button
                              onClick={() => setDeletingCode(null)}
                              className="text-text-muted cursor-pointer"
                            >
                              <X size={14} />
                            </button>
                          </span>
                        ) : (
                          <button
                            onClick={() => setDeletingCode(session.code)}
                            disabled={isOffline}
                            title={t('hosts.delete_session')}
                            className="p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-red-400 cursor-pointer disabled:opacity-50"
                          >
                            <Trash size={14} />
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
