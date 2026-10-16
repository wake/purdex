// F-2d: a probe host's own page on the Hosts page — the form written to BOTH daemons, the check, the delete, the header.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act, cleanup } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { createTab } from '../../types/tab'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'

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
const runProbeCheck = vi.fn()
vi.mock('../../lib/fleet/probe-check', async (orig) => ({
  ...(await orig<typeof import('../../lib/fleet/probe-check')>()),
  runProbeCheck: (...a: unknown[]) => runProbeCheck(...a),
}))

import { ProbeHostPage } from './ProbeHostPage'
import { startNewProbe } from './useProbeEditor'
import { useFleetConfigStore } from '../../lib/fleet/config-store'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import { useFleetStore } from '../../lib/fleet/store'
import { useHostStore } from '../../stores/useHostStore'
import { HostConfigApiError, HostConfigConflictError } from '../../lib/host-config-api'
import type { FleetConfigRead, ProbeRow } from '../../lib/fleet/config-api'

const A = 'ha'
const B = 'hb'
const ID = 'aaaaaaaa'
const nas = (alias: string): ProbeRow => ({ id: ID, name: 'nas', ssh_alias: alias, icon: '', icon_weight: '' })
const row = (id: string, name: string, alias: string): ProbeRow => ({ id, name, ssh_alias: alias, icon: '', icon_weight: '' })
const cfg = (items: ProbeRow[], revision: number, dropped: FleetConfigRead['probe']['dropped'] = null): FleetConfigRead => ({
  probe: { items, revision, dropped, invalid: false },
  fleet: { items: { order: ['air26', 'mlab'], peer_ssh_alias: 'mlab', enabled: true }, revision: 5, dropped: null, invalid: false },
})
let copies: Record<string, FleetConfigRead>
const pending = () => { let release: () => void = () => {}; const p = new Promise<void>((res) => { release = res }); return { p, release } }
const typeName = (v: string) => fireEvent.change(screen.getByTestId('probe-field-name'), { target: { value: v } })
const pickAliases = () => {
  fireEvent.change(screen.getByTestId(`probe-alias-${A}`), { target: { value: 'a-other' } })
  fireEvent.change(screen.getByTestId(`probe-alias-${B}`), { target: { value: 'b-other' } })
}

beforeEach(() => {
  for (const f of [fetchFleetConfig, fetchSshAliases, putProbeHosts, putFleetRow, runProbeCheck]) f.mockReset()
  copies = { [A]: cfg([nas('nas-a')], 3), [B]: cfg([nas('nas-b')], 8) }
  fetchFleetConfig.mockImplementation(async (id: string) => copies[id])
  fetchSshAliases.mockImplementation(async (id: string) => (id === A ? ['nas-a', 'a-other'] : ['nas-b', 'b-other']))
  putProbeHosts.mockResolvedValue(undefined)
  putFleetRow.mockResolvedValue(undefined)
  runProbeCheck.mockResolvedValue({ kind: 'unsupported' })
  useFleetConfigStore.getState().reset()
  const host = (id: string) => ({ id, name: id, ip: '127.0.0.1', port: 7860, token: 't' })
  useHostStore.setState({
    hosts: { [A]: host(A), [B]: host(B) } as never,
    hostOrder: [A, B],
    runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } } as never,
  })
  useFleetStore.getState().reset()
  useFleetStore.setState({
    byHost: { [A]: { gen: 1, support: 'yes', term: 0 }, [B]: { gen: 1, support: 'yes', term: 0 } } as never,
  })
})
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

async function mounted(id = ID) {
  useFleetDraftStore.getState().selectProbe({ id, isNew: false })
  render(<ProbeHostPage probeId={id} />)
  await screen.findByTestId('probe-field-name')
}

describe('ProbeHostPage — the form writes both daemons', () => {
  it('asks the alias per daemon, options from each daemon, and writes both with their own alias and revision', async () => {
    await mounted()
    const a = screen.getByTestId(`probe-alias-${A}`) as HTMLSelectElement
    const b = screen.getByTestId(`probe-alias-${B}`) as HTMLSelectElement
    expect([...a.options].map((o) => o.value)).toEqual(['', 'nas-a', 'a-other'])
    expect([...b.options].map((o) => o.value)).toEqual(['', 'nas-b', 'b-other'])
    pickAliases()
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
    const by = Object.fromEntries(putProbeHosts.mock.calls.map((c) => [c[0], c]))
    expect(by[A][1][0].ssh_alias).toBe('a-other')
    expect(by[A][2]).toBe(3)
    expect(by[B][1][0].ssh_alias).toBe('b-other')
    expect(by[B][2]).toBe(8)
  })

  it('a 409 from one daemon reloads both and says the list changed elsewhere; the form and draft stay', async () => {
    await mounted()
    putProbeHosts.mockImplementation(async (id: string) => { if (id === B) throw new HostConfigConflictError({ items: [], revision: 9 }) })
    fetchFleetConfig.mockClear()
    typeName('keep me')
    fireEvent.click(screen.getByTestId('probe-save'))
    const msg = await screen.findByTestId('probe-form-error')
    expect(msg.textContent).toMatch(/elsewhere|別處/)
    expect(msg.textContent).toContain('Written on ha')
    expect((screen.getByTestId('probe-field-name') as HTMLInputElement).value).toBe('keep me')
    expect(fetchFleetConfig.mock.calls.map((c) => c[0]).sort()).toEqual([A, B])
  })

  it('the 400 text shows inside the form', async () => {
    await mounted()
    putProbeHosts.mockRejectedValueOnce(new HostConfigApiError(400, 'unknown_ssh_alias: nas-a'))
    fireEvent.click(screen.getByTestId('probe-save'))
    expect((await screen.findByTestId('probe-form-error')).textContent).toContain('ha refused it: unknown_ssh_alias: nas-a')
  })

  it('a daemon changed the row since the edit began: a retry asks before overwriting, then overwrites on confirm', async () => {
    await mounted()
    typeName('mine')
    putProbeHosts.mockImplementation(async (id: string) => { if (id === B) throw new HostConfigConflictError({ items: [], revision: 9 }) })
    copies[A] = cfg([{ ...nas('nas-a'), name: 'mine' }], 4)
    copies[B] = cfg([{ ...nas('nas-b'), name: 'theirs' }], 9)
    fireEvent.click(screen.getByTestId('probe-save'))
    expect((await screen.findByTestId('probe-form-error')).textContent).toContain('Changed elsewhere on hb')
    putProbeHosts.mockReset().mockResolvedValue(undefined)
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(screen.getByTestId('probe-form-error').textContent).toContain('hb was changed elsewhere since you started'))
    expect(putProbeHosts).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('probe-overwrite'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalled())
    expect(putProbeHosts.mock.calls.find((c) => c[0] === B)![2]).toBe(9)
  })

  it('a name over 40 characters, or one with a control character, is refused before anything is sent', async () => {
    await mounted()
    typeName('x'.repeat(41))
    fireEvent.click(screen.getByTestId('probe-save'))
    expect(screen.getByTestId('probe-form-invalid').textContent).toContain('1 to 40')
    typeName('bad\u0007name')
    fireEvent.click(screen.getByTestId('probe-save'))
    expect(screen.getByTestId('probe-form-invalid').textContent).toContain('control')
    expect(putProbeHosts).not.toHaveBeenCalled()
  })

  it('a daemon dropping while it is open turns Save off and says why', async () => {
    await mounted()
    act(() => { useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'disconnected' } } as never }) })
    expect((screen.getByTestId('probe-save') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByTestId('probe-form-blocked').textContent).toContain('hb')
    expect((screen.getByTestId('probe-delete') as HTMLButtonElement).disabled).toBe(true)
  })

  it('a row only one daemon has: saving asks for the missing alias before anything is written', async () => {
    copies[A] = cfg([nas('nas-a'), row('bbbbbbbb', 'half', 'half-a')], 3)
    await mounted('bbbbbbbb')
    expect(screen.getByTestId('probe-half-bbbbbbbb').textContent).toContain('exists only on')
    fireEvent.click(screen.getByTestId('probe-save'))
    expect(screen.getByTestId('probe-form-invalid')).toBeTruthy()
    expect(putProbeHosts).not.toHaveBeenCalled()
  })

  it('the same id with different content on the two daemons is marked and the rewrite writes only the daemon that differs', async () => {
    copies[B] = cfg([{ ...nas('nas-b'), name: 'other name' }], 8)
    await mounted()
    expect(screen.getByTestId(`probe-mismatch-${ID}`).textContent).toContain('disagree')
    fireEvent.click(screen.getByTestId(`probe-rewrite-${ID}`))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(1))
    expect(putProbeHosts.mock.calls[0][0]).toBe(B)
  })
})

describe('ProbeHostPage — delete', () => {
  it('asks once: the first click writes nothing, the confirm writes both and leaves the entry', async () => {
    await mounted()
    fireEvent.click(screen.getByTestId('probe-delete'))
    expect(putProbeHosts).not.toHaveBeenCalled()
    expect(screen.getByTestId('probe-delete-prompt').textContent).toContain('"nas"')
    fireEvent.click(screen.getByTestId('probe-delete-confirm'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
    expect(putProbeHosts.mock.calls.every((c) => (c[1] as unknown[]).length === 0)).toBe(true)
    await waitFor(() => expect(useFleetDraftStore.getState().selectedProbe).toBeNull())
  })

  it('cancel leaves everything as it was', async () => {
    await mounted()
    fireEvent.click(screen.getByTestId('probe-delete'))
    fireEvent.click(screen.getByTestId('probe-delete-cancel'))
    expect(screen.queryByTestId('probe-delete-prompt')).toBeNull()
    expect(putProbeHosts).not.toHaveBeenCalled()
  })

  it('dropped > 0: the notice with the reason comes first and nothing is written until confirmed', async () => {
    copies[A] = cfg([nas('nas-a')], 3, { count: 2, reasons: ['unknown_ssh_alias: gone'] })
    await mounted()
    fireEvent.click(screen.getByTestId('probe-delete'))
    fireEvent.click(screen.getByTestId('probe-delete-confirm'))
    const notice = await screen.findByTestId('probe-dropped-confirm')
    expect(notice.textContent).toContain('unknown_ssh_alias: gone')
    expect(putProbeHosts).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('probe-dropped-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
  })
})

describe('ProbeHostPage — a new probe host', () => {
  it('starts as a blank draft on its own page and a save writes it to both daemons under a fresh id', async () => {
    await act(async () => { await useFleetConfigStore.getState().loadMany([A, B]) })
    startNewProbe()
    const sel = useFleetDraftStore.getState().selectedProbe!
    expect(sel.isNew).toBe(true)
    expect(sel.id).toMatch(/^[0-9a-f]{8}$/)
    render(<ProbeHostPage probeId={sel.id} />)
    await screen.findByTestId('probe-field-name')
    expect((screen.getByTestId('probe-field-name') as HTMLInputElement).value).toBe('')
    typeName('new one')
    pickAliases()
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
    expect((putProbeHosts.mock.calls[0][1] as ProbeRow[]).map((r) => r.id)).toEqual([ID, sel.id])
  })

  it('an id that is nowhere says so', async () => {
    useFleetDraftStore.getState().selectProbe({ id: 'zzzzzzzz', isNew: false })
    render(<ProbeHostPage probeId="zzzzzzzz" />)
    expect(await screen.findByTestId('probe-page-missing')).toBeTruthy()
  })
})

describe('ProbeHostPage — the header', () => {
  const snapshot = (over: Record<string, unknown>) => useFleetStore.setState({
    primaryHostId: A,
    snapshot: {
      hostId: A, term: 1, order: [ID],
      hosts: { [ID]: { id: ID, kind: 'probe', alias: 'nas-a', name: 'nas', icon: '', os: '', status: 'ok', error: null, last_ok_at: null, fast: null, slow: null, ...over } },
    } as never,
  })
  it('shows the name, the zone word and who measures it', async () => {
    snapshot({ fast: { zone: 'warning', load: 70 } })
    await mounted()
    expect(screen.getByTestId('probe-page-name').textContent).toBe('nas')
    expect(screen.getByTestId('probe-page-zone').textContent).toBe('High')
    expect(screen.getByTestId('probe-page-measured').textContent).toBe('Measured by nas-a (primary)')
    expect(screen.queryByTestId('probe-page-last-ok')).toBeNull()
  })
  it('offline: the word 離線 and the last good reading\'s time', async () => {
    snapshot({ status: 'offline', last_ok_at: 1_700_000_000_000 })
    await mounted()
    expect(screen.getByTestId('probe-page-zone').textContent).toBe('Offline')
    expect(screen.getByTestId('probe-page-last-ok').textContent).toContain(new Date(1_700_000_000_000).toLocaleString())
  })
})

describe('ProbeHostPage — the connection check', () => {
  it('runs on open with each daemon\'s own alias, and again after a save that changed an alias', async () => {
    runProbeCheck.mockResolvedValue({ kind: 'answer', answer: { ssh: 'ok', measure: 'ok', error: null } })
    putProbeHosts.mockImplementation(async (id: string, items: ProbeRow[], rev: number) => { copies[id] = cfg(items, rev + 1) })
    await mounted()
    await waitFor(() => expect(runProbeCheck.mock.calls.map((c) => [c[0], c[1]]).sort()).toEqual([[A, 'nas-a'], [B, 'nas-b']]))
    pickAliases()
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(runProbeCheck.mock.calls.some((c) => c[0] === A && c[1] === 'a-other')).toBe(true))
    expect(runProbeCheck.mock.calls.some((c) => c[0] === B && c[1] === 'b-other')).toBe(true)
  })
})

// CLAUDE.md tab-hosted checklist: the Hosts page is a tab and unmounts when the reader switches away (keepAliveCount 0).
describe('the page across a real tab switch', () => {
  const Page = () => <ProbeHostPage probeId={ID} />
  const Other = () => <div data-testid="other-tab" />
  const hostsTab = { ...createTab({ kind: 'hosts' }), id: 't-hosts' }
  const dashTab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
  const all = [hostsTab, dashTab]
  beforeEach(() => {
    cleanup(); clearModuleRegistry()
    registerModule({ id: 'hosts', name: 'Hosts', panes: [{ kind: 'hosts', component: Page }] })
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
    useUISettingsStore.setState({ keepAliveCount: 0 })
    useFleetDraftStore.getState().selectProbe({ id: ID, isNew: false })
  })
  const away = (rerender: (ui: React.ReactElement) => void) => {
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('probe-page')).toBeNull() // really unmounted
    rerender(<TabContent activeTab={hostsTab} allTabs={all} />)
  }
  it('keeps the typed draft and the delete prompt', async () => {
    const { rerender } = render(<TabContent activeTab={hostsTab} allTabs={all} />)
    await screen.findByTestId('probe-field-name')
    typeName('half typed')
    fireEvent.click(screen.getByTestId('probe-delete'))
    away(rerender)
    await screen.findByTestId('probe-field-name')
    expect((screen.getByTestId('probe-field-name') as HTMLInputElement).value).toBe('half typed')
    expect(screen.getByTestId('probe-delete-prompt')).toBeTruthy()
  })
  it('a save in flight is still in flight after a switch away and back: Save stays off', async () => {
    const gate = pending()
    putProbeHosts.mockImplementation(async () => { await gate.p })
    const { rerender } = render(<TabContent activeTab={hostsTab} allTabs={all} />)
    await screen.findByTestId('probe-field-name')
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect((screen.getByTestId('probe-save') as HTMLButtonElement).disabled).toBe(true))
    away(rerender)
    await screen.findByTestId('probe-field-name')
    expect((screen.getByTestId('probe-save') as HTMLButtonElement).disabled).toBe(true)
    expect(putProbeHosts).toHaveBeenCalledTimes(2)
    gate.release()
  })
})

describe('ProbeHostPage — zh-TW', () => {
  it('comes out in zh-TW', async () => {
    useI18nStore.getState().setLocale('zh-TW')
    try {
      await mounted()
      expect(screen.getByText('樣式與連線')).toBeTruthy()
      expect(screen.getByText('連線檢查')).toBeTruthy()
    } finally {
      useI18nStore.getState().setLocale('en')
    }
  })
})
