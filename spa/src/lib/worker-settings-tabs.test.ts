import { describe, it, expect } from 'vitest'
import { clearWorkerSettingsTabs, getWorkerSettingsTabs, registerWorkerSettingsTab } from './worker-settings-tabs'

describe('worker settings tab registry', () => {
  it('keeps tabs sorted and replaces by id', () => {
    clearWorkerSettingsTabs()
    registerWorkerSettingsTab({ id: 'b', labelKey: 'b', order: 20, hostScoped: false, component: () => null })
    registerWorkerSettingsTab({ id: 'a', labelKey: 'a', order: 10, hostScoped: false, component: () => null })
    registerWorkerSettingsTab({ id: 'b', labelKey: 'b2', order: 5, hostScoped: true, component: () => null })
    expect(getWorkerSettingsTabs().map((t) => `${t.id}:${t.labelKey}`)).toEqual(['b:b2', 'a:a'])
  })
})
