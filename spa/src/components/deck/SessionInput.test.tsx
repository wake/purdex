import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { SessionInput } from './SessionInput'
import { clearAllDrafts, draftKey, readDraft } from '../../lib/conversations/draft-memory'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'
import type { Capabilities, ConversationItem } from '../../lib/conversations/types'

const fetchMock = vi.hoisted(() => vi.fn())
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: fetchMock }))

const SID = '11111111-2222-4333-8444-555555555555'
const PROMPT: Capabilities = { send: 'prompt', interrupt: 'prompt' }
const answer = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status }))
const sends = () => fetchMock.mock.calls.filter((c) => String(c[1]).endsWith('/submit')).map((c) => JSON.parse(c[2].body) as { text: string; client_msg_id: string })

interface Over { capabilities?: Capabilities; items?: ConversationItem[]; idle?: boolean; onSwitchToTerminal?: () => void }
const ui = (o: Over = {}) => (
  <SessionInput paneKey="p1" hostId="h" sessionId={SID} capabilities={o.capabilities ?? PROMPT} items={o.items ?? []} idle={o.idle ?? true}
    onSwitchToTerminal={o.onSwitchToTerminal ?? (() => {})} />
)
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const type = (v: string) => fireEvent.change(box(), { target: { value: v } })
const enter = () => fireEvent.keyDown(box(), { key: 'Enter' })
const tick = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms))
const userItem = (id: string, text: string, extra: object = {}): ConversationItem => ({ id, type: 'user', text, at: Date.now(), index: 0, source: 'user', ...extra }) as ConversationItem

beforeEach(() => {
  vi.useFakeTimers()
  fetchMock.mockReset()
  fetchMock.mockImplementation(() => answer(200, { status: 'accepted' }))
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllSendQueues(); vi.useRealTimers() })

describe('SessionInput sending', () => {
  it('Enter queues the message (local echo, box cleared), and it goes out after the 3 s undo window', async () => {
    render(ui())
    type('hello')
    enter()
    expect(box().value).toBe('')
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'undo')
    expect(sends()).toHaveLength(0)
    await tick(3000)
    expect(sends()).toMatchObject([{ text: 'hello' }])
    expect(sends()[0].client_msg_id).toMatch(/\S+/)
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent')
  })

  it('Shift+Enter is a line break, not a send', async () => {
    render(ui())
    type('a')
    fireEvent.keyDown(box(), { key: 'Enter', shiftKey: true })
    await tick(5000)
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(sends()).toHaveLength(0)
  })

  it('a draft of only whitespace sends nothing and says nothing', async () => {
    render(ui())
    type('  \n \t ')
    enter()
    await tick(5000)
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(screen.queryByTestId('session-input-hint')).toBeNull()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it.each(['/model', '!ls -la'])('%s is blocked with the terminal message and the draft stays', async (text) => {
    render(ui())
    type(text)
    enter()
    await tick(5000)
    expect(screen.getByTestId('session-input-hint')).toHaveTextContent('terminal')
    expect(box().value).toBe(text)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('two quick sends: two ids, one at a time, in order', async () => {
    let first: ((r: Response) => void) | undefined
    fetchMock.mockImplementationOnce(() => new Promise<Response>((r) => { first = r }))
    render(ui())
    type('one'); enter()
    type('two'); enter()
    await tick(3000)
    expect(sends().map((s) => s.text)).toEqual(['one'])
    await act(async () => { first?.(new Response(JSON.stringify({ status: 'accepted' }))) })
    await tick(0)
    expect(sends().map((s) => s.text)).toEqual(['one', 'two'])
    expect(new Set(sends().map((s) => s.client_msg_id)).size).toBe(2)
  })

  it('Undo takes the message back into the box', async () => {
    render(ui())
    type('take me back'); enter()
    await tick(1000)
    fireEvent.click(screen.getByText('Undo'))
    expect(box().value).toBe('take me back')
    await tick(5000)
    expect(sends()).toHaveLength(0)
  })

  it('a destructive draft needs a second press inside 5 s', async () => {
    render(ui())
    type('please run rm -rf build'); enter()
    expect(screen.getByTestId('session-input-hint')).toHaveTextContent('Really send')
    expect(screen.queryByTestId('queued-message')).toBeNull()
    enter()
    expect(screen.getByTestId('queued-message')).toBeInTheDocument()
  })

  it('a second press after 5 s asks again', async () => {
    render(ui())
    type('git push --force'); enter()
    await tick(5500)
    enter()
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(screen.getByTestId('session-input-hint')).toHaveTextContent('Really send')
  })
})

describe('SessionInput outcomes', () => {
  it('busy: kept in the App, resent with the same client_msg_id when the agent turns idle', async () => {
    fetchMock.mockImplementationOnce(() => answer(200, { status: 'busy' }))
    const { rerender } = render(ui({ idle: false }))
    type('later'); enter()
    await tick(3000)
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'waiting')
    rerender(ui({ idle: true }))
    await tick(0)
    expect(sends()).toHaveLength(2)
    expect(sends()[1]).toEqual(sends()[0])
  })

  it('unknown: shown as possibly sent, never resent by itself, settled by the transcript echo', async () => {
    fetchMock.mockImplementationOnce(() => answer(200, { status: 'unknown', reason: 'no_result' }))
    const { rerender } = render(ui({ idle: false }))
    type('did it go'); enter()
    await tick(3000)
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'maybe')
    expect(screen.getByTestId('queued-message')).toHaveTextContent('May have been sent')
    rerender(ui({ idle: true })); rerender(ui({ idle: false })); rerender(ui({ idle: true }))
    await tick(60_000)
    expect(sends()).toHaveLength(1)
    rerender(ui({ items: [userItem('u1', 'did it go', { client_msg_id: sends()[0].client_msg_id })] }))
    expect(screen.queryByTestId('queued-message')).toBeNull()
  })

  it('dropped shows the reason and the draft is not resurrected', async () => {
    fetchMock.mockImplementationOnce(() => answer(200, { status: 'dropped', reason: 'session_changed' }))
    render(ui())
    type('x'); enter()
    await tick(3000)
    expect(screen.getByTestId('queued-message')).toHaveTextContent('session_changed')
  })

  it('a lost connection mid-submit: resending needs a second confirmation, then goes out as a new message', async () => {
    fetchMock.mockImplementationOnce(() => Promise.reject(new TypeError('down')))
    render(ui())
    type('restart'); enter()
    await tick(3000)
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'maybe')
    expect(screen.queryByText('Check again')).toBeNull()
    fireEvent.click(screen.getByText('Send again (may duplicate)'))
    await tick(0)
    expect(sends()).toHaveLength(1) // asked, not sent
    expect(screen.getByTestId('queued-message')).toHaveTextContent('may arrive twice')
    fireEvent.click(screen.getByText('Cancel'))
    await tick(5000)
    expect(sends()).toHaveLength(1)
    fireEvent.click(screen.getByText('Send again (may duplicate)'))
    fireEvent.click(screen.getByText('Send again'))
    await tick(0)
    expect(sends()).toHaveLength(2)
    expect(sends()[1].client_msg_id).not.toBe(sends()[0].client_msg_id)
    expect(screen.getAllByTestId('queued-message')).toHaveLength(1) // the superseded one is not drawn
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent')
  })

  it('a dropped message: Try again is a new message that really calls submit', async () => {
    fetchMock.mockImplementationOnce(() => answer(200, { status: 'dropped', reason: 'session_changed' }))
    render(ui())
    type('x'); enter()
    await tick(3000)
    fireEvent.click(screen.getByText('Try again'))
    await tick(0)
    expect(sends()).toHaveLength(2)
    expect(sends()[1].client_msg_id).not.toBe(sends()[0].client_msg_id)
  })

  it('409 no_mod disables the input', async () => {
    fetchMock.mockImplementationOnce(() => answer(409, { error: 'no_mod' }))
    render(ui())
    type('x'); enter()
    await tick(3000)
    expect(screen.getByTestId('session-input-disabled')).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).toBeNull()
  })

  it('Interrupt posts to /interrupt and says so', async () => {
    render(ui())
    await act(async () => { fireEvent.click(screen.getByLabelText('Interrupt')) })
    expect(String(fetchMock.mock.calls[0][1])).toBe(`/api/conversations/claude/${SID}/interrupt`)
    expect(screen.getByTestId('session-input-hint')).toHaveTextContent('Sent')
  })
})

describe('SessionInput capability', () => {
  it.each([{ send: 'not_wired' }, {}, { send: 'keystrokes' }])('send=%j disables the input with the no-mod message and a terminal button', (caps) => {
    const onSwitch = vi.fn()
    render(ui({ capabilities: caps, onSwitchToTerminal: onSwitch }))
    expect(screen.queryByRole('textbox')).toBeNull()
    expect(screen.getByTestId('session-input-disabled')).toHaveTextContent('no Purdex mod')
    fireEvent.click(screen.getByText('Switch to terminal'))
    expect(onSwitch).toHaveBeenCalledTimes(1)
  })

  it('typing is remembered in the draft memory, per host and session', () => {
    render(ui())
    type('half')
    expect(readDraft(draftKey('p1', 'h', SID))).toBe('half')
  })

  it('the same pane rebound to another session does not show or send the old draft; switching back restores it', async () => {
    const other = '99999999-2222-4333-8444-555555555555'
    const bound = (sid: string, host = 'h') => <SessionInput paneKey="p1" hostId={host} sessionId={sid} capabilities={PROMPT} items={[]} idle onSwitchToTerminal={() => {}} />
    const { rerender } = render(bound(SID))
    type('draft for A')
    rerender(bound(other))
    expect(box().value).toBe('')
    enter()
    await tick(5000)
    expect(fetchMock).not.toHaveBeenCalled()
    type('draft for B')
    rerender(bound(SID, 'h2'))
    expect(box().value).toBe('')
    rerender(bound(SID))
    expect(box().value).toBe('draft for A')
    rerender(bound(other))
    expect(box().value).toBe('draft for B')
  })
})
