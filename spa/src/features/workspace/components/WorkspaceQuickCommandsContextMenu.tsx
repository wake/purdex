import { useCallback, useEffect, useRef, useState } from 'react'
import { CommandSlot } from '../../../components/CommandSlot'
import { HostPickerPopover } from '../../../components/HostPickerPopover'
import { runWorkspaceSlot } from '../../../lib/slot-executor'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'

interface Props {
  workspaceId: string
  /**
   * Workspace 的多數決 hostId（spec v4 §3.2.1）；null 代表 workspace 無
   * tmux-session tabs，executor 會在 callback 裡開 HostPickerPopover。
   */
  hostId: string | null
  onClose: () => void
}

/**
 * 渲染 mount=WORKSPACE_ACTIONS 的 quick commands，作為 WorkspaceContextMenu 的子 section。
 *
 * codex round-1 B7 — 不傳 `render` prop（會與 `executor` 衝突 — render 包出來的
 * 是 `<span>`，沒有 onClick，executor 不會跑）。改用 `<CommandSlot>` default
 * button render + `containerClassName="flex flex-col"` 改 layout 為 menu 條列。
 *
 * codex round-1 B5/B6 — switchToSession callback 必須做 `openSingletonAndSelect`
 * 等價邏輯：open singleton tab → insertTab to workspace → setActiveWorkspace +
 * setActiveTab。tmux-session content 欄位要齊全（mode / cachedName / tmuxInstance）
 * 以滿足 `spa/src/types/tab.ts` 的型別契約。
 */
export function WorkspaceQuickCommandsContextMenu({ workspaceId, hostId, onClose }: Props) {
  // Mirror the parent menu's gating: disabling the module or removing every
  // WORKSPACE_ACTIONS binding makes this section disappear entirely (no
  // wrapper <div>, no separator). Hooks must run before the early return so
  // React's hook order stays stable across re-renders.
  const moduleEnabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  const hasBindings = useQuickCommandStore((s) => {
    const cmds = hostId == null ? s.global : s.getCommands(hostId)
    return cmds.some((c) => s.bindings[c.id]?.includes(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS))
  })

  // codex round-2 — picker state shape pinned: open implied by resolver !== null;
  // resolver is always nulled-out the moment it's invoked (idempotent guard against
  // duplicate resolve which would no-op the Promise but is still a sign of a bug).
  const [picker, setPicker] = useState<{
    open: boolean
    resolver: ((hostId: string | null) => void) | null
    anchor: { x: number; y: number } | null
  } | null>(null)
  const lastClickPos = useRef<{ x: number; y: number } | null>(null)
  // Mirrors the latest resolver so unmount cleanup can resolve a pending
  // picker Promise without going through React state — `setPicker` updaters
  // do not run reliably on already-unmounted components.
  const pendingResolverRef = useRef<((hostId: string | null) => void) | null>(null)

  const resolveHostId = useCallback(
    () =>
      new Promise<string | null>((resolve) => {
        pendingResolverRef.current = resolve
        setPicker({ open: true, resolver: resolve, anchor: lastClickPos.current })
      }),
    [],
  )

  // codex round-2 — dangling Promise cleanup. If the parent menu closes (unmount)
  // or the popover gets force-dismissed externally while a picker resolver is
  // still pending, we MUST resolve it as null so the executor's await returns,
  // its `finally` runs, and onClose fires. Otherwise the Promise hangs forever
  // and the executor stays mid-flight (busy=true sticks, chips stay disabled).
  useEffect(() => {
    return () => {
      const resolver = pendingResolverRef.current
      pendingResolverRef.current = null
      if (resolver) resolver(null)
    }
  }, [])

  // codex round-1 B6 — workspace caller must perform full openSingletonAndSelect
  // equivalent (the helper exists at spa/src/features/workspace/hooks.ts but is
  // bound to the hook, so we replicate inline here using the same store
  // primitives it uses).
  const switchToSession = useCallback(
    (h: string, sessionCode: string) => {
      // codex round-1 B5 — fill ALL tmux-session content fields per types/tab.ts
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

  // Short-circuit AFTER all hooks are declared so hook order stays stable.
  if (!moduleEnabled || !hasBindings) return null

  return (
    <div
      className="py-1"
      onClickCapture={(e) => {
        // Capture coordinates for picker anchor (fixed-positioned next to click).
        lastClickPos.current = { x: e.clientX, y: e.clientY }
      }}
    >
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId, workspaceId }}
        // codex round-1 B7 — flex-col override; default chip render keeps onClick + executor wiring intact.
        containerClassName="flex flex-col"
        // codex round-1 C11 — disable buttons while picker is mid-flight (prevents double-click race).
        busy={picker?.open ?? false}
        executor={async (cmd, ctx) => {
          try {
            await runWorkspaceSlot(cmd, ctx, {
              switchToSession,
              resolveHostId,
            })
          } finally {
            onClose()
          }
        }}
      />
      <HostPickerPopover
        open={picker?.open ?? false}
        anchor={picker?.anchor ?? null}
        onSelect={(hostId) => {
          // codex round-2 — null-out resolver before invoking to make duplicate-call safe
          const resolver = picker?.resolver
          pendingResolverRef.current = null
          setPicker(null)
          resolver?.(hostId)
        }}
        onCancel={() => {
          const resolver = picker?.resolver
          pendingResolverRef.current = null
          setPicker(null)
          resolver?.(null)
        }}
      />
    </div>
  )
}
