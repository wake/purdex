/**
 * §3.3 — Built-in host sub-page adapter tests (PR-4 Commit 1)
 *
 * Verifies that `registerBuiltinModules()` registers exactly six host-scoped
 * contributions with the correct moduleId, order, wrap semantics, and scope
 * guard.
 *
 * Test design — Test 3 (render-level props forwarding):
 *   OverviewSection is swapped for a spy stub via vi.mock so zustand/HTTP
 *   dependencies are never loaded.  The wrapper is rendered with a real host
 *   ctx and the resulting DOM is asserted — proving (a) the wrapper renders,
 *   (b) the underlying section component is invoked, (c) hostId is forwarded.
 *
 *   The "all six identity" extra test uses the WeakMap as a complementary
 *   structural check and keeps that coverage without duplicating the render
 *   assertion for every section.
 *
 *   Test 4 (scope guard) renders the wrapper component directly with a wrong-
 *   scope ctx and asserts the output is null — the wrapper is a pure function
 *   with no external dependencies, so no mocks are needed.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render } from '@testing-library/react'
import React from 'react'

// Mock icon-path-cache — required by register-modules (phosphor weight loader)
vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

// Mock OverviewSection so Test 3 can render the wrapper without pulling in
// zustand / HTTP dependencies.  The stub is a named export (matching the
// real module's export shape) and renders identifiable content that includes
// the forwarded hostId prop.
vi.mock('../components/hosts/OverviewSection', () => ({
  OverviewSection: vi.fn(({ hostId }: { hostId: string }) => (
    <div data-testid="overview-stub">overview-for-{hostId}</div>
  )),
}))

import {
  clearModuleRegistry,
} from './module-registry'
import { clearNewTabRegistry } from './new-tab-registry'
import {
  clearSettingsSectionRegistry,
} from './settings-section-registry'
import { clearInterfaceSubsectionRegistry } from './interface-subsection-registry'
import { registerBuiltinModules } from './register-modules'
import {
  clearContributions,
  listContributions,
} from './settings-contribution-registry'
import {
  clearHostBuiltinPending,
  hostBuiltinComponentMap,
  HOST_BUILTIN_MODULE_ID,
} from './host-builtin-sections'

// Import the six section components so we can verify identity (extra test).
import { OverviewSection } from '../components/hosts/OverviewSection'
import { SessionsSection } from '../components/hosts/SessionsSection'
import { HooksSection } from '../components/hosts/HooksSection'
import { AgentsSection } from '../components/hosts/AgentsSection'
import { UploadSection } from '../components/hosts/UploadSection'
import { LogsSection } from '../components/hosts/LogsSection'

function clearAll() {
  clearModuleRegistry()
  clearNewTabRegistry()
  clearSettingsSectionRegistry()
  clearInterfaceSubsectionRegistry()
  clearContributions()
  clearHostBuiltinPending()
}

describe('§3.3 — registerBuiltinModules: built-in host sub-page contributions', () => {
  beforeEach(() => {
    clearAll()
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearAll()
  })

  // Test 1: After registerBuiltinModules(), listContributions('host') contains
  // exactly 6 items, all with moduleId === '_builtin.host'
  it('registers exactly 6 host-scoped contributions with moduleId _builtin.host', () => {
    registerBuiltinModules()
    const hostContribs = listContributions('host')
    expect(hostContribs).toHaveLength(6)
    for (const c of hostContribs) {
      expect(c.moduleId).toBe(HOST_BUILTIN_MODULE_ID)
    }
  })

  // Test 2: Order is exactly overview → sessions → hooks → agents → uploads → logs
  it('orders contributions as: overview, sessions, hooks, agents, uploads, logs', () => {
    registerBuiltinModules()
    const hostContribs = listContributions('host')
    const localIds = hostContribs.map((c) => c.localId)
    expect(localIds).toEqual(['overview', 'sessions', 'hooks', 'agents', 'uploads', 'logs'])
  })

  // Test 3: The contribution's component wraps OverviewSection and forwards hostId.
  // Renders the wrapper with a real host ctx and asserts on the stub's DOM output,
  // proving (a) wrapper renders, (b) section is invoked, (c) hostId is forwarded.
  it('renders OverviewSection with forwarded hostId for host ctx', () => {
    registerBuiltinModules()
    const overview = listContributions('host').find((c) => c.localId === 'overview')
    expect(overview).toBeDefined()

    const Wrapper = overview!.component as React.ComponentType<{
      ctx: { scope: 'host'; hostId: string }
    }>
    const { getByTestId } = render(<Wrapper ctx={{ scope: 'host', hostId: 'hA' }} />)
    const node = getByTestId('overview-stub')
    expect(node.textContent).toBe('overview-for-hA')
  })

  // Bonus identity checks for the remaining five sections (keeps Test 3's spirit).
  it('wraps all six sections to their original components', () => {
    registerBuiltinModules()
    const hostContribs = listContributions('host')

    const expected = [
      { localId: 'overview', component: OverviewSection },
      { localId: 'sessions', component: SessionsSection },
      { localId: 'hooks', component: HooksSection },
      { localId: 'agents', component: AgentsSection },
      { localId: 'uploads', component: UploadSection },
      { localId: 'logs', component: LogsSection },
    ]

    for (const { localId, component } of expected) {
      const contrib = hostContribs.find((c) => c.localId === localId)
      expect(contrib, `contribution for '${localId}' must exist`).toBeDefined()
      const original = hostBuiltinComponentMap.get(
        contrib!.component as React.ComponentType<{ ctx: { scope: 'host'; hostId: string } }>,
      )
      expect(original, `'${localId}' must wrap the correct section`).toBe(component)
    }
  })

  // Test 4: Scope guard — render with ctx.scope !== 'host' returns null.
  it('scope guard: wrapper returns null when ctx.scope is not "host"', () => {
    registerBuiltinModules()
    const hostContribs = listContributions('host')
    const overviewContrib = hostContribs.find((c) => c.localId === 'overview')
    expect(overviewContrib).toBeDefined()

    const Wrapper = overviewContrib!.component as React.ComponentType<{
      ctx: { scope: string; workspaceId?: string }
    }>

    // Render with a workspace ctx — must produce null (no DOM nodes).
    // The guard is a single `!== 'host'` check, so any non-host scope suffices.
    const { container } = render(
      <Wrapper ctx={{ scope: 'workspace', workspaceId: 'wA' }} />,
    )
    expect(container.firstChild).toBeNull()
  })

  // Structural checks on all contributions.
  it('all contributions have correct scope, valid localIds, and labelKey starting with "hosts."', () => {
    registerBuiltinModules()
    const hostContribs = listContributions('host')

    for (const c of hostContribs) {
      expect(c.scope).toBe('host')
      expect(c.id).toBe(`${HOST_BUILTIN_MODULE_ID}.${c.localId}`)
      expect(c.labelKey).toMatch(/^hosts\./)
    }
  })

  // HMR safety: re-running registerBuiltinModules does not accumulate duplicates.
  it('is HMR-safe: re-running registerBuiltinModules after clearAll does not duplicate', () => {
    registerBuiltinModules()
    const first = listContributions('host').length
    clearAll()
    registerBuiltinModules()
    const second = listContributions('host').length
    expect(first).toBe(6)
    expect(second).toBe(6)
  })
})
