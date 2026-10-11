// c9d9c7-65: the type size / line height setting (c9d9c7-60) reaches the step rows, the output folds, the thinking and system rows, the chat's work rows
// and the dock. They draw with `.deck-t-up` / `.deck-t-down` (the body +1px / -1px, so "one step under the text" stays one step under it) instead of
// Tailwind's fixed text-sm / text-xs. jsdom does not compute the stylesheet, so this checks the classes and that no fixed size is left in these files;
// the classes' own fallbacks (the look they always had) are pinned in DeckTypeSettings.test.tsx.
import { describe, it, expect, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (deliberately no @types/node; see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
import { FoldContext } from '../room/fold-context'
import { forgetFolds, usePaneFoldStore } from '../../lib/conversations/fold-memory'
import { StepView } from './StepViews'
import { OutputFold } from './OutputFold'
import { ThinkingRow, SystemRow } from './ThinkingSystem'
import type { StepItem, SystemItem, ThinkingItem } from '../../lib/conversations/types'

afterEach(() => { cleanup(); forgetFolds(PANE) })
const PANE = 'p-scale'
function Harness({ children }: { children: React.ReactNode }) {
  return <FoldContext.Provider value={usePaneFoldStore(PANE)}>{children}</FoldContext.Provider>
}
const renderIn = (ui: React.ReactElement) => render(<Harness>{ui}</Harness>)

// @ts-expect-error __dirname is untyped here.
const src = (rel: string): string => readFileSync(resolve(__dirname, rel), 'utf8')

const SCALED = [
  './StepViews.tsx', './OutputFold.tsx', './ThinkingSystem.tsx', './ChatWorkRow.tsx', './ChatPeerLine.tsx', './DeckItem.tsx', './DeckView.tsx', './ChatView.tsx',
  './SessionInput.tsx', './StatusSlot.tsx', '../room/FoldedOutput.tsx', './UserBlock.tsx', './AgentHeader.tsx', './TurnLines.tsx', './QueuedMessages.tsx', './PendingTail.tsx',
  '../dock/QuestionDock.tsx', '../room/ToolDiffView.tsx',
]

describe('no fixed Tailwind size is left in what the setting covers', () => {
  it.each(SCALED)('%s', (file) => {
    expect(src(file)).not.toMatch(/\btext-(xs|sm)\b/)
  })
})

const step = (over: Partial<StepItem>): StepItem => ({ type: 'step', id: 's', at: 1, index: 0, kind: 'execute', tool: 'Bash', status: 'done', summary: 'ls', started_at: 1, input: null, command: { text: 'ls -la' }, ...over }) as StepItem

describe('what draws', () => {
  it('a command step: the verb and subject (monospace) and the command block take the small step', () => {
    renderIn(<StepView step={step({})} />)
    const card = screen.getByTestId('deck-step-card')
    expect(card.querySelector('.deck-t-down.font-mono')).not.toBeNull()
    expect(card.innerHTML.match(/.{80}text-xs.{40}/)?.[0]).toBeUndefined()
  })

  it('the output fold', () => {
    renderIn(<OutputFold foldKey="k" output={{ text: 'a\nb', total_lines: 2, total_bytes: 3, truncated: false }} />)
    expect(screen.getByTestId('output-fold')).toHaveClass('deck-t-down')
  })

  it('a thinking row and a system row', () => {
    const thinking = { type: 'thinking', id: 't', at: 1, index: 0, text: 'hmm', duration_ms: 1000 } as unknown as ThinkingItem
    renderIn(<ThinkingRow item={thinking} />)
    expect(screen.getByTestId('deck-thinking')).toHaveClass('deck-t-down')
    cleanup()
    renderIn(<SystemRow item={{ type: 'system', id: 'y', at: 1, index: 0, kind: 'interrupted' } as unknown as SystemItem} />)
    expect(screen.getByTestId('deck-system')).toHaveClass('deck-t-down')
  })
})
