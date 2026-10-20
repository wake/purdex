// spa/src/features/workspace/components/TaskGroupView.tsx — the sidebar's second view, the task groups (task groups spec §6.1, TG-2b; name pool v2 spec §7, U11, NA-6b/6c). It takes the place of the workspace list while
// the device's `sidebarView` is `groups`:
//   - team groups first (the team's name, a colour square, a count; the lead, then the members hanging under it on a tree line);
//   - then the manual groups of `useManualGroupsStore`, in its order — a group gathers sessions of any host, so there are no per-host blocks; a group with nothing live in it is one collapsed line;
//   - then 「未分組」 after a divider.
// A session is a tab showing a tmux session; its row is the sidebar's own InlineTab (avatar, light, title as in the workspace view). A team seat no open tab shows is listed and not openable.
// A click switches to the session's workspace and tab (the same handler as the workspace view). Which group a solo session is in is decided by its (host, lineage root) (hooks/useSessionRoots.ts, the device-local
// root cache); until the root is read it is 未分組 for the display. A write that needs a root gets it with `resolveRoot` (lib/task-groups/session-actions.ts). The roots are read only while this view is mounted, i.e. open.
import { useMemo, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabStore } from '../../../stores/useTabStore'
import { TaskGroupSeatClosed } from './TaskGroupSeatClosed'
import { useSessionStore } from '../../../stores/useSessionStore'
import { useManualGroupsStore } from '../../../stores/useManualGroupsStore'
import { useGroupViewStore } from '../../../stores/useGroupViewStore'
import { useTabMenuExtrasStore } from '../../../stores/useTabMenuExtrasStore'
import { useWorkspaceStore } from '../store'
import { useTeamViews } from '../../../hooks/useTeamViews'
import { useSessionRoots, type WantedSession } from '../../../hooks/useSessionRoots'
import { useEffectiveView } from '../../../lib/task-groups/effective-view'
import { collectLeaves } from '../../../lib/pane-tree'
import { agentSessionOfTab } from '../../../lib/task-groups/placement'
import { manualGroupKey, teamGroupKey, UNGROUPED } from '../../../lib/task-groups/group-key'
import { selectGroupFromSidebar } from '../../../lib/task-groups/group-select'
import { buildTeamIndex } from '../../../lib/team/team-index'
import { failureText, rowMenuItems } from '../../../lib/task-groups/row-menu'
import { deleteManualGroup, renameManualGroup } from '../../../lib/task-groups/manual-actions'
import type { SessionActionResult } from '../../../lib/task-groups/session-actions'
import { buildGroupView, type GroupRow, type GroupSection, type SessionTab } from '../../../lib/task-groups/view-model'
import { panelName } from '../../../lib/team/team-names'
import { teamColor } from '../../../components/team/team-display'
import { openTeamSeat } from '../../../lib/team/team-actions'
import { InlineTab } from './InlineTab'
import { TaskGroupHeader } from './TaskGroupHeader'
import { TeamGroupHeader } from './TeamGroupHeader'
import { useGroupDrag } from './useGroupDrag'
import { ConfirmDialog } from '../../../components/ConfirmDialog'

interface Props {
  activeTabId: string | null
  onSelectTab: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onMiddleClickTab: (tabId: string) => void
  onContextMenuTab: (e: React.MouseEvent, tabId: string) => void
  onRenameTab?: (tabId: string) => void
}

const COLOR_DEFAULT = '#6b7280'

export function TaskGroupView({ activeTabId, onSelectTab, onCloseTab, onMiddleClickTab, onContextMenuTab, onRenameTab }: Props) {
  const t = useI18nStore((s) => s.t)
  const tabsById = useTabStore((s) => s.tabs)
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const groupOrder = useManualGroupsStore((s) => s.order)
  const groupsById = useManualGroupsStore((s) => s.groups)
  const selectedGroup = useGroupViewStore((s) => s.selectedGroup)
  const expandedMap = useGroupViewStore((s) => s.expanded)
  const setExpanded = useGroupViewStore((s) => s.setExpanded)
  const teams = useTeamViews()
  const sessionsByHost = useSessionStore((s) => s.sessions)
  // The last refusal of an edit, in words, until dismissed or replaced by the next action.
  const [notice, setNotice] = useState<string | null>(null)
  // The empty group the person asked to clear, until they answer.
  const [clearing, setClearing] = useState<string | null>(null)

  // The sessions: the tabs that show a tmux session, in workspace order, then those no workspace holds.
  const sessionTabs = useMemo<SessionTab[]>(() => {
    const order: Array<[string, string | null]> = []
    const seen = new Set<string>()
    for (const w of workspaces) for (const id of w.tabs) if (!seen.has(id)) { seen.add(id); order.push([id, w.id]) }
    for (const id of Object.keys(tabsById)) if (!seen.has(id)) order.push([id, null])
    const out: SessionTab[] = []
    for (const [tabId, workspaceId] of order) {
      const tab = tabsById[tabId]
      if (!tab) continue
      // The session a tab stands for: the first live tmux session among its panes (a split tab's session may sit in a secondary pane), as `shownSessions` reads a tab.
      const c = collectLeaves(tab.layout).map((p) => p.content).find((x) => x.kind === 'tmux-session' && !x.terminated)
      if (!c || c.kind !== 'tmux-session') continue
      out.push({ tabId, workspaceId, hostId: c.hostId, sessionCode: c.sessionCode, sessionName: sessionsByHost[c.hostId]?.find((r) => r.code === c.sessionCode)?.name ?? c.cachedName, agentSessionId: c.rebuild?.agent?.sessionId ?? null })
    }
    return out
  }, [tabsById, workspaces, sessionsByHost])

  // The open tabs' sessions, and one entry per group member (its root is its own chain's first session): a member on a host no tab shows is still read, so 「清除」 can tell whether that host has it alive.
  // A tab whose agent has ended is still placed by its root, so every tab's agent session is wanted, not only the live ones.
  const wanted = useMemo<WantedSession[]>(
    () => [
      ...Object.values(tabsById).flatMap((tab) => {
        const a = agentSessionOfTab(tab)
        return a === null ? [] : [{ hostId: a.hostId, agentSessionId: a.sessionId }]
      }),
      ...Object.values(groupsById).flatMap((g) => g.members.map((m) => ({ hostId: m.host, agentSessionId: m.root }))),
    ],
    [tabsById, groupsById],
  )
  // The roots are read only while the task-group view is really in force (wide sidebar, tabs not on the left — lib/task-groups/effective-view.ts), whatever this component is mounted for.
  const roots = useSessionRoots(useEffectiveView() === 'groups', wanted)

  const manual = useMemo(() => ({ order: groupOrder, groups: groupsById }), [groupOrder, groupsById])

  // The inclusive seat map decides which tabs are a team's (placement.ts); a render reads it once per input change.
  const index = useMemo(() => buildTeamIndex(teams, tabsById, sessionsByHost), [teams, tabsById, sessionsByHost])

  const view = useMemo<GroupSection[]>(
    () => buildGroupView({ tabs: tabsById, teams, index, manual, rootOf: roots.rootOf }),
    [tabsById, teams, index, manual, roots],
  )

  // A team seat's row opens the seat (the pane that shows it takes the keyboard — a split tab may carry several seats; a seat with no open tab gets one); any other row switches to its tab.
  // An action's refusal is said in the notice line.
  const done = (r: SessionActionResult) => { setNotice(r.ok ? null : failureText(t, r)) }
  // A group may be cleared only when nothing in it is alive: every member's host read for ITS session roots, and none of the members' roots a live session of its host (the display leaves out a session a team holds,
  // so an empty-looking group can still have live members). The dialog follows this on every render and goes away when it stops holding.
  const clearBlocked = (groupId: string): string | null => {
    const group = groupsById[groupId]
    if (!group) return null
    for (const m of group.members) {
      const live = roots.liveRoots(m.host)
      if (live === null) return t('taskgroups.clear_roots_reading')
      if (live.has(m.root)) return t('taskgroups.clear_in_use')
    }
    return null
  }
  const clearGroup = clearing !== null ? groupsById[clearing] : undefined
  const clearOk = clearing !== null && clearGroup !== undefined && clearBlocked(clearing) === null
  if (clearing !== null && !clearOk) setClearing(null) // derived state: the confirmation goes when the group stops being clearable (React re-renders at once, before anything is drawn)
  const confirmClear = () => {
    if (clearing === null || !clearOk) return
    done(deleteManualGroup(clearing))
    setClearing(null)
  }

  const sessionOfTab = useMemo(() => new Map(sessionTabs.map((s) => [s.tabId, s])), [sessionTabs])
  // A solo session's right-click menu gets the task-group rows on top (a team seat is filed by its team, L10). Set as the click bubbles up from the row, i.e. AFTER the tab menu's own handler cleared whatever an
  // earlier menu left; a tab that is no session gets none.
  const offerGroupMenu = (tabId: string, groupId: string | null) => {
    const s = sessionOfTab.get(tabId)
    if (!s) { useTabMenuExtrasStore.getState().clear(); return }
    useTabMenuExtrasStore.getState().set(tabId, rowMenuItems({
      session: { hostId: s.hostId, agentSessionId: s.agentSessionId, tabId }, groups: groupOrder.flatMap((id) => (groupsById[id] ? [{ id, name: groupsById[id].name }] : [])), currentGroupId: groupId, t, report: setNotice,
    }))
  }

  // Dragging a solo session: onto any group files it there, onto 「未分組」 takes it out (lib/task-groups/drag.ts). A team seat is not draggable and a team takes no drop (L10).
  const groupOfTab = useMemo(() => {
    const m = new Map<string, string>()
    for (const sec of view) if (sec.kind === 'manual') for (const r of sec.rows) if (r.tabId !== null) m.set(r.tabId, sec.groupId)
    return m
  }, [view])
  const drag = useGroupDrag({
    sourceOf: (tabId) => {
      const s = sessionOfTab.get(tabId)
      if (!s) return null
      return { session: { hostId: s.hostId, agentSessionId: s.agentSessionId, tabId }, currentGroupId: groupOfTab.get(tabId) ?? null }
    },
    onResult: done,
  })
  const lit = (key: string) => (drag.over === key ? ' rounded-md ring-1 ring-border-active' : '')

  const renderRow = (row: GroupRow, teamKey?: string, isLead = false, groupId: string | null = null, inTeam = false) => {
    const tab = row.tabId !== null ? tabsById[row.tabId] : undefined
    const seat = row.seat
    const openSeat = seat && teamKey ? () => { openTeamSeat(teamKey, seat.session.session_id) } : undefined
    if (tab) {
      const inner = (
        <InlineTab
          key={row.key} tab={tab} isActive={tab.id === activeTabId} noDrag leadOfTeam={isLead ? teamKey : undefined}
          seatOverride={seat ? { address: seat.session.address, title: seat.label } : undefined}
          onSelect={openSeat ? () => openSeat() : onSelectTab} onClose={onCloseTab} onMiddleClick={onMiddleClickTab} onContextMenu={onContextMenuTab} onRename={onRenameTab}
        />
      )
      return seat || inTeam ? inner : <div key={row.key} data-testid="task-group-session" onContextMenu={() => offerGroupMenu(tab.id, groupId)} {...drag.rowProps(tab.id)}>{inner}</div>
    }
    // A team seat no open tab shows: listed (dimmed, dressed as its persona), and a click opens it.
    return <TaskGroupSeatClosed key={row.key} address={seat?.session.address ?? ''} label={seat?.label ?? ''} hostId={seat?.hostId ?? null} tmuxSession={seat?.session.tmux_session} onOpen={openSeat} />
  }

  // A group is expanded unless the device collapsed it. A header click selects the group; on the selected group it toggles it (WorkspaceRow's rule); the chevron alone toggles.
  const isExpanded = (key: string) => expandedMap[key] !== false
  const headerProps = (key: string, toggleable: boolean) => ({
    selected: selectedGroup === key,
    expanded: isExpanded(key),
    onClick: () => { if (selectedGroup === key) setExpanded(key, !isExpanded(key)); else selectGroupFromSidebar(key) },
    onToggle: toggleable ? () => setExpanded(key, !isExpanded(key)) : undefined,
  })

  if (view.length === 0) {
    return <div data-testid="task-group-empty" className="px-4 py-3 text-xs text-text-muted">{t('taskgroups.empty')}</div>
  }

  return (
    <div data-testid="task-group-view" className="flex flex-col gap-1">
      {notice !== null && (
        <div data-testid="task-group-notice" role="alert" className="mx-3 my-0.5 flex items-start gap-1.5 text-[11px] text-status-warning-text">
          <span className="min-w-0 flex-1">{notice}</span>
          <button type="button" aria-label={t('taskgroups.notice_close')} onClick={() => setNotice(null)} className="shrink-0 cursor-pointer text-text-muted hover:text-text-primary">×</button>
        </div>
      )}
      {view.map((section, i) => {
        if (section.kind === 'team') {
          const name = panelName(section.view).text
          const key = teamGroupKey(section.view.key)
          const open = isExpanded(key)
          return (
            <section key={section.key} data-testid="task-group-team" className="flex flex-col">
              <TeamGroupHeader hostId={section.view.hostId} teamId={section.view.teamId} color={teamColor(section.view.colorIndex)} name={name} {...headerProps(key, true)} />
              {open && renderRow(section.lead, section.view.key, true)}
              {open && (
                <div className="ml-4 border-l border-border-subtle">
                  {section.rest.map((r) => renderRow(r, section.view.key, false, null, true))}
                </div>
              )}
            </section>
          )
        }
        if (section.kind === 'manual') {
          // Three states (spec §6.1): with tabs, listed; with members but no tab, collapsed with 「清除」; with neither (just created), the header and a muted hint row.
          const key = manualGroupKey(section.groupId)
          const collapsed = section.rows.length === 0
          const hasMembers = section.group.members.length > 0
          const open = isExpanded(key)
          return (
            <section
              key={section.key} data-testid="task-group-manual" data-collapsed={String(collapsed)} className={`flex flex-col${lit(section.groupId)}`}
              {...drag.targetProps({ kind: 'group', groupId: section.groupId })}
            >
              <TaskGroupHeader
                color={section.group.color ?? COLOR_DEFAULT} name={section.group.name} muted={collapsed} {...headerProps(key, !collapsed)}
                edit={{ blocked: null, onCommit: async (name) => { done(renameManualGroup(section.groupId, name)) } }}
                clear={collapsed && hasMembers ? { blocked: clearBlocked(section.groupId), onClick: () => setClearing(section.groupId) } : undefined}
              />
              {collapsed && !hasMembers && <div data-testid="task-group-hint" className="px-7 py-1 text-[11px] text-text-muted">{t('taskgroups.drop_hint')}</div>}
              {!collapsed && open && section.rows.map((r) => renderRow(r, undefined, false, section.groupId))}
            </section>
          )
        }
        return (
          <section key="ungrouped" data-testid="task-group-ungrouped" className={`flex flex-col${lit('ungrouped')}`} {...drag.targetProps({ kind: 'ungrouped' })}>
            {i > 0 && <div className="mx-3 my-1 h-px bg-border-default" />}
            <TaskGroupHeader color={COLOR_DEFAULT} name={t('taskgroups.ungrouped')} muted {...headerProps(UNGROUPED, true)} />
            {isExpanded(UNGROUPED) && section.rows.map((r) => renderRow(r))}
          </section>
        )
      })}
      {clearing && clearOk && clearGroup && (
        <ConfirmDialog
          testIdPrefix="task-group-clear" title={t('taskgroups.clear_title', { name: clearGroup.name })}
          body={clearGroup.members.length > 0 ? t('taskgroups.clear_body', { n: clearGroup.members.length }) : t('taskgroups.clear_body_none')}
          confirmLabel={t('taskgroups.clear_confirm')} busy={false} onCancel={() => setClearing(null)} onConfirm={confirmClear}
        />
      )}
    </div>
  )
}
