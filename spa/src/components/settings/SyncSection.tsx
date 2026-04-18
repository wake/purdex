import { useRef, useState } from 'react'
import {
  ArrowsClockwise,
  CheckCircle,
  DownloadSimple,
  Upload,
  Warning,
  WarningCircle,
} from '@phosphor-icons/react'
import { SettingItem } from './SettingItem'
import { SegmentControl } from './SegmentControl'
import { SyncConflictBanner } from './SyncConflictBanner'
import { useSyncStore } from '../../lib/sync/use-sync-store'
import { syncEngine } from '../../lib/sync/register-sync'
import { createManualProvider, ImportError } from '../../lib/sync/providers/manual-provider'
import { createDaemonProvider } from '../../lib/sync/providers/daemon-provider'
import { applyImport, syncNow, type SyncActionResult } from '../../lib/sync/sync-actions'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { pluralKey } from '../../lib/plural'

type ProviderId = 'off' | 'daemon' | 'file'

type StatusTone = 'idle' | 'busy' | 'success' | 'warn' | 'error'
interface Status { tone: StatusTone; message: string }
const IDLE: Status = { tone: 'idle', message: '' }

function formatRelativeTime(t: ReturnType<typeof useI18nStore.getState>['t'], ms: number): string {
  const diffSec = Math.floor((Date.now() - ms) / 1000)
  if (diffSec < 60) return t('settings.sync.time.secondsAgo', { n: diffSec })
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return t('settings.sync.time.minutesAgo', { n: diffMin })
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return t('settings.sync.time.hoursAgo', { n: diffHr })
  const diffDay = Math.floor(diffHr / 24)
  return t('settings.sync.time.daysAgo', { n: diffDay })
}

function triggerDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

export function SyncSection() {
  const t = useI18nStore((s) => s.t)

  const activeProviderId = useSyncStore((s) => s.activeProviderId)
  const setActiveProvider = useSyncStore((s) => s.setActiveProvider)
  const lastSyncedAt = useSyncStore((s) => s.lastSyncedAt)
  const lastSyncedBundle = useSyncStore((s) => s.lastSyncedBundle)
  const setLastSyncedBundle = useSyncStore((s) => s.setLastSyncedBundle)
  const enabledModules = useSyncStore((s) => s.enabledModules)
  const toggleModule = useSyncStore((s) => s.toggleModule)
  const getClientId = useSyncStore((s) => s.getClientId)
  const syncHostId = useSyncStore((s) => s.syncHostId)
  const setSyncHostId = useSyncStore((s) => s.setSyncHostId)
  const pendingConflicts = useSyncStore((s) => s.pendingConflicts)
  const pendingRemoteBundle = useSyncStore((s) => s.pendingRemoteBundle)
  const pendingConflictsAt = useSyncStore((s) => s.pendingConflictsAt)
  const setPendingConflicts = useSyncStore((s) => s.setPendingConflicts)
  const clearPendingConflicts = useSyncStore((s) => s.clearPendingConflicts)

  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)

  const fileInputRef = useRef<HTMLInputElement>(null)
  const [status, setStatus] = useState<Status>(IDLE)
  const [busy, setBusy] = useState(false)

  const PROVIDER_OPTIONS: { value: ProviderId; label: string }[] = [
    { value: 'off', label: t('settings.sync.provider.off') },
    { value: 'daemon', label: t('settings.sync.provider.daemon') },
    { value: 'file', label: t('settings.sync.provider.file') },
  ]

  const currentProvider: ProviderId = (activeProviderId as ProviderId | null) ?? 'off'

  const handleProviderChange = (value: ProviderId) => {
    setActiveProvider(value === 'off' ? null : value)
    setStatus(IDLE)
  }

  const contributors = syncEngine.getContributors()

  const statusFromResult = (result: SyncActionResult, okMessage: string): Status => {
    if (result.kind === 'ok') return { tone: 'success', message: okMessage }
    if (result.kind === 'conflicts') {
      const n = result.conflicts.length
      return {
        tone: 'warn',
        message: t(pluralKey('settings.sync.status.conflictsPending', n), { count: n }),
      }
    }
    return { tone: 'error', message: result.error }
  }

  const handleSyncNow = async () => {
    if (busy) return
    if (currentProvider !== 'daemon') {
      setStatus({ tone: 'warn', message: t('settings.sync.status.onlyDaemon') })
      return
    }
    if (!syncHostId || !hosts[syncHostId]) {
      setStatus({ tone: 'warn', message: t('settings.sync.status.selectHost') })
      return
    }

    setBusy(true)
    setStatus({ tone: 'busy', message: t('settings.sync.status.syncing') })

    const clientId = getClientId()
    const provider = createDaemonProvider(syncHostId, clientId)
    const result = await syncNow({
      provider,
      clientId,
      lastSyncedBundle,
      enabledModules,
      engine: syncEngine,
    })

    if (result.kind === 'ok') {
      setLastSyncedBundle(result.appliedBundle)
    } else if (result.kind === 'conflicts') {
      setLastSyncedBundle(result.partialBaseline)
      setPendingConflicts(result.conflicts, result.remoteBundle)
    }

    setStatus(statusFromResult(result, t('settings.sync.status.complete')))
    setBusy(false)
  }

  const handleExportAll = () => {
    if (busy) return
    const clientId = getClientId()
    const bundle = syncEngine.serialize(clientId, enabledModules)
    const provider = createManualProvider()
    const blob = provider.exportToBlob(bundle)
    const timestamp = new Date().toISOString().replace(/[:.]/g, '-')
    triggerDownload(blob, `purdex-sync-${timestamp}.purdex-sync`)
    setStatus({ tone: 'success', message: t('settings.sync.status.exported') })
  }

  const handleImportClick = () => fileInputRef.current?.click()

  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    if (busy) return

    setBusy(true)
    setStatus({ tone: 'busy', message: t('settings.sync.status.syncing') })

    try {
      const text = await file.text()
      const provider = createManualProvider()
      const bundle = provider.importFromText(text)

      const result = await applyImport({
        bundle,
        lastSyncedBundle,
        enabledModules,
        engine: syncEngine,
      })

      if (result.kind === 'ok') {
        setLastSyncedBundle(result.appliedBundle)
      } else if (result.kind === 'conflicts') {
        setLastSyncedBundle(result.partialBaseline)
        setPendingConflicts(result.conflicts, result.remoteBundle)
      }

      setStatus(statusFromResult(result, t('settings.sync.status.importApplied')))
    } catch (err) {
      let friendly: string
      if (err instanceof ImportError) {
        switch (err.code) {
          case 'too-large':
            friendly = t('settings.sync.import.error.tooLarge', { mb: 5 })
            break
          case 'too-deep':
            friendly = t('settings.sync.import.error.tooDeep', { depth: 32 })
            break
          default:
            friendly = t('settings.sync.status.importFailed', { reason: err.message })
        }
      } else {
        const msg = err instanceof Error ? err.message : String(err)
        friendly = t('settings.sync.status.importFailed', { reason: msg })
      }
      setStatus({ tone: 'error', message: friendly })
    } finally {
      if (fileInputRef.current) fileInputRef.current.value = ''
      setBusy(false)
    }
  }

  const handleResolveConflicts = (resolved: Record<string, 'local' | 'remote'>) => {
    if (!pendingRemoteBundle) return
    const count = pendingConflicts.length
    syncEngine.resolveConflicts(pendingRemoteBundle, pendingConflicts, resolved)
    setLastSyncedBundle(pendingRemoteBundle)
    clearPendingConflicts()
    setStatus({ tone: 'success', message: t(pluralKey('settings.sync.conflict.resolved', count), { count }) })
  }

  return (
    <div>
      <h2 className="text-lg text-text-primary">{t('settings.section.sync')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.sync.description')}</p>

      {currentProvider !== 'off' && pendingConflicts.length > 0 && pendingRemoteBundle && pendingConflictsAt !== null && (
        <SyncConflictBanner
          conflicts={pendingConflicts}
          remoteBundle={pendingRemoteBundle}
          pendingAt={pendingConflictsAt}
          onResolve={handleResolveConflicts}
          onDismiss={clearPendingConflicts}
        />
      )}

      <SettingItem
        label={t('settings.sync.provider.label')}
        description={t('settings.sync.provider.description')}
      >
        <SegmentControl options={PROVIDER_OPTIONS} value={currentProvider} onChange={handleProviderChange} />
      </SettingItem>

      {currentProvider !== 'off' && (
        <>
          {currentProvider === 'daemon' && (
            <SettingItem
              label={t('settings.sync.host.label')}
              description={t('settings.sync.host.description')}
            >
              <select
                value={syncHostId ?? ''}
                onChange={(e) => setSyncHostId(e.target.value || null)}
                className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-60 hover:border-text-muted focus:border-border-active focus:outline-none"
              >
                <option value="">{t('settings.sync.host.placeholder')}</option>
                {hostOrder.map((id) => {
                  const host = hosts[id]
                  if (!host) return null
                  return (
                    <option key={id} value={id}>
                      {t('settings.sync.host.option', { name: host.name, ip: host.ip, port: host.port })}
                    </option>
                  )
                })}
              </select>
            </SettingItem>
          )}

          <SettingItem
            label={t('settings.sync.status.label')}
            description={
              lastSyncedAt
                ? t('settings.sync.status.lastSynced', { time: formatRelativeTime(t, lastSyncedAt) })
                : t('settings.sync.status.neverSynced')
            }
          >
            <button
              onClick={handleSyncNow}
              disabled={busy}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <ArrowsClockwise size={14} className={busy ? 'animate-spin' : ''} />
              {t('settings.sync.syncNow')}
            </button>
          </SettingItem>

          {contributors.length > 0 && (
            <SettingItem
              label={t('settings.sync.modules.label')}
              description={t('settings.sync.modules.description')}
            >
              <div className="flex flex-col gap-2">
                {contributors.map((contributor) => {
                  const checked = enabledModules.includes(contributor.id)
                  return (
                    <label key={contributor.id} className="flex items-center gap-2 cursor-pointer text-xs text-text-primary">
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={() => toggleModule(contributor.id)}
                        className="accent-border-active"
                      />
                      {contributor.id}
                    </label>
                  )
                })}
              </div>
            </SettingItem>
          )}

          <SettingItem
            label={t('settings.sync.ioActions.label')}
            description={t('settings.sync.ioActions.description')}
          >
            <div className="flex items-center gap-2">
              <button
                onClick={handleExportAll}
                disabled={busy}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <DownloadSimple size={14} />
                {t('settings.sync.ioActions.exportAll')}
              </button>
              <button
                onClick={handleImportClick}
                disabled={busy}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <Upload size={14} />
                {t('settings.sync.ioActions.import')}
              </button>
              <input
                ref={fileInputRef}
                type="file"
                accept=".purdex-sync,.json"
                className="hidden"
                onChange={handleFileChange}
              />
            </div>
          </SettingItem>

          <StatusLine status={status} />
        </>
      )}
    </div>
  )
}

function StatusLine({ status }: { status: Status }) {
  if (status.tone === 'idle' || !status.message) return null
  const Icon =
    status.tone === 'success' ? CheckCircle
    : status.tone === 'warn' ? Warning
    : status.tone === 'error' ? WarningCircle
    : ArrowsClockwise
  const color =
    status.tone === 'success' ? 'text-green-500'
    : status.tone === 'warn' ? 'text-yellow-500'
    : status.tone === 'error' ? 'text-red-500'
    : 'text-text-secondary'
  return (
    <div className={`flex items-start gap-1.5 mt-3 text-xs ${color}`}>
      <Icon size={14} className={status.tone === 'busy' ? 'animate-spin mt-0.5' : 'mt-0.5'} />
      <span>{status.message}</span>
    </div>
  )
}
