import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { Sliders, ArrowSquareOut, ArrowSquareIn } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { TITLE_BAR_HEIGHT } from '../../../components/FloatingPanel'

const PADDING = 4

interface Props {
  position: { x: number; y: number }
  onSettings: () => void
  onTearOff?: () => void
  onMergeTo?: (targetWindowId: string) => void
  onClose: () => void
}

export function WorkspaceContextMenu({
  position,
  onSettings,
  onTearOff,
  onMergeTo,
  onClose,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const [windowList, setWindowList] = useState<ElectronWindowInfo[] | null>(null)
  const ref = useRef<HTMLDivElement>(null)

  // Viewport boundary correction, as in TabContextMenu — directly adjust DOM
  // before paint (no state needed). Also re-run when the window list arrives:
  // the "Loading…" row turning into one row per window changes the height.
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    // Measure without the previous placement's cap, which would otherwise be
    // read back as the menu's height; taking it off drops the scroll offset,
    // so that is put back below.
    const scrollTop = el.scrollTop
    el.style.maxHeight = ''
    el.style.overflowY = ''
    const rect = el.getBoundingClientRect()
    let { x, y } = position
    if (x + rect.width > window.innerWidth) x = window.innerWidth - rect.width - PADDING
    if (y + rect.height > window.innerHeight) y = window.innerHeight - rect.height - PADDING
    if (x < 0) x = PADDING
    // Never inside the title bar's OS drag region (see `FloatingPanel`): a tall
    // menu moved up to fit would put its first items where a click drags the window.
    if (y < TITLE_BAR_HEIGHT) y = TITLE_BAR_HEIGHT
    // Now running off the bottom edge (taller than the window below the title
    // bar — many windows to move to): cap it and scroll. The same overflow test
    // as above, so only a menu that would otherwise be cut off gets a cap.
    if (y + rect.height > window.innerHeight) {
      el.style.maxHeight = `${Math.max(0, window.innerHeight - y - PADDING)}px`
      el.style.overflowY = 'auto'
      el.scrollTop = scrollTop
    }
    el.style.left = `${x}px`
    el.style.top = `${y}px`
  }, [position, windowList])

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
    window.electronAPI.getWindows().then(setWindowList).catch(() => setWindowList([]))
  }, [onMergeTo])

  const showTearOff = !!onTearOff
  const showMerge = !!onMergeTo
  const hasWindows = windowList !== null && windowList.length > 0
  const isLoadingWindows = showMerge && !!window.electronAPI?.getWindows && windowList === null

  const showSeparator = showTearOff || showMerge

  return (
    <>
      <div
        data-testid="context-menu-backdrop"
        className="fixed inset-0 z-40"
        // The backdrop also covers the Electron title bar, a window drag region that would otherwise swallow clicks
        // there — and a click there must close the menu like a click anywhere else outside it (as ConfirmDialog's).
        style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}
        onMouseDown={onClose}
      />
      <div
        ref={ref}
        data-testid="workspace-context-menu"
        className="fixed z-50 min-w-44 bg-surface-secondary border border-border-default rounded-lg shadow-xl py-1"
        // no-drag: wherever it lands, the title bar's drag region must not take its clicks.
        style={{ left: position.x, top: position.y, WebkitAppRegion: 'no-drag' } as React.CSSProperties}
      >
        {/* Settings */}
        <button
          onClick={() => { onSettings(); onClose() }}
          className="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer transition-colors"
        >
          <Sliders size={14} />
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
                {win.title || 'Purdex'}
              </button>
            ))}
          </>
        )}
      </div>
    </>
  )
}
