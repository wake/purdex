/**
 * Smoke test for HSR PR-1/PR-2: prove that the settings contribution
 * registry is wired into the Purdex shell (PR-2), and NOT yet wired into
 * the remaining two shells (HostPage — PR-4 target, WorkspaceSettingsPage
 * — PR-3 target).
 *
 * Design rationale — Option B (static source check), preferred here:
 *
 *   The two pages below have deep coupling to app-wide state
 *   (wouter router, zustand host/workspace stores, i18n, electronAPI,
 *   plus numerous leaf components). Mounting them from cold to do a
 *   "queryByText must be null" check would require hundreds of lines
 *   of setup / mocking purely to observe the absence of a string —
 *   and would be brittle (any future i18n key collision or mock
 *   reshuffle could make the test spuriously pass).
 *
 *   A static-source check is strictly stronger for the thing we
 *   actually want to prove: that these shells do not yet import from
 *   `settings-contribution-registry`. When PR-3 / PR-4 wire them up,
 *   those corresponding entries are removed from `PAGES`.
 *
 *   We additionally register three fake contributions (one per scope)
 *   and verify they are queryable from the registry directly, so the
 *   infrastructure is exercised and regressions in the registry
 *   itself would surface here too.
 *
 *   Source files are pulled via Vite's `?raw` imports (browser/jsdom
 *   safe, no node:fs dependency).
 */
import { describe, it, expect, beforeEach } from 'vitest'
import hostPageSrc from '../components/HostPage.tsx?raw'
import workspaceSettingsPageSrc from '../features/workspace/components/WorkspaceSettingsPage.tsx?raw'
import {
  registerSettingsContribution,
  listContributions,
  clearContributions,
} from './settings-contribution-registry'

const FAKE_PURDEX_LABEL = 'SMOKE_PURDEX_LABEL_UNIQUE'
const FAKE_HOST_LABEL = 'SMOKE_HOST_LABEL_UNIQUE'
const FAKE_WORKSPACE_LABEL = 'SMOKE_WORKSPACE_LABEL_UNIQUE'

// HSR PR-2 migrated `src/components/SettingsPage.tsx` off this list — it
// now reads the contribution registry directly. The remaining two shells
// land in PR-3 (workspace) and PR-4 (host).
const PAGES: Array<{ name: string; src: string }> = [
  { name: 'src/components/HostPage.tsx', src: hostPageSrc },
  { name: 'src/features/workspace/components/WorkspaceSettingsPage.tsx', src: workspaceSettingsPageSrc },
]

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
