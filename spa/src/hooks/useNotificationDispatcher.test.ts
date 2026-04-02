import { describe, it, expect, beforeEach, vi } from 'vitest'
import { shouldNotify, shouldDispatch, clearSeenTs } from './useNotificationDispatcher'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'
import { STORAGE_KEYS } from '../lib/storage'

const defaultSettings: NotificationSettings = {
  enabled: true, events: {}, notifyWithoutTab: false, reopenTabOnClick: false,
}

describe('shouldNotify', () => {
  it('returns true for waiting event with matching tab', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(true)
  })
  it('returns true for idle event', () => {
    expect(shouldNotify({ derived: 'idle', eventName: 'Stop', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(true)
  })
  it('returns false for running event', () => {
    expect(shouldNotify({ derived: 'running', eventName: 'UserPromptSubmit', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(false)
  })
  it('returns false when focused on same session and window has focus', () => {
    vi.spyOn(document, 'hasFocus').mockReturnValue(true)
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: 'host:abc', hasTab: true, settings: defaultSettings })).toBe(false)
    vi.restoreAllMocks()
  })
  it('returns true when focused on same session but window is in background', () => {
    vi.spyOn(document, 'hasFocus').mockReturnValue(false)
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: 'host:abc', hasTab: true, settings: defaultSettings })).toBe(true)
    vi.restoreAllMocks()
  })
  it('returns false when no tab and notifyWithoutTab=false', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: false, settings: defaultSettings })).toBe(false)
  })
  it('returns true when no tab but notifyWithoutTab=true', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: false, settings: { ...defaultSettings, notifyWithoutTab: true } })).toBe(true)
  })
  it('returns false when agent disabled', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: { ...defaultSettings, enabled: false } })).toBe(false)
  })
  it('returns false when event disabled', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: { ...defaultSettings, events: { Notification: false } } })).toBe(false)
  })
  it('event defaults to true when not in events map', () => {
    expect(shouldNotify({ derived: 'idle', eventName: 'Stop', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: { ...defaultSettings, events: {} } })).toBe(true)
  })
  it('returns false for idle Notification (idle_prompt/auth_success are informational)', () => {
    expect(shouldNotify({ derived: 'idle', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(false)
  })
  it('returns true for waiting Notification (permission_prompt/elicitation_dialog)', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(true)
  })
  it('returns true for error event (StopFailure)', () => {
    expect(shouldNotify({ derived: 'error', eventName: 'StopFailure', compositeKey: 'host:abc', focusedCompositeKey: '', hasTab: true, settings: defaultSettings })).toBe(true)
  })
})

describe('shouldDispatch', () => {
  beforeEach(() => {
    localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
  })

  it('returns false for new session (sentinel Infinity) but records ts', () => {
    // First event for a session the client has never seen
    expect(shouldDispatch('abc', 1000)).toBe(false)
    // ts is now recorded, so a newer event should dispatch
    expect(shouldDispatch('abc', 2000)).toBe(true)
  })

  it('returns false for duplicate broadcast_ts', () => {
    shouldDispatch('abc', 1000) // sentinel → record
    shouldDispatch('abc', 2000) // first real → dispatch
    expect(shouldDispatch('abc', 2000)).toBe(false) // same ts → skip
  })

  it('returns false for older broadcast_ts', () => {
    shouldDispatch('abc', 1000) // sentinel → record
    shouldDispatch('abc', 2000) // dispatch
    expect(shouldDispatch('abc', 1500)).toBe(false) // older → skip
  })

  it('returns true for newer broadcast_ts after recorded', () => {
    shouldDispatch('abc', 1000) // sentinel → record
    expect(shouldDispatch('abc', 2000)).toBe(true) // newer → dispatch
    expect(shouldDispatch('abc', 3000)).toBe(true) // newer again → dispatch
  })

  it('isolates sessions', () => {
    shouldDispatch('abc', 1000) // record abc
    shouldDispatch('def', 5000) // record def
    expect(shouldDispatch('abc', 2000)).toBe(true) // abc newer
    expect(shouldDispatch('def', 3000)).toBe(false) // def older than 5000
  })

  it('persists across calls (simulates restart)', () => {
    shouldDispatch('abc', 1000) // sentinel → record
    shouldDispatch('abc', 2000) // dispatch + record
    // Simulate restart: shouldDispatch is called fresh but localStorage persists
    expect(shouldDispatch('abc', 2000)).toBe(false) // same ts
    expect(shouldDispatch('abc', 3000)).toBe(true)  // newer
  })

  it('clearSeenTs resets session so next event is sentinel again', () => {
    shouldDispatch('abc', 1000) // sentinel → record
    shouldDispatch('abc', 2000) // dispatch
    clearSeenTs('abc')
    // After clear, session is new again — sentinel behavior
    expect(shouldDispatch('abc', 500)).toBe(false) // sentinel → record (even older ts)
    expect(shouldDispatch('abc', 600)).toBe(true)  // newer than 500 → dispatch
  })
})
