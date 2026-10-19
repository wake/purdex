// XR-3: folders on other paired hosts in the team roots editor — the real section over the real stores; only the transport is faked.
import { describe, it, expect, vi, beforeEach, afterEach, type Mock } from 'vitest'
import { act } from 'react'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { TeamRootsEditor } from './TeamRootsEditor'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useRelayQuotaStore } from '../../lib/team/relay-quota'
import { useMaxMembersStore } from '../../lib/team/max-members'
import { ApprovalApiError } from '../../lib/team/approval-api'
import { configureRoots, resetRoots } from '../../lib/team/roots'
import { configureHostRoots, resetHostRoots, type HostRootsDeps, type PairedHostRoots, type HostRootChange } from '../../lib/team/host-roots'
import type { TeamRoster } from '../../lib/team/roster'

const H = 'host-a'
const lead = (id: string) => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}`, live: true })
const team = (id: string, leadId: string): TeamRoster => ({ id, host_id: 'd', created_at: 1, team_name: '', team_label: '', lead: lead(leadId), members: [], roots: ['/Users/wake/a'], roots_rev: 3 })
/** What the team panel's folders line mounts when it opens: the editor of each led team, read from the roster store. */
function Editors({ leads }: { leads: string[] }) {
  const teams = useTeamRosterStore((s) => s.byHost[H]) ?? []
  return <>{teams.filter((t) => leads.includes(t.lead.session_id)).map((t) => <TeamRootsEditor key={t.id} hostId={H} team={t} />)}</>
}
const show = (ids: string[] = ['L']) => render(<Editors leads={ids} />)
const host = (over: Partial<PairedHostRoots> = {}): PairedHostRoots => ({ hostId: 'h-air', alias: 'air26', status: 'ok', roots: [], changes: [], ...over })
const ch = (over: Partial<HostRootChange> = {}): HostRootChange => ({ id: 'c1', op: 'add', path: '/x', state: 'pending', code: '', detail: '', ...over })
const type = (v: string) => fireEvent.change(screen.getByTestId('roots-input'), { target: { value: v } })
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve() })

let hosts: PairedHostRoots[] | 'unsupported'
let fetch: Mock<HostRootsDeps['fetch']>
let post: Mock<HostRootsDeps['post']>
let dismiss: Mock<HostRootsDeps['dismiss']>

beforeEach(() => {
  cleanup()
  resetRoots()
  resetHostRoots()
  hosts = [host()]
  fetch = vi.fn<HostRootsDeps['fetch']>(async () => hosts)
  post = vi.fn<HostRootsDeps['post']>(async () => {})
  dismiss = vi.fn<HostRootsDeps['dismiss']>(async () => {})
  configureRoots({ identity: () => 'id-1' })
  configureHostRoots({ identity: () => 'id-1', fetch, post, dismiss })
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({ hosts: { [H]: { id: H, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [H], activeHostId: H, runtime: {} })
  useTeamRosterStore.getState().reset()
  useTeamRosterStore.getState().apply(H, [team('t-L', 'L')])
  useRelayQuotaStore.getState().reset()
  useUnattendedStore.getState().reset()
  useMaxMembersStore.getState().reset()
})
afterEach(() => { vi.useRealTimers() })

describe('other hosts section', () => {
  it('reads on mount; one section per paired host, saying the grant is the whole lead host\'s', async () => {
    hosts = [host({ roots: ['/Users/wake/Workspace/aigora-ios'] }), host({ hostId: 'h-b', alias: 'mini' })]
    show()
    await flush()
    expect(fetch).toHaveBeenCalledTimes(1)
    const sections = screen.getAllByTestId('host-roots-host')
    expect(sections.map((s) => s.dataset.alias)).toEqual(['air26', 'mini'])
    expect(sections[0].textContent).toContain('air26 的授權整台 mlab 共用（所有 team）')
    expect(within(sections[0]).getByTestId('host-roots-path').textContent).toBe('/Users/wake/Workspace/aigora-ios')
    expect(within(sections[0]).getByTestId('host-roots-remove')).toBeTruthy()
  })
  it('a 404 lead (older daemon) hides the section and does not route alias:/…', async () => {
    hosts = 'unsupported'
    show()
    await flush()
    expect(screen.queryByTestId('host-roots')).toBeNull()
    type('air26:/x')
    await act(async () => { fireEvent.click(screen.getByTestId('roots-add')) })
    expect(post).not.toHaveBeenCalled()
    expect(screen.getByTestId('roots-input-error').textContent).toContain('絕對路徑') // the local rule, as before
  })

  it.each([
    ['offline', 'wait_online', '等 air26 上線'],
    ['unknown', 'wait_online', '等 air26 上線'],
    ['unsupported', 'needs_update', 'air26 需要更新'],
    ['unauthorized', 'pairing_lost', 'air26 的配對失效'],
    ['ok', 'applying', '套用中'],
  ] as const)('a pending add on a %s host reads 「%s」', async (status, _k, text) => {
    hosts = [host({ status, changes: [ch()] })]
    show()
    await flush()
    const r = screen.getByTestId('host-roots-row')
    expect(r.dataset.kind).toBe('adding')
    expect(within(r).getByTestId('host-roots-status').textContent).toBe(text)
    expect(within(r).getByTestId('host-roots-remove')).toBeTruthy()
  })
  it('a pending remove marks the root 「移除中・…」 with no X', async () => {
    hosts = [host({ status: 'offline', roots: ['/x'], changes: [ch({ op: 'remove' })] })]
    show()
    await flush()
    const r = screen.getByTestId('host-roots-row')
    expect(r.dataset.kind).toBe('removing')
    expect(within(r).getByTestId('host-roots-status').textContent).toBe('移除中・等 air26 上線')
    expect(within(r).queryByTestId('host-roots-remove')).toBeNull()
  })
  it('refused: 「失敗：detail」 (else the code) and 清除; dropped: 已取消 and 清除', async () => {
    hosts = [host({ changes: [ch({ id: 'r1', state: 'refused', code: 'bad_root', detail: 'not a directory' }), ch({ id: 'r2', path: '/y', state: 'refused', code: 'roots_in_use' }), ch({ id: 'd1', path: '/z', state: 'dropped' })] })]
    show()
    await flush()
    const rows = screen.getAllByTestId('host-roots-row')
    expect(within(rows[0]).getByTestId('host-roots-status').textContent).toBe('失敗：not a directory')
    expect(within(rows[1]).getByTestId('host-roots-status').textContent).toBe('失敗：roots_in_use')
    expect(within(rows[2]).getByTestId('host-roots-status').textContent).toBe('已取消')
    await act(async () => { fireEvent.click(within(rows[2]).getByTestId('host-roots-dismiss')) })
    expect(dismiss).toHaveBeenCalledWith(H, 'd1')
    expect(fetch.mock.calls.length).toBeGreaterThanOrEqual(2) // re-read after the dismiss
  })
  it('consent_off adds the line under the heading', async () => {
    hosts = [host({ status: 'consent_off' })]
    show()
    await flush()
    expect(screen.getByTestId('host-roots-consent-off').textContent).toBe('air26 沒有允許 mlab 開 member')
  })
  it('a long path is mid-ellipsized, the tooltip has all of it', async () => {
    const long = `/Users/wake/${'deep/'.repeat(30)}end`
    hosts = [host({ roots: [long] })]
    show()
    await flush()
    const p = screen.getByTestId('host-roots-path')
    expect(p.textContent).toContain('…')
    expect(p).toHaveAttribute('title', long)
  })
})

describe('the input hint', () => {
  it('with paired hosts read, the placeholder names the first alias', async () => {
    hosts = [host(), host({ hostId: 'h-b', alias: 'mini' })]
    show()
    await flush()
    expect(screen.getByTestId('roots-input')).toHaveAttribute('placeholder', '新增絕對路徑，或 air26:/路徑 授權其他主機')
  })
  it('a 404 lead, or no paired host, keeps the original hint', async () => {
    hosts = 'unsupported'
    show()
    await flush()
    expect(screen.getByTestId('roots-input')).toHaveAttribute('placeholder', '新增絕對路徑')
    cleanup(); resetHostRoots(); configureHostRoots({ identity: () => 'id-1', fetch, post, dismiss })
    hosts = []
    show()
    await flush()
    expect(screen.getByTestId('roots-input')).toHaveAttribute('placeholder', '新增絕對路徑')
  })
})

describe('routing the typed line', () => {
  it('air26:/x (and AIR26:/x) posts a grant, clears the box, and re-reads', async () => {
    show()
    await flush()
    type('AIR26:/Users/wake/x')
    await act(async () => { fireEvent.click(screen.getByTestId('roots-add')) })
    expect(post).toHaveBeenCalledWith(H, 'air26', 'add', '/Users/wake/x')
    expect((screen.getByTestId('roots-input') as HTMLInputElement).value).toBe('')
    expect(fetch).toHaveBeenCalledTimes(2)
  })
  it('/x stays the team\'s own roots (no grant call)', async () => {
    const put = vi.fn(async () => ({ team_id: 't-L', roots: ['/Users/wake/a', '/x'], roots_rev: 4 }))
    configureRoots({ put })
    show()
    await flush()
    type('/x')
    await act(async () => { fireEvent.click(screen.getByTestId('roots-add')) })
    expect(put).toHaveBeenCalled()
    expect(post).not.toHaveBeenCalled()
  })
  it('foo:/x says there is no such paired host, not "not absolute"', async () => {
    show()
    await flush()
    type('foo:/x')
    await act(async () => { fireEvent.click(screen.getByTestId('roots-add')) })
    expect(screen.getByTestId('roots-input-error').textContent).toBe('沒有叫 foo 的已配對主機')
    expect(post).not.toHaveBeenCalled()
  })
  it('air26:x is refused as not absolute', async () => {
    show()
    await flush()
    type('air26:x')
    await act(async () => { fireEvent.click(screen.getByTestId('roots-add')) })
    expect(screen.getByTestId('roots-input-error').textContent).toContain('絕對路徑')
    expect(post).not.toHaveBeenCalled()
  })
  it('a 400 bad_root shows its detail beside the box and keeps the text', async () => {
    post.mockRejectedValue(new ApprovalApiError(400, 'bad_root', 'not_absolute'))
    show()
    await flush()
    type('air26:/nope')
    await act(async () => { fireEvent.click(screen.getByTestId('roots-add')) })
    expect(screen.getByTestId('roots-input-error').textContent).toContain('not_absolute')
    expect((screen.getByTestId('roots-input') as HTMLInputElement).value).toBe('air26:/nope')
  })
})

describe('removing', () => {
  it('X on an applied root posts a remove; X on a pending add posts a remove of that path', async () => {
    hosts = [host({ roots: ['/applied'], changes: [ch({ path: '/pending' })] })]
    show()
    await flush()
    const rows = screen.getAllByTestId('host-roots-row')
    await act(async () => { fireEvent.click(within(rows[0]).getByTestId('host-roots-remove')) })
    expect(post).toHaveBeenLastCalledWith(H, 'air26', 'remove', '/applied')
    await act(async () => { fireEvent.click(within(screen.getAllByTestId('host-roots-row')[1]).getByTestId('host-roots-remove')) })
    expect(post).toHaveBeenLastCalledWith(H, 'air26', 'remove', '/pending')
  })
  it('409 roots_in_use shows who on THAT row: title（ref）, ref, op id', async () => {
    hosts = [host({ roots: ['/a', '/b'] })]
    post.mockRejectedValue(new ApprovalApiError(409, 'roots_in_use', '', null, { error: 'roots_in_use', uses: [{ title: 'worker', ref: '_r1', cwd: '/b' }, { ref: '_r2', cwd: '/b' }, { op_id: '0123456789abcdef', cwd: '/b' }] }))
    show()
    await flush()
    await act(async () => { fireEvent.click(within(screen.getAllByTestId('host-roots-row')[1]).getByTestId('host-roots-remove')) })
    const rows = screen.getAllByTestId('host-roots-row')
    expect(within(rows[0]).queryByTestId('host-roots-row-error')).toBeNull()
    expect(within(rows[1]).getByTestId('host-roots-row-error').textContent).toBe('這個資料夾還有 member 在用：worker（_r1）、_r2、01234567。先結束它們才能移除。')
    // the next action clears it
    post.mockResolvedValue(undefined)
    await act(async () => { fireEvent.click(within(screen.getAllByTestId('host-roots-row')[0]).getByTestId('host-roots-remove')) })
    expect(screen.queryByTestId('host-roots-row-error')).toBeNull()
  })
})

describe('polling', () => {
  it('re-reads every 10 s only while a change is pending and the editor is shown', async () => {
    vi.useFakeTimers()
    hosts = [host({ changes: [ch()] })]
    const { unmount } = show()
    await flush()
    expect(fetch).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(fetch).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(fetch).toHaveBeenCalledTimes(3)
    // the change lands: polling stops
    hosts = [host({ roots: ['/x'] })]
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    const n = fetch.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(fetch).toHaveBeenCalledTimes(n)
    unmount()
  })
  it('no pending: no polling at all', async () => {
    vi.useFakeTimers()
    show()
    await flush()
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(fetch).toHaveBeenCalledTimes(1)
  })
  it('collapsing the editor stops the polling', async () => {
    vi.useFakeTimers()
    hosts = [host({ changes: [ch()] })]
    const { unmount } = show()
    await flush()
    unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(fetch).toHaveBeenCalledTimes(1)
  })
  it('a slow read is not doubled up by the next tick', async () => {
    vi.useFakeTimers()
    hosts = [host({ changes: [ch()] })]
    let release!: () => void
    fetch.mockImplementation(() => new Promise((r) => { release = () => r(hosts) }))
    show()
    await flush()
    await act(async () => { await vi.advanceTimersByTimeAsync(35_000) })
    expect(fetch).toHaveBeenCalledTimes(1)
    release()
  })
})

describe('two teams on one lead host', () => {
  it('one poll timer for both editors, however they were mounted', async () => {
    vi.useFakeTimers()
    useTeamRosterStore.getState().apply(H, [team('t-L', 'L'), team('t-M', 'M')])
    hosts = [host({ changes: [ch()] })]
    show(['L', 'M'])
    await flush()
    expect(fetch).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(fetch).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(fetch).toHaveBeenCalledTimes(3)
  })
  it('collapsing one of two editors keeps the poll; collapsing both stops it', async () => {
    vi.useFakeTimers()
    useTeamRosterStore.getState().apply(H, [team('t-L', 'L'), team('t-M', 'M')])
    hosts = [host({ changes: [ch()] })]
    const { rerender, unmount } = show(['L', 'M'])
    await flush()
    rerender(<Editors leads={['L']} />)
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(fetch).toHaveBeenCalledTimes(2)
    unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(fetch).toHaveBeenCalledTimes(2)
  })
  it('both editors show the same grants from one read', async () => {
    useTeamRosterStore.getState().apply(H, [team('t-L', 'L'), team('t-M', 'M')])
    hosts = [host({ roots: ['/shared'] })]
    show(['L', 'M'])
    await flush()
    const sections = screen.getAllByTestId('host-roots')
    expect(sections).toHaveLength(2)
    for (const s of sections) expect(within(s).getByTestId('host-roots-path').textContent).toBe('/shared')
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})
