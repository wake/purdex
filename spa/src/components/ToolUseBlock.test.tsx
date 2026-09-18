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
