// c9d9c7-53 (1): coming back to a session tab (or switching its view to the deck) puts the caret in the input, at the end of the draft.
// Not when the reader is typing somewhere else, a modal dialog is up, or a choice question waits in the dock. Real TabContent: the
// alive pool keeps no execution-kind tab by default, so the pane really unmounts and activates again.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { useLayoutEffect, useRef, useSyncExternalStore } from 'react'
import { TabContent } from '../TabContent'
import { DeckPane } from './DeckPane'
import { SessionInput } from './SessionInput'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { emptyDoc } from '../../lib/conversations/model'
import { clearAllDrafts, draftKey, writeDraft } from '../../lib/conversations/draft-memory'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'
import type { ConversationEntry } from '../../stores/useConversationStore'

const H = 'h', SID = '11111111-2222-4333-8444-555555555555'
const sessionTab: Tab = { ...createTab({ kind: 'execution', executionId: 'exc_1', host: H }), id: 't-session' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [sessionTab, dashTab]
const paneId = (sessionTab.layout as { pane: { id: string } }).pane.id

// the conversation the pane shows is a tiny external store, so a test can move it from resolving to ready between renders
type Conv = { state: 'resolving' } | { state: 'ready'; hostId: string; sessionId: string; entry: ConversationEntry }
const entry: ConversationEntry = {
  doc: { ...emptyDoc(), turns: [{ id: 't0', index: 0, started_at: 1, outcome: 'done', items: [{ type: 'user', id: 'u0', at: 1, index: 0, text: 'hi', source: 'user' }] }] },
  status: 'live', reason: '', paging: false, subagents: {},
}
let conv: Conv = { state: 'ready', hostId: H, sessionId: SID, entry }
let asking = false
const subs = new Set<() => void>()
const setConv = (c: Conv) => { conv = c; act(() => { subs.forEach((f) => f()) }) }
const useConv = () => useSyncExternalStore((cb) => { subs.add(cb); return () => { subs.delete(cb) } }, () => conv)

function Renderer({ pane, isActive, isFocusTarget }: PaneRendererProps) {
  const c = useConv()
  return (
    <DeckPane
      paneId={pane.id} conversation={c} isActive={isActive} isFocusTarget={!!isFocusTarget} onSwitchToTerminal={() => {}}
      footer={() => (
        <>
          {asking && <div data-testid="dock-card" />}
          <SessionInput paneKey={pane.id} hostId={H} sessionId={SID} capabilities={{ send: 'prompt' }} asking={asking} onSwitchToTerminal={() => {}} />
        </>
      )}
    />
  )
}
const Other = () => <div data-testid="other-tab" />
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const settle = () => act(() => { vi.advanceTimersByTime(50) }) // the activation focus waits one frame

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  conv = { state: 'ready', hostId: H, sessionId: SID, entry }
  asking = false
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: Renderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllSendQueues(); vi.useRealTimers(); document.body.innerHTML = '' })

describe('the deck input takes the focus when its tab comes back', () => {
  it('away and back: the input has the focus, the caret at the end of the draft', () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    settle()
    writeDraft(draftKey(paneId, H, SID), 'half a thought')
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('session-input')).toBeNull()
    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    settle()
    expect(document.activeElement).toBe(box())
    expect(box().value).toBe('half a thought')
    expect(box().selectionStart).toBe('half a thought'.length)
    expect(box().selectionEnd).toBe('half a thought'.length)
  })

  it('the input that mounts after the activation (the conversation was still resolving) gets it then', () => {
    conv = { state: 'resolving' }
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    settle()
    expect(screen.queryByTestId('session-input')).toBeNull()
    expect(document.activeElement).toBe(screen.getByTestId('session-view-deck')) // the frame has it meanwhile
    setConv({ state: 'ready', hostId: H, sessionId: SID, entry })
    settle()
    expect(document.activeElement).toBe(box())
  })

  it('…but not if the reader has gone elsewhere in the meantime', () => {
    conv = { state: 'resolving' }
    render(<><input data-testid="elsewhere" /><TabContent activeTab={sessionTab} allTabs={all} /></>)
    settle()
    const other = screen.getByTestId('elsewhere')
    other.focus()
    setConv({ state: 'ready', hostId: H, sessionId: SID, entry })
    settle()
    expect(document.activeElement).toBe(other)
  })
})

describe('switching from the terminal view to this one', () => {
  // the pane keeps its terminal mounted beside the deck; its focus is the reader's own, in this very pane
  function PaneWithTerminal() {
    const term = useRef<HTMLTextAreaElement>(null)
    useLayoutEffect(() => { term.current?.focus() }, [])
    return (
      <div data-testid="pane-wrap">
        <textarea ref={term} data-testid="xterm-helper" />
        <DeckPane paneId="p-x" conversation={conv} isActive isFocusTarget onSwitchToTerminal={() => {}} footer={() => <SessionInput paneKey="p-x" hostId={H} sessionId={SID} capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />} />
      </div>
    )
  }
  it('the input takes the focus from the pane\'s own terminal', () => {
    render(<PaneWithTerminal />)
    expect(document.activeElement).toBe(screen.getByTestId('xterm-helper'))
    settle()
    expect(document.activeElement).toBe(screen.getByTestId('session-input').querySelector('textarea'))
  })
})

describe('what it does not take the focus from', () => {
  it('somebody typing in another input', () => {
    const { rerender } = render(<><input data-testid="elsewhere" /><TabContent activeTab={dashTab} allTabs={all} /></>)
    screen.getByTestId('elsewhere').focus()
    rerender(<><input data-testid="elsewhere" /><TabContent activeTab={sessionTab} allTabs={all} /></>)
    settle()
    expect(screen.getByTestId('session-input')).toBeInTheDocument()
    expect(document.activeElement).toBe(screen.getByTestId('elsewhere'))
  })

  it('a modal dialog that is up', () => {
    const { rerender } = render(<><div aria-modal="true" role="dialog"><button data-testid="in-dialog">ok</button></div><TabContent activeTab={dashTab} allTabs={all} /></>)
    screen.getByTestId('in-dialog').focus()
    rerender(<><div aria-modal="true" role="dialog"><button data-testid="in-dialog">ok</button></div><TabContent activeTab={sessionTab} allTabs={all} /></>)
    settle()
    expect(document.activeElement).toBe(screen.getByTestId('in-dialog'))
  })

  it('a choice question open in the dock', () => {
    asking = true
    const { rerender } = render(<TabContent activeTab={dashTab} allTabs={all} />)
    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    settle()
    expect(screen.getByTestId('dock-card')).toBeInTheDocument()
    expect(document.activeElement).not.toBe(box())
    expect(document.activeElement).toBe(screen.getByTestId('session-view-deck'))
  })
})
