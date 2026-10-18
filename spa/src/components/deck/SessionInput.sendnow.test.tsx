// spa/src/components/deck/SessionInput.sendnow.test.tsx — the Deck input's send-now keys and Up on the first line (message-stages D8, plan L5/L6, MS2-4): Ctrl+Enter calls
// send-now and not submit; Ctrl+X then Ctrl+S inside 1.5 s does; Up on the first line takes Claude Code's queue back before the history. Gated by the live
// capability `send_now: "keys"`: without it Ctrl+Enter is today's Enter.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { SessionInput } from './SessionInput'
import { clearAllDrafts, draftKey, readDraft } from '../../lib/conversations/draft-memory'
import { UNDO_MS, clearAllSendQueues, sendQueueFor } from '../../lib/conversations/send-queue'
import { bindAdapter, clearAllAdapters } from '../../lib/conversations/deck-adapter'
import { terminalAdapter } from '../../lib/conversations/terminal-adapter'
import { clearAllTakeBack } from '../../lib/conversations/take-back'
import { CHORD_MS } from '../../lib/conversations/send-now-keys'
import type { Capabilities } from '../../lib/conversations/types'

const fetchMock = vi.hoisted(() => vi.fn())
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: fetchMock }))

const H = 'h', SID = '11111111-2222-4333-8444-555555555555', PANE = 'pane-1'
const KEY = draftKey(PANE, H, SID)
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const calls = (verb: string) => fetchMock.mock.calls.filter((c) => String(c[1]).endsWith(`/${verb}`))
let sendNowAnswer: () => Promise<Response>

function mount(capabilities: Capabilities = { send: 'keys', send_now: 'keys' }) {
  bindAdapter(KEY, terminalAdapter(H, SID, undefined, () => ({ code: 'dev001', instance: 'g1' })))
  return render(<SessionInput paneKey={PANE} hostId={H} sessionId={SID} capabilities={capabilities} onSwitchToTerminal={() => {}} />)
}
const ctrl = (key: string) => fireEvent.keyDown(box(), { key, ctrlKey: true })

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  sendNowAnswer = () => Promise.resolve(new Response(JSON.stringify({ outcome: 'sent_now' })))
  fetchMock.mockReset().mockImplementation((_h: string, path: string) => {
    if (path.endsWith('/send-now')) return sendNowAnswer()
    if (path.endsWith('/recall')) return Promise.resolve(new Response(JSON.stringify({ text: 'queued by claude' })))
    return Promise.resolve(new Response(JSON.stringify({ outcome: 'handed', mode: 'fill' })))
  })
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllSendQueues(); clearAllAdapters(); clearAllTakeBack(); vi.useRealTimers() })

describe('send now by key', () => {
  it('Ctrl+Enter calls send-now and not submit; the draft stays; the notice says the reply may be interrupted', async () => {
    mount()
    fireEvent.change(box(), { target: { value: 'draft stays' } })
    await act(async () => { ctrl('Enter'); await vi.advanceTimersByTimeAsync(UNDO_MS) })
    expect(calls('send-now')).toHaveLength(1)
    expect(calls('submit')).toHaveLength(0)
    expect(box().value).toBe('draft stays')
    expect(screen.getByText(/interrupt/i)).toBeTruthy()
  })

  it('Ctrl+X then Ctrl+S within 1.5 s sends now; after 1.5 s it does not; Ctrl+X alone keeps its normal cut', async () => {
    mount()
    const x = fireEvent.keyDown(box(), { key: 'x', ctrlKey: true })
    expect(x).toBe(true) // not default-prevented: the cut stays
    await act(async () => { ctrl('s'); await vi.advanceTimersByTimeAsync(0) })
    expect(calls('send-now')).toHaveLength(1)
    ctrl('x')
    await act(async () => { await vi.advanceTimersByTimeAsync(CHORD_MS + 1) })
    await act(async () => { ctrl('s'); await vi.advanceTimersByTimeAsync(0) })
    expect(calls('send-now')).toHaveLength(1)
  })

  it('a double press is one request while the first is out', async () => {
    let release!: (r: Response) => void
    sendNowAnswer = () => new Promise<Response>((resolve) => { release = resolve })
    mount()
    await act(async () => { ctrl('Enter'); ctrl('Enter'); await vi.advanceTimersByTimeAsync(0) })
    expect(calls('send-now')).toHaveLength(1)
    await act(async () => { release(new Response(JSON.stringify({ outcome: 'sent_now' }))); await vi.advanceTimersByTimeAsync(0) })
  })

  it('a held key (repeat) is one send now, even when the answer comes back before the next repeat', async () => {
    mount()
    await act(async () => { ctrl('Enter'); await vi.advanceTimersByTimeAsync(0) })
    expect(calls('send-now')).toHaveLength(1)
    await act(async () => { fireEvent.keyDown(box(), { key: 'Enter', ctrlKey: true, repeat: true }); await vi.advanceTimersByTimeAsync(0) })
    expect(calls('send-now')).toHaveLength(1)
    expect(calls('submit')).toHaveLength(0) // and the repeat does not fall into the plain Enter either
  })

  it('Ctrl+X, then the box loses focus: a Ctrl+S that comes back inside 1.5 s is not a send now', async () => {
    mount()
    ctrl('x')
    fireEvent.blur(box())
    fireEvent.focus(box())
    await act(async () => { ctrl('s'); await vi.advanceTimersByTimeAsync(0) })
    expect(calls('send-now')).toHaveLength(0)
  })

  it.each([
    ['input_not_empty', 'The desktop input box has unsent text'],
    ['waiting', 'Waiting for you in the terminal'],
    ['nothing_queued', 'Nothing is queued'],
  ])('a refusal (%s) shows its text', async (reason, text) => {
    sendNowAnswer = () => Promise.resolve(new Response(JSON.stringify({ outcome: 'refused', reason }), { status: 409 }))
    mount()
    await act(async () => { ctrl('Enter'); await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText(new RegExp(text))).toBeTruthy()
  })

  it('without the capability Ctrl+Enter is today\'s Enter: the draft is submitted', async () => {
    mount({ send: 'keys' })
    fireEvent.change(box(), { target: { value: 'plain' } })
    await act(async () => { ctrl('Enter'); await vi.advanceTimersByTimeAsync(UNDO_MS) })
    expect(calls('send-now')).toHaveLength(0)
    expect(calls('submit')).toHaveLength(1)
  })
})

describe('Up on the first line', () => {
  it('takes Claude Code\'s queue back in front of the draft, before the history', async () => {
    mount()
    const q = sendQueueFor(KEY, () => ({ submit: async () => ({ kind: 'handed', mode: 'fill' }), interrupt: async () => ({ kind: 'accepted' }) }))
    q.enqueue('queued by claude')
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    fireEvent.change(box(), { target: { value: 'my draft' } })
    box().setSelectionRange(0, 0)
    await act(async () => { fireEvent.keyDown(box(), { key: 'ArrowUp' }); await vi.advanceTimersByTimeAsync(0) })
    expect(calls('recall')).toHaveLength(1)
    expect(readDraft(KEY)).toBe('queued by claude\nmy draft')
  })

  it('with nothing to take back, Up is the caret\'s / the history\'s as before', async () => {
    mount()
    fireEvent.change(box(), { target: { value: 'my draft' } })
    box().setSelectionRange(0, 0)
    await act(async () => { fireEvent.keyDown(box(), { key: 'ArrowUp' }); await vi.advanceTimersByTimeAsync(0) })
    expect(calls('recall')).toHaveLength(0)
    expect(box().value).toBe('my draft')
  })
})
