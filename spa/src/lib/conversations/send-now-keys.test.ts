// spa/src/lib/conversations/send-now-keys.test.ts — the Deck input's send-now keys (D8, L6): Ctrl+Enter, and Ctrl+X then Ctrl+S inside 1.5 s.
import { describe, it, expect } from 'vitest'
import { CHORD_MS, sendNowKey } from './send-now-keys'

const k = (key: string, over: Record<string, boolean> = {}) => ({ key, shiftKey: false, altKey: false, metaKey: false, ctrlKey: false, ...over })
const ctrl = (key: string) => k(key, { ctrlKey: true })

describe('sendNowKey', () => {
  it('Ctrl+Enter sends now, at once', () => {
    expect(sendNowKey(ctrl('Enter'), null, 0)).toEqual({ action: 'send_now', armedAt: null })
  })
  it('a plain Enter, Shift+Enter and Cmd+Enter are not ours', () => {
    for (const e of [k('Enter'), k('Enter', { shiftKey: true }), k('Enter', { metaKey: true }), k('Enter', { ctrlKey: true, altKey: true })]) {
      expect(sendNowKey(e, null, 0).action).toBe('none')
    }
  })
  it('Ctrl+X arms (and is not swallowed: its normal cut stays); Ctrl+S inside the window sends now', () => {
    const a = sendNowKey(ctrl('x'), null, 1000)
    expect(a).toEqual({ action: 'arm', armedAt: 1000 })
    expect(sendNowKey(ctrl('s'), a.armedAt, 1000 + CHORD_MS)).toEqual({ action: 'send_now', armedAt: null })
  })
  it('Ctrl+S after the window, or with no Ctrl+X before, is not ours', () => {
    expect(sendNowKey(ctrl('s'), 1000, 1000 + CHORD_MS + 1)).toEqual({ action: 'none', armedAt: null })
    expect(sendNowKey(ctrl('s'), null, 5000)).toEqual({ action: 'none', armedAt: null })
  })
  it('any other key disarms; a bare modifier press does not', () => {
    expect(sendNowKey(k('a'), 1000, 1100)).toEqual({ action: 'none', armedAt: null })
    expect(sendNowKey(ctrl('x'), 1000, 1100)).toEqual({ action: 'arm', armedAt: 1100 }) // a second Ctrl+X re-arms
    for (const m of ['Control', 'Shift', 'Alt', 'Meta']) expect(sendNowKey(k(m), 1000, 1100)).toEqual({ action: 'none', armedAt: 1000 })
  })
  it('the letter is case-blind (Caps Lock)', () => {
    expect(sendNowKey(ctrl('X'), null, 0).action).toBe('arm')
    expect(sendNowKey(ctrl('S'), 0, 10).action).toBe('send_now')
  })
})
