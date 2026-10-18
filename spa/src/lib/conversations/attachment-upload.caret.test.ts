// Where an upload's placeholder lands (#3393): at the caret the file was dropped / pasted at, carried along by the edits made while
// it was on its way; several files of one drop stay together, in the files' order, at that one point.
import { describe, it, expect, afterEach, vi } from 'vitest'
import { draftKey, readDraft, writeDraft, clearAllDrafts } from './draft-memory'
import { clearAllAttachments, readAttachments } from './attachment-memory'
import { clearAllUploads, startUploads } from './attachment-upload'
import { clearAllAnchors } from './insertion-anchor'
import { releasePane } from './pane-release'
import { terminalAdapter } from './terminal-adapter'

const mocks = vi.hoisted(() => ({ upload: vi.fn() }))
vi.mock('../host-api', async (orig) => ({ ...(await orig<typeof import('../host-api')>()), agentUploadToPath: mocks.upload }))

const KEY = draftKey('pane-c', 'h', 's1')
const upload = terminalAdapter('h', 's1', 'dev001').upload!
const png = (name: string) => new File(['x'], name, { type: 'image/png' })
const pdf = (name: string) => new File(['x'], name, { type: 'application/pdf' })
const flush = async () => { for (let i = 0; i < 4; i++) await Promise.resolve() }

/** Uploads settled by hand, one per file name. */
function held(...names: string[]) {
  const settle = new Map<string, () => void>()
  mocks.upload.mockImplementation((_h: string, f: File) => new Promise((res) => { settle.set(f.name, () => res({ filename: f.name, path: `/up/dev001/${f.name}` })) }))
  return { land: async (name: string) => { settle.get(name)!(); await flush() }, names }
}
const seqOf = (name: string) => readAttachments(KEY).find((a) => a.name === name)!.seq!

afterEach(() => { clearAllUploads(); clearAllAttachments(); clearAllDrafts(); clearAllAnchors(); mocks.upload.mockReset() })

describe('the placeholder lands at the caret', () => {
  it('in the middle of the draft, with a space on either side so it does not stick to a word', async () => {
    writeDraft(KEY, 'helloworld')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 5)
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`hello [Image #${seqOf('a.png')}] world`)
  })
  it('at the start and at the end', async () => {
    writeDraft(KEY, 'tail')
    const h = held('a.png', 'b.png')
    startUploads(KEY, upload, [png('a.png')], 0)
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`[Image #${seqOf('a.png')}] tail`)
    startUploads(KEY, upload, [png('b.png')], readDraft(KEY)!.length)
    await h.land('b.png')
    expect(readDraft(KEY)).toBe(`[Image #${seqOf('a.png')}] tail [Image #${seqOf('b.png')}]`)
  })
  it('no space is added where whitespace already is', async () => {
    writeDraft(KEY, 'a b')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 2)
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`a [Image #${seqOf('a.png')}] b`)
  })
  it('without a caret it is appended, as before', async () => {
    writeDraft(KEY, 'one two')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')])
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`one two [Image #${seqOf('a.png')}]`)
  })
  it('a caret past the end of the (shortened) draft falls back to the end', async () => {
    writeDraft(KEY, 'a long draft here')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 12)
    writeDraft(KEY, 'short')
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`short [Image #${seqOf('a.png')}]`)
  })
})

describe('the insertion point moves with the edits made while the upload runs', () => {
  it('text added before it pushes it along', async () => {
    writeDraft(KEY, 'hello world')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 5)
    writeDraft(KEY, 'XYZ hello world')
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`XYZ hello [Image #${seqOf('a.png')}] world`)
  })
  it('text removed before it pulls it back', async () => {
    writeDraft(KEY, 'hello world')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 5)
    writeDraft(KEY, 'llo world')
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`llo [Image #${seqOf('a.png')}] world`)
  })
  it('text added after it does not move it', async () => {
    writeDraft(KEY, 'hello world')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 5)
    writeDraft(KEY, 'hello world, again')
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`hello [Image #${seqOf('a.png')}] world, again`)
  })
  it('what is typed at the point itself goes before the placeholder (the reader keeps typing where the caret is)', async () => {
    writeDraft(KEY, 'hello')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 5)
    writeDraft(KEY, 'hello there')
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`hello there [Image #${seqOf('a.png')}]`)
  })
  it('the text around the point is replaced: the point goes to the end of what replaced it', async () => {
    writeDraft(KEY, 'one two three')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 6)
    writeDraft(KEY, 'one 2 three')
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`one 2 [Image #${seqOf('a.png')}] three`)
  })
  it('an upload that lands first moves the point of one still on its way', async () => {
    writeDraft(KEY, 'ab cd')
    const h = held('a.png', 'b.png')
    startUploads(KEY, upload, [png('a.png')], 1)
    startUploads(KEY, upload, [png('b.png')], 4)
    await h.land('a.png')
    await h.land('b.png')
    expect(readDraft(KEY)).toBe(`a [Image #${seqOf('a.png')}] b c [Image #${seqOf('b.png')}] d`)
  })
})

describe('several files of one drop', () => {
  it('stay together at the one point in the order of the files, whichever lands first, a space apart', async () => {
    writeDraft(KEY, 'hello world')
    const h = held('a.png', 'b.png', 'c.png')
    startUploads(KEY, upload, [png('a.png'), png('b.png'), png('c.png')], 5)
    await h.land('c.png')
    await h.land('a.png')
    await h.land('b.png')
    expect(readDraft(KEY)).toBe(`hello [Image #${seqOf('a.png')}] [Image #${seqOf('b.png')}] [Image #${seqOf('c.png')}] world`)
  })
  it('a file that is not an image puts its path on a line of its own at the same point', async () => {
    writeDraft(KEY, 'before after')
    const h = held('a.png', 'doc.pdf')
    startUploads(KEY, upload, [png('a.png'), pdf('doc.pdf')], 6)
    await h.land('doc.pdf')
    expect(readDraft(KEY)).toBe('before\n/up/dev001/doc.pdf\n after')
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`before [Image #${seqOf('a.png')}]\n/up/dev001/doc.pdf\n after`)
  })
})

describe('the point is kept outside the input, per key', () => {
  it('a failed upload leaves no point behind that would move a later one', async () => {
    writeDraft(KEY, 'hello world')
    mocks.upload.mockRejectedValueOnce(Object.assign(new Error('net'), { kind: 'network' }))
    startUploads(KEY, upload, [png('bad.png')], 5)
    await flush()
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 11)
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`hello world [Image #${seqOf('a.png')}]`)
  })
  it('a released pane writes nothing', async () => {
    writeDraft(KEY, 'hello world')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 5)
    releasePane('pane-c')
    await h.land('a.png')
    expect(readDraft(KEY) ?? '').not.toContain('[Image')
  })
})

describe('an edit that repeats the text next to it (the real caret of the edit settles where it was)', () => {
  it('text typed at the start that repeats the draft: the point moves with it', async () => {
    writeDraft(KEY, 'foo foo')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 3)
    writeDraft(KEY, 'foo foo foo', 4) // "foo " typed at the start, the caret ends up behind it
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`foo foo [Image #${seqOf('a.png')}] foo`)
  })
  it('a repeated character typed just before the point: the point moves with it', async () => {
    writeDraft(KEY, 'aa')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 1)
    writeDraft(KEY, 'aaa', 2) // typed between the two a, at the point: the caret is behind it
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`aa [Image #${seqOf('a.png')}] a`)
  })
  it('a delete next to a repeat: the point is pulled back by what was removed', async () => {
    writeDraft(KEY, 'foo foo bar')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 5)
    writeDraft(KEY, 'foo bar', 0) // the first "foo " removed, the caret at the start
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`f [Image #${seqOf('a.png')}] oo bar`)
  })
  it('a caret that does not fit the change (a hint from somewhere else) falls back to the comparison of the two drafts', async () => {
    writeDraft(KEY, 'hello world')
    const h = held('a.png')
    startUploads(KEY, upload, [png('a.png')], 5)
    writeDraft(KEY, 'XYZ hello world', 2)
    await h.land('a.png')
    expect(readDraft(KEY)).toBe(`XYZ hello [Image #${seqOf('a.png')}] world`)
  })
})
