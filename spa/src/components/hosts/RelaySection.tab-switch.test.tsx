// Regression (plan v3 P9a-3; the repo CLAUDE.md tab-hosted checklist): what the reader typed into a relay prompt
// editor must survive switching to another tab and back, and a Hosts sub-page switch. The Hosts page is a tab and
// `hosts` is not a light kind, so under keepAliveCount 0 the section really unmounts — this mounts the real
// TabContent and the real HostPage, and asserts the unmount, so it cannot pass by the tab being kept alive.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act, waitFor } from '@testing-library/react'
import { useEffect } from 'react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { TabContent } from '../TabContent'
import { HostPage, resetLastHostSelection } from '../HostPage'
import { RelaySection } from './RelaySection'
import { registerModule, clearModuleRegistry } from '../../lib/module-registry'
import { clearContributions, registerSettingsContribution } from '../../lib/settings-contribution-registry'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'
import { fetchRelayPrompts, type RelayPrompts } from '../../lib/relay-prompts-api'
import { clearAllRelayPromptDrafts, readRelayPromptDraft } from '../../lib/relay-prompt-draft-memory'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { RelaySwitches } from '../../lib/host-config-api'
import { createTab, type Tab } from '../../types/tab'

vi.mock('../../lib/relay-prompts-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/relay-prompts-api')>()), fetchRelayPrompts: vi.fn(),
}))

const H = 'h1'
const PROMPTS: RelayPrompts = {
  write: 'default write', fix: 'default fix', seed: 'default seed',
  defaults: { write: 'default write', fix: 'default fix', seed: 'default seed' },
  fixed: {
    write: { head: '[pdx-relay op={{op}} n={{nonce}}] ', tail: '# HANDOFF' },
    fix: { head: '[pdx-relay op={{op}} n={{nonce}}] ', tail: '缺少段落：{{missing}}' },
    seed: { head: '↪ 接手自 {{old_ref}}\n[pdx-relay seed op={{op}} n={{nonce}}] ', tail: '' },
  },
  variables: ['path', 'old_ref', 'old_session', 'context', 'whoami'],
}

let mounts = 0
let unmounts = 0
function RelayContribution({ ctx }: { ctx: SettingsContextFor<'host'> }) {
  useEffect(() => {
    mounts += 1
    return () => { unmounts += 1 }
  }, [])
  return <RelaySection hostId={ctx.hostId} />
}
const Overview = () => <div data-testid="overview-stub" />
const Other = () => <div data-testid="other-tab" />

const hostsTab: Tab = { ...createTab({ kind: 'hosts' }), id: 't-hosts' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [hostsTab, dashTab]

const saveRelay = vi.fn(async (hostId: string, items: RelaySwitches) => {
  useHostConfigStore.setState((s) => {
    const cur = s.byHost[hostId]
    return { byHost: { ...s.byHost, [hostId]: { ...cur, relay: items, revisions: { ...cur.revisions, relay: cur.revisions.relay + 1 } } } }
  })
})

let mem: ReturnType<typeof memoryLocation>
function renderAt(active: Tab) {
  return render(<Router hook={mem.hook}><TabContent activeTab={active} allTabs={all} /></Router>)
}
const box = () => screen.getByTestId('relay-prompt-write-box') as HTMLTextAreaElement

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
  clearContributions()
  resetLastHostSelection()
  clearAllRelayPromptDrafts()
  mounts = 0
  unmounts = 0
  registerModule({ id: 'hosts', name: 'Hosts', panes: [{ kind: 'hosts', component: HostPage }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  registerSettingsContribution({ moduleId: 't', id: 't.overview', localId: 'overview', scope: 'host', order: 0, labelKey: 'hosts.overview', component: Overview })
  registerSettingsContribution({ moduleId: 't', id: 't.relay', localId: 'relay', scope: 'host', order: 11, labelKey: 'hosts.relay', component: RelayContribution })
  // The default, pinned here: a larger count keeps the tab alive and hidden, and this test would prove nothing.
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useI18nStore.getState().setLocale('en')
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], activeHostId: H, runtime: { [H]: { status: 'connected' } },
  })
  const e = emptyHostConfigEntry('ready')
  useHostConfigStore.setState({
    byHost: { [H]: { ...e, relay: { self_solo: true, self_lead: true }, relaySupported: true, revisions: { ...e.revisions, relay: 1 } } },
    load: vi.fn(async () => {}), saveRelay,
  })
  saveRelay.mockClear()
  vi.mocked(fetchRelayPrompts).mockReset().mockResolvedValue(PROMPTS)
  mem = memoryLocation({ path: `/hosts/${H}/relay`, record: true })
})
afterEach(() => { cleanup(); clearAllRelayPromptDrafts() })

describe('relay prompt drafts across tab and sub-page switches', () => {
  it('a typed draft survives a switch to another tab and back', async () => {
    const { rerender } = renderAt(hostsTab)
    await screen.findByTestId('relay-prompt-write')
    fireEvent.change(box(), { target: { value: 'half a body' } })

    rerender(<Router hook={mem.hook}><TabContent activeTab={dashTab} allTabs={all} /></Router>)
    // Really gone, not merely hidden: this is what the draft memory is for.
    expect(screen.queryByTestId('relay-section')).toBeNull()
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()
    expect(unmounts).toBe(1)

    rerender(<Router hook={mem.hook}><TabContent activeTab={hostsTab} allTabs={all} /></Router>)
    await screen.findByTestId('relay-prompt-write')
    expect(mounts).toBe(2)
    expect(box().value).toBe('half a body')
  })

  it('a typed draft survives a Hosts sub-page switch', async () => {
    renderAt(hostsTab)
    await screen.findByTestId('relay-prompt-write')
    fireEvent.change(box(), { target: { value: 'still mine' } })

    act(() => mem.navigate(`/hosts/${H}/overview`, { replace: true }))
    expect(screen.getByTestId('overview-stub')).toBeInTheDocument()
    expect(screen.queryByTestId('relay-section')).toBeNull()
    expect(unmounts).toBe(1)

    act(() => mem.navigate(`/hosts/${H}/relay`, { replace: true }))
    await screen.findByTestId('relay-prompt-write')
    expect(box().value).toBe('still mine')
  })

  it('a saved draft is gone: the next switch shows the stored text, not the draft', async () => {
    const { rerender } = renderAt(hostsTab)
    await screen.findByTestId('relay-prompt-write')
    fireEvent.change(box(), { target: { value: 'saved body' } })
    fireEvent.click(screen.getByTestId('relay-prompt-write-save'))
    await waitFor(() => expect(saveRelay).toHaveBeenCalledWith(H, { self_solo: true, self_lead: true, prompt_write: 'saved body' }))
    await waitFor(() => expect(readRelayPromptDraft(`${H}:write`)).toBeUndefined())

    // Stored elsewhere meanwhile: a draft that came back would show 'saved body' instead.
    act(() => useHostConfigStore.setState((s) => ({
      byHost: { [H]: { ...s.byHost[H], relay: { self_solo: true, self_lead: true, prompt_write: 'edited elsewhere' } } },
    })))
    rerender(<Router hook={mem.hook}><TabContent activeTab={dashTab} allTabs={all} /></Router>)
    expect(screen.queryByTestId('relay-section')).toBeNull()
    rerender(<Router hook={mem.hook}><TabContent activeTab={hostsTab} allTabs={all} /></Router>)
    await screen.findByTestId('relay-prompt-write')
    expect(box().value).toBe('edited elsewhere')
  })
})
