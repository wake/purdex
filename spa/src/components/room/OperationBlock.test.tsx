// spa/src/components/room/OperationBlock.test.tsx — one tool call is one
// block (spec §4.2), and failure is the loud one (spec §3.1.1 #1/#2).
//
// The second describe re-asserts the contracts of ToolCallBlock,
// ToolResultBlock and ToolUseBlock, which T3.3 deletes together with their
// test files. Those are P-B2 / P-B3 behaviours whose only guards live there,
// so whatever is not re-asserted here is lost (codex plan review #12).
import type { ReactElement, ReactNode } from 'react'
import { describe, it, expect, beforeEach } from 'vitest'
import { render as rtlRender, screen, cleanup, fireEvent } from '@testing-library/react'
import OperationBlock from './OperationBlock'
import { FoldContext, useFoldMemory } from './fold-context'
import type { OperationResult } from '../../lib/nex/operations'
import type { DiffHunk } from '../../lib/nex/tool-activity'
import type { ToolResultFacts } from '../../lib/nex/tool-result-facts'

beforeEach(() => { cleanup() })

/** The pane's fold memory; every foldable thing in the block reads it. */
function Harness({ children }: { children: ReactNode }) {
  const store = useFoldMemory()
  return <FoldContext.Provider value={store}>{children}</FoldContext.Provider>
}

const render = (ui: ReactElement) => rtlRender(<Harness>{ui}</Harness>)

const ok = (text: string): OperationResult => ({ text, isError: false })
const bad = (text: string): OperationResult => ({ text, isError: true })
const body = (n: number): string => Array.from({ length: n }, (_, i) => `line ${i + 1}`).join('\n')

const hunk: DiffHunk = { oldStart: 1, oldLines: 3, newStart: 1, newLines: 3, lines: [' hello', '-world', '+nexen', ' three'] }
const diffFacts: ToolResultFacts = { diff: { path: '/x', added: 1, removed: 1, hunks: [hunk], truncated: false } }

const block = () => screen.getByTestId('operation-block')

describe('OperationBlock', () => {
  it('renders the call and its result in one block', () => {
    render(<OperationBlock tool="Bash" input={{ command: 'ls -la' }} foldKey="tu1"
      activity={{ status: 'done', startedAt: 1_000, endedAt: 2_500 }} result={ok('total 8')} />)
    expect(screen.getAllByTestId('operation-block')).toHaveLength(1)
    // The result is inside the call's own block, not a second card beside it.
    expect(block()).toHaveTextContent('Bash')
    expect(block()).toHaveTextContent('ls -la')
    expect(block().contains(screen.getByTestId('fold-body'))).toBe(true)
    expect(screen.getByTestId('fold-body')).toHaveTextContent('total 8')
    expect(screen.queryByTestId('tool-result-block')).toBeNull()
  })

  it('has no border or background on success', () => {
    render(<OperationBlock tool="Bash" input={{ command: 'ls' }} foldKey="tu1"
      activity={{ status: 'done', startedAt: 1_000, endedAt: 2_500 }} result={ok('fine')} />)
    expect(block().className).not.toContain('border')
    expect(block().className).not.toContain('bg-')
    expect(screen.getByTestId('op-dot').className).toContain('bg-status-success')
    // Success carries no fill anywhere, not even on the rail (spec §3.1.1 #2).
    expect(screen.getByTestId('op-rail').className).not.toContain('bg-status')
  })

  it('an error fills the rail', () => {
    render(<OperationBlock tool="Bash" input={{ command: 'false' }} foldKey="tu1"
      activity={{ status: 'error', startedAt: 1_000, endedAt: 2_500 }} result={bad('boom')} />)
    expect(screen.getByTestId('op-rail').className).toContain('bg-status-error/10')
    expect(screen.getByTestId('op-dot').className).toContain('bg-status-error')
    expect(block().className).not.toContain('bg-')
  })

  it('a denial fills the rail with the warning tone and strikes the name', () => {
    render(<OperationBlock tool="Bash" input={{ command: 'rm -rf /' }} foldKey="tu1"
      activity={{ status: 'denied', startedAt: 100, endedAt: 200 }} result={ok('Permission denied')} />)
    expect(screen.getByTestId('op-rail').className).toContain('bg-status-warning/10')
    expect(screen.getByTestId('op-rail').className).not.toContain('bg-status-error')
    expect(screen.getByTestId('op-dot').className).toContain('bg-status-warning')
    const name = screen.getByTestId('op-name')
    expect(name).toHaveClass('line-through')
    expect(name).toHaveClass('text-text-muted')
    expect(name).not.toHaveClass('text-text-primary')
  })

  it('does not truncate a long argument', () => {
    const long = 'x'.repeat(300)
    render(<OperationBlock tool="Read" input={{ file_path: long }} foldKey="tu1"
      summaryEntry={{ primaryArg: { key: 'file_path', value: long }, known: true }}
      activity={{ status: 'done', startedAt: 1_000, endedAt: 2_500 }} result={ok('fine')} />)
    const arg = screen.getByTestId('op-arg')
    expect(arg.textContent).toHaveLength(300)
    expect(arg.textContent).toBe(long)
    // It wraps instead of being cut: no ellipsis, no `truncate`.
    expect(arg.className).toContain('whitespace-pre-wrap')
    expect(arg.className).toContain('break-all')
    expect(arg.className).not.toContain('truncate')
  })

  it('hides a sub-second duration', () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0, durationMs: 420 }} result={ok('fine')} />)
    expect(screen.queryByTestId('op-duration')).toBeNull()
    cleanup()
    // The control: the same block one millisecond over the bar does show one,
    // so the absence above is the threshold and not a missing element.
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0, durationMs: 1_000 }} result={ok('fine')} />)
    expect(screen.getByTestId('op-duration')).toHaveTextContent('1.0s')
  })

  it('shows a duration at one second', () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0, durationMs: 1_200 }} result={ok('fine')} />)
    expect(screen.getByTestId('op-duration')).toHaveTextContent('1.2s')
  })

  it('shows a placeholder while the input streams', () => {
    render(<OperationBlock tool="Read" input={{}} foldKey="partial-0"
      activity={{ status: 'streaming', rawInput: '{"file_path": "/Users/wake/Workspace/pane-des' }}
      result={null} />)
    expect(screen.getByTestId('op-arg-pending')).toBeInTheDocument()
    expect(screen.queryByTestId('op-arg')).toBeNull()
    // Never the half-assembled JSON (spec §3.1.1 #7).
    expect(block().textContent).not.toContain('file_path')
    expect(block().textContent).not.toContain('{')
    expect(block()).toHaveTextContent('Read')
  })

  it('folds a 100-line result to three lines', () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }} result={ok(body(100))} />)
    expect(screen.getByTestId('fold-body').textContent?.split('\n')).toHaveLength(3)
    expect(screen.getByTestId('fold-more')).toHaveTextContent('+97 lines')
  })

  it('keeps an error result one step less folded', () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'error', startedAt: 0, endedAt: 0 }} result={bad(body(100))} />)
    expect(screen.getByTestId('fold-body').textContent?.split('\n')).toHaveLength(6)
    expect(screen.getByTestId('fold-more')).toHaveTextContent('+94 lines')
  })

  it('reveals the raw input on demand', () => {
    render(<OperationBlock tool="Bash" input={{ command: 'ls', timeout: 5 }} foldKey="tu1"
      summaryEntry={{ primaryArg: { key: 'command', value: 'ls' }, known: true }}
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }} result={ok('fine')} />)
    expect(screen.queryByTestId('op-input')).toBeNull()
    fireEvent.click(screen.getByTestId('op-input-toggle'))
    const raw = screen.getByTestId('op-input')
    expect(raw.textContent).toBe(JSON.stringify({ command: 'ls', timeout: 5 }, null, 2))
  })

  it('offers no input affordance when the primary arg is the whole input', () => {
    render(<OperationBlock tool="Read" input={{ file_path: '/x' }} foldKey="tu1"
      summaryEntry={{ primaryArg: { key: 'file_path', value: '/x' }, known: true }}
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }} result={ok('fine')} />)
    expect(screen.queryByTestId('op-input-toggle')).toBeNull()
    cleanup()
    // The control: one key the primary arg does not cover and it is back.
    render(<OperationBlock tool="Read" input={{ file_path: '/x', offset: 10 }} foldKey="tu1"
      summaryEntry={{ primaryArg: { key: 'file_path', value: '/x' }, known: true }}
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }} result={ok('fine')} />)
    expect(screen.getByTestId('op-input-toggle')).toHaveTextContent('input')
  })

  it('renders a diff above the output', () => {
    render(<OperationBlock tool="Edit" input={{ file_path: '/x' }} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }} facts={diffFacts} result={ok('raw output')} />)
    const rail = screen.getByTestId('op-rail')
    const diff = screen.getByTestId('tool-diff')
    const out = screen.getByTestId('fold-body')
    expect(rail.contains(diff)).toBe(true)
    expect(rail.contains(out)).toBe(true)
    expect(diff.compareDocumentPosition(out) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('renders the diff stat with the diff', () => {
    // Spec §3.1.1 #3: the old right column stacked duration, size and the diff
    // stat; the stat's home in room is the diff, not the header row.
    render(<OperationBlock tool="Edit" input={{ file_path: '/x' }} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }}
      facts={{ diff: { path: '/x', added: 5, removed: 0, hunks: [hunk], truncated: false } }}
      result={ok('raw output')} />)
    const stat = screen.getByTestId('diff-stat')
    expect(stat).toHaveTextContent('+5 −0')
    // Inside the diff, inside the rail — and not in the header row beside the
    // tool name, which is where it used to be stacked.
    expect(screen.getByTestId('tool-diff').contains(stat)).toBe(true)
    expect(screen.getByTestId('op-rail').contains(stat)).toBe(true)
    const header = screen.getByTestId('op-name').parentElement as HTMLElement
    expect(header.contains(stat)).toBe(false)
    expect(header.textContent).not.toContain('+5')
  })

  it('the operation block itself has none', () => {
    // Spec §3.1.1 #1 opens the container exception for the diff alone. The
    // block around it stays frameless, so the container must land on the diff
    // and not one level up.
    render(<OperationBlock tool="Edit" input={{ file_path: '/x' }} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }} facts={diffFacts} result={ok('raw output')} />)
    expect(screen.getByTestId('tool-diff').className).toContain('border')
    expect(block().className).not.toContain('border')
    expect(block().className).not.toContain('rounded')
  })

  it('the diff stat repeats the path only when the header has none', () => {
    // Spec §3.1.1 #3 is about one fact drawn twice. For Edit and Write the
    // path *is* the header's argument, so the stat must not say it again;
    // for an orphan result — no call, therefore no argument — the stat is the
    // only place the file can be named. `+N −M` shows either way.
    const pathFacts: ToolResultFacts =
      { diff: { path: '/srv/app.ts', added: 5, removed: 0, hunks: [hunk], truncated: false } }

    const { unmount } = render(<OperationBlock tool="Edit" input={{ file_path: '/srv/app.ts' }} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }} facts={pathFacts} result={ok('ok')} />)
    expect(screen.getByTestId('op-arg')).toHaveTextContent('/srv/app.ts')
    expect(screen.queryByTestId('diff-path')).toBeNull()
    expect(screen.getByTestId('diff-stat')).toHaveTextContent('+5 −0')
    unmount()

    render(<OperationBlock tool="tool" input={{}} foldKey="tu1" facts={pathFacts} result={ok('ok')} />)
    expect(screen.queryByTestId('op-arg')).toBeNull()
    expect(screen.getByTestId('diff-path')).toHaveTextContent('/srv/app.ts')
    expect(screen.getByTestId('diff-stat')).toHaveTextContent('+5 −0')
  })

  it('the diff stat names the path when the header argument is something else', () => {
    // A header that draws an argument is not the same as one that draws the
    // path: an unknown tool whose summary is its command or description says
    // nothing about which file changed, so the stat has to.
    const pathFacts: ToolResultFacts =
      { diff: { path: '/srv/app.ts', added: 5, removed: 0, hunks: [hunk], truncated: false } }
    render(<OperationBlock tool="Bash" input={{ command: 'sed -i s/a/b/ app.ts' }} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }} facts={pathFacts} result={ok('ok')} />)
    expect(screen.getByTestId('op-arg')).toHaveTextContent('sed -i s/a/b/ app.ts')
    expect(screen.getByTestId('diff-path')).toHaveTextContent('/srv/app.ts')
  })

  it('marks a result that held non-text content', () => {
    // The other fact the dismantled facts span carried (spec §3.1.1 #3): a
    // fold must not swallow "there was something here the transcript is not
    // showing" in silence.
    render(<OperationBlock tool="Read" input={{ file_path: '/x.png' }} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }}
      facts={{ output: { totalLines: 1, totalBytes: 4, truncated: false, hasNonText: true } }}
      result={ok('data')} />)
    const marker = screen.getByTestId('op-non-text')
    expect(marker).toHaveTextContent('non-text')
    expect(screen.getByTestId('op-rail').contains(marker)).toBe(true)
  })

  it('does not mark a text-only result', () => {
    render(<OperationBlock tool="Read" input={{ file_path: '/x.ts' }} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }}
      facts={{ output: { totalLines: 1, totalBytes: 4, truncated: false, hasNonText: false } }}
      result={ok('data')} />)
    expect(screen.queryByTestId('op-non-text')).toBeNull()
  })

  it('renders an unanswered call with no rail', () => {
    render(<OperationBlock tool="Bash" input={{ command: 'sleep 8' }} foldKey="tu1"
      activity={{ status: 'running', startedAt: 1_000, now: 1_200 }} result={null} />)
    expect(screen.queryByTestId('op-rail')).toBeNull()
    expect(screen.queryByTestId('fold-body')).toBeNull()
    expect(block()).toHaveTextContent('sleep 8')
  })
})

// Contracts inherited from ToolCallBlock / ToolResultBlock / ToolUseBlock,
// whose test files T3.3 deletes (codex plan review #12).
describe('OperationBlock inherited contracts', () => {
  it('falls back to the unknown-tool label when the block has no name', () => {
    render(<OperationBlock tool="" input={{}} foldKey="tu1" result={null} />)
    expect(screen.getByTestId('op-name')).toHaveTextContent('tool')
  })

  // The `tools` map lookup itself lives in RoomTranscript (T3.3, T4.3), so
  // what this block still owns of the hostile-id case is its own key handling:
  // a fold key or an input key named `constructor` must not read off
  // Object.prototype, and the block must render as if the lookup simply missed.
  it('looks tools up by own key', () => {
    render(<OperationBlock tool="Bash" input={{ constructor: 'x' }} foldKey="constructor"
      result={ok(body(100))} />)
    // A miss leaves activity / summaryEntry / facts undefined: plain block, no crash.
    expect(screen.queryByTestId('op-duration')).toBeNull()
    expect(screen.queryByTestId('op-elapsed')).toBeNull()
    expect(screen.queryByTestId('op-aborted')).toBeNull()
    // `constructor` is not an expanded fold key just because Object.prototype has one.
    expect(screen.getByTestId('fold-more')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('fold-more'))
    expect(screen.getByTestId('fold-less')).toBeInTheDocument()
  })

  it("an N2 status outranks the raw frame's is_error", () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      facts={{ status: 'done' }} result={bad('fine')} />)
    expect(screen.getByTestId('op-dot').className).toContain('bg-status-success')
    expect(screen.getByTestId('op-dot').className).not.toContain('bg-status-error')
    expect(screen.getByTestId('op-rail').className).not.toContain('bg-status')
  })

  it('a raw result does not downgrade a denial', () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      facts={{ status: 'denied' }} result={ok('Permission denied')} />)
    expect(screen.getByTestId('op-dot').className).toContain('bg-status-warning')
    expect(screen.getByTestId('op-rail').className).toContain('bg-status-warning/10')
    expect(screen.getByTestId('op-name')).toHaveClass('line-through')
  })

  it('shows no duration when both clocks are unknown', () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }} result={ok('fine')} />)
    expect(screen.queryByTestId('op-duration')).toBeNull()
    cleanup()
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 7_200 }} result={ok('fine')} />)
    expect(screen.queryByTestId('op-duration')).toBeNull()
    cleanup()
    // The control: both clocks known and the badge is there, so the two
    // absences above are about the unknown clock and not about nothing rendering.
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'done', startedAt: 1_000, endedAt: 7_200 }} result={ok('fine')} />)
    expect(screen.getByTestId('op-duration')).toHaveTextContent('6.2s')
  })

  it("prefers the daemon's durationMs over the clock difference", () => {
    // endedAt − startedAt is 6.2s; the daemon says 1.2s and the daemon wins.
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'done', startedAt: 1_000, endedAt: 7_200, durationMs: 1_200 }} result={ok('fine')} />)
    expect(screen.getByTestId('op-duration')).toHaveTextContent(/^1\.2s$/)
    cleanup()
    // durationMs: null is an unmatched result, not a duration — fall back to the clocks.
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'done', startedAt: 1_000, endedAt: 7_200, durationMs: null }} result={ok('fine')} />)
    expect(screen.getByTestId('op-duration')).toHaveTextContent(/^6\.2s$/)
  })

  // codex R2 A2, inherited from ToolCallBlock: a call that was denied or that
  // failed still reports how long it took. The daemon's `durationMs` is the
  // only clock that survives — both raw-frame timestamps are 0 when unknown —
  // and dropping either branch from `durationOf` silently loses the badge on
  // exactly the two outcomes a reader most wants timed.
  it('shows a denial\u2019s durationMs when both clocks are unknown', () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'denied', startedAt: 0, endedAt: 0, durationMs: 1_200 }}
      result={ok('Permission denied')} />)
    expect(screen.getByTestId('op-duration')).toHaveTextContent(/^1\.2s$/)
  })

  it('shows an error\u2019s durationMs when both clocks are unknown', () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'error', startedAt: 0, endedAt: 0, durationMs: 1_200 }}
      result={bad('boom')} />)
    expect(screen.getByTestId('op-duration')).toHaveTextContent(/^1\.2s$/)
  })

  // The last fallback in `resolveStatus`: no lifecycle activity and no N2
  // status, so the raw frame's `is_error` is all there is. Every other status
  // test feeds an activity or a `facts.status`, so this path had no guard.
  it('falls back to the raw frame\u2019s is_error when nothing else says', () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1" result={bad('boom')} />)
    expect(screen.getByTestId('op-dot').className).toContain('bg-status-error')
    expect(screen.getByTestId('op-rail').className).toContain('bg-status-error/10')
    cleanup()
    // The control: the same block with is_error false is an ordinary success —
    // the ok dot and a rail with no fill at all.
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1" result={ok('fine')} />)
    expect(screen.getByTestId('op-dot').className).toContain('bg-status-success')
    expect(screen.getByTestId('op-dot').className).not.toContain('bg-status-error')
    expect(screen.getByTestId('op-rail').className).not.toContain('bg-status')
  })

  it('shows the aborted badge', () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'aborted' }} result={null} />)
    expect(screen.getByTestId('op-aborted')).toHaveTextContent('aborted')
    expect(screen.getByTestId('op-dot').className).toContain('bg-text-muted')
    expect(screen.queryByTestId('op-duration')).toBeNull()
  })

  it('renders a truncated diff that has no hunks', () => {
    render(<OperationBlock tool="Edit" input={{ file_path: '/x' }} foldKey="tu1"
      activity={{ status: 'done', startedAt: 0, endedAt: 0 }}
      facts={{ diff: { path: '/x', added: 0, removed: 0, hunks: [], truncated: true } }}
      result={ok('raw output')} />)
    expect(screen.getByTestId('tool-diff')).toBeInTheDocument()
    expect(screen.getByTestId('diff-truncated')).toBeInTheDocument()
    expect(screen.queryByTestId('diff-hunk')).toBeNull()
  })

  it('renders the elapsed timer only while running', () => {
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'running', startedAt: 1_000, now: 13_400 }} result={null} />)
    expect(screen.getByTestId('op-elapsed')).toHaveTextContent('12.4s')
    expect(screen.queryByTestId('op-duration')).toBeNull()
    cleanup()
    // A running call with an unknown start clock has nothing to tick from.
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'running', startedAt: 0, now: 13_400 }} result={null} />)
    expect(screen.queryByTestId('op-elapsed')).toBeNull()
    cleanup()
    // Finished: a duration, never an elapsed timer.
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'done', startedAt: 1_000, endedAt: 7_200 }} result={ok('fine')} />)
    expect(screen.queryByTestId('op-elapsed')).toBeNull()
    expect(screen.getByTestId('op-duration')).toHaveTextContent('6.2s')
    cleanup()
    // Streaming: no clock at all.
    render(<OperationBlock tool="Bash" input={{}} foldKey="tu1"
      activity={{ status: 'streaming', rawInput: '{' }} result={null} />)
    expect(screen.queryByTestId('op-elapsed')).toBeNull()
    expect(screen.queryByTestId('op-duration')).toBeNull()
  })
})
