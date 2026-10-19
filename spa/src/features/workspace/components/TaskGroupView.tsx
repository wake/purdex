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
import { rowMenuItems } from '../../../lib/task-groups/row-menu'
import { buildGroupView, type GroupRow, type GroupSection, type HostGroups, type SessionTab } from '../../../lib/task-groups/view-model'
import { panelName } from '../../../lib/team/team-names'
import { teamColor } from '../../../components/team/team-display'
import { openTeamSeat } from '../../../lib/team/team-actions'
import { InlineTab } from './InlineTab'

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
  const sessionOfTab = useMemo(() => new Map(sessionTabs.map((s) => [s.tabId, s])), [sessionTabs])
  // A solo session's right-click menu gets the task-group rows on top (a team seat is filed by its team, L10). Set when the click happens, before the tab menu opens.
  const offerGroupMenu = (tabId: string, groupId: string | null) => {
    const s = sessionOfTab.get(tabId)
    if (!s || !taskGroupsSupported(s.hostId)) return
    const root = s.agentSessionId === null ? null : roots.rootOf(s.hostId, s.agentSessionId)
    useTabMenuExtrasStore.getState().set(tabId, rowMenuItems({ hostId: s.hostId, root, entry: groupsByHost[s.hostId], currentGroupId: groupId, t, report: setNotice }))
  }

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
      return seat ? inner : <div key={row.key} onContextMenuCapture={() => offerGroupMenu(tab.id, groupId)}>{inner}</div>
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
              <GroupHeader color={teamColor(section.view.colorIndex)} name={name} count={count} strong />
              {renderRow(section.lead, section.view.key, true)}
              <div className="ml-4 border-l border-border-subtle">{section.members.map((m) => renderRow(m, section.view.key))}</div>
            </section>
          )
        }
        if (section.kind === 'manual') {
          const collapsed = section.rows.length === 0
          return (
            <section key={section.key} data-testid="task-group-manual" data-collapsed={String(collapsed)} className="flex flex-col">
              <GroupHeader
                color={section.group.color ?? COLOR_DEFAULT} name={section.group.name} count={section.rows.length} muted={collapsed}
                host={hostLabel(section.hostId, hostLookOf(section.hostId))}
              />
              {!collapsed && section.rows.map((r) => renderRow(r, undefined, false, section.group.id))}
            </section>
          )
        }
        return (
          <section key="ungrouped" data-testid="task-group-ungrouped" className="flex flex-col">
            {i > 0 && <div className="mx-3 my-1 h-px bg-border-default" />}
            <GroupHeader color={COLOR_DEFAULT} name={t('taskgroups.ungrouped')} count={section.rows.length} muted />
            {section.rows.map((r) => renderRow(r))}
          </section>
        )
      })}
    </div>
  )
}

/** A group's header: the colour square, the name (semibold for a team, lighter for a manual group), the count and — for a manual group — the host it gathers. */
function GroupHeader({ color, name, count, strong = false, muted = false, host }: { color: string; name: string; count: number; strong?: boolean; muted?: boolean; host?: string }) {
  return (
    <div data-testid="task-group-header" className="mx-2 flex items-center gap-1.5 pl-1.5 pr-1.5 py-1 text-xs">
      <span aria-hidden="true" className="h-2.5 w-2.5 shrink-0 rounded-[3px]" style={{ backgroundColor: color }} />
      <span data-testid="task-group-name" className={`min-w-0 truncate ${strong ? 'font-semibold text-text-primary' : muted ? 'text-text-muted' : 'font-normal text-text-secondary'}`}>{name}</span>
      {host !== undefined && <span data-testid="task-group-host" className="shrink-0 rounded bg-surface-hover px-1 text-[9.5px] text-text-secondary">{host}</span>}
      <span data-testid="task-group-count" className="ml-auto shrink-0 tabular-nums text-text-muted">{count}</span>
    </div>
  )
}
