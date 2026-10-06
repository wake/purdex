// Conversation entity spec §10.5: a rebuild swaps the pane from E1 to E2 (same
// session); the pane renderer keys ExecutionView by host + execution, so the
// view remounts. E2's prelude then draws E1's worker lines as a run attributed
// to E1, and the new view's fold memory and search query start empty.
// PreludeSegment is wrapped (in this file only) to show each run's stint.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor, within } from '@testing-library/react'
import ExecutionView from './ExecutionView'
import type { PreludeSegmentProps } from '../room/prelude/PreludeSegment'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { createTab } from '../../types/tab'
import { getPrimaryPane } from '../../lib/pane-tree'
import { executionContentFor } from '../../lib/nex/handoff'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), uploadWorkerFile: vi.fn(), fetchExecutionPrelude: vi.fn(), listExecutions: vi.fn() }))
vi.mock('../../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null, paused: false })) }))
vi.mock('../../hooks/useExecutionLease', () => ({ useExecutionLease: vi.fn() }))
vi.mock('../../lib/nex/client-id', () => ({ getNexClientId: () => 't-me000000' }))
vi.mock('../room/prelude/PreludeSegment', async (importOriginal) => {
  const Real = (await importOriginal<typeof import('../room/prelude/PreludeSegment')>()).default
  return {
    default: (p: PreludeSegmentProps) => (
      <div data-testid="prelude-run" data-stint={p.stintId ?? 'plain'}
        data-poses={(p.mode === 'room' ? p.entries.map((e) => e.pos) : p.blocks.flatMap((b) => (b.kind === 'span' ? p.posOf.slice(b.start, b.end) : [b.entry.pos]))).join(' ')}>
        <Real {...p} />
      </div>
    ),
  }
})

const H = 'h'
const S = '7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b'
const E1 = 'exc_e1', E2 = 'exc_e2'
const B1 = 1000 // E1's transcript starts here: E1's own prelude ends here
const said = (role: 'user' | 'assistant', text: string) =>
  ({ type: role, parent_tool_use_id: null, message: { role, content: [{ type: 'text', text }], stop_reason: null } })
const summary = (id: string, created_at: number) => ({
  id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {}, created_at, updated_at: created_at,
  duration_ms: null, event_count: 0, observers: 1, archived: false, effective_profile: 'standard', turn_count: 1, resume_session_id: S,
})
const longNote = Array.from({ length: 80 }, (_, i) => `row ${i}`).join('\n')
// The terminal part, before E1: what E1's own prelude holds.
const terminal = [
  { pos: '1', at: 1, offset: 0, kind: 'prelude.segment', entrypoint: 'cli' },
  { pos: '2', at: 1, offset: 0, kind: 'user', msg: said('user', 'fix the terminal build') },
  { pos: '3', at: 1, offset: 400, kind: 'prelude.note', source: 'command_output', text: longNote, truncated: false, totalBytes: null, stream: null },
]
// E2's prelude adds what E1 wrote as a worker.
const workerE1 = [
  { pos: '4', at: 1, offset: B1, kind: 'prelude.segment', entrypoint: 'sdk-cli' },
  { pos: '5', at: 1, offset: B1, kind: 'user', msg: said('user', 'the brief for E1') },
  { pos: '6', at: 1, offset: 1500, kind: 'assistant', msg: said('assistant', 'E1 did the work') },
]
const page = (items: unknown[], totalBytes: number) => ({ state: 'ok', prevCursor: null, totalBytes, items })

/** The pane renderer's own keying (register-modules ExecutionPaneWrapper): host + execution. */
function Pane({ tabId, paneId }: { tabId: string; paneId: string }) {
  const content = useTabStore((s) => getPrimaryPane(s.tabs[tabId].layout).content)
  if (content.kind !== 'execution') return null
  return (
    <ExecutionView key={`${content.host}:${content.executionId}`} hostId={content.host!} executionId={content.executionId}
      isActive tabId={tabId} paneId={paneId} onModeChange={() => {}} />
  )
}

const realEnsure = useNexHostStore.getState().ensure
beforeEach(() => {
  useExecutionStore.setState({ executions: {} })
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease: vi.fn(), release: vi.fn(), forget: vi.fn(), touch: vi.fn() })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  // `ensure` would re-resolve the (unregistered) test host and drop the seeded capabilities.
  useNexHostStore.setState({
    ensure: async () => {},
    byHost: { [H]: { phase: 'ready', capabilities: {
      transcript_prelude: { route: { method: 'GET', path: '/x' }, page_max_items: 500, page_max_bytes: 1, max_block_bytes: 1, item_offset: true },
      list: { session_filter: true },
    } } },
  } as never)
  for (const [id, at, text] of [[E1, 1, 'E1 live'], [E2, 2, 'E2 live']] as const) {
    useExecutionStore.getState().setSummary(H, id, summary(id, at) as never)
    useExecutionStore.getState().setHistoryLoaded(H, id, true)
    useExecutionStore.setState((s) => ({ executions: { ...s.executions, [`${H}:${id}`]: { ...s.executions[`${H}:${id}`], messages: [said('user', text)], turnStarts: [0] } } }) as never)
  }
  vi.mocked(api.fetchExecutionPrelude).mockReset().mockImplementation(async (_h, id) =>
    (id === E1 ? page(terminal, B1) : page([...terminal, ...workerE1], 2000)) as never)
  vi.mocked(api.listExecutions).mockReset().mockResolvedValue({ items: [summary(E1, 1), summary(E2, 2)], next_cursor: '' } as never)
})
afterEach(() => {
  useNexHostStore.setState({ byHost: {}, ensure: realEnsure })
})

const runs = () => screen.queryAllByTestId('prelude-run').map((r) => [r.getAttribute('data-stint'), r.getAttribute('data-poses')])
const noteFold = () => within(screen.getByTestId('prelude-note-command_output'))

function mountOn(executionId: string) {
  const tab = createTab({ kind: 'execution', executionId, host: H })
  useTabStore.getState().addTab(tab)
  const ids = { tabId: tab.id, paneId: getPrimaryPane(tab.layout).id }
  render(<Pane {...ids} />)
  return ids
}

describe('ExecutionView — stint switch (§10.5)', () => {
  it('a rebuild from E1 to E2 remounts the view; E1\'s worker lines become E1\'s run; folds and search start empty', async () => {
    const { tabId, paneId } = mountOn(E1)
    await screen.findByText('fix the terminal build')
    // E1's prelude is all terminal: nothing to attribute, so no listing.
    expect(runs()).toEqual([['plain', '1 2 3']])
    expect(api.listExecutions).not.toHaveBeenCalled()
    // The reader opens the note and searches.
    fireEvent.click(noteFold().getByTestId('fold-more'))
    expect(noteFold().getByTestId('fold-less')).toBeInTheDocument()
    fireEvent.pointerDown(screen.getByRole('textbox'))
    fireEvent.keyDown(document.body, { key: 'f', ctrlKey: true })
    fireEvent.change(screen.getByTestId('transcript-search-input'), { target: { value: 'terminal' } })
    const before = screen.getByTestId('execution-view')

    // The rebuild swaps the pane's content, as worker-rebuild does.
    act(() => { useTabStore.getState().trySetPaneContent(tabId, paneId, executionContentFor(H, E2)) })
    expect(screen.getByTestId('execution-view')).not.toBe(before)
    await screen.findByText('E1 did the work')
    expect(api.listExecutions).toHaveBeenCalledWith(H, expect.objectContaining({ sessionId: S, includeArchived: true }))
    await waitFor(() => expect(runs()).toEqual([['plain', '1 2 3'], [E1, '4 5 6']]))

    // The new view's fold memory and search query are fresh.
    expect(noteFold().getByTestId('fold-more')).toBeInTheDocument()
    expect(noteFold().queryByTestId('fold-less')).toBeNull()
    expect(screen.queryByTestId('transcript-search')).toBeNull()
    fireEvent.pointerDown(screen.getByRole('textbox'))
    fireEvent.keyDown(document.body, { key: 'f', ctrlKey: true })
    expect(screen.getByTestId('transcript-search-input')).toHaveValue('')
  })

  it('the stint list unavailable: E1\'s lines stay a plain segment (§10.6)', async () => {
    vi.mocked(api.listExecutions).mockReset().mockRejectedValue(new Error('down'))
    mountOn(E2)
    await screen.findByText('E1 did the work')
    await waitFor(() => expect(api.listExecutions).toHaveBeenCalled())
    await act(async () => {})
    expect(runs()).toEqual([['plain', '1 2 3 4 5 6']])
  })
})
