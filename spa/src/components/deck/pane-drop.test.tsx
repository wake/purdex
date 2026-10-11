// A file dropped anywhere on a session pane (not only on the input) is attached to the pane's input, and the whole pane lights up meanwhile
// (Mac feedback 10-11). A pane without an input takes nothing.
import { useRef } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { SessionInput } from './SessionInput'
import { usePaneFileDrop } from '../../hooks/usePaneFileDrop'
import { clearAllDrafts } from '../../lib/conversations/draft-memory'
import { clearAllAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'

const mocks = vi.hoisted(() => ({ fetch: vi.fn(), upload: vi.fn() }))
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: mocks.fetch, agentUploadToPath: mocks.upload }))

const SID = '11111111-2222-4333-8444-555555555555'
function Pane({ withInput = true }: { withInput?: boolean }) {
  const ref = useRef<HTMLDivElement>(null)
  const drop = usePaneFileDrop(ref)
  return (
    <div ref={ref} data-testid="pane" {...drop.handlers}>
      <div data-testid="stream">the stream</div>
      {withInput && <SessionInput paneKey="p1" hostId="h" sessionId={SID} sessionCode="dev001" capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />}
      {drop.overlay}
    </div>
  )
}
const png = () => new File(['x'], 'shot.png', { type: 'image/png' })
const files = (f: File[]) => ({ dataTransfer: { files: f, types: ['Files'] } })
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve() })

beforeEach(() => {
  URL.createObjectURL = vi.fn(() => 'blob:test/x'); URL.revokeObjectURL = vi.fn()
  mocks.fetch.mockReset().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ status: 'accepted' }))))
  mocks.upload.mockReset().mockImplementation((_h: string, f: File) => Promise.resolve({ filename: f.name, path: `/up/dev001/${f.name}` }))
})
afterEach(() => { cleanup(); clearAllUploads(); clearAllDrafts(); clearAllAttachments(); clearAllSendQueues() })

describe('pane-wide file drop', () => {
  it('a drop on the stream uploads to the input', async () => {
    render(<Pane />)
    fireEvent.drop(screen.getByTestId('stream'), files([png()]))
    await flush()
    expect(mocks.upload).toHaveBeenCalledTimes(1)
    expect((mocks.upload.mock.calls[0][1] as File).name).toBe('shot.png')
    expect(screen.getByTestId('attachment-chip')).toBeInTheDocument()
  })

  it('the whole pane lights up while a file is over it, and goes with the drop', () => {
    render(<Pane />)
    expect(screen.queryByTestId('pane-drop-overlay')).toBeNull()
    fireEvent.dragEnter(screen.getByTestId('stream'), files([]))
    expect(screen.getByTestId('pane-drop-overlay')).toBeInTheDocument()
    fireEvent.drop(screen.getByTestId('stream'), files([]))
    expect(screen.queryByTestId('pane-drop-overlay')).toBeNull()
  })

  it('a drop on the input itself is attached once, not twice', async () => {
    render(<Pane />)
    fireEvent.drop(screen.getByTestId('session-input-drop'), files([png()]))
    await flush()
    expect(mocks.upload).toHaveBeenCalledTimes(1)
  })

  it('moving over a child (leave then enter) keeps it lit; leaving the pane puts it out', () => {
    render(<Pane />)
    const stream = screen.getByTestId('stream')
    fireEvent.dragEnter(screen.getByTestId('pane'), files([]))
    fireEvent.dragEnter(stream, files([]))
    fireEvent.dragLeave(screen.getByTestId('pane'), files([]))
    expect(screen.getByTestId('pane-drop-overlay')).toBeInTheDocument()
    fireEvent.dragLeave(stream, files([]))
    expect(screen.queryByTestId('pane-drop-overlay')).toBeNull()
  })

  it('Esc puts the highlight out', () => {
    render(<Pane />)
    fireEvent.dragEnter(screen.getByTestId('stream'), files([]))
    expect(screen.getByTestId('pane-drop-overlay')).toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(screen.queryByTestId('pane-drop-overlay')).toBeNull()
  })

  it('a drag that carries no files does nothing', () => {
    render(<Pane />)
    fireEvent.dragEnter(screen.getByTestId('stream'), { dataTransfer: { files: [], types: ['text/plain'] } })
    expect(screen.queryByTestId('pane-drop-overlay')).toBeNull()
  })

  it('a pane without an input takes nothing and shows nothing', () => {
    render(<Pane withInput={false} />)
    fireEvent.dragEnter(screen.getByTestId('stream'), files([]))
    expect(screen.queryByTestId('pane-drop-overlay')).toBeNull()
    fireEvent.drop(screen.getByTestId('stream'), files([png()]))
    expect(mocks.upload).not.toHaveBeenCalled()
  })
})
