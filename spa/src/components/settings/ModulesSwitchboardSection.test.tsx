import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

const registerSpy = vi.hoisted(() => vi.fn())
const notifySpy = vi.hoisted(() => vi.fn())

vi.mock('../../lib/storage/sync', () => ({
  syncManager: {
    register: registerSpy,
    notify: notifySpy,
    destroy: vi.fn(),
  },
  createSyncManager: vi.fn(),
}))

import { ModulesSwitchboardSection } from './ModulesSwitchboardSection'
import { clearModuleRegistry, registerModule } from '../../lib/module-registry'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'

function resetAll() {
  clearModuleRegistry()
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
}

const purdexCtx = { scope: 'purdex' as const }

beforeEach(resetAll)

describe('ModulesSwitchboardSection', () => {
  it('T3-1: lists only disableable:true modules (core modules are hidden)', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    registerModule({ id: 'sessions', name: 'Sessions' /* not disableable */ })

    render(<ModulesSwitchboardSection ctx={purdexCtx} />)
    expect(screen.getByText('Editor')).toBeTruthy()
    expect(screen.queryByText('Sessions')).toBeNull()
  })

  it('T3-2: renders all four disableable modules when registered', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    registerModule({ id: 'files', name: 'Files', disableable: true })
    registerModule({ id: 'browser', name: 'Browser', disableable: true })
    registerModule({ id: 'memory-monitor', name: 'Memory Monitor', disableable: true })

    render(<ModulesSwitchboardSection ctx={purdexCtx} />)
    expect(screen.getByText('Editor')).toBeTruthy()
    expect(screen.getByText('Files')).toBeTruthy()
    expect(screen.getByText('Browser')).toBeTruthy()
    expect(screen.getByText('Memory Monitor')).toBeTruthy()
  })

  it('T3-3: clicking a toggle on an enabled module calls setEnabled(id, false)', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })

    render(<ModulesSwitchboardSection ctx={purdexCtx} />)
    const toggle = screen.getByRole('switch', { name: /editor/i })
    expect(toggle.getAttribute('aria-checked')).toBe('true')
    fireEvent.click(toggle)
    expect(useModuleEnabledStore.getState().isEnabled('editor')).toBe(false)
  })

  it('T3-4: clicking a toggle on a disabled module calls setEnabled(id, true)', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    useModuleEnabledStore.setState({ enabled: { editor: false }, baseline: null })

    render(<ModulesSwitchboardSection ctx={purdexCtx} />)
    const toggle = screen.getByRole('switch', { name: /editor/i })
    expect(toggle.getAttribute('aria-checked')).toBe('false')
    fireEvent.click(toggle)
    expect(useModuleEnabledStore.getState().isEnabled('editor')).toBe(true)
  })

  // T3-5 / T3-6 removed in AR-2 fixup — the "Open settings" CTA they
  // exercised was 100% dead code for the four initial opt-in modules (none
  // of them declare a purdex-scope contribution). The CTA + tests will
  // return in PR 2 once Editor migrates Buffers to a real `settings: []`.

  it('T3-7: reload-required banner is shown when hasPendingChanges is true', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    useModuleEnabledStore.getState().captureBaseline({ editor: true })
    useModuleEnabledStore.getState().setEnabled('editor', false)

    render(<ModulesSwitchboardSection ctx={purdexCtx} />)
    expect(screen.getByTestId('reload-required-banner')).toBeTruthy()
  })

  it('T3-8: reload-required banner is hidden when there are no pending changes', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    useModuleEnabledStore.getState().captureBaseline({ editor: true })
    // No user toggle — no diff.

    render(<ModulesSwitchboardSection ctx={purdexCtx} />)
    expect(screen.queryByTestId('reload-required-banner')).toBeNull()
  })

  it('T3-9: descriptionKey text renders in the module row', () => {
    registerModule({
      id: 'editor',
      name: 'Editor',
      disableable: true,
      descriptionKey: 'modules.editor.description',
    })

    render(<ModulesSwitchboardSection ctx={purdexCtx} />)
    // Locate the description div inside the editor row. `t()` may return a
    // real translation (en/zh) or the raw key if the active locale has no
    // translation — both are acceptable; the assertion only requires the
    // descriptionKey reaches the DOM.
    const row = document.querySelector('[data-module-id="editor"]') as HTMLElement
    expect(row).toBeTruthy()
    const desc = row.querySelector('.text-xs.text-text-secondary')
    expect(desc).toBeTruthy()
    expect((desc!.textContent ?? '').length).toBeGreaterThan(0)
  })
})
