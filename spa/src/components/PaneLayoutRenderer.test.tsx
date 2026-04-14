import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { PaneLayoutRenderer, isGrid4 } from './PaneLayoutRenderer'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import type { PaneLayout } from '../types/tab'

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
})

describe('isGrid4', () => {
  it('returns true for a valid 2x2 grid layout', () => {
    const layout: PaneLayout = {
      type: 'split', id: 'outer', direction: 'v',
      children: [
        { type: 'split', id: 'top', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'tl', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'tr', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
        { type: 'split', id: 'bot', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'bl', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'br', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
      ],
      sizes: [50, 50],
    }
    expect(isGrid4(layout)).toBe(true)
  })

  it('returns false for a leaf layout', () => {
    const layout: PaneLayout = { type: 'leaf', pane: { id: 'p1', content: { kind: 'dashboard' } } }
    expect(isGrid4(layout)).toBe(false)
  })

  it('returns false for a horizontal split (not vertical outer)', () => {
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'h',
      children: [
        { type: 'split', id: 'a', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'p1', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'p2', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
        { type: 'split', id: 'b', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'p3', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'p4', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
      ],
      sizes: [50, 50],
    }
    expect(isGrid4(layout)).toBe(false)
  })

  it('returns false when children are not splits', () => {
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'v',
      children: [
        { type: 'leaf', pane: { id: 'p1', content: { kind: 'dashboard' } } },
        { type: 'leaf', pane: { id: 'p2', content: { kind: 'dashboard' } } },
      ],
      sizes: [50, 50],
    }
    expect(isGrid4(layout)).toBe(false)
  })

  it('returns false when outer split has 3 children', () => {
    const makeSplit = (id: string) => ({
      type: 'split' as const, id, direction: 'h' as const,
      children: [
        { type: 'leaf' as const, pane: { id: `${id}-l`, content: { kind: 'dashboard' as const } } },
        { type: 'leaf' as const, pane: { id: `${id}-r`, content: { kind: 'dashboard' as const } } },
      ],
      sizes: [50, 50],
    })
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'v',
      children: [makeSplit('a'), makeSplit('b'), makeSplit('c')],
      sizes: [33, 33, 34],
    }
    expect(isGrid4(layout)).toBe(false)
  })

  it('validates grid-4 structure completely', () => {
    const layout: PaneLayout = {
      type: 'split', id: 'outer', direction: 'v',
      children: [
        { type: 'split', id: 'top', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'tl', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'tr', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
        { type: 'split', id: 'bot', direction: 'h', children: [
          { type: 'leaf', pane: { id: 'bl', content: { kind: 'dashboard' } } },
          { type: 'leaf', pane: { id: 'br', content: { kind: 'dashboard' } } },
        ], sizes: [50, 50] },
      ],
      sizes: [50, 50],
    }
    expect(isGrid4(layout)).toBe(true)
    // After isSplit guard, .children is accessible
    if (layout.type === 'split' && isGrid4(layout)) {
      expect(layout.children.length).toBe(2)
      expect(layout.direction).toBe('v')
      expect(layout.id).toBe('outer')
    }
  })
})

describe('PaneLayoutRenderer', () => {
  it('renders the correct component for a registered kind', () => {
    registerModule({
      id: 'dashboard',
      name: 'Dashboard',
      panes: [{
        kind: 'dashboard',
        component: ({ pane }) => <div data-testid="dashboard">Dashboard:{pane.id}</div>,
      }],
    })
    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'p1', content: { kind: 'dashboard' } },
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    expect(screen.getByTestId('dashboard')).toBeTruthy()
    expect(screen.getByTestId('dashboard').textContent).toBe('Dashboard:p1')
  })

  it('shows fallback for an unregistered kind', () => {
    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'p1', content: { kind: 'settings', scope: 'global' } },
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={false} />)
    expect(screen.getByText(/No renderer for/)).toBeTruthy()
    expect(screen.getByText(/settings/)).toBeTruthy()
  })

  it('passes isActive prop to the rendered component', () => {
    registerModule({
      id: 'history',
      name: 'History',
      panes: [{
        kind: 'history',
        component: ({ isActive }) => (
          <div data-testid="history">{isActive ? 'active' : 'inactive'}</div>
        ),
      }],
    })
    const layout: PaneLayout = {
      type: 'leaf',
      pane: { id: 'p2', content: { kind: 'history' } },
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={false} />)
    expect(screen.getByTestId('history').textContent).toBe('inactive')
  })

  it('shows fallback for empty split children', () => {
    const layout: PaneLayout = {
      type: 'split',
      id: 's1',
      direction: 'h',
      children: [],
      sizes: [],
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    expect(screen.getByText(/Empty split layout/)).toBeTruthy()
  })

  it('renders all children of a split layout', () => {
    registerModule({
      id: 'dashboard-multi',
      name: 'Dashboard',
      panes: [{ kind: 'dashboard', component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div> }],
    })
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'h',
      children: [
        { type: 'leaf', pane: { id: 'left', content: { kind: 'dashboard' } } },
        { type: 'leaf', pane: { id: 'right', content: { kind: 'dashboard' } } },
      ],
      sizes: [50, 50],
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    expect(screen.getByTestId('dash-left')).toBeTruthy()
    expect(screen.getByTestId('dash-right')).toBeTruthy()
  })

  it('renders nested splits', () => {
    registerModule({
      id: 'dashboard-nested',
      name: 'Dashboard',
      panes: [{ kind: 'dashboard', component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div> }],
    })
    const layout: PaneLayout = {
      type: 'split', id: 's1', direction: 'v',
      children: [
        { type: 'leaf', pane: { id: 'top', content: { kind: 'dashboard' } } },
        { type: 'split', id: 's2', direction: 'h',
          children: [
            { type: 'leaf', pane: { id: 'bl', content: { kind: 'dashboard' } } },
            { type: 'leaf', pane: { id: 'br', content: { kind: 'dashboard' } } },
          ],
          sizes: [50, 50] },
      ],
      sizes: [50, 50],
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    expect(screen.getByTestId('dash-top')).toBeTruthy()
    expect(screen.getByTestId('dash-bl')).toBeTruthy()
    expect(screen.getByTestId('dash-br')).toBeTruthy()
  })

  it('renders grid-4 layout with 4 pane areas', () => {
    registerModule({
      id: 'dashboard-grid4',
      name: 'Dashboard',
      panes: [{ kind: 'dashboard', component: ({ pane }) => <div data-testid={`dash-${pane.id}`}>{pane.id}</div> }],
    })
    // Grid-4: vertical split of two horizontal splits → 2x2 grid
    const layout: PaneLayout = {
      type: 'split', id: 'outer', direction: 'v',
      children: [
        {
          type: 'split', id: 'top-row', direction: 'h',
          children: [
            { type: 'leaf', pane: { id: 'tl', content: { kind: 'dashboard' } } },
            { type: 'leaf', pane: { id: 'tr', content: { kind: 'dashboard' } } },
          ],
          sizes: [50, 50],
        },
        {
          type: 'split', id: 'bot-row', direction: 'h',
          children: [
            { type: 'leaf', pane: { id: 'bl', content: { kind: 'dashboard' } } },
            { type: 'leaf', pane: { id: 'br', content: { kind: 'dashboard' } } },
          ],
          sizes: [50, 50],
        },
      ],
      sizes: [50, 50],
    }
    render(<PaneLayoutRenderer layout={layout} tabId="t1" isActive={true} />)
    // All 4 pane leaf areas must be rendered
    expect(screen.getByTestId('dash-tl')).toBeTruthy()
    expect(screen.getByTestId('dash-tr')).toBeTruthy()
    expect(screen.getByTestId('dash-bl')).toBeTruthy()
    expect(screen.getByTestId('dash-br')).toBeTruthy()
    // Verify text content of each pane
    expect(screen.getByTestId('dash-tl').textContent).toBe('tl')
    expect(screen.getByTestId('dash-tr').textContent).toBe('tr')
    expect(screen.getByTestId('dash-bl').textContent).toBe('bl')
    expect(screen.getByTestId('dash-br').textContent).toBe('br')
  })
})
