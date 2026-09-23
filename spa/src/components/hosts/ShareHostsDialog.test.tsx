import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import * as transferApi from '../../lib/host-transfer-api'
import { useHostStore } from '../../stores/useHostStore'
import { ShareHostsDialog } from './ShareHostsDialog'

const TRUST = /will hold the access tokens of the hosts you share, readable by that host, until the code is used or expires \(10 min\)\. Only relay through a host you trust\./

beforeEach(() => {
  cleanup()
  useHostStore.setState({
    hosts: {
      relay: { id: 'relay', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, token: 'relay-tok', daemonId: 'd1_m' },
      air: { id: 'air', name: 'air26', ip: '100.64.0.4', port: 7860, order: 1, token: 'air-tok', icon: 'Laptop' },
      bare: { id: 'bare', name: 'tokenless', ip: '10.0.0.9', port: 7860, order: 2 },
    },
    hostOrder: ['relay', 'air', 'bare'],
    runtime: { relay: { status: 'connected' }, air: { status: 'disconnected' } },
    activeHostId: 'relay',
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ShareHostsDialog', () => {
  it('shows the trust sentence, naming the relay, before any code is created', () => {
    const create = vi.spyOn(transferApi, 'createTransfer')
    render(<ShareHostsDialog onClose={() => {}} />)
    const trust = screen.getByTestId('transfer-trust')
    expect(trust.textContent).toMatch(TRUST)
    expect(trust.textContent).toContain('mlab')
    expect(create).not.toHaveBeenCalled()
  })

  it('lists a host without a token disabled, and never sends it', async () => {
    const create = vi.spyOn(transferApi, 'createTransfer').mockResolvedValue({ kind: 'ok', code: 'ABCD2345', expiresAt: Date.now() + 600_000 })
    render(<ShareHostsDialog onClose={() => {}} />)
    expect((screen.getByLabelText(/tokenless/) as HTMLInputElement).disabled).toBe(true)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create code' }))
    })
    expect(create).toHaveBeenCalledTimes(1)
    const [relay, rows] = create.mock.calls[0]
    expect(relay).toBe('relay')
    expect(rows.map((r) => r.name)).toEqual(['mlab', 'air26'])
    expect(rows[0]).toMatchObject({ token: 'relay-tok', daemonId: 'd1_m' })
    expect(rows[1]).toMatchObject({ token: 'air-tok', look: { icon: 'Laptop' } })
  })

  it('an unticked host is left out', async () => {
    const create = vi.spyOn(transferApi, 'createTransfer').mockResolvedValue({ kind: 'ok', code: 'ABCD2345', expiresAt: 0 })
    render(<ShareHostsDialog onClose={() => {}} />)
    fireEvent.click(screen.getByLabelText(/air26/))
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create code' }))
    })
    expect(create.mock.calls[0][1].map((r) => r.name)).toEqual(['mlab'])
  })

  it('shows the code as ABCD-2345 with the relay name', async () => {
    vi.spyOn(transferApi, 'createTransfer').mockResolvedValue({ kind: 'ok', code: 'ABCD2345', expiresAt: Date.now() + 600_000 })
    render(<ShareHostsDialog onClose={() => {}} />)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create code' }))
    })
    expect(screen.getByTestId('transfer-code').textContent).toBe('ABCD-2345')
    expect(screen.getByTestId('transfer-code-meta').textContent).toContain('mlab')
  })

  it('a failure is shown in words', async () => {
    vi.spyOn(transferApi, 'createTransfer').mockResolvedValue({ kind: 'failed', reason: 'capacity', status: 429 })
    render(<ShareHostsDialog onClose={() => {}} />)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Create code' }))
    })
    expect(screen.getByRole('alert').textContent).toMatch(/Too many open codes on mlab/)
  })

  it('offers only connected hosts as the relay; none connected → create disabled', () => {
    useHostStore.setState({ runtime: {} })
    render(<ShareHostsDialog onClose={() => {}} />)
    expect((screen.getByRole('button', { name: 'Create code' }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByText(/No connected host to relay through/)).toBeTruthy()
  })
})
