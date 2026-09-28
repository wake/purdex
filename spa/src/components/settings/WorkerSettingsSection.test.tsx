import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { WorkerSettingsSection } from './WorkerSettingsSection'
import { useWorkerSettingsStore, DEFAULT_WORKER_SETTINGS } from '../../stores/useWorkerSettingsStore'
import type { WorkerTheme } from '../../lib/worker-theme/types'

// `alt` lists a second theme AHEAD of `purdex`: with a value no option matches,
// React selects the first option — so without it the fallback bug is invisible
// (the one real theme is also the first).
const h = vi.hoisted(() => ({ alt: false }))

vi.mock('../../lib/worker-theme/registry', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/worker-theme/registry')>()
  return {
    ...actual,
    listWorkerThemes: (): WorkerTheme[] => {
      const real = actual.listWorkerThemes()
      if (!h.alt) return real
      const purdex = actual.getWorkerTheme('purdex')
      return [{ ...purdex, id: 'alt', labelKey: 'worker.theme.purdex' }, ...real]
    },
  }
})

describe('WorkerSettingsSection', () => {
  beforeEach(() => {
    useWorkerSettingsStore.setState({ ...DEFAULT_WORKER_SETTINGS })
  })

  afterEach(() => {
    h.alt = false
  })

  it('renders a theme select with the Purdex option', () => {
    render(<WorkerSettingsSection />)
    const select = screen.getByRole('combobox')
    expect(select).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Purdex' })).toBeInTheDocument()
  })

  it('choosing the Purdex option calls setTheme(\'purdex\')', () => {
    render(<WorkerSettingsSection />)
    const select = screen.getByRole('combobox')
    fireEvent.change(select, { target: { value: 'purdex' } })
    expect(useWorkerSettingsStore.getState().theme).toBe('purdex')
  })

  it('an unregistered persisted theme id shows the theme it falls back to (purdex), not whatever comes first', () => {
    h.alt = true
    useWorkerSettingsStore.setState({ theme: 'gone' })
    render(<WorkerSettingsSection />)
    const select = screen.getByRole('combobox') as HTMLSelectElement
    expect(select.options[0].value).toBe('alt') // the premise: purdex is not the first option
    expect(select.value).toBe('purdex')
    expect(useWorkerSettingsStore.getState().theme).toBe('gone') // display only: the stored id is not rewritten
  })
})
