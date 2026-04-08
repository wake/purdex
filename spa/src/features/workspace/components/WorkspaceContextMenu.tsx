import { useEffect, useState } from 'react'
import { GearSix, ArrowSquareOut, ArrowSquareIn } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  position: { x: number; y: number }
  onSettings: () => void
  onTearOff?: () => void
  onMergeTo?: (targetWindowId: string) => void
  onClose: () => void
}

export function WorkspaceContextMenu({ position, onSettings, onTearOff, onMergeTo, onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  const [windowList, setWindowList] = useState<ElectronWindowInfo[] | null>(null)

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  // Load window list when onMergeTo is provided
  useEffect(() => {
    if (!onMergeTo) return
    if (!window.electronAPI?.getWindows) return
    window.electronAPI.getWindows().then(setWindowList)
  }, [onMergeTo])

  const showTearOff = !!onTearOff
  const showMerge = !!onMergeTo
  const hasWindows = windowList !== null && windowList.length > 0
  const isLoadingWindows = showMerge && !!window.electronAPI?.getWindows && windowList === null

  const showSeparator = showTearOff || showMerge

  return (
    <>
      <div data-testid="context-menu-backdrop" className="fixed inset-0 z-40" onMouseDown={onClose} />
      <div
        className="fixed z-50 min-w-44 bg-surface-secondary border border-border-default rounded-lg shadow-xl py-1"
        style={{ left: position.x, top: position.y }}
      >
        {/* Settings */}
        <button
          onClick={() => { onSettings(); onClose() }}
          className="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer transition-colors"
        >
          <GearSix size={14} />
          {t('nav.settings') ?? 'Settings'}
        </button>

        {/* Separator before window management actions */}
        {showSeparator && (
          <div className="border-t border-border-default my-1" />
        )}

        {/* Tear off — move workspace to new window */}
        {showTearOff && (
          <button
            onClick={() => { onTearOff!(); onClose() }}
            className="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer transition-colors"
          >
            <ArrowSquareOut size={14} />
            {t('workspace.tear_off') ?? 'Move to New Window'}
          </button>
        )}

        {/* Merge to — loading state */}
        {showMerge && isLoadingWindows && (
          <button
            disabled
            className="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-text-secondary opacity-50 cursor-not-allowed"
          >
            <ArrowSquareIn size={14} />
            {t('workspace.merge_loading') ?? 'Loading...'}
          </button>
        )}

        {/* Merge to — window list loaded, has windows */}
        {showMerge && !isLoadingWindows && hasWindows && (
          <>
            <div className="px-3 py-1 text-xs text-text-muted font-medium flex items-center gap-2">
              <ArrowSquareIn size={14} />
              {t('workspace.merge_to') ?? 'Move to Window'}
            </div>
            {windowList!.map((win) => (
              <button
                key={win.id}
                onClick={() => { onMergeTo!(win.id); onClose() }}
                className="w-full flex items-center gap-2 pl-8 pr-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer transition-colors"
              >
                {win.title}
              </button>
            ))}
          </>
        )}
      </div>
    </>
  )
}
