import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { NewTabPage } from './NewTabPage'
import {
  registerNewTabProvider,
  clearNewTabRegistry,
  type NewTabProviderProps,
} from '../lib/new-tab-registry'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { useI18nStore } from '../stores/useI18nStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

// Commit 2 — A2-4 / A2-5: NewTabPage module-aware filter (spec §4.9.3).
//
// Registering providers directly (not via registerBuiltinModules) so each
// test case has a predictable set.  `useNewTabLayoutStore` is preloaded with
// both columns containing all known provider ids so they actually render;
// `persist.hasHydrated()` is forced true to bypass the loading placeholder.

const FakeEditorCard: React.FC<NewTabProviderProps> = () => <div data-testid="card-editor">editor</div>
const FakeEditorBuffersCard: React.FC<NewTabProviderProps> = () => <div data-testid="card-editor-buffers">buffers</div>
const FakeSessionsCard: React.FC<NewTabProviderProps> = () => <div data-testid="card-sessions">sessions</div>

function primeLayout(ids: string[]) {
  // Force 1col active and place every provider into the single column so the
  // rendering path doesn't depend on `ensureDefaults` timing.
  useNewTabLayoutStore.setState({
    profiles: {
      '3col': { enabled: false, columns: [[], [], []] },
      '2col': { enabled: false, columns: [[], []] },
      '1col': { enabled: true, columns: [[...ids]] },
    },
    knownIds: ids,
    activeEditingProfile: '1col',
  })
}

function forceHydrated() {
  // NewTabPage gates its content behind `persist.hasHydrated()`. In test
  // environments rehydration may not have run yet — trigger it directly so
  // the grid mounts synchronously.
  const api = useNewTabLayoutStore.persist as unknown as {
    hasHydrated: () => boolean
    rehydrate?: () => Promise<void>
  }
  if (!api.hasHydrated()) {
    api.rehydrate?.()
  }
}

beforeEach(() => {
  clearNewTabRegistry()
  clearModuleRegistry()
  // Declare the editor module so `isEnabled('editor')` can distinguish
  // "disableable + user off" from "not found → default true".
  registerModule({ id: 'editor', name: 'Editor', disableable: true })
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  useI18nStore.setState({ t: (k: string) => k })
  forceHydrated()
})

afterEach(() => {
  clearNewTabRegistry()
  clearModuleRegistry()
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
})

describe('NewTabPage — module-aware provider filter', () => {
  it('A2-4: hides providers whose moduleId is disabled (editor + editor-buffers)', () => {
    registerNewTabProvider({
      id: 'editor',
      label: 'editor.provider_label',
      icon: 'File',
      order: 5,
      component: FakeEditorCard,
      moduleId: 'editor',
    })
    registerNewTabProvider({
      id: 'editor-buffers',
      label: 'newTab.editor.buffers.label',
      icon: 'Stack',
      order: 6,
      component: FakeEditorBuffersCard,
      moduleId: 'editor',
    })
    registerNewTabProvider({
      id: 'sessions',
      label: 'session.provider_label',
      icon: 'List',
      order: 0,
      component: FakeSessionsCard,
      // no moduleId — legacy provider
    })
    primeLayout(['sessions', 'editor', 'editor-buffers'])

    // Disable the editor module.
    useModuleEnabledStore.setState({ enabled: { editor: false }, baseline: null })

    render(<NewTabPage onSelect={() => {}} />)

    expect(screen.queryByTestId('card-editor')).toBeNull()
    expect(screen.queryByTestId('card-editor-buffers')).toBeNull()
    // Sessions still visible — it has no moduleId.
    expect(screen.getByTestId('card-sessions')).toBeTruthy()
  })

  it('A2-5: providers without moduleId remain visible even when any module is disabled', () => {
    registerNewTabProvider({
      id: 'sessions',
      label: 'session.provider_label',
      icon: 'List',
      order: 0,
      component: FakeSessionsCard,
      // no moduleId
    })
    primeLayout(['sessions'])

    // Disable editor module — must not affect a no-moduleId provider.
    useModuleEnabledStore.setState({ enabled: { editor: false }, baseline: null })

    render(<NewTabPage onSelect={() => {}} />)

    expect(screen.getByTestId('card-sessions')).toBeTruthy()
  })
})
