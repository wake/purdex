// F-2d review fixes: leaving a probe host's page (route rewrites, paired hosts, +) and its unsaved draft.
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { clearContributions } from '../lib/settings-contribution-registry'
import { clearModuleRegistry } from '../lib/module-registry'
import { registerBuiltinModules } from '../lib/register-modules'
import { useHostStore } from '../stores/useHostStore'
import { useFleetStore } from '../lib/fleet/store'
import { useFleetConfigStore } from '../lib/fleet/config-store'
import { useFleetDraftStore } from '../lib/fleet/config-draft'
import { HostPage, resetLastHostSelection } from './HostPage'

vi.mock('../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]) }
})
vi.mock('../lib/fleet/config-api', () => ({
  fetchFleetConfig: async () => ({
    probe: { items: [], revision: 1, dropped: null, invalid: false },
    fleet: { items: { order: [], peer_ssh_alias: '', enabled: true }, revision: 1, dropped: null, invalid: false },
  }),
  fetchSshAliases: async () => [],
  putProbeHosts: vi.fn(),
  putFleetRow: vi.fn(),
}))
vi.mock('./hosts/ProbeHostPage', () => ({ ProbeHostPage: (p: { probeId: string }) => <div data-testid="probe-page-stub" data-id={p.probeId} /> }))
vi.mock('./hosts/OverviewSection', () => ({ OverviewSection: () => <div data-testid="overview-section" /> }))
vi.mock('./hosts/PairedPhonesSection', () => ({ PairedPhonesSection: () => <div data-testid="paired-phones" /> }))

const H = 'test-host'
const entry = (id: string) => ({ id, name: 'orig', icon: '', icon_weight: '', aliases: {} })
const selectDirty = (id: string, isNew = false) => {
  const d = useFleetDraftStore.getState()
  d.beginProbe(entry(id), isNew, { revisions: {}, rows: {} })
  d.setProbeDraft((x) => ({ ...x, name: 'typed' }))
  d.selectProbe({ id, isNew })
}

function mount(path: string) {
  const loc = memoryLocation({ path, record: true })
  render(<Router hook={loc.hook}><HostPage pane={{ id: 'pane-hosts', content: { kind: 'hosts' } }} isActive /></Router>)
  return loc
}

beforeEach(() => {
  cleanup(); clearContributions(); clearModuleRegistry(); registerBuiltinModules(); resetLastHostSelection()
  useFleetDraftStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H],
    activeHostId: H,
    runtime: { [H]: { status: 'connected' } } as never,
  })
  useFleetConfigStore.getState().reset()
  useFleetStore.getState().reset()
  useFleetStore.setState({ byHost: { [H]: { gen: 1, support: 'yes', term: 0 } } as never })
})
afterEach(() => { vi.restoreAllMocks() })

describe('HostPage — a probe host\'s page', () => {
  it('stays open when the route is rewritten to its canonical path (late hydration)', async () => {
    useFleetDraftStore.getState().selectProbe({ id: 'p1', isNew: false })
    const loc = mount('/hosts')
    await waitFor(() => expect(loc.history!.at(-1)).toBe(`/hosts/${H}/overview`)) // the canonical replace happened
    expect(screen.getByTestId('probe-page-stub').getAttribute('data-id')).toBe('p1')
    expect(useFleetDraftStore.getState().selectedProbe?.id).toBe('p1')
  })

  it('a paired host asks before it drops a dirty draft; declining stays', () => {
    selectDirty('p1')
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    mount('/hosts')
    fireEvent.click(screen.getByText('Test Host'))
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('probe-page-stub')).toBeTruthy()
    expect(useFleetDraftStore.getState().probe?.draft.name).toBe('typed')
  })

  it('accepting leaves the probe page and drops the draft', () => {
    selectDirty('p1')
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    mount('/hosts')
    fireEvent.click(screen.getByText('Test Host'))
    expect(screen.queryByTestId('probe-page-stub')).toBeNull()
    expect(useFleetDraftStore.getState().selectedProbe).toBeNull()
    expect(useFleetDraftStore.getState().probe).toBeNull()
  })

  it('the paired-phones page asks the same way', () => {
    selectDirty('p1')
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    mount('/hosts')
    fireEvent.click(screen.getByText('Paired phones'))
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('paired-phones')).toBeNull()
  })

  it('a clean draft leaves without asking', () => {
    useFleetDraftStore.getState().beginProbe(entry('p1'), false, { revisions: {}, rows: {} })
    useFleetDraftStore.getState().selectProbe({ id: 'p1', isNew: false })
    const confirm = vi.spyOn(window, 'confirm')
    mount('/hosts')
    fireEvent.click(screen.getByText('Test Host'))
    expect(confirm).not.toHaveBeenCalled()
    expect(screen.queryByTestId('probe-page-stub')).toBeNull()
  })

  it('accepting the discard of a NEW draft leaves no ghost row in the sidebar', () => {
    selectDirty('newone01', true)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    mount('/hosts')
    expect(screen.getByTestId('probe-item-newone01')).toBeTruthy()
    fireEvent.click(screen.getByText('Test Host'))
    expect(screen.queryByTestId('probe-item-newone01')).toBeNull()
  })

  it('declining through the sidebar + keeps the draft and the page', async () => {
    selectDirty('p1')
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    mount('/hosts')
    const add = screen.getByTestId('probe-group-add') as HTMLButtonElement
    await waitFor(() => expect(add.disabled).toBe(false))
    fireEvent.click(add)
    expect(useFleetDraftStore.getState().selectedProbe?.id).toBe('p1')
    expect(useFleetDraftStore.getState().probe?.draft.name).toBe('typed')
  })

  describe('an outside navigation (launcher, headless launcher, hidden pane)', () => {
    const outside = `/hosts/${encodeURIComponent(H)}/projects`

    it('leaves the probe page and shows the requested host page', async () => {
      useFleetDraftStore.getState().selectProbe({ id: 'p1', isNew: false })
      const loc = mount('/hosts')
      expect(screen.getByTestId('probe-page-stub')).toBeTruthy()
      act(() => { loc.navigate(outside) })
      await waitFor(() => expect(screen.queryByTestId('probe-page-stub')).toBeNull())
      expect(useFleetDraftStore.getState().selectedProbe).toBeNull()
      expect(loc.history!.at(-1)).toBe(outside)
    })

    it('a dirty draft asks first: declining keeps the probe page', () => {
      selectDirty('p1')
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
      const loc = mount('/hosts')
      act(() => { loc.navigate(outside) })
      expect(confirm).toHaveBeenCalledTimes(1)
      expect(screen.getByTestId('probe-page-stub')).toBeTruthy()
      expect(useFleetDraftStore.getState().probe?.draft.name).toBe('typed')
    })

    it('a dirty draft asks first: accepting leaves', () => {
      selectDirty('p1')
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
      const loc = mount('/hosts')
      act(() => { loc.navigate(outside) })
      expect(confirm).toHaveBeenCalledTimes(1)
      expect(screen.queryByTestId('probe-page-stub')).toBeNull()
      expect(useFleetDraftStore.getState().probe).toBeNull()
    })
  })
})
