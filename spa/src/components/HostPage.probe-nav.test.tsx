// #3039 review: an outside navigation (session launcher, headless launcher, hidden pane) comes from ANOTHER tab; the URL
// changes first and useRouteSync activates the Hosts tab a commit later. Driven with the real useRouteSync + tab store.
import { act, cleanup, render, screen } from '@testing-library/react'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { useRouteSync } from '../hooks/useRouteSync'
import { getPrimaryPane } from '../lib/pane-tree'
import { clearContributions } from '../lib/settings-contribution-registry'
import { clearModuleRegistry } from '../lib/module-registry'
import { registerBuiltinModules } from '../lib/register-modules'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useHistoryStore } from '../stores/useHistoryStore'
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

const H = 'test-host'
const entry = (id: string) => ({ id, name: 'orig', icon: '', icon_weight: '', aliases: {} })
const selectDirty = (id: string) => {
  const d = useFleetDraftStore.getState()
  d.beginProbe(entry(id), false, { revisions: {}, rows: {} })
  d.setProbeDraft((x) => ({ ...x, name: 'typed' }))
  d.selectProbe({ id, isNew: false })
}

// Like TabContent for a light pane: HostPage mounts the first time the Hosts tab is active, then stays mounted.
function Harness() {
  useRouteSync()
  const kind = useTabStore((s) => {
    const t = s.activeTabId ? s.tabs[s.activeTabId] : null
    return t ? getPrimaryPane(t.layout).content.kind : null
  })
  const [mounted, setMounted] = useState(false)
  if (kind === 'hosts' && !mounted) setMounted(true)
  return mounted ? <HostPage pane={{ id: 'pane-hosts', content: { kind: 'hosts' } }} isActive={kind === 'hosts'} /> : null
}

function mount(path: string) {
  const loc = memoryLocation({ path, record: true })
  render(<Router hook={loc.hook}><Harness /></Router>)
  return loc
}
const hostsTabId = () =>
  Object.values(useTabStore.getState().tabs).find((t) => getPrimaryPane(t.layout).content.kind === 'hosts')!.id
const probeShown = () => screen.queryByTestId('probe-page-stub') != null
const flush = () => act(async () => {})

beforeEach(() => {
  cleanup(); clearContributions(); clearModuleRegistry(); registerBuiltinModules(); resetLastHostSelection()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  useFleetDraftStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], activeHostId: H, runtime: { [H]: { status: 'connected' } } as never,
  })
  useFleetConfigStore.getState().reset()
  useFleetStore.getState().reset()
  useFleetStore.setState({ byHost: { [H]: { gen: 1, support: 'yes', term: 0 } } as never })
})
afterEach(() => { vi.restoreAllMocks() })

async function probeOpenThenOtherTab(dirty: boolean) {
  const loc = mount(`/hosts/${H}/overview`)
  await flush()
  if (dirty) act(() => { selectDirty('p1') })
  else act(() => { useFleetDraftStore.getState().selectProbe({ id: 'p1', isNew: false }) })
  expect(probeShown()).toBe(true)
  const hostsTab = hostsTabId()
  act(() => { useTabStore.getState().openSingletonTab({ kind: 'history' }) })
  await flush()
  expect(useTabStore.getState().activeTabId).not.toBe(hostsTab)
  return { loc, hostsTab }
}

describe('HostPage probe page — navigation from another tab', () => {
  it('an outside navigation to a host page leaves the probe page', async () => {
    const { loc, hostsTab } = await probeOpenThenOtherTab(false)
    act(() => { loc.navigate(`/hosts/${H}/projects`) })
    await flush()
    expect(useTabStore.getState().activeTabId).toBe(hostsTab)
    expect(probeShown()).toBe(false)
    expect(useFleetDraftStore.getState().selectedProbe).toBeNull()
  })

  it('a dirty draft asks first: declining keeps the probe page, accepting leaves', async () => {
    const { loc } = await probeOpenThenOtherTab(true)
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    act(() => { loc.navigate(`/hosts/${H}/projects`) })
    await flush()
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(probeShown()).toBe(true)

    // The mark was cleared by the decline: a later plain tab switch back must not ask again.
    confirm.mockReturnValue(true)
    await flush()
    expect(confirm).toHaveBeenCalledTimes(1)

    // Another outside navigation, this time accepted.
    act(() => { useTabStore.getState().openSingletonTab({ kind: 'history' }) })
    await flush()
    act(() => { loc.navigate(`/hosts/${H}/nex`) })
    await flush()
    expect(confirm).toHaveBeenCalledTimes(2)
    expect(probeShown()).toBe(false)
    expect(useFleetDraftStore.getState().probe).toBeNull()
  })

  it('switching back to the Hosts tab (bare /hosts, then the canonical rewrite) keeps the probe page', async () => {
    const { loc, hostsTab } = await probeOpenThenOtherTab(false)
    act(() => { useTabStore.getState().setActiveTab(hostsTab) })
    await flush()
    expect(loc.history!.at(-1)).toMatch(/^\/hosts\/test-host\//)
    expect(useTabStore.getState().activeTabId).toBe(hostsTab)
    expect(probeShown()).toBe(true)
  })

  it('HostPage not mounted yet: a mount at an outside host URL shows that host, not the probe page', async () => {
    useFleetDraftStore.getState().selectProbe({ id: 'p1', isNew: false })
    mount(`/hosts/${H}/nex`)
    await flush()
    expect(probeShown()).toBe(false)
    expect(useFleetDraftStore.getState().selectedProbe).toBeNull()
  })
})
