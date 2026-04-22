/**
 * HostPage.test.tsx
 *
 * Two test suites:
 *
 * 1. `HostPage` — original route-navigation smoke tests (survive refactor).
 *    These mock HostSidebar + individual section stubs, then assert the right
 *    section stub is rendered for the given URL and that route
 *    canonicalization works correctly.
 *
 * 2. `HostPage (registry-driven, §3.1)` — Commit 2 render-level tests.
 *    These assert that HostPage + HostSidebar read `listContributions('host')`
 *    and render via the contribution registry.
 *
 * NOTE (commit 3): `isHostSubPage` is now dynamic via the contribution
 * registry. No shim is needed — the real implementation accepts any localId
 * registered in the registry, so §3.1 T4/T5/T7 with custom localIds work
 * out of the box.
 */

// vi.mock calls must come before any imports that reference the mocked modules.

vi.mock('../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]) }
})

vi.mock('./hosts/HostSidebar', () => ({
  HostSidebar: (props: {
    selectedHostId: string
    selectedSubPage: string
    onSelect: (hostId: string, subPage: string) => void
  }) => (
    <div data-testid="host-sidebar" data-host={props.selectedHostId} data-subpage={props.selectedSubPage}>
      <button data-testid="select-test-host-logs" onClick={() => props.onSelect('test-host', 'logs')}>
        test-host logs
      </button>
      <button data-testid="select-second-host-sessions" onClick={() => props.onSelect('second-host', 'sessions')}>
        second-host sessions
      </button>
    </div>
  ),
}))
vi.mock('./hosts/OverviewSection', () => ({
  OverviewSection: (props: { hostId: string }) => <div data-testid="overview-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/SessionsSection', () => ({
  SessionsSection: (props: { hostId: string }) => <div data-testid="sessions-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/HooksSection', () => ({
  HooksSection: (props: { hostId: string }) => <div data-testid="hooks-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/AgentsSection', () => ({
  AgentsSection: (props: { hostId: string }) => <div data-testid="agents-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/UploadSection', () => ({
  UploadSection: (props: { hostId: string }) => <div data-testid="upload-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/LogsSection', () => ({
  LogsSection: (props: { hostId: string }) => <div data-testid="logs-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/AddHostDialog', () => ({
  AddHostDialog: (props: { onClose: () => void }) => <div data-testid="add-host-dialog" onClick={props.onClose} />,
}))

import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { HostPage, resetLastHostSelection } from './HostPage'
import { useHostStore } from '../stores/useHostStore'
import {
  clearContributions,
  listContributions,
  registerSettingsContribution,
} from '../lib/settings-contribution-registry'
import { clearModuleRegistry } from '../lib/module-registry'
import { registerBuiltinModules } from '../lib/register-modules'
import type { Pane } from '../types/tab'
import type { SettingsContextFor } from '../lib/settings-contribution-types'

const TEST_HOST_ID = 'test-host'
const SECOND_HOST_ID = 'second-host'
const HOST_A = 'hA'

const hostPane: Pane = {
  id: 'pane-hosts',
  content: { kind: 'hosts' },
}

function seedHosts(options?: { activeHostId?: string | null; hostOrder?: string[] }) {
  useHostStore.setState({
    hosts: {
      [TEST_HOST_ID]: { id: TEST_HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
      [SECOND_HOST_ID]: { id: SECOND_HOST_ID, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
    },
    hostOrder: options?.hostOrder ?? [TEST_HOST_ID, SECOND_HOST_ID],
    activeHostId: options?.activeHostId ?? TEST_HOST_ID,
    runtime: {},
  })
}

function renderHostPage(initialPath: string) {
  const mem = memoryLocation({ path: initialPath, record: true })
  const view = render(
    <Router hook={mem.hook}>
      <HostPage pane={hostPane} isActive />
    </Router>,
  )
  return { ...view, mem }
}

function currentPath(mem: ReturnType<typeof memoryLocation>) {
  return mem.history[mem.history.length - 1]
}

// ---------------------------------------------------------------------------
// Shared beforeEach: populate registry + seed hosts
// HostPage.renderContent() reads listContributions('host') — the registry must
// be populated with built-in host sub-pages so section bodies render.
// vi.mock hoisting ensures mocked section stubs are used by registerBuiltinModules.
// ---------------------------------------------------------------------------
beforeEach(() => {
  resetLastHostSelection()
  clearContributions()
  clearModuleRegistry()
  registerBuiltinModules()
  seedHosts()
})

afterEach(() => {
  delete (window as unknown as Record<string, unknown>).electronAPI
  clearContributions()
  clearModuleRegistry()
})

// ---------------------------------------------------------------------------
// Suite 1: Original route-navigation smoke tests (HostPage switch → registry)
// ---------------------------------------------------------------------------
describe('HostPage', () => {
  it('mounts from a deep link on first paint', () => {
    renderHostPage('/hosts/test-host/logs')

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.queryByTestId('overview-section')).not.toBeInTheDocument()
  })

  it('remounts bare /hosts using the last valid selection', () => {
    const firstMount = renderHostPage('/hosts/test-host/logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    firstMount.unmount()

    const { mem } = renderHostPage('/hosts')

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(currentPath(mem)).toBe('/hosts/test-host/logs')
  })

  it('canonicalizes an invalid host while preserving a valid subpage', () => {
    const { mem } = renderHostPage('/hosts/missing-host/logs')

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(currentPath(mem)).toBe('/hosts/test-host/logs')
  })

  it('canonicalizes to encoded host ids when the fallback host contains reserved URL characters', () => {
    const specialHostId = 'host/with?hash#value'
    useHostStore.setState({
      hosts: {
        [specialHostId]: { id: specialHostId, name: 'Special Host', ip: '9.9.9.9', port: 7860, order: 0 },
      },
      hostOrder: [specialHostId],
      activeHostId: specialHostId,
      runtime: {},
    })

    const { mem } = renderHostPage('/hosts/missing-host/logs')

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', specialHostId)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(currentPath(mem)).toBe('/hosts/host%2Fwith%3Fhash%23value/logs')
  })

  it('does not overwrite the last valid selection on non-host routes', () => {
    const firstMount = renderHostPage('/hosts/test-host/logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)

    firstMount.mem.navigate('/history')
    firstMount.unmount()

    const { mem } = renderHostPage('/hosts')

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(currentPath(mem)).toBe('/hosts/test-host/logs')
  })

  it('canonicalizes an extra hosts path while preserving a valid subpage', () => {
    const { mem } = renderHostPage('/hosts/test-host/logs/extra')

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(currentPath(mem)).toBe('/hosts/test-host/logs')
  })

  it('preserves the current subpage when the selected host disappears', () => {
    const { rerender, mem } = renderHostPage('/hosts/test-host/logs')

    useHostStore.setState({
      hosts: {
        [SECOND_HOST_ID]: { id: SECOND_HOST_ID, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 0 },
      },
      hostOrder: [SECOND_HOST_ID],
      activeHostId: SECOND_HOST_ID,
    })

    rerender(
      <Router hook={mem.hook}>
        <HostPage pane={hostPane} isActive />
      </Router>,
    )

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', SECOND_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', SECOND_HOST_ID)
    expect(currentPath(mem)).toBe('/hosts/second-host/logs')
  })

  it('updates the URL when the sidebar selection changes', () => {
    const { mem } = renderHostPage('/hosts')

    fireEvent.click(screen.getByTestId('select-test-host-logs'))

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(currentPath(mem)).toBe('/hosts/test-host/logs')
  })

  it('renders no-host state when the host list is empty', () => {
    useHostStore.setState({
      hosts: {},
      hostOrder: [],
      activeHostId: null,
    })

    renderHostPage('/hosts')

    expect(screen.getByText('No host selected.')).toBeInTheDocument()
  })

  it('canonicalizes invalid host deep links to bare /hosts when no hosts remain', () => {
    useHostStore.setState({
      hosts: {},
      hostOrder: [],
      activeHostId: null,
      runtime: {},
    })

    const { mem } = renderHostPage('/hosts/missing-host/logs')

    expect(screen.getByText('No host selected.')).toBeInTheDocument()
    expect(currentPath(mem)).toBe('/hosts')
  })
})

// ---------------------------------------------------------------------------
// Suite 2: §3.1 registry-driven render tests (Commit 2)
// ---------------------------------------------------------------------------

function seedSingleHost(hostId = HOST_A) {
  useHostStore.setState({
    hosts: {
      [hostId]: { id: hostId, name: `Host ${hostId}`, ip: '1.2.3.4', port: 7860, order: 0 },
    },
    hostOrder: [hostId],
    activeHostId: hostId,
    runtime: {},
  })
}

describe('HostPage (registry-driven, §3.1)', () => {
  beforeEach(() => {
    seedSingleHost()
  })

  // Test 1: URL /hosts/hA/overview → render OverviewSection stub
  it('T1: /hosts/hA/overview renders OverviewSection', () => {
    renderHostPage('/hosts/hA/overview')
    expect(screen.getByTestId('overview-section')).toBeInTheDocument()
    expect(screen.getByTestId('overview-section')).toHaveAttribute('data-host', HOST_A)
    expect(screen.queryByTestId('sessions-section')).not.toBeInTheDocument()
  })

  // Test 2: URL /hosts/hA/sessions → render SessionsSection stub
  it('T2: /hosts/hA/sessions renders SessionsSection', () => {
    renderHostPage('/hosts/hA/sessions')
    expect(screen.getByTestId('sessions-section')).toBeInTheDocument()
    expect(screen.getByTestId('sessions-section')).toHaveAttribute('data-host', HOST_A)
    expect(screen.queryByTestId('overview-section')).not.toBeInTheDocument()
  })

  // Test 3: Registry has six built-in sub-pages in correct order
  it('T3: registry has six built-in sub-pages in correct order', () => {
    const contributions = listContributions('host')
    expect(contributions.length).toBeGreaterThanOrEqual(6)

    const localIds = contributions.map((c) => c.localId)
    expect(localIds.slice(0, 6)).toEqual([
      'overview', 'sessions', 'hooks', 'agents', 'uploads', 'logs',
    ])
  })

  // Test 4: Extra registered contribution (order=100) → URL renders its body
  it('T4: extra registered contribution appears in registry and renders its body', () => {
    const FakeBody = () => <div data-testid="fake-body">FAKE_CONTENT</div>
    registerSettingsContribution({
      moduleId: 'fakemod',
      id: 'fakemod.custom-host-section',
      localId: 'custom-host-section',
      scope: 'host',
      order: 100,
      labelKey: 'custom-host-section',
      component: FakeBody,
    })

    renderHostPage('/hosts/hA/custom-host-section')

    // Fake body is rendered via registry
    expect(screen.getByTestId('fake-body')).toBeInTheDocument()

    // Registry now has 7 contributions (6 built-in + 1 fake)
    expect(listContributions('host').length).toBeGreaterThanOrEqual(7)
  })

  // Test 5: Fake contribution receives ctx with scope==='host' && hostId===HOST_A
  it('T5: fake contribution receives ctx.scope="host" and ctx.hostId=HOST_A', () => {
    // Use a mutable container array to capture ctx without reassigning a let binding
    // (avoids react-hooks/globals lint error for closed-over mutation in component body).
    const capturedCtx: Array<SettingsContextFor<'host'>> = []

    const FakeBodyWithCtx = ({ ctx }: { ctx: SettingsContextFor<'host'> }) => {
      capturedCtx.push(ctx)
      return <div data-testid="fake-body-ctx">CTX_RECEIVED</div>
    }

    registerSettingsContribution({
      moduleId: 'fakectx',
      id: 'fakectx.ctx-probe',
      localId: 'ctx-probe',
      scope: 'host',
      order: 100,
      labelKey: 'ctx-probe',
      component: FakeBodyWithCtx,
    })

    renderHostPage('/hosts/hA/ctx-probe')

    expect(screen.getByTestId('fake-body-ctx')).toBeInTheDocument()
    expect(capturedCtx.length).toBeGreaterThan(0)
    expect(capturedCtx[0].scope).toBe('host')
    expect(capturedCtx[0].hostId).toBe(HOST_A)
  })

  // Test 6: URL /hosts/hA/nonexistent → redirects to canonical path
  it('T6: /hosts/hA/nonexistent redirects to canonical (overview)', () => {
    const { mem } = renderHostPage('/hosts/hA/nonexistent')

    // resolveSelection self-heals to the first contribution's localId
    const history = mem.history
    const lastPath = history[history.length - 1]
    // Should redirect to the fallback sub-page (overview = first built-in)
    expect(lastPath).toBe('/hosts/hA/overview')
  })

  // Test 7: Disabled contribution → body NOT rendered + URL redirects.
  // NOTE: HostSidebar is mocked in this file, so we can only assert the body
  // absence here. The disabled-row sidebar styling (data-disabled-ctx) is tested
  // in HostSidebar.test.tsx which renders the real sidebar with the registry.
  it('T7: disabled fake contribution does NOT render body when navigated to', () => {
    const DisabledBody = () => <div data-testid="disabled-body">DISABLED_CONTENT</div>

    registerSettingsContribution({
      moduleId: 'fakemod',
      id: 'fakemod.disabled-section',
      localId: 'disabled-section',
      scope: 'host',
      order: 100,
      labelKey: 'disabled-section',
      component: DisabledBody,
      disabled: () => true,
      disabledReasonKey: 'settings.coming_soon',
    })

    // Navigate to the disabled section's URL
    const { mem } = renderHostPage('/hosts/hA/disabled-section')

    // Body must NOT be rendered (disabled contribution skipped by HostPage.renderContent)
    expect(screen.queryByTestId('disabled-body')).not.toBeInTheDocument()
    // URL must self-heal to the first selectable sub-page (overview)
    expect(currentPath(mem)).toBe('/hosts/hA/overview')
    // The selectable overview renders
    expect(screen.getByTestId('overview-section')).toBeInTheDocument()
  })

  // ──────────────────────────────────────────────────────────────────────────
  // §A.7 — Finding A regression tests (R1+R2 fixup)
  // ──────────────────────────────────────────────────────────────────────────

  // A.7 Test 1: Direct deep-link to disabled sub-page redirects to first selectable.
  it('A.7-1: disabled deep-link redirects to first selectable sub-page', () => {
    registerSettingsContribution({
      moduleId: 'fakemod',
      id: 'fakemod.disabled-section',
      localId: 'disabled-section',
      scope: 'host',
      order: 100,
      labelKey: 'disabled-section',
      component: () => <div data-testid="disabled-body" />,
      disabled: () => true,
    })

    const { mem } = renderHostPage('/hosts/hA/disabled-section')

    // URL must canonicalize to the first selectable page (overview)
    expect(currentPath(mem)).toBe('/hosts/hA/overview')
    // OverviewSection renders — not blank
    expect(screen.getByTestId('overview-section')).toBeInTheDocument()
    expect(screen.queryByTestId('disabled-body')).not.toBeInTheDocument()
  })

  // A.7 Test 2: lastSelection clamped when contribution removed from registry.
  it('A.7-2: removed contribution in lastSelection falls back to overview, not stale sub-page', () => {
    // Step 1: register fake custom contribution, navigate to it → seeds lastSelection
    registerSettingsContribution({
      moduleId: 'fakemod',
      id: 'fakemod.custom',
      localId: 'custom',
      scope: 'host',
      order: 100,
      labelKey: 'custom',
      component: () => <div data-testid="custom-body" />,
    })

    const firstMount = renderHostPage('/hosts/hA/custom')
    expect(screen.getByTestId('custom-body')).toBeInTheDocument()
    firstMount.unmount()

    // Step 2: clear all, re-register only built-ins (no 'custom')
    clearContributions()
    clearModuleRegistry()
    registerBuiltinModules()

    // Step 3: render bare /hosts — lastSelection.subPage='custom' must be ignored
    const { mem } = renderHostPage('/hosts')

    // Must fall back to 'overview', not the stale 'custom'
    expect(screen.getByTestId('overview-section')).toBeInTheDocument()
    expect(screen.queryByTestId('custom-body')).not.toBeInTheDocument()
    // URL must have been redirected to overview (not custom which no longer exists)
    const lastPath = currentPath(mem)
    expect(lastPath).toBe('/hosts/hA/overview')
  })

  // A.7 Test 3: Cross-host switch with per-host disabled → redirects to first selectable.
  it('A.7-3: cross-host switch to host where current sub-page is disabled redirects', () => {
    const HOST_B = 'hB'
    // Seed two hosts
    useHostStore.setState({
      hosts: {
        [HOST_A]: { id: HOST_A, name: 'Host A', ip: '1.2.3.4', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'Host B', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_A, HOST_B],
      activeHostId: HOST_A,
      runtime: {},
    })

    // Register featureX: enabled on hA, disabled on hB
    registerSettingsContribution({
      moduleId: 'fakemod',
      id: 'fakemod.feature-x',
      localId: 'feature-x',
      scope: 'host',
      order: 100,
      labelKey: 'feature-x',
      component: () => <div data-testid="feature-x-body" />,
      disabled: (ctx) => ctx.hostId === HOST_B,
    })

    // Start on hA/feature-x (enabled)
    const { mem, rerender } = renderHostPage('/hosts/hA/feature-x')
    expect(screen.getByTestId('feature-x-body')).toBeInTheDocument()

    // Simulate cross-host switch to hB/feature-x (disabled on hB)
    // Navigate directly to hB/feature-x — resolveSelection must redirect
    mem.navigate('/hosts/hB/feature-x')
    rerender(
      <Router hook={mem.hook}>
        <HostPage pane={hostPane} isActive />
      </Router>,
    )

    // URL must canonicalize to hB's first selectable page (overview)
    expect(currentPath(mem)).toBe('/hosts/hB/overview')
    // Overview renders for hB — not blank
    expect(screen.getByTestId('overview-section')).toHaveAttribute('data-host', HOST_B)
    expect(screen.queryByTestId('feature-x-body')).not.toBeInTheDocument()
  })

  // A.7 Test 4: lastSelection sub-page clamped on read when not in live registry.
  it('A.7-4: stale lastSelection subPage not in registry is clamped to overview on render', () => {
    // Seed lastSelection with a sub-page that is no longer in the registry
    // by mounting with it, then removing it before the next render.
    registerSettingsContribution({
      moduleId: 'fakemod',
      id: 'fakemod.gone',
      localId: 'gone',
      scope: 'host',
      order: 100,
      labelKey: 'gone',
      component: () => <div data-testid="gone-body" />,
    })

    const first = renderHostPage('/hosts/hA/gone')
    expect(screen.getByTestId('gone-body')).toBeInTheDocument()
    first.unmount()

    // Remove 'gone' from the registry
    clearContributions()
    clearModuleRegistry()
    registerBuiltinModules()
    // 'gone' is no longer registered.

    // Render with no URL sub-page — bare /hosts
    const { mem } = renderHostPage('/hosts')

    // lastSelection.subPage = 'gone' must be clamped — overview must render
    expect(screen.getByTestId('overview-section')).toBeInTheDocument()
    expect(screen.queryByTestId('gone-body')).not.toBeInTheDocument()
    // URL settles on overview (or the first selectable in the canonicalized path)
    const lastPath = currentPath(mem)
    expect(lastPath).toBe('/hosts/hA/overview')
  })
})
