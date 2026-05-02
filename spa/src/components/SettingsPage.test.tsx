import { vi } from 'vitest'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

vi.mock('../features/workspace/components/WorkspaceSettingsPage', () => ({
  WorkspaceSettingsPage: ({ workspaceId }: { workspaceId: string }) => (
    <div data-testid="workspace-settings-mock">ws:{workspaceId}</div>
  ),
}))

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { SettingsPage, resetLastSection, useSettingsRoute } from './SettingsPage'
import { registerSettingsSection, clearSettingsSectionRegistry } from '../lib/settings-section-registry'
import { clearContributions, registerSettingsContribution } from '../lib/settings-contribution-registry'
import { dispatchSettingsContributions } from '../lib/dispatch-settings-contributions'
import { clearModuleRegistry, registerModule } from '../lib/module-registry'
import type { SettingsContext } from '../lib/settings-contribution-types'
import { AppearanceSection } from './settings/AppearanceSection'
import { TerminalSection } from './settings/TerminalSection'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import type { Pane } from '../types/tab'

const settingsPane: Pane = {
  id: 'pane-set',
  content: { kind: 'settings', scope: 'global' },
}

function renderWithLocation(initialPath: string) {
  const { hook, navigate, history } = memoryLocation({ path: initialPath, record: true })
  const result = render(
    <Router hook={hook}>
      <SettingsPage pane={settingsPane} isActive />
    </Router>,
  )
  return { ...result, navigate, hook, history: history as string[] }
}

const SyncStub = () => <div>SyncStub</div>

describe('SettingsPage', () => {
  beforeEach(() => {
    resetLastSection()
    clearSettingsSectionRegistry()
    clearContributions()
    registerSettingsSection({ id: 'appearance', label: 'Appearance', order: 0, component: AppearanceSection })
    registerSettingsSection({ id: 'terminal', label: 'Terminal', order: 1, component: TerminalSection })
    registerSettingsSection({ id: 'sync', label: 'Sync', order: 11, component: SyncStub })
    dispatchSettingsContributions([])
  })

  it('renders sidebar and default appearance section at /settings', () => {
    renderWithLocation('/settings')
    expect(screen.getAllByText('Appearance').length).toBeGreaterThan(0)
    expect(screen.getByText('Terminal')).toBeTruthy()
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
  })

  it('switches to terminal section on sidebar click', () => {
    renderWithLocation('/settings')
    fireEvent.click(screen.getByText('Terminal'))
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
  })

  it('preserves section across unmount/remount', () => {
    const first = renderWithLocation('/settings')
    fireEvent.click(screen.getByText('Terminal'))
    const desc = 'Terminal rendering and connection settings'
    expect(screen.getByText(desc)).toBeTruthy()
    first.unmount()
    renderWithLocation('/settings')
    expect(screen.getByText(desc)).toBeTruthy()
  })

  it('deep-links to section via /settings/terminal on mount', () => {
    renderWithLocation('/settings/terminal')
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
  })

  it('sidebar click updates URL to /settings/<id>', () => {
    const { history } = renderWithLocation('/settings')
    fireEvent.click(screen.getByText('Terminal'))
    expect(history[history.length - 1]).toBe('/settings/terminal')
  })

  it('invalid deep-link section falls through to default', () => {
    renderWithLocation('/settings/nonexistent-section')
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
  })

  it('self-heals URL when deep-link section is invalid (replaces to canonical)', async () => {
    const { history } = renderWithLocation('/settings/nonexistent-section')
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
    await waitFor(() => {
      expect(history[history.length - 1]).toBe('/settings/appearance')
    })
  })

  it('self-heals URL when deep-link path has extra segments (>2 levels)', async () => {
    const { history } = renderWithLocation('/settings/sync/extra/level3')
    await waitFor(() => {
      expect(history[history.length - 1]).toBe('/settings/sync')
    })
  })
})

describe('SettingsPage subsection', () => {
  beforeEach(() => {
    resetLastSection()
    clearSettingsSectionRegistry()
    clearContributions()
    registerSettingsSection({ id: 'appearance', label: 'Appearance', order: 0, component: AppearanceSection })
    registerSettingsSection({ id: 'terminal', label: 'Terminal', order: 1, component: TerminalSection })
    registerSettingsSection({ id: 'sync', label: 'Sync', order: 11, component: SyncStub })
    dispatchSettingsContributions([])
  })

  it('renders /settings/sync/history without self-heal (valid subsection)', async () => {
    const { hook, history } = memoryLocation({ path: '/settings/sync/history', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    // allow any self-heal effects to run; then assert URL did NOT change
    await new Promise((r) => setTimeout(r, 20))
    const hist = history as string[]
    // The URL should still be /settings/sync/history (no self-heal)
    expect(hist[hist.length - 1]).toBe('/settings/sync/history')
  })

  it('self-heals /settings/sync/extra/level3 back to /settings/sync', async () => {
    const { hook, history } = memoryLocation({ path: '/settings/sync/extra/level3', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    const hist = history as string[]
    await waitFor(() => {
      expect(hist[hist.length - 1]).toBe('/settings/sync')
    })
  })
})

// ---------------------------------------------------------------------------
// §3.2 — Registry-driven render + ctx injection + workspace dispatch (PR-2).
// ---------------------------------------------------------------------------

describe('SettingsPage (registry-driven)', () => {
  beforeEach(() => {
    resetLastSection()
    clearSettingsSectionRegistry()
    clearContributions()
    clearModuleRegistry()
  })

  it('renders a new-registry contribution by label (fake module bypasses legacy adapter)', () => {
    const captured: SettingsContext[] = []
    const Fake = ({ ctx }: { ctx: SettingsContext }) => {
      captured.push(ctx)
      return <div>MY_FAKE_BODY</div>
    }
    registerSettingsContribution({
      moduleId: 'fakemod',
      id: 'fakemod.primary',
      localId: 'primary',
      scope: 'purdex',
      order: 0,
      labelKey: 'FAKE_LABEL',
      component: Fake,
    })

    const { hook } = memoryLocation({ path: '/settings', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    expect(screen.getByText('FAKE_LABEL')).toBeTruthy()
    expect(screen.getByText('MY_FAKE_BODY')).toBeTruthy()
    expect(captured[0]).toEqual({ scope: 'purdex' })
  })

  it('renders contributions in ascending `order`', () => {
    const Body = ({ label }: { label: string }) => <div>{label}</div>
    const Third = () => <Body label="THIRD" />
    const First = () => <Body label="FIRST" />
    const Second = () => <Body label="SECOND" />

    registerSettingsContribution({
      moduleId: 'm', id: 'm.c', localId: 'c', scope: 'purdex',
      order: 50, labelKey: 'C', component: Third,
    })
    registerSettingsContribution({
      moduleId: 'm', id: 'm.a', localId: 'a', scope: 'purdex',
      order: 0, labelKey: 'A', component: First,
    })
    registerSettingsContribution({
      moduleId: 'm', id: 'm.b', localId: 'b', scope: 'purdex',
      order: 10, labelKey: 'B', component: Second,
    })

    const { hook } = memoryLocation({ path: '/settings', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    const sidebar = screen.getByText('A').closest('[class*="w-48"]')
    expect(sidebar).toBeTruthy()
    const labels = Array.from(sidebar!.querySelectorAll('button')).map((b) =>
      b.textContent?.trim(),
    )
    expect(labels).toEqual(['A', 'B', 'C'])
  })

  it('adapter integration: legacy registerSettingsSection + dispatch renders via new registry', () => {
    const Legacy = () => <div>LEGACY_BODY</div>
    registerSettingsSection({ id: 'leg', label: 'LEGACY_LABEL', order: 0, component: Legacy })
    dispatchSettingsContributions([])

    const { hook } = memoryLocation({ path: '/settings', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    expect(screen.getByText('LEGACY_LABEL')).toBeTruthy()
    expect(screen.getByText('LEGACY_BODY')).toBeTruthy()
  })

  it('subsection is exposed via useSettingsRoute()', () => {
    const Probe = () => {
      const { subsection } = useSettingsRoute()
      return <div>sub:{subsection ?? 'null'}</div>
    }
    registerSettingsContribution({
      moduleId: 'fakemod',
      id: 'fakemod.primary',
      localId: 'primary',
      scope: 'purdex',
      order: 0,
      labelKey: 'Primary',
      component: Probe,
    })
    const { hook } = memoryLocation({ path: '/settings/primary/deep', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    // "/settings/<section>/<subsection>" preserves subsection via context.
    expect(screen.getByText('sub:deep')).toBeTruthy()
  })

  it('dispatches to WorkspaceSettingsPage when pane content has a workspace scope', () => {
    const workspacePane: Pane = {
      id: 'pane-ws',
      content: { kind: 'settings', scope: { workspaceId: 'wsA' } },
    }
    const { hook } = memoryLocation({ path: '/settings', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage pane={workspacePane} isActive />
      </Router>,
    )
    expect(screen.getByTestId('workspace-settings-mock').textContent).toBe('ws:wsA')
  })
})

// ---------------------------------------------------------------------------
// F7 — shell honors `disabled(ctx)`. A contribution that is disabled under
// the current ctx must NOT mount its component, even if the URL / default
// selection would otherwise pick it. Shell self-heals to the first
// non-disabled section (matching the "invalid section" pattern).
// ---------------------------------------------------------------------------

describe('SettingsPage (F7: disabled contribution gating)', () => {
  beforeEach(() => {
    resetLastSection()
    clearSettingsSectionRegistry()
    clearContributions()
    clearModuleRegistry()
  })

  it('does NOT mount the active component when disabled(ctx) returns true', () => {
    const Alive = () => <div>ALIVE_BODY</div>
    const Dead = () => <div>DEAD_BODY</div>
    registerSettingsContribution({
      moduleId: 'mod', id: 'mod.alive', localId: 'alive',
      scope: 'purdex', order: 0, labelKey: 'Alive', component: Alive,
    })
    registerSettingsContribution({
      moduleId: 'mod', id: 'mod.dead', localId: 'dead',
      scope: 'purdex', order: 1, labelKey: 'Dead', component: Dead,
      disabled: () => true,
    })

    // Deep-link straight to the disabled section.
    const { hook, history } = memoryLocation({ path: '/settings/dead', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    // Dead component did not render.
    expect(screen.queryByText('DEAD_BODY')).toBeNull()
    // Alive (first non-disabled) is mounted instead.
    expect(screen.getByText('ALIVE_BODY')).toBeTruthy()
    // URL self-heals to the first non-disabled section.
    expect((history as string[]).at(-1)).toBe('/settings/alive')
  })

  it('mounts the component normally when disabled(ctx) returns false', () => {
    const Alive = () => <div>ALIVE_BODY_2</div>
    registerSettingsContribution({
      moduleId: 'mod', id: 'mod.alive', localId: 'alive',
      scope: 'purdex', order: 0, labelKey: 'Alive', component: Alive,
      disabled: () => false,
    })

    const { hook } = memoryLocation({ path: '/settings/alive', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    expect(screen.getByText('ALIVE_BODY_2')).toBeTruthy()
  })

  it('treats disabled default (first) as invalid and picks next non-disabled section', () => {
    const First = () => <div>FIRST_BODY</div>
    const Second = () => <div>SECOND_BODY</div>
    registerSettingsContribution({
      moduleId: 'mod', id: 'mod.first', localId: 'first',
      scope: 'purdex', order: 0, labelKey: 'First', component: First,
      disabled: () => true,
    })
    registerSettingsContribution({
      moduleId: 'mod', id: 'mod.second', localId: 'second',
      scope: 'purdex', order: 1, labelKey: 'Second', component: Second,
    })

    // Bare /settings — the default would otherwise be `first` by order.
    const { hook, history } = memoryLocation({ path: '/settings', record: true })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    expect(screen.queryByText('FIRST_BODY')).toBeNull()
    expect(screen.getByText('SECOND_BODY')).toBeTruthy()
    // URL should reflect the actual mounted section.
    expect((history as string[]).at(-1)).toBe('/settings/second')
  })
})

// ---------------------------------------------------------------------------
// L1-1 — Legacy URL alias: bookmarked `/settings/editor-buffers` must still
// land on the new Editor section after the legacy registerSettingsSection
// call is removed (spec §4.2).
// ---------------------------------------------------------------------------

describe('SettingsPage (legacy editor-buffers alias)', () => {
  beforeEach(() => {
    resetLastSection()
    clearSettingsSectionRegistry()
    clearContributions()
    clearModuleRegistry()
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  })

  it('L1-1: redirects /settings/editor-buffers to the new `editor` section', async () => {
    const EditorBody = () => <div>EDITOR_SECTION_BODY</div>
    const AppearanceBody = () => <div>APPEARANCE_SECTION_BODY</div>
    registerSettingsContribution({
      moduleId: '_builtin.legacy-section',
      id: '_builtin.legacy-section.appearance',
      localId: 'appearance',
      scope: 'purdex',
      order: 0,
      labelKey: 'Appearance',
      component: AppearanceBody,
    })
    registerSettingsContribution({
      moduleId: 'editor',
      id: 'editor.editor',
      localId: 'editor',
      scope: 'purdex',
      order: 9,
      labelKey: 'Editor',
      component: EditorBody,
    })

    const { hook, history } = memoryLocation({
      path: '/settings/editor-buffers',
      record: true,
    })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )

    // The Editor section (localId 'editor') is rendered, not the default
    // `appearance` fallback.
    expect(screen.getByText('EDITOR_SECTION_BODY')).toBeTruthy()
    expect(screen.queryByText('APPEARANCE_SECTION_BODY')).toBeNull()
    // URL self-heals to the canonical new section id.
    await waitFor(() => {
      expect((history as string[]).at(-1)).toBe('/settings/editor')
    })
  })
})

// ---------------------------------------------------------------------------
// PR-2 (spec §4.2.3) — URL alias map covers `link-detect` / `open-behavior`
// in addition to the existing `editor-buffers` legacy alias. Bookmarks /
// history entries pointing at the now-collapsed sub-section URLs must
// continue to land on the consolidated Editor page.
//
// Fixture rules (plan §Task 2.2):
//   - `appearance` registered via legacy `registerSettingsSection` (always
//     selectable; serves as the firstSelectable fallback when editor is
//     disabled).
//   - `editor` registered via `registerModule({ disableable: true })` so
//     `useModuleEnabledStore` can gate it through the dispatch filter.
// ---------------------------------------------------------------------------

describe('SettingsPage (PR-2 alias map: link-detect / open-behavior)', () => {
  const EditorBody = () => <div>EDITOR_SECTION_BODY</div>
  const AppearanceBody = () => <div>APPEARANCE_SECTION_BODY</div>

  function setupFixture(): void {
    resetLastSection()
    clearSettingsSectionRegistry()
    clearContributions()
    clearModuleRegistry()
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
    // Plan §Task 2.2 calls for `appearance` registered via legacy
    // `registerSettingsSection` to serve as the firstSelectable fallback.
    // The legacy adapter pushes onto a queue that is drained on the first
    // dispatch — but redispatch (e.g. inside 2.2.e after toggling editor
    // off) will not re-emit those entries because the queue is empty by
    // then. A non-disableable `registerModule` call is the equivalent
    // "always-selectable" fixture that survives any number of dispatches.
    registerModule({
      id: '_appearance-fixture',
      name: 'AppearanceFixture',
      settings: [
        {
          localId: 'appearance',
          scope: 'purdex',
          order: 0,
          labelKey: 'Appearance',
          component: AppearanceBody,
        },
      ],
    })
    // Editor as a real disableable module so 2.2.e (alias canonical not
    // selectable) can drive `useModuleEnabledStore.setEnabled('editor', false)`
    // and the dispatch filter actually drops the contribution.
    registerModule({
      id: 'editor',
      name: 'Editor',
      disableable: true,
      settings: [
        {
          localId: 'editor',
          scope: 'purdex',
          order: 11,
          labelKey: 'Editor',
          component: EditorBody,
        },
      ],
    })
    dispatchSettingsContributions()
  }

  beforeEach(() => {
    setupFixture()
  })

  it('2.2.a: /settings/link-detect resolves to /settings/editor', async () => {
    const { hook, history } = memoryLocation({
      path: '/settings/link-detect',
      record: true,
    })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    expect(screen.getByText('EDITOR_SECTION_BODY')).toBeTruthy()
    await waitFor(() => {
      expect((history as string[]).at(-1)).toBe('/settings/editor')
    })
  })

  it('2.2.b: /settings/open-behavior resolves to /settings/editor', async () => {
    const { hook, history } = memoryLocation({
      path: '/settings/open-behavior',
      record: true,
    })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    expect(screen.getByText('EDITOR_SECTION_BODY')).toBeTruthy()
    await waitFor(() => {
      expect((history as string[]).at(-1)).toBe('/settings/editor')
    })
  })

  it('2.2.c: /settings/editor-buffers still resolves to /settings/editor (existing alias preserved)', async () => {
    const { hook, history } = memoryLocation({
      path: '/settings/editor-buffers',
      record: true,
    })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    expect(screen.getByText('EDITOR_SECTION_BODY')).toBeTruthy()
    await waitFor(() => {
      expect((history as string[]).at(-1)).toBe('/settings/editor')
    })
  })

  it('2.2.d: /settings/editor stays at /settings/editor (canonical identity, no extra history entry)', async () => {
    const { hook, history } = memoryLocation({
      path: '/settings/editor',
      record: true,
    })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    expect(screen.getByText('EDITOR_SECTION_BODY')).toBeTruthy()
    // Allow self-heal effects to settle, then assert the path did not change.
    await new Promise((r) => setTimeout(r, 30))
    expect((history as string[]).at(-1)).toBe('/settings/editor')
  })

  it('2.2.e: when alias canonical (editor) is disabled, URL self-heals to firstSelectable (appearance)', async () => {
    // Disable the editor module BEFORE rendering so dispatch's filter drops
    // the editor contribution from `listContributions`. The non-disableable
    // appearance fixture survives the second dispatch automatically.
    useModuleEnabledStore.getState().setEnabled('editor', false)
    dispatchSettingsContributions()

    const { hook, history } = memoryLocation({
      path: '/settings/link-detect',
      record: true,
    })
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    // Editor body must NOT mount because the contribution is gone; the
    // firstSelectable fallback (appearance) is what renders.
    expect(screen.queryByText('EDITOR_SECTION_BODY')).toBeNull()
    expect(screen.getByText('APPEARANCE_SECTION_BODY')).toBeTruthy()
    await waitFor(() => {
      expect((history as string[]).at(-1)).toBe('/settings/appearance')
    })
  })

  it('2.2.f: identity case (editor → editor) does not push a duplicate history entry', async () => {
    const { hook, history } = memoryLocation({
      path: '/settings/editor',
      record: true,
    })
    const initialLength = (history as string[]).length
    render(
      <Router hook={hook}>
        <SettingsPage pane={settingsPane} isActive />
      </Router>,
    )
    expect(screen.getByText('EDITOR_SECTION_BODY')).toBeTruthy()
    await new Promise((r) => setTimeout(r, 30))
    // memoryLocation records every change; with `replace: true` semantics the
    // entry count should not grow when path === canonical. We assert exactly
    // no growth so a regression that re-entered setLocation on every render
    // would fail loudly.
    expect((history as string[]).length).toBe(initialLength)
  })
})
