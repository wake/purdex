import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerSettingsSection,
  getSettingsSections,
  clearSettingsSectionRegistry,
  drainLegacyContributionQueue,
  clearLegacyPending,
  type SettingsSectionDef,
} from './settings-section-registry'
import {
  clearContributions,
  listContributions,
  registerSettingsContribution,
} from './settings-contribution-registry'
import { dispatchSettingsContributions } from './dispatch-settings-contributions'
import { clearModuleRegistry, registerModule } from './module-registry'

const FakeComponent = () => null
const FakeComponent2 = () => null

function makeDef(overrides: Partial<SettingsSectionDef> = {}): SettingsSectionDef {
  return { id: 'test', label: 'Test', order: 0, component: FakeComponent, ...overrides }
}

function resetAll() {
  clearSettingsSectionRegistry()
  clearLegacyPending()
  clearContributions()
  clearModuleRegistry()
}

describe('settings-section-registry', () => {
  beforeEach(resetAll)

  // --- Existing adapter-bridged behavior (dispatch-aware) -----------------

  it('registers and retrieves sections (after dispatch)', () => {
    registerSettingsSection(makeDef({ id: 'a', label: 'A', order: 1 }))
    registerSettingsSection(makeDef({ id: 'b', label: 'B', order: 0 }))
    dispatchSettingsContributions([])
    const sections = getSettingsSections()
    expect(sections.map((s) => s.id)).toEqual(['b', 'a']) // sorted by order
  })

  it('returns a copy (not the internal array)', () => {
    registerSettingsSection(makeDef())
    dispatchSettingsContributions([])
    const a = getSettingsSections()
    const b = getSettingsSections()
    expect(a).not.toBe(b)
  })

  it('is idempotent — re-registering same id updates in place', () => {
    registerSettingsSection(makeDef({ id: 'x', label: 'Old' }))
    registerSettingsSection(makeDef({ id: 'x', label: 'New' }))
    dispatchSettingsContributions([])
    const sections = getSettingsSections()
    expect(sections).toHaveLength(1)
    expect(sections[0].label).toBe('New')
  })

  it('clearSettingsSectionRegistry removes all', () => {
    registerSettingsSection(makeDef({ id: 'a' }))
    registerSettingsSection(makeDef({ id: 'b' }))
    clearSettingsSectionRegistry()
    // After clear the internal legacy store is empty; dispatch sees no entries.
    dispatchSettingsContributions([])
    expect(getSettingsSections()).toHaveLength(0)
  })

  // --- PR-3: reserved (component: undefined) is now a hard error -----------

  it('PR-3: registerSettingsSection throws when component is undefined', () => {
    // Reserved semantics were removed alongside the last reserved entry
    // (`workspace`). A caller attempting to register a component-less
    // section must fail loudly rather than silently buffering a coming-soon
    // row.
    expect(() =>
      registerSettingsSection({
        id: 'ghost',
        label: 'Ghost',
        order: 10,
        // @ts-expect-error — intentional misuse for the runtime guard
        component: undefined,
      }),
    ).toThrow(/Reserved .* removed/i)
  })

  // --- Pending buffer: dispatch-flushed semantics (spec §7.2) --------------

  it('pending buffer: registerSettingsSection does NOT flush into listContributions until dispatch', () => {
    registerSettingsSection(makeDef({ id: 'p1' }))
    expect(listContributions('purdex')).toEqual([])
  })

  it('pending buffer: drainLegacyContributionQueue yields declarations and empties the buffer', () => {
    registerSettingsSection(makeDef({ id: 'p1', order: 3, label: 'Label' }))
    const drained = drainLegacyContributionQueue()
    expect(drained).toHaveLength(1)
    expect(drained[0].localId).toBe('p1')
    expect(drained[0].order).toBe(3)
    expect(drained[0].labelKey).toBe('Label')
    // Re-drain is empty.
    expect(drainLegacyContributionQueue()).toEqual([])
  })

  it('dispatch flush: legacy sections land in new registry under _builtin.legacy-section', () => {
    registerSettingsSection(makeDef({ id: 'disp1', order: 5 }))
    dispatchSettingsContributions([])
    const contribs = listContributions('purdex')
    expect(contribs.map((c) => c.id)).toContain('_builtin.legacy-section.disp1')
  })

  // Finding 1 regression guard: legacy must SURVIVE multi-dispatch.
  it('dispatch survive: multiple dispatches do not wipe legacy entries (Finding 1 regression)', () => {
    registerSettingsSection(makeDef({ id: 'survive' }))
    dispatchSettingsContributions([])
    expect(listContributions('purdex').map((c) => c.id)).toContain(
      '_builtin.legacy-section.survive',
    )
    // Simulate HMR: re-register BEFORE each dispatch (matches real flow where
    // registerBuiltinModules() pushes again before dispatch runs at its end).
    registerSettingsSection(makeDef({ id: 'survive' }))
    dispatchSettingsContributions([])
    expect(listContributions('purdex').map((c) => c.id)).toContain(
      '_builtin.legacy-section.survive',
    )
  })

  it('adapter round-trip: dispatch-restored shape matches original def', () => {
    registerSettingsSection(makeDef({ id: 'rt', label: 'Roundtrip', order: 7, component: FakeComponent }))
    dispatchSettingsContributions([])
    const sections = getSettingsSections()
    const entry = sections.find((s) => s.id === 'rt')
    expect(entry).toBeDefined()
    expect(entry!.label).toBe('Roundtrip')
    expect(entry!.order).toBe(7)
  })

  it('React identity: getSettingsSections returns the originally-passed component reference', () => {
    registerSettingsSection(makeDef({ id: 'id1', component: FakeComponent }))
    dispatchSettingsContributions([])
    const entry = getSettingsSections().find((s) => s.id === 'id1')
    expect(entry?.component).toBe(FakeComponent)
  })

  it('order preservation: active sections sort ascending via getSettingsSections', () => {
    registerSettingsSection(makeDef({ id: 'a', order: 0 }))
    registerSettingsSection(makeDef({ id: 'b', order: 11 }))
    registerSettingsSection(makeDef({ id: 'c', order: 2 }))
    dispatchSettingsContributions([])
    expect(getSettingsSections().map((s) => s.id)).toEqual(['a', 'c', 'b'])
  })

  // --- N1: active upsert -----------------------------------------------------

  it('N1 active upsert: re-registering same active id replaces, dispatch yields one entry', () => {
    registerSettingsSection({ id: 'a1', label: 'L', order: 0, component: FakeComponent })
    registerSettingsSection({ id: 'a1', label: 'L', order: 0, component: FakeComponent2 })
    dispatchSettingsContributions([])
    const contribs = listContributions('purdex').filter((c) => c.localId === 'a1')
    expect(contribs).toHaveLength(1)
    // Component reference reflects the latest registration.
    expect(getSettingsSections().find((s) => s.id === 'a1')?.component).toBe(FakeComponent2)
  })

  // --- HMR dispose: pending buffer clear -----------------------------------

  it('HMR dispose: clearLegacyPending clears the active pending buffer', () => {
    registerSettingsSection(makeDef({ id: 'act' }))
    clearLegacyPending()
    expect(drainLegacyContributionQueue()).toEqual([])
  })

  it('HMR re-run: register → dispatch → clearLegacyPending → register → dispatch stays single-entry', () => {
    registerSettingsSection(makeDef({ id: 'hmr' }))
    dispatchSettingsContributions([])
    clearLegacyPending()
    registerSettingsSection(makeDef({ id: 'hmr' }))
    expect(() => dispatchSettingsContributions([])).not.toThrow()
    const contribs = listContributions('purdex').filter((c) => c.localId === 'hmr')
    expect(contribs).toHaveLength(1)
  })

  // --- Namespace / clear / cross-id -----------------------------------------

  it('namespace isolation: module-declared contributions are not visible via getSettingsSections', () => {
    registerModule({
      id: 'mod',
      name: 'Mod',
      settings: [
        { localId: 'mysection', scope: 'purdex', order: 0, labelKey: 'mod.mysection', component: FakeComponent },
      ],
    })
    dispatchSettingsContributions()
    // New registry sees both the module-declared entry.
    expect(listContributions('purdex').map((c) => c.id)).toContain('mod.mysection')
    // Legacy view only sees `_builtin.legacy-section` entries — zero here.
    expect(getSettingsSections()).toEqual([])
  })

  it('clear scope: clearSettingsSectionRegistry only wipes pending buffer, not other contributions', () => {
    // A contribution registered through the new registry directly (simulating
    // what a module declaration flush would produce).
    registerSettingsContribution({
      moduleId: 'other',
      id: 'other.keep',
      localId: 'keep',
      scope: 'purdex',
      order: 0,
      labelKey: 'other.keep',
      component: FakeComponent,
    })
    // Legacy pending section.
    registerSettingsSection(makeDef({ id: 'legacy' }))

    clearSettingsSectionRegistry()

    // New registry entry must remain.
    expect(listContributions('purdex').map((c) => c.id)).toContain('other.keep')
    // Legacy pending drained.
    expect(drainLegacyContributionQueue()).toEqual([])
  })

  it('cross-id guard (F1): module-declared and legacy with same localId under same scope throws', () => {
    // Per F1 dispatch invariant: within a scope, localId must be unique across
    // modules — the shell uses localId as URL/selection/React key, so two
    // contributions with the same localId (regardless of differing moduleId
    // prefixes) are ambiguous at the UI layer.
    registerModule({
      id: 'foo',
      name: 'Foo',
      settings: [
        { localId: 'appearance', scope: 'purdex', order: 0, labelKey: 'foo.appearance', component: FakeComponent },
      ],
    })
    registerSettingsSection({ id: 'appearance', label: 'legacy', order: 0, component: FakeComponent2 })
    let thrown: Error | undefined
    try {
      dispatchSettingsContributions()
    } catch (e) {
      thrown = e as Error
    }
    expect(thrown).toBeDefined()
    expect(thrown!.message).toMatch(/localId "appearance"/)
  })
})
