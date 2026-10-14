// Regression (fleet plan T-2a.4; the repo CLAUDE.md tab-hosted checklist): the draft of the 「只探查的主機」 dialog — name,
// SSH host, icon, colours — must survive switching to another tab and back, and a Hosts sub-page switch. The Hosts page is
// a tab and `hosts` is not a light kind, so under keepAliveCount 0 the section really unmounts: this mounts the real
// TabContent and the real HostPage and asserts the unmount, so it cannot pass by the tab being kept alive.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act, waitFor } from '@testing-library/react'
import { useEffect } from 'react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { TabContent } from '../TabContent'
import { HostPage, resetLastHostSelection } from '../HostPage'
import { ProbeHostsSection } from './ProbeHostsSection'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { clearContributions, registerSettingsContribution } from '../../lib/settings-contribution-registry'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'
import { useFleetStore } from '../../lib/fleet/store'
import { useProbeDraftStore } from '../../lib/fleet/probe-draft-memory'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { createTab, type Tab } from '../../types/tab'

vi.mock('../../lib/fleet/api', async (orig) => ({
  ...(await orig<typeof import('../../lib/fleet/api')>()),
  fetchProbeHosts: vi.fn(), fetchSshAliases: vi.fn(), putProbeHosts: vi.fn(),
}))
import { fetchProbeHosts, fetchSshAliases } from '../../lib/fleet/api'

const H = 'h1'
let mounts = 0
let unmounts = 0
function ProbeContribution({ ctx }: { ctx: SettingsContextFor<'host'> }) {
  useEffect(() => {
    mounts += 1
    return () => { unmounts += 1 }
  }, [])
  return <ProbeHostsSection hostId={ctx.hostId} />
}
const Overview = () => <div data-testid="overview-stub" />
const Other = () => <div data-testid="other-tab" />

const hostsTab: Tab = { ...createTab({ kind: 'hosts' }), id: 't-hosts' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [hostsTab, dashTab]

let mem: ReturnType<typeof memoryLocation>
const view = (active: Tab) => <Router hook={mem.hook}><TabContent activeTab={active} allTabs={all} /></Router>

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
  clearContributions()
  resetLastHostSelection()
  useProbeDraftStore.getState().reset()
  useFleetStore.getState().reset()
  mounts = 0
  unmounts = 0
  registerModule({ id: 'hosts', name: 'Hosts', panes: [{ kind: 'hosts', component: HostPage }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  registerSettingsContribution({ moduleId: 't', id: 't.overview', localId: 'overview', scope: 'host', order: 0, labelKey: 'hosts.overview', component: Overview })
  registerSettingsContribution({ moduleId: 't', id: 't.probe', localId: 'probe', scope: 'host', order: 11, labelKey: 'hosts.probe.title', component: ProbeContribution })
  // The default, pinned here: a larger count keeps the tab alive and hidden, and this test would prove nothing.
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useI18nStore.getState().setLocale('en')
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], activeHostId: H, runtime: { [H]: { status: 'connected' } },
  } as never)
  const gen = useFleetStore.getState().begin(H)
  useFleetStore.getState().setSupport(H, gen, 'yes')
  vi.mocked(fetchProbeHosts).mockReset().mockResolvedValue({ items: [], revision: 1 })
  vi.mocked(fetchSshAliases).mockReset().mockResolvedValue(['air19', 'mserv'])
  mem = memoryLocation({ path: `/hosts/${H}/probe`, record: true })
})
afterEach(() => { cleanup(); useProbeDraftStore.getState().reset() })

const name = () => screen.getByTestId('probe-field-name') as HTMLInputElement
const alias = () => screen.getByTestId('probe-field-alias') as HTMLSelectElement

async function startDraft() {
  const r = render(view(hostsTab))
  await waitFor(() => expect((screen.getByTestId('probe-add') as HTMLButtonElement).disabled).toBe(false))
  fireEvent.click(screen.getByTestId('probe-add'))
  fireEvent.change(name(), { target: { value: 'half a name' } })
  await waitFor(() => expect(alias().querySelectorAll('option').length).toBeGreaterThan(1))
  fireEvent.change(alias(), { target: { value: 'mserv' } })
  fireEvent.click(screen.getByTestId('host-color-layer-main')) // a colour: the draft gets a main layer
  return r
}

describe('probe host dialog draft across tab and sub-page switches', () => {
  it('survives a switch to another tab and back', async () => {
    const { rerender } = await startDraft()
    const swatch = screen.getByTestId('host-color-layer-main').textContent

    rerender(view(dashTab))
    // Really gone, not merely hidden: this is what the draft memory is for.
    expect(screen.queryByTestId('probe-section')).toBeNull()
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()
    expect(unmounts).toBe(1)

    rerender(view(hostsTab))
    await screen.findByTestId('probe-dialog')
    expect(mounts).toBe(2)
    expect(name().value).toBe('half a name')
    await waitFor(() => expect(alias().value).toBe('mserv'))
    expect(screen.getByTestId('host-color-layer-main').textContent).toBe(swatch)
    expect(useProbeDraftStore.getState().byHost[H]?.colors?.console?.main.color).toMatch(/^#[0-9a-f]{6}$/)
  })

  it('survives a Hosts sub-page switch', async () => {
    await startDraft()
    act(() => mem.navigate(`/hosts/${H}/overview`, { replace: true }))
    expect(screen.getByTestId('overview-stub')).toBeInTheDocument()
    expect(screen.queryByTestId('probe-section')).toBeNull()
    expect(unmounts).toBe(1)

    act(() => mem.navigate(`/hosts/${H}/probe`, { replace: true }))
    await screen.findByTestId('probe-dialog')
    expect(name().value).toBe('half a name')
  })

  it('a cancelled draft is gone: the next switch shows a closed dialog', async () => {
    const { rerender } = await startDraft()
    fireEvent.click(screen.getByTestId('probe-cancel'))
    rerender(view(dashTab))
    rerender(view(hostsTab))
    await screen.findByTestId('probe-section')
    expect(screen.queryByTestId('probe-dialog')).toBeNull()
  })

  it('a removed host takes its draft with it', async () => {
    await startDraft()
    act(() => useFleetStore.getState().forgetHost(H))
    expect(useProbeDraftStore.getState().byHost[H]).toBeUndefined()
  })
})
