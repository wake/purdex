import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup, act } from '@testing-library/react'
import { ProbeHostsSection } from './ProbeHostsSection'
import { useFleetStore } from '../../lib/fleet/store'
import { useProbeDraftStore } from '../../lib/fleet/probe-draft-memory'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { FleetProbe, ProbeHostDef } from '../../lib/fleet/types'

vi.mock('../../lib/fleet/api', async (orig) => ({
  ...(await orig<typeof import('../../lib/fleet/api')>()),
  fetchProbeHosts: vi.fn(), fetchSshAliases: vi.fn(), putProbeHosts: vi.fn(),
}))
import { fetchProbeHosts, fetchSshAliases, putProbeHosts } from '../../lib/fleet/api'

const H = 'h1'
const A: ProbeHostDef = { id: 'a1', name: 'air-2019', ssh_alias: 'air19' }
const B: ProbeHostDef = { id: 'b2', name: 'mserv', ssh_alias: 'mserv' }
const nums = { cpu: 1, mem: 2, disk: 3, load1: 0, ncpu: 4, zone: 'normal', trend: 'flat', sampled_at: 1 } as const
const liveProbe = (d: ProbeHostDef, over: Partial<FleetProbe> = {}): FleetProbe => ({ ...d, ...nums, os: 'Linux', status: 'ok', last_ok_at: 1, error: null, ...over })

function setup(opts: { items?: ProbeHostDef[]; revision?: number; probes?: FleetProbe[]; support?: 'yes' | 'no' } = {}) {
  const items = opts.items ?? [A]
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: [H], activeHostId: H, runtime: { [H]: { status: 'connected' } },
  } as never)
  const gen = useFleetStore.getState().begin(H)
  useFleetStore.getState().setSupport(H, gen, opts.support ?? 'yes')
  useFleetStore.getState().applyEvent(H, { self: null, probes: opts.probes ?? [] })
  vi.mocked(fetchProbeHosts).mockResolvedValue({ items, revision: opts.revision ?? 3 })
}

beforeEach(() => {
  cleanup()
  useFleetStore.getState().reset()
  useProbeDraftStore.getState().reset()
  vi.mocked(fetchProbeHosts).mockReset()
  vi.mocked(putProbeHosts).mockReset()
  vi.mocked(fetchSshAliases).mockReset().mockResolvedValue(['air19', 'mserv'])
  useI18nStore.getState().setLocale('en')
})
afterEach(() => cleanup())

const fill = async (name: string, alias: string) => {
  fireEvent.change(screen.getByTestId('probe-field-name'), { target: { value: name } })
  await waitFor(() => expect(screen.getByTestId('probe-field-alias').querySelectorAll('option').length).toBeGreaterThan(1))
  fireEvent.change(screen.getByTestId('probe-field-alias'), { target: { value: alias } })
}
const openAdd = async () => {
  render(<ProbeHostsSection hostId={H} />)
  await waitFor(() => expect((screen.getByTestId('probe-add') as HTMLButtonElement).disabled).toBe(false))
  fireEvent.click(screen.getByTestId('probe-add'))
}

describe('visibility', () => {
  it('shows nothing for a host without fleet.v1', () => {
    setup({ support: 'no' })
    render(<ProbeHostsSection hostId={H} />)
    expect(screen.queryByTestId('probe-section')).toBeNull()
    expect(fetchProbeHosts).not.toHaveBeenCalled()
  })
  it('shows nothing while the host is offline', () => {
    setup()
    useHostStore.setState({ runtime: { [H]: { status: 'disconnected' } } } as never)
    render(<ProbeHostsSection hostId={H} />)
    expect(screen.queryByTestId('probe-section')).toBeNull()
  })
})

describe('rows', () => {
  it('lists the stored hosts with the live state beside each', async () => {
    setup({ items: [A, B], probes: [liveProbe(A), liveProbe(B, { status: 'error', error: 'host_key' })] })
    render(<ProbeHostsSection hostId={H} />)
    await screen.findByTestId('probe-row-a1')
    expect(screen.getByTestId('probe-status-a1').textContent).toBe('Reachable')
    expect(screen.getByTestId('probe-status-b2').textContent).toBe('Problem · host key refused')
  })
  it('a row the daemon has not probed yet says so', async () => {
    setup({ items: [A], probes: [] })
    render(<ProbeHostsSection hostId={H} />)
    await screen.findByTestId('probe-row-a1')
    expect(screen.getByTestId('probe-status-a1').textContent).toBe('Not probed yet')
  })
})

describe('add / edit / save', () => {
  it('PUTs the whole list with the collection revision and closes on success', async () => {
    setup({ items: [A], revision: 3 })
    vi.mocked(putProbeHosts).mockResolvedValue({ kind: 'ok', current: { items: [A, { ...B, id: 'srv' }], revision: 4 } })
    await openAdd()
    await fill('  mserv ', 'mserv')
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalled())
    expect(putProbeHosts).toHaveBeenCalledWith(H, [A, { id: '', name: 'mserv', ssh_alias: 'mserv' }], 3)
    await waitFor(() => expect(screen.queryByTestId('probe-dialog')).toBeNull())
    expect(screen.getByTestId('probe-row-srv')).toBeInTheDocument()
  })

  it('the draft\'s look goes into the PUT', async () => {
    setup({ items: [] })
    vi.mocked(putProbeHosts).mockResolvedValue({ kind: 'ok', current: { items: [], revision: 4 } })
    await openAdd()
    await fill('x', 'air19')
    fireEvent.click(screen.getByTestId('host-color-layer-main'))
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalled())
    const sent = vi.mocked(putProbeHosts).mock.calls[0][1][0]
    expect(sent.colors?.console?.main.color).toMatch(/^#[0-9a-f]{6}$/)
  })

  it('editing replaces the row with the same id', async () => {
    setup({ items: [A, B] })
    vi.mocked(putProbeHosts).mockResolvedValue({ kind: 'ok', current: { items: [], revision: 4 } })
    render(<ProbeHostsSection hostId={H} />)
    fireEvent.click(await screen.findByTestId('probe-edit-b2'))
    fireEvent.change(screen.getByTestId('probe-field-name'), { target: { value: 'mserv 2' } })
    await waitFor(() => expect((screen.getByTestId('probe-field-alias') as HTMLSelectElement).value).toBe('mserv'))
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalled())
    expect(putProbeHosts).toHaveBeenCalledWith(H, [A, { id: 'b2', name: 'mserv 2', ssh_alias: 'mserv' }], 3)
  })

  it('refuses an empty name or SSH host without a request', async () => {
    setup()
    await openAdd()
    fireEvent.click(screen.getByTestId('probe-save'))
    expect(screen.getByTestId('probe-error-name')).toBeInTheDocument()
    fireEvent.change(screen.getByTestId('probe-field-name'), { target: { value: 'n' } })
    fireEvent.click(screen.getByTestId('probe-save'))
    expect(screen.getByTestId('probe-error-alias')).toBeInTheDocument()
    expect(putProbeHosts).not.toHaveBeenCalled()
  })

  it('a 409 reloads the list, says it changed elsewhere and keeps the dialog; saving again uses the new revision', async () => {
    setup({ items: [A], revision: 3 })
    vi.mocked(putProbeHosts)
      .mockResolvedValueOnce({ kind: 'conflict', current: { items: [A, B], revision: 9 } })
      .mockResolvedValueOnce({ kind: 'ok', current: { items: [], revision: 10 } })
    await openAdd()
    await fill('new', 'air19')
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(screen.getByTestId('probe-dialog-error').textContent).toContain('Changed elsewhere'))
    expect(screen.getByTestId('probe-row-b2')).toBeInTheDocument()
    expect(screen.getByTestId('probe-dialog')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
    expect(putProbeHosts).toHaveBeenLastCalledWith(H, [A, B, { id: '', name: 'new', ssh_alias: 'air19' }], 9)
  })

  it('a 400 shows the daemon text as it is, in the dialog', async () => {
    setup()
    vi.mocked(putProbeHosts).mockResolvedValue({ kind: 'invalid', text: 'unknown_ssh_alias: nope' })
    await openAdd()
    await fill('n', 'air19')
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(screen.getByTestId('probe-dialog-error').textContent).toBe('unknown_ssh_alias: nope'))
  })

  it('a late answer for a cancelled dialog does not close or mark the next one', async () => {
    setup({ items: [A] })
    let answer!: (v: never) => void
    vi.mocked(putProbeHosts).mockReturnValue(new Promise((r) => { answer = r as never }))
    await openAdd()
    await fill('first', 'air19')
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalled())
    fireEvent.click(screen.getByTestId('probe-cancel'))
    fireEvent.click(screen.getByTestId('probe-add'))
    fireEvent.change(screen.getByTestId('probe-field-name'), { target: { value: 'second' } })
    await act(async () => { answer({ kind: 'invalid', text: 'old failure' } as never) })
    expect(screen.getByTestId('probe-dialog')).toBeInTheDocument()
    expect(screen.queryByTestId('probe-dialog-error')).toBeNull()
    expect((screen.getByTestId('probe-field-name') as HTMLInputElement).value).toBe('second')
  })

  it('cancel closes the dialog without a request', async () => {
    setup()
    await openAdd()
    fireEvent.click(screen.getByTestId('probe-cancel'))
    expect(screen.queryByTestId('probe-dialog')).toBeNull()
    expect(putProbeHosts).not.toHaveBeenCalled()
  })
})

describe('remove', () => {
  it('asks once: nothing is sent until the confirm', async () => {
    setup({ items: [A, B], revision: 5 })
    vi.mocked(putProbeHosts).mockResolvedValue({ kind: 'ok', current: { items: [B], revision: 6 } })
    render(<ProbeHostsSection hostId={H} />)
    fireEvent.click(await screen.findByTestId('probe-remove-a1'))
    expect(putProbeHosts).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('probe-remove-confirm-a1'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(1))
    expect(putProbeHosts).toHaveBeenCalledWith(H, [B], 5)
    await waitFor(() => expect(screen.queryByTestId('probe-row-a1')).toBeNull())
  })
  it('cancelling the question sends nothing', async () => {
    setup({ items: [A] })
    render(<ProbeHostsSection hostId={H} />)
    fireEvent.click(await screen.findByTestId('probe-remove-a1'))
    fireEvent.click(screen.getByText('Cancel'))
    expect(putProbeHosts).not.toHaveBeenCalled()
    expect(screen.getByTestId('probe-row-a1')).toBeInTheDocument()
  })
  it('a 409 on remove reloads and says so above the list', async () => {
    setup({ items: [A] })
    vi.mocked(putProbeHosts).mockResolvedValue({ kind: 'conflict', current: { items: [A, B], revision: 8 } })
    render(<ProbeHostsSection hostId={H} />)
    fireEvent.click(await screen.findByTestId('probe-remove-a1'))
    fireEvent.click(screen.getByTestId('probe-remove-confirm-a1'))
    await waitFor(() => expect(screen.getByTestId('probe-notice').textContent).toContain('Changed elsewhere'))
    expect(screen.getByTestId('probe-row-b2')).toBeInTheDocument()
  })
})

describe('focusProbe', () => {
  it('scrolls to the named row, highlights it once and clears the request', async () => {
    setup({ items: [A, B] })
    const scroll = vi.fn()
    Element.prototype.scrollIntoView = scroll
    render(<ProbeHostsSection hostId={H} />)
    await screen.findByTestId('probe-row-b2')
    act(() => useFleetStore.getState().setFocusProbe({ proberHostId: H, probeId: 'b2' }))
    await waitFor(() => expect(screen.getByTestId('probe-row-b2').getAttribute('data-highlight')).toBe('true'))
    expect(scroll).toHaveBeenCalled()
    expect(useFleetStore.getState().focusProbe).toBeNull()
    expect(screen.getByTestId('probe-row-a1').getAttribute('data-highlight')).toBe('false')
  })
  it('a request for another prober is left alone', async () => {
    setup({ items: [A] })
    render(<ProbeHostsSection hostId={H} />)
    await screen.findByTestId('probe-row-a1')
    act(() => useFleetStore.getState().setFocusProbe({ proberHostId: 'other', probeId: 'a1' }))
    expect(useFleetStore.getState().focusProbe).not.toBeNull()
  })
})
