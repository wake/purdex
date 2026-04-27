import { useEffect, useState } from 'react'
import { Sliders, ArrowSquareOut, ArrowSquareIn } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { WorkspaceQuickCommandsContextMenu } from './WorkspaceQuickCommandsContextMenu'

interface Props {
  position: { x: number; y: number }
  /**
   * Workspace this menu was opened against. Optional for back-compat with
   * existing tests that don't exercise the quick-commands section; required
   * to render the WORKSPACE_ACTIONS chip list.
   */
  workspaceId?: string
  /**
   * spec v4 §3.2.1 — multi-tab majority-vote hostId, or null when no
   * tmux-session pane exists in the workspace. Forwarded to the quick-commands
   * sub-menu which opens the HostPickerPopover lazily on click when null.
   */
  hostId?: string | null
  onSettings: () => void
  onTearOff?: () => void
  onMergeTo?: (targetWindowId: string) => void
  onClose: () => void
}

export function WorkspaceContextMenu({
  position,
  workspaceId,
  hostId,
  onSettings,
  onTearOff,
  onMergeTo,
  onClose,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const [windowList, setWindowList] = useState<ElectronWindowInfo[] | null>(null)

  // Quick Commands section visibility — depends on module enabled + at least
  // one binding into WORKSPACE_ACTIONS. Computed via a selector to avoid
  // re-rendering on unrelated store updates. hostId-null path uses globals
  // only (no per-host overrides reachable without a host).
  const hasQuickCommands = useQuickCommandStore((s) => {
    const cmds = hostId == null ? s.global : s.getCommands(hostId)
    return cmds.some((c) => s.bindings[c.id]?.includes(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS))
  })
  const quickCommandsModuleEnabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  const showQuickCommandsSection = !!workspaceId && quickCommandsModuleEnabled && hasQuickCommands

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
      <div data-testid="context-menu-backdrop" className="fixed inset-0 z-40" onMouseDown={onClose} />
      <div
        className="fixed z-50 min-w-44 bg-surface-secondary border border-border-default rounded-lg shadow-xl py-1"
        style={{ left: position.x, top: position.y }}
      >
        {/* Quick Commands — WORKSPACE_ACTIONS slot */}
        {showQuickCommandsSection && (
          <>
            <WorkspaceQuickCommandsContextMenu
              workspaceId={workspaceId!}
              hostId={hostId ?? null}
              onClose={onClose}
            />
            <div className="border-t border-border-default my-1" />
          </>
        )}

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
