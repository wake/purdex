// spa/src/lib/find-shortcut.test.ts — Mod+F (R3 plan T3.3).
import { describe, it, expect } from 'vitest'
import { isFindShortcut } from './find-shortcut'

const key = (over: Partial<KeyboardEvent>) =>
  ({ key: 'f', metaKey: false, ctrlKey: false, shiftKey: false, altKey: false, ...over }) as KeyboardEvent

describe('isFindShortcut', () => {
  it('is Cmd+F on a Mac', () => {
    expect(isFindShortcut(key({ metaKey: true }), true)).toBe(true)
    expect(isFindShortcut(key({ key: 'F', metaKey: true }), true)).toBe(true)
    expect(isFindShortcut(key({ ctrlKey: true }), true)).toBe(false)
  })

  it('is Ctrl+F elsewhere', () => {
    expect(isFindShortcut(key({ ctrlKey: true }), false)).toBe(true)
    expect(isFindShortcut(key({ metaKey: true }), false)).toBe(false)
  })

  it('takes no other modifier and no other key', () => {
    expect(isFindShortcut(key({ metaKey: true, shiftKey: true }), true)).toBe(false)
    expect(isFindShortcut(key({ metaKey: true, altKey: true }), true)).toBe(false)
    expect(isFindShortcut(key({ metaKey: true, ctrlKey: true }), true)).toBe(false)
    expect(isFindShortcut(key({ key: 'g', metaKey: true }), true)).toBe(false)
    expect(isFindShortcut(key({}), true)).toBe(false)
  })
})
