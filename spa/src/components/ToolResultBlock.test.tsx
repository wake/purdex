// spa/src/components/ToolResultBlock.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { Prohibit } from '@phosphor-icons/react'
import ToolResultBlock from './ToolResultBlock'

beforeEach(() => { cleanup() })

describe('ToolResultBlock', () => {
  it('renders collapsed success state', () => {
    render(<ToolResultBlock content="output text" isError={false} />)
    expect(screen.getByTestId('tool-result-header')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-result-content')).toBeNull()
  })

  it('expands to show content on click', () => {
    render(<ToolResultBlock content="command output here" isError={false} />)
    fireEvent.click(screen.getByTestId('tool-result-header'))
    expect(screen.getByTestId('tool-result-content')).toHaveTextContent('command output here')
  })

  it('renders error state with different styling', () => {
    const { container } = render(<ToolResultBlock content="error msg" isError={true} />)
    const block = container.querySelector('[data-testid="tool-result-block"]')
    expect(block?.className).toContain('border-[#302a2a]')
  })

  it('truncates long content in header summary', () => {
    const longContent = 'a'.repeat(200)
    render(<ToolResultBlock content={longContent} isError={false} />)
    const header = screen.getByTestId('tool-result-header')
    expect(header.textContent!.length).toBeLessThan(150)
  })

  it('collapses again on second click', () => {
    render(<ToolResultBlock content="some output" isError={false} />)
    fireEvent.click(screen.getByTestId('tool-result-header'))
    expect(screen.getByTestId('tool-result-content')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('tool-result-header'))
    expect(screen.queryByTestId('tool-result-content')).toBeNull()
  })

  it('renders success state with green border styling', () => {
    const { container } = render(<ToolResultBlock content="ok" isError={false} />)
    const block = container.querySelector('[data-testid="tool-result-block"]')
    expect(block?.className).toContain('border-[#2a302a]')
  })
})

// P-B3.2 guard: the no-`facts` DOM must stay byte-identical while Task 8
// adds the optional `facts` prop. Taken BEFORE any renderer change; later
// tasks run these WITHOUT `-u` — a diff means fix the component, not the snap.
describe('baseline snapshots (P-B3.2 guard)', () => {
  const content = 'line one\nline two'
  const longContent = 'error: '.repeat(20) // 140 chars → `...` summary in header

  it('collapsed ok', () => {
    const { container } = render(<ToolResultBlock content={content} isError={false} />)
    expect(container.firstChild).toMatchSnapshot()
  })

  it('collapsed error with long content (summary truncated)', () => {
    const { container } = render(<ToolResultBlock content={longContent} isError={true} />)
    expect(container.firstChild).toMatchSnapshot()
  })

  it('expanded ok', () => {
    const { container } = render(<ToolResultBlock content={content} isError={false} />)
    fireEvent.click(screen.getByTestId('tool-result-header'))
    expect(container.firstChild).toMatchSnapshot()
  })

  it('expanded error', () => {
    const { container } = render(<ToolResultBlock content={content} isError={true} />)
    fireEvent.click(screen.getByTestId('tool-result-header'))
    expect(container.firstChild).toMatchSnapshot()
  })
})

// P-B3.2 Task 8 — `facts` prop: R4 facts span + R3 denied override of isError.
describe('facts prop (P-B3.2 R3 / R4)', () => {
  // Phosphor renders no testid on these icons; identify them by the path data
  // (the same strings the baseline snapshots above pin down).
  const XCIRCLE_PATH = 'M165.66,101.66,139.31,128l26.35,26.34a8,8,0,0,1-11.32,11.32L128,139.31l-26.34,26.35a8,8,0,0,1-11.32-11.32L116.69,128,90.34,101.66a8,8,0,0,1,11.32-11.32L128,116.69l26.34-26.35a8,8,0,0,1,11.32,11.32ZM232,128A104,104,0,1,1,128,24,104.11,104.11,0,0,1,232,128Zm-16,0a88,88,0,1,0-88,88A88.1,88.1,0,0,0,216,128Z'
  const CHECKCIRCLE_PATH = 'M173.66,98.34a8,8,0,0,1,0,11.32l-56,56a8,8,0,0,1-11.32,0l-24-24a8,8,0,0,1,11.32-11.32L112,148.69l50.34-50.35A8,8,0,0,1,173.66,98.34ZM232,128A104,104,0,1,1,128,24,104.11,104.11,0,0,1,232,128Zm-16,0a88,88,0,1,0-88,88A88.1,88.1,0,0,0,216,128Z'
  const statusIconPath = (container: HTMLElement) =>
    container.querySelectorAll('[data-testid="tool-result-header"] > svg')[1]?.querySelector('path')?.getAttribute('d')
  const prohibitPath = () => {
    const { container, unmount } = render(<Prohibit size={14} />)
    const d = container.querySelector('path')?.getAttribute('d')
    unmount()
    return d
  }

  it('R4: file.lines → "4 lines" in the facts span after the summary', () => {
    render(<ToolResultBlock content="a\nb\nc\nd" isError={false} facts={{ file: { path: '/x', lines: 4 } }} />)
    const facts = screen.getByTestId('tool-result-facts')
    expect(facts).toHaveTextContent('4 lines')
    expect(facts.className).toContain('tabular-nums')
    // order: summary span, then facts span
    const spans = screen.getByTestId('tool-result-header').querySelectorAll('span')
    expect(spans[0].className).toContain('truncate')
    expect(spans[1]).toBe(facts)
  })

  it('R4: diff → "+1 −1"', () => {
    render(<ToolResultBlock content="ok" isError={false}
      facts={{ diff: { path: '/x', added: 1, removed: 1, hunks: [], truncated: false } }} />)
    expect(screen.getByTestId('tool-result-facts')).toHaveTextContent('+1 −1')
  })

  it('R3: denied + isError → neutral colours, Prohibit icon, denied badge, raw content still shown', () => {
    const { container } = render(<ToolResultBlock content="Permission denied" isError={true} facts={{ status: 'denied' }} />)
    const block = container.querySelector('[data-testid="tool-result-block"]')!
    expect(block.className).toContain('border-[#2a302a]')
    expect(block.className).not.toContain('border-[#302a2a]')
    const header = screen.getByTestId('tool-result-header')
    expect(header.className).toContain('text-[#8bc]')
    expect(header.className).not.toContain('text-[#c77]')
    const badge = screen.getByTestId('tool-result-denied')
    expect(badge).toHaveTextContent('denied')
    expect(badge.className).toContain('text-status-warning')
    // icon: Prohibit, not XCircle / CheckCircle; still exactly two svgs (caret + status)
    expect(container.querySelectorAll('[data-testid="tool-result-header"] > svg')).toHaveLength(2)
    const d = statusIconPath(container as HTMLElement)
    expect(d).not.toBe(XCIRCLE_PATH)
    expect(d).not.toBe(CHECKCIRCLE_PATH)
    expect(d).toBe(prohibitPath())
    fireEvent.click(header)
    const content = screen.getByTestId('tool-result-content')
    expect(content).toHaveTextContent('Permission denied')
    expect(content.className).toContain('text-[#9b9]')
  })

  it('R3: denied without isError also renders the Prohibit icon and the badge', () => {
    const { container } = render(<ToolResultBlock content="x" isError={false} facts={{ status: 'denied' }} />)
    expect(screen.getByTestId('tool-result-denied')).toBeInTheDocument()
    expect(statusIconPath(container as HTMLElement)).toBe(prohibitPath())
  })

  it('facts with no renderable segments → no facts node; DOM identical to the no-facts render', () => {
    const a = render(<ToolResultBlock content="ok" isError={false} />)
    const plain = a.container.innerHTML
    a.unmount()
    const b = render(<ToolResultBlock content="ok" isError={false} facts={{ status: 'done' }} />)
    expect(screen.queryByTestId('tool-result-facts')).not.toBeInTheDocument()
    expect(screen.queryByTestId('tool-result-denied')).not.toBeInTheDocument()
    expect(b.container.innerHTML).toBe(plain)
  })

  it('error + facts (not denied) keeps the error colours and the XCircle icon', () => {
    const { container } = render(<ToolResultBlock content="boom" isError={true}
      facts={{ status: 'error', output: { totalLines: 3, totalBytes: 9, truncated: false, hasNonText: false } }} />)
    expect(container.querySelector('[data-testid="tool-result-block"]')!.className).toContain('border-[#302a2a]')
    expect(statusIconPath(container as HTMLElement)).toBe(XCIRCLE_PATH)
    expect(screen.getByTestId('tool-result-facts')).toHaveTextContent('3 lines')
  })

  // codex R2 A1: whenever N2 supplies a status it decides the tone; the raw
  // `is_error` flag is only the pre-N2 fallback.
  it('A1: facts.status error + isError false → error colours and the XCircle icon (N2 wins)', () => {
    const { container } = render(<ToolResultBlock content="boom" isError={false} facts={{ status: 'error' }} />)
    const block = container.querySelector('[data-testid="tool-result-block"]')!
    expect(block.className).toContain('border-[#302a2a]')
    expect(block.className).not.toContain('border-[#2a302a]')
    expect(screen.getByTestId('tool-result-header').className).toContain('text-[#c77]')
    expect(statusIconPath(container as HTMLElement)).toBe(XCIRCLE_PATH)
    expect(screen.queryByTestId('tool-result-denied')).toBeNull()
  })

  it('A1: facts.status done + isError true → ok colours and the CheckCircle icon (N2 wins)', () => {
    const { container } = render(<ToolResultBlock content="fine" isError={true} facts={{ status: 'done' }} />)
    const block = container.querySelector('[data-testid="tool-result-block"]')!
    expect(block.className).toContain('border-[#2a302a]')
    expect(block.className).not.toContain('border-[#302a2a]')
    expect(screen.getByTestId('tool-result-header').className).toContain('text-[#8bc]')
    expect(statusIconPath(container as HTMLElement)).toBe(CHECKCIRCLE_PATH)
    fireEvent.click(screen.getByTestId('tool-result-header'))
    expect(screen.getByTestId('tool-result-content').className).toContain('text-[#9b9]')
  })

  it('A1: facts without status → falls back to isError', () => {
    const { container } = render(<ToolResultBlock content="boom" isError={true} facts={{ file: { path: '/x', lines: 1 } }} />)
    expect(container.querySelector('[data-testid="tool-result-block"]')!.className).toContain('border-[#302a2a]')
    expect(statusIconPath(container as HTMLElement)).toBe(XCIRCLE_PATH)
  })
})

// P-B3.3 Task 10 — ToolDiffView mounted in the expanded body (spec §4.4 R5).
describe('diff view in the expanded body (P-B3.3 R5)', () => {
  const hunk = { oldStart: 1, oldLines: 3, newStart: 1, newLines: 3, lines: [' hello', '-world', '+nexen', ' three'] }
  const withHunks = { path: '/x', added: 1, removed: 1, hunks: [hunk], truncated: false }

  it('expanded + facts.diff with hunks → tool-diff inside tool-result-content, before the raw content', () => {
    render(<ToolResultBlock content="raw output" isError={false} facts={{ diff: withHunks }} />)
    expect(screen.queryByTestId('tool-diff')).toBeNull()
    fireEvent.click(screen.getByTestId('tool-result-header'))
    const body = screen.getByTestId('tool-result-content')
    const diff = screen.getByTestId('tool-diff')
    expect(body.contains(diff)).toBe(true)
    // the diff wrapper is the first child; the raw content stays a bare trailing text node
    expect(body.firstElementChild!.contains(diff)).toBe(true)
    expect(body.lastChild!.nodeType).toBe(Node.TEXT_NODE)
    expect(body.lastChild!.textContent).toBe('raw output')
    expect(body.textContent!.indexOf('nexen')).toBeLessThan(body.textContent!.indexOf('raw output'))
  })

  it('collapsed → no tool-diff', () => {
    render(<ToolResultBlock content="raw output" isError={false} facts={{ diff: withHunks }} />)
    expect(screen.queryByTestId('tool-diff')).toBeNull()
  })

  it('facts.diff.hunks: [] expanded → no tool-diff; body DOM identical to the no-facts render', () => {
    const a = render(<ToolResultBlock content="raw output" isError={false} />)
    fireEvent.click(screen.getByTestId('tool-result-header'))
    const plain = screen.getByTestId('tool-result-content').innerHTML
    a.unmount()
    render(<ToolResultBlock content="raw output" isError={false}
      facts={{ diff: { path: '/x', added: 0, removed: 0, hunks: [], truncated: false } }} />)
    fireEvent.click(screen.getByTestId('tool-result-header'))
    expect(screen.queryByTestId('tool-diff')).toBeNull()
    expect(screen.getByTestId('tool-result-content').innerHTML).toBe(plain)
  })

  it('facts without diff expanded → no tool-diff', () => {
    render(<ToolResultBlock content="raw output" isError={false} facts={{ file: { path: '/x', lines: 1 } }} />)
    fireEvent.click(screen.getByTestId('tool-result-header'))
    expect(screen.queryByTestId('tool-diff')).toBeNull()
  })
})
