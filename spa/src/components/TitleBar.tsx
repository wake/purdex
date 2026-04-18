import { Columns, Rows, GridFour, Square, SidebarSimple, SquareHalfBottom, Warning } from '@phosphor-icons/react'
import { useLocation } from 'wouter'
import { useTabStore } from '../stores/useTabStore'
import { useLayoutStore } from '../stores/useLayoutStore'
import { useSyncStore } from '../lib/sync/use-sync-store'
import { useI18nStore } from '../stores/useI18nStore'
import { pluralKey } from '../lib/plural'
import type { LayoutPattern } from '../types/tab'
import type { SidebarRegion } from '../types/layout'
import { CollapseButton } from '../features/workspace/components/CollapseButton'

interface Props { title: string }

const patterns: { pattern: LayoutPattern; icon: typeof Square; label: string }[] = [
  { pattern: 'single', icon: Square, label: 'Single pane' },
  { pattern: 'split-h', icon: Columns, label: 'Split horizontal' },
  { pattern: 'split-v', icon: Rows, label: 'Split vertical' },
  { pattern: 'grid-4', icon: GridFour, label: 'Grid' },
]

const regionToggles: { region: SidebarRegion; icon: typeof SidebarSimple; label: string; mirror?: boolean }[] = [
  { region: 'primary-sidebar', icon: SidebarSimple, label: 'Primary Sidebar' },
  { region: 'primary-panel', icon: SquareHalfBottom, label: 'Primary Panel' },
  { region: 'secondary-panel', icon: SquareHalfBottom, label: 'Secondary Panel', mirror: true },
  { region: 'secondary-sidebar', icon: SidebarSimple, label: 'Secondary Sidebar', mirror: true },
]

export function TitleBar({ title }: Props) {
  const activeTabId = useTabStore((s) => s.activeTabId)
  const regions = useLayoutStore((s) => s.regions)
  const toggleVisibility = useLayoutStore((s) => s.toggleVisibility)
  // Match SyncSection banner predicate exactly — otherwise the icon flashes
  // on states where the banner silently refuses to render, creating a
  // dead-end click path.
  const showSyncWarning = useSyncStore(
    (s) =>
      s.activeProviderId !== null &&
      s.pendingConflicts.length > 0 &&
      s.pendingRemoteBundle !== null &&
      s.pendingConflictsAt !== null,
  )
  const pendingCount = useSyncStore((s) => s.pendingConflicts.length)
  const t = useI18nStore((s) => s.t)
  const [, setLocation] = useLocation()

  const handlePattern = (pattern: LayoutPattern) => {
    if (!activeTabId) return
    useTabStore.getState().applyLayout(activeTabId, pattern)
  }

  return (
    <div
      className="shrink-0 relative flex items-center bg-surface-secondary border-b border-border-subtle px-2"
      style={{ height: 36, WebkitAppRegion: 'drag' } as React.CSSProperties}
    >
      {/* macOS traffic-light reserve (titleBarStyle='hiddenInset' draws them at x=12, y=12). */}
      <div className="w-[72px] shrink-0" aria-hidden="true" />
      <div
        data-testid="sidebar-toggle"
        className="shrink-0 flex items-center translate-y-[5px]"
        style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
        <CollapseButton variant="topbar" />
      </div>

      <div className="absolute inset-0 flex items-center justify-center pointer-events-none select-none px-2 gap-2">
        <span className="text-xs text-text-secondary truncate max-w-[calc(100%-27rem)]">{title}</span>
        {showSyncWarning && (
          <button
            aria-label={t(pluralKey('settings.sync.conflict.tooltip', pendingCount), { count: pendingCount })}
            title={t(pluralKey('settings.sync.conflict.tooltip', pendingCount), { count: pendingCount })}
            className="pointer-events-auto flex items-center shrink-0 cursor-pointer"
            style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
            onClick={() => setLocation('/settings/sync')}
          >
            <Warning size={14} className="text-yellow-500" />
          </button>
        )}
      </div>

      <div className="flex-1" />
      <div
        data-testid="layout-buttons"
        className="shrink-0 flex items-center gap-0.5 translate-y-[5px]"
        style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
        {regionToggles.map(({ region, icon: Icon, label, mirror }) => {
          const isVisible = regions[region].mode !== 'hidden'
          return (
            <button
              key={region}
              className={`p-1 rounded transition-colors cursor-pointer ${
                isVisible
                  ? 'text-accent-base bg-accent-base/10 hover:bg-accent-base/20'
                  : 'text-text-secondary hover:text-text-primary hover:bg-surface-hover'
              }`}
              title={label}
              onClick={() => toggleVisibility(region)}
              style={mirror ? { transform: 'scaleX(-1)' } : undefined}
            >
              <Icon size={14} />
            </button>
          )
        })}
        <div className="w-px h-3.5 bg-border-subtle mx-0.5" />
        {patterns.map(({ pattern, icon: Icon, label }) => (
          <button
            key={pattern}
            disabled={!activeTabId}
            className="p-1 rounded cursor-pointer text-text-secondary hover:text-text-primary hover:bg-surface-hover disabled:opacity-40 disabled:pointer-events-none"
            title={label}
            onClick={() => handlePattern(pattern)}
          >
            <Icon size={14} />
          </button>
        ))}
      </div>
    </div>
  )
}
