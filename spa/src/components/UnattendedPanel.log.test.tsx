// spa/src/components/UnattendedPanel.log.test.tsx — the unattended panel's log (name pool v2 spec §8, U16, NA-8b): a host whose daemon sends `log` is shown by it — decisions with
// a status tag, the switch's records in bold on a pink row, newest first across hosts, the newest 30, scrollable; a host without `log` keeps today's list; a mixed set shows both.
// The real panel over the real stores; only the API is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createRef } from 'react'
import { UnattendedPanel } from './UnattendedPanel'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useUnattendedStore } from '../stores/useUnattendedStore'
import { useRelayQuotaStore } from '../lib/team/relay-quota'
import { resetReader } from '../lib/team/unattended-reader'
import { getUnattended, parseUnattendedLog } from '../lib/team/unattended-api'
import type { Approval, Origin, UnattendedLogEntry, UnattendedView } from '../lib/team/types'

vi.mock('../lib/team/unattended-api', async (orig) => ({ ...(await orig<typeof import('../lib/team/unattended-api')>()), getUnattended: vi.fn() }))
const mockedGet = vi.mocked(getUnattended)

const A = 'host-a'
const B = 'host-b'
const at = (h: number, m: number) => new Date(2026, 9, 19, h, m).getTime()
const origin = (id: string, over: Partial<Origin> = {}): Origin => ({ session_id: `S-${id}`, ref: `_${id}`, name: `sess-${id}`, pid: 1, proc_start: 'x', cwd: '/w', tmux: 'p:@1.%1', ...over })
const decision = (id: string, when: number, state: 'approved' | 'denied' | 'timeout', by: 'unattended' | 'app' | null, kind = 'self_relay', o: Partial<Origin> = {}): UnattendedLogEntry => ({
  type: 'decision', at: when, kind, origin: origin(id, o), state, ...(by === null ? {} : { decided_by: { kind: by, label: by === 'app' ? 'Purdex.app' : '無人值守模式' } }),
})
const toggle = (when: number, on: boolean, kind: 'app' | 'person' = 'app'): UnattendedLogEntry => ({ type: 'toggle', at: when, on, by: { kind, label: kind === 'app' ? 'Purdex.app' : '終端機指令' } })
const legacy = (id: string, decidedAt: number): Approval => ({
  id, kind: 'self_relay', host_id: 'd1', origin: origin(id), payload: {}, state: 'approved', created_at: decidedAt - 1_000, deadline_at: decidedAt + 540_000, lease_until: decidedAt + 30_000,
  decided_by: { kind: 'unattended', label: '無人值守模式' }, decided_at: decidedAt,
})
const page = (over: Partial<UnattendedView> = {}): UnattendedView => ({ on: true, since: at(8, 0), changed_at: at(8, 0), approved: [], truncated: false, ...over })

function open(hostIds: string[], unreachableIds: string[] = []) {
  const anchorRef = createRef<HTMLDivElement>()
  return render(<><div ref={anchorRef} /><UnattendedPanel hostIds={hostIds} unreachableIds={unreachableIds} anchorRef={anchorRef} onClose={vi.fn()} /></>)
}
const logRows = () => screen.getAllByTestId(/^unattended-log-(row|toggle)$/)
const tagOf = (row: HTMLElement) => within(row).getByTestId('unattended-log-tag')

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({
    hosts: { [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 }, [B]: { id: B, name: 'air26', ip: '2', port: 2, token: 't', order: 1 } },
    hostOrder: [A, B], activeHostId: A, runtime: {},
  })
  useTeamRosterStore.getState().reset()
  useUnattendedStore.getState().reset()
  useRelayQuotaStore.getState().reset()
  resetReader()
  mockedGet.mockReset()
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(at(12, 0))
})
afterEach(() => { vi.useRealTimers(); useHostStore.getState().reset() })

describe('the log in the unattended panel', () => {
  it('shows the decisions with a status tag, newest first, and the switch\'s records as a bold pink row', async () => {
    mockedGet.mockResolvedValue(page({
      approved: [legacy('x1', at(9, 0))], // an older App\'s page: not drawn when `log` is there
      log: [
        toggle(at(10, 30), true),
        decision('a', at(10, 0), 'approved', 'unattended'),
        decision('b', at(9, 30), 'approved', 'app'),
        decision('c', at(9, 0), 'denied', 'app'),
        decision('d', at(8, 30), 'denied', 'unattended', 'adopt'),
        decision('e', at(8, 15), 'timeout', null),
        toggle(at(8, 0), false, 'person'),
      ],
    }))
    open([A])
    await waitFor(() => expect(logRows()).toHaveLength(7))
    const rows = logRows()
    expect(screen.getByTestId('unattended-log-title')).toHaveTextContent('最近的決定')
    expect(rows[0]).toHaveTextContent('無人值守開啟 · 由你 · 10:30')
    expect(rows[0].className).toContain('font-bold')
    expect(rows[0].className).toContain('pink')
    expect(rows[6]).toHaveTextContent('無人值守關閉 · 由終端機指令 · 08:00')
    expect([1, 2, 3, 4, 5].map((i) => [tagOf(rows[i]).textContent, tagOf(rows[i]).getAttribute('data-tone')])).toEqual([
      ['自動通過', 'green'], ['你核准', 'blue'], ['你拒絕', 'red'], ['自動拒絕', 'red'], ['逾時', 'yellow'],
    ])
    expect(within(rows[1]).getByTestId('unattended-log-name')).toHaveTextContent('sess-a')
    expect(within(rows[1]).getByTestId('unattended-log-kind')).toHaveTextContent('接力申請')
    expect(rows[1]).toHaveTextContent('10:00')
    expect(screen.queryByTestId('unattended-row')).toBeNull() // the auto-approved list is gone
    expect(screen.queryByTestId('unattended-approved-title')).toBeNull()
  })

  it('a lead request carries the team name; a kind the App does not know shows as it came', async () => {
    useTeamRosterStore.getState().apply(A, [{
      id: 't1', host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '', lead: { session_id: 'S-L', ref: '_L', address: 'mlab/l', live: true }, members: [],
    }])
    mockedGet.mockResolvedValue(page({ log: [
      decision('L', at(10, 0), 'approved', 'app', 'lead', { session_id: 'S-L' }),
      decision('q', at(9, 0), 'approved', 'unattended', 'spawn_resources'),
      decision('z', at(8, 0), 'approved', 'unattended', 'future_kind'),
    ] }))
    open([A])
    await waitFor(() => expect(logRows()).toHaveLength(3))
    const rows = logRows()
    expect(within(rows[0]).getByTestId('unattended-log-name')).toHaveTextContent('Payments')
    expect(within(rows[0]).getByTestId('unattended-log-kind')).toHaveTextContent('lead 申請')
    expect(within(rows[1]).getByTestId('unattended-log-kind')).toHaveTextContent('成員資源申請')
    expect(within(rows[2]).getByTestId('unattended-log-kind')).toHaveTextContent('future_kind')
  })

  it('merges two hosts newest first and names the host on each decision', async () => {
    mockedGet.mockImplementation(async (hostId) => page({ log: hostId === A
      ? [decision('a2', at(9, 30), 'approved', 'unattended'), decision('a1', at(7, 5), 'approved', 'unattended')]
      : [decision('b1', at(8, 45), 'denied', 'app')] }))
    open([A, B])
    await waitFor(() => expect(logRows()).toHaveLength(3))
    expect(logRows().map((r) => within(r).getByTestId('unattended-log-name').textContent)).toEqual(['sess-a2', 'sess-b1', 'sess-a1'])
    expect(within(logRows()[1]).getByTestId('unattended-log-host')).toHaveTextContent('air26')
  })

  it('names the host on a switch record too, and on every row while several hosts are shown (even when the newest 30 are all one host\'s)', async () => {
    mockedGet.mockImplementation(async (hostId) => hostId === A
      ? page({ log: [toggle(at(10, 0), true), decision('a', at(9, 0), 'approved', 'unattended')] })
      : page({ log: [] }))
    open([A, B])
    await waitFor(() => expect(logRows()).toHaveLength(2))
    expect(within(logRows()[0]).getByTestId('unattended-log-host')).toHaveTextContent('mlab')
    expect(within(logRows()[1]).getByTestId('unattended-log-host')).toHaveTextContent('mlab')
  })

  it('names the host while another host is shown but unreachable, or has not answered yet', async () => {
    mockedGet.mockImplementation(async (hostId) => hostId === A ? page({ log: [decision('a', at(9, 0), 'approved', 'unattended')] }) : new Promise<UnattendedView>(() => {}))
    open([A, B], [])
    await waitFor(() => expect(logRows()).toHaveLength(1))
    expect(within(logRows()[0]).getByTestId('unattended-log-host')).toHaveTextContent('mlab') // B has not answered, and is still part of what the panel shows
    cleanup()
    mockedGet.mockReset()
    mockedGet.mockResolvedValue(page({ log: [decision('a', at(9, 0), 'approved', 'unattended')] }))
    open([A], [B])
    await waitFor(() => expect(logRows()).toHaveLength(1))
    expect(within(logRows()[0]).getByTestId('unattended-log-host')).toHaveTextContent('mlab')
  })

  it('a single host\'s log names no host', async () => {
    mockedGet.mockResolvedValue(page({ log: [toggle(at(10, 0), true), decision('a', at(9, 0), 'approved', 'unattended')] }))
    open([A])
    await waitFor(() => expect(logRows()).toHaveLength(2))
    expect(screen.queryByTestId('unattended-log-host')).toBeNull()
  })

  it('keeps the newest 30 of everything the hosts sent, in a list that scrolls', async () => {
    const many = (prefix: string, n: number, base: number) => Array.from({ length: n }, (_, i) => decision(`${prefix}${i}`, base - i * 60_000, 'approved', 'unattended'))
    mockedGet.mockImplementation(async (hostId) => page({ log: hostId === A ? many('a', 30, at(11, 59)) : many('b', 30, at(11, 29)) }))
    open([A, B])
    await waitFor(() => expect(logRows()).toHaveLength(30))
    expect(logRows().every((r) => within(r).getByTestId('unattended-log-name').textContent!.startsWith('sess-a'))).toBe(true) // all of A\'s are newer than B\'s
    expect(screen.getByTestId('unattended-log-list').className).toContain('overflow-y-auto')
  })

  it('says so when the log is empty', async () => {
    mockedGet.mockResolvedValue(page({ log: [] }))
    open([A])
    expect(await screen.findByTestId('unattended-log-empty')).toHaveTextContent('還沒有任何決定')
  })

  it('a host without `log` keeps today\'s list; a mixed set shows both', async () => {
    mockedGet.mockImplementation(async (hostId) => hostId === A
      ? page({ log: [decision('a', at(10, 0), 'approved', 'unattended')] })
      : page({ approved: [legacy('b1', at(9, 0))] }))
    open([A, B])
    await waitFor(() => expect(screen.getAllByTestId('unattended-row')).toHaveLength(1))
    expect(screen.getAllByTestId('unattended-row')[0]).toHaveTextContent('air26：sess-b1 · 接力申請 · 09:00')
    expect(logRows()).toHaveLength(1)
    expect(screen.getByTestId('unattended-approved-title')).toBeInTheDocument()
  })

  it('a log host that cannot be read is still named, without a list title above it', async () => {
    mockedGet.mockImplementation(async (hostId) => {
      if (hostId === B) throw new Error('boom')
      return page({ log: [decision('a', at(10, 0), 'approved', 'unattended')] })
    })
    open([A, B])
    await waitFor(() => expect(screen.getByTestId('unattended-host-failed')).toBeInTheDocument())
    expect(screen.queryByTestId('unattended-approved-title')).toBeNull()
    expect(logRows()).toHaveLength(1)
  })
})

describe('a decision without a decider', () => {
  it('approved or denied with no decided_by is shown plain, on neither the person nor the daemon; a timeout is not that', async () => {
    mockedGet.mockResolvedValue(page({ log: [
      decision('a', at(10, 0), 'approved', null),
      decision('b', at(9, 0), 'denied', null),
      decision('c', at(8, 0), 'timeout', null),
    ] }))
    open([A])
    await waitFor(() => expect(logRows()).toHaveLength(3))
    expect([0, 1, 2].map((i) => [tagOf(logRows()[i]).textContent, tagOf(logRows()[i]).getAttribute('data-tone')])).toEqual([['已核准', 'muted'], ['已拒絕', 'muted'], ['逾時', 'yellow']])
  })
})

describe('「顯示更多」 with a log host and a list host', () => {
  it('asks only the list host for its next page', async () => {
    mockedGet.mockImplementation(async (hostId, q) => {
      if (hostId === A) return page({ log: [decision('a', at(10, 0), 'approved', 'unattended')], truncated: true, next_before: 5 })
      return page({ approved: [legacy('b1', at(9, 0))], truncated: q?.before === undefined, ...(q?.before === undefined ? { next_before: 7 } : {}) })
    })
    open([A, B])
    const more = await screen.findByTestId('unattended-more')
    mockedGet.mockClear()
    fireEvent.click(more)
    await waitFor(() => expect(mockedGet).toHaveBeenCalled())
    expect(mockedGet.mock.calls.map(([h, q]) => [h, q?.before])).toEqual([[B, 7]])
  })
})

describe('parseUnattendedLog', () => {
  it('is undefined when `log` is not an array (an older daemon)', () => {
    for (const v of [undefined, null, {}, 'x']) expect(parseUnattendedLog(v)).toBeUndefined()
  })

  it('leaves out a broken entry, a later daemon\'s type and a state that is not a decision, and keeps the rest in order', () => {
    const good = decision('a', 5, 'approved', 'unattended')
    expect(parseUnattendedLog([
      toggle(9, true), { type: 'toggle', at: 8, on: 'yes', by: { kind: 'app', label: 'x' } }, { type: 'later', at: 7 },
      { ...good, state: 'cancelled' }, { ...good, origin: undefined }, { ...good, at: 'soon' }, good,
    ])).toEqual([toggle(9, true), good])
  })

  it('a timeout has no decided_by, and that is not a defect', () => {
    const e = decision('t', 3, 'timeout', null)
    expect(parseUnattendedLog([e])).toEqual([e])
  })
})
