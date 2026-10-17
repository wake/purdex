// W5c1: the 設定 page of a host tab — the 守門員 section writes the whole `watch` row with its revision, a 409 reloads, a 400 shows the
// daemon's text and puts the field back, the two daemons' `targets` are compared, offline locks it; a probe host gets the note and F-2a2's editor.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'

const hostFetch = vi.fn()
vi.mock('../../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../../lib/host-api')>()), hostFetch: (...a: unknown[]) => hostFetch(...a) }))
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
vi.mock('../../lib/fleet/reread', () => ({ readAllFleets: vi.fn(), readFleetOf: vi.fn() }))

import { WatchSettings } from './WatchSettings'
import { useFleetConfigStore } from '../../lib/fleet/config-store'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import { useFleetStore } from '../../lib/fleet/store'
import type { FleetConfigRead, ProbeRow } from '../../lib/fleet/config-api'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

const FULL = {
  enabled: true, x_cores: 2, y_mb: 1024, t_s: 60, pressure_level: 2, pressure_t_s: 120, page_rate: 50, page_t_s: 60,
  targets: ['air26', 'mlab'], mode: 'patrol', patrol_idle_min: 120, auto_wake: true,
  agent_command: 'claude --model opus', workdir: '~/w', start_prompt: '', wake_prompt: '', future_field: { a: 1 },
}
let daemons: Record<string, { items: Record<string, unknown>; revision: number }>
let putReply: (hostId: string, body: { items: Record<string, unknown>; baseRevision: number }) => Response
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

beforeEach(() => {
  cleanup()
  hostFetch.mockReset()
  daemons = { A: { items: { ...FULL }, revision: 3 }, B: { items: { ...FULL, targets: ['mlab', 'air26'] }, revision: 5 } }
  putReply = (id, body) => { daemons[id] = { items: body.items, revision: body.baseRevision + 1 }; return json(200, daemons[id]) }
  hostFetch.mockImplementation(async (id: string, path: string, init?: RequestInit) => {
    if (path === '/api/hostconfig' && init?.method !== 'PUT') return json(200, { watch: daemons[id] })
    if (path === '/api/hostconfig/watch' && init?.method === 'PUT') return putReply(id, JSON.parse(init.body as string))
    return json(404, {})
  })
  const host = (id: string) => ({ id, name: id, ip: '127.0.0.1', port: 7860, token: 't' })
  useHostStore.setState({ hosts: { A: host('A'), B: host('B') } as never, hostOrder: ['A', 'B'], runtime: {} as never })
})
afterEach(() => cleanup())

const daemonPage = (over: Partial<Parameters<typeof WatchSettings>[0]> = {}) =>
  render(<WatchSettings kind="daemon" fleetHostId="d-a" hostConfigId="A" offline={false} peerIds={[]} {...over} />)
const edit = (key: string, value: string) => {
  const el = screen.getByTestId(`watch-set-${key}`)
  fireEvent.change(el, { target: { value } })
  fireEvent.blur(el)
}
const puts = () => hostFetch.mock.calls.filter((c) => (c[2] as RequestInit | undefined)?.method === 'PUT')

describe('WatchSettings — the 守門員 section', () => {
  it('loads the row: values from it, and each value says where it comes from', async () => {
    daemons.A = { items: { enabled: true, x_cores: 2, y_mb: 1024, t_s: 60, pressure_level: 2, pressure_t_s: 120, page_rate: 50, page_t_s: 60 }, revision: 3 }
    daemonPage()
    expect(await screen.findByTestId('watch-set-x_cores')).toHaveValue('2')
    expect(screen.getByTestId('watch-set-source-x_cores')).toHaveTextContent(t('watch.set.source_set'))
    expect(screen.getByTestId('watch-set-source-mode')).toHaveTextContent(t('watch.set.source_default'))
    expect(screen.getByTestId('watch-set-targets')).toHaveValue('air26, mlab')
    expect(screen.getByTestId('watch-set-agent_command')).toHaveValue('claude --dangerously-skip-permissions --model opus')
  })

  it('a write sends the whole row (unknown fields too) with the revision, and shows the stored answer', async () => {
    daemonPage()
    await screen.findByTestId('watch-set-y_mb')
    edit('y_mb', '2048')
    await waitFor(() => expect(puts()).toHaveLength(1))
    const [id, path, init] = puts()[0] as [string, string, RequestInit]
    expect([id, path]).toEqual(['A', '/api/hostconfig/watch'])
    expect(JSON.parse(init.body as string)).toEqual({ items: { ...FULL, y_mb: 2048 }, baseRevision: 3 })
    await waitFor(() => expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('2048'))
    // The PUT's own answer is the new row (same shape as the GET): no second read.
    expect(hostFetch.mock.calls.filter((c) => c[0] === 'A' && (c[2] as RequestInit | undefined)?.method !== 'PUT')).toHaveLength(1)
    // The next write is built on the stored row and its new revision.
    edit('t_s', '90')
    await waitFor(() => expect(puts()).toHaveLength(2))
    expect(JSON.parse((puts()[1][2] as RequestInit).body as string)).toEqual({ items: { ...FULL, y_mb: 2048, t_s: 90 }, baseRevision: 4 })
  })

  it('a switch writes at once, and the targets are sent as a list of aliases', async () => {
    daemonPage()
    fireEvent.click(await screen.findByTestId('watch-set-auto_wake'))
    await waitFor(() => expect(puts()).toHaveLength(1))
    expect(JSON.parse((puts()[0][2] as RequestInit).body as string).items.auto_wake).toBe(false)
    edit('targets', 'mlab, air26 ,a19')
    await waitFor(() => expect(puts()).toHaveLength(2))
    expect(JSON.parse((puts()[1][2] as RequestInit).body as string).items.targets).toEqual(['mlab', 'air26', 'a19'])
  })

  it('a 409 shows the daemon\'s copy and says so; the change is not sent again by itself', async () => {
    putReply = () => json(409, { items: { ...FULL, x_cores: 9 }, revision: 4 })
    daemonPage()
    await screen.findByTestId('watch-set-y_mb')
    edit('y_mb', '2048')
    expect(await screen.findByTestId('watch-set-conflict')).toHaveTextContent(t('watch.set.conflict'))
    await waitFor(() => expect(screen.getByTestId('watch-set-x_cores')).toHaveValue('9'))
    expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('1024')
    expect(puts()).toHaveLength(1)
  })

  it('a 400 shows the daemon\'s text under the field and puts the field back', async () => {
    putReply = () => new Response('y_mb must be between 64 and 1048576', { status: 400 })
    daemonPage()
    await screen.findByTestId('watch-set-y_mb')
    edit('y_mb', '5')
    expect(await screen.findByTestId('watch-set-error-y_mb')).toHaveTextContent('y_mb must be between 64 and 1048576')
    expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('1024')
    expect(screen.queryByTestId('watch-set-conflict')).toBeNull()
  })

  it('an answer that does not read is a failed write: the error shows and the field goes back', async () => {
    putReply = () => json(200, { nonsense: true })
    daemonPage()
    await screen.findByTestId('watch-set-y_mb')
    edit('y_mb', '2048')
    expect(await screen.findByTestId('watch-set-error-y_mb')).toBeInTheDocument()
    expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('1024')
  })

  it('a value that is not a number is refused here, nothing is sent', async () => {
    daemonPage()
    await screen.findByTestId('watch-set-y_mb')
    edit('y_mb', 'abc')
    expect(await screen.findByTestId('watch-set-error-y_mb')).toHaveTextContent(t('watch.set.not_int'))
    expect(screen.getByTestId('watch-set-y_mb')).toHaveValue('1024')
    expect(puts()).toHaveLength(0)
  })

  it('warns when the other daemon\'s targets differ, and not when they match', async () => {
    daemonPage({ peerIds: ['B'] })
    expect(await screen.findByTestId('watch-set-targets-differ')).toBeInTheDocument()
    cleanup()
    daemons.B = { items: { ...FULL }, revision: 5 }
    daemonPage({ peerIds: ['B'] })
    await screen.findByTestId('watch-set-y_mb')
    await waitFor(() => expect(hostFetch.mock.calls.filter((c) => c[0] === 'B')).toHaveLength(2))
    expect(screen.queryByTestId('watch-set-targets-differ')).toBeNull()
  })

  it('offline: nothing is read, nothing can be edited', async () => {
    daemonPage({ offline: true })
    expect(await screen.findByTestId('watch-set-offline')).toHaveTextContent(t('watch.set.offline'))
    expect(hostFetch).not.toHaveBeenCalled()
    expect(screen.queryByTestId('watch-set-y_mb')).toBeNull()
  })

  it('a host the App has no config for says so', () => {
    daemonPage({ hostConfigId: undefined })
    expect(screen.getByTestId('watch-set-unmapped')).toBeInTheDocument()
    expect(hostFetch).not.toHaveBeenCalled()
  })
})

describe('WatchSettings — a probe host', () => {
  const ID = 'aaaaaaaa'
  const nas = (alias: string): ProbeRow => ({ id: ID, name: 'nas', ssh_alias: alias, icon: '', icon_weight: '' })
  const cfg = (alias: string, revision: number): FleetConfigRead => ({
    probe: { items: [nas(alias)], revision, dropped: null, invalid: false },
    fleet: { items: { order: ['air26', 'mlab'], peer_ssh_alias: 'mlab', enabled: true }, revision: 5, dropped: null, invalid: false },
  })

  it('shows the note, then the probe-host editor, which writes both daemons', async () => {
    for (const f of [fetchFleetConfig, fetchSshAliases, putProbeHosts, putFleetRow]) f.mockReset()
    const copies: Record<string, FleetConfigRead> = { A: cfg('nas-a', 3), B: cfg('nas-b', 8) }
    fetchFleetConfig.mockImplementation(async (id: string) => copies[id])
    fetchSshAliases.mockResolvedValue(['nas-a', 'nas-b'])
    putProbeHosts.mockResolvedValue(undefined)
    useFleetConfigStore.getState().reset()
    useFleetDraftStore.getState().selectProbe(null)
    useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } as never })
    useFleetStore.getState().reset()
    useFleetStore.setState({ byHost: { A: { gen: 1, support: 'yes', term: 0 }, B: { gen: 1, support: 'yes', term: 0 } } as never })

    render(<WatchSettings kind="probe" fleetHostId={ID} offline={false} peerIds={[]} />)
    expect(screen.getByTestId('watch-set-probe')).toHaveTextContent(t('watch.set.probe_note'))
    const name = await screen.findByTestId('probe-field-name')
    // The editor alone: no second header (badge and name) and no delete button.
    expect(screen.queryByTestId('probe-page')).toBeNull()
    expect(screen.queryByTestId('probe-page-name')).toBeNull()
    expect(screen.queryByTestId('probe-delete')).toBeNull()
    fireEvent.change(name, { target: { value: 'nas2' } })
    fireEvent.click(screen.getByTestId('probe-save'))
    await waitFor(() => expect(putProbeHosts).toHaveBeenCalledTimes(2))
    expect(putProbeHosts.mock.calls.map((c) => [c[0], c[1][0].name]).sort()).toEqual([['A', 'nas2'], ['B', 'nas2']])
  })

  describe('another probe host\'s unsaved draft (the Hosts page holds one draft at a time)', () => {
    const OTHER = 'bbbbbbbb'
    const row2 = (alias: string): ProbeRow => ({ id: OTHER, name: 'other', ssh_alias: alias, icon: '', icon_weight: '' })
    beforeEach(() => {
      for (const f of [fetchFleetConfig, fetchSshAliases, putProbeHosts, putFleetRow]) f.mockReset()
      const two = (alias: string, rev: number): FleetConfigRead => ({ ...cfg(alias, rev), probe: { items: [nas(alias), row2(alias)], revision: rev, dropped: null, invalid: false } })
      const copies: Record<string, FleetConfigRead> = { A: two('nas-a', 3), B: two('nas-b', 8) }
      fetchFleetConfig.mockImplementation(async (id: string) => copies[id])
      fetchSshAliases.mockResolvedValue(['nas-a', 'nas-b'])
      useFleetConfigStore.getState().reset()
      useFleetDraftStore.getState().reset()
      useHostStore.setState({ runtime: { A: { status: 'connected' }, B: { status: 'connected' } } as never })
      useFleetStore.getState().reset()
      useFleetStore.setState({ byHost: { A: { gen: 1, support: 'yes', term: 0 }, B: { gen: 1, support: 'yes', term: 0 } } as never })
      // The reader has typed into probe host `nas` on the Hosts page and not saved.
      const entry = { id: ID, name: 'nas', icon: '', icon_weight: '', aliases: { A: 'nas-a', B: 'nas-b' } }
      useFleetDraftStore.getState().beginProbe(entry, false, { revisions: { A: 3, B: 8 }, rows: { A: nas('nas-a'), B: nas('nas-b') } })
      useFleetDraftStore.getState().setProbeDraft((d) => ({ ...d, name: 'typed, not saved' }))
    })
    afterEach(() => { vi.restoreAllMocks(); useFleetDraftStore.getState().reset() })

    it('asks first; refused → the draft stays, no editor, a line says whose it is, with a way there', async () => {
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
      const onGoto = vi.fn()
      render(<WatchSettings kind="probe" fleetHostId={OTHER} offline={false} peerIds={[]} onGotoProbe={onGoto} />)
      const held = await screen.findByTestId('probe-held')
      expect(confirm).toHaveBeenCalledTimes(1)
      expect(held).toHaveTextContent(t('watch.set.held', { name: 'nas' }))
      expect(useFleetDraftStore.getState().probe?.draft.name).toBe('typed, not saved')
      expect(useFleetDraftStore.getState().probe?.initial.id).toBe(ID)
      expect(screen.queryByTestId('probe-field-name')).toBeNull()
      fireEvent.click(screen.getByTestId('probe-held-goto'))
      expect(onGoto).toHaveBeenCalledWith(ID)
    })

    it('agreed → the other draft is dropped and this host\'s editor opens', async () => {
      vi.spyOn(window, 'confirm').mockReturnValue(true)
      render(<WatchSettings kind="probe" fleetHostId={OTHER} offline={false} peerIds={[]} />)
      expect(await screen.findByTestId('probe-field-name')).toHaveValue('other')
      expect(useFleetDraftStore.getState().probe?.initial.id).toBe(OTHER)
      expect(screen.queryByTestId('probe-held')).toBeNull()
    })
  })
})
