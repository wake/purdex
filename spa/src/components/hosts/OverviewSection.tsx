import { useEffect, useRef, useState } from 'react'
import { CaretDown, CaretRight, ArrowsClockwise, Trash, Plugs, Eye, EyeSlash, Check, X } from '@phosphor-icons/react'
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

/* ─── Connection error message ─── */

function connectionErrorMessage(runtime: HostRuntime | undefined, t: (key: string) => string): string | null {
  if (!runtime || runtime.status !== 'connected') {
    if (runtime?.daemonState === 'unreachable') return t('hosts.error_unreachable')
    if (runtime?.daemonState === 'refused') return t('hosts.error_refused')
    return null
  }
  if (runtime.tmuxState === 'unavailable') return t('hosts.error_tmux_down')
  return null
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
      setTestResult({ ok: false, error: err instanceof Error ? err.message : t('hosts.connection_failed') })
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
        {connectionErrorMessage(runtime, t) && (
          <div className="text-xs text-red-400 px-1 py-1">
            {connectionErrorMessage(runtime, t)}
          </div>
        )}

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
