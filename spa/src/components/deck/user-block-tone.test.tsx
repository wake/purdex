// c9d9c7-59 pts 3, 4: the person's block borrows the sidebar's selected-workspace look (same frame, same tint, so a retune of the sidebar shows up here
// as a red test), and the agent's head line is one step brighter than it was.
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (deliberately no @types/node; see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
import { UserBlock } from './UserBlock'
import { AgentHeader } from './AgentHeader'
import type { UserItem } from '../../lib/conversations/types'

// @ts-expect-error __dirname is untyped here.
const rowSource: string = readFileSync(resolve(__dirname, '../../features/workspace/components/WorkspaceRow.tsx'), 'utf8')
const me: UserItem = { type: 'user', id: 'u', at: 1, index: 0, text: 'hello', source: 'user' }

describe('the person\'s block', () => {
  it('wears the sidebar\'s selected-workspace frame and tint (the classes are read from WorkspaceRow)', () => {
    const active = /isActive\s*\?\s*'([^']+)'/.exec(rowSource)![1]
    const tint = /bg-\[#[0-9a-f]{6}\]\/\d+/.exec(active)![0]
    const ring = /ring-1 ring-[a-z]+-\d+/.exec(active)![0]
    render(<UserBlock item={me} />)
    const box = screen.getByTestId('deck-user')
    // Mac feedback 10-11: an accepted message is one small step dimmer than the sidebar's selected row (ring 400 → 500, tint 25% → 20%),
    // same hue and frame; a retune of the sidebar's hue shows up here
    const dimmer = (s: string) => s.replace(/ring-([a-z]+)-400/, 'ring-$1-500').replace(/(bg-\[#[0-9a-f]{6}\]\/)25/, '$120')
    expect(box.className).toContain(dimmer(tint))
    expect(box.className).toContain(dimmer(ring))
  })
})

describe('the agent head line', () => {
  it('model name in primary text, icon in the brighter accent, time in secondary text', () => {
    render(<AgentHeader model="claude-opus-5-5" at={Date.UTC(2026, 9, 11, 11, 59)} />)
    const head = screen.getByTestId('deck-agent-header')
    expect(head).toHaveClass('text-text-secondary')
    expect(head.querySelector('.text-text-primary')?.textContent).toBe('claude-opus-5-5')
    expect(head.querySelector('.text-accent-hover')).not.toBeNull()
    expect(head.className).not.toContain('text-text-muted')
  })
})
