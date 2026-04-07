import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { WorkspaceEmptyState } from './WorkspaceEmptyState'

describe('WorkspaceEmptyState', () => {
  beforeEach(() => { cleanup() })

  it('renders empty message', () => {
    render(<WorkspaceEmptyState />)
    expect(screen.getByText(/no tabs/i)).toBeInTheDocument()
  })
})
