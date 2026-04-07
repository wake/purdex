import { vi } from 'vitest'

vi.mock('../features/workspace/generated/icon-loader', () => ({
  ALL_ICON_NAMES: ['House', 'Star'],
  iconLoaders: {
    House: () => new Promise(() => {}),
    Star: () => new Promise(() => {}),
  },
}))

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SettingsPage, resetLastSection } from './SettingsPage'
import { registerSettingsSection, clearSettingsSectionRegistry } from '../lib/settings-section-registry'
import { AppearanceSection } from './settings/AppearanceSection'
import { TerminalSection } from './settings/TerminalSection'
import type { Pane } from '../types/tab'

const settingsPane: Pane = {
  id: 'pane-set',
  content: { kind: 'settings', scope: 'global' },
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

  it('renders sidebar and default appearance section', () => {
    render(<SettingsPage pane={settingsPane} isActive />)
    expect(screen.getAllByText('Appearance').length).toBeGreaterThan(0)
    expect(screen.getByText('Terminal')).toBeTruthy()
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
  })

  it('switches to terminal section on sidebar click', () => {
    render(<SettingsPage pane={settingsPane} isActive />)
    fireEvent.click(screen.getByText('Terminal'))
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
  })

  it('preserves section across unmount/remount', () => {
    const { unmount } = render(<SettingsPage pane={settingsPane} isActive />)
    fireEvent.click(screen.getByText('Terminal'))
    const desc = 'Terminal rendering and connection settings'
    expect(screen.getByText(desc)).toBeTruthy()
    unmount()
    // Remount — should restore last section via module-level cache
    render(<SettingsPage pane={settingsPane} isActive />)
    expect(screen.getByText(desc)).toBeTruthy()
  })
})
