// Deck / chat visual revision (round 2): system rows are the terminal's grey and left-aligned, the deck's own message is a thin
// accent frame with a faint tint, the chat keeps its bubbles (user text white), agent prose is one size / weight / leading in the
// normal face, the input is terminal-style. jsdom does not lay out, so this pins the class and CSS contract.
import { describe, it, expect, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import type { ReactNode } from 'react'
import { FoldContext } from '../room/fold-context'
import { usePaneFoldStore } from '../../lib/conversations/fold-memory'
import { SystemRow } from './ThinkingSystem'
import { UserBlock } from './UserBlock'
import { UserBubble, AgentBubble } from './ChatBubbles'
import type { AgentTextItem, SystemItem, UserItem } from '../../lib/conversations/types'

afterEach(cleanup)
const sys = (kind: string, detail?: unknown) => ({ type: 'system', id: `s-${kind}`, at: 1, index: 0, kind, detail }) as unknown as SystemItem
const user = (over: Partial<UserItem> = {}) => ({ type: 'user', id: 'u', at: new Date(2026, 9, 10, 1, 56).getTime(), index: 0, text: 'hi', source: 'user', ...over }) as UserItem

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8')
const css = read('../../index.css')
const themes = read('../../styles/themes.css')
const inputSrc = read('SessionInput.tsx')

function Folds({ children }: { children: ReactNode }) {
  return <FoldContext.Provider value={usePaneFoldStore('p')}>{children}</FoldContext.Provider>
}

describe('system rows', () => {
  it.each(['interrupted', 'compacted'])('%s: grey token, left-aligned, no white label', (kind) => {
    render(<Folds><SystemRow item={sys(kind)} /></Folds>)
    const row = screen.getByTestId('deck-system')
    expect(row).toHaveClass('text-text-system', 'text-left')
    expect(row.className).not.toMatch(/text-center|text-text-primary|text-text-secondary|font-(medium|bold|semibold)|uppercase/)
    expect(row.textContent).not.toMatch(/^System|系統/)
  })
  it('the grey token is ~#949494 in dark and a darker grey of similar contrast in light', () => {
    expect(themes).toMatch(/\[data-theme="dark"\][^}]*--text-system: #949494/s)
    expect(themes).toMatch(/\[data-theme="light"\][^}]*--text-system: #6e6e6e/s)
    expect(themes).toContain('--color-text-system: var(--text-system)')
  })
})

describe('deck user block', () => {
  it('thin accent border, a very faint accent tint, accent caption', () => {
    render(<UserBlock item={user()} />)
    const box = screen.getByTestId('deck-user')
    expect(box).toHaveClass('border', 'border-accent', 'bg-accent/5')
    expect(box.className).not.toMatch(/bg-accent\/(1\d|[2-9]\d)/)
    expect(screen.getByTestId('deck-user-caption')).toHaveClass('text-accent')
  })
})

describe('chat keeps its bubbles', () => {
  it('the person\'s message: right-aligned accent bubble with WHITE text', () => {
    render(<UserBubble item={user()} />)
    expect(screen.getByTestId('chat-user')).toHaveClass('items-end')
    const bubble = screen.getByTestId('chat-user-bubble')
    expect(bubble).toHaveClass('bg-accent', 'text-white')
    expect(bubble.className).not.toContain('text-text-inverse')
    expect(screen.queryByTestId('deck-user')).toBeNull()
  })
  it('the agent\'s message: the original surface bubble, no head line', () => {
    render(<AgentBubble item={{ type: 'agent_text', id: 'a', at: 1, index: 0, markdown: 'yo' } as unknown as AgentTextItem} />)
    expect(screen.getByTestId('chat-agent').firstElementChild).toHaveClass('bg-surface-secondary', 'rounded-2xl')
    expect(screen.queryByTestId('deck-agent-header')).toBeNull()
  })
})

describe('agent prose typography (index.css)', () => {
  const block = css.slice(css.indexOf('=== deck / chat agent text'))
  it('keeps the normal face: no font-family override on the prose', () => {
    expect(block).not.toMatch(/font-family\s*:\s*Menlo/)
    expect(block).not.toMatch(/font-size\s*:\s*14px/)
  })
  it('headings are body-size and bold, strong is bold only, lists keep one size and leading', () => {
    expect(block).toMatch(/h1, h2, h3, h4, h5, h6, strong, b\)[^{]*\{\s*font-size: 1em; font-weight: 700/)
    expect(block).toMatch(/p, li, ul, ol, blockquote, h1[^{]*\{\s*font-size: 1em; line-height: inherit/)
  })
  it('inline code stays a chip without backticks', () => {
    expect(css).toMatch(/\.deck-md \.worker-prose :not\(pre\) > code::after\s*\{\s*content: none/)
  })
})

describe('terminal-style input', () => {
  const src = inputSrc
  it('uses the normal face (no mono) and no frame', () => {
    expect(src).not.toContain('font-mono text-sm')
    expect(src).toContain('border-y')
    expect(src).toContain('border-[#808080]')
  })
})
