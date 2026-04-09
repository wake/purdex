import { describe, it, expect, vi, afterEach } from 'vitest'
import {
  registerTabShortcuts,
  getTabShortcutHandler,
  clearTabShortcutRegistry,
} from './tab-shortcut-registry'

describe('tab-shortcut-registry', () => {
  afterEach(() => clearTabShortcutRegistry())

  it('registers and retrieves a handler', () => {
    const handler = vi.fn()
    registerTabShortcuts('browser', { reload: handler })
    expect(getTabShortcutHandler('browser', 'reload')).toBe(handler)
  })

  it('returns undefined for unregistered kind', () => {
    expect(getTabShortcutHandler('browser', 'reload')).toBeUndefined()
  })

  it('returns undefined for unregistered action', () => {
    registerTabShortcuts('browser', { reload: vi.fn() })
    expect(getTabShortcutHandler('browser', 'go-back')).toBeUndefined()
  })

  it('merges handlers when registering same kind twice', () => {
    const reload = vi.fn()
    const goBack = vi.fn()
    registerTabShortcuts('browser', { reload })
    registerTabShortcuts('browser', { 'go-back': goBack })
    expect(getTabShortcutHandler('browser', 'reload')).toBe(reload)
    expect(getTabShortcutHandler('browser', 'go-back')).toBe(goBack)
  })

  it('clearTabShortcutRegistry removes all handlers', () => {
    registerTabShortcuts('browser', { reload: vi.fn() })
    clearTabShortcutRegistry()
    expect(getTabShortcutHandler('browser', 'reload')).toBeUndefined()
  })
})
