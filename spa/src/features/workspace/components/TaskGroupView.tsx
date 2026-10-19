// spa/src/features/workspace/components/TaskGroupView.tsx — the sidebar's second view, the task groups (name pool v2 spec §7, U11, NA-6b; the editing is NA-6c). It takes the place of the workspace list while
// the device's `sidebarView` is `groups`:
//   - team groups first (the team's name, a colour square, a count; the lead, then the members hanging under it on a tree line);
//   - then each host's manual groups in creation order (a lighter header with the host it belongs to); a group with nothing live in it is one collapsed line;
//   - then 「未分組」 after a divider.
// A session is a tab showing a tmux session; its row is the sidebar's own InlineTab (avatar, light, title as in the workspace view), not draggable here. A team seat no open tab shows is listed and not openable.
// A click switches to the session's workspace and tab (the same handler as the workspace view). Which group a solo session is in is decided by its lineage root (hooks/useSessionRoots.ts); until the
// root is read it is 未分組 for the display. What it reads — each connected host's groups, the roots — it reads only while this view is mounted, i.e. open.
import { useEffect, useMemo, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabStore } from '../../../stores/useTabStore'
import { daemonMismatchKey, useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { useTaskGroupsStore } from '../../../stores/useTaskGroupsStore'
import { useTabMenuExtrasStore } from '../../../stores/useTabMenuExtrasStore'
import { useWorkspaceStore } from '../store'
import { useTeamViews } from '../../../hooks/useTeamViews'
import { useSessionRoots, type WantedSession } from '../../../hooks/useSessionRoots'
import { hostLabel, hostLookOf } from '../../../lib/host-look'
import { collectLeaves } from '../../../lib/pane-tree'
import { loadTaskGroupsIfSupported, taskGroupsSupported } from '../../../lib/task-groups/loader'
import { hostBlockedReason, failureText, rowMenuItems } from '../../../lib/task-groups/row-menu'
import { clearTaskGroup, renameTaskGroup, type ActionResult } from '../../../lib/task-groups/actions'
import type { TaskGroup } from '../../../lib/task-groups/api'
import { buildGroupView, type GroupRow, type GroupSection, type HostGroups, type SessionTab } from '../../../lib/task-groups/view-model'
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
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const mismatched = useHostStore(daemonMismatchKey)
  const nex = useNexHostStore((s) => s.byHost)
  const groupsByHost = useTaskGroupsStore((s) => s.byHost)
  const teams = useTeamViews()
  // The last refusal of an edit, in words, until dismissed or replaced by the next action.
  const [notice, setNotice] = useState<string | null>(null)
  // The empty group the person asked to clear, until they answer.
  const [clearing, setClearing] = useState<{ hostId: string; groupId: string } | null>(null)
  const [clearBusy, setClearBusy] = useState(false)

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
      out.push({ tabId, workspaceId, hostId: c.hostId, sessionCode: c.sessionCode, agentSessionId: c.rebuild?.agent?.sessionId ?? null })
    }
    return out
  }, [tabsById, workspaces])

  // Opening the view reads each connected host's groups (when its daemon serves them); the capability entry is asked for first.
  const connectedKey = hostOrder.filter((h) => runtime[h]?.status === 'connected').join('\n')
  const capsKey = hostOrder.map((h) => `${h}:${nex[h]?.fetchedAt ?? 0}`).join('\n')
  useEffect(() => {
    for (const hostId of connectedKey === '' ? [] : connectedKey.split('\n')) {
      if (taskGroupsSupported(hostId)) void loadTaskGroupsIfSupported(hostId)
      else queueMicrotask(() => { void useNexHostStore.getState().ensure(hostId) })
    }
  }, [connectedKey, capsKey, mismatched])

  const wanted = useMemo<WantedSession[]>(
    () => sessionTabs.filter((s): s is SessionTab & { agentSessionId: string } => s.agentSessionId !== null).map((s) => ({ hostId: s.hostId, agentSessionId: s.agentSessionId })),
    [sessionTabs],
  )
  const roots = useSessionRoots(true, wanted)

  const manual = useMemo(() => {
    const out: Record<string, HostGroups> = {}
    for (const [hostId, entry] of Object.entries(groupsByHost)) {
      if (!taskGroupsSupported(hostId) || !entry.loaded) continue
      const unsafe = entry.invalid || entry.dropped !== null
        ? { count: entry.dropped?.count ?? 0, reasons: entry.dropped?.reasons ?? [], invalid: entry.invalid }
        : null
      out[hostId] = { groups: entry.groups, unsafe }
    }
    return out
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `nex` and `mismatched` (a daemon mismatch) move the capability gate
  }, [groupsByHost, nex, mismatched])

  const view = useMemo<GroupSection[]>(
    () => buildGroupView({ tabs: sessionTabs, teams, manual, rootOf: roots.rootOf, hostOrder }),
    [sessionTabs, teams, manual, roots, hostOrder],
  )

  // A team seat's row opens the seat (the pane that shows it takes the keyboard — a split tab may carry several seats; a seat with no open tab gets one); any other row switches to its tab.
  // Why a host's groups cannot be edited now (unsafe, not read yet), in words; null = they can. An action's refusal is said in the notice line.
  const hostBlocked = (hostId: string) => hostBlockedReason(groupsByHost[hostId], t)
  const done = (r: ActionResult) => { setNotice(r.ok ? null : failureText(t, r)) }
  // A group may be cleared only when nothing in it is alive: the host's groups editable, ITS session roots read, and none of the group's roots a live session of the host (the display leaves out a session a team holds,
  // so an empty-looking group can still have live members). The dialog follows this on every render and goes away when it stops holding.
  const clearBlocked = (hostId: string, group: TaskGroup): string | null => {
    const blocked = hostBlocked(hostId)
    if (blocked !== null) return blocked
    const live = roots.liveRoots(hostId)
    if (live === null) return t('taskgroups.clear_roots_reading')
    return group.roots.some((r) => live.has(r)) ? t('taskgroups.clear_in_use') : null
  }
  const clearGroup = clearing ? groupsByHost[clearing.hostId]?.groups.find((g) => g.id === clearing.groupId) : undefined
  const clearOk = clearing !== null && clearGroup !== undefined && clearBlocked(clearing.hostId, clearGroup) === null
  useEffect(() => { if (clearing !== null && !clearOk && !clearBusy) setClearing(null) }, [clearing, clearOk, clearBusy])
  const confirmClear = async () => {
    if (!clearing || !clearOk) return
    setClearBusy(true)
    try { done(await clearTaskGroup(clearing.hostId, clearing.groupId, clearGroup?.roots ?? [])) } finally { setClearBusy(false); setClearing(null) }
  }

  const sessionOfTab = useMemo(() => new Map(sessionTabs.map((s) => [s.tabId, s])), [sessionTabs])
  // A solo session's right-click menu gets the task-group rows on top (a team seat is filed by its team, L10). Set as the click bubbles up from the row, i.e. AFTER the tab menu's own handler cleared whatever an
  // earlier menu left; a host that cannot serve groups, or a tab that is no session, gets none.
  const offerGroupMenu = (tabId: string, groupId: string | null) => {
    const s = sessionOfTab.get(tabId)
    if (!s || !taskGroupsSupported(s.hostId)) { useTabMenuExtrasStore.getState().clear(); return }
    const root = s.agentSessionId === null ? null : roots.rootOf(s.hostId, s.agentSessionId)
    useTabMenuExtrasStore.getState().set(tabId, rowMenuItems({ hostId: s.hostId, root, entry: groupsByHost[s.hostId], currentGroupId: groupId, t, report: setNotice }))
  }

  // Dragging a solo session: onto a group of its host files it there, onto 「未分組」 takes it out (lib/task-groups/drag.ts). A team seat is not draggable and a team takes no drop (L10).
  const groupOfTab = useMemo(() => {
    const m = new Map<string, string>()
    for (const sec of view) if (sec.kind === 'manual') for (const r of sec.rows) if (r.tabId !== null) m.set(r.tabId, sec.group.id)
    return m
  }, [view])
  const drag = useGroupDrag({
    sourceOf: (tabId) => {
      const s = sessionOfTab.get(tabId)
      if (!s || !taskGroupsSupported(s.hostId)) return null
      return { hostId: s.hostId, root: s.agentSessionId === null ? null : roots.rootOf(s.hostId, s.agentSessionId), currentGroupId: groupOfTab.get(tabId) ?? null }
    },
    entryOf: (hostId) => groupsByHost[hostId], t, onNotice: setNotice, onResult: done,
  })
  const lit = (key: string) => (drag.over === key ? ' rounded-md ring-1 ring-border-active' : '')

  const renderRow = (row: GroupRow, teamKey?: string, isLead = false, groupId: string | null = null) => {
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
      return seat ? inner : <div key={row.key} data-testid="task-group-session" onContextMenu={() => offerGroupMenu(tab.id, groupId)} {...drag.rowProps(tab.id)}>{inner}</div>
    }
    // A team seat no open tab shows: listed, and a click opens it.
    return (
      <button
        key={row.key} type="button" data-testid="task-group-seat-closed" onClick={openSeat}
        className="mx-2 pl-[18px] pr-1.5 py-1 text-left text-xs text-text-muted truncate cursor-pointer hover:bg-surface-hover rounded-md" title={seat?.session.address}
      >
        {seat?.label}
      </button>
    )
  }

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
        if (section.kind === 'unsafe') {
          return (
            <div
              key={`unsafe:${section.hostId}`} data-testid="task-group-unsafe" role="status" title={section.reasons.join('\n')}
              className="mx-3 my-0.5 text-[11px] text-status-warning-text"
            >
              {section.invalid ? t('taskgroups.invalid') : t('taskgroups.unsafe', { n: section.count })}
            </div>
          )
        }
        if (section.kind === 'team') {
          const name = panelName(section.view).text
          const count = 1 + section.members.length
          return (
            <section key={section.key} data-testid="task-group-team" className="flex flex-col">
              <TeamGroupHeader hostId={section.view.hostId} teamId={section.view.teamId} color={teamColor(section.view.colorIndex)} name={name} count={count} />
              {renderRow(section.lead, section.view.key, true)}
              <div className="ml-4 border-l border-border-subtle">{section.members.map((m) => renderRow(m, section.view.key))}</div>
            </section>
          )
        }
        if (section.kind === 'manual') {
          const collapsed = section.rows.length === 0
          return (
            <section
              key={section.key} data-testid="task-group-manual" data-collapsed={String(collapsed)} className={`flex flex-col${lit(section.group.id)}`}
              {...drag.targetProps({ kind: 'group', hostId: section.hostId, groupId: section.group.id })}
            >
              <TaskGroupHeader
                color={section.group.color ?? COLOR_DEFAULT} name={section.group.name} count={section.rows.length} muted={collapsed}
                host={hostLabel(section.hostId, hostLookOf(section.hostId))}
                edit={{ blocked: hostBlocked(section.hostId), onCommit: (name) => renameTaskGroup(section.hostId, section.group.id, name).then(done) }}
                clear={collapsed ? { blocked: clearBlocked(section.hostId, section.group), onClick: () => setClearing({ hostId: section.hostId, groupId: section.group.id }) } : undefined}
              />
              {!collapsed && section.rows.map((r) => renderRow(r, undefined, false, section.group.id))}
            </section>
          )
        }
        return (
          <section key="ungrouped" data-testid="task-group-ungrouped" className={`flex flex-col${lit('ungrouped')}`} {...drag.targetProps({ kind: 'ungrouped' })}>
            {i > 0 && <div className="mx-3 my-1 h-px bg-border-default" />}
            <TaskGroupHeader color={COLOR_DEFAULT} name={t('taskgroups.ungrouped')} count={section.rows.length} muted />
            {section.rows.map((r) => renderRow(r))}
          </section>
        )
      })}
      {clearing && clearOk && clearGroup && (
        <ConfirmDialog
          testIdPrefix="task-group-clear" title={t('taskgroups.clear_title', { name: clearGroup.name })}
          body={clearGroup.roots.length > 0 ? t('taskgroups.clear_body', { n: clearGroup.roots.length }) : t('taskgroups.clear_body_none')}
          confirmLabel={t('taskgroups.clear_confirm')} busy={clearBusy} onCancel={() => setClearing(null)} onConfirm={() => { void confirmClear() }}
        />
      )}
    </div>
  )
}
