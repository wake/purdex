import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { PaneLayoutRenderer } from './PaneLayoutRenderer'
import { registerPaneRenderer, clearPaneRegistry } from '../lib/pane-registry'
import type { PaneLayout } from '../types/tab'

beforeEach(() => {
  cleanup()
  clearPaneRegistry()
})

describe('PaneLayoutRenderer', () => {
  it('renders the correct component for a registered kind', () => {
    registerPaneRenderer('dashboard', {
      component: ({ pane }) => <div data-testid="dashboard">Dashboard:{pane.id}</div>,
    })
    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'p1', content: { kind: 'dashboard' } },
    }
    render(<PaneLayoutRenderer layout={layout} isActive={true} />)
    expect(screen.getByTestId('dashboard')).toBeTruthy()
    expect(screen.getByTestId('dashboard').textContent).toBe('Dashboard:p1')
  })

  it('shows fallback for an unregistered kind', () => {
    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'p1', content: { kind: 'settings', scope: 'global' } },
    }
    render(<PaneLayoutRenderer layout={layout} isActive={false} />)
    expect(screen.getByText(/No renderer for/)).toBeTruthy()
    expect(screen.getByText(/settings/)).toBeTruthy()
  })

  it('passes isActive prop to the rendered component', () => {
    registerPaneRenderer('history', {
      component: ({ isActive }) => (
        <div data-testid="history">{isActive ? 'active' : 'inactive'}</div>
      ),
    })
    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'p2', content: { kind: 'history' } },
    }
    render(<PaneLayoutRenderer layout={layout} isActive={false} />)
    expect(screen.getByTestId('history').textContent).toBe('inactive')
  })

  it('renders first child of a split layout', () => {
    registerPaneRenderer('dashboard', {
      component: ({ pane }) => <div data-testid="dash">{pane.id}</div>,
    })
    const layout: PaneLayout = {
      type: 'split',
      id: 's1',
      direction: 'h',
      children: [
        { type: 'leaf', pane: { id: 'left', content: { kind: 'dashboard' } } },
        { type: 'leaf', pane: { id: 'right', content: { kind: 'dashboard' } } },
      ],
      sizes: [50, 50],
    }
    render(<PaneLayoutRenderer layout={layout} isActive={true} />)
    expect(screen.getByTestId('dash').textContent).toBe('left')
  })
})
