// spa/src/lib/nex/pasted-text.test.ts — U3 (spec §5.3 "Pasted text"): a human
// prompt's `<pasted_content id="hhhh">\n…\n</pasted_content id="hhhh">`
// segments (Claude Code's own format) become their own blocks, without the wrapper.
import { describe, it, expect } from 'vitest'
import { splitPasted } from './pasted-text'
import { foldPlan, utf8Length } from './fold'
import type { ContentBlock } from './message-types'

const text = (t: string, extra: Partial<ContentBlock> = {}): ContentBlock => ({ type: 'text', text: t, ...extra })
const typed = (t: string): ContentBlock => ({ type: 'text', text: t })
const paste = (t: string, lines: number, cut = false): ContentBlock => ({ type: 'text', text: t, pasted: { lines, cut } })
const open = (id = 'bb1b') => `<pasted_content id="${id}">\n`
const close = (id = 'bb1b') => `\n</pasted_content id="${id}">`
const wrap = (body: string, id = 'bb1b') => `${open(id)}${body}${close(id)}`

describe('splitPasted', () => {
  it('returns the block itself when it holds no opening tag', () => {
    const b = text('just typed')
    expect(splitPasted(b)).toEqual([b])
    expect(splitPasted(b)[0]).toBe(b)
  })

  it('leaves a non-text block alone', () => {
    const b: ContentBlock = { type: 'tool_result', tool_use_id: 't', content: wrap('x') }
    expect(splitPasted(b)).toEqual([b])
  })

  it('a whole-message paste is one pasted block, wrapper and its newlines gone (the live capture\'s shape)', () => {
    const body = 'Reply with just the word ok. …\nfiller line 00000 …\nfiller line 00001 …'
    expect(splitPasted(text(`\n\n${wrap(body)}\n`))).toEqual([paste(body, 3)])
  })

  it('typed + paste + typed: up to two newlines on either side belong to the wrapper', () => {
    expect(splitPasted(text(`fix this:\n${wrap('err 1\nerr 2', 'a1b2')}\nthanks`))).toEqual([
      typed('fix this:'), paste('err 1\nerr 2', 2), typed('thanks'),
    ])
    expect(splitPasted(text(`fix this:\n\n${wrap('err 1\nerr 2', 'a1b2')}\n\nthanks`))).toEqual([
      typed('fix this:'), paste('err 1\nerr 2', 2), typed('thanks'),
    ])
  })

  it('a third newline stays with the typed text, on either side', () => {
    expect(splitPasted(text(`fix this:\n\n\n${wrap('err', 'a1b2')}\n\n\nthanks`))).toEqual([
      typed('fix this:\n'), paste('err', 1), typed('\nthanks'),
    ])
  })

  it('two pastes with different ids, typed text between them', () => {
    expect(splitPasted(text(`${wrap('a', '0001')} and ${wrap('b\nc', '0002')}`))).toEqual([
      paste('a', 1), typed(' and '), paste('b\nc', 2),
    ])
  })

  it('drops empty typed parts (adjacent pastes, a paste at either end)', () => {
    expect(splitPasted(text(`${wrap('a', 'aaaa')}\n\n${wrap('b', 'bbbb')}`))).toEqual([paste('a', 1), paste('b', 1)])
  })

  it('a literal </pasted_content> and another paste\'s tags inside a body never end it', () => {
    const body = `x\n</pasted_content>\n${wrap('inner', 'aaaa')}\ny`
    expect(splitPasted(text(`see:\n${wrap(body, 'bbbb')}`))).toEqual([typed('see:'), paste(body, 6)])
  })

  it('a closer with a different id does not close the paste', () => {
    const b = text(`${open('aaaa')}x${close('bbbb')}`)
    expect(splitPasted(b)).toEqual([b])
  })

  it('an opening tag with an invalid id, or without its `">` + newline, stays literal', () => {
    for (const s of [
      `<pasted_content id="BB1B">\nx\n</pasted_content id="BB1B">`, // uppercase
      `<pasted_content id="bb1">\nx\n</pasted_content id="bb1">`, // 3 chars
      `<pasted_content id="bb1b5">\nx\n</pasted_content id="bb1b5">`, // 5 chars
      `<pasted_content id="zzzz">\nx\n</pasted_content id="zzzz">`, // not hex
      `<pasted_content id="bb1b">x${close()}`, // no newline after the tag
      `<pasted_content id="bb1b" >\nx${close()}`, // not `">`
      `<pasted_content>\nx\n</pasted_content>`, // no id at all
    ]) {
      const b = text(s)
      expect(splitPasted(b), s).toEqual([b])
    }
  })

  it('the search goes on past an invalid opening tag', () => {
    expect(splitPasted(text(`<pasted_content id="BB1B">\nx ${wrap('y')}`))).toEqual([
      typed('<pasted_content id="BB1B">\nx '), paste('y', 1),
    ])
  })

  it('an unclosed opening tag in a block the daemon did NOT cut stays literal, as the CLI keeps it', () => {
    const b = text(`see ${open()}l1\nl2`)
    expect(splitPasted(b)).toEqual([b])
    expect(splitPasted(text(`${wrap('a', 'aaaa')} then ${open()}l1`))).toEqual([paste('a', 1), typed(` then ${open()}l1`)])
  })

  it('an unclosed opening tag in a cut block runs to the end and is cut', () => {
    const t = `see\n\n${open()}l1\nl2\nl3 ${wrap('inner', 'aaaa')}`
    expect(splitPasted(text(t, { truncated: true }))).toEqual([
      typed('see'), { ...paste(`l1\nl2\nl3 ${wrap('inner', 'aaaa')}`, 5, true), truncated: true, shown_bytes: t.length },
    ])
  })

  it('a cut that fell inside the closer itself leaves no fragment of it in the body', () => {
    for (const frag of ['\n', '\n<', '\n</pasted_con', '\n</pasted_content id="bb', '\n</pasted_content id="bb1b"']) {
      const [b] = splitPasted(text(`${open()}l1\nl2${frag}`, { truncated: true }))
      expect(b.text, JSON.stringify(frag)).toBe('l1\nl2')
    }
  })

  it('an empty body and a body that is one newline', () => {
    expect(splitPasted(text('<pasted_content id="bb1b">\n</pasted_content id="bb1b">'))).toEqual([paste('', 0)])
    expect(splitPasted(text(wrap('')))).toEqual([paste('', 0)])
    expect(splitPasted(text(wrap('\n')))).toEqual([paste('\n', 1)])
  })

  it('counts lines the way the fold plan does (a trailing newline ends the last line)', () => {
    for (const body of ['a', 'a\nb', 'a\nb\n', '\n', 'a\n\nb']) {
      const [b] = splitPasted(text(wrap(body)))
      expect(b.text, JSON.stringify(body)).toBe(body)
      expect(b.pasted?.lines, JSON.stringify(body)).toBe(foldPlan({ text: body }).totalLines)
    }
  })

  it('moves truncated / total_bytes and the whole block\'s shown size to the last block produced, and only there', () => {
    const t = `see … ${open()}l1\nl2`
    const out = splitPasted(text(t, { truncated: true, total_bytes: 90000 }))
    expect(out).toEqual([typed('see … '), { ...paste('l1\nl2', 2, true), truncated: true, total_bytes: 90000, shown_bytes: utf8Length(t) }])
    expect(utf8Length(t)).toBeGreaterThan(t.length) // bytes, not UTF-16 units
    for (const k of ['truncated', 'total_bytes', 'shown_bytes']) expect(k in out[0], k).toBe(false)
  })

  it('a block cut inside a typed suffix: the paste closed, so it is NOT cut; the flags and the shown size sit on the suffix', () => {
    const t = `${wrap('body')}\nthe typed suf`
    const out = splitPasted(text(t, { truncated: true, total_bytes: 70000 }))
    expect(out).toEqual([paste('body', 1), { ...typed('the typed suf'), truncated: true, total_bytes: 70000, shown_bytes: t.length }])
  })

  it('a block the daemon did not cut gets no shown size', () => {
    const out = splitPasted(text(`${wrap('body')} tail`))
    expect(out).toEqual([paste('body', 1), typed(' tail')])
    expect(out.some((b) => 'shown_bytes' in b)).toBe(false)
  })
})
