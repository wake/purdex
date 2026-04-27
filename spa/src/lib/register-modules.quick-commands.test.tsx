import { describe, it, expect, beforeEach } from 'vitest'
import { clearModuleRegistry, getModule } from './module-registry'
import { registerBuiltinModules } from './register-modules'

describe('register-modules — quick-commands module (Phase 1a)', () => {
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

  it('Phase 1a: quick-commands has NO settings contribution yet (deferred to Phase 1b)', () => {
    registerBuiltinModules()
    const m = getModule('quick-commands')
    expect(m!.settings ?? []).toHaveLength(0)
  })
})
