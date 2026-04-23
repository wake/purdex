import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

const registerSpy = vi.hoisted(() => vi.fn())
const notifySpy = vi.hoisted(() => vi.fn())
const setLocationMock = vi.hoisted(() => vi.fn())

vi.mock('../../lib/storage/sync', () => ({
  syncManager: {
    register: registerSpy,
    notify: notifySpy,
    destroy: vi.fn(),
  },
  createSyncManager: vi.fn(),
}))

vi.mock('wouter', () => ({
  useLocation: () => ['/settings/module-config', setLocationMock],
}))

import { ModulesSwitchboardSection } from './ModulesSwitchboardSection'
import {
  clearModuleRegistry,
  registerModule,
} from '../../lib/module-registry'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { clearContributions } from '../../lib/settings-contribution-registry'
import {
  dispatchSettingsContributions,
  resetSettingsContributionsForHmr,
} from '../../lib/dispatch-settings-contributions'

const FakeComponent = () => null

function resetAll() {
  clearModuleRegistry()
  clearContributions()
  resetSettingsContributionsForHmr()
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  setLocationMock.mockClear()
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

  it('T3-5: "Open settings" link only appears when the module has a purdex contribution', () => {
    registerModule({
      id: 'editor',
      name: 'Editor',
      disableable: true,
      settings: [
        { localId: 'workspace-home-path', scope: 'workspace', order: 0, labelKey: 'x', component: FakeComponent },
        { localId: 'editor-prefs', scope: 'purdex', order: 0, labelKey: 'x', component: FakeComponent },
      ],
    })
    registerModule({
      id: 'files',
      name: 'Files',
      disableable: true,
      // no purdex contribution
    })
    dispatchSettingsContributions()

    render(<ModulesSwitchboardSection ctx={purdexCtx} />)
    const editorRow = screen.getByText('Editor').closest('[data-module-id]') as HTMLElement
    const filesRow = screen.getByText('Files').closest('[data-module-id]') as HTMLElement
    expect(editorRow.querySelector('[data-open-settings]')).toBeTruthy()
    expect(filesRow.querySelector('[data-open-settings]')).toBeNull()
  })

  it('T3-6: "Open settings" link is aria-disabled and does not navigate when module is disabled', () => {
    registerModule({
      id: 'editor',
      name: 'Editor',
      disableable: true,
      settings: [
        { localId: 'editor-prefs', scope: 'purdex', order: 0, labelKey: 'x', component: FakeComponent },
      ],
    })
    dispatchSettingsContributions()
    useModuleEnabledStore.setState({ enabled: { editor: false }, baseline: null })

    render(<ModulesSwitchboardSection ctx={purdexCtx} />)
    const link = document.querySelector('[data-open-settings]') as HTMLElement
    expect(link).toBeTruthy()
    expect(link.getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(link)
    expect(setLocationMock).not.toHaveBeenCalled()
  })

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
