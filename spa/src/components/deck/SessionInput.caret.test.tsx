// #3393: a file dropped on, pasted into or picked for the session input leaves its placeholder at the caret the box had at that moment
// (not at the end of the draft), and a paste that also carries text puts the placeholder after that text.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { SessionInput } from './SessionInput'
import { clearAllDrafts } from '../../lib/conversations/draft-memory'
import { clearAllAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { clearAllAnchors } from '../../lib/conversations/insertion-anchor'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'
import { dropTargetIn } from '../../lib/conversations/pane-drop'

const mocks = vi.hoisted(() => ({ fetch: vi.fn(), upload: vi.fn() }))
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: mocks.fetch, agentUploadToPath: mocks.upload }))

const SID = '11111111-2222-4333-8444-555555555555'
const ui = () => <SessionInput paneKey="p1" hostId="h" sessionId={SID} sessionCode="dev001" capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const png = (name = 'shot.png') => new File(['x'], name, { type: 'image/png' })
const flush = () => act(async () => { for (let i = 0; i < 4; i++) await Promise.resolve() })
const type = (value: string, caret: number) => { fireEvent.change(box(), { target: { value } }); box().setSelectionRange(caret, caret) }

/** Uploads that land only when `land()` says so. */
function held() {
  const settle: Array<() => void> = []
  mocks.upload.mockImplementation((_h: string, f: File) => new Promise((res) => { settle.push(() => res({ filename: f.name, path: `/up/dev001/${f.name}` })) }))
  return { land: async () => { settle.splice(0).forEach((s) => s()); await flush() } }
}

beforeEach(() => {
  URL.createObjectURL = vi.fn(() => 'blob:test/x'); URL.revokeObjectURL = vi.fn()
  mocks.fetch.mockReset()
})
afterEach(() => { cleanup(); clearAllUploads(); clearAllDrafts(); clearAllAttachments(); clearAllSendQueues(); clearAllAnchors(); mocks.upload.mockReset() })

describe('SessionInput: the placeholder goes where the caret was', () => {
  it('a drop with the caret in the middle of the draft', async () => {
    render(ui())
    const up = held()
    type('helloworld', 5)
    fireEvent.drop(screen.getByTestId('session-input-drop'), { dataTransfer: { files: [png()], types: ['Files'] } })
    await up.land()
    expect(box().value).toBe('hello [Image #1] world')
  })

  it('a paste of a picture with the caret in the middle of the draft', async () => {
    render(ui())
    const up = held()
    type('helloworld', 5)
    fireEvent.paste(box(), { clipboardData: { files: [png()], getData: () => '', types: ['Files'] } })
    await up.land()
    expect(box().value).toBe('hello [Image #1] world')
  })

  it('a paste that carries text too: the browser puts the text at the caret, the placeholder follows that text', async () => {
    render(ui())
    const up = held()
    type('helloworld', 5)
    fireEvent.paste(box(), { clipboardData: { files: [png()], getData: () => 'XX', types: ['Files', 'text/plain'] } })
    fireEvent.change(box(), { target: { value: 'helloXXworld' } }) // what the browser's own paste does next
    await up.land()
    expect(box().value).toBe('helloXX [Image #1] world')
  })

  it('files picked with the attach button', async () => {
    render(ui())
    const up = held()
    type('helloworld', 5)
    fireEvent.change(screen.getByTestId('attach-file-input'), { target: { files: [png()] } })
    await up.land()
    expect(box().value).toBe('hello [Image #1] world')
  })

  it('a file handed to the pane-level drop target (the pane takes a drop anywhere on it)', async () => {
    const { container } = render(ui())
    const up = held()
    type('helloworld', 5)
    dropTargetIn(container)!([png()])
    await up.land()
    expect(box().value).toBe('hello [Image #1] world')
  })

  it('typing before the point while the upload runs moves the placeholder along', async () => {
    render(ui())
    const up = held()
    type('helloworld', 5)
    fireEvent.drop(screen.getByTestId('session-input-drop'), { dataTransfer: { files: [png()], types: ['Files'] } })
    fireEvent.change(box(), { target: { value: '>> helloworld' } })
    await up.land()
    expect(box().value).toBe('>> hello [Image #1] world')
  })

  it('two pictures dropped together stay in order at the one point', async () => {
    render(ui())
    const up = held()
    type('helloworld', 5)
    fireEvent.drop(screen.getByTestId('session-input-drop'), { dataTransfer: { files: [png('a.png'), png('b.png')], types: ['Files'] } })
    await up.land()
    expect(box().value).toBe('hello [Image #1] [Image #2] world')
  })
})
