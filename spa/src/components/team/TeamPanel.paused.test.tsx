// spa/src/components/team/TeamPanel.paused.test.tsx — the 被擋下 chip on the team panel's rows (UP-5b): shown exactly when the
// roster row has `paused`, with the light text and a 「解除」 for that seat. Real stores and provider.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { clearModuleRegistry } from '../../lib/module-registry'
import { postPauseRelease } from '../../lib/team/paused-api'

vi.mock('../../lib/team/paused-api', async (orig) => ({ ...(await orig<typeof import('../../lib/team/paused-api')>()), postPauseRelease: vi.fn(async () => ({ kind: 'released', n: 1 })) }))
vi.mock('../../lib/team/paused-refresh', () => ({ refreshPaused: vi.fn(async () => {}) }))
vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
const row = (id: string) => screen.getAllByTestId('team-panel-row').find((r) => r.getAttribute('data-session-id') === id)!

function pause(ids: Record<string, string[]>) {
  const teams = useTeamRosterStore.getState().byHost[HOST]
  useTeamRosterStore.getState().apply(HOST, teams.map((t) => ({
    ...t,
    lead: ids[t.lead.session_id] ? { ...t.lead, paused: { causes: ids[t.lead.session_id] as never, since: 1 } } : t.lead,
    members: t.members.map((m) => (ids[m.session_id] ? { ...m, paused: { causes: ids[m.session_id] as never, since: 1 } } : m)),
  })))
}

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useWorkbookStore.getState().reset()
  useTeamUiStore.setState({ panel: { width: 412 }, teamDrill: {}, endedSeats: {} })
  clearModuleRegistry()
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('zh-TW')
  seedScene({ members: [['A', 'a-tm'], ['B', 'b-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }], activeTabId: 'lead' })
})
afterEach(() => cleanup())

describe('the paused chip', () => {
  it('appears only on rows whose roster entry has paused, lead and members alike', () => {
    pause({ L: ['quota'], A: ['lead'] })
    mount()
    expect(within(row('L')).getByTestId('team-panel-paused')).toHaveTextContent('被擋下：接力額度用完')
    expect(within(row('A')).getByTestId('team-panel-paused')).toHaveTextContent('被擋下：lead 已暫停')
    expect(within(row('B')).queryByTestId('team-panel-paused')).toBeNull()
  })
  it('is absent when nothing is paused', () => {
    mount()
    expect(screen.queryByTestId('team-panel-paused')).toBeNull()
  })
  it('its 解除 releases that seat on its host', async () => {
    pause({ A: ['quota'] })
    mount()
    await act(async () => { within(row('A')).getByTestId('pause-release').click() })
    expect(postPauseRelease).toHaveBeenCalledWith(HOST, 'A')
  })
  it('relay_unanswered: the lead reads the unanswered text, a member waits for its lead, and 解除 asks first', async () => {
    pause({ L: ['relay_unanswered'], A: ['quota', 'relay_unanswered'] })
    mount()
    expect(within(row('L')).getByTestId('team-panel-paused')).toHaveTextContent('被擋下：接力申請沒有回應')
    expect(within(row('A')).getByTestId('team-panel-paused')).toHaveTextContent('被擋下：等待 lead 確認接力')
    vi.mocked(postPauseRelease).mockClear()
    await act(async () => { within(row('A')).getByTestId('pause-release').click() })
    expect(postPauseRelease).not.toHaveBeenCalled()
    await act(async () => { screen.getByTestId('pause-release-confirm-confirm').click() })
    expect(postPauseRelease).toHaveBeenCalledWith(HOST, 'A')
  })
})
