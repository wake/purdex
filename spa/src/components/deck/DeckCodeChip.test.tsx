// The deck's agent text shares the chat bubble's inline-code chip (#2463): the `.deck-md` wrapper on DeckItem is what the
// index.css rule keys on. jsdom does not compute CSS, so this pins the wrapper (deck has it; a bare RoomProse — the
// execution pane / room caller — does not) and the presence of the stylesheet rules (real-Chromium pixels are in the report).
import { describe, it, expect, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (deliberately no @types/node; see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
import { DeckItem } from './DeckItem'
import RoomProse from '../room/RoomProse'
import type { AgentTextItem } from '../../lib/conversations/types'

afterEach(cleanup)
const item = { type: 'agent_text', id: 'a1', at: 1, index: 0, markdown: '看 `x.ts`' } as unknown as AgentTextItem

describe('deck inline code chip scope', () => {
  it("the deck's RoomProse sits under .deck-md", () => {
    render(<DeckItem item={item} />)
    expect(screen.getByTestId('room-prose').closest('.deck-md')).not.toBeNull()
  })

  it('a RoomProse outside the deck (execution pane / room callers) has no .deck-md or .chat-md ancestor', () => {
    render(<RoomProse content="看 `x.ts`" />)
    const prose = screen.getByTestId('room-prose')
    expect(prose.closest('.deck-md')).toBeNull()
    expect(prose.closest('.chat-md')).toBeNull()
  })

  it('index.css keeps the .deck-md chip rule and its backtick-removing ::before/::after', () => {
    // @ts-expect-error __dirname is untyped here.
    const css = readFileSync(resolve(__dirname, '../../index.css'), 'utf8')
    expect(css).toMatch(/\.deck-md \.worker-prose :not\(pre\) > code\s*[,{]/)
    expect(css).toMatch(/\.deck-md \.worker-prose :not\(pre\) > code::before,\s*\.deck-md \.worker-prose :not\(pre\) > code::after\s*\{\s*content: none;/)
  })
})
