import { CheckCircle, XCircle, DownloadSimple, Trash, WarningCircle, CircleNotch } from '@phosphor-icons/react'
import { useModuleHook } from '../../hooks/useModuleHook'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { HookModule } from '../../lib/hook-modules'

interface Props {
  module: HookModule
  hostId: string
  refreshKey: number
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

function formatRelativeTime(ts: number, t: (key: string, p?: Record<string, string | number>) => string): string {
  const diff = Date.now() - ts / 1_000_000 // broadcast_ts is nanoseconds
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return t('hosts.hook_just_now')
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return t('hosts.hook_minutes_ago', { n: minutes })
  const hours = Math.floor(minutes / 60)
  return t('hosts.hook_hours_ago', { n: hours })
}

export function HookModuleCard({ module, hostId, refreshKey }: Props) {
  const t = useI18nStore((s) => s.t)
  const isOffline = useHostStore((s) => {
    const rt = s.runtime[hostId]
    return rt != null && rt.status !== 'connected'
  })

  const { status, loading, error, setup, lastTrigger } = useModuleHook(module, hostId, refreshKey)

  const eventEntries = status ? Object.entries(status.events) : []

  return (
    <div className="p-4 bg-surface-secondary rounded-lg border border-border-subtle">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold">{t(module.labelKey)}</h3>
        {status && <StatusBadge installed={status.installed} t={t} />}
      </div>
      <p className="text-xs text-text-muted mb-3">{t(module.descKey)}</p>

      {status && (status.agentVersion || status.supportedVersion) && (
        <div className="text-xs text-text-muted mb-3 space-y-1">
          {status.agentVersion && (
            <div>
              <span className="text-text-secondary">{t('hosts.hook_agent_version')}:</span> <span className="font-mono">{status.agentVersion}</span>
            </div>
          )}
          {status.supportedVersion && (
            <div>
              <span className="text-text-secondary">{t('hosts.hook_supported_version')}:</span> <span className="font-mono">{status.supportedVersion}</span>
            </div>
          )}
          {status.exceedsSupport && (
            <div className="flex items-center gap-1 text-yellow-400">
              <WarningCircle size={12} />
              <span>{t('hosts.hook_version_warning')}</span>
            </div>
          )}
        </div>
      )}

      {loading && !status && !error && (
        <div className="flex items-center gap-1.5 text-xs text-text-muted mb-3">
          <CircleNotch size={14} className="animate-spin" />
          <span>{t('hosts.loading')}</span>
        </div>
      )}

      {error && (
        <div className="flex items-center gap-1.5 text-xs text-red-400 mb-3">
          <WarningCircle size={14} />
          <span>{error}</span>
        </div>
      )}

      {status && status.upgradesAvailable && status.upgradesAvailable.length > 0 && (
        <div className="flex items-center gap-1 text-xs text-amber-400 mb-3">
          <WarningCircle size={12} />
          <span>{t('hosts.hook_upgrade_available', { n: status.upgradesAvailable.length })}</span>
        </div>
      )}

      {status && eventEntries.length > 0 && (
        <div className="space-y-1 mb-3">
          {eventEntries.map(([event, detail]) => {
            // FutureOnly + not installed → neutral "FutureOnly" badge
            // (tolerated absent). Avoids the red-light pressure Finding #4
            // warned about for pre-expansion users.
            const isToleratedFutureOnly = detail.futureOnly && !detail.installed
            return (
              <div key={event} className="flex items-center gap-3 text-xs py-1">
                <span className="text-text-secondary w-40 shrink-0 font-mono">{event}</span>
                {isToleratedFutureOnly ? (
                  <span className="inline-flex items-center gap-1 text-text-muted">
                    <WarningCircle size={12} />
                    FutureOnly
                  </span>
                ) : (
                  <span className={`inline-flex items-center gap-1 ${detail.installed ? 'text-green-400' : 'text-text-muted'}`}>
                    {detail.installed ? <CheckCircle size={12} /> : <XCircle size={12} />}
                    {detail.installed ? t('hosts.installed') : t('hosts.not_installed')}
                  </span>
                )}
                {lastTrigger?.[event] && (
                  <span className="text-text-muted ml-auto">
                    {formatRelativeTime(lastTrigger[event], t)}
                  </span>
                )}
              </div>
            )
          })}
        </div>
      )}

      {status?.issues && status.issues.length > 0 && (
        <div className="text-xs text-yellow-400 mb-3 space-y-0.5">
          {status.issues.map((issue, i) => (
            <div key={i} className="flex items-center gap-1">
              <WarningCircle size={12} />
              <span>{issue}</span>
            </div>
          ))}
        </div>
      )}

      <div className="flex gap-2">
        <button
          onClick={() => setup('install')}
          // Install is disabled only when the install is fully complete
          // (installed=true AND no FutureOnly upgrades pending). Finding
          // #4: legacy 3-event users must still be able to trigger an
          // upgrade even while installed=true.
          disabled={
            isOffline ||
            loading ||
            (!!status?.installed && !(status?.upgradesAvailable && status.upgradesAvailable.length > 0))
          }
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
        >
          <DownloadSimple size={14} />
          {t('hosts.install')}
        </button>
        <button
          onClick={() => setup('remove')}
          // Remove is enabled whenever there is a pdx artifact on disk,
          // not only when installed=true. Finding #2: drifted-but-
          // managed state (opencode plugin body mismatch, stale codex
          // legacy entry) still needs to be removable.
          disabled={isOffline || loading || !status?.managed}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-red-500/10 text-red-400 border border-red-500/30 cursor-pointer disabled:opacity-50"
        >
          <Trash size={14} />
          {t('hosts.remove')}
        </button>
      </div>
    </div>
  )
}
