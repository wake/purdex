// #2666: Up / Down in the deck's input walk the conversation's sent history when nothing can be taken back. Real TabContent for the tab switch.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { SessionInput } from './SessionInput'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { clearAllDrafts, draftKey, readDraft } from '../../lib/conversations/draft-memory'
import { clearAllAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllCleared } from '../../lib/conversations/draft-clear'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { clearAllSendQueues, sendQueueFor } from '../../lib/conversations/send-queue'
import { clearAllInputHistory, setHistorySource } from '../../lib/conversations/input-history'
import type { SendOutcome, SendPort } from '../../lib/conversations/send'
import type { UserItem } from '../../lib/conversations/types'

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
const up = () => fireEvent.keyDown(box(), { key: 'ArrowUp' })
const down = () => fireEvent.keyDown(box(), { key: 'ArrowDown' })
const caret = (at: number) => box().setSelectionRange(at, at)

let n = 0
const user = (text: string, source: UserItem['source'] = 'user'): UserItem => ({ id: `u${++n}`, at: n, index: n, type: 'user', text, source })
const port: SendPort = { submit: () => new Promise<SendOutcome>(() => {}), interrupt: () => Promise.resolve<SendOutcome>({ kind: 'accepted' }) }

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: Renderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  sendQueueFor(KEY, () => port)
  setHistorySource(KEY, [user('one'), user('from a peer', 'peer'), user('two\nlines')])
})
afterEach(() => { cleanup(); clearAllCleared(); clearAllDrafts(); clearAllAttachments(); clearAllUploads(); clearAllSendQueues(); clearAllInputHistory() })

describe('Up / Down walk the sent history', () => {
  it('Up in an empty box shows the newest of the person\'s own messages, Up again the older one, Down comes back to empty', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    up()
    expect(box().value).toBe('two\nlines')
    expect(box().selectionStart).toBe('two\nlines'.length) // caret at the end
    caret(0) // on the first line: Up steps
    up()
    expect(box().value).toBe('one') // the peer's message is skipped
    expect(readDraft(KEY)).toBe('one')
    down()
    expect(box().value).toBe('two\nlines')
    caret('two\nlines'.length) // on the last line: Down steps
    down()
    expect(box().value).toBe('')
    expect(readDraft(KEY)).toBeUndefined()
  })

  it('inside a multi-line item Up / Down are the caret\'s until the first / last line', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    up() // "two\nlines", caret at the end (last line)
    const stop = fireEvent.keyDown(box(), { key: 'ArrowUp' }) // caret on the 2nd line: not a history step
    expect(stop).toBe(true) // not default-prevented
    expect(box().value).toBe('two\nlines')
    caret(1)
    expect(fireEvent.keyDown(box(), { key: 'ArrowDown' })).toBe(true)
    expect(box().value).toBe('two\nlines')
  })

  it('an edit ends the walk: Up then is the caret\'s', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    up()
    type('two\nlines!')
    caret(0)
    expect(fireEvent.keyDown(box(), { key: 'ArrowUp' })).toBe(true)
    expect(box().value).toBe('two\nlines!')
    expect(fireEvent.keyDown(box(), { key: 'ArrowDown' })).toBe(true)
  })

  it('Up in a box with a typed draft is the caret\'s', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('typing')
    expect(fireEvent.keyDown(box(), { key: 'ArrowUp' })).toBe(true)
    expect(box().value).toBe('typing')
  })

  it('something to take back wins over history', () => {
    const q = sendQueueFor(KEY, () => port)
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    type('held message')
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(q.heldCount()).toBe(1)
    up()
    expect(box().value).toBe('held message')
  })

  it('the walk survives a tab switch (the pane really unmounts)', () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    up()
    caret(0)
    up()
    expect(box().value).toBe('one')
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('session-input')).toBeNull()
    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(box().value).toBe('one')
    caret(3)
    down()
    expect(box().value).toBe('two\nlines') // continues from where it was
  })
})
