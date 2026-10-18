// Regression (CLAUDE.md tab-hosted rule, NA-4b): the team settings block is part of a tab-hosted panel. Real TabContent (the pane really unmounts: keepAliveCount 0):
// what the block shows lives in the stores, so it is the same on return — the cap, the relay quotas the host last answered, and a cap the roster moved while the reader was
// away. The read the block starts is held to its end: a switch away does not cancel what fills the store.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { TeamSettingsBlock } from './TeamSettingsBlock'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useRelayQuotaStore } from '../../lib/team/relay-quota'
import { resetReader } from '../../lib/team/unattended-reader'
import { resetWriter } from '../../lib/team/relay-quota-writer'
import { getUnattended } from '../../lib/team/unattended-api'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import type { RosterSession, TeamRoster } from '../../lib/team/roster'
import type { UnattendedView } from '../../lib/team/types'

vi.mock('../../lib/team/unattended-api', () => ({ getUnattended: vi.fn(), putRelayQuota: vi.fn() }))
const mockedGet = vi.mocked(getUnattended)

const A = 'host-a'
const T = 't-L'
const sess = (id: string): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true })
const team = (over: Partial<TeamRoster> = {}): TeamRoster => ({ id: T, host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '', lead: sess('L'), members: [], max_members: 3, in_use: 1, ...over })
const page = (): UnattendedView => ({ on: true, since: 1, changed_at: 1, approved: [], truncated: false, quotas: [{ session_id: 'L', root_session_id: 'root-L', title: 'T', address: 'mlab/L-xx', is_lead: true, self_left: 4, member_pool_left: 2, rev: 3 }] })

const Block = () => <TeamSettingsBlock hostId={A} teamId={T} name="Payments" />
const Other = () => <div data-testid="other-tab" />
const blockTab: Tab = { ...createTab({ kind: 'execution', executionId: 'exc_1', host: A }), id: 't-block' }
const otherTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-other' }
const all = [blockTab, otherTab]
const flush = () => act(async () => { for (let i = 0; i < 5; i++) await Promise.resolve() })

beforeEach(() => {
  cleanup()
  resetReader()
  resetWriter()
  mockedGet.mockReset()
  mockedGet.mockResolvedValue(page())
  clearModuleRegistry()
  registerModule({ id: 'blk', name: 'blk', panes: [{ kind: 'execution', component: Block }] })
  registerModule({ id: 'other', name: 'other', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [A] })
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({ hosts: { [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [A], activeHostId: A, runtime: {} })
  useTeamRosterStore.getState().reset()
  useTeamRosterStore.getState().apply(A, [team()])
  useRelayQuotaStore.getState().reset()
  useUnattendedStore.getState().reset()
  useUnattendedStore.getState().setQuotaSupport(A, 'yes')
  useUnattendedStore.getState().setMaxMembersSupport(A, 'yes')
})
afterEach(() => { resetReader(); resetWriter(); cleanup() })

describe('the settings block across a tab switch', () => {
  it('shows the same on return, and follows a cap the roster moved while the reader was away', async () => {
    const r = render(<TabContent activeTab={blockTab} allTabs={all} />)
    await flush()
    expect(within(screen.getByTestId('team-settings-block')).getByTestId('cap-value')).toHaveTextContent('3')
    expect(within(screen.getByTestId('team-settings-quota')).getAllByTestId('quota-value').map((v) => v.textContent)).toEqual(['4', '2'])

    r.rerender(<TabContent activeTab={otherTab} allTabs={all} />)
    expect(screen.queryByTestId('team-settings-block')).toBeNull() // really unmounted
    act(() => { useTeamRosterStore.getState().apply(A, [team({ max_members: 6 })]) })

    r.rerender(<TabContent activeTab={blockTab} allTabs={all} />)
    await flush()
    expect(within(screen.getByTestId('team-settings-block')).getByTestId('cap-value')).toHaveTextContent('6')
    expect(within(screen.getByTestId('team-settings-quota')).getAllByTestId('quota-value').map((v) => v.textContent)).toEqual(['4', '2'])
  })

  it('a read still out when the reader leaves fills the store: the block has the rows on return, without a second request', async () => {
    let resolve!: (v: UnattendedView) => void
    mockedGet.mockImplementation(() => new Promise<UnattendedView>((res) => { resolve = res }))
    const r = render(<TabContent activeTab={blockTab} allTabs={all} />)
    expect(screen.getByTestId('team-settings-quota-loading')).toBeInTheDocument()
    r.rerender(<TabContent activeTab={otherTab} allTabs={all} />)
    await act(async () => { resolve(page()) })
    r.rerender(<TabContent activeTab={blockTab} allTabs={all} />)
    await flush()
    expect(within(screen.getByTestId('team-settings-quota')).getAllByTestId('quota-value').map((v) => v.textContent)).toEqual(['4', '2'])
    expect(mockedGet.mock.calls.length).toBeLessThanOrEqual(2)
  })
})
