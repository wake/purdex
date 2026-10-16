// F-2a2: the Hosts page's probe-host section (written to BOTH daemons) and the `fleet` row editor.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act, cleanup } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { createTab } from '../../types/tab'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

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
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

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
    // The dialog stays (the draft is not lost) and says what each daemon took.
    const msg = await screen.findByTestId('probe-dialog-error')
    expect(msg.textContent).toMatch(/elsewhere|別處/)
    expect(screen.getByTestId('probe-dialog')).toBeTruthy()
    expect(fetchFleetConfig.mock.calls.map((c) => c[0]).sort()).toEqual([A, B])
  })

  it('the 400 text shows inside the dialog', async () => {
    await mounted()
    putProbeHosts.mockRejectedValueOnce(new HostConfigApiError(400, 'unknown_ssh_alias: nas-a'))
    fireEvent.click(screen.getByTestId('probe-edit-aaaaaaaa'))
    fireEvent.click(screen.getByTestId('probe-save'))
    expect((await screen.findByTestId('probe-dialog-error')).textContent).toContain('ha refused it: unknown_ssh_alias: nas-a')
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
    fireEvent.change(screen.getByTestId('fleet-row-peer'), { target: { value: '' } }) // this daemon's alias list has no `mlab`
    putFleetRow.mockRejectedValueOnce(new HostConfigApiError(400, 'order: bad'))
    fireEvent.click(screen.getByTestId('fleet-row-save'))
    expect((await screen.findByTestId('fleet-row-error')).textContent).toBe('order: bad')
    putFleetRow.mockRejectedValueOnce(new HostConfigConflictError({ items: {}, revision: 6 }))
    fireEvent.click(screen.getByTestId('fleet-row-save'))
    expect(await screen.findByTestId('fleet-row-conflict')).toBeTruthy()
  })
})

const row = (id: string, name: string, alias: string): ProbeRow => ({ id, name, ssh_alias: alias, icon: '', icon_weight: '' })
const typeName = (v: string) => fireEvent.change(screen.getByTestId('probe-field-name'), { target: { value: v } })
const pickAliases = () => {
  fireEvent.change(screen.getByTestId(`probe-alias-${A}`), { target: { value: 'a-other' } })
  fireEvent.change(screen.getByTestId(`probe-alias-${B}`), { target: { value: 'b-other' } })
}

describe('ProbeHostsSection — a row only one daemon has', () => {
  it('is never sent with an empty alias: a save refuses and says which daemon has the row', async () => {
    copies[A] = cfg([nas('nas-a'), row('bbbbbbbb', 'half', 'half-a')], 3)
    await mounted()
    expect(screen.getByTestId('probe-half-bbbbbbbb').textContent).toContain('exists only on')
    fireEvent.click(screen.getByTestId('probe-delete-aaaaaaaa'))
    fireEvent.click(screen.getByTestId('probe-delete-confirm-aaaaaaaa'))
    expect((await screen.findByTestId('probe-list-error')).textContent).toContain('"half" exists only on')
    expect(putProbeHosts).not.toHaveBeenCalled()
  })
  it('removing the half row itself is allowed', async () => {
    copies[A] = cfg([nas('nas-a'), row('bbbbbbbb', 'half', 'half-a')], 3)
    await mounted()
    fireEvent.click(screen.getByTestId('probe-delete-bbbbbbbb'))
    fireEvent.click(screen.getByTestId('probe-delete-confirm-bbbbbbbb'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
    for (const c of putProbeHosts.mock.calls) expect((c[1] as ProbeRow[]).every((r) => r.ssh_alias !== '')).toBe(true)
  })
  it('editing it asks for the missing daemon\'s alias before anything is written', async () => {
    copies[A] = cfg([nas('nas-a'), row('bbbbbbbb', 'half', 'half-a')], 3)
    await mounted()
    fireEvent.click(screen.getByTestId('probe-edit-bbbbbbbb'))
    fireEvent.click(screen.getByTestId('probe-save'))
    expect(screen.getByTestId('probe-dialog-invalid')).toBeTruthy()
    expect(putProbeHosts).not.toHaveBeenCalled()
  })
})

describe('ProbeHostsSection — what each daemon took', () => {
  it('A saved and B failed: the dialog and draft stay, the text says where it was written and why B refused', async () => {
    await mounted()
    putProbeHosts.mockImplementation(async (id: string) => { if (id === B) throw new HostConfigApiError(400, 'unknown_ssh_alias: nas-b') })
    fetchFleetConfig.mockClear()
    fireEvent.click(screen.getByTestId('probe-edit-aaaaaaaa'))
    typeName('renamed')
    fireEvent.click(screen.getByTestId('probe-save'))
    const msg = (await screen.findByTestId('probe-dialog-error')).textContent!
    expect(msg).toContain('Written on ha')
    expect(msg).toContain('hb refused it: unknown_ssh_alias: nas-b')
    expect(msg).toMatch(/now differ/)
    expect((screen.getByTestId('probe-field-name') as HTMLInputElement).value).toBe('renamed')
    expect(fetchFleetConfig.mock.calls.map((c) => c[0]).sort()).toEqual([A, B])
  })
  it('one saved and one 409: the draft stays and the message names the daemon that holds the other version', async () => {
    await mounted()
    putProbeHosts.mockImplementation(async (id: string) => { if (id === B) throw new HostConfigConflictError({ items: [], revision: 9 }) })
    fireEvent.click(screen.getByTestId('probe-edit-aaaaaaaa'))
    typeName('keep me')
    fireEvent.click(screen.getByTestId('probe-save'))
    const msg = (await screen.findByTestId('probe-dialog-error')).textContent!
    expect(msg).toContain('Written on ha')
    expect(msg).toContain('Changed elsewhere on hb')
    expect((screen.getByTestId('probe-field-name') as HTMLInputElement).value).toBe('keep me')
  })
  it('the same id with different content on the two daemons is marked and can be rewritten on both', async () => {
    copies[B] = cfg([{ ...nas('nas-b'), name: 'other name' }], 8)
    await mounted()
    expect(screen.getByTestId('probe-mismatch-aaaaaaaa').textContent).toContain('disagree')
    fireEvent.click(screen.getByTestId('probe-rewrite-aaaaaaaa'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
    expect((putProbeHosts.mock.calls[0][1] as ProbeRow[])[0].name).toBe('nas')
    expect((putProbeHosts.mock.calls[1][1] as ProbeRow[])[0].name).toBe('nas')
  })
})

describe('ProbeHostsSection — fewer reachable daemons than known ones', () => {
  it('writing is off and the text says which daemon is unreachable', async () => {
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'disconnected' } } as never })
    render(<ProbeHostsSection />)
    await screen.findByTestId('probe-row-aaaaaaaa')
    expect(screen.getByTestId('probe-unreachable').textContent).toContain('hb')
    expect((screen.getByTestId('probe-add') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByTestId('probe-edit-aaaaaaaa') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByTestId('probe-delete-aaaaaaaa') as HTMLButtonElement).disabled).toBe(true)
    expect(putProbeHosts).not.toHaveBeenCalled()
  })
})

describe('ProbeHostsSection — the dialog mirrors the daemon', () => {
  const openAdd = async () => { await mounted(); fireEvent.click(screen.getByTestId('probe-add')) }
  it('a name over 40 characters is refused before anything is sent', async () => {
    await openAdd()
    typeName('x'.repeat(41))
    pickAliases()
    fireEvent.click(screen.getByTestId('probe-save'))
    expect(screen.getByTestId('probe-dialog-invalid').textContent).toContain('1 to 40')
    expect(putProbeHosts).not.toHaveBeenCalled()
  })
  it('40 characters pass, counted as characters (not bytes)', async () => {
    await openAdd()
    typeName('機'.repeat(40))
    pickAliases()
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
  })
  it('a control character is refused', async () => {
    await openAdd()
    typeName('bad\u0007name')
    pickAliases()
    fireEvent.click(screen.getByTestId('probe-save'))
    expect(screen.getByTestId('probe-dialog-invalid').textContent).toContain('control')
    expect(putProbeHosts).not.toHaveBeenCalled()
  })
  it('a new id avoids the ids already in the list', async () => {
    let n = 0
    vi.spyOn(crypto, 'getRandomValues').mockImplementation(((a: Uint8Array) => { a.set(n++ === 0 ? [0xaa, 0xaa, 0xaa, 0xaa] : [1, 2, 3, 4]); return a }) as never)
    await openAdd()
    typeName('new one')
    pickAliases()
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
    expect((putProbeHosts.mock.calls[0][1] as ProbeRow[]).map((r) => r.id)).toEqual(['aaaaaaaa', '01020304'])
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

describe('the config store reads', () => {
  it('a slow older read does not put an old revision back', async () => {
    let releaseOld: (v: FleetConfigRead) => void = () => {}
    fetchFleetConfig.mockReset()
    fetchFleetConfig.mockImplementationOnce(() => new Promise<FleetConfigRead>((res) => { releaseOld = res }))
    fetchFleetConfig.mockImplementationOnce(async () => cfg([nas('new')], 9))
    const old = useFleetConfigStore.getState().load(A)
    await useFleetConfigStore.getState().load(A)
    releaseOld(cfg([nas('old')], 1))
    await old
    expect(useFleetConfigStore.getState().byHost[A].probe.revision).toBe(9)
  })
})

// CLAUDE.md tab-hosted checklist: the Hosts page is a tab and unmounts when the reader switches away (keepAliveCount 0).
describe('the editors across a real tab switch', () => {
  const Probe = () => <><FleetRowEditor hostId={A} /><ProbeHostsSection /></>
  const Other = () => <div data-testid="other-tab" />
  const hostsTab = { ...createTab({ kind: 'hosts' }), id: 't-hosts' }
  const dashTab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
  const all = [hostsTab, dashTab]
  beforeEach(() => {
    cleanup(); clearModuleRegistry()
    registerModule({ id: 'hosts', name: 'Hosts', panes: [{ kind: 'hosts', component: Probe }] })
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
    useUISettingsStore.setState({ keepAliveCount: 0 })
  })
  const away = (rerender: (ui: React.ReactElement) => void) => {
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('probe-hosts-section')).toBeNull() // really unmounted
    rerender(<TabContent activeTab={hostsTab} allTabs={all} />)
  }
  it('keeps the probe dialog draft, the delete prompt, the dropped confirmation and the fleet row fields', async () => {
    copies[A] = cfg([nas('nas-a')], 3, { count: 1, reasons: ['gone'] })
    const { rerender } = render(<TabContent activeTab={hostsTab} allTabs={all} />)
    await screen.findByTestId('probe-row-aaaaaaaa')
    const order = (await screen.findByTestId('fleet-row-order')) as HTMLInputElement
    await waitFor(() => expect(order.value).toBe('air26, mlab'))
    fireEvent.change(order, { target: { value: 'mlab, air26' } })
    fireEvent.click(screen.getByTestId('probe-edit-aaaaaaaa'))
    typeName('half typed')
    away(rerender)
    await screen.findByTestId('probe-row-aaaaaaaa')
    expect((screen.getByTestId('probe-field-name') as HTMLInputElement).value).toBe('half typed')
    expect((screen.getByTestId('fleet-row-order') as HTMLInputElement).value).toBe('mlab, air26')
    // the delete prompt and the dropped confirmation
    fireEvent.click(screen.getAllByText('Cancel')[0])
    fireEvent.click(screen.getByTestId('probe-delete-aaaaaaaa'))
    away(rerender)
    fireEvent.click(await screen.findByTestId('probe-delete-confirm-aaaaaaaa'))
    await screen.findByTestId('probe-dropped-confirm')
    away(rerender)
    expect(await screen.findByTestId('probe-dropped-confirm')).toBeTruthy()
  })
})
