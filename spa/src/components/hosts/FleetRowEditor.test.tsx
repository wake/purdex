// F-2a2: the `fleet` row editor.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

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
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

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

  it('keeps the row\'s source when it saves (the editor has no field for it, and a save must not drop it)', async () => {
    copies[A].fleet.items = { ...copies[A].fleet.items, source: 'pool' }
    render(<FleetRowEditor hostId={A} />)
    const order = (await screen.findByTestId('fleet-row-order')) as HTMLInputElement
    await waitFor(() => expect(order.value).toBe('air26, mlab'))
    fireEvent.change(order, { target: { value: 'mlab, air26' } })
    fireEvent.change(screen.getByTestId('fleet-row-peer'), { target: { value: 'a-other' } })
    fireEvent.click(screen.getByTestId('fleet-row-save'))
    await waitFor(() => expect(putFleetRow).toHaveBeenCalledTimes(1))
    expect(putFleetRow).toHaveBeenCalledWith(A, { order: ['mlab', 'air26'], peer_ssh_alias: 'a-other', enabled: true, source: 'pool' }, 5)
  })

  it('shows the 400 text and the conflict notice', async () => {
    render(<FleetRowEditor hostId={A} />)
    await screen.findByTestId('fleet-row-order')
    await waitFor(() => expect((screen.getByTestId('fleet-row-save') as HTMLButtonElement).disabled).toBe(false))
    fireEvent.change(screen.getByTestId('fleet-row-peer'), { target: { value: '' } }) // this daemon's alias list has no `mlab`
    putFleetRow.mockRejectedValueOnce(new HostConfigApiError(400, 'order: bad'))
    fireEvent.click(screen.getByTestId('fleet-row-save'))
    expect((await screen.findByTestId('fleet-row-error')).textContent).toBe('order: bad')
    putFleetRow.mockRejectedValueOnce(new HostConfigConflictError({ items: {}, revision: 6 }))
    fireEvent.click(screen.getByTestId('fleet-row-save'))
    expect(await screen.findByTestId('fleet-row-conflict')).toBeTruthy()
  })
})

describe('the fleet row editor mirrors the daemon', () => {
  const save = async (order: string) => {
    render(<FleetRowEditor hostId={A} />)
    const input = (await screen.findByTestId('fleet-row-order')) as HTMLInputElement
    await waitFor(() => expect(input.value).toBe('air26, mlab'))
    fireEvent.change(input, { target: { value: order } })
    fireEvent.change(screen.getByTestId('fleet-row-peer'), { target: { value: '' } })
    fireEvent.click(screen.getByTestId('fleet-row-save'))
  }
  it.each([
    ['five aliases', 'a, b, c, d, e', /1 to 4/],
    ['none', ' , ', /1 to 4/],
    ['a bad shape', 'air26, bad alias', /not a valid host alias/],
    ['a duplicate, compared in lower case', 'air26, AIR26', /listed twice/],
  ])('%s is refused with a message and nothing is sent', async (_n, order, msg) => {
    await save(order)
    expect((await screen.findByTestId('fleet-row-invalid')).textContent).toMatch(msg)
    expect(putFleetRow).not.toHaveBeenCalled()
  })
  it('a peer alias this daemon does not know is refused', async () => {
    render(<FleetRowEditor hostId={A} />)
    await waitFor(() => expect((screen.getByTestId('fleet-row-save') as HTMLButtonElement).disabled).toBe(false))
    // the stored row names `mlab`, which this daemon's alias list does not carry
    fireEvent.click(screen.getByTestId('fleet-row-save'))
    expect((await screen.findByTestId('fleet-row-invalid')).textContent).toContain('"mlab" is not an SSH alias this daemon knows')
    expect(putFleetRow).not.toHaveBeenCalled()
  })
})
