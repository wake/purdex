// electron/badge.test.ts — the Dock badge in main (app-badge spec §3.2): the union of every window's report, and when a
// window's report is dropped.
import { EventEmitter } from 'node:events'
import { describe, expect, it, vi } from 'vitest'
import { attachBadgeSender, BadgeCenter, badgeCount, MAX_STRING, parseBadgeReport, submitBadgeReport, type BadgeReport } from './badge'

const card = (id: string, fallback: string, session?: string) => ({ id, fallback, ...(session ? { session } : {}) })
const rep = (sessions: string[], cards: BadgeReport['cards'] = []): BadgeReport => ({ sessions, cards })

describe('badgeCount — the union over windows', () => {
  it('no windows, nothing open → 0', () => {
    expect(badgeCount([])).toBe(0)
    expect(badgeCount([rep([])])).toBe(0)
  })

  it('two windows with overlapping sessions count the union', () => {
    expect(badgeCount([rep(['h:a', 'h:b']), rep(['h:b', 'h:c'])])).toBe(3)
  })

  it('a card resolved to a session counts that session (once with its unread)', () => {
    expect(badgeCount([rep(['h:a'], [card('h\0c1', 'c:h:S1', 'h:a')])])).toBe(1)
  })

  it('an unresolved card counts its fallback; two cards of one unresolved session count 1', () => {
    expect(badgeCount([rep([], [card('h\0c1', 'c:h:S1'), card('h\0c2', 'c:h:S1')])])).toBe(1)
    expect(badgeCount([rep([], [card('h\0c1', 'c:h:S1'), card('h\0c2', 'c:h:S2')])])).toBe(2)
  })

  it('one card resolved by one window and not by another counts once, as the resolved session', () => {
    const resolved = rep(['h:a'], [card('h\0c1', 'c:h:S1', 'h:a')])
    const unresolved = rep([], [card('h\0c1', 'c:h:S1')])
    expect(badgeCount([resolved, unresolved])).toBe(1)
    expect(badgeCount([unresolved, resolved])).toBe(1)
  })

  it('a card resolved only by the card entry (not in the window\'s sessions) still counts its session, not its fallback', () => {
    expect(badgeCount([rep([], [card('h\0c1', 'c:h:S1', 'h:a')]), rep(['h:a'])])).toBe(1)
  })

  it('a card is one card by id across windows: the same fallback twice counts once, unresolved in both', () => {
    expect(badgeCount([rep([], [card('h\0c1', 'c:h:S1')]), rep([], [card('h\0c1', 'c:h:S1')])])).toBe(1)
  })

  it('a session key and a fallback never collide even when they read alike', () => {
    expect(badgeCount([rep(['c:h:S1'], [card('h\0c1', 'c:h:S1')])])).toBe(2)
  })
})

describe('parseBadgeReport — the IPC boundary', () => {
  it('accepts the shape', () => {
    const r = rep(['h:a'], [card('h\0c1', 'c:h:S1', 'h:a'), card('h\0c2', 'a:h:c2')])
    expect(parseBadgeReport(r)).toEqual(r)
  })

  it.each([
    null, 'x', 42, {}, { sessions: 'a', cards: [] }, { sessions: [1], cards: [] }, { sessions: [], cards: {} },
    { sessions: [], cards: [{ id: 1, fallback: 'f' }] }, { sessions: [], cards: [{ id: 'i' }] },
    { sessions: [], cards: [{ id: 'i', fallback: 'f', session: 5 }] }, { sessions: [], cards: [null] },
  ])('rejects %j', (bad) => {
    expect(parseBadgeReport(bad)).toBeNull()
  })

  it('rejects a report larger than any real window could send', () => {
    expect(parseBadgeReport({ sessions: Array.from({ length: 10_001 }, (_, i) => `h:${i}`), cards: [] })).toBeNull()
    expect(parseBadgeReport({ sessions: [], cards: Array.from({ length: 10_001 }, (_, i) => card(`c${i}`, `a:h:${i}`)) })).toBeNull()
  })

  it('rejects a string longer than any id or key could be (the entry cap alone does not bound the bytes)', () => {
    const long = 'x'.repeat(MAX_STRING + 1)
    expect(parseBadgeReport({ sessions: [long], cards: [] })).toBeNull()
    expect(parseBadgeReport({ sessions: [], cards: [card(long, 'a:h:1')] })).toBeNull()
    expect(parseBadgeReport({ sessions: [], cards: [card('c1', long)] })).toBeNull()
    expect(parseBadgeReport({ sessions: [], cards: [card('c1', 'a:h:1', long)] })).toBeNull()
    const edge = 'x'.repeat(MAX_STRING)
    expect(parseBadgeReport({ sessions: [edge], cards: [card(edge, edge, edge)] })).not.toBeNull()
  })

  it('drops unknown extra fields', () => {
    expect(parseBadgeReport({ sessions: ['h:a'], cards: [], extra: 1 })).toEqual(rep(['h:a']))
  })
})

describe('BadgeCenter', () => {
  const setup = () => {
    const apply = vi.fn()
    return { apply, center: new BadgeCenter(apply) }
  }

  it('sets the union count and recomputes on every window\'s report', () => {
    const { apply, center } = setup()
    center.set(1, rep(['h:a']))
    expect(apply).toHaveBeenLastCalledWith(1)
    center.set(2, rep(['h:a', 'h:b']))
    expect(apply).toHaveBeenLastCalledWith(2)
    center.set(1, rep([]))
    expect(apply).toHaveBeenLastCalledWith(2)
  })

  it('a window\'s new report replaces its previous one', () => {
    const { apply, center } = setup()
    center.set(1, rep(['h:a', 'h:b']))
    center.set(1, rep(['h:a']))
    expect(apply).toHaveBeenLastCalledWith(1)
  })

  it('dropping a window recomputes without it; dropping the last clears to 0', () => {
    const { apply, center } = setup()
    center.set(1, rep(['h:a']))
    center.set(2, rep(['h:b']))
    center.drop(1)
    expect(apply).toHaveBeenLastCalledWith(1)
    center.drop(2)
    expect(apply).toHaveBeenLastCalledWith(0)
  })

  it('dropping a window that has no report does nothing', () => {
    const { apply, center } = setup()
    center.drop(9)
    expect(apply).not.toHaveBeenCalled()
  })

  it('applies only when the number changes', () => {
    const { apply, center } = setup()
    center.set(1, rep(['h:a']))
    center.set(1, rep(['h:b']))
    center.set(2, rep(['h:b']))
    expect(apply).toHaveBeenCalledTimes(1)
  })

  it('clear() applies 0 once at start-up even though nothing was reported (a stale Dock badge from a crashed run)', () => {
    const { apply, center } = setup()
    center.clear()
    expect(apply).toHaveBeenCalledExactlyOnceWith(0)
    center.set(1, rep([]))
    expect(apply).toHaveBeenCalledTimes(1)
  })
})

// A webContents stand-in: an emitter with an id and an `isDestroyed`.
function fakeSender(id: number) {
  return Object.assign(new EventEmitter(), { id })
}

describe('submitBadgeReport — one `badge:set`', () => {
  const setup = () => {
    const apply = vi.fn()
    const center = new BadgeCenter(apply)
    return { apply, center, sender: fakeSender(5), onAttach: vi.fn() }
  }

  it('a valid report is recorded for the sender and counted', () => {
    const { apply, center, sender } = setup()
    expect(submitBadgeReport(center, sender, rep(['h:a']))).toBe(true)
    expect(apply).toHaveBeenLastCalledWith(1)
  })

  it('a malformed report is ignored whole: nothing recorded, nothing attached, an earlier report stays', () => {
    const { apply, center, sender, onAttach } = setup()
    submitBadgeReport(center, sender, rep(['h:a']), onAttach)
    apply.mockClear()
    expect(submitBadgeReport(center, sender, { sessions: 'no' }, onAttach)).toBe(false)
    expect(apply).not.toHaveBeenCalled()
    expect(onAttach).toHaveBeenCalledTimes(1)
  })

  it('the per-window hook runs once however many reports a window sends (no listener pile-up)', () => {
    const { center, sender, onAttach } = setup()
    for (let i = 0; i < 25; i++) submitBadgeReport(center, sender, rep([`h:${i}`]), onAttach)
    expect(onAttach).toHaveBeenCalledTimes(1)
    expect(sender.listenerCount('destroyed')).toBe(1)
  })

  it('the first report of a malformed-then-valid window still attaches', () => {
    const { center, sender, onAttach } = setup()
    submitBadgeReport(center, sender, null, onAttach)
    expect(onAttach).not.toHaveBeenCalled()
    submitBadgeReport(center, sender, rep(['h:a']), onAttach)
    expect(onAttach).toHaveBeenCalledTimes(1)
  })
})

describe('attachBadgeSender — when a window\'s report is dropped', () => {
  const nav = (over: Record<string, unknown> = {}) => ({ isMainFrame: true, isSameDocument: false, url: 'app://./index.html', ...over })
  const armed = () => {
    const apply = vi.fn()
    const center = new BadgeCenter(apply)
    const sender = fakeSender(7)
    center.set(7, rep(['h:a']))
    center.set(8, rep(['h:b']))
    attachBadgeSender(center, sender)
    apply.mockClear()
    return { apply, center, sender }
  }

  it('the window closing drops its report', () => {
    const { apply, sender } = armed()
    sender.emit('destroyed')
    expect(apply).toHaveBeenLastCalledWith(1)
  })

  it('the renderer going away drops its report', () => {
    const { apply, sender } = armed()
    sender.emit('render-process-gone', {}, { reason: 'crashed' })
    expect(apply).toHaveBeenLastCalledWith(1)
  })

  it('a main-frame navigation (a reload) drops its report', () => {
    const { apply, sender } = armed()
    sender.emit('did-start-navigation', nav())
    expect(apply).toHaveBeenLastCalledWith(1)
  })

  it('a sub-frame navigation does not', () => {
    const { apply, sender } = armed()
    sender.emit('did-start-navigation', nav({ isMainFrame: false }))
    expect(apply).not.toHaveBeenCalled()
  })

  it('a same-document navigation (the SPA\'s own routing) does not', () => {
    const { apply, sender } = armed()
    sender.emit('did-start-navigation', nav({ isSameDocument: true }))
    expect(apply).not.toHaveBeenCalled()
  })

  it('only that window\'s report goes; the other window\'s stays', () => {
    const { apply, center, sender } = armed()
    sender.emit('render-process-gone', {}, { reason: 'killed' })
    center.set(8, rep(['h:b'])) // unchanged
    expect(apply).toHaveBeenCalledTimes(1)
    expect(apply).toHaveBeenLastCalledWith(1)
  })

  it('attaching again to the same sender is a no-op and says so, so the caller can hang its own listeners once', () => {
    const { center } = armed()
    const fresh = fakeSender(9)
    expect(attachBadgeSender(center, fresh)).toBe(true)
    expect(attachBadgeSender(center, fresh)).toBe(false)
    expect(attachBadgeSender(center, fresh)).toBe(false)
  })

  it('attaching twice to one sender listens once (a window reports many times)', () => {
    const { center, sender } = armed()
    attachBadgeSender(center, sender)
    attachBadgeSender(center, sender)
    expect(sender.listenerCount('destroyed')).toBe(1)
    expect(sender.listenerCount('render-process-gone')).toBe(1)
    expect(sender.listenerCount('did-start-navigation')).toBe(1)
  })

  it('a window reporting again after a reload is counted afresh', () => {
    const { apply, center, sender } = armed()
    sender.emit('did-start-navigation', nav())
    center.set(7, rep(['h:a', 'h:z']))
    expect(apply).toHaveBeenLastCalledWith(3)
  })
})
