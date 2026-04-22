import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { clearModuleRegistry, registerModule, type ModuleDefinition } from './module-registry'
import { clearContributions, listContributions } from './settings-contribution-registry'
import { dispatchSettingsContributions } from './dispatch-settings-contributions'
import {
  drainLegacyContributionQueue,
  registerSettingsSection,
  clearLegacyPending,
} from './settings-section-registry'

const FakeComponent = () => null

function resetRegistries() {
  clearModuleRegistry()
  clearContributions()
  // Drain any leftover legacy pending buffer from previous tests.
  drainLegacyContributionQueue()
  clearLegacyPending()
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

  // --- F1: per-scope localId uniqueness at dispatch -----------------------

  describe('F1: per-scope localId uniqueness', () => {
    it('throws when two modules declare the same localId under the same scope', () => {
      registerModule({
        id: 'moduleA',
        name: 'Module A',
        settings: [
          { localId: 'general', scope: 'purdex', order: 0, labelKey: 'a.general', component: FakeComponent },
        ],
      })
      registerModule({
        id: 'moduleB',
        name: 'Module B',
        settings: [
          { localId: 'general', scope: 'purdex', order: 1, labelKey: 'b.general', component: FakeComponent },
        ],
      })

      // Error must name both sources so the author can locate them.
      let thrown: Error | undefined
      try {
        dispatchSettingsContributions()
      } catch (e) {
        thrown = e as Error
      }
      expect(thrown).toBeDefined()
      expect(thrown!.message).toMatch(/localId "general"/)
      expect(thrown!.message).toMatch(/purdex/)
      expect(thrown!.message).toMatch(/moduleA\.general/)
      expect(thrown!.message).toMatch(/moduleB\.general/)
    })

    it('throws when a legacy section collides with a module-declared localId in purdex scope', () => {
      registerModule({
        id: 'foo',
        name: 'Foo',
        settings: [
          { localId: 'appearance', scope: 'purdex', order: 0, labelKey: 'foo.appearance', component: FakeComponent },
        ],
      })
      registerSettingsSection({ id: 'appearance', label: 'Legacy', order: 0, component: FakeComponent })

      let thrown: Error | undefined
      try {
        dispatchSettingsContributions()
      } catch (e) {
        thrown = e as Error
      }
      expect(thrown).toBeDefined()
      expect(thrown!.message).toMatch(/localId "appearance"/)
      expect(thrown!.message).toMatch(/purdex/)
    })

    it('does NOT throw when same localId appears under different scopes', () => {
      registerModule({
        id: 'moduleA',
        name: 'Module A',
        settings: [
          { localId: 'general', scope: 'purdex', order: 0, labelKey: 'a.general', component: FakeComponent },
        ],
      })
      registerModule({
        id: 'moduleB',
        name: 'Module B',
        settings: [
          { localId: 'general', scope: 'host', order: 0, labelKey: 'b.general', component: FakeComponent },
        ],
      })

      expect(() => dispatchSettingsContributions()).not.toThrow()
      expect(listContributions('purdex').map((c) => c.id)).toEqual(['moduleA.general'])
      expect(listContributions('host').map((c) => c.id)).toEqual(['moduleB.general'])
    })

    it('does NOT throw when different localIds coexist under the same scope', () => {
      registerModule({
        id: 'moduleA',
        name: 'Module A',
        settings: [
          { localId: 'general', scope: 'purdex', order: 0, labelKey: 'a.general', component: FakeComponent },
          { localId: 'advanced', scope: 'purdex', order: 1, labelKey: 'a.advanced', component: FakeComponent },
        ],
      })

      expect(() => dispatchSettingsContributions()).not.toThrow()
      expect(listContributions('purdex').map((c) => c.localId)).toEqual(['general', 'advanced'])
    })

    it('leaves registry state unchanged when a localId collision throws', () => {
      registerModule({
        id: 'moduleA',
        name: 'Module A',
        settings: [
          { localId: 'general', scope: 'purdex', order: 0, labelKey: 'a.general', component: FakeComponent },
        ],
      })
      registerModule({
        id: 'moduleB',
        name: 'Module B',
        settings: [
          { localId: 'general', scope: 'purdex', order: 1, labelKey: 'b.general', component: FakeComponent },
        ],
      })

      let thrown: Error | undefined
      try {
        dispatchSettingsContributions()
      } catch (e) {
        thrown = e as Error
      }
      expect(thrown).toBeDefined()
      // No partial write — nothing registered at all.
      expect(listContributions('purdex')).toEqual([])
      expect(listContributions('host')).toEqual([])
      expect(listContributions('workspace')).toEqual([])
    })
  })
})
