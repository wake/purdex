import { describe, it, expect } from 'vitest'
import { normalizeEventName } from './event-name'

describe('normalizeEventName', () => {
  it('PdxNotification → Notification', () => {
    expect(normalizeEventName('PdxNotification')).toBe('Notification')
  })
  it('PdxPermissionRequest → PermissionRequest', () => {
    expect(normalizeEventName('PdxPermissionRequest')).toBe('PermissionRequest')
  })
  it('PdxStop → Stop', () => {
    expect(normalizeEventName('PdxStop')).toBe('Stop')
  })
  it('PdxStopFailure → StopFailure', () => {
    expect(normalizeEventName('PdxStopFailure')).toBe('StopFailure')
  })
  it('legacy literal passes through unchanged', () => {
    expect(normalizeEventName('Notification')).toBe('Notification')
    expect(normalizeEventName('PermissionRequest')).toBe('PermissionRequest')
    expect(normalizeEventName('Stop')).toBe('Stop')
    expect(normalizeEventName('StopFailure')).toBe('StopFailure')
  })
  it('non-notification PdxXxx events pass through unchanged', () => {
    // Only the 4 events that reach buildNotificationContent / shouldNotify
    // informational paths get normalized. Other PdxXxx events (PdxSessionStart,
    // PdxUserPromptSubmit, etc.) are not notification-targets.
    expect(normalizeEventName('PdxSessionStart')).toBe('PdxSessionStart')
    expect(normalizeEventName('PdxUserPromptSubmit')).toBe('PdxUserPromptSubmit')
  })
  it('empty / unknown string passes through unchanged', () => {
    expect(normalizeEventName('')).toBe('')
    expect(normalizeEventName('SomethingElse')).toBe('SomethingElse')
  })
})
