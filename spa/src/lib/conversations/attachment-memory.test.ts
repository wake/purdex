import { describe, it, expect, afterEach } from 'vitest'
import { visibleAttachments, trackUpload, addAttachment, attachmentText, clearAllAttachments, forgetAttachmentsWhere, insertAttachmentText, isImageFile, readAttachments, removeAttachment, removeAttachmentText } from './attachment-memory'

afterEach(() => clearAllAttachments())

describe('attachment draft text', () => {
  it('an image becomes [Image: source: <path>]; any other file is only its path', () => {
    expect(attachmentText('/up/a.png', true)).toBe('[Image: source: /up/a.png]')
    expect(attachmentText('/up/a.pdf', false)).toBe('/up/a.pdf')
  })
  it('goes straight in an empty draft and on its own line after text', () => {
    expect(insertAttachmentText('', '/up/a.pdf')).toBe('/up/a.pdf')
    expect(insertAttachmentText('look at', '/up/a.pdf')).toBe('look at\n/up/a.pdf')
    expect(insertAttachmentText('look at\n', '/up/a.pdf')).toBe('look at\n/up/a.pdf')
  })
  it('removing takes out only that line and leaves the rest of the draft alone', () => {
    expect(removeAttachmentText('look at\n/up/a.pdf', '/up/a.pdf')).toBe('look at')
    expect(removeAttachmentText('/up/a.pdf\nthen this', '/up/a.pdf')).toBe('then this')
    expect(removeAttachmentText('a\n/up/a.pdf\nb', '/up/a.pdf')).toBe('a\nb')
    expect(removeAttachmentText('/up/a.pdf', '/up/a.pdf')).toBe('')
    expect(removeAttachmentText('typed it away', '/up/a.pdf')).toBe('typed it away')
  })
  it('knows an image by type or extension', () => {
    expect(isImageFile(new File([''], 'x.bin', { type: 'image/png' }))).toBe(true)
    expect(isImageFile(new File([''], 'x.JPG'))).toBe(true)
    expect(isImageFile(new File([''], 'x.pdf', { type: 'application/pdf' }))).toBe(false)
  })
})

describe('attachment chips memory', () => {
  const att = (id: string) => ({ id, name: `${id}.png`, path: `/up/${id}.png`, text: `[Image: source: /up/${id}.png]` })
  it('keeps chips per key, in order, and removes one by id', () => {
    addAttachment('k', att('a')); addAttachment('k', att('b')); addAttachment('o', att('c'))
    expect(readAttachments('k').map((a) => a.id)).toEqual(['a', 'b'])
    removeAttachment('k', 'a')
    expect(readAttachments('k').map((a) => a.id)).toEqual(['b'])
    removeAttachment('k', 'b')
    expect(readAttachments('k')).toEqual([])
  })
  it('a release aborts the uploads of the matching keys only', () => {
    const a = new AbortController(), b = new AbortController()
    trackUpload('p1|h|s', a); trackUpload('p2|h|s', b)
    forgetAttachmentsWhere((k) => k.startsWith('p1|'))
    expect([a.signal.aborted, b.signal.aborted]).toEqual([true, false])
  })
  it('forgets by predicate', () => {
    addAttachment('p1|h|s', att('a')); addAttachment('p2|h|s', att('b'))
    forgetAttachmentsWhere((k) => k.startsWith('p1|'))
    expect(readAttachments('p1|h|s')).toEqual([])
    expect(readAttachments('p2|h|s')).toHaveLength(1)
  })
})

describe('visibleAttachments: the draft is the truth', () => {
  const a = (id: string, text: string) => ({ id, name: id, path: text, text })
  it('a chip whose marker line is gone from the draft is not shown', () => {
    const list = [a('1', '/up/a.pdf'), a('2', '/up/b.pdf')]
    expect(visibleAttachments('x\n/up/b.pdf', list).map((c) => c.id)).toEqual(['2'])
    expect(visibleAttachments('', list)).toEqual([])
  })
  it('a rewritten line (not exactly the marker) does not count', () => {
    expect(visibleAttachments('/up/a.pdf edited', [a('1', '/up/a.pdf')])).toEqual([])
  })
  it('identical markers need one line each', () => {
    const list = [a('1', '/up/a.pdf'), a('2', '/up/a.pdf')]
    expect(visibleAttachments('/up/a.pdf', list).map((c) => c.id)).toEqual(['1'])
    expect(visibleAttachments('/up/a.pdf\n/up/a.pdf', list)).toHaveLength(2)
  })
})
