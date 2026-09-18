import { describe, it, expect, vi, beforeEach } from 'vitest'
import { StrictMode } from 'react'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { PeersSection } from './PeersSection'
import { useHostStore } from '../../stores/useHostStore'
import * as api from '../../lib/host-api'
import { HostApiError, type PeerHostRow, type PeerHostVerify } from '../../lib/host-api'

vi.mock('../../lib/host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/host-api')>()),
  fetchHostInfo: vi.fn(),
  fetchPeerSettings: vi.fn(),
  listPeerHosts: vi.fn(),
  verifyPeerHost: vi.fn(),
  updatePeerHost: vi.fn(),
}))

const M = 'hM'
const A = 'hA'

const AIR_ROW: PeerHostRow = { alias: 'air', url: 'http://100.64.0.4:7860', host_id: 'wakes-air-2026:oa6drb',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true }
const MLAB_ROW: PeerHostRow = { alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true }
const ok = (alias: string, self_alias: string, host_id: string): PeerHostVerify =>
  ({ alias, host_id, ok: true, self_alias, daemon_version: '1.0.0-alpha.378' })

// High-entropy so a leak into the DOM cannot be mistaken for ordinary text (spec D-8).
const SECRET_M = 'pdx_admin_secret_M_9f3k2q8w'
const SECRET_A = 'pdx_admin_secret_A_7t5r1z0x'

function seedHosts(airStatus: 'connected' | 'disconnected' = 'connected') {
  useHostStore.setState({
    hosts: {
      [M]: { id: M, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, token: SECRET_M },
      [A]: { id: A, name: 'Air 2026', ip: '100.64.0.4', port: 7860, order: 1, token: SECRET_A },
    },
    hostOrder: [M, A],
    runtime: { [M]: { status: 'connected' }, [A]: { status: airStatus } },
  })
}

/** The §2.1 fixture: mlab's entry `air`, the peer calls itself `air26`, both directions green. */
function seedFixture() {
  vi.mocked(api.fetchHostInfo).mockImplementation(async (h) => ({
    host_id: h === M ? 'mini-lab:278cbm' : 'wakes-air-2026:oa6drb',
    tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '',
  }))
  vi.mocked(api.fetchPeerSettings).mockImplementation(async (h) => ({ deliver: true, alias: h === M ? 'mini-lab' : 'air26' }))
  vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [AIR_ROW] : [MLAB_ROW]))
  vi.mocked(api.verifyPeerHost).mockImplementation(async (h, alias) =>
    h === M ? ok(alias, 'air26', 'wakes-air-2026:oa6drb') : ok(alias, 'mini-lab', 'mini-lab:278cbm'))
  vi.mocked(api.updatePeerHost).mockResolvedValue({ ...AIR_ROW, alias: 'air26' })
}

beforeEach(() => {
  vi.mocked(api.fetchHostInfo).mockReset()
  vi.mocked(api.fetchPeerSettings).mockReset()
  vi.mocked(api.listPeerHosts).mockReset()
  vi.mocked(api.verifyPeerHost).mockReset()
  vi.mocked(api.updatePeerHost).mockReset()
  seedHosts()
  seedFixture()
})

describe('PeersSection — the §2.1 fixture', () => {
  it('renders the §5.3 row: three names labelled, both lines green, status bidirectional, drift + Rename', async () => {
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    // The selected host's own self alias, labelled (the third of the §2.1 names).
    const self = screen.getByTestId('peers-self')
    expect(self).toHaveTextContent('self alias')
    expect(self).toHaveTextContent('mini-lab')
    expect(self).toHaveTextContent('mini-lab:278cbm')
    expect(within(row).getByTestId('peer-alias')).toHaveTextContent('air')
    expect(within(row).getByTestId('peer-app-host')).toHaveTextContent('Air 2026')
    expect(within(row).getByTestId('peer-url')).toHaveTextContent('http://100.64.0.4:7860')
    expect(within(row).getByTestId('peer-host-id')).toHaveTextContent('wakes-air-2026:oa6drb')
    expect(within(row).getByTestId('peer-outbound')).toHaveAttribute('data-ok', 'true')
    expect(within(row).getByTestId('peer-outbound')).toHaveTextContent('1.0.0-alpha.378')
    expect(within(row).getByTestId('peer-inbound')).toHaveAttribute('data-ok', 'true')
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('mini-lab')     // "(Air 2026's entry: mini-lab)"
    expect(within(row).getByTestId('peer-outbound-drift')).toHaveTextContent('air26')
    expect(within(row).getByTestId('peer-outbound-rename')).toHaveTextContent('air26')
    expect(within(row).queryByTestId('peer-inbound-drift')).toBeNull()               // mlab's self alias equals air's entry name
    expect(within(row).queryByTestId('peer-inbound-rename')).toBeNull()
  })

  it('paints checking while the dials are out, then fills in', async () => {
    let release!: (v: PeerHostVerify) => void
    vi.mocked(api.verifyPeerHost).mockImplementation((h, alias) =>
      h === M ? new Promise<PeerHostVerify>((r) => { release = r }) : Promise.resolve(ok(alias, 'mini-lab', 'mini-lab:278cbm')))
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-inbound')).toHaveAttribute('data-ok', 'true'))
    expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'checking')
    release(ok('air', 'air26', 'wakes-air-2026:oa6drb'))
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
  })

  it('Rename calls updatePeerHost(X, "air", {alias: "air26"}) and refreshes (verifies run again)', async () => {
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    const before = vi.mocked(api.verifyPeerHost).mock.calls.length
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [{ ...AIR_ROW, alias: 'air26' }] : [MLAB_ROW]))
    fireEvent.click(within(row).getByTestId('peer-outbound-rename'))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(M, 'air', { alias: 'air26' }))
    await screen.findByTestId('peer-row-air26')
    await waitFor(() => expect(vi.mocked(api.verifyPeerHost).mock.calls.length).toBeGreaterThan(before))
    expect(screen.queryByTestId('peer-outbound-drift')).toBeNull()
  })

  it('a 409 on Rename shows the daemon message inline and keeps the row', async () => {
    vi.mocked(api.updatePeerHost).mockRejectedValue(new HostApiError(409, 'Conflict', 'alias "air26" is already used by another host'))
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    fireEvent.click(within(row).getByTestId('peer-outbound-rename'))
    expect(await within(row).findByTestId('peer-outbound-rename-error')).toHaveTextContent('alias "air26" is already used by another host')
    expect(screen.getByTestId('peer-row-air')).toBeInTheDocument()
    expect(within(row).getByTestId('peer-outbound-rename')).toBeEnabled()
  })

  it('Rename on the return line acts on the counterpart host', async () => {
    // air's entry for mlab is named "mlab", mlab calls itself "mini-lab" → drift on the return line.
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [AIR_ROW] : [{ ...MLAB_ROW, alias: 'mlab' }]))
    vi.mocked(api.updatePeerHost).mockResolvedValue({ ...MLAB_ROW, alias: 'mini-lab' })
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    const btn = await within(row).findByTestId('peer-inbound-rename')
    expect(btn).toHaveTextContent('mini-lab')
    fireEvent.click(btn)
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(A, 'mlab', { alias: 'mini-lab' }))
  })
})

describe('PeersSection — the return side', () => {
  it('a peer that is not an App host renders outbound-only, the D-4 sentence, and no return Rename', async () => {
    vi.mocked(api.listPeerHosts).mockImplementation(async () => [{ ...AIR_ROW, alias: 'stranger', host_id: 'stranger:aaaaaa', url: 'http://10.0.0.1:7860' }])
    vi.mocked(api.verifyPeerHost).mockResolvedValue(ok('stranger', 'strange-self', 'stranger:aaaaaa'))
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-stranger')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'outbound-only'))
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('not verifiable')
    expect(within(row).queryByTestId('peer-inbound-rename')).toBeNull()
    expect(within(row).queryByTestId('peer-app-host')).toBeNull()
    // outbound drift still offered
    expect(within(row).getByTestId('peer-outbound-rename')).toHaveTextContent('strange-self')
  })

  it('a disconnected App host renders return-unknown with the cause and Refresh as the only action', async () => {
    seedHosts('disconnected')
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'return-unknown'))
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('Air 2026 could not be asked: disconnected')
    expect(within(row).queryByTestId('peer-inbound-rename')).toBeNull()
    expect(api.fetchHostInfo).not.toHaveBeenCalledWith(A)
    expect(screen.getByTestId('peers-refresh')).toBeEnabled()
  })

  it('outbound failure with no return entry renders unpaired and shows the daemon error', async () => {
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [{ ...AIR_ROW, has_token: false }] : []))
    vi.mocked(api.verifyPeerHost).mockResolvedValue({ alias: 'air', host_id: 'wakes-air-2026:oa6drb', ok: false, error: 'no outbound token', self_alias: '', daemon_version: '' })
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'unpaired'))
    expect(within(row).getByTestId('peer-outbound')).toHaveAttribute('data-ok', 'false')
    expect(within(row).getByTestId('peer-outbound')).toHaveTextContent('no outbound token')
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('no entry for “mini-lab” on Air 2026')
  })
})

describe('PeersSection — page states', () => {
  it('a failing precondition renders one banner naming the call and a Retry that re-runs', async () => {
    vi.mocked(api.listPeerHosts).mockRejectedValueOnce(new HostApiError(403, 'Forbidden', 'admin required'))
    render(<PeersSection hostId={M} />)
    const banner = await screen.findByTestId('peers-banner')
    expect(banner).toHaveTextContent('list')
    expect(banner).toHaveTextContent('admin required')
    expect(screen.queryByTestId(/^peer-row-/)).toBeNull()
    fireEvent.click(screen.getByTestId('peers-retry'))
    await screen.findByTestId('peer-row-air')
  })

  it('an empty list renders the empty sentence', async () => {
    vi.mocked(api.listPeerHosts).mockResolvedValue([])
    render(<PeersSection hostId={M} />)
    expect(await screen.findByTestId('peers-empty')).toBeInTheDocument()
  })

  it('Refresh re-runs everything and disables itself while running', async () => {
    render(<PeersSection hostId={M} />)
    await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
    const n = vi.mocked(api.verifyPeerHost).mock.calls.length
    fireEvent.click(screen.getByTestId('peers-refresh'))
    await waitFor(() => expect(vi.mocked(api.verifyPeerHost).mock.calls.length).toBe(n + 2))
  })

  it('a result that lands after hostId changed is dropped', async () => {
    // Every verify(M, …) parks until released, in call order — the FIRST one
    // belongs to the run for hostId=M that the rerender abandons.
    const releases: Array<(v: PeerHostVerify) => void> = []
    vi.mocked(api.verifyPeerHost).mockImplementation((h, alias) =>
      h === M ? new Promise<PeerHostVerify>((r) => { releases.push(r) }) : Promise.resolve(ok(alias, 'mini-lab', 'mini-lab:278cbm')))
    const { rerender } = render(<PeersSection hostId={M} />)
    await screen.findByTestId('peer-row-air')
    expect(releases).toHaveLength(1)
    rerender(<PeersSection hostId={A} />)
    await screen.findByTestId('peer-row-mini-lab')       // A's page: its entry for mlab
    expect(releases).toHaveLength(2)                     // A's run dialled M for the return path
    releases[0](ok('air', 'air26', 'wakes-air-2026:oa6drb'))   // the abandoned run's dial lands late
    await new Promise((r) => setTimeout(r, 0))
    expect(screen.queryByTestId('peer-row-air')).toBeNull()
    expect(screen.getByTestId('peer-row-mini-lab')).toBeInTheDocument()
  })

  it('never renders a token value (spec D-8): neither host admin token reaches the DOM, even in attributes', async () => {
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    expect(api.updatePeerHost).not.toHaveBeenCalled()
    expect(document.body.innerHTML).not.toContain(SECRET_M)
    expect(document.body.innerHTML).not.toContain(SECRET_A)
  })

  it('under StrictMode (dev double-mount) the page still ends bidirectional and does not paint the discarded first run', async () => {
    render(<StrictMode><PeersSection hostId={M} /></StrictMode>)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    expect(screen.getAllByTestId('peer-row-air')).toHaveLength(1)
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
  })
})
