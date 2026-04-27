import { describe, it, expect, beforeEach } from 'vitest'
import { clearModuleRegistry, getModule } from './module-registry'
import { registerBuiltinModules } from './register-modules'

describe('register-modules — quick-commands module', () => {
  beforeEach(() => {
    clearModuleRegistry()
  })

  it('registers quick-commands as a disableable module', () => {
    registerBuiltinModules()
    const m = getModule('quick-commands')
    expect(m).toBeDefined()
    expect(m!.disableable).toBe(true)
    expect(m!.descriptionKey).toBe('modules.quick_commands.description')
  })

  it('Phase 1b: quick-commands declares purdex-scope settings contribution', () => {
    registerBuiltinModules()
    const m = getModule('quick-commands')
    const settings = m!.settings ?? []
    expect(settings).toHaveLength(1)
    expect(settings[0]).toMatchObject({
      localId: 'quick-commands',
      scope: 'purdex',
      labelKey: 'settings.section.quick_commands',
    })
  })
})
