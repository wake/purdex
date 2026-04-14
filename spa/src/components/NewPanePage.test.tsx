import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { NewPanePage } from './NewPanePage'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

beforeEach(() => {
  clearModuleRegistry()
})

describe('NewPanePage', () => {
  it('renders a list of available simple pane modules', () => {
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => null }] })
    registerModule({ id: 'history', name: 'History', panes: [{ kind: 'history', component: () => null }] })

    render(<NewPanePage onSelect={vi.fn()} />)
    expect(screen.getByText('Dashboard')).toBeTruthy()
    expect(screen.getByText('History')).toBeTruthy()
  })

  it('calls onSelect with correct content when module is clicked', () => {
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => null }] })

    const onSelect = vi.fn()
    render(<NewPanePage onSelect={onSelect} />)
    fireEvent.click(screen.getByText('Dashboard'))
    expect(onSelect).toHaveBeenCalledWith({ kind: 'dashboard' })
  })

  it('skips modules without pane', () => {
    registerModule({ id: 'files', name: 'Files' })
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => null }] })

    render(<NewPanePage onSelect={vi.fn()} />)
    expect(screen.queryByText('Files')).toBeNull()
    expect(screen.getByText('Dashboard')).toBeTruthy()
  })

  it('skips modules with complex content kinds', () => {
    registerModule({ id: 'session', name: 'Session', panes: [{ kind: 'tmux-session', component: () => null }] })
    registerModule({ id: 'browser', name: 'Browser', panes: [{ kind: 'browser', component: () => null }] })
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => null }] })

    render(<NewPanePage onSelect={vi.fn()} />)
    expect(screen.queryByText('Session')).toBeNull()
    expect(screen.queryByText('Browser')).toBeNull()
    expect(screen.getByText('Dashboard')).toBeTruthy()
  })
})
