import { describe, expect, it, vi } from 'vitest'
import { deliverNotificationClick, NOTIFICATION_CLICKED, type ClickWindow } from './notification-router'

// Two (or three) renderers: a window records what it was sent.
function fakeWindow(id: number, destroyed = false) {
  const send = vi.fn()
  const win: ClickWindow = { id, isDestroyed: () => destroyed, send }
  return { win, send }
}
const approval = { sessionCode: 's1', action: { kind: 'open-approval', hostId: 'h1' } }
const session = { sessionCode: 's1', action: { kind: 'open-session', hostId: 'h1', sessionCode: 's1' } }

describe('deliverNotificationClick (#1919)', () => {
  describe('an approval click', () => {
    it('only the window that showed the notification gets it', () => {
      const a = fakeWindow(1)
      const b = fakeWindow(2)
      expect(deliverNotificationClick(2, 'open-approval', [a.win, b.win], approval)).toBe(1)
      expect(b.send).toHaveBeenCalledExactlyOnceWith(NOTIFICATION_CLICKED, approval)
      expect(a.send).not.toHaveBeenCalled()
    })

    it('falls back to every window when the one that showed it is gone', () => {
      const gone = fakeWindow(1, true)
      const b = fakeWindow(2)
      const c = fakeWindow(3)
      expect(deliverNotificationClick(1, 'open-approval', [gone.win, b.win, c.win], approval)).toBe(2)
      expect(gone.send).not.toHaveBeenCalled()
      expect(b.send).toHaveBeenCalledWith(NOTIFICATION_CLICKED, approval)
      expect(c.send).toHaveBeenCalledWith(NOTIFICATION_CLICKED, approval)
    })

    it('falls back to every window when the owner is no longer in the window list (closed and removed)', () => {
      const b = fakeWindow(2)
      const c = fakeWindow(3)
      expect(deliverNotificationClick(1, 'open-approval', [b.win, c.win], approval)).toBe(2)
    })

    it('an unknown owner broadcasts', () => {
      const a = fakeWindow(1)
      const b = fakeWindow(2)
      expect(deliverNotificationClick(undefined, 'open-approval', [a.win, b.win], approval)).toBe(2)
    })
  })

  describe('every other click is still a broadcast (the window that holds the tab handles it)', () => {
    it.each(['open-session', 'open-host', undefined, 'something-new'])('%s reaches every live window, owner or not', (kind) => {
      const a = fakeWindow(1)
      const b = fakeWindow(2)
      expect(deliverNotificationClick(2, kind, [a.win, b.win], session)).toBe(2)
      expect(a.send).toHaveBeenCalledWith(NOTIFICATION_CLICKED, session)
      expect(b.send).toHaveBeenCalledWith(NOTIFICATION_CLICKED, session)
    })
  })

  it('never sends to a destroyed window, and sends to nobody when none is left', () => {
    const a = fakeWindow(1, true)
    const b = fakeWindow(2, true)
    expect(deliverNotificationClick(1, 'open-approval', [a.win, b.win], approval)).toBe(0)
    expect(deliverNotificationClick(1, 'open-session', [a.win, b.win], session)).toBe(0)
    expect(a.send).not.toHaveBeenCalled()
    expect(b.send).not.toHaveBeenCalled()
  })

  it('the payload goes through untouched', () => {
    const a = fakeWindow(1)
    deliverNotificationClick(1, 'open-approval', [a.win], approval)
    expect(a.send.mock.calls[0][1]).toBe(approval)
  })
})
