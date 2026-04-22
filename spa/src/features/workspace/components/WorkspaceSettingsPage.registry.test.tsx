import { vi } from 'vitest'

vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { useState } from 'react'
import { WorkspaceSettingsPage } from './WorkspaceSettingsPage'
import { useWorkspaceStore } from '../store'
import {
  clearContributions,
  registerSettingsContribution,
} from '../../../lib/settings-contribution-registry'
import type {
  SettingsContext,
  SettingsContextFor,
} from '../../../lib/settings-contribution-types'

// --------------------------------------------------------------------------
// Shared harness
// --------------------------------------------------------------------------

function makeWorkspace(name = 'Test WS'): string {
  const ws = useWorkspaceStore.getState().addWorkspace(name)
  return ws.id
}

describe('WorkspaceSettingsPage — workspace-scoped registry rendering', () => {
  let wsId: string

  beforeEach(() => {
    cleanup()
    useWorkspaceStore.getState().reset()
    clearContributions()
    wsId = makeWorkspace('Test WS')
  })

  afterEach(() => {
    clearContributions()
  })

  it('baseline: renders icon picker + name input + danger zone with no registry block when no workspace contributions', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    // Existing page elements still render
    expect(screen.getByDisplayValue('Test WS')).toBeInTheDocument()
    expect(screen.getByTestId('delete-workspace-btn')).toBeInTheDocument()
    // No registry-driven contribution present → no body labels from fake
    expect(screen.queryByText('NO_CONTRIBUTION_MARKER')).toBeNull()
  })

  it('renders a single registered workspace-scoped contribution (label + body)', () => {
    const Fake = () => <div>FAKE_WS_BODY</div>
    registerSettingsContribution({
      moduleId: 'fakews',
      id: 'fakews.primary',
      localId: 'primary',
      scope: 'workspace',
      order: 0,
      labelKey: 'FAKE_WS_LABEL',
      component: Fake,
    })

    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    expect(screen.getByText('FAKE_WS_LABEL')).toBeInTheDocument()
    expect(screen.getByText('FAKE_WS_BODY')).toBeInTheDocument()
  })

  it('injects ctx with scope="workspace" and matching workspaceId into the contribution component', () => {
    const captured: SettingsContext[] = []
    const Fake = ({ ctx }: { ctx: SettingsContextFor<'workspace'> }) => {
      captured.push(ctx)
      return <div>CTX_BODY</div>
    }
    registerSettingsContribution({
      moduleId: 'ctxprobe',
      id: 'ctxprobe.main',
      localId: 'main',
      scope: 'workspace',
      order: 0,
      labelKey: 'CTX_LABEL',
      component: Fake,
    })

    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    expect(captured.length).toBeGreaterThan(0)
    expect(captured[0]).toEqual({ scope: 'workspace', workspaceId: wsId })
  })

  it('renders multiple contributions in ascending order (5, 10, 20)', () => {
    const A = () => <div data-testid="body-a">A_BODY</div>
    const B = () => <div data-testid="body-b">B_BODY</div>
    const C = () => <div data-testid="body-c">C_BODY</div>

    // Registration order deliberately unsorted to prove sorting happens.
    registerSettingsContribution({
      moduleId: 'mm', id: 'mm.b', localId: 'b', scope: 'workspace',
      order: 10, labelKey: 'LABEL_B', component: B,
    })
    registerSettingsContribution({
      moduleId: 'mm', id: 'mm.c', localId: 'c', scope: 'workspace',
      order: 20, labelKey: 'LABEL_C', component: C,
    })
    registerSettingsContribution({
      moduleId: 'mm', id: 'mm.a', localId: 'a', scope: 'workspace',
      order: 5, labelKey: 'LABEL_A', component: A,
    })

    const { container } = render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const headers = Array.from(container.querySelectorAll('h3'))
      .map((h) => h.textContent?.trim())
      .filter((t): t is string => t !== undefined)
    // Labels should appear in registered-order 5 / 10 / 20 → A / B / C
    const idxA = headers.indexOf('LABEL_A')
    const idxB = headers.indexOf('LABEL_B')
    const idxC = headers.indexOf('LABEL_C')
    expect(idxA).toBeGreaterThanOrEqual(0)
    expect(idxB).toBeGreaterThan(idxA)
    expect(idxC).toBeGreaterThan(idxB)
  })

  // F2 — PR-2 disabled-row pattern: header visible (data-disabled-ctx="true")
  // but body is not mounted, and `disabledReasonKey` surfaces via the
  // `title` attribute on the section header. Mirrors SettingsSidebar's
  // disabled-by-ctx behavior so the workspace shell honors the same
  // contract across scopes.
  it('F2: renders disabled(ctx)=true sections as a disabled header, skips body, surfaces reason', () => {
    const Alive = () => <div>ALIVE_BODY</div>
    const Dead = () => <div>DEAD_BODY</div>
    registerSettingsContribution({
      moduleId: 'm', id: 'm.alive', localId: 'alive', scope: 'workspace',
      order: 0, labelKey: 'ALIVE_LABEL', component: Alive,
    })
    registerSettingsContribution({
      moduleId: 'm', id: 'm.dead', localId: 'dead', scope: 'workspace',
      order: 1, labelKey: 'DEAD_LABEL', component: Dead,
      disabled: () => true,
      disabledReasonKey: 'm.dead.reason',
    })

    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    // Enabled row: header + body.
    expect(screen.getByText('ALIVE_LABEL')).toBeInTheDocument()
    expect(screen.getByText('ALIVE_BODY')).toBeInTheDocument()
    // Disabled row: header present, body absent.
    const disabledHeader = screen.getByText('DEAD_LABEL')
    expect(disabledHeader).toBeInTheDocument()
    expect(screen.queryByText('DEAD_BODY')).toBeNull()
    // Disabled marker exposed on the section wrapper, and title carries the
    // (unresolved — fallback is the key itself) i18n key.
    const section = disabledHeader.closest('[data-section]') as HTMLElement | null
    expect(section).not.toBeNull()
    expect(section!.getAttribute('data-disabled-ctx')).toBe('true')
    expect(section!.getAttribute('title')).toBe('m.dead.reason')
  })

  // F1 regression — disabled() must be re-evaluated per render so reactive
  // state changes flip the row live. The previous implementation memoized
  // the filtered list against ctx only (stable per workspaceId), which
  // froze the disabled state at initial mount.
  it('F1: re-evaluates disabled(ctx) on every render (no stale memoization)', () => {
    let gate = false
    const Body = () => <div>REACTIVE_BODY</div>
    registerSettingsContribution({
      moduleId: 'r', id: 'r.main', localId: 'main', scope: 'workspace',
      order: 0, labelKey: 'REACTIVE_LABEL', component: Body,
      // Closure reads the live flag each invocation.
      disabled: () => gate,
    })

    const { rerender } = render(<WorkspaceSettingsPage workspaceId={wsId} />)
    // Initially enabled: body mounted.
    expect(screen.getByText('REACTIVE_BODY')).toBeInTheDocument()

    // Flip the gate — simulate reactive state (store write) without
    // changing workspaceId. A rerender must re-ask disabled(ctx).
    gate = true
    rerender(<WorkspaceSettingsPage workspaceId={wsId} />)
    // Body gone, label still rendered as disabled-row.
    expect(screen.queryByText('REACTIVE_BODY')).toBeNull()
    expect(screen.getByText('REACTIVE_LABEL')).toBeInTheDocument()
  })

  // F4 regression — when workspaceId changes, per-section subtrees must
  // remount so any local state seeded from ctx.workspaceId does not leak
  // across workspaces. Keying by `${workspaceId}:${c.id}` achieves this
  // without disturbing intra-workspace re-renders.
  it('F4: remounts contribution subtree when workspaceId changes (no cross-workspace state leak)', () => {
    // Captures ctx.workspaceId into local state on mount — a common pattern
    // for contributions that want an initial value tied to the active
    // workspace. Without a workspaceId-aware key, React reuses the same
    // instance and the initial stays pinned to the first workspace.
    const Stateful = ({ ctx }: { ctx: SettingsContextFor<'workspace'> }) => {
      const [initial] = useState(ctx.workspaceId)
      return <div data-testid="initial-ws">{initial}</div>
    }
    registerSettingsContribution({
      moduleId: 'f4', id: 'f4.probe', localId: 'probe', scope: 'workspace',
      order: 0, labelKey: 'F4_LABEL', component: Stateful,
    })

    const wsB = makeWorkspace('Second F4 WS')

    const { rerender } = render(<WorkspaceSettingsPage workspaceId={wsId} />)
    expect(screen.getByTestId('initial-ws').textContent).toBe(wsId)

    rerender(<WorkspaceSettingsPage workspaceId={wsB} />)
    // After workspace swap the subtree remounted → new useState seed.
    expect(screen.getByTestId('initial-ws').textContent).toBe(wsB)
  })

  it('propagates workspaceId into ctx when the prop changes (re-render yields fresh ctx)', () => {
    const captured: SettingsContext[] = []
    const Probe = ({ ctx }: { ctx: SettingsContextFor<'workspace'> }) => {
      captured.push(ctx)
      return <div>PROBE_BODY</div>
    }
    registerSettingsContribution({
      moduleId: 'probe', id: 'probe.main', localId: 'main', scope: 'workspace',
      order: 0, labelKey: 'PROBE_LABEL', component: Probe,
    })

    const wsId2 = makeWorkspace('Second WS')

    const { rerender } = render(<WorkspaceSettingsPage workspaceId={wsId} />)
    expect(captured.at(-1)).toEqual({ scope: 'workspace', workspaceId: wsId })

    rerender(<WorkspaceSettingsPage workspaceId={wsId2} />)
    expect(captured.at(-1)).toEqual({ scope: 'workspace', workspaceId: wsId2 })
  })

  it('purdex-scoped and host-scoped contributions do NOT appear in the workspace page', () => {
    const Purdex = () => <div>PURDEX_BODY</div>
    const HostC = () => <div>HOST_BODY</div>
    registerSettingsContribution({
      moduleId: 'x', id: 'x.p', localId: 'p', scope: 'purdex',
      order: 0, labelKey: 'PURDEX_LABEL', component: Purdex,
    })
    registerSettingsContribution({
      moduleId: 'x', id: 'x.h', localId: 'h', scope: 'host',
      order: 0, labelKey: 'HOST_LABEL', component: HostC,
    })

    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    expect(screen.queryByText('PURDEX_LABEL')).toBeNull()
    expect(screen.queryByText('PURDEX_BODY')).toBeNull()
    expect(screen.queryByText('HOST_LABEL')).toBeNull()
    expect(screen.queryByText('HOST_BODY')).toBeNull()
  })
})
