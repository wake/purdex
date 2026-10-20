// spa/src/features/workspace/components/TaskGroupView.test.tsx — the sidebar's task-group view (task groups spec §6.1, TG-2b; name pool v2 spec §7, NA-6b/6c): team groups, then the manual groups of `useManualGroupsStore`
// in its order — a group gathers sessions of any host — then 未分組; a click on a session switches to its tab; a group with nothing live is one collapsed line. The rows are the sidebar's own InlineTab, so the avatar,
// the light and the title come as they do in the workspace view. Real stores; the daemon read is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, within, act } from '@testing-library/react'

vi.mock('@dnd-kit/sortable', () => ({
  useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: vi.fn(), transform: null, transition: null, isDragging: false }),
}))
vi.mock('../lib/icon-path-cache', () => ({ getIconPath: (n: string, w: string) => `M ${n} ${w}`, isWeightLoaded: () => true, prefetchWeight: () => Promise.resolve() }))
const readFirstPage = vi.fn(async (_hostId: string) => ({}))
// a fresh read answers with the quota rows the test put in the store
const freshRead = async (h: string) => { const r = useRelayQuotaStore.getState().reads[h]; return { quotas: r?.ok ? r.quotas : undefined, approved: [], truncated: false } }
vi.mock('../../../lib/team/unattended-reader', () => ({ readFirstPage: (h: string) => readFirstPage(h), refreshHost: (h: string) => freshRead(h) }))

const openTeamSeat = vi.fn()
vi.mock('../../../lib/team/team-actions', () => ({ openTeamSeat: (...a: unknown[]) => openTeamSeat(...a) }))

import { TaskGroupView } from './TaskGroupView'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../store'
import { useSessionStore } from '../../../stores/useSessionStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useTeamRosterStore } from '../../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../../stores/useUnattendedStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useManualGroupsStore } from '../../../stores/useManualGroupsStore'
import { useGroupViewStore } from '../../../stores/useGroupViewStore'
import { manualGroupKey, UNGROUPED } from '../../../lib/task-groups/group-key'
import { useTabMenuExtrasStore } from '../../../stores/useTabMenuExtrasStore'
import { useRelayQuotaStore } from '../../../lib/team/relay-quota'
import { useRootCacheStore } from '../../../lib/task-groups/root-cache'
import { startSessionRootsLifecycle } from '../../../lib/task-groups/session-roots-lifecycle'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { useTeamUiStore } from '../../../stores/useTeamUiStore'
import { usePersonaStore } from '../../../stores/usePersonaStore'
import { makeCollection } from '../../../lib/names/__fixtures__/personas'
import { useAgentStore } from '../../../stores/useAgentStore'
import type { Tab } from '../../../types/tab'

const tab = (id: string, code: string, agentSessionId: string | null, hostId = 'h1'): Tab => ({
  id, kind: 'tmux-session', locked: false, pinned: false,
  layout: { type: 'leaf', pane: { id: `${id}-p`, content: { kind: 'tmux-session', hostId, sessionCode: code, mode: 'terminal', cachedName: `name-${code}`, tmuxInstance: 'I', ...(agentSessionId ? { rebuild: { agent: { sessionId: agentSessionId } } } : {}) } } },
} as never)
const group = (id: string, roots: string[], host = 'h1') => ({ id, name: `Group ${id}`, members: roots.map((root) => ({ host, root })), createdAt: 1 })
const setGroups = (groups: Array<ReturnType<typeof group>>) =>
  useManualGroupsStore.setState({ order: groups.map((g) => g.id), groups: Object.fromEntries(groups.map(({ id, ...g }) => [id, g])) })
const membersOf = (id: string) => useManualGroupsStore.getState().groups[id]?.members
const setRoots = (rows: Array<[string, string]>) =>
  useRelayQuotaStore.setState({ reads: { h1: { ok: true, quotas: rows.map(([session_id, root_session_id]) => ({ session_id, root_session_id, title: '', address: `a/${session_id}`, is_lead: false, self_left: 0, member_pool_left: 0, rev: 1 })) } } })

let stopRoots: () => void
const onSelectTab = vi.fn()
const mount = () => render(<TaskGroupView activeTabId={null} onSelectTab={onSelectTab} onCloseTab={vi.fn()} onMiddleClickTab={vi.fn()} onContextMenuTab={vi.fn()} />)

beforeEach(() => {
  cleanup()
  onSelectTab.mockClear(); readFirstPage.mockClear(); openTeamSeat.mockClear()
  useI18nStore.getState().setLocale('zh-TW')
  useUISettingsStore.setState({ dynamicTabName: false, tabNameTooltipMode: 'both', tabIndicatorStyle: 'badge', ccIconVariant: 'bot', codexIconVariant: 'openai', hostBadgeSidebarEnabled: false } as never)
  useAgentStore.setState({ statuses: {}, unread: {}, subagents: {}, agentTypes: {}, oscTitles: {} } as never)
  const tabs = { t1: tab('t1', 'S1', 'AG1'), t2: tab('t2', 'S2', 'AG2'), t3: tab('t3', 'S3', 'AG3') }
  useTabStore.setState({ tabs, activeTabId: null } as never)
  useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: ['t1', 't2', 't3'], activeTabId: null, moduleConfig: {} }], activeWorkspaceId: 'w1' } as never)
  useSessionStore.setState({ sessions: { h1: [{ code: 'S1', name: 'name-S1' }, { code: 'S2', name: 'name-S2' }, { code: 'S3', name: 'name-S3' }] as never } })
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['h1'], runtime: { h1: { status: 'connected' } } } as never)
  useTeamRosterStore.getState().reset()
  useManualGroupsStore.setState({ order: [], groups: {} })
  useGroupViewStore.setState({ selectedGroup: null, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
  useTeamUiStore.setState({ sidebarView: 'groups' }) // the task-group view is in force: stored, wide sidebar, tabs not on the left
  useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'top' })
  useRelayQuotaStore.getState().reset()
  useRootCacheStore.setState({ roots: {} })
  useUnattendedStore.setState((s) => ({ byHost: { ...s.byHost, h1: { ...s.byHost.h1, quotaSupport: 'yes' } } }) as never) // the roots are read from hosts that list team.relay_quota.v1
  stopRoots = startSessionRootsLifecycle() // the lifecycle feeds the root cache from the reads, as main.tsx starts it
})
afterEach(() => { cleanup(); stopRoots() })

const sections = () => screen.getAllByTestId(/^task-group-(manual|team|ungrouped)/)

describe('the task-group view', () => {
  it('lists a manual group with the sessions whose lineage root it holds (a relayed session included), then 未分組', () => {
    setGroups([group('g1', ['ROOT1'])])
    setRoots([['AG1', 'ROOT1'], ['AG2', 'AG2'], ['AG3', 'AG3']])
    mount()
    const secs = sections()
    expect(secs.map((s) => s.getAttribute('data-testid'))).toEqual(['task-group-manual', 'task-group-ungrouped'])
    expect(within(secs[0]).getByTestId('task-group-name')).toHaveTextContent('Group g1')
    expect(within(secs[0]).getAllByTestId('inline-tab-row')).toHaveLength(1)
    expect(within(secs[0]).getByTestId('task-group-chevron')).toHaveAttribute('aria-expanded', 'true') // the chevron stands where the count was
    expect(within(secs[1]).getAllByTestId('inline-tab-row')).toHaveLength(2)
    expect(within(secs[1]).getByTestId('task-group-name')).toHaveTextContent('未分組')
  })

  it('until the roots are read, the sessions are 未分組 for the display', () => {
    setGroups([group('g1', ['ROOT1'])])
    mount()
    const manual = screen.getByTestId('task-group-manual')
    expect(within(manual).queryAllByTestId('inline-tab-row')).toHaveLength(0)
    expect(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')).toHaveLength(3)
  })

  it('a group with nothing live in it is one collapsed line', () => {
    setGroups([group('g1', ['GONE'])])
    setRoots([['AG1', 'AG1']])
    mount()
    const manual = screen.getByTestId('task-group-manual')
    expect(manual).toHaveAttribute('data-collapsed', 'true')
    expect(within(manual).queryByTestId('inline-tab-row')).toBeNull()
  })

  it('a split tab stands for the session in whichever pane shows one: a tmux session in a secondary pane is placed by its root, and a tab with no session at all is listed too (in 未分組)', () => {
    const split = (id: string, children: unknown[]) => ({ type: 'split', id, direction: 'h', children, sizes: [50, 50] })
    const leaf = (id: string, content: unknown) => ({ type: 'leaf', pane: { id, content } })
    const t4 = { id: 't4', kind: 'tmux-session', locked: false, pinned: false, layout: split('s4', [leaf('p4a', { kind: 'new-tab' }), leaf('p4b', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'S4', mode: 'terminal', cachedName: 'n4', tmuxInstance: 'I', rebuild: { agent: { sessionId: 'AG4' } } })]) }
    const t5 = { id: 't5', kind: 'new-tab', locked: false, pinned: false, layout: leaf('p5', { kind: 'new-tab' }) }
    act(() => {
      useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, t4, t5 } } as never)
      useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: ['t1', 't2', 't3', 't4', 't5'], activeTabId: null, moduleConfig: {} }] } as never)
      setGroups([group('g1', ['ROOT4'])]); setRoots([['AG4', 'ROOT4']])
    })
    mount()
    expect(within(screen.getByTestId('task-group-manual')).getAllByTestId('inline-tab-row')).toHaveLength(1)
    expect(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')).toHaveLength(4) // t1 t2 t3, and t5 (no session: every tab is listed)
  })

  const rosterSession = (id: string, tmux: string, over: Record<string, unknown> = {}) => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-x`, live: true, tmux_session: tmux, title: `title ${id}`, ...over })
  const teamOf = (lead: ReturnType<typeof rosterSession>, members: ReturnType<typeof rosterSession>[]) => ({
    id: 'team-1', host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '', lead,
    members: members.map((m) => ({ ...m, state: 'active', origin: 'spawned', joined_at: 1 })),
  })

  it('a team is a group of its own: the lead, its members under it; a seat\'s row opens the seat, not just a tab', () => {
    useTeamRosterStore.getState().apply('h1', [teamOf(rosterSession('L', 'name-S1'), [rosterSession('M', 'name-S2')]) as never])
    mount()
    const secs = sections()
    expect(secs[0].getAttribute('data-testid')).toBe('task-group-team')
    expect(within(secs[0]).getByTestId('task-group-name')).toHaveTextContent('Payments')
    expect(within(secs[0]).getAllByTestId('inline-tab-row')).toHaveLength(2)
    expect(within(secs[1]).getAllByTestId('inline-tab-row')).toHaveLength(1) // t3 is nobody's
    fireEvent.click(within(secs[0]).getAllByTestId('inline-tab-row')[1])
    expect(openTeamSeat).toHaveBeenCalledWith(expect.stringContaining('team-1'), 'M')
    expect(onSelectTab).not.toHaveBeenCalled()
  })

  it('a second tab showing a seat session is an extra row of its team and not a drag source', () => {
    useTeamRosterStore.getState().apply('h1', [teamOf(rosterSession('L', 'name-S1'), [rosterSession('M', 'name-S2')]) as never])
    act(() => {
      useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, t2b: tab('t2b', 'S2', 'AG2b') } } as never)
    })
    mount()
    const team = screen.getByTestId('task-group-team')
    expect(within(team).getAllByTestId('inline-tab-row')).toHaveLength(3) // lead, member, and the member's second tab
    expect(within(team).queryByTestId('task-group-session')).toBeNull() // an extra team row is not draggable into a manual group
    expect(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')).toHaveLength(1) // t3 only
  })

  it('a tab whose agent has ended stays in the group its root names', () => {
    const ended = tab('t1', 'S1', 'AG1') as never as { layout: { pane: { content: { rebuild: Record<string, unknown> } } } }
    ended.layout.pane.content.rebuild.agentExited = { at: 2, reason: 'session-end' }
    act(() => { useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, t1: ended } } as never) })
    setGroups([group('g1', ['ROOT1'])])
    setRoots([['AG1', 'ROOT1']])
    mount()
    expect(within(screen.getByTestId('task-group-manual')).getAllByTestId('inline-tab-row')).toHaveLength(1)
  })

  it('two seats in the panes of one split tab are two rows, each opening its own seat', () => {
    const split = (id: string, children: unknown[]) => ({ type: 'split', id, direction: 'h', children, sizes: [50, 50] })
    const leaf = (id: string, code: string) => ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: code, mode: 'terminal', cachedName: `name-${code}`, tmuxInstance: 'I' } } })
    act(() => {
      useTabStore.setState({ tabs: { t6: { id: 't6', kind: 'tmux-session', locked: false, pinned: false, layout: split('s6', [leaf('p6a', 'S6'), leaf('p6b', 'S7')]) } } } as never)
      useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: ['t6'], activeTabId: null, moduleConfig: {} }] } as never)
      useSessionStore.setState({ sessions: { h1: [{ code: 'S6', name: 'name-S6' }, { code: 'S7', name: 'name-S7' }] as never } })
      useTeamRosterStore.getState().apply('h1', [teamOf(rosterSession('L', 'name-S6'), [rosterSession('M', 'name-S7')]) as never])
    })
    mount()
    const rows = within(screen.getByTestId('task-group-team')).getAllByTestId('inline-tab-row')
    // both rows show the tab's title (TG-0b); what tells them apart is the persona name, and each opens its own seat
    expect(rows.map((r) => r.textContent)).toEqual([expect.stringContaining('name-S6'), expect.stringContaining('name-S6')])
    // a seat's row has no close button: its tab carries the other seat too, and closing it would close both
    for (const r of rows) expect(within(r).queryByRole('button', { name: /Close/ })).toBeNull()
    fireEvent.click(rows[1])
    expect(openTeamSeat).toHaveBeenLastCalledWith(expect.any(String), 'M')
    fireEvent.click(rows[0])
    expect(openTeamSeat).toHaveBeenLastCalledWith(expect.any(String), 'L')
  })

  it('a seat no open tab shows is listed, and a click opens it', () => {
    useTeamRosterStore.getState().apply('h1', [teamOf(rosterSession('L', 'name-S1'), [rosterSession('M', 'name-gone')]) as never])
    mount()
    const closed = within(screen.getByTestId('task-group-team')).getByTestId('task-group-seat-closed')
    expect(closed).toHaveTextContent('title M')
    fireEvent.click(closed)
    expect(openTeamSeat).toHaveBeenCalledWith(expect.any(String), 'M')
  })

  describe('seat rows (TG-0b)', () => {
    const personas = () => {
      useTeamUiStore.setState({ sidebarView: 'groups' })
      usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: makeCollection(['kenji']) } } } as never)
    }
    const kenji = (id: string, tmux: string) => rosterSession(id, tmux, { address: 'mlab/work-kenji' })

    it('a seat no tab shows wears its persona: avatar and a bold name, then the label, dimmed', () => {
      personas()
      useTeamRosterStore.getState().apply('h1', [teamOf(rosterSession('L', 'name-S1'), [kenji('M', 'name-gone')]) as never])
      mount()
      const closed = within(screen.getByTestId('task-group-team')).getByTestId('task-group-seat-closed')
      expect(within(closed).getByRole('img', { name: 'Kenji' })).toBeInTheDocument()
      expect(within(closed).getByTestId('task-group-seat-name')).toHaveTextContent('Kenji')
      expect(closed).toHaveTextContent('title M')
      expect(closed.className).toContain('opacity-60')
      fireEvent.click(closed)
      expect(openTeamSeat).toHaveBeenCalledWith(expect.any(String), 'M')
    })

    it('a seat with no persona and no tab keeps the default agent icon, dimmed', () => {
      personas()
      useTeamRosterStore.getState().apply('h1', [teamOf(rosterSession('L', 'name-S1'), [rosterSession('M', 'name-gone', { address: 'mlab/work-nobody' })]) as never])
      mount()
      const closed = within(screen.getByTestId('task-group-team')).getByTestId('task-group-seat-closed')
      expect(within(closed).getByTestId('team-bead-bot')).toBeInTheDocument() // the default agent icon, as the team panel's beads draw it
      expect(within(closed).queryByRole('img')).toBeNull()
      expect(closed.className).toContain('opacity-60')
    })

    const closedSeat = () => within(screen.getByTestId('task-group-team')).getByTestId('task-group-seat-closed')
    const gone = () => {
      useSessionStore.setState({ sessions: { h1: [{ code: 'S1', name: 'name-S1' }, { code: 'S9', name: 'name-gone' }] as never } })
      useTeamRosterStore.getState().apply('h1', [teamOf(rosterSession('L', 'name-S1'), [rosterSession('M', 'name-gone', { address: 'mlab/work-nobody' })]) as never])
    }

    it('a closed seat whose session has an agent type shows that agent icon, not the default one', () => {
      gone()
      useAgentStore.setState({ agentTypes: { 'h1:S9': 'cc' } } as never)
      mount()
      expect(within(closedSeat()).queryByTestId('team-bead-bot')).toBeNull()
    })

    it('the host badge of a closed seat follows the agent type: terminal colours when the session has one, console otherwise', () => {
      useUISettingsStore.setState({ hostBadgeSidebarEnabled: true } as never)
      useHostStore.setState((s) => ({ hosts: { h1: { ...s.hosts.h1, colors: { console: { main: { color: '#ff0000', alpha: 100 } }, terminal: { main: { color: '#0000ff', alpha: 100 } } } } } }) as never)
      gone()
      const main = () => closedSeat().querySelector<HTMLElement>('[data-testid="host-badge"]')!.style.getPropertyValue('--hb-main')
      mount()
      const consoleColor = main()
      cleanup()
      useAgentStore.setState({ agentTypes: { 'h1:S9': 'cc' } } as never)
      mount()
      expect(consoleColor).not.toBe('')
      expect(main()).not.toBe('')
      expect(main()).not.toBe(consoleColor)
    })

    it('a seat row with a tab: the name is dimmed unless active, and the title after it is the tab title, not the seat label', () => {
      personas()
      useTeamRosterStore.getState().apply('h1', [teamOf(rosterSession('L', 'name-S1', { address: 'mlab/work-kenji' }), []) as never])
      mount()
      const row = within(screen.getByTestId('task-group-team')).getByTestId('inline-tab-row')
      expect(within(row).getByTestId('inline-tab-name').className).toContain('text-text-secondary')
      expect(within(row).getByTestId('inline-tab-subtitle')).toHaveTextContent('name-S1')
      expect(row).not.toHaveTextContent('title L')
    })
  })

  it('a click on a session row switches to its tab', () => {
    mount()
    fireEvent.click(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')[1])
    expect(onSelectTab).toHaveBeenCalledWith('t2')
  })

  it('reads the roots of the sessions it shows when the view opens (the groups are the device\'s own: nothing else to read)', () => {
    mount()
    expect(readFirstPage).toHaveBeenCalledWith('h1')
  })

  it.each([
    ['a narrow sidebar', { activityBarWidth: 'narrow', tabPosition: 'both' }],
    ['the tabs on the left', { activityBarWidth: 'wide', tabPosition: 'left' }],
  ] as const)('reads no roots when the task-group view is not really in force: %s', (_n, layout) => {
    act(() => { useLayoutStore.setState(layout) })
    mount()
    expect(readFirstPage).not.toHaveBeenCalled()
  })

  it('a group gathers sessions of ANY host: the same root on another host is another session, and there are no per-host blocks', () => {
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, t9: tab('t9', 'S9', 'AG9', 'h2'), t8: tab('t8', 'S8', 'AG8', 'h3') } } as never)
    useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: ['t1', 't2', 't3', 't9', 't8'], activeTabId: null, moduleConfig: {} }] } as never)
    useRootCacheStore.getState().ingest('h1', [{ session_id: 'AG1', root_session_id: 'ROOT1' }])
    useRootCacheStore.getState().ingest('h2', [{ session_id: 'AG9', root_session_id: 'ROOT9' }])
    useRootCacheStore.getState().ingest('h3', [{ session_id: 'AG8', root_session_id: 'ROOT1' }]) // same root text, another host
    setGroups([{ ...group('g1', ['ROOT1']), members: [{ host: 'h1', root: 'ROOT1' }, { host: 'h2', root: 'ROOT9' }] }])
    mount()
    expect(screen.getAllByTestId('task-group-manual')).toHaveLength(1)
    expect(within(screen.getByTestId('task-group-manual')).getAllByTestId('inline-tab-row')).toHaveLength(2) // t1 (h1) and t9 (h2)
    expect(screen.queryByTestId('task-group-host')).toBeNull()
  })

  it('an offline host still answers: the roots come from the device-local cache', () => {
    useRootCacheStore.getState().ingest('h1', [{ session_id: 'AG1', root_session_id: 'ROOT1' }])
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } } as never)
    setGroups([group('g1', ['ROOT1'])])
    mount()
    expect(within(screen.getByTestId('task-group-manual')).getAllByTestId('inline-tab-row')).toHaveLength(1)
  })

  it('shows an empty line when there is no session at all', () => {
    useTabStore.setState({ tabs: {} } as never)
    useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: [], activeTabId: null, moduleConfig: {} }] } as never)
    mount()
    expect(screen.getByTestId('task-group-empty')).toHaveTextContent('沒有 session')
  })

  it('follows the stores: a group that arrives after the first paint takes its sessions', () => {
    mount()
    expect(screen.queryByTestId('task-group-manual')).toBeNull()
    act(() => { setGroups([group('g1', ['ROOT1'])]); setRoots([['AG1', 'ROOT1']]) })
    expect(within(screen.getByTestId('task-group-manual')).getAllByTestId('inline-tab-row')).toHaveLength(1)
  })
})

describe('the right-click rows (NA-6c)', () => {
  const extras = () => useTabMenuExtrasStore.getState().items
  const rightClick = (el: HTMLElement) => fireEvent.contextMenu(el)

  beforeEach(() => { useTabMenuExtrasStore.getState().clear() })

  it('an ungrouped session is offered each group and a new one; the rows are set for that tab', () => {
    setGroups([group('g1', ['ROOT9'])])
    setRoots([['AG1', 'AG1']])
    mount()
    rightClick(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')[0])
    expect(useTabMenuExtrasStore.getState().tabId).toBe('t1')
    expect(extras().map((x) => x.key)).toEqual(['put:g1', 'new'])
    expect(extras().every((x) => !x.disabled)).toBe(true)
  })

  it('a session of any host is offered every group (a group is not tied to a host); a team seat, which is filed by its team, gets none', () => {
    useTabStore.setState({ tabs: { t4: tab('t4', 'S4', 'AG4', 'h2') }, activeTabId: null } as never)
    useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: ['t4'], activeTabId: null, moduleConfig: {} }] } as never)
    setGroups([group('g1', ['ROOT9'])])
    useTabMenuExtrasStore.getState().set('t4', [{ key: 'stale', label: 'Stale', onSelect: vi.fn() }])
    mount()
    rightClick(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')[0])
    expect(extras().map((x) => x.key)).toEqual(['put:g1', 'new'])
  })

  it('a session in a group is offered to be taken out of it', () => {
    setGroups([group('g1', ['ROOT1']), group('g2', [])])
    setRoots([['AG1', 'ROOT1']])
    mount()
    rightClick(within(screen.getAllByTestId('task-group-manual')[0]).getAllByTestId('inline-tab-row')[0])
    expect(extras().map((x) => x.key)).toEqual(['put:g2', 'new', 'out'])
  })

  it('its root is not read yet: the rows are there, enabled; choosing one asks the host, and only when the host cannot answer is it said (no-root) — nothing is written', async () => {
    setGroups([group('g1', [])])
    mount()
    rightClick(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')[0])
    expect(extras().every((x) => !x.disabled)).toBe(true)
    readFirstPage.mockClear()
    await act(async () => { extras().find((x) => x.key === 'put:g1')!.onSelect() })
    expect(readFirstPage).toHaveBeenCalledWith('h1') // the cache lacked it: the host is asked
    expect(await screen.findByTestId('task-group-notice')).toHaveTextContent('這個 session 的來源還沒讀到')
    expect(membersOf('g1')).toEqual([])
  })

  it('a refused edit is said in words, and can be dismissed', async () => {
    setGroups([group('g1', [])])
    setRoots([['AG1', 'AG1']])
    mount()
    rightClick(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')[0])
    const put = extras().find((x) => x.key === 'put:g1')!
    act(() => { setGroups([]) }) // another device deleted the group while the menu was up
    await act(async () => { put.onSelect() })
    expect(await screen.findByTestId('task-group-notice')).toHaveTextContent('這個任務編組已經不存在')
    fireEvent.click(screen.getByRole('button', { name: '關閉' }))
    expect(screen.queryByTestId('task-group-notice')).toBeNull()
  })
})

describe('dragging a session onto a group (NA-6c, TG-2b)', () => {
  const MIME = 'application/x-purdex-group-session'
  const dt = () => ({ types: [MIME], setData: vi.fn(), getData: () => '', effectAllowed: '', dropEffect: '' })
  const rowOf = (section: HTMLElement, i = 0) => within(section).getAllByTestId('task-group-session')[i]
  /** Drags `row` onto `target`: start, over, drop, end — true when the target let the drop through. */
  const drag = (row: HTMLElement, target: HTMLElement) => {
    const data = dt()
    fireEvent.dragStart(row, { dataTransfer: data })
    const allowed = !fireEvent.dragOver(target, { dataTransfer: data }) // preventDefault called = not "not cancelled"
    fireEvent.drop(target, { dataTransfer: data })
    fireEvent.dragEnd(row, { dataTransfer: data })
    return allowed
  }

  it('a solo session dropped on a group is filed there, by its lineage root', async () => {
    setGroups([group('g1', [])])
    setRoots([['AG1', 'ROOT1'], ['AG2', 'AG2'], ['AG3', 'AG3']])
    mount()
    drag(rowOf(screen.getByTestId('task-group-ungrouped')), screen.getByTestId('task-group-manual')); await act(async () => {})
    expect(membersOf('g1')).toEqual([{ host: 'h1', root: 'ROOT1' }])
  })

  it('dropped on 「未分組」 it is taken out of its group', async () => {
    setGroups([group('g1', ['ROOT1'])])
    setRoots([['AG1', 'ROOT1'], ['AG2', 'AG2'], ['AG3', 'AG3']])
    mount()
    drag(rowOf(screen.getByTestId('task-group-manual')), screen.getByTestId('task-group-ungrouped')); await act(async () => {})
    expect(membersOf('g1')).toEqual([])
  })

  it('a target that does not take the session lets nothing through: the group it is already in, and a team', async () => {
    const lead = { session_id: 'L', ref: '_L', address: 'mlab/L-x', live: true, tmux_session: 'name-S1', title: 'title L' }
    useTeamRosterStore.getState().apply('h1', [{ id: 'team-1', host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '', lead, members: [] } as never])
    setGroups([group('g1', ['ROOT2'])])
    setRoots([['AG1', 'AG1'], ['AG2', 'ROOT2'], ['AG3', 'AG3']])
    mount()
    const manual = screen.getByTestId('task-group-manual')
    expect(drag(rowOf(manual), manual)).toBe(false) // already in it
    expect(drag(rowOf(screen.getByTestId('task-group-ungrouped')), screen.getByTestId('task-group-team'))).toBe(false) // a team takes no drop
    await act(async () => {})
    expect(membersOf('g1')).toEqual([{ host: 'h1', root: 'ROOT2' }])
  })

  it('a session whose root the host cannot tell is not filed: the drop says so (no-root), after asking the host', async () => {
    setGroups([group('g1', [])])
    mount() // no roots read
    readFirstPage.mockClear()
    drag(rowOf(screen.getByTestId('task-group-ungrouped')), screen.getByTestId('task-group-manual')); await act(async () => {})
    expect(readFirstPage).toHaveBeenCalledWith('h1')
    expect(screen.getByTestId('task-group-notice')).toHaveTextContent('這個 session 的來源還沒讀到')
    expect(membersOf('g1')).toEqual([])
  })

  it('the drop uses the session as it is when it lands, not as it was when the drag began (its root was read meanwhile)', async () => {
    setGroups([group('g1', [])])
    mount() // no roots read yet
    const row = rowOf(screen.getByTestId('task-group-ungrouped'))
    const target = screen.getByTestId('task-group-manual')
    const data = dt()
    fireEvent.dragStart(row, { dataTransfer: data })
    act(() => { setRoots([['AG1', 'ROOT-NOW'], ['AG2', 'AG2'], ['AG3', 'AG3']]) })
    fireEvent.dragOver(target, { dataTransfer: data })
    fireEvent.drop(target, { dataTransfer: data })
    await act(async () => {})
    expect(membersOf('g1')).toEqual([{ host: 'h1', root: 'ROOT-NOW' }])
  })

  it('a drag that starts on a control inside the row (the close button) is not a group drag', async () => {
    setGroups([group('g1', [])])
    setRoots([['AG1', 'AG1'], ['AG2', 'AG2'], ['AG3', 'AG3']])
    mount()
    const row = rowOf(screen.getByTestId('task-group-ungrouped'))
    const target = screen.getByTestId('task-group-manual')
    const data = dt()
    fireEvent.mouseDown(row.querySelector('button') as HTMLElement)
    expect(fireEvent.dragStart(row, { dataTransfer: data })).toBe(false) // cancelled
    expect(fireEvent.dragOver(target, { dataTransfer: data })).toBe(true) // nothing taken: the drop is not let through
    fireEvent.drop(target, { dataTransfer: data })
    await act(async () => {})
    expect(membersOf('g1')).toEqual([])
    // ... and the next press on the row itself drags as usual
    fireEvent.mouseDown(within(row).getByTestId('inline-tab-title'))
    expect(fireEvent.dragStart(row, { dataTransfer: data })).toBe(true)
  })

  it('a team seat\'s row is not draggable', () => {
    const lead = { session_id: 'L', ref: '_L', address: 'mlab/L-x', live: true, tmux_session: 'name-S1', title: 'title L' }
    useTeamRosterStore.getState().apply('h1', [{ id: 'team-1', host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '', lead, members: [] } as never])
    setGroups([group('g1', [])])
    setRoots([['AG1', 'AG1'], ['AG2', 'AG2'], ['AG3', 'AG3']])
    mount()
    expect(within(screen.getByTestId('task-group-team')).queryAllByTestId('task-group-session')).toHaveLength(0)
  })

  it('a session of another host is draggable too, into a group that holds sessions of other hosts', async () => {
    useTabStore.setState({ tabs: { t9: tab('t9', 'S9', 'AG9', 'h2') }, activeTabId: null } as never)
    useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: ['t9'], activeTabId: null, moduleConfig: {} }] } as never)
    useRootCacheStore.getState().ingest('h2', [{ session_id: 'AG9', root_session_id: 'ROOT9' }])
    setGroups([group('g1', ['ROOT1'])])
    mount()
    expect(drag(rowOf(screen.getByTestId('task-group-ungrouped')), screen.getByTestId('task-group-manual'))).toBe(true)
    await act(async () => {})
    expect(membersOf('g1')).toEqual([{ host: 'h1', root: 'ROOT1' }, { host: 'h2', root: 'ROOT9' }])
  })
})

describe('the group headers (NA-6c, TG-2b)', () => {
  it('a manual group is renamed in place: double-click, type, Enter — written through the store', async () => {
    setGroups([group('g1', ['ROOT1'])])
    setRoots([['AG1', 'ROOT1']])
    mount()
    fireEvent.doubleClick(within(screen.getByTestId('task-group-manual')).getByTestId('task-group-name'))
    const input = screen.getByTestId('task-group-name-input')
    fireEvent.change(input, { target: { value: 'Billing' } })
    await act(async () => { fireEvent.keyDown(input, { key: 'Enter' }) })
    expect(useManualGroupsStore.getState().groups.g1.name).toBe('Billing')
    expect(within(screen.getByTestId('task-group-manual')).getByTestId('task-group-name')).toHaveTextContent('Billing')
  })

  it('a refused rename (a name the store would not take) is said in words and leaves the name alone', async () => {
    setGroups([group('g1', ['ROOT1'])])
    setRoots([['AG1', 'ROOT1']])
    mount()
    fireEvent.doubleClick(within(screen.getByTestId('task-group-manual')).getByTestId('task-group-name'))
    fireEvent.change(screen.getByTestId('task-group-name-input'), { target: { value: 'x'.repeat(70) } })
    await act(async () => { fireEvent.keyDown(screen.getByTestId('task-group-name-input'), { key: 'Enter' }) })
    expect(useManualGroupsStore.getState().groups.g1.name).toBe('Group g1')
    expect(screen.getByTestId('task-group-notice')).toHaveTextContent('名稱需要 1 到 64 個位元組')
  })

  it('only a group with nothing live offers 「清除」', () => {
    setGroups([group('g1', ['ROOT1']), group('g2', ['GONE'])])
    setRoots([['AG1', 'ROOT1']])
    mount()
    const [full, empty] = screen.getAllByTestId('task-group-manual')
    expect(within(full).queryByTestId('task-group-clear')).toBeNull()
    expect(within(empty).getByTestId('task-group-clear')).toBeInTheDocument()
  })

  it('「清除」 asks once, says what it also forgets, and deletes the group on confirm', async () => {
    setGroups([group('g1', ['GONE', 'GONE2']), group('g2', [])])
    setRoots([['AG1', 'AG1']])
    mount()
    fireEvent.click(within(screen.getAllByTestId('task-group-manual')[0]).getByTestId('task-group-clear'))
    expect(screen.getByTestId('task-group-clear-panel')).toHaveTextContent('清除「Group g1」？')
    expect(screen.getByTestId('task-group-clear-panel')).toHaveTextContent('2 個 session')
    fireEvent.click(screen.getByTestId('task-group-clear-cancel'))
    expect(useManualGroupsStore.getState().order).toEqual(['g1', 'g2'])
    fireEvent.click(within(screen.getAllByTestId('task-group-manual')[0]).getByTestId('task-group-clear'))
    await act(async () => { fireEvent.click(screen.getByTestId('task-group-clear-confirm')) })
    expect(useManualGroupsStore.getState().order).toEqual(['g2'])
    expect(screen.queryByTestId('task-group-clear-dialog')).toBeNull()
  })

  it('until the member hosts\' session roots are read, an empty-looking group cannot be cleared (it may hold live sessions)', () => {
    setGroups([group('g1', ['ROOT1'])])
    mount() // no roots read: the group looks empty because nobody is filed in it yet
    const clear = within(screen.getByTestId('task-group-manual')).getByTestId('task-group-clear')
    expect(clear).toHaveAttribute('title', '這台主機的 session 來源還在讀取中，暫時不能清除')
    fireEvent.click(clear)
    expect(screen.queryByTestId('task-group-clear-dialog')).toBeNull()
  })

  it('a group with a member on another host waits for THAT host to be read too', () => {
    setGroups([group('g1', ['GONE'], 'h2')])
    setRoots([['AG1', 'AG1']]) // h1 is read, h2 is not
    mount()
    expect(within(screen.getByTestId('task-group-manual')).getByTestId('task-group-clear')).toHaveAttribute('title', '這台主機的 session 來源還在讀取中，暫時不能清除')
  })

  it('a member on a host no open tab shows is still read, so the group can be cleared once that host says it is not alive', () => {
    useUnattendedStore.setState((s) => ({ byHost: { ...s.byHost, h2: { quotaSupport: 'yes' } } }) as never)
    setGroups([group('g1', ['GONE'], 'h2')])
    setRoots([['AG1', 'AG1']])
    mount()
    expect(readFirstPage).toHaveBeenCalledWith('h2') // no tab shows h2: the member alone makes it wanted
    act(() => { useRelayQuotaStore.setState((s) => ({ reads: { ...s.reads, h2: { ok: true, quotas: [] } } })) })
    expect(within(screen.getByTestId('task-group-manual')).getByTestId('task-group-clear')).not.toHaveAttribute('title')
  })

  it('a group whose member is alive but held by a team looks empty and still cannot be cleared', () => {
    const lead = { session_id: 'L', ref: '_L', address: 'mlab/L-x', live: true, tmux_session: 'name-S1', title: 'title L' }
    useTeamRosterStore.getState().apply('h1', [{ id: 'team-1', host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '', lead, members: [] } as never])
    setGroups([group('g1', ['ROOT1'])])
    setRoots([['AG1', 'ROOT1']]) // S1 (the team's lead) is in g1 by root; the team holds its row
    mount()
    const manual = screen.getByTestId('task-group-manual')
    expect(manual).toHaveAttribute('data-collapsed', 'true')
    const clear = within(manual).getByTestId('task-group-clear')
    expect(clear).toHaveAttribute('title', '這個編組還有在線的成員（例如在團隊裡），暫時不能清除')
    fireEvent.click(clear)
    expect(screen.queryByTestId('task-group-clear-dialog')).toBeNull()
  })

  it('the confirmation goes away if the group stops being empty while it is up', () => {
    setGroups([group('g1', ['LATER'])])
    setRoots([['AG1', 'AG1']])
    mount()
    fireEvent.click(within(screen.getByTestId('task-group-manual')).getByTestId('task-group-clear'))
    expect(screen.getByTestId('task-group-clear-dialog')).toBeInTheDocument()
    act(() => { setRoots([['AG1', 'AG1'], ['AG9', 'LATER']]) }) // a session whose root the group holds comes alive
    expect(screen.queryByTestId('task-group-clear-dialog')).toBeNull()
    expect(useManualGroupsStore.getState().order).toEqual(['g1'])
  })

  it('a team header opens the team edit form on a double-click when the lead\'s host offers it, and not otherwise', () => {
    const lead = { session_id: 'L', ref: '_L', address: 'mlab/L-x', live: true, tmux_session: 'name-S1', title: 'title L' }
    useTeamRosterStore.getState().apply('h1', [{ id: 'team-1', host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '', lead, members: [] } as never])
    mount()
    fireEvent.doubleClick(within(screen.getByTestId('task-group-team')).getByTestId('task-group-name'))
    expect(screen.queryByRole('textbox')).toBeNull()
    cleanup()
    act(() => { useUnattendedStore.setState({ byHost: { h1: { editSupport: 'yes' } } } as never) })
    mount()
    fireEvent.doubleClick(within(screen.getByTestId('task-group-team')).getByTestId('task-group-name'))
    expect(screen.getAllByRole('textbox').length).toBeGreaterThan(0)
  })
})

describe('headers: selection, collapse and the three manual group states (task groups spec §6.1, TG-3c)', () => {
  const g1 = manualGroupKey('g1')
  const rosterSession = (id: string, tmux: string) => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-x`, live: true, tmux_session: tmux, title: `title ${id}` })
  const teamOf = (lead: ReturnType<typeof rosterSession>, members: ReturnType<typeof rosterSession>[]) => ({
    id: 'team-1', host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '',
    lead, members: members.map((m) => ({ ...m, state: 'active', origin: 'spawned', joined_at: 1 })),
  })
  const head =(testId: string) => within(screen.getByTestId(testId)).getByTestId('task-group-header')
  const rowsIn = (testId: string) => within(screen.getByTestId(testId)).queryAllByTestId('inline-tab-row')

  it('a header click selects the group and activates its tab; the selected header is marked', () => {
    setGroups([group('g1', ['ROOT2'])])
    setRoots([['AG1', 'AG1'], ['AG2', 'ROOT2'], ['AG3', 'AG3']])
    mount()
    fireEvent.click(head('task-group-manual'))
    expect(useGroupViewStore.getState().selectedGroup).toBe(g1)
    expect(useTabStore.getState().activeTabId).toBe('t2')
    expect(head('task-group-manual')).toHaveAttribute('data-selected', 'true')
    expect(head('task-group-ungrouped')).not.toHaveAttribute('data-selected')
  })

  it('a click on the header of the selected group toggles it: collapses an expanded one and expands a collapsed one', () => {
    setGroups([group('g1', ['ROOT2'])])
    setRoots([['AG2', 'ROOT2']])
    mount()
    fireEvent.click(head('task-group-manual')) // select
    expect(rowsIn('task-group-manual')).toHaveLength(1)
    fireEvent.click(head('task-group-manual')) // selected + expanded: collapse
    expect(useGroupViewStore.getState().expanded[g1]).toBe(false)
    expect(rowsIn('task-group-manual')).toHaveLength(0)
    expect(within(screen.getByTestId('task-group-manual')).getByTestId('task-group-chevron')).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(head('task-group-manual')) // selected but collapsed: expands (WorkspaceRow's rule is a toggle)
    expect(useGroupViewStore.getState().expanded[g1]).toBe(true)
    expect(rowsIn('task-group-manual')).toHaveLength(1)
  })

  it('the chevron alone toggles, without selecting', () => {
    setGroups([group('g1', ['ROOT2'])])
    setRoots([['AG2', 'ROOT2']])
    mount()
    const chevron = within(screen.getByTestId('task-group-manual')).getByTestId('task-group-chevron')
    fireEvent.click(chevron)
    expect(rowsIn('task-group-manual')).toHaveLength(0)
    expect(useGroupViewStore.getState().selectedGroup).toBeNull()
    fireEvent.click(chevron)
    expect(rowsIn('task-group-manual')).toHaveLength(1)
  })

  it('「未分組」 and a team collapse and select the same way', () => {
    useTeamRosterStore.getState().apply('h1', [teamOf(rosterSession('L', 'name-S1'), [rosterSession('M', 'name-S2')]) as never])
    mount()
    expect(rowsIn('task-group-team')).toHaveLength(2)
    fireEvent.click(within(screen.getByTestId('task-group-team')).getByTestId('task-group-chevron'))
    expect(rowsIn('task-group-team')).toHaveLength(0)
    fireEvent.click(head('task-group-ungrouped'))
    expect(useGroupViewStore.getState().selectedGroup).toBe(UNGROUPED)
    expect(useTabStore.getState().activeTabId).toBe('t3')
    fireEvent.click(within(screen.getByTestId('task-group-ungrouped')).getByTestId('task-group-chevron'))
    expect(rowsIn('task-group-ungrouped')).toHaveLength(0)
  })

  it('a group with members but no tab is collapsed with 「清除」; one with neither shows a muted hint and no 「清除」', () => {
    setGroups([group('g1', ['GONE']), { ...group('g2', []), id: 'g2' }])
    mount()
    const [withMembers, bare] = screen.getAllByTestId('task-group-manual')
    expect(within(withMembers).getByTestId('task-group-clear')).toBeInTheDocument()
    expect(within(withMembers).queryByTestId('task-group-hint')).toBeNull()
    expect(within(bare).queryByTestId('task-group-clear')).toBeNull()
    expect(within(bare).getByTestId('task-group-hint')).toHaveTextContent('拖曳 session 到這裡')
    expect(within(bare).queryByTestId('task-group-chevron')).toBeNull()
  })

  it('the hint of a group with neither members nor tabs shows even if the group was collapsed while it had tabs', () => {
    setGroups([{ ...group('g2', []), id: 'g2' }])
    useGroupViewStore.setState({ expanded: { [manualGroupKey('g2')]: false } })
    mount()
    expect(within(screen.getByTestId('task-group-manual')).getByTestId('task-group-hint')).toBeInTheDocument()
  })

  it('selecting an empty group sets the active tab to none and keeps the group selected', () => {
    setGroups([{ ...group('g2', []), id: 'g2' }])
    useTabStore.setState({ activeTabId: 't1' } as never)
    mount()
    fireEvent.click(head('task-group-manual'))
    expect(useTabStore.getState().activeTabId).toBeNull()
    expect(useGroupViewStore.getState().selectedGroup).toBe(manualGroupKey('g2'))
  })

  it('selecting a group again returns to the tab that was last active in it', () => {
    setGroups([group('g1', ['ROOT1', 'ROOT2'])])
    setRoots([['AG1', 'ROOT1'], ['AG2', 'ROOT2'], ['AG3', 'AG3']])
    useGroupViewStore.setState({ lastActiveTab: { [g1]: 't2' } })
    mount()
    fireEvent.click(head('task-group-manual'))
    expect(useTabStore.getState().activeTabId).toBe('t2')
  })
})

describe('header menu: rename, colour, delete (task groups spec §6.1, TG-3d)', () => {
  const g1 = manualGroupKey('g1')
  const menuOf = (testId: string) => fireEvent.contextMenu(within(screen.getByTestId(testId)).getByTestId('task-group-header'))

  it('a right-click on a manual group header opens its menu; a team and 「未分組」 have none', () => {
    useTeamRosterStore.getState().apply('h1', [{ id: 'team-1', host_id: 'd', created_at: 1, team_name: 'P', team_label: '', lead: { session_id: 'L', ref: '_L', address: 'mlab/L-x', live: true, tmux_session: 'name-S1', title: 'L' }, members: [] } as never])
    setGroups([group('g1', ['ROOT2'])])
    mount()
    menuOf('task-group-manual')
    expect(screen.getByTestId('task-group-menu')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByTestId('task-group-menu')).toBeNull()
    menuOf('task-group-team')
    menuOf('task-group-ungrouped')
    expect(screen.queryByTestId('task-group-menu')).toBeNull()
  })

  it('「重新命名」 opens the in-place rename of that group', () => {
    setGroups([group('g1', ['ROOT2'])])
    mount()
    menuOf('task-group-manual')
    fireEvent.click(screen.getByTestId('task-group-menu-rename'))
    expect(screen.getByTestId('task-group-name-input')).toBeInTheDocument()
  })

  it('a colour from the menu is stored on the group (and the default clears it)', () => {
    setGroups([group('g1', ['ROOT2'])])
    mount()
    menuOf('task-group-manual')
    fireEvent.click(screen.getByTestId('task-group-menu-color-#fb923c'))
    expect(useManualGroupsStore.getState().groups.g1.color).toBe('#fb923c')
    menuOf('task-group-manual')
    fireEvent.click(screen.getByTestId('task-group-menu-color-default'))
    expect(useManualGroupsStore.getState().groups.g1.color).toBeUndefined()
  })

  it('deleting a group with nothing in it goes at once', () => {
    setGroups([{ ...group('g2', []), id: 'g2' }])
    mount()
    menuOf('task-group-manual')
    fireEvent.click(screen.getByTestId('task-group-menu-delete'))
    expect(screen.queryByTestId('task-group-delete-dialog')).toBeNull()
    expect(useManualGroupsStore.getState().groups.g2).toBeUndefined()
  })

  it('a group holding tabs asks first; cancel keeps it, confirm deletes it and its tabs fall back to 未分組', () => {
    setGroups([group('g1', ['ROOT2'])])
    setRoots([['AG2', 'ROOT2']])
    mount()
    expect(within(screen.getByTestId('task-group-manual')).getAllByTestId('inline-tab-row')).toHaveLength(1)
    menuOf('task-group-manual')
    fireEvent.click(screen.getByTestId('task-group-menu-delete'))
    expect(screen.getByTestId('task-group-delete-dialog')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('task-group-delete-cancel'))
    expect(useManualGroupsStore.getState().groups.g1).toBeDefined()
    menuOf('task-group-manual')
    fireEvent.click(screen.getByTestId('task-group-menu-delete'))
    fireEvent.click(screen.getByTestId('task-group-delete-confirm'))
    expect(useManualGroupsStore.getState().groups.g1).toBeUndefined()
    expect(screen.queryByTestId('task-group-manual')).toBeNull()
    expect(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')).toHaveLength(3)
  })

  it('a group with members but no tab asks first too', () => {
    setGroups([group('g1', ['GONE'])])
    mount()
    menuOf('task-group-manual')
    fireEvent.click(screen.getByTestId('task-group-menu-delete'))
    expect(screen.getByTestId('task-group-delete-dialog')).toBeInTheDocument()
    expect(useManualGroupsStore.getState().groups.g1).toBeDefined()
  })

  it('the tabs that named the deleted group in their own `group` lose it', () => {
    setGroups([group('g1', [])])
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, t1: { ...useTabStore.getState().tabs.t1, group: g1 } } } as never)
    mount()
    expect(within(screen.getByTestId('task-group-manual')).getAllByTestId('inline-tab-row')).toHaveLength(1) // t1 is in it by its own group
    menuOf('task-group-manual')
    fireEvent.click(screen.getByTestId('task-group-menu-delete'))
    fireEvent.click(screen.getByTestId('task-group-delete-confirm'))
    expect(useTabStore.getState().tabs.t1.group).toBeUndefined()
  })
})

