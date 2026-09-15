import { useEffect } from 'react'
import { WarningCircle } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { EMPTY_HOST_CONFIG, useHostConfigStore, type HostConfigEntry } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'

export interface GateNotice { key: string; params?: Record<string, string> }

export interface HostConfigGate { entry: HostConfigEntry; editable: boolean; notice: GateNotice | null }

/**
 * Shared by Projects / Commands: loads on mount (spec §4.1) and decides
 * whether editing is allowed. Offline or an old daemon → notice, read-only.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function useHostConfigGate(hostId: string): HostConfigGate {
  const entry = useHostConfigStore((s) => s.byHost[hostId] ?? EMPTY_HOST_CONFIG)
  const online = useHostStore((s) => s.runtime[hostId]?.status === 'connected')

  useEffect(() => {
    if (online) void useHostConfigStore.getState().load(hostId)
  }, [hostId, online])

  let notice: GateNotice | null = null
  if (!online) notice = { key: 'host_config.offline' }
  else if (entry.status === 'unsupported') notice = { key: 'host_config.unsupported' }
  else if (entry.status === 'error') notice = { key: 'host_config.load_failed', params: { reason: entry.error ?? '' } }
  else if (entry.status !== 'ready') notice = { key: 'host_config.loading' }

  return { entry, editable: online && entry.status === 'ready', notice }
}

export function HostConfigNotice({ notice }: { notice: GateNotice | null }) {
  const t = useI18nStore((s) => s.t)
  if (!notice) return null
  return (
    <div data-testid="host-config-notice" data-notice={notice.key} className="mb-3 flex items-center gap-1.5 text-xs text-text-secondary">
      <WarningCircle size={14} className="shrink-0" />
      {t(notice.key, notice.params)}
    </div>
  )
}
