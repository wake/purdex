// spa/src/components/team/TeamSettingsBlock.test.tsx — the team settings block at the team panel's bottom (name pool v2 spec §9.5, journal v5 §3.9, U14): the cap and the lead's
// relay quotas. The quotas are the unattended panel's — the same store and writer, read through the one shared reader — so a change in either place shows in the other at
// once, and showing the block reads the host itself (it never relies on the unattended panel having read it). Real stores, panels and writer; only the HTTP is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { createRef } from 'react'
import { TeamSettingsBlock } from './TeamSettingsBlock'
import { UnattendedPanel } from '../UnattendedPanel'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useRelayQuotaStore } from '../../lib/team/relay-quota'
import { refetchHost, resetWriter } from '../../lib/team/relay-quota-writer'
import { resetReader } from '../../lib/team/unattended-reader'
import { ApprovalApiError } from '../../lib/team/approval-api'
import { getUnattended, putRelayQuota } from '../../lib/team/unattended-api'
import type { RosterSession, TeamRoster } from '../../lib/team/roster'
import type { SessionQuota, UnattendedView } from '../../lib/team/types'

vi.mock('../../lib/team/unattended-api', () => ({ getUnattended: vi.fn(), putRelayQuota: vi.fn() }))
const mockedGet = vi.mocked(getUnattended)
const mockedPut = vi.mocked(putRelayQuota)

const A = 'host-a'
const T = 't-L'
const sess = (id: string): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true })
const team = (over: Partial<TeamRoster> = {}): TeamRoster => ({
  id: T, host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '', lead: sess('L'),
  members: [{ ...sess('M1'), state: 'active', origin: 'spawned', joined_at: 1 }], ...over,
})
const quota = (id: string, over: Partial<SessionQuota> = {}): SessionQuota => ({
  session_id: id, root_session_id: `root-${id}`, title: `T-${id}`, address: `mlab/${id}-xx`, is_lead: false, self_left: 0, member_pool_left: 0, rev: 1, ...over,
})
const leadRow = (over: Partial<SessionQuota> = {}) => quota('L', { is_lead: true, self_left: 4, member_pool_left: 2, rev: 3, ...over })
const page = (over: Partial<UnattendedView> = {}): UnattendedView => ({ on: true, since: 1, changed_at: 1, approved: [], truncated: false, ...over })
const flush = () => act(async () => { for (let i = 0; i < 5; i++) await Promise.resolve() })

/** Every GET held open, in order. */
function held() {
  const calls: { hostId: string; resolve: (v: UnattendedView) => void; reject: (e: unknown) => void }[] = []
  mockedGet.mockImplementation((hostId) => new Promise<UnattendedView>((resolve, reject) => { calls.push({ hostId, resolve, reject }) }))
  return calls
}

function openSettings() {
  return render(<TeamSettingsBlock hostId={A} teamId={T} name="Payments" />)
}
const settings = () => screen.getByTestId('team-settings-block')
const quotaSection = () => within(settings()).getByTestId('team-settings-quota')
/** The numbers the settings' steppers show, in order (自動接力, member). */
const settingsValues = () => within(quotaSection()).queryAllByTestId('quota-value').map((v) => v.textContent)

beforeEach(() => {
  resetReader()
  resetWriter()
  mockedGet.mockReset()
  mockedPut.mockReset()
  mockedPut.mockReturnValue(new Promise(() => {})) // a write stays out: what shows is the desired value
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({ hosts: { [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [A], activeHostId: A, runtime: {} })
  useTeamRosterStore.getState().reset()
  useTeamRosterStore.getState().apply(A, [team()])
  useRelayQuotaStore.getState().reset()
  useUnattendedStore.getState().reset()
  useUnattendedStore.getState().setQuotaSupport(A, 'yes')
})
afterEach(() => { resetReader(); resetWriter(); useHostStore.getState().reset() })

describe('the block', () => {
  it('the lines come in order — the cap, then the relay quotas — under a top border', async () => {
    mockedGet.mockResolvedValue(page({ quotas: [leadRow()] }))
    useUnattendedStore.getState().setMaxMembersSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [team({ max_members: 3, in_use: 1 })])
    openSettings()
    await flush()
    const block = settings()
    expect(Array.from(block.children).map((c) => c.getAttribute('data-testid'))).toEqual(['team-settings-cap', 'team-settings-quota'])
    expect(block.className).toContain('border-t')
    expect(within(block).getByTestId('team-settings-cap')).toHaveTextContent('成員上限')
    expect(within(block).getByTestId('team-settings-in-use')).toHaveTextContent('用了 1')
  })

  it('a team the roster no longer holds has no block (and reads nothing)', () => {
    useTeamRosterStore.getState().apply(A, [])
    openSettings()
    expect(screen.queryByTestId('team-settings-block')).toBeNull()
    expect(mockedGet).not.toHaveBeenCalled()
  })

  it('a host that offers nothing to set has no block', () => {
    useUnattendedStore.getState().setQuotaSupport(A, 'no')
    openSettings()
    expect(screen.queryByTestId('team-settings-block')).toBeNull()
  })
})

describe('「接力額度」', () => {
  it('cold start (the unattended panel never opened): opening reads the host and shows the lead\'s 自動接力 and member pool', async () => {
    mockedGet.mockResolvedValue(page({ quotas: [quota('P'), leadRow(), quota('M1')] }))
    openSettings()
    expect(mockedGet).toHaveBeenCalledTimes(1)
    expect(mockedGet.mock.calls[0][0]).toBe(A)
    expect(mockedGet.mock.calls[0][1]).toBeUndefined() // the first page, as the unattended panel reads it
    await flush()
    expect(within(quotaSection()).getAllByTestId('quota-stepper').map((s) => s.getAttribute('data-field'))).toEqual(['self_left', 'member_pool_left'])
    expect(settingsValues()).toEqual(['4', '2']) // the lead's row, found by the lead's session, not the first row
  })

  it('while the read is out: 「讀取中…」 and no stepper', async () => {
    const calls = held()
    openSettings()
    expect(within(quotaSection()).getByTestId('team-settings-quota-loading')).toHaveTextContent('讀取中…')
    expect(within(quotaSection()).queryByTestId('quota-stepper')).toBeNull()
    await act(async () => { calls[0].resolve(page({ quotas: [leadRow()] })) })
    expect(within(quotaSection()).queryByTestId('team-settings-quota-loading')).toBeNull()
    expect(settingsValues()).toEqual(['4', '2'])
  })

  it('a failed read: the error line with its code, no stepper', async () => {
    mockedGet.mockRejectedValue(new ApprovalApiError(503, 'not_ready'))
    openSettings()
    await flush()
    expect(within(quotaSection()).getByTestId('team-settings-quota-error')).toHaveTextContent('讀不到額度（not_ready）')
    expect(within(quotaSection()).queryByTestId('quota-stepper')).toBeNull()
  })

  it('a quotas array the daemon could not give (null / malformed): the unreadable line, no stepper', async () => {
    mockedGet.mockResolvedValue(page({ quotasFailed: true }))
    openSettings()
    await flush()
    expect(within(quotaSection()).getByTestId('team-settings-quota-error')).toHaveTextContent('讀不到額度')
    expect(within(quotaSection()).queryByTestId('quota-stepper')).toBeNull()
  })

  it('the lead missing from the rows says so', async () => {
    mockedGet.mockResolvedValue(page({ quotas: [quota('P')] }))
    openSettings()
    await flush()
    expect(within(quotaSection()).getByTestId('team-settings-quota-missing')).toBeInTheDocument()
  })

  it('a host without team.relay_quota.v1: no quota line and nothing is read (the cap is still there)', async () => {
    useUnattendedStore.getState().setQuotaSupport(A, 'no')
    useUnattendedStore.getState().setMaxMembersSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [team({ max_members: 3, in_use: 1 })])
    openSettings()
    await flush()
    expect(within(settings()).queryByTestId('team-settings-quota')).toBeNull()
    expect(within(settings()).getByTestId('team-settings-cap')).toBeInTheDocument()
    expect(mockedGet).not.toHaveBeenCalled()
  })

  it('two quick shows make one request (single-flight): the second joins the read the first left out', async () => {
    const calls = held()
    const first = openSettings()
    first.unmount() // gone before the host answered
    openSettings()
    expect(calls).toHaveLength(1)
    await act(async () => { calls[0].resolve(page({ quotas: [leadRow()] })) })
    expect(settingsValues()).toEqual(['4', '2'])
  })

  it('a re-read the writer asks for (after pending_lineage) reaches the open settings: the lead\'s new root', async () => {
    mockedGet.mockResolvedValueOnce(page({ quotas: [leadRow({ root_session_id: 'provisional' })] }))
    openSettings()
    await flush()
    mockedGet.mockResolvedValueOnce(page({ quotas: [leadRow({ root_session_id: 'real-root', self_left: 7, rev: 5 })] }))
    await act(async () => { refetchHost(A) })
    await flush()
    expect(mockedGet).toHaveBeenCalledTimes(2)
    expect(settingsValues()).toEqual(['7', '2'])
  })
})

// One value per chain (U14): the team settings and the unattended panel show the same store, written by the same writer.
describe('one value in both places', () => {
  function openBoth() {
    const anchorRef = createRef<HTMLButtonElement>()
    render(<>
      <button ref={anchorRef} />
      <UnattendedPanel hostIds={[A]} anchorRef={anchorRef} onClose={vi.fn()} />
      <TeamSettingsBlock hostId={A} teamId={T} name="Payments" />
    </>)
  }
  const unattendedLead = () => screen.getAllByTestId('quota-row').find((r) => r.getAttribute('data-session') === 'L')!
  const unattendedValues = () => within(unattendedLead()).getAllByTestId('quota-value').map((v) => v.textContent)

  it('both panels read the host once between them, and a change in the settings shows in the unattended panel at once', async () => {
    mockedGet.mockResolvedValue(page({ quotas: [leadRow(), quota('P')] }))
    openBoth()
    expect(mockedGet).toHaveBeenCalledTimes(1)
    await flush()
    expect(unattendedValues()).toEqual(['4', '2'])
    expect(settingsValues()).toEqual(['4', '2'])
    fireEvent.click(within(quotaSection()).getAllByRole('button', { name: '增加' })[0])
    expect(settingsValues()).toEqual(['5', '2'])
    expect(unattendedValues()).toEqual(['5', '2'])
    fireEvent.click(within(quotaSection()).getAllByRole('button', { name: '減少' })[1])
    expect(unattendedValues()).toEqual(['5', '1'])
  })

  it('and the reverse: a change in the unattended panel shows in the settings at once', async () => {
    mockedGet.mockResolvedValue(page({ quotas: [leadRow(), quota('P')] }))
    openBoth()
    await flush()
    fireEvent.click(within(unattendedLead()).getAllByRole('button', { name: '增加' })[1])
    expect(unattendedValues()).toEqual(['4', '3'])
    expect(settingsValues()).toEqual(['4', '3'])
    // a confirmed number from elsewhere (another window's write) shows in both too
    act(() => { useRelayQuotaStore.getState().applyEvent(A, { op: 'changed', root_session_id: 'root-L', self_left: 9, member_pool_left: 3, rev: 8 }) })
    expect(settingsValues()).toEqual(['9', '3'])
    expect(unattendedValues()).toEqual(['9', '3'])
  })
})
