import { useEffect, useRef, useState } from 'react'
import { CaretDown, CaretRight, ArrowsClockwise, Trash, Plugs, Eye, EyeSlash, Check, X } from '@phosphor-icons/react'
import { useHostStore, type HostConfig, type HostInfo, type HostRuntime } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useAgentStore, type AgentHookEvent, type AgentStatus } from '../../stores/useAgentStore'
import { useStreamStore, type PerSessionState } from '../../stores/useStreamStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { hostFetch, fetchInfo, fetchHealth } from '../../lib/host-api'
import { scanPaneTree } from '../../lib/pane-tree'
import { connectionErrorMessage } from '../../lib/host-utils'
import type { ConfigData } from '../../lib/api'
import type { Session } from '../../lib/api'
import type { Tab } from '../../types/tab'

interface Props {
  hostId: string
}

/* ─── Collapsible section wrapper ─── */

function Section({ title, defaultOpen = true, children }: { title: string; defaultOpen?: boolean; children: React.ReactNode }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="mb-6">
      <button
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="flex items-center gap-2 text-sm font-semibold text-text-primary mb-3 cursor-pointer"
      >
        {open ? <CaretDown size={12} /> : <CaretRight size={12} />}
        {title}
      </button>
      {open && children}
    </div>
  )
}

/* ─── Field row ─── */

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-4 py-2">
      <span className="text-xs text-text-secondary w-32 shrink-0">{label}</span>
      <div className="flex-1">{children}</div>
    </div>
  )
}

/* ─── Editable text field ─── */

function EditableField({ label, value, onSave }: { label: string; value: string; onSave: (v: string) => void }) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value)
  const savedRef = useRef(false)

  useEffect(() => { setDraft(value) }, [value])
  useEffect(() => { if (editing) savedRef.current = false }, [editing])

  const save = () => {
    if (savedRef.current) return
    savedRef.current = true
    onSave(draft)
    setEditing(false)
  }

  if (!editing) {
    return (
      <Field label={label}>
        <span
          className="text-sm text-text-primary cursor-pointer hover:text-accent"
          onClick={() => setEditing(true)}
        >
          {value || '—'}
        </span>
      </Field>
    )
  }

  return (
    <Field label={label}>
      <input
        className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary w-full max-w-xs"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => {
          if (e.key === 'Enter') save()
          if (e.key === 'Escape') { setDraft(value); setEditing(false) }
        }}
        autoFocus
      />
    </Field>
  )
}

/* ─── Token field with validation ─── */

function TokenField({ token, ip, port, onSave, t }: {
  token?: string
  ip: string
  port: number
  onSave: (token: string) => void
  t: (key: string) => string
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(token ?? '')
  const [visible, setVisible] = useState(false)
  const [validating, setValidating] = useState(false)
  const [error, setError] = useState('')

  const handleSave = async () => {
    if (draft === (token ?? '')) {
      setEditing(false)
      return
    }
    // Empty token: skip validation, just save
    if (!draft.trim()) {
      setError('')
      onSave(draft)
      setEditing(false)
      return
    }
    // Validate token by testing /api/sessions with it
    setValidating(true)
    setError('')
    try {
      const base = `http://${ip}:${port}`
      const headers: Record<string, string> = {}
      if (draft) headers['Authorization'] = `Bearer ${draft}`
      const res = await fetch(`${base}/api/sessions`, { headers })
      if (res.ok) {
        onSave(draft)
        setEditing(false)
        setVisible(false)
      } else if (res.status === 401) {
        setError(t('hosts.invalid_token'))
      } else {
        setError(`HTTP ${res.status}`)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t('hosts.connection_failed'))
    } finally {
      setValidating(false)
    }
  }

  const handleCancel = () => {
    setDraft(token ?? '')
    setEditing(false)
    setError('')
  }

  if (!editing) {
    return (
      <Field label={t('hosts.token')}>
        <span className="inline-flex items-center gap-2">
          <span className="text-sm text-text-muted font-mono">
            {token ? (visible ? token : '••••••••') : '—'}
          </span>
          {token && (
            <button
              onClick={() => setVisible(!visible)}
              aria-label={visible ? t('hosts.hide_token') : t('hosts.show_token')}
              className="text-text-muted hover:text-text-secondary cursor-pointer"
            >
              {visible ? <EyeSlash size={14} /> : <Eye size={14} />}
            </button>
          )}
          <button
            onClick={() => { setDraft(token ?? ''); setEditing(true) }}
            className="text-xs text-accent hover:text-accent/80 cursor-pointer"
          >
            {t('common.edit')}
          </button>
        </span>
      </Field>
    )
  }

  return (
    <Field label={t('hosts.token')}>
      <div className="space-y-1">
        <div className="flex items-center gap-1">
          <input
            type={visible ? 'text' : 'password'}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="tbox_..."
            className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary font-mono w-full max-w-xs"
            autoFocus
            onKeyDown={(e) => {
              if (e.key === 'Enter') handleSave()
              if (e.key === 'Escape') handleCancel()
            }}
          />
          <button
            onClick={() => setVisible(!visible)}
            aria-label={visible ? t('hosts.hide_token') : t('hosts.show_token')}
            className="text-text-muted hover:text-text-secondary cursor-pointer p-1"
          >
            {visible ? <EyeSlash size={14} /> : <Eye size={14} />}
          </button>
          <button
            onClick={handleSave}
            disabled={validating}
            aria-label={t('common.save')}
            className="text-green-400 hover:text-green-300 cursor-pointer p-1 disabled:opacity-50"
          >
            <Check size={14} />
          </button>
          <button
            onClick={handleCancel}
            aria-label={t('common.cancel')}
            className="text-text-muted hover:text-text-secondary cursor-pointer p-1"
          >
            <X size={14} />
          </button>
        </div>
        {validating && <p className="text-xs text-text-muted">{t('hosts.validating_token')}</p>}
        {error && <p className="text-xs text-red-400">{error}</p>}
        <p className="text-xs text-text-muted">{t('hosts.token_hint')}</p>
      </div>
    </Field>
  )
}

/* ─── Main component ─── */

export function OverviewSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const host = useHostStore((s) => s.hosts[hostId])
  const runtime = useHostStore((s) => s.runtime[hostId])
  const updateHost = useHostStore((s) => s.updateHost)
  const hostOrder = useHostStore((s) => s.hostOrder)

  const [info, setInfo] = useState<HostInfo | null>(null)
  const [config, setConfig] = useState<ConfigData | null>(null)
  const [testResult, setTestResult] = useState<{ ok: boolean; latency?: number; error?: string } | null>(null)
  const [testing, setTesting] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [closeTabs, setCloseTabs] = useState(true)

  // Fetch info + config on mount or hostId change
  useEffect(() => {
    let cancelled = false
    fetchInfo(hostId)
      .then((r) => r.ok ? r.json() : null)
      .then((data) => { if (!cancelled && data) setInfo(data) })
      .catch(() => {})

    hostFetch(hostId, '/api/config')
      .then((r) => r.ok ? r.json() : null)
      .then((data) => { if (!cancelled && data) setConfig(data) })
      .catch(() => {})

    return () => { cancelled = true }
  }, [hostId])

  if (!host) return null

  const handleTestConnection = async () => {
    setTesting(true)
    setTestResult(null)
    const start = performance.now()
    try {
      const res = await fetchHealth(hostId)
      const latency = Math.round(performance.now() - start)
      if (res.ok) {
        setTestResult({ ok: true, latency })
      } else {
        setTestResult({ ok: false, error: `HTTP ${res.status}` })
      }
    } catch (err) {
      setTestResult({ ok: false, error: err instanceof Error ? err.message : t('hosts.connection_failed') })
    } finally {
      setTesting(false)
    }
  }

  const handleDeleteHost = () => {
    const hostStore = useHostStore.getState()
    const tabStore = useTabStore.getState()
    const sessionStore = useSessionStore.getState()
    const agentStore = useAgentStore.getState()
    const streamStore = useStreamStore.getState()

    const hostName = hostStore.hosts[hostId]?.name ?? hostId
    const prefix = `${hostId}:`

    // --- Snapshot for undo (serializable data only) ---
    const snapshot: {
      host: HostConfig | undefined
      hostOrder: string[]
      sessions: Session[] | undefined
      activeHostId: string | null
      // AgentStore data (exclude transient activeSubagents)
      agentEvents: Record<string, AgentHookEvent>
      agentStatuses: Record<string, AgentStatus>
      agentUnread: Record<string, boolean>
      // StreamStore data (exclude non-serializable conn)
      streamSessions: Record<string, Omit<PerSessionState, 'conn'>>
      // Tab data for undo
      closedTabs: Tab[]
      terminatedTabPaneIds: { tabId: string; paneId: string }[]
    } = {
      host: hostStore.hosts[hostId],
      hostOrder: [...hostStore.hostOrder],
      sessions: sessionStore.sessions[hostId],
      activeHostId: hostStore.activeHostId,
      agentEvents: {},
      agentStatuses: {},
      agentUnread: {},
      streamSessions: {},
      closedTabs: [],
      terminatedTabPaneIds: [],
    }

    // Snapshot AgentStore entries for this host
    for (const [k, v] of Object.entries(agentStore.events)) {
      if (k.startsWith(prefix)) snapshot.agentEvents[k] = v
    }
    for (const [k, v] of Object.entries(agentStore.statuses)) {
      if (k.startsWith(prefix)) snapshot.agentStatuses[k] = v
    }
    for (const [k, v] of Object.entries(agentStore.unread)) {
      if (k.startsWith(prefix)) snapshot.agentUnread[k] = v
    }

    // Snapshot StreamStore entries for this host (exclude conn)
    for (const [k, v] of Object.entries(streamStore.sessions)) {
      if (k.startsWith(prefix)) {
        const { conn: _, ...serializable } = v // eslint-disable-line @typescript-eslint/no-unused-vars
        snapshot.streamSessions[k] = serializable
      }
    }

    // Execute cascade: tabs -> sessions -> agent -> stream -> host
    if (closeTabs) {
      // Close all tmux-session tabs for this host (scan ALL panes, not just primary)
      for (const [tabId, tab] of Object.entries(tabStore.tabs)) {
        let hasHostPane = false
        scanPaneTree(tab.layout, (pane) => {
          if (pane.content.kind === 'tmux-session' && pane.content.hostId === hostId) {
            hasHostPane = true
          }
        })
        if (hasHostPane) {
          snapshot.closedTabs.push(tab)
          tabStore.closeTab(tabId)
        }
      }
    } else {
      // Track which panes will be marked terminated (for undo)
      for (const [tabId, tab] of Object.entries(tabStore.tabs)) {
        scanPaneTree(tab.layout, (pane) => {
          if (pane.content.kind === 'tmux-session' && pane.content.hostId === hostId && !pane.content.terminated) {
            snapshot.terminatedTabPaneIds.push({ tabId, paneId: pane.id })
          }
        })
      }
      // Mark all tmux-session tabs as terminated
      tabStore.markHostTerminated(hostId, 'host-removed')
    }

    sessionStore.removeHost(hostId)
    agentStore.removeHost(hostId)
    streamStore.clearHost(hostId)
    hostStore.removeHost(hostId)

    setConfirmDelete(false)

    // Show undo toast via global store
    useUndoToast.getState().show(
      t('hosts.deleted_toast', { name: hostName }),
      () => {
        // --- Restore host + hostOrder position ---
        if (snapshot.host) {
          useHostStore.getState().addHost(snapshot.host)
          // Restore original hostOrder position
          useHostStore.getState().reorderHosts(snapshot.hostOrder)
          if (snapshot.activeHostId === hostId) {
            useHostStore.getState().setActiveHost(hostId)
          }
        }

        // --- Restore sessions ---
        if (snapshot.sessions) {
          useSessionStore.getState().replaceHost(hostId, snapshot.sessions)
        }

        // --- Restore AgentStore data ---
        const ag = useAgentStore.getState()
        if (Object.keys(snapshot.agentEvents).length > 0) {
          useAgentStore.setState({
            events: { ...ag.events, ...snapshot.agentEvents },
            statuses: { ...ag.statuses, ...snapshot.agentStatuses },
            unread: { ...ag.unread, ...snapshot.agentUnread },
          })
        }

        // --- Restore StreamStore data (conn set to null) ---
        if (Object.keys(snapshot.streamSessions).length > 0) {
          const st = useStreamStore.getState()
          const restored: Record<string, PerSessionState> = {}
          for (const [k, v] of Object.entries(snapshot.streamSessions)) {
            restored[k] = { ...v, conn: null }
          }
          useStreamStore.setState({
            sessions: { ...st.sessions, ...restored },
          })
        }

        // --- Restore tabs ---
        if (closeTabs && snapshot.closedTabs.length > 0) {
          const ts = useTabStore.getState()
          for (const tab of snapshot.closedTabs) {
            // Only restore if tab wasn't re-created by user during undo window
            if (!ts.tabs[tab.id]) {
              useTabStore.getState().addTab(tab)
            }
          }
        } else if (!closeTabs && snapshot.terminatedTabPaneIds.length > 0) {
          // Clear terminated marking on panes that were marked by this delete
          for (const { tabId, paneId } of snapshot.terminatedTabPaneIds) {
            const currentTab = useTabStore.getState().tabs[tabId]
            if (!currentTab) continue
            // Find the pane and clear its terminated field
            let found = false
            scanPaneTree(currentTab.layout, (pane) => {
              if (pane.id === paneId && pane.content.kind === 'tmux-session' && pane.content.terminated === 'host-removed') {
                found = true
              }
            })
            if (found) {
              // Re-read to get current content and remove terminated
              scanPaneTree(useTabStore.getState().tabs[tabId].layout, (pane) => {
                if (pane.id === paneId && pane.content.kind === 'tmux-session' && pane.content.terminated === 'host-removed') {
                  const { terminated: _, ...contentWithoutTerminated } = pane.content // eslint-disable-line @typescript-eslint/no-unused-vars
                  useTabStore.getState().setPaneContent(tabId, paneId, contentWithoutTerminated as typeof pane.content)
                }
              })
            }
          }
        }
      },
    )
  }

  const statusLabel = (r?: HostRuntime) => {
    if (!r) return 'unknown'
    return r.status
  }

  const handleConfigSave = async (updates: Partial<ConfigData>) => {
    try {
      const res = await hostFetch(hostId, '/api/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(updates),
      })
      if (res.ok) {
        const data = await res.json()
        setConfig(data)
      }
    } catch { /* ignore */ }
  }

  return (
    <div className="max-w-2xl space-y-2">
      <h2 className="text-lg font-semibold mb-4">{host.name}</h2>

      {/* ─── Connection ─── */}
      <Section title={t('hosts.connection')}>
        <EditableField
          label={t('hosts.name')}
          value={host.name}
          onSave={(v) => updateHost(hostId, { name: v })}
        />
        <EditableField
          label={t('hosts.ip')}
          value={host.ip}
          onSave={(v) => updateHost(hostId, { ip: v })}
        />
        <EditableField
          label={t('hosts.port')}
          value={String(host.port)}
          onSave={(v) => updateHost(hostId, { port: parseInt(v, 10) || 7860 })}
        />
        <TokenField
          token={host.token}
          ip={host.ip}
          port={host.port}
          onSave={(token) => updateHost(hostId, { token: token || undefined })}
          t={t}
        />
        <Field label={t('hosts.status')}>
          <span className={`text-sm ${
            runtime?.status === 'connected' && runtime?.tmuxState === 'unavailable' ? 'text-yellow-400'
              : runtime?.status === 'connected' ? 'text-green-400'
              : runtime?.status === 'reconnecting' ? 'text-yellow-400'
              : 'text-red-400'
          }`}>
            {statusLabel(runtime)}
            {runtime?.latency != null && ` (${runtime.latency}ms)`}
          </span>
        </Field>
        {(() => {
          const errorMsg = connectionErrorMessage(runtime, t)
          return errorMsg && (
            <div className="text-xs text-red-400 px-1 py-1">
              {errorMsg}
            </div>
          )
        })()}

        <div className="flex gap-2 mt-3">
          <button
            onClick={handleTestConnection}
            disabled={testing}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary border border-border-default text-text-secondary cursor-pointer disabled:opacity-50"
          >
            <Plugs size={14} />
            {testing ? t('hosts.testing') : t('hosts.test_connection')}
          </button>
          {hostOrder.length > 1 && (
            <button
              onClick={() => setConfirmDelete(true)}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-red-500/10 hover:bg-red-500/20 border border-red-500/30 text-red-400 cursor-pointer"
            >
              <Trash size={14} />
              {t('hosts.delete')}
            </button>
          )}
        </div>

        {testResult && (
          <div className={`mt-2 text-xs ${testResult.ok ? 'text-green-400' : 'text-red-400'}`}>
            {testResult.ok
              ? `✓ ${t('hosts.connected')} (${testResult.latency}ms)`
              : `✗ ${testResult.error}`}
          </div>
        )}

        {confirmDelete && (
          <div className="mt-3 p-3 bg-red-500/10 border border-red-500/30 rounded">
            <p className="text-xs text-red-400 mb-2">{t('hosts.confirm_delete')}</p>
            <label className="flex items-center gap-2 text-xs text-zinc-400 mb-3 cursor-pointer">
              <input
                type="checkbox"
                checked={closeTabs}
                onChange={(e) => setCloseTabs(e.target.checked)}
                className="rounded"
              />
              {t('hosts.confirm_delete_tabs')}
            </label>
            <div className="flex gap-2">
              <button
                onClick={handleDeleteHost}
                className="px-3 py-1 rounded text-xs bg-red-500 text-white cursor-pointer"
              >
                {t('hosts.delete')}
              </button>
              <button
                onClick={() => setConfirmDelete(false)}
                className="px-3 py-1 rounded text-xs bg-surface-secondary text-text-secondary cursor-pointer"
              >
                {t('common.cancel')}
              </button>
            </div>
          </div>
        )}
      </Section>

      {/* ─── Daemon Config ─── */}
      <Section title={t('hosts.daemon_config')}>
        {config ? (
          <>
            <Field label={t('hosts.sizing_mode')}>
              <select
                value={config.terminal?.sizing_mode ?? 'auto'}
                onChange={(e) => handleConfigSave({ terminal: { sizing_mode: e.target.value } })}
                className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary"
              >
                <option value="auto">auto</option>
                <option value="terminal-first">terminal-first</option>
                <option value="minimal-first">minimal-first</option>
              </select>
            </Field>
            <Field label={t('hosts.stream_presets')}>
              <span className="text-sm text-text-muted">
                {t('hosts.preset_count', { count: config.stream?.presets?.length ?? 0 })}
              </span>
            </Field>
            <Field label={t('hosts.detect_commands')}>
              <span className="text-sm text-text-muted font-mono">
                {config.detect?.cc_commands?.join(', ') || '—'}
              </span>
            </Field>
            <Field label={t('hosts.poll_interval')}>
              <span className="text-sm text-text-muted">
                {config.detect?.poll_interval ?? '—'}s
              </span>
            </Field>
          </>
        ) : (
          <p className="text-xs text-text-muted">{t('hosts.loading')}</p>
        )}
      </Section>

      {/* ─── System Info ─── */}
      <Section title={t('hosts.system_info')}>
        {info ? (
          <>
            <Field label="tbox">
              <span className="text-sm text-text-muted font-mono">{info.tbox_version}</span>
            </Field>
            <Field label="tmux">
              <span className="text-sm text-text-muted font-mono">{info.tmux_version}</span>
            </Field>
            <Field label="OS">
              <span className="text-sm text-text-muted font-mono">{info.os} / {info.arch}</span>
            </Field>
          </>
        ) : (
          <div className="flex items-center gap-2 text-xs text-text-muted">
            <ArrowsClockwise size={12} className="animate-spin" />
            {t('hosts.loading')}
          </div>
        )}
      </Section>
    </div>
  )
}
