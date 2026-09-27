// spa/src/features/workspace/components/WorkspaceConflict.tsx — a workspace row's sync-conflict icon and the panel
// that resolves it in place (sidebar conflict icons spec §5; the plan's review amendments I1 / I2 / I5 / M6 / M8).
//
// WHEN: the master's `tabs.<this workspace>` is locked (`tabsLockOf`) AND the master world is settled and on screen.
// While a slave is on screen the sidebar's workspaces are the slave's — a demoted master shares ids with it, so a
// match by id would lie: no icon (spec D2). Nothing is stored: the icon is a reading of `useProfileSync()`, and goes
// the moment the lock does.
//
// THE PANEL IS SETTINGS' OWN RESOLVE ROW (`ResolveRow`, reused UNCHANGED): what is frozen and checked when the
// confirmation opens, the send, "sent" and its TTL stay in `useResolveContext`, in one place.
//
// TWO PIECES, TWO PLACES IN THE ROW (review I1 / I2). The BUTTON sits in the header, among the dnd-kit listeners, and
// does NOT stop pointer-down: like the name / + / chevron, it must leave the header draggable (`distance: 5` tells a
// click from a drag; the header has no onClick, so a click selects nothing). The PANEL is rendered as a SIBLING of the
// header: it is a portal, and a portal's events bubble through the React tree — inside the header, dragging the
// panel by its title bar would start a workspace drag. The row owns the button's ref and hands it to both.
//
// OPEN lives in `useConflictPanelStore`, because the Home row's popover opens it too. This row closes its own entry
// when the lock is gone, when a slave is ACTUALLY on screen, and when it unmounts — never while the world is merely
// unsettled (a window catching up): then icon and panel are hidden, and come back with it (review I5). That reading
// is `useWorkspaceConflict`'s.
import type { RefObject } from 'react'
import { WarningCircle } from '@phosphor-icons/react'
import { FloatingPanel } from '../../../components/FloatingPanel'
import { ResolveRow } from '../../../components/settings/profile/ResolveRow'
import { tabsSectionKey } from '../../../lib/profile/projections'
import { useConflictPanelStore } from '../../../stores/useConflictPanelStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { Workspace } from '../../../types/tab'
import type { WorkspaceConflictState } from './useWorkspaceConflict'

interface Props {
  workspace: Workspace
  conflict: WorkspaceConflictState
  buttonRef: RefObject<HTMLButtonElement | null>
}

/** The header's icon. Always visible (not hover-only): a lock waits for the user. */
export function WorkspaceConflictButton({ workspace, conflict, buttonRef }: Props) {
  const t = useI18nStore((s) => s.t)
  if (conflict.lock === null) return null
  const label = t('profile.conflict.workspace_button', { name: workspace.name })
  return (
    <button
      ref={buttonRef}
      type="button"
      data-testid={`ws-conflict-button-${workspace.id}`}
      aria-label={label}
      title={label}
      aria-expanded={conflict.open}
      onClick={() => useConflictPanelStore.getState().toggle(workspace.id)}
      className="p-0.5 rounded text-amber-500 hover:bg-surface-secondary cursor-pointer focus:outline-none focus-visible:ring-1 focus-visible:ring-border-active"
    >
      <WarningCircle size={14} />
    </button>
  )
}

/** The panel: one Resolve row, for this workspace's tabs. Rendered OUTSIDE the header (see above). */
export function WorkspaceConflictPanel({ workspace, conflict, buttonRef }: Props) {
  const t = useI18nStore((s) => s.t)
  const { lock, open, sync } = conflict
  if (!open || lock === null || sync.master === null || sync.status === null) return null
  const key = tabsSectionKey(workspace.id) // cannot throw: `tabsLockOf` found a lock, so the id forms a key
  const close = () => useConflictPanelStore.getState().closeFor(workspace.id)
  return (
    <FloatingPanel
      title={t('profile.conflict.workspace_title', { name: workspace.name })}
      anchorRef={buttonRef}
      onClose={close}
      width={360}
      testId={`ws-conflict-panel-${workspace.id}`}
    >
      <ul className="flex flex-col [&>li]:border-t-0">
        <ResolveRow
          // Keyed by the master and the section, as in Settings: a master that changes takes the open confirmation
          // and the "sent" with it.
          key={JSON.stringify([sync.master.hostId, sync.master.profileId, key])}
          sectionKey={key}
          kind="tabs"
          // The live store IS the master world here (the row shows a lock only with the master on screen).
          label={t('settings.profile.current.label.tabs', { workspace: workspace.name })}
          lock={lock}
          invalidReason={sync.status.detail[key]?.invalidReason ?? null}
          fromLeader={sync.remote}
          disabled={sync.blocked !== null}
        />
      </ul>
    </FloatingPanel>
  )
}
