// Every kind of item, from the daemon's own golden output (testdata/conversation/v1), drawn as the spec §4 table says.
import type { ReactElement, ReactNode } from 'react'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { cleanup, fireEvent, render as rtlRender, screen, within } from '@testing-library/react'
import { FoldContext } from '../room/fold-context'
import { forgetFolds, usePaneFoldStore } from '../../lib/conversations/fold-memory'
import type { ConversationItem, StepItem, UserItem } from '../../lib/conversations/types'
import { DeckItem } from './DeckItem'
import readRange from '../../../../testdata/conversation/v1/cc-transcript/read-range/expected.json'
import askQuestion from '../../../../testdata/conversation/v1/cc-transcript/ask-question/expected.json'
import denial from '../../../../testdata/conversation/v1/cc-transcript/denial-kinds/expected.json'
import outputCaps from '../../../../testdata/conversation/v1/cc-transcript/output-caps/expected.json'
import subagent from '../../../../testdata/conversation/v1/cc-transcript/subagent/expected.json'
import sourceKinds from '../../../../testdata/conversation/v1/cc-transcript/source-kinds/expected.json'
import compact from '../../../../testdata/conversation/v1/cc-transcript/compact/expected.json'
import readGrep from '../../../../testdata/conversation/v1/cc-transcript/read-grep-glob-webfetch/expected.json'
import editWrite from '../../../../testdata/conversation/v1/cc-transcript/edit-write-multiedit/expected.json'
import interrupted from '../../../../testdata/conversation/v1/cc-transcript/ios-f2b-queue-interrupt/expected.json'
import sendKeys from '../../../../testdata/conversation/v1/cc-transcript/ios-c2-send-keys/expected.json'

const PANE = 'deck-test'
function Harness({ children }: { children: ReactNode }) {
  return <FoldContext.Provider value={usePaneFoldStore(PANE)}>{children}</FoldContext.Provider>
}
const render = (ui: ReactElement) => rtlRender(<Harness>{ui}</Harness>)

type Fixture = { conversation: { turns: Array<{ items: unknown[] }> } }
// The fixtures carry no placement `index`; the renderer does not read it.
const itemsOf = (f: unknown): ConversationItem[] =>
  (f as Fixture).conversation.turns.flatMap((t) => t.items).map((it, index) => ({ ...(it as object), index }) as ConversationItem)
const steps = (f: unknown) => itemsOf(f).filter((i): i is StepItem => i.type === 'step')
const find = <T extends ConversationItem>(items: ConversationItem[], pred: (i: ConversationItem) => boolean): T => {
  const hit = items.find(pred)
  if (!hit) throw new Error('fixture item missing')
  return hit as T
}

beforeEach(() => { cleanup(); forgetFolds(PANE) })

describe('user', () => {
  it('is a framed well with the caption of its source', () => {
    const items = itemsOf(sourceKinds).filter((i) => i.type === 'user') as UserItem[]
    const peer = find<UserItem>(items, (i) => (i as UserItem).source === 'peer')
    render(<DeckItem item={peer} />)
    expect(screen.getByTestId('deck-user-caption')).toHaveTextContent('From host/fixture-peer')
    cleanup()
    const queued = find<UserItem>(items, (i) => (i as UserItem).source === 'queued')
    render(<DeckItem item={queued} />)
    expect(screen.getByTestId('deck-user-caption')).toHaveTextContent('You · queued')
    cleanup()
    const sched = find<UserItem>(items, (i) => (i as UserItem).source === 'scheduled')
    render(<DeckItem item={sched} />)
    expect(screen.getByTestId('deck-user-caption')).toHaveTextContent('Scheduled wake-up')
  })

  it('a normal message is captioned with its time and shows its text whole', () => {
    const u = find<UserItem>(itemsOf(readGrep), (i) => i.type === 'user')
    render(<DeckItem item={u} />)
    expect(screen.getByTestId('deck-user-caption').textContent).toMatch(/^You · \d\d:\d\d$/)
    expect(screen.getByTestId('deck-user')).toHaveTextContent(u.text.slice(0, 20))
  })

  it('shows bash-mode input as a command and a background report with its caption', () => {
    const bash = find<UserItem>(itemsOf(sendKeys), (i) => (i as UserItem).source === 'bash')
    render(<DeckItem item={bash} />)
    expect(screen.getByTestId('deck-user')).toHaveTextContent(`! ${bash.text}`)
    cleanup()
    const task = find<UserItem>(itemsOf(subagent), (i) => (i as UserItem).source === 'task')
    render(<DeckItem item={task} />)
    expect(screen.getByTestId('deck-user-caption')).toHaveTextContent('Background task report')
  })
})

describe('agent_text and thinking', () => {
  it('renders the markdown unframed', () => {
    const a = find(itemsOf(readGrep), (i) => i.type === 'agent_text')
    render(<DeckItem item={a} />)
    expect(screen.getByTestId('deck-agent-text')).toBeInTheDocument()
    expect(screen.queryByTestId('stream-cursor')).toBeNull()
  })

  it('draws the cursor while streaming', () => {
    render(<DeckItem item={{ type: 'agent_text', id: 'x', at: 1, index: 0, markdown: 'hi', streaming: true }} />)
    expect(screen.getByTestId('stream-cursor')).toBeInTheDocument()
  })

  it('a thought is a collapsed disclosure that opens', () => {
    const th = { type: 'thinking', id: 'th1', at: 1, index: 0, text: 'pondering', duration_ms: 4200 } as ConversationItem
    render(<DeckItem item={th} />)
    expect(screen.getByTestId('thinking-toggle')).toHaveTextContent('Thinking · 4 s')
    expect(screen.queryByTestId('thinking-body')).toBeNull()
    fireEvent.click(screen.getByTestId('thinking-toggle'))
    expect(screen.getByTestId('thinking-body')).toHaveTextContent('pondering')
  })

  it('keeps the thought open across an unmount (a tab switch)', () => {
    const th = { type: 'thinking', id: 'th2', at: 1, index: 0, text: 'pondering' } as ConversationItem
    const first = render(<DeckItem item={th} />)
    fireEvent.click(screen.getByTestId('thinking-toggle'))
    first.unmount()
    render(<DeckItem item={th} />)
    expect(screen.getByTestId('thinking-body')).toBeInTheDocument()
  })
})

describe('step · edit', () => {
  it('is a card with the path, the +n −n stat and the diff', () => {
    const edit = find<StepItem>(steps(editWrite), (i) => (i as StepItem).kind === 'edit' && (i as StepItem).tool === 'Edit')
    render(<DeckItem item={edit} />)
    const card = screen.getByTestId('deck-step-card')
    expect(card).toHaveTextContent('Edit')
    expect(card).toHaveTextContent(edit.diff!.path)
    expect(within(card).getByTestId('diff-stat')).toHaveTextContent(`+${edit.diff!.added} −${edit.diff!.removed}`)
    expect(within(card).getByTestId('tool-diff')).toBeInTheDocument()
  })

  it('a created file says New file', () => {
    const created = find<StepItem>(steps(readRange), (i) => (i as StepItem).diff?.created === true)
    render(<DeckItem item={created} />)
    expect(screen.getByTestId('deck-step-card')).toHaveTextContent('New file')
  })

  it('a refused edit carries the 已拒絕 chip', () => {
    const denied = find<StepItem>(steps(readRange), (i) => (i as StepItem).status === 'denied')
    render(<DeckItem item={denied} />)
    expect(screen.getByTestId('step-chip')).toHaveTextContent('Denied')
  })
})

describe('step · edit, long diff and 顯示全部', () => {
  const longDiff = (lines: number, over: object = {}): StepItem => ({
    type: 'step', id: 'long', at: 1, index: 0, kind: 'edit', tool: 'Edit', status: 'done', summary: '/a', started_at: 1, input: null,
    diff: {
      path: '/a', added: lines, removed: 0, exact: true,
      hunks: [{ old_start: 1, old_lines: 0, new_start: 1, new_lines: lines, lines: Array.from({ length: lines }, (_, i) => `+row ${i + 1}`) }],
      ...over,
    },
  })

  it('draws the first 16 lines and hands the rest to the panel', () => {
    const onShowAll = vi.fn()
    const step = longDiff(40)
    render(<DeckItem item={step} actions={{ onShowAll }} />)
    expect(screen.getByTestId('tool-diff').textContent).toContain('row 16')
    expect(screen.getByTestId('tool-diff').textContent).not.toContain('row 17')
    expect(screen.queryByTestId('diff-more')).toBeNull()
    fireEvent.click(screen.getByTestId('diff-show-all'))
    expect(onShowAll).toHaveBeenCalledWith(step)
    expect(screen.getByTestId('diff-show-all')).toHaveTextContent('Show all 40 lines')
  })

  it('a diff of exactly 16 lines is whole, with no button', () => {
    render(<DeckItem item={longDiff(16)} actions={{ onShowAll: vi.fn() }} />)
    expect(screen.getByTestId('tool-diff').textContent).toContain('row 16')
    expect(screen.queryByTestId('diff-show-all')).toBeNull()
  })

  it('a daemon-truncated short diff offers the panel too', () => {
    render(<DeckItem item={longDiff(3, { truncated: true })} actions={{ onShowAll: vi.fn() }} />)
    expect(screen.getByTestId('diff-show-all')).toBeInTheDocument()
  })

  it('draws no 顯示全部 when nobody can show it', () => {
    render(<DeckItem item={longDiff(40)} />)
    expect(screen.queryByTestId('diff-show-all')).toBeNull()
  })

  it('an output without a handler has no 顯示全部 either', () => {
    const big = find<StepItem>(steps(outputCaps), (i) => (i as StepItem).kind === 'execute')
    render(<DeckItem item={big} />)
    fireEvent.click(screen.getByTestId('output-toggle'))
    expect(screen.queryByTestId('output-show-all')).toBeNull()
  })
})

describe('an output that is only pictures', () => {
  it('says the images are not shown instead of a 0-line fold', () => {
    const step = {
      type: 'step', id: 'img', at: 1, index: 0, kind: 'other', tool: 'Screenshot', status: 'done', summary: 'shot', started_at: 1, input: null,
      output: { text: '', total_lines: 0, total_bytes: 0, truncated: false, images: [{ media_type: 'image/png', bytes: 10 }] },
    } as ConversationItem
    render(<DeckItem item={step} />)
    expect(screen.getByTestId('output-images')).toHaveTextContent('1 image(s) not shown')
    expect(screen.queryByTestId('output-toggle')).toBeNull()
  })
})

describe('command_output', () => {
  const lines = (n: number) => Array.from({ length: n }, (_, i) => `out ${i + 1}`).join('\n')

  it('a user-source command_output is folded to 「輸出 · N 行」 and opens to the last 10 lines', () => {
    const item = { type: 'user', source: 'command_output', id: 'co1', at: 1, index: 0, text: lines(30) } as ConversationItem
    render(<DeckItem item={item} />)
    expect(screen.getByTestId('output-toggle')).toHaveTextContent('Output · 30 lines')
    expect(screen.queryByTestId('output-body')).toBeNull()
    fireEvent.click(screen.getByTestId('output-toggle'))
    expect(screen.getByTestId('output-body').textContent?.split('\n')).toHaveLength(10)
    expect(screen.getByTestId('output-cut')).toBeInTheDocument()
  })

  it('a long system command_output folds the same way; a short one stays a notice', () => {
    const long = { type: 'system', kind: 'command_output', id: 'co2', at: 1, index: 0, detail: { text: lines(12) } } as ConversationItem
    render(<DeckItem item={long} />)
    expect(screen.getByTestId('output-toggle')).toHaveTextContent('Output · 12 lines')
    cleanup()
    const short = { type: 'system', kind: 'command_output', id: 'co3', at: 1, index: 0, detail: { text: 'Bye!' } } as ConversationItem
    render(<DeckItem item={short} />)
    expect(screen.queryByTestId('output-toggle')).toBeNull()
    expect(screen.getByTestId('deck-system')).toHaveTextContent('Bye!')
  })
})

describe('step · execute', () => {
  it('shows the command in a clamped box and folds the output to the last 10 lines', () => {
    const big = find<StepItem>(steps(outputCaps), (i) => (i as StepItem).kind === 'execute')
    const onShowAll = vi.fn()
    render(<DeckItem item={big} actions={{ onShowAll }} />)
    expect(screen.getByTestId('exec-command')).toHaveTextContent(`$ ${big.command!.text}`)
    expect(screen.getByTestId('exec-command').className).toContain('line-clamp-6')
    expect(screen.queryByTestId('output-body')).toBeNull()
    fireEvent.click(screen.getByTestId('output-toggle'))
    const body = screen.getByTestId('output-body').textContent ?? ''
    expect(body.split('\n')).toHaveLength(10)
    expect(screen.getByTestId('output-cut')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('output-show-all'))
    expect(onShowAll).toHaveBeenCalledWith(big)
  })

  it('a failed command shows exit N in the status slot', () => {
    const failed = find<StepItem>(steps(denial), (i) => (i as StepItem).status === 'failed')
    render(<DeckItem item={{ ...failed, command: { ...failed.command!, exit_code: 3 } }} />)
    expect(screen.getByTestId('step-chip')).toHaveTextContent('exit 3')
  })

  it('interrupted and other denials read differently', () => {
    const exec = steps(denial).filter((s) => s.status === 'denied')
    const byReason = (r: string) => find<StepItem>(exec, (i) => (i as StepItem).denial === r)
    render(<DeckItem item={byReason('interrupted')} />)
    expect(screen.getByTestId('step-chip')).toHaveTextContent('Interrupted')
    cleanup()
    render(<DeckItem item={byReason('user-rejected')} />)
    expect(screen.getByTestId('step-chip')).toHaveTextContent('Denied')
    cleanup()
    render(<DeckItem item={byReason('permission-rule')} />)
    expect(screen.getByTestId('step-chip')).toHaveTextContent('Denied')
  })

  it('keeps an opened output open across an unmount', () => {
    const big = find<StepItem>(steps(outputCaps), (i) => (i as StepItem).kind === 'execute')
    const first = render(<DeckItem item={big} />)
    fireEvent.click(screen.getByTestId('output-toggle'))
    first.unmount()
    render(<DeckItem item={big} />)
    expect(screen.getByTestId('output-body')).toBeInTheDocument()
  })
})

describe('step · one-liners', () => {
  it('read: icon, verb, path, line range', () => {
    const ranged = find<StepItem>(steps(readRange), (i) => (i as StepItem).read?.limit === 20)
    render(<DeckItem item={ranged} />)
    const line = screen.getByTestId('deck-step-line')
    expect(line).toHaveTextContent('Read')
    expect(line).toHaveTextContent(ranged.summary)
    expect(screen.getByTestId('read-range')).toHaveTextContent('lines 10–29')
  })

  it('read without a range shows none', () => {
    const plain = find<StepItem>(steps(readRange), (i) => (i as StepItem).kind === 'read' && !(i as StepItem).read)
    render(<DeckItem item={plain} />)
    expect(screen.queryByTestId('read-range')).toBeNull()
  })

  it('search shows its scope; a click on the output line opens the output', () => {
    const search = find<StepItem>(steps(readRange), (i) => (i as StepItem).search?.where === '*.md')
    render(<DeckItem item={search} />)
    expect(screen.getByTestId('search-where')).toHaveTextContent('*.md')
    if (search.output) {
      fireEvent.click(screen.getByTestId('output-toggle'))
      expect(screen.getByTestId('output-body')).toBeInTheDocument()
    }
  })

  it('fetch and an unknown tool draw one line each', () => {
    const fetch = find<StepItem>(steps(readGrep), (i) => (i as StepItem).kind === 'fetch')
    render(<DeckItem item={fetch} />)
    expect(screen.getByTestId('deck-step-line')).toHaveTextContent('Web')
    cleanup()
    const other = find<StepItem>(steps(readGrep), (i) => (i as StepItem).tool === 'ToolSearch')
    render(<DeckItem item={other} />)
    expect(screen.getByTestId('deck-step-line')).toHaveTextContent('ToolSearch')
  })

  it('every step line has the fixed status slot, so a finish shifts nothing', () => {
    for (const s of [...steps(readGrep), ...steps(readRange)]) {
      render(<DeckItem item={s} />)
      expect(screen.getAllByTestId('status-slot').length).toBeGreaterThan(0)
      cleanup()
    }
  })

  it('a running step shows the dot', () => {
    const run = { ...find<StepItem>(steps(readGrep), (i) => (i as StepItem).kind === 'fetch'), status: 'running' } as StepItem
    render(<DeckItem item={run} />)
    expect(screen.getByTestId('step-running')).toBeInTheDocument()
  })
})

describe('step · task (subagent)', () => {
  it('is one line with the type and description; a click hands it to the panel, not nested', () => {
    const task = find<StepItem>(steps(subagent), (i) => (i as StepItem).kind === 'task')
    const onOpenSubagent = vi.fn()
    render(<DeckItem item={task} actions={{ onOpenSubagent }} />)
    const line = screen.getByTestId('deck-step-task')
    expect(line).toHaveTextContent(task.subagent!.type ? `Subagent · ${task.subagent!.type}` : 'Subagent')
    expect(line).toHaveTextContent(task.subagent?.description ?? task.summary)
    fireEvent.click(line)
    expect(onOpenSubagent).toHaveBeenCalledWith(task)
  })
})

describe('step · question', () => {
  const qs = steps(askQuestion).filter((s) => s.question)

  it('marks the chosen option once answered, and shows a typed answer', () => {
    const typed = find<StepItem>(qs, (i) => (i as StepItem).question?.answers?.[0]?.[0] === '晚上七點')
    render(<DeckItem item={typed} />)
    expect(screen.getByTestId('deck-question-free')).toHaveTextContent('✓ 晚上七點')
    cleanup()
    const picked = find<StepItem>(qs, (i) => (i as StepItem).question?.questions.length === 2)
    render(<DeckItem item={picked} />)
    const chosen = screen.getAllByTestId('deck-question-option').filter((el) => el.getAttribute('data-chosen') === 'true')
    expect(chosen.length).toBeGreaterThan(0)
    expect(chosen[0].textContent).toMatch(/^✓ /)
  })

  it('while open it says to answer below', () => {
    const open = find<StepItem>(qs, (i) => (i as StepItem).status === 'running')
    render(<DeckItem item={open} />)
    expect(screen.getByTestId('deck-question-open')).toHaveTextContent('Answer below')
  })

  it('a dismissed question carries the denied chip and no answer mark', () => {
    const denied = find<StepItem>(qs, (i) => (i as StepItem).status === 'denied')
    render(<DeckItem item={denied} />)
    expect(screen.getByTestId('step-chip')).toHaveTextContent('Denied')
    expect(screen.queryByTestId('deck-question-open')).toBeNull()
    expect(screen.getAllByTestId('deck-question-option').every((el) => el.getAttribute('data-chosen') === 'false')).toBe(true)
  })
})

describe('malformed question payloads', () => {
  const base = { type: 'step', id: 'q', at: 1, index: 0, kind: 'other', tool: 'AskUserQuestion', status: 'done', summary: 'q?', started_at: 1, input: null }
  const draw = (question: unknown) => render(<DeckItem item={{ ...base, question } as unknown as ConversationItem} />)

  it('questions that are not a list fall back to a plain line', () => {
    draw({ questions: 'nope' })
    expect(screen.getByTestId('deck-step-line')).toHaveTextContent('AskUserQuestion')
  })
  it('options, answers and entries of the wrong type read as empty', () => {
    draw({ questions: [{ question: 'a?', options: 5 }, null, { question: 'b?', options: [{ label: 'x' }] }], answers: [7, 'y', ['x', 3]] })
    expect(screen.getAllByTestId('deck-question')).toHaveLength(3)
    expect(screen.getAllByTestId('deck-question-option')).toHaveLength(1)
    expect(screen.getByTestId('deck-question-option').getAttribute('data-chosen')).toBe('true')
  })
  it('options that are null or have a non-string label are skipped', () => {
    draw({ questions: [{ question: 'a?', options: [null, { label: { x: 1 } }, { label: 'ok', description: 9 }] }] })
    expect(screen.getAllByTestId('deck-question-option')).toHaveLength(1)
    expect(screen.getByTestId('deck-question-option')).toHaveTextContent('ok')
  })
  it('answers that is not a list counts as unanswered', () => {
    draw({ questions: [{ question: 'a?', options: [{ label: 'x' }] }], answers: 'x' })
    expect(screen.queryByTestId('deck-question-free')).toBeNull()
  })
})

describe('system', () => {
  it('compaction and interrupt have their own lines', () => {
    const items = [...itemsOf(compact), ...itemsOf(interrupted)]
    render(<DeckItem item={find(items, (i) => i.type === 'system' && (i as { kind: string }).kind === 'compacted')} />)
    expect(screen.getByTestId('deck-system').textContent).toMatch(/^Context compacted · \d\d:\d\d$/)
    cleanup()
    render(<DeckItem item={find(items, (i) => i.type === 'system' && (i as { kind: string }).kind === 'interrupted')} />)
    expect(screen.getByTestId('deck-system')).toHaveTextContent('Interrupted')
  })

  it('a short command output is a small notice with the colour codes stripped', () => {
    const colored = find(itemsOf(compact), (i) => i.type === 'system' && JSON.stringify((i as { detail?: unknown }).detail).includes('Compacted ('))
    render(<DeckItem item={colored} />)
    expect(screen.getByTestId('deck-system').textContent).toBe('Compacted (ctrl+o to see full summary)')
  })

  it('a long machine note is folded under 系統 and opens', () => {
    const note = { type: 'system', kind: 'notice', id: 's1', at: 1, index: 0, detail: { text: 'line one\nline two' } } as ConversationItem
    render(<DeckItem item={note} />)
    expect(screen.queryByTestId('system-note')).toBeNull()
    fireEvent.click(screen.getByTestId('system-toggle'))
    expect(screen.getByTestId('system-note')).toHaveTextContent('line two')
  })
})

describe('unknown items', () => {
  it('an item type this build does not know draws nothing', () => {
    const { container } = render(<DeckItem item={{ type: 'hologram', id: 'h', at: 1, index: 0 }} />)
    expect(container).toBeEmptyDOMElement()
  })
})
