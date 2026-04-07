import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { MigrateTabsDialog } from './MigrateTabsDialog'

describe('MigrateTabsDialog', () => {
  beforeEach(() => { cleanup() })

  it('renders dialog with tab count', () => {
    render(<MigrateTabsDialog tabCount={3} workspaceName="New WS" onMigrate={vi.fn()} onSkip={vi.fn()} />)
    expect(screen.getByText(/3/)).toBeInTheDocument()
    expect(screen.getByText(/New WS/)).toBeInTheDocument()
  })

  it('calls onMigrate when user chooses to migrate', () => {
    const onMigrate = vi.fn()
    render(<MigrateTabsDialog tabCount={2} workspaceName="WS" onMigrate={onMigrate} onSkip={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: /move/i }))
    expect(onMigrate).toHaveBeenCalled()
  })

  it('calls onSkip when user chooses not to migrate', () => {
    const onSkip = vi.fn()
    render(<MigrateTabsDialog tabCount={2} workspaceName="WS" onMigrate={vi.fn()} onSkip={onSkip} />)
    fireEvent.click(screen.getByRole('button', { name: /skip/i }))
    expect(onSkip).toHaveBeenCalled()
  })
})
