// c9d9c7-59 pt 1: what the App still holds shows grey (no card) with a small Undo; Up in an empty box calls every held message back (joined by
// newlines, chips and `[Image #N]` with them); anywhere else Up is the caret's. Real TabContent for the tab switch.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { SessionInput } from './SessionInput'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { clearAllDrafts, draftKey, readDraft, writeDraft } from '../../lib/conversations/draft-memory'
import { addAttachment, clearAllAttachments, readAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllCleared } from '../../lib/conversations/draft-clear'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { UNDO_MS, clearAllSendQueues, sendQueueFor } from '../../lib/conversations/send-queue'
import type { SendOutcome, SendPort } from '../../lib/conversations/send'

const H = 'h', SID = '11111111-2222-4333-8444-555555555555'
function Renderer({ pane }: PaneRendererProps) {
  return <SessionInput paneKey={pane.id} hostId={H} sessionId={SID} capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />
}
const Other = () => <div data-testid="other-tab" />
const sessionTab: Tab = { ...createTab({ kind: 'execution', executionId: 'exc_1', host: H }), id: 't-session' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [sessionTab, dashTab]
const paneId = (sessionTab.layout as { pane: { id: string } }).pane.id
const KEY = draftKey(paneId, H, SID)
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const type = (v: string) => fireEvent.change(box(), { target: { value: v } })
const enter = () => fireEvent.keyDown(box(), { key: 'Enter' })
const up = (init: Record<string, unknown> = {}) => fireEvent.keyDown(box(), { key: 'ArrowUp', ...init })
const held = () => screen.queryAllByTestId('queued-message')

const calls: Array<{ text: string; id: string; resolve: (o: SendOutcome) => void }> = []
const port: SendPort = {
  submit: (text, id) => new Promise<SendOutcome>((resolve) => { calls.push({ text, id, resolve }) }),
  interrupt: () => Promise.resolve<SendOutcome>({ kind: 'accepted' }),
}
const revoke = vi.fn()
const chip = (over: Record<string, unknown> = {}) => ({ id: 'a1', name: 'shot.png', path: '/tmp/shot.png', text: '[Image #7]', image: true, previewUrl: 'blob:p1', size: 10, seq: 7, ...over })

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  calls.length = 0
  revoke.mockReset()
  Object.assign(URL, { revokeObjectURL: revoke })
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: Renderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  sendQueueFor(KEY, () => port) // the pane's queue, with a port that answers nothing by itself
})
afterEach(() => { cleanup(); clearAllCleared(); clearAllDrafts(); clearAllAttachments(); clearAllUploads(); clearAllSendQueues(); vi.useRealTimers() })

describe('what the App holds is shown grey, without a card', () => {
  it('a held message: the queued caption, the text, a small Undo; no frame', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('hold me'); enter()
    const [m] = held()
    expect(m).toHaveClass('text-text-system')
    expect(m.className).not.toMatch(/border|bg-/)
    expect(m).toHaveTextContent('You · queued')
    expect(m).toHaveTextContent('hold me')
    expect(m.querySelector('button')).toHaveTextContent('Undo')
  })

  it('once handed out it is a card that says it is on its way, never "queued"', async () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('go'); enter()
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    const [m] = held()
    expect(m).toHaveAttribute('data-state', 'sending')
    expect(m).toHaveTextContent('You · sending…')
    expect(m.textContent).not.toMatch(/queued/i)
  })
})

describe('Up in an empty box calls the held messages back', () => {
  it('one message: the box has it again, the queue does not', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('first'); enter()
    expect(box().value).toBe('')
    expect(up()).toBe(false) // the key was taken (preventDefault)
    expect(box().value).toBe('first')
    expect(held()).toHaveLength(0)
    expect(readDraft(KEY)).toBe('first')
  })

  it('several: in sending order, joined by newlines', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('one'); enter()
    type('two'); enter()
    type('three'); enter()
    expect(held()).toHaveLength(3)
    up()
    expect(box().value).toBe('one\ntwo\nthree')
    expect(held()).toHaveLength(0)
  })

  it('with an attachment: the placeholder, the chip and its picture come back, at the same number', () => {
    writeDraft(KEY, 'look [Image #7]')
    addAttachment(KEY, chip())
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    enter()
    expect(readAttachments(KEY)).toEqual([])
    expect(revoke).not.toHaveBeenCalled()
    up()
    expect(box().value).toBe('look [Image #7]')
    expect(readAttachments(KEY).map((a) => [a.id, a.seq, a.previewUrl])).toEqual([['a1', 7, 'blob:p1']])
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(1)
    expect(revoke).not.toHaveBeenCalled()
  })

  it('a message the mod refused as busy (waiting for the agent) comes back with its chip and picture too', async () => {
    writeDraft(KEY, 'look [Image #7]')
    addAttachment(KEY, chip())
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    enter()
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    await act(async () => { calls[0].resolve({ kind: 'busy' }); await vi.advanceTimersByTimeAsync(0) })
    expect(held()[0]).toHaveAttribute('data-state', 'waiting')
    expect(revoke).not.toHaveBeenCalled()
    up()
    expect(box().value).toBe('look [Image #7]')
    expect(readAttachments(KEY).map((a) => [a.seq, a.previewUrl])).toEqual([[7, 'blob:p1']])
    expect(revoke).not.toHaveBeenCalled()
  })

  it('never reaches the mod: nothing is sent after the undo window', async () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('mine'); enter()
    up()
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS * 3) })
    expect(calls).toEqual([])
  })

  it('a message the driver already handed out stays; only the held ones come back', async () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('out'); enter()
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    type('still mine'); enter()
    up()
    expect(box().value).toBe('still mine')
    expect(calls.map((c) => c.text)).toEqual(['out'])
    expect(held().map((m) => m.getAttribute('data-state'))).toEqual(['sending'])
  })

  it('what is recalled survives a tab switch and back', () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('stay'); enter(); up()
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(box().value).toBe('stay')
  })

  it('the Undo button still works (one message back, before what is typed)', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('a'); enter()
    type('b'); enter()
    type('typed')
    fireEvent.click(held()[0].querySelector('button')!)
    expect(box().value).toBe('a\ntyped')
    expect(held()).toHaveLength(1)
  })
})

describe('where Up stays the caret\'s', () => {
  it('a box with text in it', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('held'); enter()
    type('typing')
    expect(up()).toBe(true)
    expect(box().value).toBe('typing')
    expect(held()).toHaveLength(1)
  })

  it('an empty box with an attachment chip showing', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('held'); enter()
    writeDraft(KEY, '[Image #7]')
    addAttachment(KEY, chip())
    cleanup()
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(up()).toBe(true)
    expect(held()).toHaveLength(1)
  })

  it('while an IME is composing', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('held'); enter()
    expect(up({ isComposing: true })).toBe(true)
    expect(up({ keyCode: 229 })).toBe(true)
    expect(held()).toHaveLength(1)
  })

  it.each([['Shift', { shiftKey: true }], ['Alt', { altKey: true }], ['Ctrl', { ctrlKey: true }], ['Meta', { metaKey: true }]])('with %s held', (_n, init) => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('held'); enter()
    expect(up(init)).toBe(true)
    expect(held()).toHaveLength(1)
  })

  it('right after Esc cleared a draft: its undo window is not overwritten (Up leaves the held message and the ⌘Z hint alone)', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('held'); enter()
    type('a long draft'); fireEvent.keyDown(box(), { key: 'Escape' })
    expect(screen.getByTestId('draft-cleared')).toBeInTheDocument()
    expect(up()).toBe(true)
    expect(box().value).toBe('')
    expect(held()).toHaveLength(1)
    expect(screen.getByTestId('draft-cleared')).toBeInTheDocument()
  })

  it('nothing held: Up is untouched', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(up()).toBe(true)
  })
})
