import { describe, it, expect, afterEach } from 'vitest'
import { clearAllAttachments, forgetAttachmentsWhere, nextAttachmentSeq, visibleAttachments } from './attachment-memory'
import {
  expandPlaceholders, imagePlaceholder, insertPlaceholderText, placeholderRanges, removePlaceholderText, splitImageRefs,
} from './image-placeholder'

afterEach(() => clearAllAttachments())

const chip = (seq: number, path = `/up/${seq}.png`) => ({ id: `c${seq}`, name: `${seq}.png`, path, text: imagePlaceholder(seq), image: true, seq })

describe('placeholder numbers', () => {
  it('count up across every draft (no two panes share a number) and are never handed out again', () => {
    expect([nextAttachmentSeq(), nextAttachmentSeq(), nextAttachmentSeq()]).toEqual([1, 2, 3])
    forgetAttachmentsWhere(() => true) // a send or a release does not start the count over
    expect(nextAttachmentSeq()).toBe(4)
  })
})

describe('inserting a placeholder', () => {
  it('goes straight in an empty draft, after a space behind text, and as it is behind whitespace', () => {
    expect(insertPlaceholderText('', '[Image #1]')).toBe('[Image #1]')
    expect(insertPlaceholderText('look', '[Image #1]')).toBe('look [Image #1]')
    expect(insertPlaceholderText('look\n', '[Image #2]')).toBe('look\n[Image #2]')
    expect(insertPlaceholderText('[Image #1] ', '[Image #2]')).toBe('[Image #1] [Image #2]')
  })
})

describe('expanding placeholders for sending', () => {
  it('turns each [Image #N] of a known chip into the full marker and leaves the rest alone', () => {
    const list = [chip(1, '/up/a.png'), chip(2, '/up/b b.png')]
    expect(expandPlaceholders('see [Image #1] and [Image #2] now', list)).toBe('see [Image: source: /up/a.png] and [Image: source: /up/b b.png] now')
    expect(expandPlaceholders('[Image #1]\n[Image #2]', list)).toBe('[Image: source: /up/a.png]\n[Image: source: /up/b b.png]')
  })
  it('a placeholder the reader broke is plain text', () => {
    const list = [chip(1)]
    expect(expandPlaceholders('[Image #1', list)).toBe('[Image #1')
    expect(expandPlaceholders('[Image #]', list)).toBe('[Image #]')
    expect(expandPlaceholders('[Imag #1]', list)).toBe('[Imag #1]')
    expect(expandPlaceholders('[Image #1]]', list)).toBe('[Image: source: /up/1.png]]')
  })
  it('a number no chip owns is left as typed', () => {
    expect(expandPlaceholders('[Image #7]', [chip(1)])).toBe('[Image #7]')
  })
  it('the same number twice expands twice (both name the same file)', () => {
    expect(expandPlaceholders('[Image #1] [Image #1]', [chip(1, '/up/a.png')])).toBe('[Image: source: /up/a.png] [Image: source: /up/a.png]')
  })
  it('a path with $ & characters comes through whole', () => {
    expect(expandPlaceholders('[Image #1]', [chip(1, '/up/$&-x.png')])).toBe('[Image: source: /up/$&-x.png]')
  })
})

describe('which chips the draft still shows', () => {
  it('a placeholder chip needs its placeholder somewhere in the draft (in the middle of a line too)', () => {
    const list = [chip(1), chip(2)]
    expect(visibleAttachments('a [Image #2] b', list).map((c) => c.seq)).toEqual([2])
    expect(visibleAttachments('[Image #1] [Image #2]', list).map((c) => c.seq)).toEqual([1, 2])
    expect(visibleAttachments('[Image #2', list)).toEqual([])
  })
  it('a path chip still needs its own exact line', () => {
    const pdf = { id: 'p', name: 'a.pdf', path: '/up/a.pdf', text: '/up/a.pdf' }
    expect(visibleAttachments('x /up/a.pdf', [pdf])).toEqual([])
    expect(visibleAttachments('x\n/up/a.pdf', [pdf])).toHaveLength(1)
  })
})

describe('removing a placeholder', () => {
  it('takes every copy of it out with one space beside it; a line that was only the placeholder goes with its break', () => {
    expect(removePlaceholderText('look\n[Image #1]', '[Image #1]')).toBe('look')
    expect(removePlaceholderText('[Image #1]\nthen', '[Image #1]')).toBe('then')
    expect(removePlaceholderText('a [Image #1] b', '[Image #1]')).toBe('a b')
    expect(removePlaceholderText('[Image #1] [Image #2]', '[Image #1]')).toBe('[Image #2]')
    expect(removePlaceholderText('[Image #1] [Image #2]', '[Image #2]')).toBe('[Image #1]')
    expect(removePlaceholderText('[Image #1] x [Image #1]', '[Image #1]')).toBe('x')
    expect(removePlaceholderText('a[Image #1]b', '[Image #1]')).toBe('ab')
    expect(removePlaceholderText('[Image #2]', '[Image #1]')).toBe('[Image #2]')
  })
})

describe('placeholder ranges (for the tag overlay)', () => {
  it('lists where the known placeholders sit', () => {
    expect(placeholderRanges('ab [Image #1] cd [Image #9] [Image #2]', new Set([1, 2]))).toEqual([{ start: 3, end: 13 }, { start: 28, end: 38 }])
  })
})

describe('image references in a sent message', () => {
  it('splits the text around [Image: source: path], keeping the order', () => {
    expect(splitImageRefs('look [Image: source: /up/a.png] and\n[Image: source: /up/b.png]\nend')).toEqual([
      { kind: 'text', text: 'look ' },
      { kind: 'image', path: '/up/a.png' },
      { kind: 'text', text: ' and\n' },
      { kind: 'image', path: '/up/b.png' },
      { kind: 'text', text: '\nend' },
    ])
  })
  it('text with none is one text part; a broken marker stays text', () => {
    expect(splitImageRefs('hello')).toEqual([{ kind: 'text', text: 'hello' }])
    expect(splitImageRefs('[Image: source: /up/a.png')).toEqual([{ kind: 'text', text: '[Image: source: /up/a.png' }])
    expect(splitImageRefs('')).toEqual([])
  })
})
