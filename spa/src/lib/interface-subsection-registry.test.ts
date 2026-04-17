import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerInterfaceSubsection,
  getInterfaceSubsections,
  clearInterfaceSubsectionRegistry,
  type InterfaceSubsection,
} from './interface-subsection-registry'

const Fake = () => null

function make(overrides: Partial<InterfaceSubsection> = {}): InterfaceSubsection {
  return { id: 'test', label: 'Test', order: 0, component: Fake, ...overrides }
}

describe('interface-subsection-registry', () => {
  beforeEach(() => clearInterfaceSubsectionRegistry())

  it('registers and sorts by order', () => {
    registerInterfaceSubsection(make({ id: 'a', order: 2 }))
    registerInterfaceSubsection(make({ id: 'b', order: 0 }))
    registerInterfaceSubsection(make({ id: 'c', order: 1 }))
    expect(getInterfaceSubsections().map((s) => s.id)).toEqual(['b', 'c', 'a'])
  })

  it('re-registering same id updates in place (upsert)', () => {
    registerInterfaceSubsection(make({ id: 'x', label: 'Old' }))
    registerInterfaceSubsection(make({ id: 'x', label: 'New' }))
    const items = getInterfaceSubsections()
    expect(items).toHaveLength(1)
    expect(items[0].label).toBe('New')
  })

  it('upsert with changed order re-sorts correctly', () => {
    registerInterfaceSubsection(make({ id: 'a', order: 10 }))
    registerInterfaceSubsection(make({ id: 'b', order: 20 }))
    registerInterfaceSubsection(make({ id: 'a', order: 5 }))  // upsert lowers a's order
    expect(getInterfaceSubsections().map((s) => s.id)).toEqual(['a', 'b'])
  })

  it('returns a copy, not the internal array', () => {
    registerInterfaceSubsection(make())
    expect(getInterfaceSubsections()).not.toBe(getInterfaceSubsections())
  })

  it('supports disabled subsections', () => {
    registerInterfaceSubsection(make({ id: 'pane', disabled: true, disabledReason: 'settings.coming_soon' }))
    const [only] = getInterfaceSubsections()
    expect(only.disabled).toBe(true)
    expect(only.disabledReason).toBe('settings.coming_soon')
  })

  it('clear removes all', () => {
    registerInterfaceSubsection(make({ id: 'a' }))
    registerInterfaceSubsection(make({ id: 'b' }))
    clearInterfaceSubsectionRegistry()
    expect(getInterfaceSubsections()).toHaveLength(0)
  })
})
