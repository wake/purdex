import { useEffect, useState } from 'react'
import { ArrowsClockwise, CheckCircle, XCircle, DownloadSimple, Trash } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { fetchHooksStatus, installHooks, removeHooks } from '../../lib/host-api'

interface Props {
  hostId: string
}

interface HooksStatus {
  tmux_hooks_installed: boolean
  agent_hooks_installed: boolean
  hooks?: Array<{ event: string; command: string }>
}

function StatusBadge({ installed, t }: { installed: boolean; t: (key: string) => string }) {
  return (
    <span className={`inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded ${
      installed ? 'bg-green-500/20 text-green-400' : 'bg-surface-tertiary text-text-muted'
    }`}>
      {installed ? <CheckCircle size={12} /> : <XCircle size={12} />}
      {installed ? t('hosts.installed') : t('hosts.not_installed')}
    </span>
  )
}

export function HooksSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const runtime = useHostStore((s) => s.runtime[hostId])
  const isOffline = runtime?.status !== 'connected'
  const [status, setStatus] = useState<HooksStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [actionLoading, setActionLoading] = useState(false)

  const refresh = async () => {
    setLoading(true)
    try {
      const res = await fetchHooksStatus(hostId)
      if (res.ok) setStatus(await res.json())
    } catch { /* ignore */ }
    setLoading(false)
  }

  useEffect(() => {
    let cancelled = false
    fetchHooksStatus(hostId)
      .then((res) => res.ok ? res.json() : null)
      .then((data) => { if (!cancelled && data) setStatus(data) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [hostId])

  const handleInstall = async () => {
    setActionLoading(true)
    try {
      const res = await installHooks(hostId)
      if (res.ok) await refresh()
    } catch { /* ignore */ }
    setActionLoading(false)
  }

  const handleRemove = async () => {
    setActionLoading(true)
    try {
      const res = await removeHooks(hostId)
      if (res.ok) await refresh()
    } catch { /* ignore */ }
    setActionLoading(false)
  }

  return (
    <div className="max-w-2xl">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">{t('hosts.hooks')}</h2>
        <button
          onClick={refresh}
          disabled={isOffline || loading}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary border border-border-default text-text-secondary cursor-pointer disabled:opacity-50"
        >
          <ArrowsClockwise size={14} className={loading ? 'animate-spin' : ''} />
          {t('hosts.check_status')}
        </button>
      </div>

      {status ? (
        <div className="space-y-4">
          {/* tmux hooks */}
          <div className="p-4 bg-surface-secondary rounded-lg border border-border-subtle">
            <div className="flex items-center justify-between mb-2">
              <h3 className="text-sm font-semibold">{t('hosts.tmux_hooks')}</h3>
              <StatusBadge installed={status.tmux_hooks_installed} t={t} />
            </div>
            <p className="text-xs text-text-muted mb-3">{t('hosts.tmux_hooks_desc')}</p>
            <div className="flex gap-2">
              <button
                onClick={handleInstall}
                disabled={isOffline || actionLoading || status.tmux_hooks_installed}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
              >
                <DownloadSimple size={14} />
                {t('hosts.install')}
              </button>
              <button
                onClick={handleRemove}
                disabled={isOffline || actionLoading || !status.tmux_hooks_installed}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-red-500/10 text-red-400 border border-red-500/30 cursor-pointer disabled:opacity-50"
              >
                <Trash size={14} />
                {t('hosts.remove')}
              </button>
            </div>
          </div>

          {/* Agent hooks */}
          <div className="p-4 bg-surface-secondary rounded-lg border border-border-subtle">
            <div className="flex items-center justify-between mb-2">
              <h3 className="text-sm font-semibold">{t('hosts.agent_hooks')}</h3>
              <StatusBadge installed={status.agent_hooks_installed} t={t} />
            </div>
            <p className="text-xs text-text-muted">{t('hosts.agent_hooks_desc')}</p>
          </div>

          {/* Hook events list */}
          {status.hooks && status.hooks.length > 0 && (
            <div className="p-4 bg-surface-secondary rounded-lg border border-border-subtle">
              <h3 className="text-sm font-semibold mb-2">{t('hosts.hook_events')}</h3>
              <div className="space-y-1">
                {status.hooks.map((hook, i) => (
                  <div key={i} className="flex items-center gap-3 text-xs py-1">
                    <span className="text-text-secondary w-40 shrink-0 font-mono">{hook.event}</span>
                    <span className="text-text-muted font-mono truncate">{hook.command}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      ) : (
        <div className="flex items-center gap-2 text-xs text-text-muted">
          <ArrowsClockwise size={12} className={loading ? 'animate-spin' : ''} />
          {loading ? t('hosts.loading') : t('hosts.load_failed')}
        </div>
      )}
    </div>
  )
}
