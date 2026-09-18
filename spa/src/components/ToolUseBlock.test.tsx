// spa/src/components/ToolUseBlock.test.tsx — the durable tool_use block:
// tools-entry lookup + toToolCallActivity, then ToolCallBlock (P-B2.2 R2).
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import ToolUseBlock from './ToolUseBlock'
import type { ContentBlock } from '../lib/nex/message-types'
import type { ToolActivity } from '../lib/nex/tool-activity'

const block: ContentBlock = { type: 'tool_use', id: 'tu1', name: 'Bash', input: { command: 'ls' } }
const running: ToolActivity = { name: 'Bash', startedAt: 1_000, endedAt: null, status: 'running' }

describe('ToolUseBlock', () => {
  it('with a matching tools entry → activity passed (spinner + elapsed from now)', () => {
    render(<ToolUseBlock block={block} tools={{ tu1: running }} now={13_400} />)
    expect(screen.getByTestId('tool-icon-spinner')).toBeInTheDocument()
    expect(screen.getByTestId('tool-elapsed')).toHaveTextContent('12.4s')
    expect(screen.getByTestId('tool-header')).toHaveTextContent('ls')
  })

  it('done entry → wrench + duration badge', () => {
    render(<ToolUseBlock block={block} tools={{ tu1: { ...running, endedAt: 7_200, status: 'done' } }} now={99_999} />)
    expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
    expect(screen.getByTestId('tool-duration')).toHaveTextContent('6.2s')
  })

  it("no entry for this id, or no tools at all → plain (today's DOM: wrench, no badge)", () => {
    const { unmount } = render(<ToolUseBlock block={block} tools={{ other: running }} now={13_400} />)
    expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-elapsed')).not.toBeInTheDocument()
    unmount()
    render(<ToolUseBlock block={block} now={13_400} />)
    expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-icon-spinner')).not.toBeInTheDocument()
  })

  it('own-key lookup: block.id "constructor" with an empty tools map → plain DOM, no crash', () => {
    render(<ToolUseBlock block={{ ...block, id: 'constructor' }} tools={{}} now={13_400} />)
    expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-elapsed')).not.toBeInTheDocument()
    expect(screen.queryByTestId('tool-duration')).not.toBeInTheDocument()
    expect(screen.queryByTestId('tool-denied')).not.toBeInTheDocument()
  })

  it('own-key lookup: an entry reachable only through the prototype chain is ignored', () => {
    const inherited = Object.create({ tu1: running }) as Record<string, ToolActivity>
    render(<ToolUseBlock block={block} tools={inherited} now={13_400} />)
    expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-icon-spinner')).not.toBeInTheDocument()
    expect(screen.queryByTestId('tool-elapsed')).not.toBeInTheDocument()
  })

  it('a block without an id never looks up tools', () => {
    render(<ToolUseBlock block={{ type: 'tool_use', name: 'Bash', input: {} }} tools={{ undefined: running } as Record<string, ToolActivity>} now={13_400} />)
    expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-elapsed')).not.toBeInTheDocument()
  })

  it('missing name / input fall back to the unknown-tool label and an empty input', () => {
    render(<ToolUseBlock block={{ type: 'tool_use', id: 'tu1' }} now={0} />)
    expect(screen.getByTestId('tool-header')).toHaveTextContent('tool')
  })
})

// P-B3.2 Task 6 — the entry's N2 overlay reaches ToolCallBlock as `summaryEntry` (R1).
describe('ToolUseBlock N2 overlay passthrough (P-B3 R1)', () => {
  it('entry.primaryArg wins over the client summary of block.input', () => {
    const entry: ToolActivity = { ...running, endedAt: 7_200, status: 'done', primaryArg: { key: 'file_path', value: '/srv/x.ts' }, known: true }
    render(<ToolUseBlock block={block} tools={{ tu1: entry }} now={99_999} />)
    expect(screen.getByTestId('tool-header')).toHaveTextContent('/srv/x.ts')
    expect(screen.getByTestId('tool-header')).not.toHaveTextContent(/\bls\b/)
  })

  it('entry.known === false → R10 key: value fallback from block.input', () => {
    const entry: ToolActivity = { ...running, name: 'Mystery', endedAt: 7_200, status: 'done', known: false }
    render(<ToolUseBlock block={{ ...block, name: 'Mystery', input: { a: 1, b: 2 } }} tools={{ tu1: entry }} now={99_999} />)
    expect(screen.getByTestId('tool-header')).toHaveTextContent('a: 1, b: 2')
  })

  it('entry.durationMs reaches the badge; denied entry strikes the name through', () => {
    const entry: ToolActivity = { ...running, endedAt: 7_200, status: 'denied', durationMs: 26 }
    render(<ToolUseBlock block={block} tools={{ tu1: entry }} now={99_999} />)
    expect(screen.getByTestId('tool-denied')).toBeInTheDocument()
    expect(screen.getByText('Bash')).toHaveClass('line-through')
  })
})
