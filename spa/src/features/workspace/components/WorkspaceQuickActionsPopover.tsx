import { useCallback, useEffect, useRef, useState } from 'react'
import { CommandSlot } from '../../../components/CommandSlot'
import { HostPickerPopover } from '../../../components/HostPickerPopover'
import { runWorkspaceSlot } from '../../../lib/slot-executor'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { getBindingTargets } from '../../../lib/quick-command-bindings'

interface Props {
  workspaceId: string
  /**
   * hostId 為 null 時 chip 仍顯示，executor 點擊後會開 HostPickerPopover
   * 讓 user 選 host（spec v4 §3.2.2）。
   */
  hostId: string | null
  /**
   * codex round-1 P2 (F3 — picker hover-dismissal) — invoked whenever the
   * internal HostPickerPopover open state flips. The parent hub uses this to
   * suppress its own mouseleave / pointerdown close logic while the picker is
   * up, so moving the pointer toward a host option does not unmount the
   * popover (and the picker with it) before the user can click.
   */
  onPickerOpenChange?: (open: boolean) => void
}

/**
 * Popover chip-list rendered to the LEFT of the Plus-button on each
 * WorkspaceRow on hover/focus. Uses CommandSlot internally — already
 * short-circuits when module disabled / no bindings; we additionally
 * skip rendering the popover wrapper itself in those cases so the
 * hover trigger doesn't expose an empty floating panel.
 *
 * NOTE (spec v4 §3.2.2): we do NOT short-circuit on hostId == null —
 * the picker flow handles that case. Only no-bindings / module-disabled
 * suppress the wrapper.
 */
export function WorkspaceQuickActionsPopover({
  workspaceId,
  hostId,
  onPickerOpenChange,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const moduleEnabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  const hasBindings = useQuickCommandStore((s) => {
    const cmds = hostId == null ? s.global : s.getCommands(hostId)
    return cmds.some((c) => {
      const targets = getBindingTargets(s.bindings, c.id)
      return targets !== undefined && targets.includes(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS)
    })
  })
  // codex round-1 P2 (F1 — cwd parity with context-menu) — spec §3.2 says
  // WORKSPACE_ACTIONS sessions inherit `workspace.moduleConfig.files.projectPath`
  // as cwd. Without this, hover-popover commands fall back to `~` while the
  // identical command from the right-click context menu would run in projectPath.
  const cwd = useWorkspaceStore((s) => {
    const ws = s.workspaces.find((w) => w.id === workspaceId)
    const path = ws?.moduleConfig?.['files']?.['projectPath']
    return typeof path === 'string' && path.length > 0 ? path : undefined
  })
  const wrapperRef = useRef<HTMLDivElement>(null)
  // codex round-2 — picker state shape pinned: open implied by resolver !== null.
  const [picker, setPicker] = useState<{
    open: boolean
    resolver: ((id: string | null) => void) | null
    anchor: HTMLElement | null
  } | null>(null)

  // codex round-1 P2 (F2 — double-click race) — when hostId is already known the
  // picker never opens, so `picker?.open` stays false for the entire async
  // createSession + send-keys round-trip. Without an explicit pending flag a
  // fast double-click would queue two executor pipelines and create two sessions
  // for the same command. The ref is the synchronous guard (covers same-tick
  // double-fire before React re-renders); `executing` mirrors it to React state
  // so the disabled UI in CommandSlot updates.
  const executingRef = useRef(false)
  const [executing, setExecuting] = useState(false)

  const resolveHostId = useCallback(
    () =>
      new Promise<string | null>((resolve) => {
        setPicker({ open: true, resolver: resolve, anchor: wrapperRef.current })
      }),
    [],
  )

  // codex round-2 — dangling Promise cleanup. The popover lives behind a hover
  // trigger; mouseleave on the parent hub will unmount this component while a
  // resolver may still be pending. We MUST resolve null on unmount so the
  // executor's await returns and busy state clears.
  useEffect(() => {
    return () => {
      setPicker((current) => {
        if (current?.resolver) current.resolver(null)
        return null
      })
    }
  }, [])

  // codex round-1 P2 (F3 — picker hover-dismissal) — propagate picker open state
  // to the parent hub so it can suppress its mouseleave/pointerdown close logic
  // while the picker is up. Cleanup notifies false on unmount so the hub never
  // gets stuck thinking a picker is still open after the popover is gone.
  const pickerOpen = picker?.open ?? false
  useEffect(() => {
    onPickerOpenChange?.(pickerOpen)
  }, [pickerOpen, onPickerOpenChange])
  useEffect(() => {
    return () => onPickerOpenChange?.(false)
  }, [onPickerOpenChange])

  // codex round-1 B5/B6 — full openSingletonAndSelect equivalent: complete
  // tmux-session content fields + insertTab into workspace + setActive.
  const switchToSession = useCallback(
    (h: string, sessionCode: string) => {
      const tabId = useTabStore.getState().openSingletonTab({
        kind: 'tmux-session',
        hostId: h,
        sessionCode,
        mode: 'terminal',
        cachedName: sessionCode,
        tmuxInstance: '',
      })
      useWorkspaceStore.getState().insertTab(tabId, workspaceId)
      useWorkspaceStore.getState().setActiveWorkspace(workspaceId)
      useTabStore.getState().setActiveTab(tabId)
    },
    [workspaceId],
  )

  if (!moduleEnabled || !hasBindings) return null

  return (
    <div
      ref={wrapperRef}
      role="group"
      // codex round-1 C16 — i18n key, not hard-coded English
      aria-label={t('quick_commands.aria.workspace_actions')}
      className="absolute right-full top-1/2 -translate-y-1/2 mr-1 flex items-center gap-1 px-2 py-1 rounded-md bg-gradient-to-l from-surface-secondary/0 to-surface-secondary/95 backdrop-blur-sm shadow-md z-30"
    >
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        // codex round-1 P2 (F1) — pass cwd so hover-popover commands inherit
        // workspace projectPath, matching context-menu behavior.
        ctx={{ hostId, workspaceId, cwd }}
        // codex round-1 C11 + P2 (F2) — busy=true during picker mid-flight OR
        // executor mid-flight; without `executing` the hostId-known path would
        // never disable chips, allowing double-click duplicate session creation.
        busy={pickerOpen || executing}
        executor={async (cmd, ctx) => {
          // codex round-1 P2 (F2) — synchronous ref guard catches the
          // double-click window before React re-renders the disabled state.
          if (executingRef.current) return
          executingRef.current = true
          setExecuting(true)
          try {
            await runWorkspaceSlot(
              cmd,
              { ...ctx, workspaceId },
              {
                switchToSession,
                resolveHostId,
                // #690 enforcement (alpha.242) — workspace liveness probe is
                // type-level required on Deps. Workspace gets unmounted async
                // (mouseleave / route change) while createSession is in flight;
                // returning false here aborts before send-keys.
                assertContextLive: () =>
                  useWorkspaceStore.getState().workspaces.some((w) => w.id === workspaceId),
              },
            )
          } finally {
            executingRef.current = false
            setExecuting(false)
          }
        }}
      />
      <HostPickerPopover
        open={picker?.open ?? false}
        anchor={picker?.anchor ?? null}
        onSelect={(hid) => {
          // codex round-2 — null-out resolver before invoking to make duplicate-call safe
          const resolver = picker?.resolver
          setPicker(null)
          resolver?.(hid)
        }}
        onCancel={() => {
          const resolver = picker?.resolver
          setPicker(null)
          resolver?.(null)
        }}
      />
    </div>
  )
}
