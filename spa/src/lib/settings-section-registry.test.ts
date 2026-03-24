import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerSettingsSection,
  getSettingsSections,
  clearSettingsSectionRegistry,
  type SettingsSectionDef,
} from './settings-section-registry'

const FakeComponent = () => null

function makeDef(overrides: Partial<SettingsSectionDef> = {}): SettingsSectionDef {
  return { id: 'test', label: 'Test', order: 0, component: FakeComponent, ...overrides }
}

describe('settings-section-registry', () => {
  beforeEach(() => clearSettingsSectionRegistry())

  it('registers and retrieves sections', () => {
    registerSettingsSection(makeDef({ id: 'a', label: 'A', order: 1 }))
    registerSettingsSection(makeDef({ id: 'b', label: 'B', order: 0 }))
    const sections = getSettingsSections()
    expect(sections.map((s) => s.id)).toEqual(['b', 'a']) // sorted by order
  })

  it('returns a copy (not the internal array)', () => {
    registerSettingsSection(makeDef())
    const a = getSettingsSections()
    const b = getSettingsSections()
    expect(a).not.toBe(b)
  })

  it('is idempotent — re-registering same id updates in place', () => {
    registerSettingsSection(makeDef({ id: 'x', label: 'Old' }))
    registerSettingsSection(makeDef({ id: 'x', label: 'New' }))
    const sections = getSettingsSections()
    expect(sections).toHaveLength(1)
    expect(sections[0].label).toBe('New')
  })

  it('clearSettingsSectionRegistry removes all', () => {
    registerSettingsSection(makeDef({ id: 'a' }))
    registerSettingsSection(makeDef({ id: 'b' }))
    clearSettingsSectionRegistry()
    expect(getSettingsSections()).toHaveLength(0)
  })

  it('supports reserved sections (no component)', () => {
    registerSettingsSection({ id: 'ws', label: 'Workspace', order: 10 })
    const sections = getSettingsSections()
    expect(sections[0].component).toBeUndefined()
  })
})
