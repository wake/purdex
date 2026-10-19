// spa/src/features/workspace/components/TaskGroupView.test.tsx — the sidebar's task-group view (name pool v2 spec §7, NA-6b): team groups, then each host's manual groups, then 未分組; a click on a session switches to
// its tab; a group with nothing live is one collapsed line; a host that cannot be written to says so on its block. Read-only here (the editing is 6c). The rows are the sidebar's own InlineTab, so the
// avatar, the light and the title come as they do in the workspace view. Real stores; the daemon reads are mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, within, act } from '@testing-library/react'

vi.mock('@dnd-kit/sortable', () => ({
  useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: vi.fn(), transform: null, transition: null, isDragging: false }),
}))
vi.mock('../lib/icon-path-cache', () => ({ getIconPath: (n: string, w: string) => `M ${n} ${w}`, isWeightLoaded: () => true, prefetchWeight: () => Promise.resolve() }))
const loadIfSupported = vi.fn(async (_hostId: string) => {})
vi.mock('../../../lib/task-groups/loader', () => ({ taskGroupsSupported: (h: string) => h === 'h1', loadTaskGroupsIfSupported: (h: string) => loadIfSupported(h) }))
const readFirstPage = vi.fn(async (_hostId: string) => ({}))
// a fresh read answers with the quota rows the test put in the store
const freshRead = async (h: string) => { const r = useRelayQuotaStore.getState().reads[h]; return { quotas: r?.ok ? r.quotas : undefined, approved: [], truncated: false } }
vi.mock('../../../lib/team/unattended-reader', () => ({ readFirstPage: (h: string) => readFirstPage(h), refreshHost: (h: string) => freshRead(h) }))

const putTaskGroups = vi.fn()
vi.mock('../../../lib/task-groups/api', async (orig) => ({ ...(await orig<typeof import('../../../lib/task-groups/api')>()), putTaskGroups: (...a: unknown[]) => putTaskGroups(...a) }))

const openTeamSeat = vi.fn()
vi.mock('../../../lib/team/team-actions', () => ({ openTeamSeat: (...a: unknown[]) => openTeamSeat(...a) }))

import { TaskGroupView } from './TaskGroupView'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../store'
import { useSessionStore } from '../../../stores/useSessionStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useTeamRosterStore } from '../../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../../stores/useUnattendedStore'
import { useTaskGroupsStore } from '../../../stores/useTaskGroupsStore'
import { useTabMenuExtrasStore } from '../../../stores/useTabMenuExtrasStore'
import { useRelayQuotaStore } from '../../../lib/team/relay-quota'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { useAgentStore } from '../../../stores/useAgentStore'
import type { Tab } from '../../../types/tab'
import type { TaskGroup } from '../../../lib/task-groups/api'

const tab = (id: string, code: string, agentSessionId: string | null, hostId = 'h1'): Tab => ({
  id, kind: 'tmux-session', locked: false, pinned: false,
  layout: { type: 'leaf', pane: { id: `${id}-p`, content: { kind: 'tmux-session', hostId, sessionCode: code, mode: 'terminal', cachedName: `name-${code}`, tmuxInstance: 'I', ...(agentSessionId ? { rebuild: { agent: { sessionId: agentSessionId } } } : {}) } } },
} as never)
const group = (id: string, roots: string[], created_at = 1): TaskGroup => ({ id, name: `Group ${id}`, roots, created_at, updated_at: created_at })
const setGroups = (hostId: string, groups: TaskGroup[], over: Record<string, unknown> = {}) =>
  useTaskGroupsStore.setState({ byHost: { [hostId]: { status: 'ready', groups, revision: 1, loaded: true, dropped: null, invalid: false, ...over } as never } })
const setRoots = (rows: Array<[string, string]>) =>
  useRelayQuotaStore.setState({ reads: { h1: { ok: true, quotas: rows.map(([session_id, root_session_id]) => ({ session_id, root_session_id, title: '', address: `a/${session_id}`, is_lead: false, self_left: 0, member_pool_left: 0, rev: 1 })) } } })

const onSelectTab = vi.fn()
const mount = () => render(<TaskGroupView activeTabId={null} onSelectTab={onSelectTab} onCloseTab={vi.fn()} onMiddleClickTab={vi.fn()} onContextMenuTab={vi.fn()} />)

beforeEach(() => {
  cleanup()
  onSelectTab.mockClear(); loadIfSupported.mockClear(); readFirstPage.mockClear(); openTeamSeat.mockClear()
  useI18nStore.getState().setLocale('zh-TW')
  useUISettingsStore.setState({ dynamicTabName: false, tabNameTooltipMode: 'both', tabIndicatorStyle: 'badge', ccIconVariant: 'bot', codexIconVariant: 'openai', hostBadgeSidebarEnabled: false } as never)
  useAgentStore.setState({ statuses: {}, unread: {}, subagents: {}, agentTypes: {}, oscTitles: {} } as never)
  const tabs = { t1: tab('t1', 'S1', 'AG1'), t2: tab('t2', 'S2', 'AG2'), t3: tab('t3', 'S3', 'AG3') }
  useTabStore.setState({ tabs, activeTabId: null } as never)
  useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: ['t1', 't2', 't3'], activeTabId: null, moduleConfig: {} }], activeWorkspaceId: 'w1' } as never)
  useSessionStore.setState({ sessions: { h1: [{ code: 'S1', name: 'name-S1' }, { code: 'S2', name: 'name-S2' }, { code: 'S3', name: 'name-S3' }] as never } })
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['h1'], runtime: { h1: { status: 'connected' } } } as never)
  useTeamRosterStore.getState().reset()
  useTaskGroupsStore.setState({ byHost: {} })
  useRelayQuotaStore.getState().reset()
})
afterEach(() => { cleanup() })

const sections = () => screen.getAllByTestId(/^task-group-(manual|team|ungrouped|unsafe)/)

describe('the task-group view', () => {
  it('lists a manual group with the sessions whose lineage root it holds (a relayed session included), then 未分組', () => {
    setGroups('h1', [group('g1', ['ROOT1'])])
    setRoots([['AG1', 'ROOT1'], ['AG2', 'AG2'], ['AG3', 'AG3']])
    mount()
    const secs = sections()
    expect(secs.map((s) => s.getAttribute('data-testid'))).toEqual(['task-group-manual', 'task-group-ungrouped'])
    expect(within(secs[0]).getByTestId('task-group-name')).toHaveTextContent('Group g1')
    expect(within(secs[0]).getAllByTestId('inline-tab-row')).toHaveLength(1)
    expect(within(secs[0]).getByTestId('task-group-count')).toHaveTextContent('1')
    expect(within(secs[1]).getAllByTestId('inline-tab-row')).toHaveLength(2)
    expect(within(secs[1]).getByTestId('task-group-name')).toHaveTextContent('未分組')
  })

  it('until the roots are read, the sessions are 未分組 for the display', () => {
    setGroups('h1', [group('g1', ['ROOT1'])])
    mount()
    const manual = screen.getByTestId('task-group-manual')
    expect(within(manual).queryAllByTestId('inline-tab-row')).toHaveLength(0)
    expect(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')).toHaveLength(3)
  })

  it('a group with nothing live in it is one collapsed line', () => {
    setGroups('h1', [group('g1', ['GONE'])])
    setRoots([['AG1', 'AG1']])
    mount()
    const manual = screen.getByTestId('task-group-manual')
    expect(manual).toHaveAttribute('data-collapsed', 'true')
    expect(within(manual).queryByTestId('inline-tab-row')).toBeNull()
  })

  it('a split tab stands for the session in whichever pane shows one: a tmux session in a secondary pane is listed, a tab with no session at all is not', () => {
    const split = (id: string, children: unknown[]) => ({ type: 'split', id, direction: 'h', children, sizes: [50, 50] })
    const leaf = (id: string, content: unknown) => ({ type: 'leaf', pane: { id, content } })
    const t4 = { id: 't4', kind: 'tmux-session', locked: false, pinned: false, layout: split('s4', [leaf('p4a', { kind: 'new-tab' }), leaf('p4b', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'S4', mode: 'terminal', cachedName: 'n4', tmuxInstance: 'I', rebuild: { agent: { sessionId: 'AG4' } } })]) }
    const t5 = { id: 't5', kind: 'new-tab', locked: false, pinned: false, layout: leaf('p5', { kind: 'new-tab' }) }
    act(() => {
      useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, t4, t5 } } as never)
      useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: ['t1', 't2', 't3', 't4', 't5'], activeTabId: null, moduleConfig: {} }] } as never)
      setGroups('h1', [group('g1', ['ROOT4'])]); setRoots([['AG4', 'ROOT4']])
    })
    mount()
    expect(within(screen.getByTestId('task-group-manual')).getAllByTestId('inline-tab-row')).toHaveLength(1)
    expect(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')).toHaveLength(3) // t1 t2 t3; t5 shows no session
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
    expect(within(secs[0]).getByTestId('task-group-count')).toHaveTextContent('2')
    expect(within(secs[0]).getAllByTestId('inline-tab-row')).toHaveLength(2)
    expect(within(secs[1]).getAllByTestId('inline-tab-row')).toHaveLength(1) // t3 is nobody's
    fireEvent.click(within(secs[0]).getAllByTestId('inline-tab-row')[1])
    expect(openTeamSeat).toHaveBeenCalledWith(expect.stringContaining('team-1'), 'M')
    expect(onSelectTab).not.toHaveBeenCalled()
  })

  it('two seats in the panes of one split tab are two rows, each with its own title, each opening its own seat', () => {
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
    expect(rows.map((r) => r.textContent)).toEqual([expect.stringContaining('title L'), expect.stringContaining('title M')])
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

  it('a stored value that is not a list says so — no count it cannot know', () => {
    setGroups('h1', [], { invalid: true })
    mount()
    const unsafe = screen.getByTestId('task-group-unsafe')
    expect(unsafe).toHaveTextContent('資料格式讀不懂')
    expect(unsafe.textContent).not.toMatch(/\d 筆/)
  })

  it('a click on a session row switches to its tab', () => {
    mount()
    fireEvent.click(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')[1])
    expect(onSelectTab).toHaveBeenCalledWith('t2')
  })

  it('a host that cannot be written to says how many rows it could not read, with the daemon\'s reasons on hover', () => {
    setGroups('h1', [group('g1', ['R'])], { dropped: { count: 2, reasons: ['root "x" is already in group "a"'] } })
    mount()
    const unsafe = screen.getByTestId('task-group-unsafe')
    expect(unsafe).toHaveTextContent('這台主機的任務編組有 2 筆讀不懂，暫時不能修改')
    expect(unsafe).toHaveAttribute('title', expect.stringContaining('already in group'))
  })

  it('reads the groups of the connected hosts when the view opens, and the roots of the sessions it shows', () => {
    mount()
    expect(loadIfSupported).toHaveBeenCalledWith('h1')
    expect(readFirstPage).toHaveBeenCalledWith('h1')
  })

  it('reads the groups again once a daemon mismatch on a connected host is gone', () => {
    useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0, daemonId: 'D1' } } } as never)
    mount()
    act(() => { useHostStore.setState({ runtime: { h1: { status: 'connected', daemonIdMismatch: { stored: 'D1', observed: 'D2', endpoint: '1:1' } } } } as never) })
    loadIfSupported.mockClear()
    act(() => { useHostStore.setState({ runtime: { h1: { status: 'connected' } } } as never) })
    expect(loadIfSupported).toHaveBeenCalledWith('h1')
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
    act(() => { setGroups('h1', [group('g1', ['ROOT1'])]); setRoots([['AG1', 'ROOT1']]) })
    expect(within(screen.getByTestId('task-group-manual')).getAllByTestId('inline-tab-row')).toHaveLength(1)
  })
})

describe('the right-click rows (NA-6c)', () => {
  const extras = () => useTabMenuExtrasStore.getState().items
  const rightClick = (el: HTMLElement) => fireEvent.contextMenu(el)

  beforeEach(() => { useTabMenuExtrasStore.getState().clear() })

  it('an ungrouped session is offered each group and a new one; the rows are set for that tab', () => {
    setGroups('h1', [group('g1', ['ROOT9'])])
    setRoots([['AG1', 'AG1']])
    mount()
    rightClick(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')[0])
    expect(useTabMenuExtrasStore.getState().tabId).toBe('t1')
    expect(extras().map((x) => x.key)).toEqual(['put:g1', 'new'])
    expect(extras().every((x) => !x.disabled)).toBe(true)
  })

  it('a session on a host that cannot serve groups gets none, and clears what was left', () => {
    useTabStore.setState({ tabs: { t4: tab('t4', 'S4', 'AG4', 'h2') }, activeTabId: null } as never)
    useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: ['t4'], activeTabId: null, moduleConfig: {} }] } as never)
    useTabMenuExtrasStore.getState().set('t4', [{ key: 'stale', label: 'Stale', onSelect: vi.fn() }])
    mount()
    rightClick(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')[0])
    expect(extras()).toEqual([])
  })

  it('a session in a group is offered to be taken out of it', () => {
    setGroups('h1', [group('g1', ['ROOT1']), group('g2', [])])
    setRoots([['AG1', 'ROOT1']])
    mount()
    rightClick(within(screen.getAllByTestId('task-group-manual')[0]).getAllByTestId('inline-tab-row')[0])
    expect(extras().map((x) => x.key)).toEqual(['put:g2', 'new', 'out'])
  })

  it('its root is not read yet: the rows are there, disabled, and say so', () => {
    setGroups('h1', [group('g1', [])])
    mount()
    rightClick(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')[0])
    expect(extras().length).toBeGreaterThan(0)
    for (const x of extras()) { expect(x.disabled).toBe(true); expect(x.title).toBe('這個 session 的來源還在讀取中') }
  })

  it('an unsafe host: the rows are disabled with the count', () => {
    setGroups('h1', [group('g1', [])], { dropped: { count: 2, reasons: ['bad'] } })
    setRoots([['AG1', 'AG1']])
    mount()
    rightClick(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')[0])
    for (const x of extras()) { expect(x.disabled).toBe(true); expect(x.title).toBe('這台主機有 2 列讀不了，暫時不能編輯') }
  })

  it('a refused edit is said in words, and can be dismissed', async () => {
    setGroups('h1', [group('g1', [])])
    setRoots([['AG1', 'AG1']])
    mount()
    rightClick(within(screen.getByTestId('task-group-ungrouped')).getAllByTestId('inline-tab-row')[0])
    const put = extras().find((x) => x.key === 'put:g1')!
    act(() => { setGroups('h1', []) }) // another device deleted the group while the menu was up
    await act(async () => { put.onSelect() })
    expect(await screen.findByTestId('task-group-notice')).toHaveTextContent('這個任務編組已經不存在')
    fireEvent.click(screen.getByRole('button', { name: '關閉' }))
    expect(screen.queryByTestId('task-group-notice')).toBeNull()
  })
})

describe('dragging a session onto a group (NA-6c)', () => {
  const MIME = 'application/x-purdex-group-session'
  const sent = () => putTaskGroups.mock.calls.map((c) => c[1] as TaskGroup[])
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

  beforeEach(() => {
    putTaskGroups.mockReset()
    putTaskGroups.mockImplementation(async (_h: string, items: TaskGroup[], base: number) => ({ items, revision: base + 1, dropped: null, invalid: false }))
  })

  it('a solo session dropped on a group of its host is filed there, by its lineage root', async () => {
    setGroups('h1', [group('g1', [])])
    setRoots([['AG1', 'ROOT1'], ['AG2', 'AG2'], ['AG3', 'AG3']])
    mount()
    drag(rowOf(screen.getByTestId('task-group-ungrouped')), screen.getByTestId('task-group-manual')); await act(async () => {})
    expect(sent()[0][0].roots).toEqual(['ROOT1'])
  })

  it('dropped on 「未分組」 it is taken out of its group', async () => {
    setGroups('h1', [group('g1', ['ROOT1'])])
    setRoots([['AG1', 'ROOT1'], ['AG2', 'AG2'], ['AG3', 'AG3']])
    mount()
    drag(rowOf(screen.getByTestId('task-group-manual')), screen.getByTestId('task-group-ungrouped')); await act(async () => {})
    expect(sent()[0][0].roots).toEqual([])
  })

  it('a target that does not take the session lets nothing through: the group it is already in, and a team', () => {
    setGroups('h1', [group('g1', ['ROOT1'])])
    setRoots([['AG1', 'ROOT1'], ['AG2', 'AG2'], ['AG3', 'AG3']])
    mount()
    const manual = screen.getByTestId('task-group-manual')
    expect(drag(rowOf(manual), manual)).toBe(false) // already in it
    expect(putTaskGroups).not.toHaveBeenCalled()
  })

  it('an unsafe host takes the drop and says why instead of writing', async () => {
    setGroups('h1', [group('g1', [])], { dropped: { count: 2, reasons: ['bad'] } })
    setRoots([['AG1', 'AG1'], ['AG2', 'AG2'], ['AG3', 'AG3']])
    mount()
    expect(drag(rowOf(screen.getByTestId('task-group-ungrouped')), screen.getByTestId('task-group-manual'))).toBe(true)
    await act(async () => {})
    expect(screen.getByTestId('task-group-notice')).toHaveTextContent('這台主機有 2 列讀不了，暫時不能編輯')
    expect(putTaskGroups).not.toHaveBeenCalled()
  })

  it('a session whose root is not read yet is not filed: the drop says it is still being read', async () => {
    setGroups('h1', [group('g1', [])])
    mount() // no roots read
    drag(rowOf(screen.getByTestId('task-group-ungrouped')), screen.getByTestId('task-group-manual')); await act(async () => {})
    expect(screen.getByTestId('task-group-notice')).toHaveTextContent('這個 session 的來源還在讀取中')
    expect(putTaskGroups).not.toHaveBeenCalled()
  })

  it('a team seat\'s row is not draggable, and a team takes no drop', () => {
    const lead = { session_id: 'L', ref: '_L', address: 'mlab/L-x', live: true, tmux_session: 'name-S1', title: 'title L' }
    useTeamRosterStore.getState().apply('h1', [{ id: 'team-1', host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '', lead, members: [] } as never])
    setGroups('h1', [group('g1', [])])
    setRoots([['AG1', 'AG1'], ['AG2', 'AG2'], ['AG3', 'AG3']])
    mount()
    expect(within(screen.getByTestId('task-group-team')).queryAllByTestId('task-group-session')).toHaveLength(0)
    const solo = rowOf(screen.getByTestId('task-group-ungrouped'))
    expect(drag(solo, screen.getByTestId('task-group-team'))).toBe(false)
    expect(putTaskGroups).not.toHaveBeenCalled()
  })

  it('a session of another host cannot be dropped on this host\'s group', () => {
    useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0 }, h2: { id: 'h2', name: 'air', ip: '2', port: 1, token: 't', order: 1 } }, hostOrder: ['h1', 'h2'], runtime: { h1: { status: 'connected' }, h2: { status: 'connected' } } } as never)
    useTabStore.setState({ tabs: { t9: tab('t9', 'S9', 'AG9', 'h2') }, activeTabId: null } as never)
    useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W', tabs: ['t9'], activeTabId: null, moduleConfig: {} }] } as never)
    setGroups('h1', [group('g1', [])])
    mount()
    expect(drag(rowOf(screen.getByTestId('task-group-ungrouped')), screen.getByTestId('task-group-manual'))).toBe(false)
    expect(putTaskGroups).not.toHaveBeenCalled()
  })
})

describe('the group headers (NA-6c)', () => {
  const sent = () => putTaskGroups.mock.calls.map((c) => c[1] as TaskGroup[])
  beforeEach(() => {
    putTaskGroups.mockReset()
    putTaskGroups.mockImplementation(async (_h: string, items: TaskGroup[], base: number) => ({ items, revision: base + 1, dropped: null, invalid: false }))
  })

  it('a manual group is renamed in place: double-click, type, Enter — written through the store', async () => {
    setGroups('h1', [group('g1', ['ROOT1'])])
    setRoots([['AG1', 'ROOT1']])
    mount()
    fireEvent.doubleClick(within(screen.getByTestId('task-group-manual')).getByTestId('task-group-name'))
    const input = screen.getByTestId('task-group-name-input')
    fireEvent.change(input, { target: { value: 'Billing' } })
    await act(async () => { fireEvent.keyDown(input, { key: 'Enter' }) })
    expect(sent()[0].map((g) => g.name)).toEqual(['Billing'])
    expect(within(screen.getByTestId('task-group-manual')).getByTestId('task-group-name')).toHaveTextContent('Billing')
  })

  it('an unsafe host: the name does not open and 「清除」 is inert, both saying the same sentence', () => {
    setGroups('h1', [group('g1', ['GONE'])], { dropped: { count: 2, reasons: ['bad'] } })
    setRoots([['AG1', 'AG1']])
    mount()
    const manual = screen.getByTestId('task-group-manual')
    fireEvent.doubleClick(within(manual).getByTestId('task-group-name'))
    expect(screen.queryByTestId('task-group-name-input')).toBeNull()
    expect(within(manual).getByTestId('task-group-name')).toHaveAttribute('title', '這台主機有 2 列讀不了，暫時不能編輯')
    fireEvent.click(within(manual).getByTestId('task-group-clear'))
    expect(within(manual).getByTestId('task-group-clear')).toHaveAttribute('title', '這台主機有 2 列讀不了，暫時不能編輯')
    expect(screen.queryByTestId('task-group-clear-dialog')).toBeNull()
  })

  it('only a group with nothing live offers 「清除」', () => {
    setGroups('h1', [group('g1', ['ROOT1']), group('g2', ['GONE'])])
    setRoots([['AG1', 'ROOT1']])
    mount()
    const [full, empty] = screen.getAllByTestId('task-group-manual')
    expect(within(full).queryByTestId('task-group-clear')).toBeNull()
    expect(within(empty).getByTestId('task-group-clear')).toBeInTheDocument()
  })

  it('「清除」 asks once, says what it also forgets, and deletes the group on confirm', async () => {
    setGroups('h1', [group('g1', ['GONE', 'GONE2']), group('g2', [])])
    setRoots([['AG1', 'AG1']])
    mount()
    fireEvent.click(within(screen.getAllByTestId('task-group-manual')[0]).getByTestId('task-group-clear'))
    expect(screen.getByTestId('task-group-clear-panel')).toHaveTextContent('清除「Group g1」？')
    expect(screen.getByTestId('task-group-clear-panel')).toHaveTextContent('2 個 session')
    fireEvent.click(screen.getByTestId('task-group-clear-cancel'))
    expect(putTaskGroups).not.toHaveBeenCalled()
    fireEvent.click(within(screen.getAllByTestId('task-group-manual')[0]).getByTestId('task-group-clear'))
    await act(async () => { fireEvent.click(screen.getByTestId('task-group-clear-confirm')) })
    expect(sent()[0].map((g) => g.id)).toEqual(['g2'])
    expect(screen.queryByTestId('task-group-clear-dialog')).toBeNull()
  })

  it('until the host\'s session roots are read, an empty-looking group cannot be cleared (it may hold live sessions)', () => {
    setGroups('h1', [group('g1', ['ROOT1'])])
    mount() // no roots read: the group looks empty because nobody is filed in it yet
    const clear = within(screen.getByTestId('task-group-manual')).getByTestId('task-group-clear')
    expect(clear).toHaveAttribute('title', '這台主機的 session 來源還在讀取中，暫時不能清除')
    fireEvent.click(clear)
    expect(screen.queryByTestId('task-group-clear-dialog')).toBeNull()
  })

  it('a group whose member is alive but held by a team looks empty and still cannot be cleared', () => {
    const lead = { session_id: 'L', ref: '_L', address: 'mlab/L-x', live: true, tmux_session: 'name-S1', title: 'title L' }
    useTeamRosterStore.getState().apply('h1', [{ id: 'team-1', host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '', lead, members: [] } as never])
    setGroups('h1', [group('g1', ['ROOT1'])])
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
    setGroups('h1', [group('g1', ['LATER'])])
    setRoots([['AG1', 'AG1']])
    mount()
    fireEvent.click(within(screen.getByTestId('task-group-manual')).getByTestId('task-group-clear'))
    expect(screen.getByTestId('task-group-clear-dialog')).toBeInTheDocument()
    act(() => { setRoots([['AG1', 'AG1'], ['AG9', 'LATER']]) }) // a session whose root the group holds comes alive
    expect(screen.queryByTestId('task-group-clear-dialog')).toBeNull()
    expect(putTaskGroups).not.toHaveBeenCalled()
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
