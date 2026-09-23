import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import * as hostApi from '../../lib/host-api'
import * as transferApi from '../../lib/host-transfer-api'
import { useHostStore, type HostInfo } from '../../stores/useHostStore'
import { ReceiveHostsDialog } from './ReceiveHostsDialog'

function info(hostId: string): HostInfo {
  return { host_id: hostId, tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '' }
}

const PAYLOAD = [
  { name: 'air26', ip: '100.64.0.4', port: 7860, token: 'tok-air', daemonId: 'd1_air' },
  { name: 'm-remote', ip: '1.1.1.1', port: 1, token: 'payload-m-tok' },
  { name: 'dead', ip: '10.9.9.9', port: 7860, token: 'tok-dead' },
  { bogus: true },
]

const ANSWERS: Record<string, HostInfo | Error> = {
  'http://100.64.0.4:7860': info('d1_air'),
  'http://1.1.1.1:1': info('d1_m'),
  'http://10.9.9.9:7860': new Error('unreachable'),
}

let infoSpy: MockInstance<typeof hostApi.fetchInfoAt>
const realApply = useHostStore.getState().applyHostTransfer

beforeEach(() => {
  cleanup()
  useHostStore.setState({
    applyHostTransfer: realApply,
    hosts: {
      relay: { id: 'relay', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, token: 'relay-tok' },
      m: { id: 'm', name: 'm', ip: '1.1.1.1', port: 1, order: 1, token: 'local-m-tok', daemonId: 'd1_m' },
    },
    hostOrder: ['relay', 'm'],
    runtime: { relay: { status: 'connected' } },
    activeHostId: 'relay',
  })
  infoSpy = vi.spyOn(hostApi, 'fetchInfoAt').mockImplementation(async (base: string) => {
    const a = ANSWERS[base]
    if (!a) throw new Error('unexpected base')
    if (a instanceof Error) throw a
    return a
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

async function redeem(code = 'abcd-2345') {
  render(<ReceiveHostsDialog onClose={() => {}} />)
  fireEvent.change(screen.getByLabelText('Transfer code'), { target: { value: code } })
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Redeem' }))
  })
}

function rowOf(name: string): HTMLElement {
  return screen.getByTestId(`transfer-row-${name}`)
}

function checkboxOf(name: string): HTMLInputElement {
  return rowOf(name).querySelector('input[type="checkbox"]') as HTMLInputElement
}

describe('ReceiveHostsDialog', () => {
  it('redeems on the picked relay with the typed code', async () => {
    const spy = vi.spyOn(transferApi, 'redeemTransfer').mockResolvedValue({ kind: 'ok', hosts: PAYLOAD })
    await redeem()
    expect(spy).toHaveBeenCalledWith('relay', 'abcd-2345')
  })

  it('shows invalid_code in words', async () => {
    vi.spyOn(transferApi, 'redeemTransfer').mockResolvedValue({ kind: 'failed', reason: 'invalid_code', status: 404 })
    await redeem()
    expect(screen.getByRole('alert').textContent).toBe('Unknown, expired or already used code.')
  })

  it('shows rate_limited with the wait in seconds', async () => {
    vi.spyOn(transferApi, 'redeemTransfer').mockResolvedValue({ kind: 'failed', reason: 'rate_limited', status: 429, retryAfterS: 42 })
    await redeem()
    expect(screen.getByRole('alert').textContent).toMatch(/try again in 42 s/)
  })

  it('verifies EVERY row with the PAYLOAD token, never a local one', async () => {
    vi.spyOn(transferApi, 'redeemTransfer').mockResolvedValue({ kind: 'ok', hosts: PAYLOAD })
    await redeem()
    const calls = infoSpy.mock.calls.map(([base, token]) => [base, token])
    expect(calls).toEqual([
      ['http://100.64.0.4:7860', 'tok-air'],
      ['http://1.1.1.1:1', 'payload-m-tok'],
      ['http://10.9.9.9:7860', 'tok-dead'],
    ])
  })

  it('counts unreadable rows and shows a status per row', async () => {
    vi.spyOn(transferApi, 'redeemTransfer').mockResolvedValue({ kind: 'ok', hosts: PAYLOAD })
    await redeem()
    expect(screen.getByText('1 row(s) could not be read.')).toBeTruthy()
    expect(rowOf('air26').textContent).toContain('New')
    expect(rowOf('m-remote').textContent).toContain('Already here')
    expect(rowOf('dead').textContent).toContain('Could not verify')
  })

  it('only pickable rows can be ticked; existing only in overwrite mode', async () => {
    vi.spyOn(transferApi, 'redeemTransfer').mockResolvedValue({ kind: 'ok', hosts: PAYLOAD })
    await redeem()
    expect(checkboxOf('air26').disabled).toBe(false)
    expect(checkboxOf('air26').checked).toBe(true)
    expect(checkboxOf('dead').disabled).toBe(true)
    expect(checkboxOf('dead').checked).toBe(false)
    expect(checkboxOf('m-remote').disabled).toBe(true)
    fireEvent.click(screen.getByLabelText(/Also update matching hosts/))
    expect(checkboxOf('m-remote').disabled).toBe(false)
    expect(checkboxOf('m-remote').checked).toBe(true)
  })

  it('replace-all is shown disabled, "available after an update"', async () => {
    vi.spyOn(transferApi, 'redeemTransfer').mockResolvedValue({ kind: 'ok', hosts: PAYLOAD })
    await redeem()
    const radio = screen.getByLabelText(/Replace all hosts/) as HTMLInputElement
    expect(radio.disabled).toBe(true)
    expect(radio.closest('label')?.textContent).toMatch(/available after an update/)
  })

  it('a unverified row can be retried', async () => {
    vi.spyOn(transferApi, 'redeemTransfer').mockResolvedValue({ kind: 'ok', hosts: PAYLOAD })
    await redeem()
    ANSWERS['http://10.9.9.9:7860'] = info('d1_dead')
    await act(async () => {
      fireEvent.click(rowOf('dead').querySelector('button') as HTMLButtonElement)
    })
    ANSWERS['http://10.9.9.9:7860'] = new Error('unreachable')
    expect(rowOf('dead').textContent).toContain('New')
    expect(infoSpy).toHaveBeenCalledTimes(4)
  })

  it('confirm calls applyHostTransfer ONCE with the picked rows', async () => {
    vi.spyOn(transferApi, 'redeemTransfer').mockResolvedValue({ kind: 'ok', hosts: PAYLOAD })
    const apply = vi.fn().mockReturnValue({ kind: 'applied', created: ['x'], overwritten: [] })
    useHostStore.setState({ applyHostTransfer: apply })
    await redeem()
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(apply).toHaveBeenCalledTimes(1)
    expect(apply.mock.calls[0][0]).toEqual({
      create: [{ name: 'air26', ip: '100.64.0.4', port: 7860, token: 'tok-air', daemonId: 'd1_air' }],
      overwrite: [],
    })
    expect(screen.getByText('Added 1, updated 0.')).toBeTruthy()
  })

  it('a stale plan says so and writes nothing', async () => {
    vi.spyOn(transferApi, 'redeemTransfer').mockResolvedValue({ kind: 'ok', hosts: PAYLOAD })
    useHostStore.setState({ applyHostTransfer: vi.fn().mockReturnValue({ kind: 'stale' }) })
    await redeem()
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    expect(screen.getByRole('alert').textContent).toBe('The host list changed, check again.')
  })

  it('end to end with the real store: air26 is added and verified', async () => {
    vi.spyOn(transferApi, 'redeemTransfer').mockResolvedValue({ kind: 'ok', hosts: PAYLOAD })
    await redeem()
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    const s = useHostStore.getState()
    const added = Object.values(s.hosts).find((h) => h.name === 'air26')
    expect(added).toMatchObject({ ip: '100.64.0.4', token: 'tok-air', daemonId: 'd1_air' })
    expect(s.hosts.m.token).toBe('local-m-tok')
  })
})
