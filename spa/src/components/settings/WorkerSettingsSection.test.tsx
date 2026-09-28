import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { WorkerSettingsSection } from './WorkerSettingsSection'
import { useWorkerSettingsStore, DEFAULT_WORKER_SETTINGS } from '../../stores/useWorkerSettingsStore'
import type { WorkerTheme } from '../../lib/worker-theme/types'

// `alt` lists a second theme AHEAD of `purdex`: with a value no option matches,
// React selects the first option — so without it the fallback bug is invisible
// (the one real theme is also the first).
const h = vi.hoisted(() => ({ alt: false }))

// jsdom has no layout: render every picker row (as WorkspaceIconPicker.test does).
vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 38,
    getVirtualItems: () => Array.from({ length: count }, (_, i) => ({ index: i, key: i, start: i * 38, size: 38 })),
  }),
}))
vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0L10,10',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

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

  describe('icon (spec §8.3 / L2)', () => {
    const iconSelect = () => screen.getByRole('combobox', { name: 'Icon' }) as HTMLSelectElement

    it('offers mono / color / custom and shows the stored style', () => {
      useWorkerSettingsStore.setState({ iconStyle: 'color' })
      render(<WorkerSettingsSection />)
      expect([...iconSelect().options].map((o) => o.value)).toEqual(['mono', 'color', 'custom'])
      expect(iconSelect().value).toBe('color')
    })

    it('switching the select calls setIconStyle', () => {
      render(<WorkerSettingsSection />)
      fireEvent.change(iconSelect(), { target: { value: 'color' } })
      expect(useWorkerSettingsStore.getState().iconStyle).toBe('color')
      fireEvent.change(iconSelect(), { target: { value: 'custom' } })
      expect(useWorkerSettingsStore.getState().iconStyle).toBe('custom')
    })

    it('shows the icon picker only for custom', () => {
      const { rerender } = render(<WorkerSettingsSection />)
      expect(screen.queryByTestId('worker-icon-picker-toggle')).toBeNull()
      act(() => { useWorkerSettingsStore.setState({ iconStyle: 'color' }) })
      rerender(<WorkerSettingsSection />)
      expect(screen.queryByTestId('worker-icon-picker-toggle')).toBeNull()
      act(() => { useWorkerSettingsStore.setState({ iconStyle: 'custom' }) })
      rerender(<WorkerSettingsSection />)
      expect(screen.getByTestId('worker-icon-picker-toggle')).toBeInTheDocument()
    })

    it('picking an icon stores its name', () => {
      useWorkerSettingsStore.setState({ iconStyle: 'custom' })
      render(<WorkerSettingsSection />)
      fireEvent.click(screen.getByTestId('worker-icon-picker-toggle'))
      fireEvent.click(document.querySelector('[data-icon="House"]') as HTMLElement)
      expect(useWorkerSettingsStore.getState().customIcon).toBe('House')
    })
  })

  it('renders a theme select with the Purdex option', () => {
    render(<WorkerSettingsSection />)
    const select = screen.getByRole('combobox', { name: 'Theme' })
    expect(select).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Purdex' })).toBeInTheDocument()
  })

  it('choosing the Purdex option calls setTheme(\'purdex\')', () => {
    render(<WorkerSettingsSection />)
    const select = screen.getByRole('combobox', { name: 'Theme' })
    fireEvent.change(select, { target: { value: 'purdex' } })
    expect(useWorkerSettingsStore.getState().theme).toBe('purdex')
  })

  it('an unregistered persisted theme id shows the theme it falls back to (purdex), not whatever comes first', () => {
    h.alt = true
    useWorkerSettingsStore.setState({ theme: 'gone' })
    render(<WorkerSettingsSection />)
    const select = screen.getByRole('combobox', { name: 'Theme' }) as HTMLSelectElement
    expect(select.options[0].value).toBe('alt') // the premise: purdex is not the first option
    expect(select.value).toBe('purdex')
    expect(useWorkerSettingsStore.getState().theme).toBe('gone') // display only: the stored id is not rewritten
  })
})
