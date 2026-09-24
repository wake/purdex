import { useEffect, useRef, useState } from 'react'
import { ArrowsClockwise, Trash, Plugs, LockSimple } from '@phosphor-icons/react'
import { requestAtOf, useHostStore, type HostInfo, type HostRuntime } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { hostFetch, fetchInfo, fetchHealth } from '../../lib/host-api'
import { HostDeleteRollbackIncompleteError, deleteHostWithUndoToast } from '../../lib/host-lifecycle'
import { useUndoToast } from '../../stores/useUndoToast'
import { connectionErrorMessage } from '../../lib/host-utils'
import type { ConfigData } from '../../lib/host-api'
import { Section, Field, EditableField, TokenField } from './form-fields'
import { HostColorField } from './HostColorField'
import { HostIconField } from './HostIconField'
import type { HostColorMode } from '../../lib/host-color'
import { hostLabel, hostLookOf, useHostLook } from '../../lib/host-look'

interface Props {
  hostId: string
}

const STATUS_LABEL_KEYS: Record<HostRuntime['status'], string> = {
  connected: 'hosts.status_value.connected',
  disconnected: 'hosts.status_value.disconnected',
  reconnecting: 'hosts.status_value.reconnecting',
  'auth-error': 'hosts.status_value.auth_error',
}

/* ─── Main component ─── */

export function OverviewSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const host = useHostStore((s) => s.hosts[hostId])
  const look = useHostLook(hostId)
  const runtime = useHostStore((s) => s.runtime[hostId])
  const updateHost = useHostStore((s) => s.updateHost)
  const setHostName = useHostStore((s) => s.setHostName)
  const hostOrder = useHostStore((s) => s.hostOrder)

  const [info, setInfo] = useState<HostInfo | null>(null)
  const [config, setConfig] = useState<ConfigData | null>(null)
  const [testResult, setTestResult] = useState<{ ok: boolean; latency?: number; error?: string } | null>(null)
  const [testing, setTesting] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [colorMode, setColorMode] = useState<HostColorMode>('console')

  const prevStatusRef = useRef(runtime?.status)
  useEffect(() => {
    const prev = prevStatusRef.current
    prevStatusRef.current = runtime?.status
    if (prev !== 'connected' && runtime?.status === 'connected') {
      setTestResult(null)
    }
  }, [runtime?.status])

  // Fetch info + config on mount or hostId change
  useEffect(() => {
    let cancelled = false
    const atRequest = useHostStore.getState().hosts[hostId]
    const at = atRequest ? requestAtOf(atRequest) : { endpoint: '', token: '' }
    fetchInfo(hostId)
      .then((r) => r.ok ? r.json() : null)
      .then((data) => {
        // Also this device's daemon-identity check (spec 2026-09-23 D4.3); guarded by the
        // endpoint + token captured above, so it holds even after unmount.
        if (data && typeof data.host_id === 'string') {
          useHostStore.getState().observeDaemonId(hostId, data.host_id, at)
        }
        if (!cancelled && data) setInfo(data)
      })
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
        // Test succeeded — also trigger SM reconnect so WS recovers
        const rt = useHostStore.getState().runtime[hostId]
        if (rt?.manualRetry) rt.manualRetry()
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
    const hostName = hostLabel(hostId, hostLookOf(hostId))
    setConfirmDelete(false)
    void deleteHostWithUndoToast(hostId, { deleted: t('hosts.deleted_toast', { name: hostName }), busy: t('hosts.delete_busy', { name: hostName }), stale: t('hosts.delete_stale', { name: hostName }) }).catch((err: unknown) => {
      // Said, not only logged: a deletion that failed was put back (nothing changed); one whose put-back failed too
      // may have left part of it behind — that notice stays until closed.
      console.error('[hosts] deleting a host failed', err)
      if (err instanceof HostDeleteRollbackIncompleteError) {
        useUndoToast.getState().show(t('hosts.delete_failed_incomplete', { name: hostName }), undefined, undefined, { persistent: true })
      } else {
        useUndoToast.getState().show(t('hosts.delete_failed', { name: hostName }))
      }
    })
  }

  const statusLabel = (r?: HostRuntime) => {
    const key = r ? STATUS_LABEL_KEYS[r.status] : undefined
    // A status the map does not know (a runtime value outside the type) shows raw.
    if (r && !key) return r.status
    return t(key ?? 'hosts.status_value.unknown')
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
      <h2 className="text-lg font-semibold mb-4">{look.name}</h2>

      {runtime?.status === 'auth-error' && (
        <div className="flex items-start gap-3 px-3 py-2.5 rounded-md mb-4 bg-red-500/10 border border-red-500/20">
          <LockSimple size={16} weight="fill" className="text-red-400 flex-shrink-0 mt-0.5" />
          <div>
            <p className="text-sm text-red-400 font-medium">{t('hosts.auth_error')}</p>
            <p className="text-xs text-text-muted mt-0.5">{t('hosts.auth_error_hint')}</p>
          </div>
        </div>
      )}

      {/* ─── Connection ─── */}
      <Section title={t('hosts.connection')}>
        <EditableField
          label={t('hosts.name')}
          value={look.name ?? ''}
          onSave={(v) => setHostName(hostId, v)}
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
          token={host.token ?? undefined}
          ip={host.ip}
          port={host.port}
          onSave={(token) => {
            updateHost(hostId, { token: token || undefined })
            // Auto-retry: set reconnecting then trigger SM
            useHostStore.getState().setRuntime(hostId, { status: 'reconnecting' })
            const rt = useHostStore.getState().runtime[hostId]
            if (rt?.manualRetry) rt.manualRetry()
          }}
          t={t}
        />
        <Field label={t('hosts.status')}>
          <span className={`text-sm ${
            runtime?.status === 'auth-error' ? 'text-red-400'
              : runtime?.status === 'connected' && runtime?.tmuxState === 'unavailable' ? 'text-yellow-400'
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
            <p className="text-xs text-zinc-400 mb-3">{t('hosts.delete_keeps_tabs')}</p>
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

      {/* ─── Appearance ─── */}
      <Section title={t('hosts.appearance')}>
        <HostColorField hostId={hostId} mode={colorMode} onModeChange={setColorMode} />
        <HostIconField hostId={hostId} mode={colorMode} />
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
                <option value="auto">{t('hosts.sizing_mode_option.auto')}</option>
                <option value="terminal-first">{t('hosts.sizing_mode_option.terminal_first')}</option>
                <option value="minimal-first">{t('hosts.sizing_mode_option.minimal_first')}</option>
              </select>
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
            <Field label="purdex">
              <span className="text-sm text-text-muted font-mono">{info.purdex_version}</span>
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
