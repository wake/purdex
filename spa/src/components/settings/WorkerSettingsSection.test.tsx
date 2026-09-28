import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { WorkerSettingsSection } from './WorkerSettingsSection'
import { useWorkerSettingsStore, DEFAULT_WORKER_SETTINGS } from '../../stores/useWorkerSettingsStore'

describe('WorkerSettingsSection', () => {
  beforeEach(() => {
    useWorkerSettingsStore.setState({ ...DEFAULT_WORKER_SETTINGS })
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
})
