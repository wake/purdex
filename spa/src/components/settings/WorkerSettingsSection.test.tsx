import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { WorkerSettingsSection } from './WorkerSettingsSection'
import { useWorkerSettingsStore, DEFAULT_WORKER_SETTINGS } from '../../stores/useWorkerSettingsStore'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import type { WorkerTheme } from '../../lib/worker-theme/types'
import en from '../../locales/en.json'
import zh from '../../locales/zh-TW.json'

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
    useWorkerSettingsStore.setState({ ...DEFAULT_WORKER_SETTINGS, permissionTimeoutMin: 0 })
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

    it('changing the icon style resets the open picker, so switching back to custom does not reopen it', () => {
      useWorkerSettingsStore.setState({ iconStyle: 'custom' })
      render(<WorkerSettingsSection />)
      fireEvent.click(screen.getByTestId('worker-icon-picker-toggle'))
      expect(screen.getByTestId('worker-icon-picker-toggle')).toHaveAttribute('aria-expanded', 'true')
      fireEvent.change(iconSelect(), { target: { value: 'mono' } })
      fireEvent.change(iconSelect(), { target: { value: 'custom' } })
      expect(screen.getByTestId('worker-icon-picker-toggle')).toHaveAttribute('aria-expanded', 'false')
    })
  })

  // Permission channel spec §5.5 / plan Task 6: 「等待核准逾時」 is offered only when some ready host can honour
  // `permission_timeout_s` (capabilities.permissions.timeout) — an older daemon ignores it silently.
  describe('approval timeout (permission channel §5.5)', () => {
    const PERMS = {
      profiles: ['handoff_ask'],
      answer: { method: 'POST', path: '/api/nex/v1/executions/{id}/permissions/{request_id}' },
      timeout: { max_s: 86400 },
    }
    const timeoutSelect = () => screen.queryByTestId('worker-permission-timeout') as HTMLSelectElement | null
    const nexEntry = (capabilities: Record<string, unknown> | null, phase = 'ready') =>
      ({ info: null, capabilities, phase, error: null, fetchedAt: 0, generation: 1, fingerprint: 'f' })

    beforeEach(() => {
      useHostStore.getState().reset()
      useHostStore.getState().addHost({ id: 'h1', name: 'h1', ip: '1.2.3.4', port: 7860, token: 't' })
      useHostStore.getState().addHost({ id: 'h2', name: 'h2', ip: '1.2.3.5', port: 7860, token: 't' })
      useNexHostStore.setState({ byHost: {} })
    })

    it('is hidden while no host reports the timeout capability', () => {
      useNexHostStore.setState({
        byHost: {
          h1: nexEntry({ sandbox_profiles: ['handoff_ask', 'handoff'], permissions: { profiles: PERMS.profiles, answer: PERMS.answer } }),
          // the capability is there but the host is not ready
          h2: nexEntry({ sandbox_profiles: ['handoff_ask', 'handoff'], permissions: PERMS }, 'unavailable'),
        },
      } as never)
      render(<WorkerSettingsSection />)
      expect(timeoutSelect()).toBeNull()
      expect(screen.queryByText('Approval timeout')).toBeNull()
    })

    it('is hidden when only a host this device no longer has reports it', () => {
      useNexHostStore.setState({ byHost: { gone: nexEntry({ sandbox_profiles: ['handoff_ask'], permissions: PERMS }) } } as never)
      render(<WorkerSettingsSection />)
      expect(timeoutSelect()).toBeNull()
    })

    it('shows when one ready host reports it: label, the "new handoffs only" note, never / 5 / 15 / 30 / 60, the stored value', () => {
      useWorkerSettingsStore.setState({ permissionTimeoutMin: 30 })
      useNexHostStore.setState({ byHost: { h2: nexEntry({ sandbox_profiles: ['handoff_ask', 'handoff'], permissions: PERMS }) } } as never)
      render(<WorkerSettingsSection />)
      const select = timeoutSelect()!
      expect(select).toBeInTheDocument()
      expect(screen.getByRole('combobox', { name: 'Approval timeout' })).toBe(select)
      expect(screen.getByText(/applies to new handoffs only/)).toBeInTheDocument()
      expect([...select.options].map((o) => o.value)).toEqual(['0', '5', '15', '30', '60'])
      expect([...select.options].map((o) => o.textContent)).toEqual(['Never', '5 minutes', '15 minutes', '30 minutes', '60 minutes'])
      expect(select.value).toBe('30')
    })

    it('choosing a value writes the store as a number', () => {
      useNexHostStore.setState({ byHost: { h1: nexEntry({ sandbox_profiles: ['handoff_ask'], permissions: PERMS }) } } as never)
      render(<WorkerSettingsSection />)
      fireEvent.change(timeoutSelect()!, { target: { value: '15' } })
      expect(useWorkerSettingsStore.getState().permissionTimeoutMin).toBe(15)
      fireEvent.change(timeoutSelect()!, { target: { value: '0' } })
      expect(useWorkerSettingsStore.getState().permissionTimeoutMin).toBe(0)
    })

    it('has its copy in both locales (zh-TW: 等待核准逾時 / 只影響之後的交接 / 不逾時 / N 分鐘)', () => {
      const keys = ['worker.permission_timeout.label', 'worker.permission_timeout.desc', 'worker.permission_timeout.never', 'worker.permission_timeout.minutes']
      for (const loc of [en, zh] as Array<Record<string, string>>) for (const k of keys) expect(loc[k], k).toBeTruthy()
      const z = zh as Record<string, string>
      expect(z['worker.permission_timeout.label']).toBe('等待核准逾時')
      expect(z['worker.permission_timeout.desc']).toContain('只影響之後的交接')
      expect(z['worker.permission_timeout.never']).toBe('不逾時')
      expect(z['worker.permission_timeout.minutes']).toBe('{{n}} 分鐘')
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
