// spa/src/components/ThinkingBlock.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import ThinkingBlock from './ThinkingBlock'

beforeEach(() => { cleanup() })

describe('ThinkingBlock', () => {
  it('renders collapsed by default showing Thinking header', () => {
    render(<ThinkingBlock content="Let me analyze..." />)
    expect(screen.getByText('Thinking...')).toBeInTheDocument()
    expect(screen.queryByTestId('thinking-content')).toBeNull()
  })

  it('expands on click to show thinking content', () => {
    render(<ThinkingBlock content="Let me analyze this problem." />)
    fireEvent.click(screen.getByTestId('thinking-header'))
    expect(screen.getByTestId('thinking-content')).toHaveTextContent('Let me analyze this problem.')
  })

  it('collapses again on second click', () => {
    render(<ThinkingBlock content="content" />)
    fireEvent.click(screen.getByTestId('thinking-header'))
    expect(screen.getByTestId('thinking-content')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('thinking-header'))
    expect(screen.queryByTestId('thinking-content')).toBeNull()
  })
})

// P-B2.2 task 8 (spec §4.4 R1): `streaming` shows a cursor in the header so
// it is visible while collapsed, and additionally at the end of the content
// once expanded.
describe('ThinkingBlock streaming cursor (R1)', () => {
  it('streaming + collapsed renders exactly one cursor, inside the header', () => {
    render(<ThinkingBlock content="thinking..." streaming />)
    const cursors = screen.getAllByTestId('stream-cursor')
    expect(cursors).toHaveLength(1)
    expect(cursors[0]).toHaveTextContent('▌')
    expect(cursors[0]).toHaveClass('stream-cursor')
    expect(cursors[0]).toHaveAttribute('aria-hidden', 'true')
    const header = screen.getByTestId('thinking-header')
    expect(header.contains(cursors[0])).toBe(true)
    // Right after the label, not tucked into the caret slot.
    const label = screen.getByText('Thinking...')
    expect(label.nextElementSibling).toBe(cursors[0])
  })

  it('streaming + expanded renders two cursors: header and end of content', () => {
    render(<ThinkingBlock content="thinking..." streaming />)
    fireEvent.click(screen.getByTestId('thinking-header'))
    const cursors = screen.getAllByTestId('stream-cursor')
    expect(cursors).toHaveLength(2)
    const header = screen.getByTestId('thinking-header')
    const content = screen.getByTestId('thinking-content')
    expect(header.contains(cursors[0])).toBe(true)
    expect(content.contains(cursors[1])).toBe(true)
    expect(content.lastElementChild).toBe(cursors[1])
  })

  it('without streaming renders no cursor, collapsed or expanded', () => {
    render(<ThinkingBlock content="thinking..." />)
    expect(screen.queryByTestId('stream-cursor')).toBeNull()
    fireEvent.click(screen.getByTestId('thinking-header'))
    expect(screen.queryByTestId('stream-cursor')).toBeNull()
  })
})

// P-B2.2 G5 guard: default-prop rendering must stay byte-identical while
// task 8 adds the optional `streaming` prop. Taken BEFORE any renderer change.
describe('ThinkingBlock default-prop snapshots (G5)', () => {
  it('collapsed', () => {
    const { container } = render(<ThinkingBlock content="Let me analyze this problem." />)
    expect(container.firstChild).toMatchSnapshot()
  })

  it('expanded after clicking the header', () => {
    const { container } = render(<ThinkingBlock content="Let me analyze this problem." />)
    fireEvent.click(screen.getByTestId('thinking-header'))
    expect(container.firstChild).toMatchSnapshot()
  })
})
