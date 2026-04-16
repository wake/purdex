import { useRef } from 'react'
import { ArrowsClockwise, DownloadSimple, Upload } from '@phosphor-icons/react'
import { SettingItem } from './SettingItem'
import { SegmentControl } from './SegmentControl'
import { useSyncStore } from '../../lib/sync/use-sync-store'
import { syncEngine } from '../../lib/sync/register-sync'
import { createManualProvider } from '../../lib/sync/providers/manual-provider'

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

// ---------------------------------------------------------------------------
// SyncSection
// ---------------------------------------------------------------------------

export function SyncSection() {
  const activeProviderId = useSyncStore((s) => s.activeProviderId)
  const setActiveProvider = useSyncStore((s) => s.setActiveProvider)
  const lastSyncedAt = useSyncStore((s) => s.lastSyncedAt)
  const enabledModules = useSyncStore((s) => s.enabledModules)
  const toggleModule = useSyncStore((s) => s.toggleModule)
  const getClientId = useSyncStore((s) => s.getClientId)

  const fileInputRef = useRef<HTMLInputElement>(null)

  // Normalise: null → 'off'
  const currentProvider: ProviderId =
    (activeProviderId as ProviderId | null) ?? 'off'

  const handleProviderChange = (value: ProviderId) => {
    setActiveProvider(value === 'off' ? null : value)
  }

  const contributors = syncEngine.getContributors()

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
    try {
      const text = await file.text()
      const provider = createManualProvider()
      const bundle = provider.importFromText(text)
      // Full import flow will be wired in a future task
      console.log('[SyncSection] Imported bundle:', bundle)
    } catch (err) {
      console.error('[SyncSection] Import failed:', err)
    } finally {
      // Reset so the same file can be re-selected
      if (fileInputRef.current) fileInputRef.current.value = ''
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
              onClick={() => {
                // Actual sync logic will be wired in a future task
                console.log('[SyncSection] Sync Now triggered')
              }}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active"
            >
              <ArrowsClockwise size={14} />
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
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active"
              >
                <DownloadSimple size={14} />
                Export All
              </button>
              <button
                onClick={handleImportClick}
                className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active"
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
        </>
      )}
    </div>
  )
}
