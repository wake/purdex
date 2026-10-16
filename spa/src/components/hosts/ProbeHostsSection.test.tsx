// F-2d: the overview's probe-host list — a short list whose rows open the entry; it no longer edits.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

const fetchFleetConfig = vi.fn()
const fetchSshAliases = vi.fn()
vi.mock('../../lib/fleet/config-api', () => ({
  fetchFleetConfig: (...a: unknown[]) => fetchFleetConfig(...a),
  fetchSshAliases: (...a: unknown[]) => fetchSshAliases(...a),
  putProbeHosts: vi.fn(),
  putFleetRow: vi.fn(),
}))

import { ProbeHostsSection } from './ProbeHostsSection'
import { useFleetConfigStore } from '../../lib/fleet/config-store'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import { useFleetStore } from '../../lib/fleet/store'
import { useHostStore } from '../../stores/useHostStore'
import type { FleetConfigRead, ProbeRow } from '../../lib/fleet/config-api'

const A = 'ha'
const B = 'hb'
const nas = (alias: string): ProbeRow => ({ id: 'aaaaaaaa', name: 'nas', ssh_alias: alias, icon: '', icon_weight: '' })
const cfg = (items: ProbeRow[], revision: number): FleetConfigRead => ({
  probe: { items, revision, dropped: null, invalid: false },
  fleet: { items: { order: ['air26', 'mlab'], peer_ssh_alias: 'mlab', enabled: true }, revision: 5, dropped: null, invalid: false },
})
let copies: Record<string, FleetConfigRead>

beforeEach(() => {
  fetchFleetConfig.mockReset(); fetchSshAliases.mockReset()
  copies = { [A]: cfg([nas('nas-a')], 3), [B]: cfg([nas('nas-b')], 8) }
  fetchFleetConfig.mockImplementation(async (id: string) => copies[id])
  fetchSshAliases.mockResolvedValue([])
  useFleetConfigStore.getState().reset()
  const host = (id: string) => ({ id, name: id, ip: '127.0.0.1', port: 7860, token: 't' })
  useHostStore.setState({
    hosts: { [A]: host(A), [B]: host(B) } as never,
    hostOrder: [A, B],
    runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } } as never,
  })
  useFleetStore.getState().reset()
  useFleetStore.setState({ byHost: { [A]: { gen: 1, support: 'yes', term: 0 }, [B]: { gen: 1, support: 'yes', term: 0 } } as never })
})

afterEach(() => { vi.restoreAllMocks() })

describe('ProbeHostsSection (the short list)', () => {
  it('lists each probe host once with its badge, name and status', async () => {
    render(<ProbeHostsSection />)
    const r = await screen.findByTestId('probe-row-aaaaaaaa')
    expect(r.textContent).toContain('nas')
    expect(r.querySelector('[data-testid="probe-badge"]')).toBeTruthy()
    expect(r.querySelector('[data-testid="probe-status"]')).toBeTruthy()
    expect(screen.getAllByTestId(/^probe-row-/)).toHaveLength(1)
  })

  it('a row opens the entry', async () => {
    render(<ProbeHostsSection />)
    fireEvent.click(await screen.findByTestId('probe-row-aaaaaaaa'))
    expect(useFleetDraftStore.getState().selectedProbe).toEqual({ id: 'aaaaaaaa', isNew: false })
  })

  it('「新增觀察主機」 selects a fresh id as a new entry', async () => {
    render(<ProbeHostsSection />)
    await screen.findByTestId('probe-row-aaaaaaaa')
    await waitFor(() => expect((screen.getByTestId('probe-add') as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(screen.getByTestId('probe-add'))
    const sel = useFleetDraftStore.getState().selectedProbe!
    expect(sel.isNew).toBe(true)
    expect(sel.id).not.toBe('aaaaaaaa')
  })

  it('has no edit or delete controls any more', async () => {
    render(<ProbeHostsSection />)
    await screen.findByTestId('probe-row-aaaaaaaa')
    expect(screen.queryByTestId('probe-edit-aaaaaaaa')).toBeNull()
    expect(screen.queryByTestId('probe-delete-aaaaaaaa')).toBeNull()
  })

  it('says it is reading while the daemons are read, not "no probe hosts"', () => {
    fetchFleetConfig.mockImplementation(() => new Promise(() => {}))
    render(<ProbeHostsSection />)
    expect(screen.getByTestId('probe-state').textContent).toMatch(/Reading/)
  })

  describe('switching with an unsaved draft of another probe host', () => {
    const entry = (id: string, name: string) => ({ id, name, icon: '', icon_weight: '', aliases: {} })
    const dirty = () => {
      const d = useFleetDraftStore.getState()
      d.beginProbe(entry('bbbbbbbb', 'other'), false, { revisions: {}, rows: {} })
      d.setProbeDraft((x) => ({ ...x, name: 'typed' }))
      d.selectProbe({ id: 'bbbbbbbb', isNew: false })
    }
    it('asks first and stays when the reader declines', async () => {
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
      render(<ProbeHostsSection />)
      dirty()
      fireEvent.click(await screen.findByTestId('probe-row-aaaaaaaa'))
      expect(confirm).toHaveBeenCalledTimes(1)
      expect(useFleetDraftStore.getState().selectedProbe?.id).toBe('bbbbbbbb')
      expect(useFleetDraftStore.getState().probe?.draft.name).toBe('typed')
    })
    it('switches when the reader accepts', async () => {
      vi.spyOn(window, 'confirm').mockReturnValue(true)
      render(<ProbeHostsSection />)
      dirty()
      fireEvent.click(await screen.findByTestId('probe-row-aaaaaaaa'))
      expect(useFleetDraftStore.getState().selectedProbe?.id).toBe('aaaaaaaa')
    })
    it('a draft with the same content in another key order is not dirty (colour toggled on and off)', async () => {
      const confirm = vi.spyOn(window, 'confirm')
      render(<ProbeHostsSection />)
      const d = useFleetDraftStore.getState()
      const initial = { id: 'bbbbbbbb', name: 'other', icon: '', icon_weight: '', colors: { console: { main: { color: '#fff' } } }, aliases: { ha: 'x' } }
      d.beginProbe(initial as never, false, { revisions: {}, rows: {} })
      d.setProbeDraft(() => ({ aliases: { ha: 'x' }, colors: { console: { main: { color: '#fff' } } }, icon_weight: '', icon: '', name: 'other', id: 'bbbbbbbb' }) as never)
      fireEvent.click(await screen.findByTestId('probe-row-aaaaaaaa'))
      expect(confirm).not.toHaveBeenCalled()
      expect(useFleetDraftStore.getState().selectedProbe?.id).toBe('aaaaaaaa')
    })
    it('does not ask when the draft is unchanged', async () => {
      const confirm = vi.spyOn(window, 'confirm')
      render(<ProbeHostsSection />)
      useFleetDraftStore.getState().beginProbe(entry('bbbbbbbb', 'other'), false, { revisions: {}, rows: {} })
      fireEvent.click(await screen.findByTestId('probe-row-aaaaaaaa'))
      expect(confirm).not.toHaveBeenCalled()
      expect(useFleetDraftStore.getState().selectedProbe?.id).toBe('aaaaaaaa')
    })
  })

  it('renders nothing when no daemon carries fleet.v1', () => {
    useFleetStore.setState({ byHost: { [A]: { gen: 1, support: 'no', term: 0 } } as never })
    const { container } = render(<ProbeHostsSection />)
    expect(container.innerHTML).toBe('')
  })
})
