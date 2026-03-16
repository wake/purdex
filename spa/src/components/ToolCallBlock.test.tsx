// spa/src/components/ToolCallBlock.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import ToolCallBlock from './ToolCallBlock'

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
})
