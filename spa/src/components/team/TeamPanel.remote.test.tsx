// spa/src/components/team/TeamPanel.remote.test.tsx — remote members in the team panel (cross-host teams, X5-App-a): the
// host chip, `context_unavailable`, the joining / releasing / killing words and dimmed light, and a click on a seat whose
// host this Mac does not have. Real stores and provider; the light and host badge are stand-ins (as in TeamPanelArea.test).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useI18nStore } from '../../stores/useI18nStore'
import type { RosterMember } from '../../lib/team/roster'

vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: ({ hostId }: { hostId: string }) => <span data-testid="seat-icon" data-host={hostId} />,
  TeamSeatHostBadge: ({ hostId }: { hostId: string }) => <span data-testid="seat-host" data-host={hostId} />,
}))

const ZH = {
  notInApp: '此主機未加入本機 App', noAnswer: '主機沒有回應',
  joining: '加入中', releasing: '釋出中', killing: '結束中',
}

type Extra = Partial<RosterMember> & { host_untrusted?: boolean }
function patchRemote(extra: Extra) {
  const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
  roster[0].members = roster[0].members.map((m) => (m.session_id === 'R'
    ? { ...m, host_id: 'dm-b', host_alias: 'b26', ...extra } : m))
  useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
}

/** A team of a lead and a local member A, plus a remote member R on daemon `dm-b` ("b26"). */
function scene(opts: { extra?: Extra; mapped?: 'verified' | 'unverified' | 'none'; remoteTab?: boolean } = {}) {
  const { extra = {}, mapped = 'verified', remoteTab = false } = opts
  seedScene({
    members: [['A', 'a-tm'], ['R', 'r-tm']],
    tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']],
    workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }],
    activeTabId: 'lead',
  })
  patchRemote(extra)
  if (mapped !== 'none') {
    useHostStore.setState({
      hosts: { h2: { id: 'h2', name: 'b26', ip: '10.0.0.2', port: 7860, daemonId: 'dm-b' } } as never,
      runtime: (mapped === 'verified' ? { h2: { daemonIdVerified: { endpoint: '10.0.0.2:7860', daemonId: 'dm-b' } } } : {}) as never,
      hostOrder: [HOST, 'h2'],
    })
  }
  useSessionStore.setState({
    sessions: { ...useSessionStore.getState().sessions, h2: [{ code: 'code-r-tm', name: 'r-tm', mode: 'terminal', cwd: '~' }] as never },
  })
  useShownHostsStore.setState({ ids: [HOST, 'h2'] })
  if (remoteTab) {
    const tab = {
      id: 'mr', pinned: false, locked: false, createdAt: 0,
      layout: { type: 'leaf', pane: { id: 'p-mr', content: { kind: 'tmux-session', hostId: 'h2', sessionCode: 'code-r-tm', mode: 'terminal', cachedName: 'r-tm', tmuxInstance: 'i' } } },
    } as never
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, mr: tab }, tabOrder: [...useTabStore.getState().tabOrder, 'mr'] })
  }
}

const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
const row = (id: string) => screen.getAllByTestId('team-panel-row').find((r) => r.getAttribute('data-session-id') === id)!
const cell = (id: string) => screen.getAllByTestId('team-panel-cell').find((r) => r.getAttribute('data-session-id') === id)!

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useHostStore.setState({ hosts: {}, runtime: {}, hostOrder: [] } as never)
  useTeamUiStore.setState({ panel: { width: 312, expanded: false }, teamDrill: {}, workbookTabs: {} })
  clearModuleRegistry()
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => cleanup())

describe('the host chip', () => {
  it('a remote member shows its own host_alias, with or without a tab; a local row has no chip', () => {
    scene()
    mount()
    expect(within(row('R')).getByTestId('team-panel-host-chip')).toHaveTextContent('b26')
    expect(within(row('R')).getByText(/未開/)).toBeTruthy() // no tab: still drawn, from the roster
    expect(within(row('A')).queryByTestId('team-panel-host-chip')).toBeNull()
    expect(within(row('L')).queryByTestId('team-panel-host-chip')).toBeNull()
  })

  it('a remote member WITH a tab on the mapped host is that tab\'s seat (no "unopened") and still shows its alias', () => {
    scene({ remoteTab: true })
    mount()
    expect(within(row('R')).getByTestId('team-panel-host-chip')).toHaveTextContent('b26')
    expect(within(row('R')).queryByText(/未開/)).toBeNull()
    expect(within(row('R')).getByTestId('seat-host')).toHaveAttribute('data-host', 'h2')
  })

  it('a remote member uses its OWN host, never the lead\'s: the light is keyed to h2, not the lead\'s host', () => {
    scene({ remoteTab: true })
    mount()
    expect(within(row('R')).getByTestId('seat-icon')).toHaveAttribute('data-host', 'h2')
    expect(within(row('A')).getByTestId('seat-icon')).toHaveAttribute('data-host', HOST)
  })

  it('a remote member with no alias on the wire still gets a chip (the generic host word)', () => {
    scene({ extra: { host_alias: undefined } })
    mount()
    expect(within(row('R')).getByTestId('team-panel-host-chip').textContent).toMatch(/\S/)
  })
})

describe('daemon id mapping is the only identity', () => {
  it('an unverified host, an unknown host, and a host whose alias matches but whose daemon id does not are all "not in this App"', () => {
    for (const mapped of ['unverified', 'none'] as const) {
      cleanup(); resetTeamStores()
      scene({ mapped })
      mount()
      expect(within(row('R')).getByTestId('seat-icon')).toHaveAttribute('data-host', '')
      expect(row('R')).toHaveAttribute('title', ZH.notInApp)
    }
    cleanup(); resetTeamStores()
    scene({ mapped: 'none' })
    // a host NAMED "b26" (the alias) with another daemon id, verified: the alias must not be used to guess
    useHostStore.setState({
      hosts: { h3: { id: 'h3', name: 'b26', ip: '10.0.0.3', port: 7860, daemonId: 'dm-other' } } as never,
      runtime: { h3: { daemonIdVerified: { endpoint: '10.0.0.3:7860', daemonId: 'dm-other' } } } as never,
    })
    mount()
    expect(within(row('R')).getByTestId('seat-icon')).toHaveAttribute('data-host', '')
    expect(row('R')).toHaveAttribute('title', ZH.notInApp)
  })

  it('a verified host maps: no "not in App" tooltip', () => {
    scene()
    mount()
    expect(row('R')).not.toHaveAttribute('title')
  })
})

describe('context_unavailable', () => {
  const seedReadings = { model: 'claude-sonnet-5-5', effort: 'low', context: { used_percentage: 42, window: 1000, at: 1 } }

  it('model and context read "—" with the tooltip; a row that answered shows its numbers', () => {
    scene({ extra: { ...seedReadings, context_unavailable: true } })
    mount()
    expect(within(row('R')).getByTestId('team-panel-model')).toHaveTextContent('—')
    expect(within(row('R')).getByTestId('team-panel-ctx')).toHaveTextContent('—')
    expect(within(row('R')).getByTestId('team-panel-model')).toHaveAttribute('title', ZH.noAnswer)
    expect(within(row('R')).getByTestId('team-panel-ctx').parentElement).toHaveAttribute('title', ZH.noAnswer)
  })

  it('without the flag the same readings are drawn', () => {
    scene({ extra: seedReadings })
    mount()
    expect(within(row('R')).getByTestId('team-panel-ctx')).toHaveTextContent('42%')
    expect(within(row('R')).getByTestId('team-panel-model')).not.toHaveTextContent('—')
  })
})

describe('joining / releasing / killing', () => {
  it.each([['joining', ZH.joining], ['releasing', ZH.releasing], ['killing', ZH.killing]])('%s: a state word after the title and a dimmed light', (state, word) => {
    scene({ extra: { state } })
    mount()
    expect(within(row('R')).getByTestId('team-panel-state')).toHaveTextContent(word)
    expect(within(row('R')).getByTestId('team-panel-light')).toHaveAttribute('data-dim', 'true')
  })

  it('active (and an unknown value) adds no word and no dimming', () => {
    scene({ extra: { state: 'active' } })
    mount()
    expect(within(row('R')).queryByTestId('team-panel-state')).toBeNull()
    expect(within(row('R')).getByTestId('team-panel-light')).toHaveAttribute('data-dim', 'false')
    act(() => patchRemote({ state: 'weird' }))
    expect(within(row('R')).queryByTestId('team-panel-state')).toBeNull()
  })
})

describe('one-line mode', () => {
  const toLine = () => act(() => useTeamUiStore.getState().setPanelMode(`${HOST}\u0000t1`, 'line'))

  it('a remote cell has the host badge and the alias + state word in its tooltip, dimmed in a transition; a local cell has none of it', () => {
    scene({ extra: { state: 'releasing' } })
    toLine()
    mount()
    expect(within(cell('R')).getByTestId('seat-host')).toBeTruthy()
    expect(cell('R').getAttribute('title')).toContain('b26')
    expect(cell('R').getAttribute('title')).toContain(ZH.releasing)
    expect(within(cell('R')).getByTestId('team-panel-light')).toHaveAttribute('data-dim', 'true')
    expect(within(cell('A')).queryByTestId('seat-host')).toBeNull()
    expect(cell('A').getAttribute('title')).not.toContain('b26')
    expect(within(cell('A')).getByTestId('team-panel-light')).toHaveAttribute('data-dim', 'false')
  })

  it('context_unavailable and not-in-App reach the tooltip', () => {
    scene({ mapped: 'none', extra: { context_unavailable: true } })
    toLine()
    mount()
    expect(cell('R').getAttribute('title')).toContain(ZH.noAnswer)
    expect(cell('R').getAttribute('title')).toContain(ZH.notInApp)
  })
})

describe('clicking a remote seat', () => {
  it('on a host this Mac does not have: nothing opens, the same sentence is toasted, the title tooltip carries the reason', () => {
    scene({ mapped: 'none' })
    mount()
    const tabs = Object.keys(useTabStore.getState().tabs)
    fireEvent.click(row('R'))
    fireEvent.keyDown(row('R'), { key: 'Enter' })
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(tabs)
    expect(useUndoToast.getState().toast?.message).toBe(ZH.notInApp)
    expect(within(row('R')).getByText('title R')).toHaveAttribute('title', `title R — ${ZH.notInApp}`)
    expect(within(row('A')).getByText('title A')).toHaveAttribute('title', 'title A') // other seats: unchanged
  })

  it('in line mode too', () => {
    scene({ mapped: 'none' })
    act(() => useTeamUiStore.getState().setPanelMode(`${HOST}\u0000t1`, 'line'))
    mount()
    const tabs = Object.keys(useTabStore.getState().tabs)
    fireEvent.click(cell('R'))
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(tabs)
    expect(useUndoToast.getState().toast?.message).toBe(ZH.notInApp)
  })

  it('on a verified host it opens a tab there (R3), or switches to the one it has', () => {
    scene()
    mount()
    fireEvent.click(row('R'))
    const opened = Object.values(useTabStore.getState().tabs).find((t) => t.layout.type === 'leaf' && t.layout.pane.content.kind === 'tmux-session' && t.layout.pane.content.hostId === 'h2')
    expect(opened).toBeTruthy()
    expect(useTabStore.getState().activeTabId).toBe(opened!.id)
  })
})
