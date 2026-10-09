import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import * as devicesApi from '../../lib/devices-api'
import type { DeviceRow } from '../../lib/devices-api'
import * as retry from '../../lib/pending-revocation-retry'
import { useHostStore } from '../../stores/useHostStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { usePendingRevocationsStore } from '../../stores/usePendingRevocationsStore'
import { PairedPhonesSection } from './PairedPhonesSection'

const FAR = Date.now() + 3_600_000
let n = 0
function row(over: Partial<DeviceRow>): DeviceRow {
  n++
  return {
    id: `d${n}`, pairing_id: 'P1', profile_id: '', label: 'Wake iPhone', created_at: 100, created_by: 'admin',
    use_by: FAR, first_used_at: 0, last_used_at: 0, revoked_at: 0, ...over,
  }
}

let list: ReturnType<typeof vi.spyOn>
let revoke: ReturnType<typeof vi.spyOn>
const byHost: Record<string, devicesApi.ListResult> = {}

beforeEach(() => {
  cleanup()
  localStorage.clear()
  for (const k of Object.keys(byHost)) delete byHost[k]
  usePendingRevocationsStore.setState({ items: [] })
  useHostLookStore.setState({ looks: {} })
  useHostStore.setState({
    hosts: {
      a: { id: 'a', name: 'mlab', ip: '1.1.1.1', port: 1, order: 0, token: 'ta' },
      b: { id: 'b', name: 'air26', ip: '1.1.1.2', port: 1, order: 1, token: 'tb' },
      c: { id: 'c', name: 'offline', ip: '1.1.1.3', port: 1, order: 2, token: 'tc' },
      d: { id: 'd', name: 'noauth', ip: '1.1.1.4', port: 1, order: 3 },
    },
    hostOrder: ['a', 'b', 'c', 'd'],
    runtime: { a: { status: 'connected' }, b: { status: 'connected' }, c: { status: 'disconnected' }, d: { status: 'connected' } },
    activeHostId: 'a',
  })
  list = vi.spyOn(devicesApi, 'listDevices').mockImplementation(async (h: string) => byHost[h] ?? { kind: 'ok', rows: [] })
  revoke = vi.spyOn(devicesApi, 'revokePairing').mockResolvedValue({ kind: 'ok' })
  vi.spyOn(retry, 'retryPendingRevocations').mockResolvedValue(undefined)
  vi.spyOn(window, 'confirm').mockReturnValue(true)
})
afterEach(() => {
  vi.restoreAllMocks()
})

function connect(id: string) {
  useHostStore.setState((s) => ({ runtime: { ...s.runtime, [id]: { status: 'connected' } } }))
}

function ok(rows: DeviceRow[]): devicesApi.ListResult {
  return { kind: 'ok', rows }
}

describe('PairedPhonesSection', () => {
  it('lists only connected hosts that have a token, in parallel', async () => {
    render(<PairedPhonesSection />)
    await waitFor(() => expect(list).toHaveBeenCalledTimes(2))
    expect(list.mock.calls.map((c: unknown[]) => c[0]).sort()).toEqual(['a', 'b'])
  })

  it('shows the empty state', async () => {
    connect('c')
    render(<PairedPhonesSection />)
    expect((await screen.findByTestId('paired-empty')).textContent).toBe('No phone is paired yet.')
  })

  it('renders one card per pairing with label, status, times and hosts', async () => {
    byHost.a = ok([row({ id: 'a1', first_used_at: 5_000, last_used_at: 9_000 }), row({ id: 'x', pairing_id: 'P2', label: 'iPad' })])
    byHost.b = ok([row({ id: 'b1', first_used_at: 4_000, last_used_at: 8_000 })])
    render(<PairedPhonesSection />)
    const p1 = await screen.findByTestId('paired-phone-P1')
    expect(within(p1).getByText('Wake iPhone')).toBeTruthy()
    expect(within(p1).getByTestId('paired-state').textContent).toBe('Paired')
    expect(within(p1).getByTestId('paired-first').textContent).toMatch(/First used/)
    expect(within(p1).getByTestId('paired-last').textContent).toMatch(/Last used/)
    expect(within(p1).getByTestId('paired-hosts').textContent).toMatch(/mlab/)
    expect(within(p1).getByTestId('paired-hosts').textContent).toMatch(/air26/)
    const p2 = screen.getByTestId('paired-phone-P2')
    expect(within(p2).getByTestId('paired-state').textContent).toBe('Waiting to pair')
    expect(within(p2).queryByTestId('paired-first')).toBeNull()
  })

  it('an unused pairing past use_by says it is unused and expired', async () => {
    byHost.a = ok([row({ use_by: 1 })])
    render(<PairedPhonesSection />)
    expect((await screen.findByTestId('paired-state')).textContent).toBe('Not used, expired')
  })

  it('a host that cannot be listed is counted, and the others still show', async () => {
    byHost.a = ok([row({})])
    byHost.b = { kind: 'failed', reason: 'network', status: 0 }
    render(<PairedPhonesSection />)
    await screen.findByTestId('paired-phone-P1')
    expect(screen.getByTestId('paired-unreachable').textContent).toBe('1 host(s) could not be reached')
  })

  it('a daemon without devices.v1 is not counted as unreachable', async () => {
    byHost.a = ok([row({})])
    byHost.b = { kind: 'failed', reason: 'unsupported', status: 404 }
    render(<PairedPhonesSection />)
    await screen.findByTestId('paired-phone-P1')
    expect(screen.queryByTestId('paired-unreachable')).toBeNull()
  })

  it('Revoke asks first; cancelling sends nothing', async () => {
    byHost.a = ok([row({})])
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    render(<PairedPhonesSection />)
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }))
    expect(revoke).not.toHaveBeenCalled()
  })

  it('Revoke calls every host that has rows of the pairing, then the card goes away', async () => {
    connect('c')
    byHost.a = ok([row({ id: 'a1' })])
    byHost.b = ok([row({ id: 'b1' })])
    render(<PairedPhonesSection />)
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(screen.queryByTestId('paired-phone-P1')).toBeNull())
    expect(revoke.mock.calls.map((c: unknown[]) => [c[0], c[1]]).sort()).toEqual([['a', 'P1'], ['b', 'P1'], ['c', 'P1']])
    expect(usePendingRevocationsStore.getState().items).toEqual([])
  })

  it('a host that cannot be reached for the revoke is recorded as pending and the row says so', async () => {
    byHost.a = ok([row({ id: 'a1' })])
    byHost.b = ok([row({ id: 'b1' })])
    connect('c')
    revoke.mockImplementation(async (h: string) => (h === 'b' ? { kind: 'failed', reason: 'network', status: 0 } : { kind: 'ok' }))
    render(<PairedPhonesSection />)
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(usePendingRevocationsStore.getState().has('b', 'P1')).toBe(true))
    expect(usePendingRevocationsStore.getState().has('a', 'P1')).toBe(false)
    const card = await screen.findByTestId('paired-phone-P1')
    expect(within(card).getByTestId('paired-pending').textContent).toBe('Not yet revoked on air26')
    expect(within(card).getByTestId('paired-hosts').textContent).not.toMatch(/mlab/)
  })

  it('treats unsupported as done', async () => {
    connect('c')
    byHost.a = ok([row({})])
    revoke.mockResolvedValue({ kind: 'unsupported' })
    render(<PairedPhonesSection />)
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(screen.queryByTestId('paired-phone-P1')).toBeNull())
    expect(usePendingRevocationsStore.getState().items).toEqual([])
  })

  it('B goes offline after listing: A is revoked, B is recorded pending without a request, the row stays; the retry on connect completes it', async () => {
    byHost.a = ok([row({ id: 'a1' })])
    byHost.b = ok([row({ id: 'b1' })])
    connect('c')
    render(<PairedPhonesSection />)
    const btn = await screen.findByRole('button', { name: 'Revoke' })
    useHostStore.setState((s) => ({ runtime: { ...s.runtime, b: { status: 'disconnected' } } }))
    fireEvent.click(btn)
    await waitFor(() => expect(usePendingRevocationsStore.getState().has('b', 'P1')).toBe(true))
    expect(revoke.mock.calls.map((c: unknown[]) => c[0]).sort()).toEqual(['a', 'c'])
    const card = await screen.findByTestId('paired-phone-P1')
    expect(within(card).getByTestId('paired-pending').textContent).toBe('Not yet revoked on air26')

    // B connects: the real retry runs against the same (mocked) transport and clears the entry.
    vi.mocked(retry.retryPendingRevocations).mockRestore()
    byHost.a = ok([])
    byHost.b = ok([])
    connect('b')
    await retry.retryPendingRevocations('b')
    expect(usePendingRevocationsStore.getState().items).toEqual([])
    expect(revoke).toHaveBeenLastCalledWith('b', 'P1')
    await waitFor(() => expect(screen.queryByTestId('paired-phone-P1')).toBeNull())
  })

  it('a host whose list failed is still targeted, and keeps the row while it is pending', async () => {
    byHost.a = ok([row({ id: 'a1' })])
    byHost.b = { kind: 'failed', reason: 'network', status: 0 }
    revoke.mockImplementation(async (h: string) => (h === 'b' ? { kind: 'failed', reason: 'network', status: 0 } : { kind: 'ok' }))
    render(<PairedPhonesSection />)
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(usePendingRevocationsStore.getState().has('b', 'P1')).toBe(true))
    expect(revoke.mock.calls.map((c: unknown[]) => c[0]).sort()).toEqual(['a', 'b'])
    const card = await screen.findByTestId('paired-phone-P1')
    expect(within(card).getByTestId('paired-pending').textContent).toMatch(/air26/)
  })

  it('hosts without a token are named in the confirm text and on the row', async () => {
    byHost.a = ok([row({})])
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    render(<PairedPhonesSection />)
    const card = await screen.findByTestId('paired-phone-P1')
    expect(within(card).getByTestId('paired-no-token').textContent).toMatch(/noauth/)
    fireEvent.click(within(card).getByRole('button', { name: 'Revoke' }))
    expect(String(confirm.mock.calls[0][0])).toMatch(/noauth/)
    expect(revoke).not.toHaveBeenCalled()
  })

  it('D: with a manageable host offline the empty line is NOT shown; a different line says it cannot be confirmed', async () => {
    render(<PairedPhonesSection />) // c (token) is disconnected
    const line = await screen.findByTestId('paired-unconfirmed')
    expect(line.textContent).toBe('Cannot confirm: 1 host(s) offline or not listable; paired phones may still exist on them')
    expect(screen.queryByTestId('paired-empty')).toBeNull()
  })

  it('D: a host whose list failed also counts toward the cannot-confirm line', async () => {
    connect('c')
    byHost.b = { kind: 'failed', reason: 'network', status: 0 }
    render(<PairedPhonesSection />)
    expect((await screen.findByTestId('paired-unconfirmed')).textContent).toMatch(/1 host/)
    expect(screen.queryByTestId('paired-empty')).toBeNull()
  })

  it('D: every token host listed ok and nothing pending: the definite empty state', async () => {
    connect('c')
    render(<PairedPhonesSection />)
    await screen.findByTestId('paired-empty')
    expect(screen.queryByTestId('paired-unconfirmed')).toBeNull()
  })

  it('C: a pending entry keeps its card after a remount with an empty live list', async () => {
    connect('c')
    usePendingRevocationsStore.getState().add('c', 'P9', { label: 'Wake iPhone' })
    const first = render(<PairedPhonesSection />)
    await screen.findByTestId('paired-phone-P9')
    first.unmount()
    render(<PairedPhonesSection />)
    const card = await screen.findByTestId('paired-phone-P9')
    expect(within(card).getByText('Wake iPhone')).toBeTruthy()
    expect(within(card).getByTestId('paired-pending').textContent).toBe('Not yet revoked on offline')
    expect(screen.queryByTestId('paired-empty')).toBeNull()
    expect(screen.queryByTestId('paired-unconfirmed')).toBeNull()
  })

  it('C: a pending entry and a live row of the same pairing are one card', async () => {
    byHost.a = ok([row({ pairing_id: 'P9' })])
    usePendingRevocationsStore.getState().add('c', 'P9', { label: 'other label' })
    render(<PairedPhonesSection />)
    await screen.findByTestId('paired-phone-P9')
    expect(screen.getAllByTestId('paired-phone-P9')).toHaveLength(1)
  })

  it('B: an entry whose host was deleted stays, says so, and only the confirmed 放棄追蹤 action removes it', async () => {
    usePendingRevocationsStore.getState().add('c', 'P9', { label: 'Wake iPhone' })
    useHostStore.setState((s) => {
      const { c: _c, ...rest } = s.hosts
      return { hosts: rest, hostOrder: s.hostOrder.filter((id) => id !== 'c') }
    })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    render(<PairedPhonesSection />)
    const card = await screen.findByTestId('paired-phone-P9')
    expect(within(card).getByTestId('paired-attention').textContent).toMatch(/Host removed, not yet revoked \(offline\)/)
    const stop = within(card).getByRole('button', { name: 'Stop tracking' })
    fireEvent.click(stop)
    expect(String(confirm.mock.calls[0][0])).toMatch(/offline/)
    expect(usePendingRevocationsStore.getState().has('c', 'P9')).toBe(true)
    confirm.mockReturnValue(true)
    fireEvent.click(stop)
    expect(usePendingRevocationsStore.getState().items).toEqual([])
    await waitFor(() => expect(screen.queryByTestId('paired-phone-P9')).toBeNull())
    expect(revoke).not.toHaveBeenCalled()
  })

  it('A: an entry whose host was re-pointed is shown as needing attention and never sent', async () => {
    connect('c')
    usePendingRevocationsStore.getState().add('c', 'P9', { label: 'Wake iPhone' })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, c: { ...s.hosts.c, ip: '7.7.7.7' } } }))
    render(<PairedPhonesSection />)
    const card = await screen.findByTestId('paired-phone-P9')
    expect(within(card).getByTestId('paired-attention').textContent).toMatch(/address of offline has changed/)
    expect(within(card).getByRole('button', { name: 'Stop tracking' })).toBeTruthy()
    expect(within(card).queryByTestId('paired-pending')).toBeNull()
    expect(revoke).not.toHaveBeenCalled()
  })

  it('legacy entry (no endpoint) is shown as needing attention with the same action', async () => {
    connect('c')
    usePendingRevocationsStore.setState({ items: [{ hostId: 'c', pairingId: 'P8' }] })
    render(<PairedPhonesSection />)
    const card = await screen.findByTestId('paired-phone-P8')
    expect(within(card).getByTestId('paired-attention').textContent).toMatch(/older version/)
    expect(within(card).getByRole('button', { name: 'Stop tracking' })).toBeTruthy()
  })

  it('shows a pending note for a host recorded earlier (e.g. by the pairing dialog)', async () => {
    byHost.a = ok([row({})])
    usePendingRevocationsStore.getState().add('c', 'P1')
    render(<PairedPhonesSection />)
    const card = await screen.findByTestId('paired-phone-P1')
    expect(within(card).getByTestId('paired-pending').textContent).toBe('Not yet revoked on offline')
  })

  it('retries pending revocations when it mounts, and reloads when one gets cleared', async () => {
    byHost.a = ok([row({})])
    usePendingRevocationsStore.getState().add('c', 'P1')
    render(<PairedPhonesSection />)
    await screen.findByTestId('paired-phone-P1')
    expect(retry.retryPendingRevocations).toHaveBeenCalledTimes(1)
    const before = list.mock.calls.length
    usePendingRevocationsStore.getState().remove('c', 'P1')
    await waitFor(() => expect(list.mock.calls.length).toBeGreaterThan(before))
  })

  it('never renders anything token-shaped', async () => {
    byHost.a = ok([row({})])
    const { container } = render(<PairedPhonesSection />)
    await screen.findByTestId('paired-phone-P1')
    expect(container.textContent).not.toMatch(/token/i)
  })
})
