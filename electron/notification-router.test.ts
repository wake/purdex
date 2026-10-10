import { describe, expect, it, vi } from 'vitest'
import { deliverNotificationClick, NOTIFICATION_CLICKED, type ClickWindow } from './notification-router'

// Two (or three) renderers: a window records what it was sent.
function fakeWindow(id: number, destroyed = false) {
  const send = vi.fn()
  const win: ClickWindow = { id, isDestroyed: () => destroyed, send }
  return { win, send }
}
const payload = { sessionCode: 's1', action: { kind: 'open-approval', hostId: 'h1' } }

describe('deliverNotificationClick (#1919)', () => {
  it('only the window that showed the notification gets the click', () => {
    const a = fakeWindow(1)
    const b = fakeWindow(2)
    expect(deliverNotificationClick(2, [a.win, b.win], payload)).toBe(1)
    expect(b.send).toHaveBeenCalledExactlyOnceWith(NOTIFICATION_CLICKED, payload)
    expect(a.send).not.toHaveBeenCalled()
  })

  it('falls back to every window when the one that showed it is gone', () => {
    const gone = fakeWindow(1, true)
    const b = fakeWindow(2)
    const c = fakeWindow(3)
    expect(deliverNotificationClick(1, [gone.win, b.win, c.win], payload)).toBe(2)
    expect(gone.send).not.toHaveBeenCalled()
    expect(b.send).toHaveBeenCalledWith(NOTIFICATION_CLICKED, payload)
    expect(c.send).toHaveBeenCalledWith(NOTIFICATION_CLICKED, payload)
  })

  it('falls back to every window when the owner is no longer in the window list (closed and removed)', () => {
    const b = fakeWindow(2)
    const c = fakeWindow(3)
    expect(deliverNotificationClick(1, [b.win, c.win], payload)).toBe(2)
    expect(b.send).toHaveBeenCalledOnce()
    expect(c.send).toHaveBeenCalledOnce()
  })

  it('an unknown owner (undefined) broadcasts, as before', () => {
    const a = fakeWindow(1)
    const b = fakeWindow(2)
    expect(deliverNotificationClick(undefined, [a.win, b.win], payload)).toBe(2)
  })

  it('never sends to a destroyed window, and sends to nobody when none is left', () => {
    const a = fakeWindow(1, true)
    const b = fakeWindow(2, true)
    expect(deliverNotificationClick(1, [a.win, b.win], payload)).toBe(0)
    expect(a.send).not.toHaveBeenCalled()
    expect(b.send).not.toHaveBeenCalled()
  })

  it('the payload goes through untouched', () => {
    const a = fakeWindow(1)
    deliverNotificationClick(1, [a.win], payload)
    expect(a.send.mock.calls[0][1]).toBe(payload)
  })
})
