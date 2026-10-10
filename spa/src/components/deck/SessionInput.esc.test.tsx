// c9d9c7-53 (2): Esc clears the draft and its attachments; for five seconds ⌘Z (or a click on the hint) puts all of it back, previews and
// image numbers included. An empty draft: Esc does nothing at all. An IME composing: Esc is the IME's. Real TabContent for the tab switch:
// a cleared draft does not come back by itself when the reader leaves and returns.
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
import { addAttachment, clearAllAttachments, readAttachments, trackUpload } from '../../lib/conversations/attachment-memory'
import { UNDO_CLEAR_MS, clearAllCleared, undoClear } from '../../lib/conversations/draft-clear'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'

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
const hint = () => screen.queryByTestId('draft-cleared')
const esc = (init: Record<string, unknown> = {}) => fireEvent.keyDown(box(), { key: 'Escape', ...init })
const undoKey = () => fireEvent.keyDown(box(), { key: 'z', metaKey: true })

const revoke = vi.fn()
const chip = (over: Record<string, unknown> = {}) => ({ id: 'a1', name: 'shot.png', path: '/tmp/shot.png', text: '[Image #7]', image: true, previewUrl: 'blob:p1', size: 10, seq: 7, ...over })

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  revoke.mockReset()
  Object.assign(URL, { revokeObjectURL: revoke })
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: Renderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
})
afterEach(() => { cleanup(); clearAllCleared(); clearAllDrafts(); clearAllAttachments(); clearAllUploads(); clearAllSendQueues(); vi.useRealTimers() })

describe('Esc clears the draft', () => {
  it('the text goes (in the box and in the memory) and a hint says how to undo', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'half a thought' } })
    esc()
    expect(box().value).toBe('')
    expect(readDraft(KEY)).toBeUndefined()
    expect(hint()).toHaveTextContent('Draft cleared · ⌘Z to undo')
  })

  it('⌘Z brings the whole draft back, and so does a click on the hint', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'line one\nline two' } })
    esc()
    undoKey()
    expect(box().value).toBe('line one\nline two')
    expect(readDraft(KEY)).toBe('line one\nline two')
    expect(hint()).toBeNull()
    esc()
    fireEvent.click(hint()!)
    expect(box().value).toBe('line one\nline two')
    expect(hint()).toBeNull()
  })

  it('Ctrl+Z works too; Shift+⌘Z does not', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'abc' } })
    esc()
    fireEvent.keyDown(box(), { key: 'z', metaKey: true, shiftKey: true })
    expect(box().value).toBe('')
    fireEvent.keyDown(box(), { key: 'z', ctrlKey: true })
    expect(box().value).toBe('abc')
  })

  it('after five seconds the hint goes and ⌘Z is just the browser\'s again', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'abc' } })
    esc()
    act(() => { vi.advanceTimersByTime(UNDO_CLEAR_MS - 1) })
    expect(hint()).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(2) })
    expect(hint()).toBeNull()
    undoKey()
    expect(box().value).toBe('')
  })

  it('typing something new ends the window (the old draft is not brought back over it)', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'old' } })
    esc()
    fireEvent.change(box(), { target: { value: 'new' } })
    expect(hint()).toBeNull()
    undoKey()
    expect(box().value).toBe('new')
  })

  it('something that landed in the draft since (an upload finishing) ends the window: the undo does not overwrite it', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'old text' } })
    esc()
    act(() => { writeDraft(KEY, 'a file landed'); addAttachment(KEY, chip()) }) // as attachment-upload does when an upload finishes
    expect(undoClear(KEY)).toBeNull()
    expect(readDraft(KEY)).toBe('a file landed')
    expect(readAttachments(KEY)).toHaveLength(1)
  })

  it('a key code 229 (an IME in Safari) is not an Esc', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'にほん' } })
    esc({ keyCode: 229 })
    expect(box().value).toBe('にほん')
  })

  it('a chip hidden from the screen does not by itself take an Esc (nothing visible to clear)', () => {
    addAttachment(KEY, chip({ hiddenSince: 5 }))
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(esc()).toBe(true)
    expect(hint()).toBeNull()
  })

  it('an empty draft: Esc does nothing, not even to the event (it still reaches the pane\'s own Esc handlers)', () => {
    const parent = vi.fn()
    render(<div onKeyDown={parent}><TabContent activeTab={sessionTab} allTabs={all} /></div>)
    const notPrevented = esc()
    expect(notPrevented).toBe(true)
    expect(parent).toHaveBeenCalledTimes(1)
    expect(hint()).toBeNull()
  })

  it('a clearing Esc is the input\'s own: it does not reach the pane\'s Esc (a side panel stays open)', () => {
    const parent = vi.fn()
    render(<div onKeyDown={parent}><TabContent activeTab={sessionTab} allTabs={all} /></div>)
    fireEvent.change(box(), { target: { value: 'abc' } })
    expect(esc()).toBe(false)
    expect(parent).not.toHaveBeenCalled()
  })

  it('while an IME is composing, Esc cancels the composition and leaves the draft alone', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'にほん' } })
    esc({ isComposing: true })
    expect(box().value).toBe('にほん')
    expect(hint()).toBeNull()
  })
})

describe('the attachments go and come back with the draft', () => {
  it('chips (hidden ones too) leave the memory; their previews stay alive for the undo window and come back with the same image number', () => {
    writeDraft(KEY, 'look [Image #7] here')
    addAttachment(KEY, chip())
    addAttachment(KEY, chip({ id: 'a2', text: '[Image #8]', seq: 8, previewUrl: 'blob:p2', hiddenSince: 123 })) // its marker already left the draft
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(1)
    esc()
    expect(readAttachments(KEY)).toHaveLength(0)
    expect(screen.queryByTestId('attachment-chip')).toBeNull()
    expect(revoke).not.toHaveBeenCalled() // the URLs are still the undo's
    undoKey()
    expect(box().value).toBe('look [Image #7] here')
    expect(readAttachments(KEY).map((a) => [a.id, a.seq, a.previewUrl])).toEqual([['a1', 7, 'blob:p1'], ['a2', 8, 'blob:p2']])
    expect(readAttachments(KEY).every((a) => a.hiddenSince === undefined)).toBe(true)
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(1) // [Image #7] is in the draft again, so its chip shows
    expect(revoke).not.toHaveBeenCalled()
  })

  it('without an undo the previews are revoked when the window closes (not before)', () => {
    writeDraft(KEY, '[Image #7]')
    addAttachment(KEY, chip())
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    esc()
    act(() => { vi.advanceTimersByTime(UNDO_CLEAR_MS - 10) })
    expect(revoke).not.toHaveBeenCalled()
    act(() => { vi.advanceTimersByTime(20) })
    expect(revoke).toHaveBeenCalledWith('blob:p1')
    expect(readAttachments(KEY)).toHaveLength(0)
  })

  it('an upload still on its way is cancelled by the clear', () => {
    writeDraft(KEY, 'text')
    const ctl = new AbortController()
    trackUpload(KEY, ctl)
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    esc()
    expect(ctl.signal.aborted).toBe(true)
  })
})

describe('across a tab switch', () => {
  it('a cleared draft does not come back when the reader leaves and returns', () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'gone' } })
    esc()
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('session-input')).toBeNull()
    act(() => { vi.advanceTimersByTime(UNDO_CLEAR_MS + 100) })
    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(box().value).toBe('')
    expect(readDraft(KEY)).toBeUndefined()
    expect(hint()).toBeNull()
  })

  it('and a window still open when the reader comes back (within five seconds) still offers the undo', () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'still mine' } })
    esc()
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(box().value).toBe('')
    expect(hint()).toBeInTheDocument()
    undoKey()
    expect(box().value).toBe('still mine')
  })
})
