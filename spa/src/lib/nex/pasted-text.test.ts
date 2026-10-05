// spa/src/lib/nex/pasted-text.test.ts — U3 (spec §5.3 "Pasted text"): a human
// prompt's `<pasted_content id="…">…</pasted_content>` segments become their
// own blocks, without the wrapper.
import { describe, it, expect } from 'vitest'
import { splitPasted } from './pasted-text'
import { foldPlan } from './fold'
import type { ContentBlock } from './message-types'

const text = (t: string, extra: Partial<ContentBlock> = {}): ContentBlock => ({ type: 'text', text: t, ...extra })
const typed = (t: string): ContentBlock => ({ type: 'text', text: t })
const paste = (t: string, lines: number, cut = false): ContentBlock => ({ type: 'text', text: t, pasted: { lines, cut } })
const open = (id = 'bb1b') => `<pasted_content id="${id}">`
const CLOSE = '</pasted_content>'

describe('splitPasted', () => {
  it('returns the block itself when it holds no opening tag', () => {
    const b = text('just typed')
    expect(splitPasted(b)).toEqual([b])
    expect(splitPasted(b)[0]).toBe(b)
  })

  it('leaves a non-text block alone', () => {
    const b: ContentBlock = { type: 'tool_result', tool_use_id: 't', content: `${open()}\nx\n${CLOSE}` }
    expect(splitPasted(b)).toEqual([b])
  })

  it('a whole-message paste is one pasted block, wrapper gone (the live capture\'s shape)', () => {
    const body = 'Reply with just the word ok. …\nfiller line 00000 …\nfiller line 00001 …'
    expect(splitPasted(text(`${open()}\n${body}\n${CLOSE}`))).toEqual([paste(body, 3)])
  })

  it('typed + paste + typed keeps the typed parts as they are, in order', () => {
    expect(splitPasted(text(`fix this:\n${open('a')}\nerr 1\nerr 2\n${CLOSE}\nthanks`))).toEqual([
      typed('fix this:\n'), paste('err 1\nerr 2', 2), typed('\nthanks'),
    ])
  })

  it('two pastes with typed text between them', () => {
    expect(splitPasted(text(`${open('1')}\na\n${CLOSE} and ${open('2')}\nb\nc\n${CLOSE}`))).toEqual([
      paste('a', 1), typed(' and '), paste('b\nc', 2),
    ])
  })

  it('drops empty typed parts (adjacent pastes, a paste at either end)', () => {
    expect(splitPasted(text(`${open('1')}\na\n${CLOSE}${open('2')}\nb\n${CLOSE}`))).toEqual([paste('a', 1), paste('b', 1)])
  })

  it('an opening tag with no closing tag runs to the end and is cut', () => {
    expect(splitPasted(text(`see ${open()}\nl1\nl2\nl3`))).toEqual([typed('see '), paste('l1\nl2\nl3', 3, true)])
  })

  it('a stray closing tag stays literal, alone or after a closed paste', () => {
    const stray = text(`a ${CLOSE} b`)
    expect(splitPasted(stray)).toEqual([stray])
    expect(splitPasted(text(`${open()}\nx\n${CLOSE} tail ${CLOSE}`))).toEqual([paste('x', 1), typed(` tail ${CLOSE}`)])
  })

  it('an opening tag that does not match <pasted_content id="…"> stays literal', () => {
    for (const s of [`<pasted_content>\nx\n${CLOSE}`, `<pasted_content id=x>\nx\n${CLOSE}`, `<pasted_content id="x" >\nx\n${CLOSE}`]) {
      const b = text(s)
      expect(splitPasted(b), s).toEqual([b])
    }
    // An empty id is still an id.
    expect(splitPasted(text(`${open('')}\nx\n${CLOSE}`))).toEqual([paste('x', 1)])
  })

  it('exactly one newline after the opening tag and one before the closing tag belong to the wrapper', () => {
    expect(splitPasted(text(`${open()}\n\nx\n\n${CLOSE}`))).toEqual([paste('\nx\n', 2)])
    expect(splitPasted(text(`${open()}x${CLOSE}`))).toEqual([paste('x', 1)])
    expect(splitPasted(text(`${open()}\n${CLOSE}`))).toEqual([paste('', 0)])
  })

  it('counts lines the way the fold plan does (a trailing newline ends the last line)', () => {
    for (const body of ['a', 'a\nb', 'a\nb\n', '\n', 'a\n\nb']) {
      const [b] = splitPasted(text(`${open()}\n${body}\n${CLOSE}`))
      expect(b.pasted?.lines, JSON.stringify(body)).toBe(foldPlan({ text: body }).totalLines)
    }
  })

  it('moves truncated / total_bytes to the last block produced, and only there', () => {
    const out = splitPasted(text(`see ${open()}\nl1\nl2`, { truncated: true, total_bytes: 90000 }))
    expect(out).toEqual([typed('see '), { ...paste('l1\nl2', 2, true), truncated: true, total_bytes: 90000 }])
    expect('truncated' in out[0]).toBe(false)
    expect('total_bytes' in out[0]).toBe(false)
  })

  it('a block cut inside a typed suffix: the paste closed, so it is NOT cut; the flags sit on the suffix', () => {
    const out = splitPasted(text(`${open()}\nbody\n${CLOSE}\nthe typed suf`, { truncated: true, total_bytes: 70000 }))
    expect(out).toEqual([paste('body', 1), { ...typed('\nthe typed suf'), truncated: true, total_bytes: 70000 }])
  })
})
