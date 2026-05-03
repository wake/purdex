import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { clearModuleRegistry, registerModule, type ModuleDefinition } from './module-registry'
import { clearContributions, listContributions } from './settings-contribution-registry'
import {
  dispatchSettingsContributions,
  resetSettingsContributionsForHmr,
} from './dispatch-settings-contributions'
import {
  drainLegacyContributionQueue,
  peekLegacyContributionQueue,
  registerSettingsSection,
  clearLegacyPending,
} from './settings-section-registry'
import {
  clearHostBuiltinSources,
  HOST_BUILTIN_MODULE_ID,
  setHostBuiltinSections,
} from './host-builtin-sections'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'

const FakeComponent = () => null
const FakeHostSection = ({ hostId: _ }: { hostId: string }) => null

function resetRegistries() {
  clearModuleRegistry()
  clearContributions()
  // Drain any leftover legacy pending buffer from previous tests.
  drainLegacyContributionQueue()
  clearLegacyPending()
  // Clear the host-builtin source map so #586 tests start from a clean state.
  clearHostBuiltinSources()
  // Reset module-enabled store so disabled-module filter tests start clean.
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
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

    // --- F3: atomic legacy drain — validate before committing -------------

    describe('F3: atomic legacy drain', () => {
      it('legacy queue survives when module-declared validation fails', () => {
        // A module with an invariant-violation (globalConfig + settings[scope=purdex]).
        registerModule({
          id: 'bad',
          name: 'Bad',
          globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
          settings: [
            { localId: 'globalConfig', scope: 'purdex', order: 0, labelKey: 'bad.gc', component: FakeComponent },
          ],
        })
        // A valid legacy section queued.
        registerSettingsSection({ id: 'kept', label: 'Kept', order: 5, component: FakeComponent })

        expect(() => dispatchSettingsContributions()).toThrow(/bad.*globalConfig.*purdex/)

        // Legacy queue MUST be preserved so the next dispatch (after the
        // user fixes the module) can still commit the previously-queued
        // legacy sections without requiring the author to re-register them.
        const peeked = peekLegacyContributionQueue()
        expect(peeked).toHaveLength(1)
        expect(peeked[0].localId).toBe('kept')
      })

      it('legacy queue survives when legacy-vs-module localId collision throws (F1)', () => {
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
        expect(peekLegacyContributionQueue()).toHaveLength(1)
      })

      it('retry after fixing the conflict succeeds without author re-registration', () => {
        registerModule({
          id: 'bad',
          name: 'Bad',
          globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
          settings: [
            { localId: 'globalConfig', scope: 'purdex', order: 0, labelKey: 'bad.gc', component: FakeComponent },
          ],
        })
        registerSettingsSection({ id: 'kept', label: 'Kept', order: 5, component: FakeComponent })

        expect(() => dispatchSettingsContributions()).toThrow()

        // Simulate fixing the module by removing the bad one, then retry.
        clearModuleRegistry()
        expect(() => dispatchSettingsContributions()).not.toThrow()

        const ids = listContributions('purdex').map((c) => c.id)
        expect(ids).toContain('_builtin.legacy-section.kept')
      })

      it('happy path: valid dispatch clears queue + populates registry (unchanged)', () => {
        registerSettingsSection({ id: 'ok', label: 'OK', order: 0, component: FakeComponent })
        dispatchSettingsContributions()
        expect(peekLegacyContributionQueue()).toEqual([])
        expect(listContributions('purdex').map((c) => c.id)).toContain('_builtin.legacy-section.ok')
      })

      it('peek is non-destructive — repeated calls return same data', () => {
        registerSettingsSection({ id: 'a', label: 'A', order: 0, component: FakeComponent })
        registerSettingsSection({ id: 'b', label: 'B', order: 1, component: FakeComponent })
        const first = peekLegacyContributionQueue()
        const second = peekLegacyContributionQueue()
        expect(first.map((d) => d.localId)).toEqual(['a', 'b'])
        expect(second.map((d) => d.localId)).toEqual(['a', 'b'])
      })
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

  // ----- #586: host-builtin source-map idempotence ------------------------

  describe('#586 — host-builtin sources are idempotent across re-dispatches', () => {
    function defs(...ids: string[]) {
      return ids.map((id, i) => ({
        localId: id,
        labelKey: `hosts.${id}`,
        order: i,
        component: FakeHostSection,
      }))
    }

    // Test 6 (spec §6.1 #6): standalone re-dispatch preserves host built-ins
    // AND keeps wrapper component references identical.
    it('Test 6 — second standalone dispatchSettingsContributions() preserves built-ins (the #586 bug)', () => {
      setHostBuiltinSections(defs('overview', 'sessions', 'hooks', 'agents', 'uploads', 'logs'))

      dispatchSettingsContributions()
      const first = listContributions('host')
      expect(first).toHaveLength(6)
      const firstIds = first.map((c) => c.id)
      const firstComponents = first.map((c) => c.component)

      dispatchSettingsContributions()  // standalone — no re-setHostBuiltinSections
      const second = listContributions('host')
      expect(second).toHaveLength(6)
      expect(second.map((c) => c.id)).toEqual(firstIds)
      expect(second.map((c) => c.component)).toEqual(firstComponents)
      for (const c of second) {
        expect(c.moduleId).toBe(HOST_BUILTIN_MODULE_ID)
      }
    })

    // Test 7 (spec §6.1 #7): interleaving module-declared + built-in sources is stable.
    it('Test 7 — interleaved module + built-in dispatch state is stable across calls', () => {
      registerModule({
        id: 'editor',
        name: 'Editor',
        settings: [
          { localId: 'editor-opts', scope: 'purdex', order: 0, labelKey: 'ed.editor-opts', component: FakeComponent },
        ],
      })
      setHostBuiltinSections(defs('overview', 'sessions'))

      dispatchSettingsContributions()
      const purdexFirst = listContributions('purdex').map((c) => c.id)
      const hostFirst = listContributions('host').map((c) => c.id)
      expect(purdexFirst).toEqual(['editor.editor-opts'])
      expect(hostFirst).toEqual(['_builtin.host.overview', '_builtin.host.sessions'])

      dispatchSettingsContributions()
      expect(listContributions('purdex').map((c) => c.id)).toEqual(purdexFirst)
      expect(listContributions('host').map((c) => c.id)).toEqual(hostFirst)
    })

    // Test 8 (spec §6.1 #8): HMR reset clears host built-ins; subsequent dispatch sees zero.
    it('Test 8 — resetSettingsContributionsForHmr() clears host-builtin sources', () => {
      setHostBuiltinSections(defs('overview'))
      dispatchSettingsContributions()
      expect(listContributions('host')).toHaveLength(1)

      resetSettingsContributionsForHmr()
      dispatchSettingsContributions()
      expect(listContributions('host')).toEqual([])
    })
  })

  // --- Modules Switchboard: disabled-module filter --------------------------

  describe('disabled module filter (useModuleEnabledStore)', () => {
    it('T2-1: disabled module settings are excluded from dispatch', () => {
      registerModule({
        id: 'a',
        name: 'A',
        disableable: true,
        settings: [
          { localId: 'alpha', scope: 'purdex', order: 0, labelKey: 'a.alpha', component: FakeComponent },
        ],
      })
      useModuleEnabledStore.setState({ enabled: { a: false }, baseline: null })
      dispatchSettingsContributions()
      expect(listContributions('purdex').map((c) => c.id)).not.toContain('a.alpha')
    })

    it('T2-2: enabled module settings dispatch normally', () => {
      registerModule({
        id: 'a',
        name: 'A',
        disableable: true,
        settings: [
          { localId: 'alpha', scope: 'purdex', order: 0, labelKey: 'a.alpha', component: FakeComponent },
        ],
      })
      useModuleEnabledStore.setState({ enabled: { a: true }, baseline: null })
      dispatchSettingsContributions()
      expect(listContributions('purdex').map((c) => c.id)).toContain('a.alpha')
    })

    it('T2-3: disabled module does not participate in localId collision', () => {
      registerModule({
        id: 'a',
        name: 'A',
        disableable: true,
        settings: [
          { localId: 'x', scope: 'purdex', order: 0, labelKey: 'a.x', component: FakeComponent },
        ],
      })
      registerModule({
        id: 'b',
        name: 'B',
        settings: [
          { localId: 'x', scope: 'purdex', order: 1, labelKey: 'b.x', component: FakeComponent },
        ],
      })
      useModuleEnabledStore.setState({ enabled: { a: false }, baseline: null })
      expect(() => dispatchSettingsContributions()).not.toThrow()
      expect(listContributions('purdex').map((c) => c.id)).toEqual(['b.x'])
    })

    it('T2-4: legacy adapter contributions are unaffected by the filter', () => {
      registerModule({
        id: 'a',
        name: 'A',
        disableable: true,
        settings: [
          { localId: 'alpha', scope: 'purdex', order: 0, labelKey: 'a.alpha', component: FakeComponent },
        ],
      })
      registerSettingsSection({ id: 'legacy', label: 'Legacy', order: 0, component: FakeComponent })
      useModuleEnabledStore.setState({ enabled: { a: false }, baseline: null })
      dispatchSettingsContributions()
      const ids = listContributions('purdex').map((c) => c.id)
      expect(ids).toContain('_builtin.legacy-section.legacy')
      expect(ids).not.toContain('a.alpha')
    })

    it('T2-5: host-builtin contributions are unaffected by the filter', () => {
      setHostBuiltinSections([
        { localId: 'overview', labelKey: 'hosts.overview', order: 0, component: FakeHostSection },
      ])
      registerModule({
        id: 'a',
        name: 'A',
        disableable: true,
        // The purdex stub satisfies spec §I1 (every disableable module must
        // declare at least one purdex contribution); the test is about the
        // disable filter on host-scope contributions, not about I1.
        settings: [
          { localId: 'pdx-stub', scope: 'purdex', order: 0, labelKey: 'a.pdx', component: FakeComponent },
          { localId: 'host-stuff', scope: 'host', order: 0, labelKey: 'a.host', component: FakeComponent },
        ],
      })
      useModuleEnabledStore.setState({ enabled: { a: false }, baseline: null })
      dispatchSettingsContributions()
      const hostIds = listContributions('host').map((c) => c.id)
      expect(hostIds).toContain(`${HOST_BUILTIN_MODULE_ID}.overview`)
      expect(hostIds).not.toContain('a.host-stuff')
    })

    it('T2-6: disabled module workspace-scope contributions are also skipped', () => {
      registerModule({
        id: 'a',
        name: 'A',
        disableable: true,
        // Purdex stub for spec §I1 — see T2-5 note.
        settings: [
          { localId: 'pdx-stub', scope: 'purdex', order: 0, labelKey: 'a.pdx', component: FakeComponent },
          { localId: 'ws-section', scope: 'workspace', order: 0, labelKey: 'a.ws', component: FakeComponent },
        ],
      })
      useModuleEnabledStore.setState({ enabled: { a: false }, baseline: null })
      dispatchSettingsContributions()
      expect(listContributions('workspace').map((c) => c.id)).not.toContain('a.ws-section')
    })
  })
})
