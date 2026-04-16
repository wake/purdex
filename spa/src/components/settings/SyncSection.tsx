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
import { useSyncStore } from '../../lib/sync/use-sync-store'
import { syncEngine } from '../../lib/sync/register-sync'
import { createManualProvider } from '../../lib/sync/providers/manual-provider'
import { createDaemonProvider } from '../../lib/sync/providers/daemon-provider'
import { applyImport, syncNow, type SyncActionResult } from '../../lib/sync/sync-actions'
import { useHostStore } from '../../stores/useHostStore'

// ---------------------------------------------------------------------------
// Provider selector type
// ---------------------------------------------------------------------------

type ProviderId = 'off' | 'daemon' | 'file'

const PROVIDER_OPTIONS: { value: ProviderId; label: string }[] = [
  { value: 'off', label: 'Off' },
  { value: 'daemon', label: 'Daemon' },
  { value: 'file', label: 'File' },
]

// ---------------------------------------------------------------------------
// Status shape (ephemeral, UI-only)
// ---------------------------------------------------------------------------

type StatusTone = 'idle' | 'busy' | 'success' | 'warn' | 'error'

interface Status {
  tone: StatusTone
  message: string
}

const IDLE: Status = { tone: 'idle', message: '' }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatRelativeTime(ms: number): string {
  const diffSec = Math.floor((Date.now() - ms) / 1000)
  if (diffSec < 60) return `${diffSec}s ago`
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  const diffDay = Math.floor(diffHr / 24)
  return `${diffDay}d ago`
}

function triggerDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

function statusFromResult(result: SyncActionResult, okMessage: string): Status {
  if (result.kind === 'ok') return { tone: 'success', message: okMessage }
  if (result.kind === 'conflicts') {
    return {
      tone: 'warn',
      message: `${result.conflicts.length} field conflict(s) detected. Resolution UI coming soon; local data preserved.`,
    }
  }
  return { tone: 'error', message: result.error }
}

// ---------------------------------------------------------------------------
// SyncSection
// ---------------------------------------------------------------------------

export function SyncSection() {
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

  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)

  const fileInputRef = useRef<HTMLInputElement>(null)
  const [status, setStatus] = useState<Status>(IDLE)
  const [busy, setBusy] = useState(false)

  // Normalise: null → 'off'
  const currentProvider: ProviderId =
    (activeProviderId as ProviderId | null) ?? 'off'

  const handleProviderChange = (value: ProviderId) => {
    setActiveProvider(value === 'off' ? null : value)
    setStatus(IDLE)
  }

  const contributors = syncEngine.getContributors()

  // --------------------------------------------------------------------------
  // Sync Now
  // --------------------------------------------------------------------------

  const handleSyncNow = async () => {
    if (busy) return

    if (currentProvider !== 'daemon') {
      setStatus({
        tone: 'warn',
        message: 'Sync Now is only available with the Daemon provider for now.',
      })
      return
    }

    if (!syncHostId || !hosts[syncHostId]) {
      setStatus({ tone: 'warn', message: 'Select a sync host first.' })
      return
    }

    setBusy(true)
    setStatus({ tone: 'busy', message: 'Syncing…' })

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
      // engine partial-applied non-conflicting contributors; advance their
      // baseline so the next sync doesn't rebase them against a stale ancestor.
      setLastSyncedBundle(result.partialBaseline)
    }

    setStatus(statusFromResult(result, 'Sync complete.'))
    setBusy(false)
  }

  // --------------------------------------------------------------------------
  // Export
  // --------------------------------------------------------------------------

  const handleExportAll = () => {
    const clientId = getClientId()
    const bundle = syncEngine.serialize(clientId, enabledModules)
    const provider = createManualProvider()
    const blob = provider.exportToBlob(bundle)
    const timestamp = new Date().toISOString().replace(/[:.]/g, '-')
    triggerDownload(blob, `purdex-sync-${timestamp}.purdex-sync`)
    setStatus({ tone: 'success', message: 'Exported.' })
  }

  // --------------------------------------------------------------------------
  // Import
  // --------------------------------------------------------------------------

  const handleImportClick = () => {
    fileInputRef.current?.click()
  }

  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    if (busy) return

    setBusy(true)
    setStatus({ tone: 'busy', message: 'Importing…' })

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
      }

      setStatus(statusFromResult(result, 'Import applied.'))
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      setStatus({ tone: 'error', message: `Import failed: ${message}` })
    } finally {
      if (fileInputRef.current) fileInputRef.current.value = ''
      setBusy(false)
    }
  }

  // --------------------------------------------------------------------------
  // Render
  // --------------------------------------------------------------------------

  return (
    <div>
      <h2 className="text-lg text-text-primary">Sync</h2>
      <p className="text-xs text-text-secondary mb-6">
        Synchronise settings and workspaces across devices.
      </p>

      {/* Provider selector */}
      <SettingItem
        label="Provider"
        description="Select where sync data is stored and exchanged."
      >
        <SegmentControl
          options={PROVIDER_OPTIONS}
          value={currentProvider}
          onChange={handleProviderChange}
        />
      </SettingItem>

      {currentProvider !== 'off' && (
        <>
          {/* Sync host selector (Daemon only) */}
          {currentProvider === 'daemon' && (
            <SettingItem
              label="Sync Host"
              description="Daemon to push and pull sync data through."
            >
              <select
                value={syncHostId ?? ''}
                onChange={(e) => setSyncHostId(e.target.value || null)}
                className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-60 hover:border-text-muted focus:border-border-active focus:outline-none"
              >
                <option value="">— Select —</option>
                {hostOrder.map((id) => {
                  const host = hosts[id]
                  if (!host) return null
                  return (
                    <option key={id} value={id}>
                      {host.name} ({host.ip}:{host.port})
                    </option>
                  )
                })}
              </select>
            </SettingItem>
          )}

          {/* Sync status */}
          <SettingItem
            label="Sync Status"
            description={
              lastSyncedAt
                ? `Last sync: ${formatRelativeTime(lastSyncedAt)}`
                : 'Never synced'
            }
          >
            <button
              onClick={handleSyncNow}
              disabled={busy}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <ArrowsClockwise size={14} className={busy ? 'animate-spin' : ''} />
              Sync Now
            </button>
          </SettingItem>

          {/* Module checkboxes */}
          {contributors.length > 0 && (
            <SettingItem
              label="Modules"
              description="Choose which data to include in sync."
            >
              <div className="flex flex-col gap-2">
                {contributors.map((contributor) => {
                  const checked = enabledModules.includes(contributor.id)
                  return (
                    <label
                      key={contributor.id}
                      className="flex items-center gap-2 cursor-pointer text-xs text-text-primary"
                    >
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

          {/* Export / Import */}
          <SettingItem
            label="Export / Import"
            description="Manually export or import a sync bundle."
          >
            <div className="flex items-center gap-2">
              <button
                onClick={handleExportAll}
                disabled={busy}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <DownloadSimple size={14} />
                Export All
              </button>
              <button
                onClick={handleImportClick}
                disabled={busy}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <Upload size={14} />
                Import
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

// ---------------------------------------------------------------------------
// StatusLine
// ---------------------------------------------------------------------------

function StatusLine({ status }: { status: Status }) {
  if (status.tone === 'idle' || !status.message) return null

  const Icon =
    status.tone === 'success'
      ? CheckCircle
      : status.tone === 'warn'
      ? Warning
      : status.tone === 'error'
      ? WarningCircle
      : ArrowsClockwise

  const color =
    status.tone === 'success'
      ? 'text-green-500'
      : status.tone === 'warn'
      ? 'text-yellow-500'
      : status.tone === 'error'
      ? 'text-red-500'
      : 'text-text-secondary'

  return (
    <div className={`flex items-start gap-1.5 mt-3 text-xs ${color}`}>
      <Icon
        size={14}
        className={status.tone === 'busy' ? 'animate-spin mt-0.5' : 'mt-0.5'}
      />
      <span>{status.message}</span>
    </div>
  )
}
