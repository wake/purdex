// F-2a2: the Hosts page's probe-host section (written to BOTH daemons) and the `fleet` row editor.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'

vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 38,
    getVirtualItems: () => Array.from({ length: count }, (_, i) => ({ index: i, key: i, start: i * 38, size: 38 })),
  }),
}))
vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0L10,10', isWeightLoaded: () => true, prefetchWeight: () => Promise.resolve(),
}))

const fetchFleetConfig = vi.fn()
const fetchSshAliases = vi.fn()
const putProbeHosts = vi.fn()
const putFleetRow = vi.fn()
vi.mock('../../lib/fleet/config-api', () => ({
  fetchFleetConfig: (...a: unknown[]) => fetchFleetConfig(...a),
  fetchSshAliases: (...a: unknown[]) => fetchSshAliases(...a),
  putProbeHosts: (...a: unknown[]) => putProbeHosts(...a),
  putFleetRow: (...a: unknown[]) => putFleetRow(...a),
}))

import { ProbeHostsSection } from './ProbeHostsSection'
import { FleetRowEditor } from './FleetRowEditor'
import { useFleetConfigStore } from '../../lib/fleet/config-store'
import { useFleetStore } from '../../lib/fleet/store'
import { useHostStore } from '../../stores/useHostStore'
import { HostConfigApiError, HostConfigConflictError } from '../../lib/host-config-api'
import type { FleetConfigRead, ProbeRow } from '../../lib/fleet/config-api'

const A = 'ha'
const B = 'hb'
const nas = (alias: string): ProbeRow => ({ id: 'aaaaaaaa', name: 'nas', ssh_alias: alias, icon: '', icon_weight: '' })
const cfg = (items: ProbeRow[], revision: number, dropped: FleetConfigRead['probe']['dropped'] = null): FleetConfigRead => ({
  probe: { items, revision, dropped, invalid: false },
  fleet: { items: { order: ['air26', 'mlab'], peer_ssh_alias: 'mlab', enabled: true }, revision: 5, dropped: null, invalid: false },
})
let copies: Record<string, FleetConfigRead>

beforeEach(() => {
  for (const f of [fetchFleetConfig, fetchSshAliases, putProbeHosts, putFleetRow]) f.mockReset()
  copies = { [A]: cfg([nas('nas-a')], 3), [B]: cfg([nas('nas-b')], 8) }
  fetchFleetConfig.mockImplementation(async (id: string) => copies[id])
  fetchSshAliases.mockImplementation(async (id: string) => (id === A ? ['nas-a', 'a-other'] : ['nas-b', 'b-other']))
  putProbeHosts.mockResolvedValue(undefined)
  putFleetRow.mockResolvedValue(undefined)
  useFleetConfigStore.getState().reset()
  const host = (id: string) => ({ id, name: id, ip: '127.0.0.1', port: 7860, token: 't' })
  useHostStore.setState({
    hosts: { [A]: host(A), [B]: host(B) } as never,
    hostOrder: [A, B],
    runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } } as never,
  })
  useFleetStore.setState({
    byHost: { [A]: { gen: 1, support: 'yes', term: 0 }, [B]: { gen: 1, support: 'yes', term: 0 } } as never,
  })
})
afterEach(() => vi.useRealTimers())

async function mounted() {
  render(<ProbeHostsSection />)
  await screen.findByTestId('probe-row-aaaaaaaa')
}

describe('ProbeHostsSection', () => {
  it('lists the probe hosts once, with each daemon\'s alias', async () => {
    await mounted()
    expect(screen.getAllByTestId(/^probe-row-/)).toHaveLength(1)
    expect(screen.getByTestId('probe-row-aaaaaaaa').textContent).toContain('nas-a')
    expect(screen.getByTestId('probe-row-aaaaaaaa').textContent).toContain('nas-b')
  })

  it('the dialog asks the alias per daemon, options from each daemon, and writes both with their own alias and revision', async () => {
    await mounted()
    fireEvent.click(screen.getByTestId('probe-edit-aaaaaaaa'))
    const a = screen.getByTestId(`probe-alias-${A}`) as HTMLSelectElement
    const b = screen.getByTestId(`probe-alias-${B}`) as HTMLSelectElement
    expect([...a.options].map((o) => o.value)).toEqual(['', 'nas-a', 'a-other'])
    expect([...b.options].map((o) => o.value)).toEqual(['', 'nas-b', 'b-other'])
    fireEvent.change(a, { target: { value: 'a-other' } })
    fireEvent.change(b, { target: { value: 'b-other' } })
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
    const by = Object.fromEntries(putProbeHosts.mock.calls.map((c) => [c[0], c]))
    expect(by[A][1][0].ssh_alias).toBe('a-other')
    expect(by[A][2]).toBe(3)
    expect(by[B][1][0].ssh_alias).toBe('b-other')
    expect(by[B][2]).toBe(8)
  })

  it('a 409 from one daemon reloads both and says the list changed elsewhere', async () => {
    await mounted()
    putProbeHosts.mockImplementation(async (id: string) => { if (id === B) throw new HostConfigConflictError({ items: [], revision: 9 }) })
    fetchFleetConfig.mockClear()
    fireEvent.click(screen.getByTestId('probe-edit-aaaaaaaa'))
    fireEvent.click(screen.getByTestId('probe-save'))
    const msg = await screen.findByTestId('probe-list-error')
    expect(msg.textContent).toMatch(/elsewhere|別處/)
    expect(fetchFleetConfig.mock.calls.map((c) => c[0]).sort()).toEqual([A, B])
  })

  it('the 400 text shows inside the dialog', async () => {
    await mounted()
    putProbeHosts.mockRejectedValueOnce(new HostConfigApiError(400, 'unknown_ssh_alias: nas-a'))
    fireEvent.click(screen.getByTestId('probe-edit-aaaaaaaa'))
    fireEvent.click(screen.getByTestId('probe-save'))
    expect((await screen.findByTestId('probe-dialog-error')).textContent).toBe('unknown_ssh_alias: nas-a')
    expect(screen.getByTestId('probe-dialog')).toBeTruthy()
  })

  it('remove asks once: the first click writes nothing, the confirm writes both', async () => {
    await mounted()
    fireEvent.click(screen.getByTestId('probe-delete-aaaaaaaa'))
    expect(putProbeHosts).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('probe-delete-confirm-aaaaaaaa'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
    expect(putProbeHosts.mock.calls.every((c) => (c[1] as unknown[]).length === 0)).toBe(true)
  })

  it('dropped > 0: saving first shows the notice with the reason and writes nothing until confirmed', async () => {
    copies[A] = cfg([nas('nas-a')], 3, { count: 2, reasons: ['unknown_ssh_alias: gone'] })
    await mounted()
    fireEvent.click(screen.getByTestId('probe-delete-aaaaaaaa'))
    fireEvent.click(screen.getByTestId('probe-delete-confirm-aaaaaaaa'))
    const notice = await screen.findByTestId('probe-dropped-confirm')
    expect(notice.textContent).toContain('unknown_ssh_alias: gone')
    expect(putProbeHosts).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('probe-dropped-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
  })

  it('a focus request highlights the row once and is cleared', async () => {
    await mounted()
    vi.useFakeTimers()
    act(() => useFleetConfigStore.getState().requestProbeFocus('aaaaaaaa'))
    expect(screen.getByTestId('probe-row-aaaaaaaa').getAttribute('data-highlighted')).toBe('true')
    expect(useFleetConfigStore.getState().focusProbe).toBeNull()
    act(() => { vi.advanceTimersByTime(2500) })
    expect(screen.getByTestId('probe-row-aaaaaaaa').getAttribute('data-highlighted')).toBeNull()
  })

  it('renders nothing when no daemon carries fleet.v1', () => {
    useFleetStore.setState({ byHost: { [A]: { gen: 1, support: 'no', term: 0 } } as never })
    const { container } = render(<ProbeHostsSection />)
    expect(container.innerHTML).toBe('')
  })
})

describe('FleetRowEditor', () => {
  it('round trips order, peer alias and enabled with the row\'s revision', async () => {
    render(<FleetRowEditor hostId={A} />)
    const order = (await screen.findByTestId('fleet-row-order')) as HTMLInputElement
    await waitFor(() => expect(order.value).toBe('air26, mlab'))
    fireEvent.change(order, { target: { value: 'mlab, air26' } })
    fireEvent.change(screen.getByTestId('fleet-row-peer'), { target: { value: 'a-other' } })
    fireEvent.click(screen.getByLabelText('Fleet enabled on this daemon'))
    fireEvent.click(screen.getByTestId('fleet-row-save'))
    await waitFor(() => expect(putFleetRow).toHaveBeenCalledTimes(1))
    expect(putFleetRow).toHaveBeenCalledWith(A, { order: ['mlab', 'air26'], peer_ssh_alias: 'a-other', enabled: false }, 5)
  })

  it('shows the 400 text and the conflict notice', async () => {
    render(<FleetRowEditor hostId={A} />)
    await screen.findByTestId('fleet-row-order')
    await waitFor(() => expect((screen.getByTestId('fleet-row-save') as HTMLButtonElement).disabled).toBe(false))
    putFleetRow.mockRejectedValueOnce(new HostConfigApiError(400, 'order: bad'))
    fireEvent.click(screen.getByTestId('fleet-row-save'))
    expect((await screen.findByTestId('fleet-row-error')).textContent).toBe('order: bad')
    putFleetRow.mockRejectedValueOnce(new HostConfigConflictError({ items: {}, revision: 6 }))
    fireEvent.click(screen.getByTestId('fleet-row-save'))
    expect(await screen.findByTestId('fleet-row-conflict')).toBeTruthy()
  })
})
