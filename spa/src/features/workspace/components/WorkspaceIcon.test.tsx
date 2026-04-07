import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'

// Mock icon-loader so lazy imports never resolve — Suspense fallback is stable
vi.mock('../generated/icon-loader', () => ({
  iconLoaders: {
    Rocket: () => new Promise(() => {}), // never resolves
  },
}))

import { WorkspaceIcon } from './WorkspaceIcon'

describe('WorkspaceIcon', () => {
  beforeEach(() => { cleanup() })

  it('shows first char of name when icon is undefined', () => {
    render(<WorkspaceIcon icon={undefined} name="Default" size={18} />)
    expect(screen.getByText('D')).toBeInTheDocument()
  })

  it('shows single-char icon as text (legacy)', () => {
    render(<WorkspaceIcon icon="X" name="Test" size={18} />)
    expect(screen.getByText('X')).toBeInTheDocument()
  })

  it('shows emoji icon as text (legacy)', () => {
    render(<WorkspaceIcon icon="🚀" name="Test" size={18} />)
    expect(screen.getByText('🚀')).toBeInTheDocument()
  })

  it('shows first char as Suspense fallback for Phosphor icon name', () => {
    render(<WorkspaceIcon icon="Rocket" name="Test" size={18} />)
    expect(screen.getByText('T')).toBeInTheDocument()
  })

  it('shows fallback when icon name has no loader', () => {
    render(<WorkspaceIcon icon="NonExistentIcon" name="Foo" size={18} />)
    expect(screen.getByText('F')).toBeInTheDocument()
  })
})
