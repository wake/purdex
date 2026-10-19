// spa/src/features/workspace/components/InlineTab.persona.test.tsx — a sidebar session row wears its persona's avatar (name pool v2 spec §9.6, NA-5b): an 18 px avatar in the agent icon's place, the light kept at its
// top-right corner whatever the light style, then the host badge, the NAME (semibold) and the title (muted). Who it is comes from the roster seat, else the host's cached peers row (same tmux generation only);
// neither → the agent icon as always. The row asks for nothing: no peers refresh.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, act, within } from '@testing-library/react'
import { useAgentStore } from '../../../stores/useAgentStore'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useSessionStore } from '../../../stores/useSessionStore'
import { usePeerStore, emptyPeerHostEntry, type PeerRow } from '../../../stores/usePeerStore'
import { useTeamRosterStore } from '../../../stores/useTeamRosterStore'
import { usePersonaStore } from '../../../stores/usePersonaStore'
import { makeCollection } from '../../../lib/names/__fixtures__/personas'
import type { Tab } from '../../../types/tab'
import type { TeamRoster } from '../../../lib/team/roster'

vi.mock('@dnd-kit/sortable', () => ({
  useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: vi.fn(), transform: null, transition: null, isDragging: false }),
}))
vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: (name: string, weight: string) => `M ${name} ${weight}`,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

const { InlineTab } = await import('./InlineTab')

const INSTANCE = '4242:1700000000'
const tab: Tab = {
  id: 't1', kind: 'tmux-session', locked: false,
  layout: { type: 'leaf', pane: { id: 't1-pane', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'S1', terminated: false } } },
} as never

const peerRow = (address: string, tmuxInstance = INSTANCE): PeerRow => ({ address, ref: '_abc123', title: '', titleSource: '', deliverable: true, reason: '', tmuxInstance, agent: null })
const setPeers = (row: PeerRow) => act(() => { usePeerStore.setState({ byHost: { h1: { ...emptyPeerHostEntry(), rows: { S1: row }, fetchedAt: 1 } } }) })
const roster = (address: string): TeamRoster => ({
  id: 't1', host_id: 'd', created_at: 1, team_name: '', team_label: '',
  lead: { session_id: 'L', ref: '_L', address, live: true, tmux_session: 'work', tmux_instance: INSTANCE }, members: [],
})
const mount = () => render(<InlineTab tab={tab} isActive={false} onSelect={() => {}} onClose={() => {}} onMiddleClick={() => {}} onContextMenu={() => {}} />)
const avatar = () => screen.queryByTestId('inline-tab-avatar')

beforeEach(() => {
  cleanup()
  useAgentStore.setState({ statuses: { 'h1:S1': 'running' }, unread: {}, subagents: {}, agentTypes: { 'h1:S1': 'cc' }, oscTitles: {} } as never)
  useUISettingsStore.setState({
    dynamicTabName: false, tabNameTooltipMode: 'both', tabIndicatorStyle: 'badge', ccIconVariant: 'bot', codexIconVariant: 'openai',
    hostBadgeSidebarEnabled: false, hostBadgeSidebarLineColor: 'host', hostBadgeSidebarBox: 16, hostBadgeSidebarInset: 2, hostBadgeSidebarRadius: 4,
  })
  useSessionStore.setState({ sessions: { h1: [{ code: 'S1', name: 'work', tmux_instance: INSTANCE }] as never }, activeHostId: null, activeCode: null })
  useHostStore.setState({ runtime: {}, hosts: { h1: { id: 'h1', name: 'h1', ip: '127.0.0.1', port: 7860, order: 0 } } })
  useLayoutStore.setState(useLayoutStore.getInitialState())
  usePeerStore.setState({ byHost: {} })
  useTeamRosterStore.getState().reset()
  usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: makeCollection(['freya', 'kenji']) } } } as never)
})
afterEach(() => { cleanup() })

describe('a session row with a persona', () => {
  it('from the cached peers row (same tmux generation): the avatar replaces the agent icon, 18 px', () => {
    setPeers(peerRow('mlab/work-freya'))
    mount()
    expect(avatar()).not.toBeNull()
    const img = within(avatar()!).getByRole('img')
    expect(img).toHaveAttribute('aria-label', 'Freya')
    expect(img).toHaveStyle({ width: '18px', height: '18px' })
    expect(screen.queryByTestId('inline-tab-dot-overlay')).toBeNull() // the agent icon's slot is not there
  })

  it('from the roster seat; the roster wins over a peers row that says another persona', () => {
    useTeamRosterStore.getState().apply('h1', [roster('mlab/work-kenji')])
    setPeers(peerRow('mlab/work-freya'))
    mount()
    expect(within(avatar()!).getByRole('img')).toHaveAttribute('aria-label', 'Kenji')
  })

  it('the name is semibold and the title follows it, muted', () => {
    setPeers(peerRow('mlab/work-freya'))
    mount()
    const title = screen.getByTestId('inline-tab-title')
    expect(within(title).getByTestId('inline-tab-name')).toHaveTextContent('Freya')
    expect(within(title).getByTestId('inline-tab-name').className).toContain('font-semibold')
    expect(title).toHaveTextContent('Freya')
    expect(title).toHaveTextContent('work') // the title the row had before (no dynamic name: the tmux name)
    expect(within(title).getByTestId('inline-tab-subtitle').className).toContain('text-text-muted')
  })

  it.each(['badge', 'dot', 'iconDot'] as const)('the light stays at the corner in the "%s" style', (style) => {
    useUISettingsStore.setState({ tabIndicatorStyle: style })
    setPeers(peerRow('mlab/work-freya'))
    mount()
    expect(within(avatar()!).getByTestId('tab-status-indicator')).toBeInTheDocument()
  })

  it('lights off: a plain avatar, no light', () => {
    useUISettingsStore.setState({ tabIndicatorStyle: 'icon' })
    setPeers(peerRow('mlab/work-freya'))
    mount()
    expect(avatar()).not.toBeNull()
    expect(within(avatar()!).queryByTestId('tab-status-indicator')).toBeNull()
  })
})

describe('a session row without one keeps the agent icon (and its light)', () => {
  it.each([
    ['the peers row is of another tmux generation', () => setPeers(peerRow('mlab/work-freya', '9999:1'))],
    ['the persona is not one the App knows', () => setPeers(peerRow('mlab/work-nobody'))],
    ['nothing is cached', () => {}],
  ])('%s', (_n, setup) => {
    setup()
    mount()
    expect(avatar()).toBeNull()
    expect(screen.getByTestId('inline-tab-dot-overlay')).toBeInTheDocument()
    expect(screen.getByTestId('tab-status-indicator')).toBeInTheDocument()
    expect(screen.queryByTestId('inline-tab-name')).toBeNull()
  })
})

describe('the row asks for nothing', () => {
  it('rendering never refreshes the peers', () => {
    const refresh = vi.spyOn(usePeerStore.getState(), 'refresh')
    setPeers(peerRow('mlab/work-freya'))
    mount()
    expect(refresh).not.toHaveBeenCalled()
    refresh.mockRestore()
  })
})
