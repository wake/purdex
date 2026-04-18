import { vi } from 'vitest'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { SettingsPage, resetLastSection } from './SettingsPage'
import { registerSettingsSection, clearSettingsSectionRegistry } from '../lib/settings-section-registry'
import { AppearanceSection } from './settings/AppearanceSection'
import { TerminalSection } from './settings/TerminalSection'
import type { Pane } from '../types/tab'

const settingsPane: Pane = {
  id: 'pane-set',
  content: { kind: 'settings', scope: 'global' },
}

function renderWithLocation(initialPath: string) {
  const { hook, navigate, history } = memoryLocation({ path: initialPath, record: true })
  const result = render(
    <Router hook={hook}>
      <SettingsPage pane={settingsPane} isActive />
    </Router>,
  )
  return { ...result, navigate, hook, history: history as string[] }
}

describe('SettingsPage', () => {
  beforeEach(() => {
    resetLastSection()
    clearSettingsSectionRegistry()
    registerSettingsSection({ id: 'appearance', label: 'Appearance', order: 0, component: AppearanceSection })
    registerSettingsSection({ id: 'terminal', label: 'Terminal', order: 1, component: TerminalSection })
    registerSettingsSection({ id: 'workspace', label: 'Workspace', order: 10 })
    registerSettingsSection({ id: 'sync', label: 'Sync', order: 11 })
  })

  it('renders sidebar and default appearance section at /settings', () => {
    renderWithLocation('/settings')
    expect(screen.getAllByText('Appearance').length).toBeGreaterThan(0)
    expect(screen.getByText('Terminal')).toBeTruthy()
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
  })

  it('switches to terminal section on sidebar click', () => {
    renderWithLocation('/settings')
    fireEvent.click(screen.getByText('Terminal'))
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
  })

  it('preserves section across unmount/remount', () => {
    const first = renderWithLocation('/settings')
    fireEvent.click(screen.getByText('Terminal'))
    const desc = 'Terminal rendering and connection settings'
    expect(screen.getByText(desc)).toBeTruthy()
    first.unmount()
    renderWithLocation('/settings')
    expect(screen.getByText(desc)).toBeTruthy()
  })

  it('deep-links to section via /settings/terminal on mount', () => {
    renderWithLocation('/settings/terminal')
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
  })

  it('sidebar click updates URL to /settings/<id>', () => {
    const { history } = renderWithLocation('/settings')
    fireEvent.click(screen.getByText('Terminal'))
    expect(history[history.length - 1]).toBe('/settings/terminal')
  })

  it('invalid deep-link section falls through to default', () => {
    renderWithLocation('/settings/nonexistent-section')
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
  })

  it('self-heals URL when deep-link section is invalid (replaces to canonical)', async () => {
    const { history } = renderWithLocation('/settings/nonexistent-section')
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
    await waitFor(() => {
      expect(history[history.length - 1]).toBe('/settings/appearance')
    })
  })

  it('self-heals URL when deep-link path has extra segments', async () => {
    const { history } = renderWithLocation('/settings/sync/extra')
    // Falls through to default appearance section
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
    await waitFor(() => {
      expect(history[history.length - 1]).toBe('/settings/appearance')
    })
  })
})
