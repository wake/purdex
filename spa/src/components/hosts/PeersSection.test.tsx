import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
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
  addPeerHost: vi.fn(),
  deletePeerHost: vi.fn(),
  rotatePeerHost: vi.fn(),
  commitRotation: vi.fn(),
  cancelRotation: vi.fn(),
  updatePeerSettings: vi.fn(),
}))

const M = 'hM'
const A = 'hA'

const AIR_ROW: PeerHostRow = { alias: 'air', url: 'http://100.64.0.4:7860', host_id: 'wakes-air-2026:oa6drb',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true,
  rotation_pending: false, last_inbound_auth: '' }
const MLAB_ROW: PeerHostRow = { alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm',
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true,
  rotation_pending: false, last_inbound_auth: '' }
const ok = (alias: string, self_alias: string, host_id: string): PeerHostVerify =>
  ({ alias, host_id, ok: true, self_alias, daemon_version: '1.0.0-alpha.378' })

// High-entropy so a leak into the DOM cannot be mistaken for ordinary text (spec D-8).
const SECRET_M = 'pdx_admin_secret_M_9f3k2q8w'
const SECRET_A = 'pdx_admin_secret_A_7t5r1z0x'
// Peer-host inbound tokens are `pdxp_` + 32 hex (internal/config/config.go:94):
// the prefix is what every leak assertion greps for (spec D-8, §8.4).
const TOK_A = 'pdxp_' + 'a1'.repeat(16)   // minted on A: what M presents to A
const TOK_M = 'pdxp_' + 'b2'.repeat(16)   // minted on M: what A presents to M
const TOK_R = 'pdxp_' + 'c3'.repeat(16)   // a rotated token
const X_URL = 'http://100.64.0.2:7860'
const Y_URL = 'http://100.64.0.4:7860'

const MOCKS = () => [api.fetchHostInfo, api.fetchPeerSettings, api.listPeerHosts, api.verifyPeerHost, api.updatePeerHost,
  api.addPeerHost, api.deletePeerHost, api.rotatePeerHost, api.commitRotation, api.cancelRotation, api.updatePeerSettings].map((m) => vi.mocked(m))

/** §8.4: no token value reaches a store or localStorage after any flow. */
function assertNoTokenLeak() {
  expect(JSON.stringify(useHostStore.getState())).not.toMatch(/pdxp_/)
  for (let i = 0; i < localStorage.length; i++) {
    const k = localStorage.key(i)!
    expect(k).not.toMatch(/pdxp_/)
    expect(localStorage.getItem(k) ?? '').not.toMatch(/pdxp_/)
  }
}
/** §8.4 / D-7: `force` never appears in anything the page sent. */
function assertNoForce() {
  for (const m of MOCKS()) for (const call of m.mock.calls) expect(JSON.stringify(call)).not.toMatch(/"force"/)
}
function assertNoTokenInDom() {
  expect(document.body.innerHTML).not.toMatch(/pdxp_/)
}
/** invocationCallOrder of the LAST call of `m` matching `pred` (-1 when none). */
function lastOrder<F extends (...a: never[]) => unknown>(m: F, pred: (args: Parameters<F>) => boolean = () => true): number {
  const mk = vi.mocked(m).mock
  for (let i = mk.calls.length - 1; i >= 0; i--) if (pred(mk.calls[i] as Parameters<F>)) return mk.invocationCallOrder[i]
  return -1
}

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
  // mlab's alias is derived from its host_id (the live fact, spec §2); air26's is configured.
  vi.mocked(api.fetchPeerSettings).mockImplementation(async (h) =>
    h === M ? { deliver: true, alias: 'mini-lab', alias_source: 'host_id' } : { deliver: true, alias: 'air26', alias_source: 'config' })
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
  vi.mocked(api.addPeerHost).mockReset()
  vi.mocked(api.deletePeerHost).mockReset()
  vi.mocked(api.rotatePeerHost).mockReset()
  vi.mocked(api.commitRotation).mockReset()
  vi.mocked(api.cancelRotation).mockReset()
  vi.mocked(api.updatePeerSettings).mockReset()
  localStorage.clear()
  seedHosts()
  seedFixture()
})

afterEach(() => {
  assertNoTokenLeak()
  assertNoForce()
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
    expect(within(row).getByTestId('peer-outbound-self-alias')).toHaveTextContent('air26')
    expect(within(row).getByTestId('peer-outbound-drift')).toBeInTheDocument()
    expect(within(row).getByTestId('peer-outbound-rename')).toHaveTextContent('air26')
    // mlab's self alias equals air's entry name: self alias still shown, no drift/Rename.
    expect(within(row).getByTestId('peer-inbound-self-alias')).toHaveTextContent('mini-lab')
    expect(within(row).queryByTestId('peer-inbound-drift')).toBeNull()
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

  it('a stale Rename error is cleared once a Refresh removes the drift', async () => {
    vi.mocked(api.updatePeerHost).mockRejectedValue(new HostApiError(409, 'Conflict', 'alias "air26" is already used by another host'))
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    fireEvent.click(within(row).getByTestId('peer-outbound-rename'))
    await within(row).findByTestId('peer-outbound-rename-error')
    // The alias has already been adopted some other way (e.g. from another client) — the next Refresh sees no drift.
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [{ ...AIR_ROW, alias: 'air26' }] : [MLAB_ROW]))
    fireEvent.click(screen.getByTestId('peers-refresh'))
    const row2 = await screen.findByTestId('peer-row-air26')
    await waitFor(() => expect(within(row2).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    expect(within(row2).queryByTestId('peer-outbound-rename-error')).toBeNull()
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
    expect(screen.getByTestId('peers-refresh')).toBeDisabled()
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

  it('a Rename that lands after the host changed does not restart the page (stale closure bug)', async () => {
    // updatePeerHost is held so its resolution lands strictly after the rerender below.
    let releasePut!: (v: PeerHostRow) => void
    vi.mocked(api.updatePeerHost).mockImplementation(() => new Promise<PeerHostRow>((r) => { releasePut = r }))
    const { rerender } = render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    fireEvent.click(within(row).getByTestId('peer-outbound-rename'))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(M, 'air', { alias: 'air26' }))

    rerender(<PeersSection hostId={A} />)
    await screen.findByTestId('peer-row-mini-lab')     // A's page: its own entry for mlab

    // A's own run legitimately dials verify(M, 'air') for the return direction —
    // only an EXTRA call after the stale rename resolves would indicate the bug.
    const callsToMAirBefore = vi.mocked(api.verifyPeerHost).mock.calls.filter(([h, a]) => h === M && a === 'air').length
    releasePut({ ...AIR_ROW, alias: 'air26' })
    await new Promise((r) => setTimeout(r, 0))
    const callsToMAirAfter = vi.mocked(api.verifyPeerHost).mock.calls.filter(([h, a]) => h === M && a === 'air').length
    expect(callsToMAirAfter).toBe(callsToMAirBefore)
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

  it('an in-flight Rename on one host does not leak error or busy state onto another host\'s row with the same alias', async () => {
    // M's page runs the unmodified §2.1 fixture first (so it reaches
    // bidirectional and its Rename can be held in flight). Only once we are
    // about to switch to A's page do A's own entries become the 'air' row
    // that points at a stranger host_id 'other:111111' (so A's row has no
    // counterpart and lands on outbound-only, no return line to wait on) —
    // both hosts then have a same-alias, same-drift 'air' row at the same
    // list position, which is what lets the alias-only key collide.
    let rejectPut!: (e: unknown) => void
    vi.mocked(api.updatePeerHost).mockImplementation(() => new Promise<PeerHostRow>((_resolve, reject) => { rejectPut = reject }))

    const { rerender } = render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    fireEvent.click(within(row).getByTestId('peer-outbound-rename'))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(M, 'air', { alias: 'air26' }))

    vi.mocked(api.listPeerHosts).mockImplementation(async (h) =>
      h === M ? [AIR_ROW] : [{ ...AIR_ROW, alias: 'air', host_id: 'other:111111', url: 'http://10.9.9.9:7860' }])
    vi.mocked(api.verifyPeerHost).mockImplementation(async (h, alias) =>
      h === M ? ok(alias, 'air26', 'wakes-air-2026:oa6drb') : ok('air', 'air26', 'other:111111'))

    rerender(<PeersSection hostId={A} />)
    const rowA = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(rowA).getByTestId('peer-status')).toHaveAttribute('data-status', 'outbound-only'))

    rejectPut(new HostApiError(409, 'Conflict', 'alias "air26" is already used by another host'))
    await new Promise((r) => setTimeout(r, 0))

    expect(screen.queryByTestId('peer-outbound-rename-error')).toBeNull()
    expect(screen.getByTestId('peer-outbound-rename')).toBeEnabled()
  })

  it('under StrictMode (dev double-mount) the page renders once and ends bidirectional', async () => {
    render(<StrictMode><PeersSection hostId={M} /></StrictMode>)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    expect(screen.getAllByTestId('peer-row-air')).toHaveLength(1)
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
  })
})

/* ─── D4 (spec §7, §8.4): pair / unpair / rotate ─── */

/** X (mlab) has no entries; A is connected and, unless a test says otherwise, has none either. */
function seedUnpaired() {
  vi.mocked(api.listPeerHosts).mockImplementation(async () => [])
}
/** The 201 bodies: A mints TOK_A for M's entry on it, M mints TOK_M for A's entry on it. */
function seedAdds() {
  vi.mocked(api.addPeerHost).mockImplementation(async (h, body) => h === A
    ? { alias: body.alias ?? 'mini-lab', url: body.url, host_id: 'mini-lab:278cbm', inbound_token: TOK_A, verified: true }
    : { alias: body.alias ?? 'air26', url: body.url, host_id: 'wakes-air-2026:oa6drb', inbound_token: TOK_M, verified: true })
}
const pending = (row: PeerHostRow, last: '' | 'current' | 'prev'): PeerHostRow => ({ ...row, rotation_pending: true, last_inbound_auth: last })

async function settled(status: string, alias = 'air') {
  const row = await screen.findByTestId(`peer-row-${alias}`)
  await waitFor(() => expect(within(row).getByTestId('peer-status')).toHaveAttribute('data-status', status))
  await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
  return row
}

describe('PeersSection — Pair (spec §7.1)', () => {
  it('happy path: the three calls in order with the right bodies, then the refresh shows the row (§8.4)', async () => {
    seedUnpaired()
    seedAdds()
    let paired = false
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (paired ? (h === M ? [AIR_ROW] : [MLAB_ROW]) : []))
    vi.mocked(api.updatePeerHost).mockImplementation(async () => { paired = true; return MLAB_ROW })
    render(<PeersSection hostId={M} />)
    const pairBtn = await screen.findByTestId('peers-pair-hA')
    await waitFor(() => expect(pairBtn).toBeEnabled())
    expect(screen.getByTestId('peers-pair')).toHaveTextContent('Air 2026')
    fireEvent.click(pairBtn)
    await settled('bidirectional')
    expect(api.addPeerHost).toHaveBeenCalledTimes(2)
    expect(api.addPeerHost).toHaveBeenNthCalledWith(1, A, { alias: 'mini-lab', url: X_URL })
    expect(api.addPeerHost).toHaveBeenNthCalledWith(2, M, { url: Y_URL, token: TOK_A })
    expect(api.updatePeerHost).toHaveBeenCalledWith(A, 'mini-lab', { token: TOK_M })
    const [c1, c2] = vi.mocked(api.addPeerHost).mock.invocationCallOrder
    expect(c1).toBeLessThan(c2)
    expect(c2).toBeLessThan(lastOrder(api.updatePeerHost))
    // §7.4: the refresh dialled both directions after the push
    expect(lastOrder(api.verifyPeerHost, ([h]) => h === A)).toBeGreaterThan(lastOrder(api.updatePeerHost))
    expect(api.commitRotation).not.toHaveBeenCalled()
    expect(screen.queryByTestId('peers-pair-hA')).toBeNull()
    expect(screen.getByTestId('peers-pair')).toHaveTextContent('Every other connected host is already paired.')
    assertNoTokenInDom()
  })

  it("step 2 fails → the step-1 entry on Y is deleted, the error is shown, nothing on X", async () => {
    // Y's list reflects what step 1 created, so the (not blind) undo finds it.
    let createdOnY = false
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === A && createdOnY ? [MLAB_ROW] : []))
    vi.mocked(api.addPeerHost).mockImplementation(async (h, body) => {
      if (h === M) throw new HostApiError(502, 'Bad Gateway', 'verify failed: dial tcp 100.64.0.4:7860: i/o timeout')
      createdOnY = true
      return { alias: body.alias ?? 'mini-lab', url: body.url, host_id: 'mini-lab:278cbm', inbound_token: TOK_A, verified: true }
    })
    vi.mocked(api.deletePeerHost).mockImplementation(async () => { createdOnY = false })
    render(<PeersSection hostId={M} />)
    const pairBtn = await screen.findByTestId('peers-pair-hA')
    await waitFor(() => expect(pairBtn).toBeEnabled())
    fireEvent.click(pairBtn)
    await waitFor(() => expect(api.deletePeerHost).toHaveBeenCalledWith(A, 'mini-lab'))
    const err = await screen.findByTestId('peer-flow-error')
    expect(err).toHaveTextContent('creating the entry here')
    expect(err).toHaveTextContent('dial tcp 100.64.0.4:7860: i/o timeout')
    expect(api.updatePeerHost).not.toHaveBeenCalled()
    expect(screen.queryByTestId(/^peer-row-/)).toBeNull()
    await waitFor(() => expect(screen.getByTestId('peers-pair-hA')).toBeEnabled())
    assertNoTokenInDom()
  })

  it('step 3 fails → one-way + hint; Retry return path = rotate → PUT → verify(Y→X) → list(X) → Commit (§8.4)', async () => {
    seedAdds()
    let phase: 'unpaired' | 'one-way' | 'rotated' | 'committed' = 'unpaired'
    let verifiedAfterRotate = false
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => {
      if (phase === 'unpaired') return []
      if (h === A) return [{ ...MLAB_ROW, has_token: phase === 'one-way' ? false : true }]
      if (phase === 'rotated') return [pending(AIR_ROW, verifiedAfterRotate ? 'current' : '')]
      return [AIR_ROW]
    })
    vi.mocked(api.verifyPeerHost).mockImplementation(async (h, alias) => {
      if (h === M) return ok(alias, 'air26', 'wakes-air-2026:oa6drb')
      if (phase === 'one-way') return { alias, host_id: 'mini-lab:278cbm', ok: false, error: 'no outbound token', self_alias: '', daemon_version: '' }
      if (phase === 'rotated') verifiedAfterRotate = true
      return ok(alias, 'mini-lab', 'mini-lab:278cbm')
    })
    vi.mocked(api.updatePeerHost).mockImplementation(async () => {
      if (phase === 'unpaired') { phase = 'one-way'; throw new HostApiError(502, 'Bad Gateway', 'verify failed: 401 unauthorized') }
      return MLAB_ROW
    })
    vi.mocked(api.rotatePeerHost).mockImplementation(async (_h, alias) => { phase = 'rotated'; return { alias, inbound_token: TOK_R } })
    vi.mocked(api.commitRotation).mockImplementation(async () => { phase = 'committed'; return AIR_ROW })

    render(<PeersSection hostId={M} />)
    const pairBtn = await screen.findByTestId('peers-pair-hA')
    await waitFor(() => expect(pairBtn).toBeEnabled())
    fireEvent.click(pairBtn)
    const row = await settled('one-way')
    expect(screen.getByTestId('peer-flow-hint')).toHaveTextContent('The return path was not stored')
    expect(api.deletePeerHost).not.toHaveBeenCalled()
    const retry = within(row).getByTestId('peer-inbound-rotate')
    expect(retry).toHaveTextContent('Retry return path')

    fireEvent.click(retry)
    await waitFor(() => expect(api.rotatePeerHost).toHaveBeenCalledWith(M, 'air'))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenLastCalledWith(A, 'mini-lab', { token: TOK_R }))
    const commit = await within(row).findByTestId('peer-inbound-commit')
    expect(within(row).queryByTestId('peer-inbound-cancel')).toBeNull()
    expect(api.commitRotation).not.toHaveBeenCalled()
    const oPush = lastOrder(api.updatePeerHost)
    const oVerify = lastOrder(api.verifyPeerHost, ([h, a]) => h === A && a === 'mini-lab')
    const oList = lastOrder(api.listPeerHosts, ([h]) => h === M)
    expect(oPush).toBeGreaterThan(lastOrder(api.rotatePeerHost))
    expect(oVerify).toBeGreaterThan(oPush)
    expect(oList).toBeGreaterThan(oVerify)

    fireEvent.click(commit)
    await waitFor(() => expect(api.commitRotation).toHaveBeenCalledWith(M, 'air'))
    expect(vi.mocked(api.commitRotation).mock.calls[0]).toHaveLength(2)
    expect(lastOrder(api.commitRotation)).toBeGreaterThan(oList)
    await settled('bidirectional')
    expect(within(row).queryByTestId('peer-inbound-commit')).toBeNull()
    expect(within(row).queryByText('rotation pending')).toBeNull()
    assertNoTokenInDom()
  })

  it('409 on step 1 → alias prompt; the retry uses the typed alias on Y', async () => {
    seedUnpaired()
    seedAdds()
    let conflicted = false
    vi.mocked(api.addPeerHost).mockImplementation(async (h, body) => {
      if (h === A && !conflicted) { conflicted = true; throw new HostApiError(409, 'Conflict', 'alias "mini-lab" is already used by another host; pass an explicit alias for this one') }
      return h === A
        ? { alias: body.alias ?? 'mini-lab', url: body.url, host_id: 'mini-lab:278cbm', inbound_token: TOK_A, verified: true }
        : { alias: 'air26', url: body.url, host_id: 'wakes-air-2026:oa6drb', inbound_token: TOK_M, verified: true }
    })
    vi.mocked(api.updatePeerHost).mockResolvedValue(MLAB_ROW)
    render(<PeersSection hostId={M} />)
    const pairBtn = await screen.findByTestId('peers-pair-hA')
    await waitFor(() => expect(pairBtn).toBeEnabled())
    fireEvent.click(pairBtn)
    const input = await screen.findByTestId('peers-alias-input-hA')
    expect(screen.getByTestId('peers-pair')).toHaveTextContent('Air 2026 already uses that alias')
    expect(api.addPeerHost).toHaveBeenCalledTimes(1)
    expect(api.deletePeerHost).not.toHaveBeenCalled()
    fireEvent.change(input, { target: { value: 'mlab-2' } })
    fireEvent.click(screen.getByTestId('peers-alias-retry-hA'))
    await waitFor(() => expect(api.addPeerHost).toHaveBeenCalledWith(A, { alias: 'mlab-2', url: X_URL }))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(A, 'mlab-2', { token: TOK_M }))
    assertNoTokenInDom()
  })

  it("a candidate whose entries could not be read is listed with Pair disabled (codex F4)", async () => {
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => {
      if (h === A) throw new HostApiError(500, 'Internal Server Error', 'config locked')
      return []
    })
    render(<PeersSection hostId={M} />)
    const pairBtn = await screen.findByTestId('peers-pair-hA')
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
    expect(pairBtn).toBeDisabled()
    expect(screen.getByTestId('peers-cand-hA')).toHaveTextContent("Air 2026's entries could not be read (config locked)")
  })

  it('the repair case is noted; Pair rotates the existing entry on Y, and commits it only after its own dial + fresh read', async () => {
    seedAdds()
    let rotated = false
    let committed = false
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => {
      if (h === M) return committed ? [{ ...AIR_ROW, alias: 'air26' }] : []
      if (!rotated || committed) return [MLAB_ROW]
      // Y's row says `current` only once X has dialled Y with the new token.
      const dialled = vi.mocked(api.verifyPeerHost).mock.calls.some(([hh, a]) => hh === M && a === 'air26')
      return [pending(MLAB_ROW, dialled ? 'current' : '')]
    })
    vi.mocked(api.rotatePeerHost).mockImplementation(async () => { rotated = true; return { alias: 'mini-lab', inbound_token: TOK_R } })
    vi.mocked(api.updatePeerHost).mockResolvedValue(pending(MLAB_ROW, ''))
    // A real commit answers with the row still noting X's last dial on the (now only) current token.
    vi.mocked(api.commitRotation).mockImplementation(async () => { committed = true; return { ...MLAB_ROW, last_inbound_auth: 'current' } })
    render(<PeersSection hostId={M} />)
    const pairBtn = await screen.findByTestId('peers-pair-hA')
    await waitFor(() => expect(pairBtn).toBeEnabled())
    expect(screen.getByTestId('peers-cand-hA')).toHaveTextContent('Air 2026 already has an entry for this host')
    fireEvent.click(pairBtn)
    await settled('bidirectional', 'air26')
    expect(api.rotatePeerHost).toHaveBeenCalledWith(A, 'mini-lab')
    expect(api.addPeerHost).toHaveBeenCalledTimes(1)
    expect(api.addPeerHost).toHaveBeenCalledWith(M, { url: Y_URL, token: TOK_R })
    expect(api.updatePeerHost).toHaveBeenCalledWith(A, 'mini-lab', { token: TOK_M })
    expect(api.commitRotation).toHaveBeenCalledWith(A, 'mini-lab')
    expect(vi.mocked(api.commitRotation).mock.calls[0]).toHaveLength(2)
    expect(screen.queryByTestId('peer-flow-error')).toBeNull()
    assertNoTokenInDom()
  })

  it("repair path whose commit is refused → the new row carries the error and offers the button by the rule (repair-pending)", async () => {
    seedAdds()
    let rotated = false
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => {
      if (h === M) return rotated ? [{ ...AIR_ROW, alias: 'air26' }] : []
      if (!rotated) return [MLAB_ROW]
      const dialled = vi.mocked(api.verifyPeerHost).mock.calls.some(([hh, a]) => hh === M && a === 'air26')
      return [pending(MLAB_ROW, dialled ? 'current' : '')]
    })
    vi.mocked(api.rotatePeerHost).mockImplementation(async () => { rotated = true; return { alias: 'mini-lab', inbound_token: TOK_R } })
    vi.mocked(api.updatePeerHost).mockResolvedValue(pending(MLAB_ROW, ''))
    vi.mocked(api.commitRotation).mockRejectedValue(new HostApiError(409, 'Conflict', 'rotation unconfirmed'))
    render(<PeersSection hostId={M} />)
    const pairBtn = await screen.findByTestId('peers-pair-hA')
    await waitFor(() => expect(pairBtn).toBeEnabled())
    fireEvent.click(pairBtn)
    const row = await settled('bidirectional', 'air26')
    expect(api.commitRotation).toHaveBeenCalledTimes(1)
    // The note re-homed to the row the pair created, and the outbound line (Y's entry) offers Commit by the rule.
    const err = within(row).getByTestId('peer-flow-error')
    expect(err).toHaveTextContent('committing')
    expect(err).toHaveTextContent('rotation unconfirmed')
    expect(within(row).getByTestId('peer-outbound-commit')).toBeInTheDocument()
    expect(screen.queryByTestId('peers-cand-hA')).toBeNull()
    assertNoTokenInDom()
  })

  it.each([
    ['current', 'commit', 'cancel'],
    ['prev', 'cancel', 'commit'],
  ] as const)("a repair candidate with a pending rotation (%s) blocks Pair and offers %s as of the peer's last dial (codex F3)", async (last, present, absent) => {
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === A ? [pending(MLAB_ROW, last)] : []))
    vi.mocked(api.commitRotation).mockResolvedValue(MLAB_ROW)
    vi.mocked(api.cancelRotation).mockResolvedValue(MLAB_ROW)
    render(<PeersSection hostId={M} />)
    const pairBtn = await screen.findByTestId('peers-pair-hA')
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
    expect(pairBtn).toBeDisabled()
    const cand = screen.getByTestId('peers-cand-hA')
    expect(cand).toHaveTextContent('has a rotation pending')
    expect(cand).toHaveTextContent("(as of the peer's last dial)")
    expect(within(cand).queryByTestId(`peers-cand-hA-${absent}`)).toBeNull()
    expect(within(cand).queryByTestId('peers-cand-hA-rotate')).toBeNull()
    const listsBefore = vi.mocked(api.listPeerHosts).mock.calls.length
    fireEvent.click(within(cand).getByTestId(`peers-cand-hA-${present}`))
    const fn = present === 'commit' ? api.commitRotation : api.cancelRotation
    await waitFor(() => expect(fn).toHaveBeenCalledWith(A, 'mini-lab'))
    expect(vi.mocked(fn).mock.calls[0]).toHaveLength(2)
    await waitFor(() => expect(vi.mocked(api.listPeerHosts).mock.calls.length).toBeGreaterThan(listsBefore))
  })

  it("a repair candidate pending with no evidence ('') blocks Pair and offers neither button", async () => {
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === A ? [pending(MLAB_ROW, '')] : []))
    render(<PeersSection hostId={M} />)
    const pairBtn = await screen.findByTestId('peers-pair-hA')
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
    expect(pairBtn).toBeDisabled()
    const cand = screen.getByTestId('peers-cand-hA')
    expect(cand).toHaveTextContent('the peer has not dialled since the rotation')
    expect(within(cand).queryByTestId('peers-cand-hA-commit')).toBeNull()
    expect(within(cand).queryByTestId('peers-cand-hA-cancel')).toBeNull()
  })
})

describe('PeersSection — Unpair (spec §7.2)', () => {
  it('the dialog names both sides; confirm deletes both entries and refreshes', async () => {
    let gone = false
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (gone ? [] : h === M ? [AIR_ROW] : [MLAB_ROW]))
    vi.mocked(api.deletePeerHost).mockImplementation(async (h) => { if (h === A) gone = true })
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    fireEvent.click(within(row).getByTestId('peer-unpair-air'))
    const dlg = await screen.findByTestId('peer-unpair-dialog')
    expect(dlg).toHaveTextContent('Unpair mlab and Air 2026?')
    expect(dlg).toHaveTextContent("Deletes mlab's entry “air” and Air 2026's entry “mini-lab”")
    fireEvent.click(screen.getByTestId('peer-unpair-confirm'))
    await waitFor(() => expect(api.deletePeerHost).toHaveBeenCalledWith(M, 'air'))
    await waitFor(() => expect(api.deletePeerHost).toHaveBeenCalledWith(A, 'mini-lab'))
    await screen.findByTestId('peers-empty')
    expect(screen.queryByTestId('peer-unpair-dialog')).toBeNull()
    expect(screen.queryByTestId('peer-flow-error')).toBeNull()
  })

  it('Cancel closes the dialog without deleting', async () => {
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    fireEvent.click(within(row).getByTestId('peer-unpair-air'))
    await screen.findByTestId('peer-unpair-dialog')
    fireEvent.click(screen.getByTestId('peer-unpair-cancel'))
    expect(screen.queryByTestId('peer-unpair-dialog')).toBeNull()
    expect(api.deletePeerHost).not.toHaveBeenCalled()
  })

  it('a 404 on either side is already done: no error', async () => {
    let gone = false
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (gone ? [] : h === M ? [AIR_ROW] : [MLAB_ROW]))
    vi.mocked(api.deletePeerHost).mockImplementation(async (h) => {
      if (h === A) { gone = true; throw new HostApiError(404, 'Not Found', 'unknown alias') }
    })
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    fireEvent.click(within(row).getByTestId('peer-unpair-air'))
    fireEvent.click(await screen.findByTestId('peer-unpair-confirm'))
    await screen.findByTestId('peers-empty')
    expect(api.deletePeerHost).toHaveBeenCalledTimes(2)
    expect(screen.queryByTestId('peer-flow-error')).toBeNull()
  })

  it('a real failure on Y is shown while X was still deleted', async () => {
    vi.mocked(api.deletePeerHost).mockImplementation(async (h) => {
      if (h === A) throw new HostApiError(500, 'Internal Server Error', 'config locked')
    })
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    fireEvent.click(within(row).getByTestId('peer-unpair-air'))
    fireEvent.click(await screen.findByTestId('peer-unpair-confirm'))
    await waitFor(() => expect(api.deletePeerHost).toHaveBeenCalledWith(M, 'air'))
    await waitFor(() => expect(api.deletePeerHost).toHaveBeenCalledWith(A, 'mini-lab'))
    expect(await screen.findByTestId('peer-flow-error')).toHaveTextContent('config locked')
  })

  it('a non-App counterpart: only X is deleted and the body says its side is left as is', async () => {
    vi.mocked(api.listPeerHosts).mockImplementation(async () => [{ ...AIR_ROW, alias: 'stranger', host_id: 'stranger:aaaaaa', url: 'http://10.0.0.1:7860' }])
    vi.mocked(api.verifyPeerHost).mockResolvedValue(ok('stranger', 'strange-self', 'stranger:aaaaaa'))
    vi.mocked(api.deletePeerHost).mockResolvedValue(undefined)
    render(<PeersSection hostId={M} />)
    const row = await settled('outbound-only', 'stranger')
    fireEvent.click(within(row).getByTestId('peer-unpair-stranger'))
    const dlg = await screen.findByTestId('peer-unpair-dialog')
    expect(dlg).toHaveTextContent("Deletes mlab's entry “stranger”. stranger is not a host in this App, so its side is left as is.")
    fireEvent.click(screen.getByTestId('peer-unpair-confirm'))
    await waitFor(() => expect(api.deletePeerHost).toHaveBeenCalledWith(M, 'stranger'))
    await waitFor(() => expect(vi.mocked(api.listPeerHosts).mock.calls.length).toBeGreaterThan(1))
    expect(api.deletePeerHost).toHaveBeenCalledTimes(1)
  })
})

describe('PeersSection — Rotate (spec §7.3, §6.4, §8.4)', () => {
  /**
   * The §8.4 stateful fixture. M's row is pending once `rotate` ran; what it
   * reads as `last_inbound_auth` is whatever the PEER'S LAST DIAL presented:
   * `afterDial` once verify(A, 'mini-lab') has been called after the rotate,
   * `beforeDial` until then. The push's own verify (Y→X with the new token)
   * is the daemon's business; here the mock PUT decides whether it counts.
   */
  function seedRotation(opts: { beforeDial: '' | 'current' | 'prev'; afterDial: '' | 'current' | 'prev'; push: 'ok' | 'fail' }) {
    let rotated = false
    let dialled = false
    let committed = false
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => {
      if (h === A) return [MLAB_ROW]
      if (!rotated || committed) return [AIR_ROW]
      return [pending(AIR_ROW, dialled ? opts.afterDial : opts.beforeDial)]
    })
    vi.mocked(api.verifyPeerHost).mockImplementation(async (h, alias) => {
      if (h === A && alias === 'mini-lab' && rotated) dialled = true
      return h === M ? ok(alias, 'air26', 'wakes-air-2026:oa6drb') : ok(alias, 'mini-lab', 'mini-lab:278cbm')
    })
    vi.mocked(api.rotatePeerHost).mockImplementation(async (_h, alias) => { rotated = true; return { alias, inbound_token: TOK_R } })
    vi.mocked(api.updatePeerHost).mockImplementation(async () => {
      if (opts.push === 'fail') throw new HostApiError(409, 'Conflict', 'entry changed concurrently')
      return MLAB_ROW
    })
    vi.mocked(api.commitRotation).mockImplementation(async () => { committed = true; return AIR_ROW })
    vi.mocked(api.cancelRotation).mockImplementation(async () => { committed = true; return AIR_ROW })
  }

  it('inbound line: mint → push → verify(Y→X) → list(X) → Commit offered only from the fresh row → commit(X, alias) with two args', async () => {
    seedRotation({ beforeDial: '', afterDial: 'current', push: 'ok' })
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    expect(within(row).queryByTestId('peer-inbound-commit')).toBeNull()
    const rotate = within(row).getByTestId('peer-inbound-rotate')
    expect(rotate).toHaveTextContent('Rotate token')
    fireEvent.click(rotate)
    await waitFor(() => expect(api.rotatePeerHost).toHaveBeenCalledWith(M, 'air'))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(A, 'mini-lab', { token: TOK_R }))
    const commit = await within(row).findByTestId('peer-inbound-commit')
    expect(within(row).queryByTestId('peer-inbound-cancel')).toBeNull()
    expect(within(row).queryByTestId('peer-inbound-rotate')).toBeNull()
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('rotation pending')
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('the peer is on the new token')
    expect(screen.getByTestId('peer-flow-hint')).toHaveTextContent('Token pushed')
    expect(api.commitRotation).not.toHaveBeenCalled()
    // §8.4 call order: mint → push → verify(Y→X) → list(X)
    const oMint = lastOrder(api.rotatePeerHost)
    const oPush = lastOrder(api.updatePeerHost)
    const oVerify = lastOrder(api.verifyPeerHost, ([h, a]) => h === A && a === 'mini-lab')
    const oList = lastOrder(api.listPeerHosts, ([h]) => h === M)
    expect(oMint).toBeLessThan(oPush)
    expect(oPush).toBeLessThan(oVerify)
    expect(oVerify).toBeLessThan(oList)
    assertNoTokenInDom()

    fireEvent.click(commit)
    await waitFor(() => expect(api.commitRotation).toHaveBeenCalledWith(M, 'air'))
    expect(vi.mocked(api.commitRotation).mock.calls[0]).toHaveLength(2)
    expect(lastOrder(api.commitRotation)).toBeGreaterThan(oList)
    await waitFor(() => expect(within(row).queryByTestId('peer-inbound-commit')).toBeNull())
    await settled('bidirectional')
    expect(within(row).getByTestId('peer-inbound')).not.toHaveTextContent('rotation pending')
    expect(within(row).getByTestId('peer-inbound-rotate')).toBeInTheDocument()
    assertNoTokenInDom()
  })

  it("push failed after its verify dial → the re-verify makes the peer present the old token → Cancel only, Commit absent, commit never sent (the §8.4 mutation fixture)", async () => {
    // Y verified with the new token (X recorded 'current') and then failed to
    // persist it: the row reads 'current' until the page's own dial, after
    // which Y presents the old token → 'prev'. A page deciding from its push
    // memory, or reading the row before the dial (M-A/M-B), offers Commit here.
    seedRotation({ beforeDial: 'current', afterDial: 'prev', push: 'fail' })
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    fireEvent.click(within(row).getByTestId('peer-inbound-rotate'))
    const cancel = await within(row).findByTestId('peer-inbound-cancel')
    expect(within(row).queryByTestId('peer-inbound-commit')).toBeNull()
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('the peer is still presenting the old token')
    expect(screen.getByTestId('peer-flow-error')).toHaveTextContent('entry changed concurrently')
    expect(api.commitRotation).not.toHaveBeenCalled()
    expect(lastOrder(api.listPeerHosts, ([h]) => h === M)).toBeGreaterThan(lastOrder(api.verifyPeerHost, ([h]) => h === A))
    assertNoTokenInDom()
    fireEvent.click(cancel)
    await waitFor(() => expect(api.cancelRotation).toHaveBeenCalledWith(M, 'air'))
    expect(vi.mocked(api.cancelRotation).mock.calls[0]).toHaveLength(2)
    expect(api.commitRotation).not.toHaveBeenCalled()
  })

  it.each([
    ['current', 'commit', 'the peer is on the new token'],
    ['prev', 'cancel', 'the peer is still presenting the old token'],
    ['', null, 'the peer has not dialled since the rotation'],
  ] as const)("reload with rotation_pending (%s): no button before the post-dial re-read, then %s", async (last, button, note) => {
    // The return dial is parked: the pre-dial paint shows the badge and no button.
    let release!: (v: PeerHostVerify) => void
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [pending(AIR_ROW, last)] : [MLAB_ROW]))
    vi.mocked(api.verifyPeerHost).mockImplementation((h, alias) =>
      h === M ? Promise.resolve(ok(alias, 'air26', 'wakes-air-2026:oa6drb')) : new Promise<PeerHostVerify>((r) => { release = r }))
    render(<PeersSection hostId={M} />)
    const row = await screen.findByTestId('peer-row-air')
    await waitFor(() => expect(within(row).getByTestId('peer-outbound')).toHaveAttribute('data-ok', 'true'))
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('rotation pending')
    expect(within(row).queryByTestId('peer-inbound-commit')).toBeNull()
    expect(within(row).queryByTestId('peer-inbound-cancel')).toBeNull()
    expect(vi.mocked(api.listPeerHosts).mock.calls.filter(([h]) => h === M)).toHaveLength(1)
    release(ok('mini-lab', 'mini-lab', 'mini-lab:278cbm'))
    await settled('bidirectional')
    expect(vi.mocked(api.listPeerHosts).mock.calls.filter(([h]) => h === M)).toHaveLength(2)
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent(note)
    for (const b of ['commit', 'cancel'] as const) {
      if (b === button) expect(within(row).getByTestId(`peer-inbound-${b}`)).toBeInTheDocument()
      else expect(within(row).queryByTestId(`peer-inbound-${b}`)).toBeNull()
    }
    expect(within(row).queryByTestId('peer-inbound-rotate')).toBeNull()
    expect(within(row).getByTestId('peer-inbound')).not.toHaveTextContent("as of the peer's last dial")
  })

  it("both sides pending, Y's re-list fails → outbound line stale with no button, inbound line has its button (codex F5)", async () => {
    let listsA = 0
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => {
      if (h === M) return [pending(AIR_ROW, vi.mocked(api.verifyPeerHost).mock.calls.some(([hh, a]) => hh === A && a === 'mini-lab') ? 'prev' : '')]
      if (++listsA >= 2) throw new HostApiError(500, 'Internal Server Error', 'HTTP 500')
      return [pending(MLAB_ROW, '')]
    })
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    const out = within(row).getByTestId('peer-outbound')
    expect(out).toHaveTextContent('rotation pending')
    expect(out).toHaveTextContent('could not re-read after the dial')
    expect(within(row).queryByTestId('peer-outbound-commit')).toBeNull()
    expect(within(row).queryByTestId('peer-outbound-cancel')).toBeNull()
    expect(within(row).getByTestId('peer-inbound-cancel')).toBeInTheDocument()
    expect(within(row).getByTestId('peer-inbound')).not.toHaveTextContent('could not re-read')
  })

  it('a Commit answered 409 shows the daemon text inline and re-reads the row', async () => {
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [pending(AIR_ROW, 'current')] : [MLAB_ROW]))
    vi.mocked(api.commitRotation).mockRejectedValue(new HostApiError(409, 'Conflict', 'rotation unconfirmed'))
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    const lists = vi.mocked(api.listPeerHosts).mock.calls.length
    fireEvent.click(within(row).getByTestId('peer-inbound-commit'))
    expect(await within(row).findByTestId('peer-inbound-gate-error')).toHaveTextContent('rotation unconfirmed')
    await waitFor(() => expect(vi.mocked(api.listPeerHosts).mock.calls.length).toBeGreaterThan(lists))
    expect(api.commitRotation).toHaveBeenCalledWith(M, 'air')
  })

  it('a Cancel answered 409 shows the daemon text inline and re-reads the row (codex F6)', async () => {
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [pending(AIR_ROW, 'prev')] : [MLAB_ROW]))
    vi.mocked(api.cancelRotation).mockRejectedValue(new HostApiError(409, 'Conflict', 'rotation unconfirmed'))
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    const lists = vi.mocked(api.listPeerHosts).mock.calls.length
    fireEvent.click(within(row).getByTestId('peer-inbound-cancel'))
    expect(await within(row).findByTestId('peer-inbound-gate-error')).toHaveTextContent('rotation unconfirmed')
    await waitFor(() => expect(vi.mocked(api.listPeerHosts).mock.calls.length).toBeGreaterThan(lists))
    expect(api.cancelRotation).toHaveBeenCalledWith(M, 'air')
    expect(api.commitRotation).not.toHaveBeenCalled()
  })

  it('Commit holds the page lock: while it is in flight, Unpair, Rotate, Refresh and Rename are disabled and a second click is ignored (codex D4b A-2)', async () => {
    // Both sides pending so the outbound line has a gate button too; a drift on the outbound line gives a Rename button.
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [pending(AIR_ROW, 'current')] : [pending(MLAB_ROW, 'current')]))
    vi.mocked(api.verifyPeerHost).mockImplementation(async (h, alias) =>
      h === M ? ok(alias, 'air-2026', 'wakes-air-2026:oa6drb') : ok(alias, 'mini-lab', 'mini-lab:278cbm'))
    let releaseCommit!: (v: PeerHostRow) => void
    vi.mocked(api.commitRotation).mockImplementation(() => new Promise<PeerHostRow>((r) => { releaseCommit = r }))
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    expect(within(row).getByTestId('peer-outbound-rename')).toBeEnabled()
    fireEvent.click(within(row).getByTestId('peer-inbound-commit'))
    await waitFor(() => expect(api.commitRotation).toHaveBeenCalledTimes(1))
    // Everything that writes is locked while the commit is out.
    expect(within(row).getByTestId('peer-inbound-commit')).toBeDisabled()
    expect(within(row).getByTestId('peer-outbound-commit')).toBeDisabled()
    expect(within(row).getByTestId('peer-unpair-air')).toBeDisabled()
    expect(within(row).getByTestId('peer-outbound-rename')).toBeDisabled()
    expect(screen.getByTestId('peers-refresh')).toBeDisabled()
    // A second click on the other line's gate (even bypassing `disabled`) is ignored by the lock.
    fireEvent.click(within(row).getByTestId('peer-outbound-commit'))
    fireEvent.click(within(row).getByTestId('peer-unpair-air'))
    expect(api.commitRotation).toHaveBeenCalledTimes(1)
    expect(api.deletePeerHost).not.toHaveBeenCalled()
    expect(screen.queryByTestId('peer-unpair-dialog')).toBeNull()
    releaseCommit(AIR_ROW)
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
  })

  it("a flow started on one host whose step lands after the page moved to another host neither locks nor repaints the new page (codex D4b A-1)", async () => {
    // rotate on M parks; the page moves to A; then the parked rotate resolves and the old flow reports its next step.
    let releaseRotate!: (v: { alias: string; inbound_token: string }) => void
    vi.mocked(api.rotatePeerHost).mockImplementation(() => new Promise((r) => { releaseRotate = r }))
    vi.mocked(api.updatePeerHost).mockResolvedValue(MLAB_ROW)
    const { rerender } = render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    fireEvent.click(within(row).getByTestId('peer-inbound-rotate'))
    await waitFor(() => expect(api.rotatePeerHost).toHaveBeenCalledWith(M, 'air'))
    expect(screen.getByTestId('peers-refresh')).toBeDisabled()

    rerender(<PeersSection hostId={A} />)
    const aRow = await screen.findByTestId('peer-row-mini-lab')
    await waitFor(() => expect(within(aRow).getByTestId('peer-status')).toHaveAttribute('data-status', 'bidirectional'))
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())   // A's page is not locked by M's flow

    const listsBefore = vi.mocked(api.listPeerHosts).mock.calls.length
    releaseRotate({ alias: 'air', inbound_token: TOK_R })                             // M's flow now reports 'push' and finishes
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(A, 'mini-lab', { token: TOK_R }))
    await new Promise((r) => setTimeout(r, 20))
    expect(screen.getByTestId('peers-refresh')).toBeEnabled()                         // the late report did not lock A's page
    expect(screen.queryByTestId('peer-flow-step')).toBeNull()                          // and painted no step on it
    expect(vi.mocked(api.listPeerHosts).mock.calls.length).toBe(listsBefore)           // and did not restart A's page
    expect(screen.queryByTestId('peer-row-air')).toBeNull()
    assertNoTokenInDom()
  })

  it("a non-App counterpart: a CLI-started rotation gets the button by the rule as of the peer's last dial; Rotate is absent with the tooltip", async () => {
    let committed = false
    vi.mocked(api.listPeerHosts).mockImplementation(async () => [committed
      ? { ...AIR_ROW, alias: 'stranger', host_id: 'stranger:aaaaaa', url: 'http://10.0.0.1:7860' }
      : pending({ ...AIR_ROW, alias: 'stranger', host_id: 'stranger:aaaaaa', url: 'http://10.0.0.1:7860' }, 'current')])
    vi.mocked(api.verifyPeerHost).mockResolvedValue(ok('stranger', 'strange-self', 'stranger:aaaaaa'))
    vi.mocked(api.commitRotation).mockImplementation(async () => { committed = true; return AIR_ROW })
    render(<PeersSection hostId={M} />)
    const row = await settled('outbound-only', 'stranger')
    const line = within(row).getByTestId('peer-inbound')
    expect(line).toHaveTextContent('rotation pending')
    expect(line).toHaveTextContent("(as of the peer's last dial)")
    expect(within(row).queryByTestId('peer-inbound-rotate')).toBeNull()
    expect(within(row).queryByTestId('peer-inbound-cancel')).toBeNull()
    fireEvent.click(within(row).getByTestId('peer-inbound-commit'))
    await waitFor(() => expect(api.commitRotation).toHaveBeenCalledWith(M, 'stranger'))
    await waitFor(() => expect(within(row).queryByTestId('peer-inbound-commit')).toBeNull())
    await settled('outbound-only', 'stranger')
    const unavailable = within(row).getByTestId('peer-inbound-rotate-unavailable')
    expect(unavailable).toBeDisabled()
    expect(unavailable).toHaveAttribute('title', 'Rotation needs the counterpart to be a host in this App')
    expect(within(row).queryByTestId('peer-inbound-rotate')).toBeNull()
  })

  it('outbound line: rotate on Y, push to X, verify(X→Y), list(Y) → Commit → commit(Y, alias)', async () => {
    let rotated = false
    let committed = false
    let dialled = false
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => {
      if (h === M) return [AIR_ROW]
      return rotated && !committed ? [pending(MLAB_ROW, dialled ? 'current' : '')] : [MLAB_ROW]
    })
    vi.mocked(api.verifyPeerHost).mockImplementation(async (h, alias) => {
      if (h === M && alias === 'air' && rotated) dialled = true
      return h === M ? ok(alias, 'air26', 'wakes-air-2026:oa6drb') : ok(alias, 'mini-lab', 'mini-lab:278cbm')
    })
    vi.mocked(api.rotatePeerHost).mockImplementation(async (_h, alias) => { rotated = true; return { alias, inbound_token: TOK_R } })
    vi.mocked(api.updatePeerHost).mockResolvedValue(AIR_ROW)
    vi.mocked(api.commitRotation).mockImplementation(async () => { committed = true; return MLAB_ROW })
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    fireEvent.click(within(row).getByTestId('peer-outbound-rotate'))
    await waitFor(() => expect(api.rotatePeerHost).toHaveBeenCalledWith(A, 'mini-lab'))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(M, 'air', { token: TOK_R }))
    const commit = await within(row).findByTestId('peer-outbound-commit')
    expect(within(row).queryByTestId('peer-outbound-cancel')).toBeNull()
    expect(within(row).queryByTestId('peer-inbound-commit')).toBeNull()
    const oPush = lastOrder(api.updatePeerHost)
    const oVerify = lastOrder(api.verifyPeerHost, ([h, a]) => h === M && a === 'air')
    const oList = lastOrder(api.listPeerHosts, ([h]) => h === A)
    expect(oPush).toBeGreaterThan(lastOrder(api.rotatePeerHost))
    expect(oVerify).toBeGreaterThan(oPush)
    expect(oList).toBeGreaterThan(oVerify)
    expect(api.commitRotation).not.toHaveBeenCalled()
    fireEvent.click(commit)
    await waitFor(() => expect(api.commitRotation).toHaveBeenCalledWith(A, 'mini-lab'))
    expect(vi.mocked(api.commitRotation).mock.calls[0]).toHaveLength(2)
    await waitFor(() => expect(within(row).queryByTestId('peer-outbound-commit')).toBeNull())
    assertNoTokenInDom()
  })

  it('Create return entry: X has the entry, Y has none → rotate on X, POST on Y with the token, verify(Y→X), list(X) → Commit', async () => {
    let rotated = false
    let created = false
    let dialled = false
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => {
      if (h === A) return created ? [MLAB_ROW] : []
      return rotated ? [pending(AIR_ROW, dialled ? 'current' : '')] : [AIR_ROW]
    })
    vi.mocked(api.verifyPeerHost).mockImplementation(async (h, alias) => {
      if (h === A && alias === 'mini-lab' && rotated) dialled = true
      return h === M ? ok(alias, 'air26', 'wakes-air-2026:oa6drb') : ok(alias, 'mini-lab', 'mini-lab:278cbm')
    })
    vi.mocked(api.rotatePeerHost).mockImplementation(async (_h, alias) => { rotated = true; return { alias, inbound_token: TOK_R } })
    vi.mocked(api.addPeerHost).mockImplementation(async (_h, body) => {
      created = true
      return { alias: body.alias ?? 'mini-lab', url: body.url, host_id: 'mini-lab:278cbm', inbound_token: TOK_A, verified: true }
    })
    render(<PeersSection hostId={M} />)
    const row = await settled('one-way')
    expect(within(row).getByTestId('peer-inbound')).toHaveTextContent('no entry for “mini-lab” on Air 2026')
    const create = within(row).getByTestId('peer-inbound-rotate')
    expect(create).toHaveTextContent('Create return entry')
    fireEvent.click(create)
    await waitFor(() => expect(api.rotatePeerHost).toHaveBeenCalledWith(M, 'air'))
    await waitFor(() => expect(api.addPeerHost).toHaveBeenCalledWith(A, { alias: 'mini-lab', url: X_URL, token: TOK_R }))
    await within(row).findByTestId('peer-inbound-commit')
    expect(api.updatePeerHost).not.toHaveBeenCalled()
    const oPush = lastOrder(api.addPeerHost)
    const oVerify = lastOrder(api.verifyPeerHost, ([h, a]) => h === A && a === 'mini-lab')
    const oList = lastOrder(api.listPeerHosts, ([h]) => h === M)
    expect(oVerify).toBeGreaterThan(oPush)
    expect(oList).toBeGreaterThan(oVerify)
    await settled('bidirectional')
    assertNoTokenInDom()
  })

  it('a disconnected App counterpart: Rotate is absent with the tooltip', async () => {
    seedHosts('disconnected')
    render(<PeersSection hostId={M} />)
    const row = await settled('return-unknown')
    expect(within(row).queryByTestId('peer-inbound-rotate')).toBeNull()
    expect(within(row).getByTestId('peer-inbound-rotate-unavailable')).toHaveAttribute('title', 'Rotation needs the counterpart to be a host in this App')
    expect(within(row).queryByTestId('peer-outbound-rotate')).toBeNull()
  })

  it('a rotate that fails to mint leaves the row unchanged and shows the error', async () => {
    vi.mocked(api.rotatePeerHost).mockRejectedValue(new HostApiError(409, 'Conflict', 'rotation already pending'))
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    fireEvent.click(within(row).getByTestId('peer-inbound-rotate'))
    expect(await screen.findByTestId('peer-flow-error')).toHaveTextContent('rotation already pending')
    expect(api.updatePeerHost).not.toHaveBeenCalled()
    await settled('bidirectional')
    expect(within(row).getByTestId('peer-inbound-rotate')).toBeEnabled()
  })
})

describe('PeersSection — self alias (#1196, spec §4.3)', () => {
  const TOO_OLD = 'older than alpha.399'

  /** A settings fake whose answer follows what the page wrote — the refresh after Save/Clear must show it. */
  function seedSettings(alias: string, source: 'config' | 'host_id') {
    const live = { alias, source }
    vi.mocked(api.fetchPeerSettings).mockImplementation(async (h) =>
      h === M ? { deliver: true, alias: live.alias, alias_source: live.source } : { deliver: true, alias: 'air26', alias_source: 'config' })
    vi.mocked(api.updatePeerSettings).mockImplementation(async (_h, patch) => {
      if (patch.alias === '') { live.alias = 'mini-lab'; live.source = 'host_id' }
      else if (patch.alias !== undefined) { live.alias = patch.alias; live.source = 'config' }
      return { deliver: true, alias: live.alias, alias_source: live.source }
    })
    return live
  }
  const settingsCalls = () => vi.mocked(api.fetchPeerSettings).mock.calls.filter(([h]) => h === M).length

  it('a derived alias shows "(from host_id)", offers Edit and no Clear; the D2 line text is unchanged', async () => {
    render(<PeersSection hostId={M} />)
    await settled('bidirectional')
    const self = screen.getByTestId('peers-self')
    expect(self).toHaveTextContent('mlab · self alias: mini-lab · mini-lab:278cbm')
    expect(self).toHaveTextContent('(from host_id)')
    expect(screen.getByTestId('peers-self-edit')).toBeEnabled()
    expect(screen.queryByTestId('peers-self-clear')).toBeNull()
    expect(screen.queryByTestId('peers-self-input')).toBeNull()
    expect(screen.queryByTestId('peers-self-too-old')).toBeNull()
  })

  it('Edit opens the input prefilled; Save is disabled on empty, enabled on the same string while the alias is derived (§6: pinning it into config is a real write)', async () => {
    render(<PeersSection hostId={M} />)
    await settled('bidirectional')
    fireEvent.click(screen.getByTestId('peers-self-edit'))
    const input = screen.getByTestId('peers-self-input') as HTMLInputElement
    expect(input.value).toBe('mini-lab')
    expect(screen.getByTestId('peers-self-save')).toBeEnabled()
    fireEvent.change(input, { target: { value: '' } })
    expect(screen.getByTestId('peers-self-save')).toBeDisabled()
    fireEvent.change(input, { target: { value: 'mlab' } })
    expect(screen.getByTestId('peers-self-save')).toBeEnabled()
    expect(screen.getByText(/Refs do not change/)).toBeInTheDocument()   // peers.self_alias_note (S-4)
    fireEvent.click(screen.getByTestId('peers-self-cancel'))
    expect(screen.queryByTestId('peers-self-input')).toBeNull()
    expect(api.updatePeerSettings).not.toHaveBeenCalled()
  })

  it('a configured alias: Save is disabled while the value equals it (a no-op write), Clear is offered', async () => {
    seedSettings('mlab', 'config')
    render(<PeersSection hostId={M} />)
    await settled('bidirectional')
    expect(screen.getByTestId('peers-self')).not.toHaveTextContent('(from host_id)')
    expect(screen.getByTestId('peers-self-clear')).toBeEnabled()
    fireEvent.click(screen.getByTestId('peers-self-edit'))
    expect((screen.getByTestId('peers-self-input') as HTMLInputElement).value).toBe('mlab')
    expect(screen.getByTestId('peers-self-save')).toBeDisabled()
    fireEvent.change(screen.getByTestId('peers-self-input'), { target: { value: 'mlab2' } })
    expect(screen.getByTestId('peers-self-save')).toBeEnabled()
  })

  it('Save → updatePeerSettings(hM, {alias:"mlab"}) exactly, then the refresh re-reads settings and the line shows the new alias', async () => {
    seedSettings('mini-lab', 'host_id')
    render(<PeersSection hostId={M} />)
    await settled('bidirectional')
    const before = settingsCalls()
    fireEvent.click(screen.getByTestId('peers-self-edit'))
    fireEvent.change(screen.getByTestId('peers-self-input'), { target: { value: 'mlab' } })
    fireEvent.click(screen.getByTestId('peers-self-save'))
    await waitFor(() => expect(api.updatePeerSettings).toHaveBeenCalledTimes(1))
    expect(api.updatePeerSettings).toHaveBeenCalledWith(M, { alias: 'mlab' })
    expect(Object.keys(vi.mocked(api.updatePeerSettings).mock.calls[0][1])).toEqual(['alias'])
    await waitFor(() => expect(settingsCalls()).toBeGreaterThan(before))
    await settled('bidirectional')
    const self = screen.getByTestId('peers-self')
    expect(self).toHaveTextContent('mlab · self alias: mlab · mini-lab:278cbm')
    expect(self).not.toHaveTextContent('(from host_id)')
    expect(screen.queryByTestId('peers-self-input')).toBeNull()       // the editor closed on success
    expect(screen.getByTestId('peers-self-clear')).toBeEnabled()       // now there is something to clear
    expect(screen.queryByTestId('peers-self-error')).toBeNull()
  })

  it('Clear → updatePeerSettings(hM, {alias:""}), then the refresh shows the derived alias again', async () => {
    seedSettings('mlab', 'config')
    render(<PeersSection hostId={M} />)
    await settled('bidirectional')
    const before = settingsCalls()
    fireEvent.click(screen.getByTestId('peers-self-clear'))
    await waitFor(() => expect(api.updatePeerSettings).toHaveBeenCalledWith(M, { alias: '' }))
    await waitFor(() => expect(settingsCalls()).toBeGreaterThan(before))
    await settled('bidirectional')
    expect(screen.getByTestId('peers-self')).toHaveTextContent('self alias: mini-lab ·')
    expect(screen.getByTestId('peers-self')).toHaveTextContent('(from host_id)')
    expect(screen.queryByTestId('peers-self-clear')).toBeNull()
  })

  it('409 → peers-self-error shows the daemon text, the input stays open with the typed value, and no orphan flow note is painted (codex F3)', async () => {
    vi.mocked(api.updatePeerSettings).mockRejectedValue(new HostApiError(409, 'Conflict', 'alias "air26" is already used by a peer host'))
    render(<PeersSection hostId={M} />)
    await settled('bidirectional')
    fireEvent.click(screen.getByTestId('peers-self-edit'))
    fireEvent.change(screen.getByTestId('peers-self-input'), { target: { value: 'air26' } })
    fireEvent.click(screen.getByTestId('peers-self-save'))
    expect(await screen.findByTestId('peers-self-error')).toHaveTextContent('alias "air26" is already used by a peer host')
    await settled('bidirectional')                                                   // the runner still refreshed
    expect((screen.getByTestId('peers-self-input') as HTMLInputElement).value).toBe('air26')
    expect(screen.getByTestId('peers-self-save')).toBeEnabled()
    // The flow ran under selfKey: the line owns its note; the page must not ALSO render it as an orphan.
    expect(screen.queryByTestId('peer-flow')).toBeNull()
    expect(screen.queryByTestId('peer-flow-error')).toBeNull()
    expect(screen.getAllByText('alias "air26" is already used by a peer host')).toHaveLength(1)
  })

  it('400 (reserved) → the daemon text inline', async () => {
    vi.mocked(api.updatePeerSettings).mockRejectedValue(new HostApiError(400, 'Bad Request', 'alias ".." is reserved'))
    render(<PeersSection hostId={M} />)
    await settled('bidirectional')
    fireEvent.click(screen.getByTestId('peers-self-edit'))
    fireEvent.change(screen.getByTestId('peers-self-input'), { target: { value: '..' } })
    fireEvent.click(screen.getByTestId('peers-self-save'))
    expect(await screen.findByTestId('peers-self-error')).toHaveTextContent('alias ".." is reserved')
  })

  it('a PUT answered 200 without alias_source is NOT success: the too-old text, the input stays open (S-5)', async () => {
    vi.mocked(api.updatePeerSettings).mockResolvedValue({ deliver: true, alias: 'mini-lab' })
    render(<PeersSection hostId={M} />)
    await settled('bidirectional')
    fireEvent.click(screen.getByTestId('peers-self-edit'))
    fireEvent.change(screen.getByTestId('peers-self-input'), { target: { value: 'mlab' } })
    fireEvent.click(screen.getByTestId('peers-self-save'))
    expect(await screen.findByTestId('peers-self-error')).toHaveTextContent(TOO_OLD)
    await settled('bidirectional')
    expect((screen.getByTestId('peers-self-input') as HTMLInputElement).value).toBe('mlab')
    expect(screen.queryByTestId('peer-flow')).toBeNull()
  })

  it('a GET without alias_source (daemon < alpha.399): no Edit, no Clear, the too-old text on the line (codex F7)', async () => {
    vi.mocked(api.fetchPeerSettings).mockImplementation(async (h) => ({ deliver: true, alias: h === M ? 'mini-lab' : 'air26' }))
    render(<PeersSection hostId={M} />)
    await settled('bidirectional')
    expect(screen.getByTestId('peers-self')).toHaveTextContent('mlab · self alias: mini-lab · mini-lab:278cbm')
    expect(screen.queryByTestId('peers-self-edit')).toBeNull()
    expect(screen.queryByTestId('peers-self-clear')).toBeNull()
    expect(screen.queryByTestId('peers-self-input')).toBeNull()
    expect(screen.getByTestId('peers-self-too-old')).toHaveTextContent(TOO_OLD)
    expect(api.updatePeerSettings).not.toHaveBeenCalled()
  })

  it('Save holds the page lock: while the PUT is parked Refresh, Rotate, Unpair and Save itself are disabled and a second click is ignored', async () => {
    let release!: (v: api.PeerSettings) => void
    vi.mocked(api.updatePeerSettings).mockImplementation(() => new Promise<api.PeerSettings>((r) => { release = r }))
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    fireEvent.click(screen.getByTestId('peers-self-edit'))
    fireEvent.change(screen.getByTestId('peers-self-input'), { target: { value: 'mlab' } })
    fireEvent.click(screen.getByTestId('peers-self-save'))
    await waitFor(() => expect(api.updatePeerSettings).toHaveBeenCalledTimes(1))
    expect(screen.getByTestId('peers-self-save')).toBeDisabled()
    expect(screen.getByTestId('peers-self-save')).toHaveTextContent('Saving')
    expect(screen.getByTestId('peers-refresh')).toBeDisabled()
    expect(within(row).getByTestId('peer-inbound-rotate')).toBeDisabled()
    expect(within(row).getByTestId('peer-unpair-air')).toBeDisabled()
    expect(within(row).getByTestId('peer-outbound-rename')).toBeDisabled()
    // Bypassing `disabled`: the lock ignores the second write.
    fireEvent.click(screen.getByTestId('peers-self-save'))
    fireEvent.click(within(row).getByTestId('peer-inbound-rotate'))
    expect(api.updatePeerSettings).toHaveBeenCalledTimes(1)
    expect(api.rotatePeerHost).not.toHaveBeenCalled()
    release({ deliver: true, alias: 'mlab', alias_source: 'config' })
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
    expect(screen.queryByTestId('peers-self-input')).toBeNull()
  })

  it('Edit and Clear are disabled while another flow (a Commit) holds the lock', async () => {
    seedSettings('mlab', 'config')
    vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [pending(AIR_ROW, 'current')] : [MLAB_ROW]))
    let releaseCommit!: (v: PeerHostRow) => void
    vi.mocked(api.commitRotation).mockImplementation(() => new Promise<PeerHostRow>((r) => { releaseCommit = r }))
    render(<PeersSection hostId={M} />)
    const row = await settled('bidirectional')
    fireEvent.click(within(row).getByTestId('peer-inbound-commit'))
    await waitFor(() => expect(api.commitRotation).toHaveBeenCalledTimes(1))
    expect(screen.getByTestId('peers-self-edit')).toBeDisabled()
    expect(screen.getByTestId('peers-self-clear')).toBeDisabled()
    fireEvent.click(screen.getByTestId('peers-self-clear'))
    expect(api.updatePeerSettings).not.toHaveBeenCalled()
    releaseCommit(AIR_ROW)
    await waitFor(() => expect(screen.getByTestId('peers-refresh')).toBeEnabled())
  })

  it('never renders a token value around the editor (spec D-8)', async () => {
    seedSettings('mini-lab', 'host_id')
    render(<PeersSection hostId={M} />)
    await settled('bidirectional')
    fireEvent.click(screen.getByTestId('peers-self-edit'))
    fireEvent.change(screen.getByTestId('peers-self-input'), { target: { value: 'mlab' } })
    fireEvent.click(screen.getByTestId('peers-self-save'))
    await waitFor(() => expect(api.updatePeerSettings).toHaveBeenCalledTimes(1))
    await settled('bidirectional')
    assertNoTokenInDom()
    expect(document.body.innerHTML).not.toContain(SECRET_M)
  })
})
