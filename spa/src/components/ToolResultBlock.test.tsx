// spa/src/components/ToolResultBlock.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
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
