import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'

const cache = vi.hoisted(() => ({ loaded: false, paths: { Rocket: 'M1,1L2,2' } as Record<string, string> }))
vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  isWeightLoaded: () => cache.loaded,
  prefetchWeight: vi.fn(async () => { cache.loaded = true }),
  getIconPath: (name: string) => (cache.loaded ? cache.paths[name] ?? null : null),
}))

import { CommandIconView } from './CommandIconView'
import { AGENT_ICON_VALUES, DEFAULT_COMMAND_ICON, agentIconComponent } from '../../lib/command-icons'

beforeEach(() => { cache.loaded = false })

describe('command-icons', () => {
  it('lists agent values in the fixed order and resolves a component for each', () => {
    expect(AGENT_ICON_VALUES).toEqual(['cc-bot', 'cc-star', 'openai', 'codex', 'opencode'])
    for (const v of AGENT_ICON_VALUES) expect(agentIconComponent(v)).toBeTypeOf('function')
    expect(new Set(AGENT_ICON_VALUES.map(agentIconComponent)).size).toBe(5)
    expect(DEFAULT_COMMAND_ICON).toEqual({ kind: 'phosphor', value: 'Terminal' })
  })
})

describe('CommandIconView', () => {
  it.each(['cc-bot', 'cc-star', 'openai', 'codex', 'opencode'] as const)('renders agent icon %s regardless of global variant settings', (value) => {
    render(<CommandIconView icon={{ kind: 'agent', value }} />)
    const el = screen.getByTestId('command-icon')
    expect(el).toHaveAttribute('data-kind', 'agent')
    expect(el).toHaveAttribute('data-value', value)
    expect(el.querySelector('svg')).not.toBeNull()
    expect(el).not.toHaveAttribute('data-fallback')
  })

  it('shows the Terminal fallback while path data loads, then the stored icon', async () => {
    render(<CommandIconView icon={{ kind: 'phosphor', value: 'Rocket' }} />)
    expect(screen.getByTestId('command-icon')).toHaveAttribute('data-fallback', 'true')
    await waitFor(() => expect(screen.getByTestId('command-icon')).not.toHaveAttribute('data-fallback'))
    expect(screen.getByTestId('command-icon').querySelector('path')?.getAttribute('d')).toBe('M1,1L2,2')
  })

  it('an unknown phosphor name stays on the fallback', async () => {
    cache.loaded = true
    render(<CommandIconView icon={{ kind: 'phosphor', value: 'NoSuchIcon' }} />)
    expect(screen.getByTestId('command-icon')).toHaveAttribute('data-fallback', 'true')
  })
})
