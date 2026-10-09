// X5-App-b: the Hosts page's cross-host team consent per paired peer (allow_team + team_roots).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { PeersSection } from './PeersSection'
import { useHostStore } from '../../stores/useHostStore'
import * as api from '../../lib/host-api'
import { HostApiError, type PeerHostRow, type PeerHostVerify, type RemoteMemberView } from '../../lib/host-api'

vi.mock('../../lib/host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/host-api')>()),
  fetchHostInfo: vi.fn(),
  fetchPeerSettings: vi.fn(),
  listPeerHosts: vi.fn(),
  verifyPeerHost: vi.fn(),
  updatePeerHost: vi.fn(),
  listRemoteMembers: vi.fn(),
  endRemoteMember: vi.fn(),
}))

const M = 'hM'
const A = 'hA'
const AIR_HOST_ID = 'wakes-air-2026:oa6drb'
const AIR: PeerHostRow = { alias: 'air', url: 'http://100.64.0.4:7860', host_id: AIR_HOST_ID,
  verified: true, has_token: true, has_inbound_token: true, allow_bypass: true,
  allow_team: false, team_roots: [], rotation_pending: false, last_inbound_auth: '' }
const MLAB: PeerHostRow = { ...AIR, alias: 'mini-lab', url: 'http://100.64.0.2:7860', host_id: 'mini-lab:278cbm' }
const ok = (alias: string, self_alias: string, host_id: string): PeerHostVerify =>
  ({ alias, host_id, ok: true, self_alias, daemon_version: '1.0.0' })
const member = (mk: string, lead_host_id = AIR_HOST_ID): RemoteMemberView =>
  ({ mk, title: `t-${mk}`, cwd: '/w', lead_host_id, lead_alias: 'air' })

/** The mocked daemon's current row for `air`: PUT merges into it, the next list returns it. */
let airRow: PeerHostRow

function seed() {
  useHostStore.setState({
    hosts: {
      [M]: { id: M, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, token: 'x' },
      [A]: { id: A, name: 'Air 2026', ip: '100.64.0.4', port: 7860, order: 1, token: 'y' },
    },
    hostOrder: [M, A],
    runtime: { [M]: { status: 'connected' }, [A]: { status: 'connected' } },
  })
  vi.mocked(api.fetchHostInfo).mockImplementation(async (h) => ({
    host_id: h === M ? 'mini-lab:278cbm' : AIR_HOST_ID,
    tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '',
  }))
  vi.mocked(api.fetchPeerSettings).mockImplementation(async (h) =>
    h === M ? { deliver: true, alias: 'mini-lab', alias_source: 'host_id' } : { deliver: true, alias: 'air', alias_source: 'config' })
  vi.mocked(api.listPeerHosts).mockImplementation(async (h) => (h === M ? [airRow] : [MLAB]))
  vi.mocked(api.verifyPeerHost).mockImplementation(async (h, alias) =>
    h === M ? ok(alias, 'air', AIR_HOST_ID) : ok(alias, 'mini-lab', 'mini-lab:278cbm'))
  vi.mocked(api.updatePeerHost).mockImplementation(async (_h, _a, patch) => {
    airRow = { ...airRow, ...(patch.allow_team !== undefined ? { allow_team: patch.allow_team } : {}),
      ...(patch.team_roots !== undefined ? { team_roots: patch.team_roots } : {}) }
    return airRow
  })
  vi.mocked(api.listRemoteMembers).mockResolvedValue([])
  vi.mocked(api.endRemoteMember).mockResolvedValue(undefined)
}

beforeEach(() => {
  for (const m of [api.fetchHostInfo, api.fetchPeerSettings, api.listPeerHosts, api.verifyPeerHost, api.updatePeerHost,
    api.listRemoteMembers, api.endRemoteMember]) vi.mocked(m).mockReset()
  localStorage.clear()
  airRow = { ...AIR }
  seed()
})

async function openRow() {
  render(<PeersSection hostId={M} />)
  const row = await screen.findByTestId('peer-row-air')
  const toggle = await within(row).findByTestId('peer-team-toggle')
  await waitFor(() => expect(toggle).toBeEnabled())
  return row
}

describe('PeersSection — team consent (allow_team / team_roots)', () => {
  it('shows the daemon current values on load', async () => {
    airRow = { ...AIR, allow_team: true, team_roots: ['/Users/w/Workspace', '/tmp/x'] }
    const row = await openRow()
    expect(within(row).getByTestId('peer-team-toggle')).toBeChecked()
    expect(within(row).getAllByTestId('peer-team-root')).toHaveLength(2)
    expect(within(row).getByTestId('peer-team')).toHaveTextContent('/Users/w/Workspace')
  })

  it('turning on sends only allow_team, then shows the daemon value', async () => {
    const row = await openRow()
    expect(within(row).getByTestId('peer-team-toggle')).not.toBeChecked()
    fireEvent.click(within(row).getByTestId('peer-team-toggle'))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledTimes(1))
    expect(api.updatePeerHost).toHaveBeenCalledWith(M, 'air', { allow_team: true })
    await waitFor(() => expect(within(screen.getByTestId('peer-row-air')).getByTestId('peer-team-toggle')).toBeChecked())
    expect(api.listRemoteMembers).not.toHaveBeenCalled()
  })

  it('turning off with no live members sends only allow_team:false, no confirm', async () => {
    airRow = { ...AIR, allow_team: true, team_roots: ['/a'] }
    const row = await openRow()
    fireEvent.click(within(row).getByTestId('peer-team-toggle'))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(M, 'air', { allow_team: false }))
    expect(screen.queryByTestId('peer-team-end-dialog')).toBeNull()
    expect(api.endRemoteMember).not.toHaveBeenCalled()
  })

  it('a toggle failure shows the daemon message and the switch stays at the daemon value', async () => {
    vi.mocked(api.updatePeerHost).mockRejectedValue(new HostApiError(409, 'Conflict', 'host not verified'))
    const row = await openRow()
    fireEvent.click(within(row).getByTestId('peer-team-toggle'))
    expect(await within(row).findByTestId('peer-team-error')).toHaveTextContent('host not verified')
    await waitFor(() => expect(within(row).getByTestId('peer-team-toggle')).toBeEnabled())
    expect(within(row).getByTestId('peer-team-toggle')).not.toBeChecked()
  })

  it('adding a root sends only team_roots (whole set), not allow_team', async () => {
    airRow = { ...AIR, team_roots: ['/a'] }
    const row = await openRow()
    fireEvent.change(within(row).getByTestId('peer-team-root-input'), { target: { value: '/b' } })
    fireEvent.click(within(row).getByTestId('peer-team-root-add'))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(M, 'air', { team_roots: ['/a', '/b'] }))
    await waitFor(() => expect(within(screen.getByTestId('peer-row-air')).getAllByTestId('peer-team-root')).toHaveLength(2))
  })

  it('removing a root sends the rest; removing the last sends an empty array', async () => {
    airRow = { ...AIR, team_roots: ['/a', '/b'] }
    const row = await openRow()
    fireEvent.click(within(row).getAllByTestId('peer-team-root-remove')[0])
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenLastCalledWith(M, 'air', { team_roots: ['/b'] }))
    await waitFor(() => expect(within(screen.getByTestId('peer-row-air')).getAllByTestId('peer-team-root')).toHaveLength(1))
    fireEvent.click(within(screen.getByTestId('peer-row-air')).getByTestId('peer-team-root-remove'))
    await waitFor(() => expect(api.updatePeerHost).toHaveBeenLastCalledWith(M, 'air', { team_roots: [] }))
    await waitFor(() => expect(within(screen.getByTestId('peer-row-air')).queryAllByTestId('peer-team-root')).toHaveLength(0))
  })

  it('a 400 on roots shows the daemon message under the field and keeps the old list', async () => {
    airRow = { ...AIR, team_roots: ['/a'] }
    vi.mocked(api.updatePeerHost).mockRejectedValue(new HostApiError(400, 'Bad Request', 'team root "rel" is not an absolute path'))
    const row = await openRow()
    fireEvent.change(within(row).getByTestId('peer-team-root-input'), { target: { value: 'rel' } })
    fireEvent.click(within(row).getByTestId('peer-team-root-add'))
    expect(await within(row).findByTestId('peer-team-roots-error')).toHaveTextContent('team root "rel" is not an absolute path')
    expect(within(row).getAllByTestId('peer-team-root')).toHaveLength(1)
  })

  it('controls are disabled while a write is in flight', async () => {
    let release!: () => void
    vi.mocked(api.updatePeerHost).mockImplementation(() => new Promise((r) => { release = () => r(airRow) }))
    const row = await openRow()
    fireEvent.click(within(row).getByTestId('peer-team-toggle'))
    await waitFor(() => expect(within(row).getByTestId('peer-team-toggle')).toBeDisabled())
    expect(within(row).getByTestId('peer-team-root-add')).toBeDisabled()
    release()
    await waitFor(() => expect(within(screen.getByTestId('peer-row-air')).getByTestId('peer-team-toggle')).toBeEnabled())
  })

  describe('turning off while that peer has live members', () => {
    beforeEach(() => {
      airRow = { ...AIR, allow_team: true }
      // one member of another lead host must not be counted
      vi.mocked(api.listRemoteMembers).mockResolvedValue([member('mk1'), member('mk2'), member('other', 'someone-else:zzzzzz')])
    })

    it('asks first; confirming turns it off then ends exactly that peer members', async () => {
      const row = await openRow()
      fireEvent.click(within(row).getByTestId('peer-team-toggle'))
      const dialog = await screen.findByTestId('peer-team-end-dialog')
      expect(dialog).toHaveTextContent('2')
      expect(dialog).toHaveTextContent('t-mk1')
      expect(api.updatePeerHost).not.toHaveBeenCalled()
      fireEvent.click(screen.getByTestId('peer-team-end-confirm'))
      await waitFor(() => expect(api.endRemoteMember).toHaveBeenCalledTimes(2))
      expect(api.updatePeerHost).toHaveBeenCalledWith(M, 'air', { allow_team: false })
      expect(api.endRemoteMember).toHaveBeenCalledWith(M, 'mk1')
      expect(api.endRemoteMember).toHaveBeenCalledWith(M, 'mk2')
      expect(vi.mocked(api.updatePeerHost).mock.invocationCallOrder[0])
        .toBeLessThan(vi.mocked(api.endRemoteMember).mock.invocationCallOrder[0])
      await waitFor(() => expect(screen.queryByTestId('peer-team-end-dialog')).toBeNull())
    })

    it('cancel changes nothing', async () => {
      const row = await openRow()
      fireEvent.click(within(row).getByTestId('peer-team-toggle'))
      await screen.findByTestId('peer-team-end-dialog')
      fireEvent.click(screen.getByTestId('peer-team-end-cancel'))
      expect(screen.queryByTestId('peer-team-end-dialog')).toBeNull()
      expect(api.updatePeerHost).not.toHaveBeenCalled()
      expect(api.endRemoteMember).not.toHaveBeenCalled()
      expect(within(screen.getByTestId('peer-row-air')).getByTestId('peer-team-toggle')).toBeChecked()
    })

    it('"turn off, keep members" turns it off without ending anyone', async () => {
      const row = await openRow()
      fireEvent.click(within(row).getByTestId('peer-team-toggle'))
      await screen.findByTestId('peer-team-end-dialog')
      fireEvent.click(screen.getByTestId('peer-team-end-keep'))
      await waitFor(() => expect(api.updatePeerHost).toHaveBeenCalledWith(M, 'air', { allow_team: false }))
      expect(api.endRemoteMember).not.toHaveBeenCalled()
    })

    it('a member that is no longer live (409) is not an error; another failure is shown', async () => {
      vi.mocked(api.endRemoteMember).mockImplementation(async (_h, mk) => {
        throw mk === 'mk1' ? new HostApiError(409, 'Conflict', 'not live') : new HostApiError(500, 'x', 'boom')
      })
      const row = await openRow()
      fireEvent.click(within(row).getByTestId('peer-team-toggle'))
      await screen.findByTestId('peer-team-end-dialog')
      fireEvent.click(screen.getByTestId('peer-team-end-confirm'))
      const err = await within(screen.getByTestId('peer-row-air')).findByTestId('peer-team-error')
      expect(err).toHaveTextContent('boom')
      expect(err).not.toHaveTextContent('not live')
    })
  })

  it('if live members cannot be listed, nothing is changed and the error shows', async () => {
    airRow = { ...AIR, allow_team: true }
    vi.mocked(api.listRemoteMembers).mockRejectedValue(new HostApiError(404, 'Not Found', 'no such route'))
    const row = await openRow()
    fireEvent.click(within(row).getByTestId('peer-team-toggle'))
    expect(await within(row).findByTestId('peer-team-error')).toHaveTextContent('no such route')
    expect(api.updatePeerHost).not.toHaveBeenCalled()
  })

  it('a host with no paired peer shows no team section', async () => {
    vi.mocked(api.listPeerHosts).mockResolvedValue([])
    render(<PeersSection hostId={M} />)
    await screen.findByTestId('peers-empty')
    expect(screen.queryByTestId('peer-team')).toBeNull()
  })
})
