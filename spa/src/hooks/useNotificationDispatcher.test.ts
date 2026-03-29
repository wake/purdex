import { describe, it, expect } from 'vitest'
import { shouldNotify } from './useNotificationDispatcher'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'

const defaultSettings: NotificationSettings = {
  enabled: true, events: {}, notifyWithoutTab: false, reopenTabOnClick: false,
}

describe('shouldNotify', () => {
  it('returns true for waiting event with matching tab', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', sessionCode: 'abc', focusedSession: null, hasTab: true, settings: defaultSettings })).toBe(true)
  })
  it('returns true for idle event', () => {
    expect(shouldNotify({ derived: 'idle', eventName: 'Stop', sessionCode: 'abc', focusedSession: null, hasTab: true, settings: defaultSettings })).toBe(true)
  })
  it('returns false for running event', () => {
    expect(shouldNotify({ derived: 'running', eventName: 'UserPromptSubmit', sessionCode: 'abc', focusedSession: null, hasTab: true, settings: defaultSettings })).toBe(false)
  })
  it('returns false when focused on same session', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', sessionCode: 'abc', focusedSession: 'abc', hasTab: true, settings: defaultSettings })).toBe(false)
  })
  it('returns false when no tab and notifyWithoutTab=false', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', sessionCode: 'abc', focusedSession: null, hasTab: false, settings: defaultSettings })).toBe(false)
  })
  it('returns true when no tab but notifyWithoutTab=true', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', sessionCode: 'abc', focusedSession: null, hasTab: false, settings: { ...defaultSettings, notifyWithoutTab: true } })).toBe(true)
  })
  it('returns false when agent disabled', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', sessionCode: 'abc', focusedSession: null, hasTab: true, settings: { ...defaultSettings, enabled: false } })).toBe(false)
  })
  it('returns false when event disabled', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', sessionCode: 'abc', focusedSession: null, hasTab: true, settings: { ...defaultSettings, events: { Notification: false } } })).toBe(false)
  })
  it('event defaults to true when not in events map', () => {
    expect(shouldNotify({ derived: 'idle', eventName: 'Stop', sessionCode: 'abc', focusedSession: null, hasTab: true, settings: { ...defaultSettings, events: {} } })).toBe(true)
  })
  it('returns false for idle Notification (idle_prompt/auth_success are informational)', () => {
    expect(shouldNotify({ derived: 'idle', eventName: 'Notification', sessionCode: 'abc', focusedSession: null, hasTab: true, settings: defaultSettings })).toBe(false)
  })
  it('returns true for waiting Notification (permission_prompt/elicitation_dialog)', () => {
    expect(shouldNotify({ derived: 'waiting', eventName: 'Notification', sessionCode: 'abc', focusedSession: null, hasTab: true, settings: defaultSettings })).toBe(true)
  })
})
