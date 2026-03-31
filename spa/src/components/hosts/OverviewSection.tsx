import { useEffect, useState } from 'react'
import { CaretDown, CaretRight, ArrowsClockwise, Trash, Plugs } from '@phosphor-icons/react'
import { useHostStore, type HostInfo, type HostRuntime } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { hostFetch, fetchInfo, fetchHealth } from '../../lib/host-api'
import type { ConfigData } from '../../lib/api'

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

  useEffect(() => { setDraft(value) }, [value])

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
        onBlur={() => { onSave(draft); setEditing(false) }}
        onKeyDown={(e) => {
          if (e.key === 'Enter') { onSave(draft); setEditing(false) }
          if (e.key === 'Escape') { setDraft(value); setEditing(false) }
        }}
        autoFocus
      />
    </Field>
  )
}

/* ─── Main component ─── */

export function OverviewSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const host = useHostStore((s) => s.hosts[hostId])
  const runtime = useHostStore((s) => s.runtime[hostId])
  const updateHost = useHostStore((s) => s.updateHost)
  const removeHost = useHostStore((s) => s.removeHost)
  const hostOrder = useHostStore((s) => s.hostOrder)

  const [info, setInfo] = useState<HostInfo | null>(null)
  const [config, setConfig] = useState<ConfigData | null>(null)
  const [testResult, setTestResult] = useState<{ ok: boolean; latency?: number; error?: string } | null>(null)
  const [testing, setTesting] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)

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
      setTestResult({ ok: false, error: err instanceof Error ? err.message : 'Connection failed' })
    } finally {
      setTesting(false)
    }
  }

  const handleDeleteHost = () => {
    removeHost(hostId)
  }

  const statusLabel = (r?: HostRuntime) => {
    if (!r) return 'unknown'
    return r.status
  }

  const handleConfigSave = async (updates: Partial<ConfigData>) => {
    try {
      const daemonBase = useHostStore.getState().getDaemonBase(hostId)
      const res = await fetch(`${daemonBase}/api/config`, {
        method: 'PUT',
        headers: {
          'Content-Type': 'application/json',
          ...useHostStore.getState().getAuthHeaders(hostId),
        },
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
        <Field label={t('hosts.token')}>
          <span className="text-sm text-text-muted font-mono">
            {host.token ? '••••••••' : '—'}
          </span>
        </Field>
        <Field label={t('hosts.status')}>
          <span className={`text-sm ${runtime?.status === 'connected' ? 'text-green-400' : 'text-red-400'}`}>
            {statusLabel(runtime)}
            {runtime?.latency != null && ` (${runtime.latency}ms)`}
          </span>
        </Field>

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
                value={config.terminal?.sizing_mode ?? 'fit'}
                onChange={(e) => handleConfigSave({ terminal: { sizing_mode: e.target.value } })}
                className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary"
              >
                <option value="fit">fit</option>
                <option value="fixed">fixed</option>
                <option value="manual">manual</option>
              </select>
            </Field>
            <Field label={t('hosts.stream_presets')}>
              <span className="text-sm text-text-muted">
                {config.stream?.presets?.length ?? 0} preset(s)
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
