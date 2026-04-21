import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { clearModuleRegistry, registerModule, type ModuleDefinition } from './module-registry'
import { clearContributions, listContributions } from './settings-contribution-registry'
import { dispatchSettingsContributions } from './dispatch-settings-contributions'
import { drainLegacyContributionQueue } from './settings-section-registry'

const FakeComponent = () => null

function resetRegistries() {
  clearModuleRegistry()
  clearContributions()
  // Drain any leftover legacy pending buffer from previous tests.
  drainLegacyContributionQueue()
}

describe('dispatchSettingsContributions', () => {
  beforeEach(resetRegistries)
  afterEach(resetRegistries)

  it('is idempotent for consecutive dispatches of the same module batch', () => {
    registerModule({
      id: 'repeatable',
      name: 'Repeatable',
      settings: [
        { localId: 'general', scope: 'purdex', order: 0, labelKey: 'general', component: FakeComponent },
        { localId: 'host', scope: 'host', order: 1, labelKey: 'host', component: FakeComponent },
      ],
    })

    expect(() => dispatchSettingsContributions()).not.toThrow()
    expect(() => dispatchSettingsContributions()).not.toThrow()

    expect(listContributions('purdex').map((item) => item.id)).toEqual(['repeatable.general'])
    expect(listContributions('host').map((item) => item.id)).toEqual(['repeatable.host'])
  })

  it('drains legacy pending contribution queue as part of the dispatch pass', () => {
    // Stub-level smoke: drainLegacyContributionQueue is exported and called
    // by dispatch. Stub returns [] so behavior is unchanged relative to main;
    // commit 2 replaces the stub with real pending-buffer semantics.
    expect(drainLegacyContributionQueue).toBeTypeOf('function')
    expect(() => dispatchSettingsContributions([])).not.toThrow()
    expect(drainLegacyContributionQueue()).toEqual([])
  })

  it('does not leave partial registry state behind when a later module fails validation', () => {
    const modules: ModuleDefinition[] = [
      {
        id: 'good',
        name: 'Good',
        settings: [
          { localId: 'general', scope: 'purdex', order: 0, labelKey: 'general', component: FakeComponent },
        ],
      },
      {
        id: 'bad',
        name: 'Bad',
        globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
        settings: [
          { localId: 'globalConfig', scope: 'purdex', order: 1, labelKey: 'bad', component: FakeComponent },
        ],
      },
    ]

    for (const module of modules) {
      registerModule(module)
    }

    expect(() => dispatchSettingsContributions()).toThrow(/bad.*globalConfig.*purdex/)
    expect(listContributions('purdex')).toEqual([])
    expect(listContributions('host')).toEqual([])
    expect(listContributions('workspace')).toEqual([])
  })
})
