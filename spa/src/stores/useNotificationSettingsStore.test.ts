import { describe, it, expect, beforeEach } from 'vitest'
import { useNotificationSettingsStore } from './useNotificationSettingsStore'

beforeEach(() => { useNotificationSettingsStore.setState({ agents: {} }) })

describe('useNotificationSettingsStore', () => {
  it('getSettingsForAgent returns defaults for unknown agent', () => {
    const s = useNotificationSettingsStore.getState().getSettingsForAgent('cc')
    expect(s.enabled).toBe(true)
    expect(s.notifyWithoutTab).toBe(false)
    expect(s.reopenTabOnClick).toBe(false)
  })
  it('setAgentEnabled toggles enabled', () => {
    useNotificationSettingsStore.getState().setAgentEnabled('cc', false)
    expect(useNotificationSettingsStore.getState().agents['cc']?.enabled).toBe(false)
  })
  it('setEventEnabled toggles per-event', () => {
    useNotificationSettingsStore.getState().setEventEnabled('cc', 'Stop', false)
    expect(useNotificationSettingsStore.getState().getSettingsForAgent('cc').events['Stop']).toBe(false)
  })
  it('setNotifyWithoutTab toggles', () => {
    useNotificationSettingsStore.getState().setNotifyWithoutTab('cc', true)
    expect(useNotificationSettingsStore.getState().getSettingsForAgent('cc').notifyWithoutTab).toBe(true)
  })
  it('setReopenTabOnClick toggles', () => {
    useNotificationSettingsStore.getState().setReopenTabOnClick('cc', true)
    expect(useNotificationSettingsStore.getState().getSettingsForAgent('cc').reopenTabOnClick).toBe(true)
  })
})
