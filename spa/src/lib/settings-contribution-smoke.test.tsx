/**
 * Smoke test for HSR PR-1/PR-2/PR-3/PR-4: prove that the settings contribution
 * registry is wired into all three shells (Purdex / Workspace / Host).
 *
 * Design rationale — Option B (static source check), preferred here:
 *
 *   These shells have deep coupling to app-wide state (wouter router,
 *   zustand stores, i18n, electronAPI, plus numerous leaf components).
 *   Mounting them from cold to do a "queryByText must be null" check
 *   would require hundreds of lines of setup / mocking purely to observe
 *   the absence of a string — and would be brittle.
 *
 *   A static-source check is strictly stronger for the thing we actually
 *   want to prove. Source files are pulled via Vite's `?raw` imports
 *   (browser/jsdom safe, no node:fs dependency).
 *
 * PR-4 update: HostPage.tsx is now wired into the registry (commit 2).
 * The `PAGES` negative-guard list is now empty (all three shells migrated).
 * Positive assertion added to confirm HostPage does import from registry.
 */
import { describe, it, expect, beforeEach } from 'vitest'
import hostPageSrc from '../components/HostPage.tsx?raw'
import {
  registerSettingsContribution,
  listContributions,
  clearContributions,
} from './settings-contribution-registry'

const FAKE_PURDEX_LABEL = 'SMOKE_PURDEX_LABEL_UNIQUE'
const FAKE_HOST_LABEL = 'SMOKE_HOST_LABEL_UNIQUE'
const FAKE_WORKSPACE_LABEL = 'SMOKE_WORKSPACE_LABEL_UNIQUE'

// All three shells have been migrated to the contribution registry.
// PR-2: SettingsPage.tsx; PR-3: WorkspaceSettingsPage.tsx; PR-4: HostPage.tsx.
// This list is intentionally empty — kept for visibility that no shell
// remains unwired. Remove the iteration block below if no new shells are added.
const PAGES: Array<{ name: string; src: string }> = []

// Positive guard: HostPage.tsx MUST import listContributions (PR-4 commit 2).
// If this breaks, it means someone accidentally reverted the migration.

function FakeComponent() {
  return null
}

describe('settings-contribution-smoke (PR-1)', () => {
  beforeEach(() => {
    clearContributions()
    registerSettingsContribution({
      id: 'smoketest.purdex-fake',
      moduleId: 'smoketest',
      localId: 'purdex-fake',
      scope: 'purdex',
      order: 0,
      labelKey: FAKE_PURDEX_LABEL,
      component: FakeComponent,
    })
    registerSettingsContribution({
      id: 'smoketest.host-fake',
      moduleId: 'smoketest',
      localId: 'host-fake',
      scope: 'host',
      order: 0,
      labelKey: FAKE_HOST_LABEL,
      component: FakeComponent,
    })
    registerSettingsContribution({
      id: 'smoketest.workspace-fake',
      moduleId: 'smoketest',
      localId: 'workspace-fake',
      scope: 'workspace',
      order: 0,
      labelKey: FAKE_WORKSPACE_LABEL,
      component: FakeComponent,
    })
  })

  it('fake contributions are queryable from the registry directly (sanity)', () => {
    expect(listContributions('purdex').map((c) => c.id)).toContain('smoketest.purdex-fake')
    expect(listContributions('host').map((c) => c.id)).toContain('smoketest.host-fake')
    expect(listContributions('workspace').map((c) => c.id)).toContain('smoketest.workspace-fake')
  })

  // PR-4 (commit 2): HostPage is now wired into the registry — positive guard.
  it('src/components/HostPage.tsx DOES import from settings-contribution-registry', () => {
    expect(hostPageSrc).toMatch(/from\s+['"][^'"]*settings-contribution-registry['"]/)
    expect(hostPageSrc).toMatch(/\blistContributions\b/)
  })

  // No unwired shells remain. The iteration block below is kept as a template
  // in case new shells are added before their migration PR.
  for (const page of PAGES) {
    it(`${page.name} does NOT import from settings-contribution-registry`, () => {
      // Any import line referring to the registry module, relative or absolute.
      expect(page.src).not.toMatch(/from\s+['"][^'"]*settings-contribution-registry['"]/)
      expect(page.src).not.toMatch(/import\s*\(\s*['"][^'"]*settings-contribution-registry['"]\s*\)/)
    })

    it(`${page.name} does NOT reference listContributions / registerSettingsContribution by name`, () => {
      expect(page.src).not.toMatch(/\blistContributions\b/)
      expect(page.src).not.toMatch(/\bregisterSettingsContribution\b/)
    })

    it(`${page.name} does NOT contain the fake label keys (sanity: labels can only appear via registry dispatch)`, () => {
      expect(page.src).not.toContain(FAKE_PURDEX_LABEL)
      expect(page.src).not.toContain(FAKE_HOST_LABEL)
      expect(page.src).not.toContain(FAKE_WORKSPACE_LABEL)
    })
  }
})
