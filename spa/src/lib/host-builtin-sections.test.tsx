/**
 * host-builtin-sections.test.tsx
 *
 * Covers two layers:
 *
 * 1. The PR-4 §3.3 baseline — `registerBuiltinModules()` registers the six
 *    expected host-scoped contributions with the correct moduleId / order
 *    / wrap semantics / scope guard.
 *
 * 2. The #586 follow-up contracts on the new batch-replace source-map API
 *    (`setHostBuiltinSections`, `getHostBuiltinDeclarations`,
 *    `clearHostBuiltinSources`):
 *    - full-replace semantics (dropped localIds disappear),
 *    - wrapper identity stable per localId across re-calls (incl. HMR-style
 *      component-reference swap),
 *    - wrapper delegates to the latest component reference,
 *    - re-add after drop rebuilds the wrapper,
 *    - clear empties the source map.
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

// Mock OverviewSection so the §3.3 render-level tests can render the wrapper
// without pulling in zustand / HTTP dependencies.  The stub is a named export
// matching the real module's export shape.
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
  clearHostBuiltinSources,
  getHostBuiltinDeclarations,
  hostBuiltinComponentMap,
  HOST_BUILTIN_MODULE_ID,
  setHostBuiltinSections,
  type HostBuiltinSectionDef,
} from './host-builtin-sections'
import { dispatchSettingsContributions } from './dispatch-settings-contributions'
import type { HostRuntime } from '../stores/useHostStore'

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
  clearHostBuiltinSources()
}

// ---------- §3.3 baseline (PR-4) ------------------------------------------

describe('§3.3 — registerBuiltinModules: built-in host sub-page contributions', () => {
  beforeEach(() => {
    clearAll()
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearAll()
  })

  it('registers exactly 6 host-scoped contributions with moduleId _builtin.host', () => {
    registerBuiltinModules()
    const hostContribs = listContributions('host')
    expect(hostContribs).toHaveLength(6)
    for (const c of hostContribs) {
      expect(c.moduleId).toBe(HOST_BUILTIN_MODULE_ID)
    }
  })

  it('orders contributions as: overview, sessions, hooks, agents, uploads, logs', () => {
    registerBuiltinModules()
    const hostContribs = listContributions('host')
    const localIds = hostContribs.map((c) => c.localId)
    expect(localIds).toEqual(['overview', 'sessions', 'hooks', 'agents', 'uploads', 'logs'])
  })

  it('renders OverviewSection with forwarded hostId for host ctx', () => {
    registerBuiltinModules()
    const overview = listContributions('host').find((c) => c.localId === 'overview')
    expect(overview).toBeDefined()

    const Wrapper = overview!.component as React.ComponentType<{
      ctx: { scope: 'host'; hostId: string; runtime: HostRuntime | undefined }
    }>
    const { getByTestId } = render(<Wrapper ctx={{ scope: 'host', hostId: 'hA', runtime: undefined }} />)
    const node = getByTestId('overview-stub')
    expect(node.textContent).toBe('overview-for-hA')
  })

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
        contrib!.component as React.ComponentType<{ ctx: { scope: 'host'; hostId: string; runtime: HostRuntime | undefined } }>,
      )
      expect(original, `'${localId}' must wrap the correct section`).toBe(component)
    }
  })

  it('scope guard: wrapper returns null when ctx.scope is not "host"', () => {
    registerBuiltinModules()
    const hostContribs = listContributions('host')
    const overviewContrib = hostContribs.find((c) => c.localId === 'overview')
    expect(overviewContrib).toBeDefined()

    const Wrapper = overviewContrib!.component as React.ComponentType<{
      ctx: { scope: string; workspaceId?: string }
    }>

    const { container } = render(
      <Wrapper ctx={{ scope: 'workspace', workspaceId: 'wA' }} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('all contributions have correct scope, valid localIds, and labelKey starting with "hosts."', () => {
    registerBuiltinModules()
    const hostContribs = listContributions('host')

    for (const c of hostContribs) {
      expect(c.scope).toBe('host')
      expect(c.id).toBe(`${HOST_BUILTIN_MODULE_ID}.${c.localId}`)
      expect(c.labelKey).toMatch(/^hosts\./)
    }
  })

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

// ---------- #586 follow-up: batch-replace source map ----------------------

describe('#586 — setHostBuiltinSections (batch-replace source map)', () => {
  beforeEach(() => {
    clearHostBuiltinSources()
    clearContributions()
  })

  afterEach(() => {
    clearHostBuiltinSources()
    clearContributions()
  })

  function makeSection(localId: string, render?: (hostId: string) => React.ReactNode): HostBuiltinSectionDef {
    const Section: React.FC<{ hostId: string }> = ({ hostId }) =>
      <div data-testid={`section-${localId}`}>{render ? render(hostId) : `${localId}-${hostId}`}</div>
    Section.displayName = `Section_${localId}`
    return { localId, labelKey: `hosts.${localId}`, order: 0, component: Section }
  }

  // Test 1 (spec §6.1 #1): batch-replace defines the exact set; absent localIds are dropped.
  it('Test 1 — batch replace is the full set; absent localIds are dropped', () => {
    setHostBuiltinSections([makeSection('a'), makeSection('b'), makeSection('c')])
    expect(getHostBuiltinDeclarations().map((d) => d.localId).sort()).toEqual(['a', 'b', 'c'])

    setHostBuiltinSections([makeSection('a'), makeSection('d')])
    expect(getHostBuiltinDeclarations().map((d) => d.localId).sort()).toEqual(['a', 'd'])
  })

  // Test 2 (spec §6.1 #2): wrapper identity stable across re-calls with same localIds (HMR).
  it('Test 2 — wrapper identity stable across re-calls with the same localIds (HMR)', () => {
    setHostBuiltinSections([makeSection('a')])
    const firstWrapped = getHostBuiltinDeclarations()[0].component

    // Simulate HMR: a fresh section component reference for the same localId.
    setHostBuiltinSections([makeSection('a')])
    const secondWrapped = getHostBuiltinDeclarations()[0].component

    expect(secondWrapped).toBe(firstWrapped)
  })

  // Test 3 (spec §6.1 #3): wrapper delegates to the latest *committed* component.
  // Post-R2 A1 design — wrappers read liveSources, which is only updated by
  // commitHostBuiltinSources() (called from dispatchSettingsContributions
  // Phase 2).  This test triggers a commit by calling dispatch directly.
  it('Test 3 — wrapper delegates to the latest committed section component', () => {
    const first = vi.fn(({ hostId }: { hostId: string }) => <div data-testid="impl">v1-{hostId}</div>)
    const second = vi.fn(({ hostId }: { hostId: string }) => <div data-testid="impl">v2-{hostId}</div>)

    setHostBuiltinSections([{ localId: 'a', labelKey: 'hosts.a', order: 0, component: first }])
    dispatchSettingsContributions()  // commit staged → live
    const Wrapper = getHostBuiltinDeclarations()[0].component as React.ComponentType<{ ctx: { scope: 'host'; hostId: string; runtime: HostRuntime | undefined } }>
    const { getByTestId, rerender } = render(<Wrapper ctx={{ scope: 'host', hostId: 'hA', runtime: undefined }} />)
    expect(getByTestId('impl').textContent).toBe('v1-hA')
    expect(first).toHaveBeenCalled()

    // Swap the underlying section component (HMR style); commit before re-render.
    setHostBuiltinSections([{ localId: 'a', labelKey: 'hosts.a', order: 0, component: second }])
    dispatchSettingsContributions()
    rerender(<Wrapper ctx={{ scope: 'host', hostId: 'hA', runtime: undefined }} />)
    expect(getByTestId('impl').textContent).toBe('v2-hA')
    expect(second).toHaveBeenCalled()
  })

  // Test 4 (post-R2 design): wrapper identity is stable across drop+re-add
  // because wrapperCache is keyed by localId and only cleared by
  // clearHostBuiltinSources(). This is a STRONGER stability invariant than
  // the pre-R2 design — a section file's wrapper component does not change
  // identity even after a transient absence from the staged set.
  it('Test 4 — wrapper identity stays stable across drop and re-add (post-R2 stronger contract)', () => {
    setHostBuiltinSections([makeSection('a')])
    const before = getHostBuiltinDeclarations()[0].component

    setHostBuiltinSections([])  // drop
    expect(getHostBuiltinDeclarations()).toEqual([])

    setHostBuiltinSections([makeSection('a')])
    const after = getHostBuiltinDeclarations()[0].component
    expect(after).toBe(before)  // SAME wrapper reference

    // clearHostBuiltinSources releases the wrapper cache; subsequent
    // re-add gets a fresh wrapper.
    clearHostBuiltinSources()
    setHostBuiltinSections([makeSection('a')])
    const afterClear = getHostBuiltinDeclarations()[0].component
    expect(afterClear).not.toBe(before)
  })

  // Test 5 (spec §6.1 #5): clearHostBuiltinSources empties the source map.
  it('Test 5 — clearHostBuiltinSources empties the source map', () => {
    setHostBuiltinSections([makeSection('a'), makeSection('b')])
    expect(getHostBuiltinDeclarations()).toHaveLength(2)
    clearHostBuiltinSources()
    expect(getHostBuiltinDeclarations()).toEqual([])
  })

  // Bonus: declaration shape is correctly carried through.
  it('declaration shape includes scope/order/labelKey from the source def', () => {
    setHostBuiltinSections([
      { localId: 'x', labelKey: 'hosts.x', order: 7, component: OverviewSection },
    ])
    const decls = getHostBuiltinDeclarations()
    expect(decls).toHaveLength(1)
    expect(decls[0]).toMatchObject({ localId: 'x', labelKey: 'hosts.x', order: 7, scope: 'host' })
  })

  // R2 attacker/defender/file-health A1 regression — staged changes do not
  // leak to wrappers until commitHostBuiltinSources() (called from dispatch)
  // runs.  A failed Phase 1 leaves liveSources untouched, so wrapper.render
  // continues to use the previously committed component / null.
  it('R2 A1 regression — wrapper renders previous committed source until dispatch commits new staged set', () => {
    const v1 = vi.fn(({ hostId }: { hostId: string }) => <div data-testid="impl">v1-{hostId}</div>)
    const v2 = vi.fn(({ hostId }: { hostId: string }) => <div data-testid="impl">v2-{hostId}</div>)

    setHostBuiltinSections([{ localId: 'a', labelKey: 'hosts.a', order: 0, component: v1 }])
    dispatchSettingsContributions()  // commit v1
    const Wrapper = getHostBuiltinDeclarations()[0].component as React.ComponentType<{ ctx: { scope: 'host'; hostId: string; runtime: HostRuntime | undefined } }>

    // Stage v2 BUT do not commit; render — must show v1 (committed) not v2 (staged).
    setHostBuiltinSections([{ localId: 'a', labelKey: 'hosts.a', order: 0, component: v2 }])
    const { getByTestId, rerender } = render(<Wrapper ctx={{ scope: 'host', hostId: 'hA', runtime: undefined }} />)
    expect(getByTestId('impl').textContent).toBe('v1-hA')
    expect(v2).not.toHaveBeenCalled()

    // After dispatch commits, wrapper sees v2.
    dispatchSettingsContributions()
    rerender(<Wrapper ctx={{ scope: 'host', hostId: 'hA', runtime: undefined }} />)
    expect(getByTestId('impl').textContent).toBe('v2-hA')
    expect(v2).toHaveBeenCalled()
  })

  it('R2 A1 regression — dispatch failure leaves committed liveSources intact', () => {
    const v1 = ({ hostId }: { hostId: string }) => <div data-testid="impl">v1-{hostId}</div>
    setHostBuiltinSections([{ localId: 'a', labelKey: 'hosts.a', order: 0, component: v1 }])
    dispatchSettingsContributions()
    const Wrapper = getHostBuiltinDeclarations()[0].component as React.ComponentType<{ ctx: { scope: 'host'; hostId: string; runtime: HostRuntime | undefined } }>
    const initial = render(<Wrapper ctx={{ scope: 'host', hostId: 'hA', runtime: undefined }} />)
    expect(initial.getByTestId('impl').textContent).toBe('v1-hA')
    initial.unmount()

    // Stage a malformed set that will fail Phase 1 validation
    // (uppercase localId fails SETTINGS_LOCAL_ID_RE).
    const v2 = ({ hostId }: { hostId: string }) => <div data-testid="impl">v2-{hostId}</div>
    setHostBuiltinSections([{ localId: 'a', labelKey: 'hosts.a', order: 0, component: v2 }])
    expect(() =>
      // localId fails SETTINGS_LOCAL_ID_RE — assertValidSettingsContribution throws
      // during Phase 1.  The cast bypasses the static check that normally
      // protects this — we want to simulate runtime validation failure.
      dispatchSettingsContributions([
        {
          id: 'broken',
          name: 'Broken',
          settings: [
            { localId: 'BadID', scope: 'host', order: 0, labelKey: 'x', component: v2 },
          ],
        },
      ] as unknown as Parameters<typeof dispatchSettingsContributions>[0]),
    ).toThrow()

    // liveSources['a'] still maps to v1 — wrapper render unchanged.  Render
    // a fresh instance to prove the live state.
    const after = render(<Wrapper ctx={{ scope: 'host', hostId: 'hA', runtime: undefined }} />)
    expect(after.getByTestId('impl').textContent).toBe('v1-hA')
    after.unmount()
  })
})
