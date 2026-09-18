// spa/src/components/ToolCallBlock.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import ToolCallBlock from './ToolCallBlock'
import { formatDuration } from '../lib/nex/format-duration'

beforeEach(() => {
  cleanup()
})

describe('ToolCallBlock', () => {
  it('shows tool name', () => {
    render(<ToolCallBlock tool="Bash" input={{}} />)
    expect(screen.getByText('Bash')).toBeInTheDocument()
  })

  it('shows command for Bash tool', () => {
    render(<ToolCallBlock tool="Bash" input={{ command: 'ls -la' }} />)
    expect(screen.getByText(/ls -la/)).toBeInTheDocument()
  })

  it('shows file path for Read tool', () => {
    render(<ToolCallBlock tool="Read" input={{ file_path: '/tmp/test.txt' }} />)
    expect(screen.getByText(/\/tmp\/test\.txt/)).toBeInTheDocument()
  })

  it('is collapsible — detail hidden initially then visible on click', () => {
    render(<ToolCallBlock tool="Bash" input={{ command: 'echo hello', description: 'Say hello' }} />)
    // detail panel starts collapsed
    expect(screen.queryByTestId('tool-detail')).toBeNull()
    // click to expand
    fireEvent.click(screen.getByTestId('tool-header'))
    expect(screen.getByTestId('tool-detail')).toBeInTheDocument()
  })

  it('shows file path for Edit tool', () => {
    render(<ToolCallBlock tool="Edit" input={{ file_path: '/src/app.ts', old_string: 'a', new_string: 'b' }} />)
    expect(screen.getByText(/\/src\/app\.ts/)).toBeInTheDocument()
  })

  it('shows URL for WebFetch tool', () => {
    render(<ToolCallBlock tool="WebFetch" input={{ url: 'https://example.com' }} />)
    expect(screen.getByText(/example\.com/)).toBeInTheDocument()
  })

  it('uses unified wrench icon for all tools', () => {
    const { container } = render(<ToolCallBlock tool="Bash" input={{}} />)
    expect(container.querySelector('[data-testid="tool-icon-wrench"]')).toBeInTheDocument()
  })

  it('shows description for Agent tool', () => {
    render(<ToolCallBlock tool="Agent" input={{ description: 'Explore handoff code' }} />)
    expect(screen.getByText(/Explore handoff code/)).toBeInTheDocument()
  })

  it('shows pattern for Grep tool', () => {
    render(<ToolCallBlock tool="Grep" input={{ pattern: 'TODO' }} />)
    expect(screen.getByText(/TODO/)).toBeInTheDocument()
  })

  it('shows pattern for Glob tool', () => {
    render(<ToolCallBlock tool="Glob" input={{ pattern: '**/*.ts' }} />)
    expect(screen.getByText(/\*\*\/\*\.ts/)).toBeInTheDocument()
  })
})

// P-B2.2 task 7 — R2 status/timing and the R1 `streaming` variant, as one
// discriminated `activity` prop. `activity` absent stays covered by the G5
// snapshots below.
describe('ToolCallBlock activity (P-B2.2 R1/R2)', () => {
  it('running with startedAt=1000 now=13400 → spinner and 12.4s elapsed badge', () => {
    render(<ToolCallBlock tool="Bash" input={{ command: 'sleep 8' }} activity={{ status: 'running', startedAt: 1000, now: 13400 }} />)
    expect(screen.getByTestId('tool-icon-spinner')).toHaveClass('animate-spin')
    expect(screen.queryByTestId('tool-icon-wrench')).toBeNull()
    expect(screen.getByTestId('tool-elapsed')).toHaveTextContent('12.4s')
  })

  it('running with startedAt=0 → spinner and no elapsed badge', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'running', startedAt: 0, now: 13400 }} />)
    expect(screen.getByTestId('tool-icon-spinner')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-elapsed')).toBeNull()
  })

  it('running with now before startedAt → clamps to 0.0s', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'running', startedAt: 5000, now: 1000 }} />)
    expect(screen.getByTestId('tool-elapsed')).toHaveTextContent('0.0s')
  })

  it('done with startedAt=1000 endedAt=7200 → wrench and 6.2s duration badge', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'done', startedAt: 1000, endedAt: 7200 }} />)
    expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-icon-spinner')).toBeNull()
    expect(screen.getByTestId('tool-duration')).toHaveTextContent('6.2s')
    expect(screen.getByTestId('tool-duration')).not.toHaveClass('text-status-error')
  })

  it('done with unknown startedAt → no duration badge', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'done', startedAt: 0, endedAt: 7200 }} />)
    expect(screen.queryByTestId('tool-duration')).toBeNull()
  })

  it('done with endedAt 0 (unknown) → no duration badge', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'done', startedAt: 1000, endedAt: 0 }} />)
    expect(screen.queryByTestId('tool-duration')).toBeNull()
  })

  it('error → duration badge carries the error colour token', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'error', startedAt: 1000, endedAt: 7200 }} />)
    expect(screen.getByTestId('tool-duration')).toHaveTextContent('6.2s')
    expect(screen.getByTestId('tool-duration')).toHaveClass('text-status-error')
  })

  it('aborted → muted localized badge, wrench, no duration', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'aborted' }} />)
    expect(screen.getByTestId('tool-aborted')).toHaveTextContent('aborted')
    expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-duration')).toBeNull()
  })

  it('denied → warning-coloured localized badge, wrench, no duration (P-B3 Task 1: badge only)', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'denied', startedAt: 100, endedAt: 200 }} />)
    expect(screen.getByTestId('tool-denied')).toHaveTextContent('denied')
    expect(screen.getByTestId('tool-denied')).toHaveClass('text-status-warning')
    expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-duration')).toBeNull()
  })

  it('streaming with rawInput → header shows the raw prefix, expanded shows it in <pre>, spinner present', () => {
    const raw = '{"command":"sleep 8'
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'streaming', rawInput: raw }} />)
    expect(screen.getByTestId('tool-icon-spinner')).toBeInTheDocument()
    expect(screen.getByTestId('tool-header')).toHaveTextContent(raw)
    fireEvent.click(screen.getByTestId('tool-header'))
    const pre = screen.getByTestId('tool-detail').querySelector('pre')
    expect(pre).toHaveTextContent(raw)
    expect(pre?.textContent).not.toContain('{}')
  })

  it('streaming truncates the header summary to 80 chars', () => {
    const raw = '{"command":"' + 'x'.repeat(100)
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'streaming', rawInput: raw }} />)
    expect(screen.getByTestId('tool-header')).toHaveTextContent(raw.slice(0, 80))
    expect(screen.getByTestId('tool-header')).not.toHaveTextContent(raw.slice(0, 81))
  })

  it('streaming with empty rawInput → header shows only the tool name', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'streaming', rawInput: '' }} />)
    expect(screen.getByTestId('tool-icon-spinner')).toBeInTheDocument()
    expect(screen.getByTestId('tool-header')).toHaveTextContent(/^Bash$/)
    expect(screen.queryByTestId('tool-elapsed')).toBeNull()
  })
})

// P-B3.2 Task 6 — spec §4.4 R1 (server primary_arg via `summaryEntry`),
// R2 (`durationMs` wins over endedAt − startedAt) and R3 (denied = struck
// through name, wrench, warning badge, no duration).
describe('ToolCallBlock N2 overlay (P-B3 R1–R3)', () => {
  it('R1: summaryEntry.primaryArg → header shows the server value, not the client table', () => {
    render(<ToolCallBlock tool="Bash" input={{ command: 'ls' }} summaryEntry={{ primaryArg: { key: 'file_path', value: '/srv/x.ts' }, known: true }} />)
    expect(screen.getByTestId('tool-header')).toHaveTextContent('/srv/x.ts')
    expect(screen.getByTestId('tool-header')).not.toHaveTextContent(/\bls\b/)
  })

  it('R1: primaryArg value longer than 80 chars is truncated to 80 in the header', () => {
    const value = 'y'.repeat(100)
    render(<ToolCallBlock tool="Bash" input={{}} summaryEntry={{ primaryArg: { key: 'command', value }, known: true }} />)
    expect(screen.getByTestId('tool-header')).toHaveTextContent(value.slice(0, 80))
    expect(screen.getByTestId('tool-header')).not.toHaveTextContent(value.slice(0, 81))
  })

  it('R1: known:false → R10 key: value fallback from the input', () => {
    render(<ToolCallBlock tool="Mystery" input={{ a: 1, b: 'two' }} summaryEntry={{ known: false }} />)
    expect(screen.getByTestId('tool-header')).toHaveTextContent('a: 1, b: two')
  })

  it('R1: streaming rawInput still wins over summaryEntry', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'streaming', rawInput: '{"command":"sl' }} summaryEntry={{ primaryArg: { key: 'command', value: 'nope' }, known: true }} />)
    expect(screen.getByTestId('tool-header')).toHaveTextContent('{"command":"sl')
    expect(screen.getByTestId('tool-header')).not.toHaveTextContent('nope')
  })

  it('R2: done with durationMs=26 → badge formats 26ms (0.0s), not endedAt − startedAt (0.1s)', () => {
    expect(formatDuration(26)).toBe('0.0s')
    expect(formatDuration(200 - 100)).toBe('0.1s')
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'done', startedAt: 100, endedAt: 200, durationMs: 26 }} />)
    expect(screen.getByTestId('tool-duration')).toHaveTextContent(/^0\.0s$/)
  })

  it('R2: done with durationMs=2600 → 2.6s (endedAt − startedAt would be 0.1s)', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'done', startedAt: 100, endedAt: 200, durationMs: 2600 }} />)
    expect(screen.getByTestId('tool-duration')).toHaveTextContent(/^2\.6s$/)
  })

  it('R2: durationMs=null (unmatched result) → falls back to endedAt − startedAt', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'done', startedAt: 1000, endedAt: 7200, durationMs: null }} />)
    expect(screen.getByTestId('tool-duration')).toHaveTextContent(/^6\.2s$/)
  })

  it('R2: durationMs=26 with startedAt=0 / endedAt=0 (N2 unseen path) → still shows the badge', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'done', startedAt: 0, endedAt: 0, durationMs: 26 }} />)
    expect(screen.getByTestId('tool-duration')).toHaveTextContent(/^0\.0s$/)
  })

  it('R2: error with durationMs keeps the error colour token', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'error', startedAt: 0, endedAt: 0, durationMs: 2600 }} />)
    expect(screen.getByTestId('tool-duration')).toHaveTextContent(/^2\.6s$/)
    expect(screen.getByTestId('tool-duration')).toHaveClass('text-status-error')
  })

  it('R3: denied → name struck through and muted, wrench, warning badge, no duration', () => {
    render(<ToolCallBlock tool="Bash" input={{ command: 'rm -rf /' }} activity={{ status: 'denied', startedAt: 100, endedAt: 200, durationMs: 26 }} />)
    const name = screen.getByText('Bash')
    expect(name).toHaveClass('line-through')
    expect(name).toHaveClass('text-text-muted')
    expect(name).toHaveClass('font-semibold')
    expect(name).not.toHaveClass('text-text-primary')
    expect(screen.getByTestId('tool-icon-wrench')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-icon-spinner')).toBeNull()
    expect(screen.getByTestId('tool-denied')).toHaveClass('text-status-warning')
    expect(screen.queryByTestId('tool-duration')).toBeNull()
  })

  it('R3: non-denied statuses keep the primary name colour (no strike-through)', () => {
    render(<ToolCallBlock tool="Bash" input={{}} activity={{ status: 'error', startedAt: 1000, endedAt: 7200 }} />)
    const name = screen.getByText('Bash')
    expect(name).toHaveClass('text-text-primary')
    expect(name).not.toHaveClass('line-through')
  })
})

// P-B2.2 G5 guard: default-prop rendering must stay byte-identical while
// tasks 7–9 add optional status/timing props. Taken BEFORE any renderer change.
describe('ToolCallBlock default-prop snapshots (G5)', () => {
  it('collapsed with a Bash input', () => {
    const { container } = render(<ToolCallBlock tool="Bash" input={{ command: 'ls -la', description: 'List files' }} />)
    expect(container.firstChild).toMatchSnapshot()
  })

  it('expanded after clicking the header', () => {
    const { container } = render(<ToolCallBlock tool="Bash" input={{ command: 'ls -la', description: 'List files' }} />)
    fireEvent.click(screen.getByTestId('tool-header'))
    expect(container.firstChild).toMatchSnapshot()
  })
})
