import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { SettingsPage } from './SettingsPage'
import { registerSettingsSection, clearSettingsSectionRegistry } from '../lib/settings-section-registry'
import { AppearanceSection } from './settings/AppearanceSection'
import { TerminalSection } from './settings/TerminalSection'
import type { Pane } from '../types/tab'

const settingsPane: Pane = {
  id: 'pane-set',
  content: { kind: 'settings', scope: 'global' },
}

function createWrapper(mem: ReturnType<typeof memoryLocation>) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return createElement(Router, { hook: mem.hook, children })
  }
}

describe('SettingsPage', () => {
  beforeEach(() => {
    clearSettingsSectionRegistry()
    registerSettingsSection({ id: 'appearance', label: 'Appearance', order: 0, component: AppearanceSection })
    registerSettingsSection({ id: 'terminal', label: 'Terminal', order: 1, component: TerminalSection })
    registerSettingsSection({ id: 'workspace', label: 'Workspace', order: 10 })
    registerSettingsSection({ id: 'sync', label: 'Sync', order: 11 })
  })

  it('renders sidebar and default appearance section', () => {
    const mem = memoryLocation({ path: '/settings', record: true })
    render(<SettingsPage pane={settingsPane} isActive />, { wrapper: createWrapper(mem) })
    expect(screen.getAllByText('Appearance').length).toBeGreaterThan(0)
    expect(screen.getByText('Terminal')).toBeTruthy()
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
  })

  it('switches to terminal section on sidebar click', () => {
    const mem = memoryLocation({ path: '/settings', record: true })
    render(<SettingsPage pane={settingsPane} isActive />, { wrapper: createWrapper(mem) })
    fireEvent.click(screen.getByText('Terminal'))
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
  })

  it('reads section from URL', () => {
    const mem = memoryLocation({ path: '/settings/terminal', record: true })
    render(<SettingsPage pane={settingsPane} isActive />, { wrapper: createWrapper(mem) })
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
  })

  it('updates URL on section switch', () => {
    const mem = memoryLocation({ path: '/settings', record: true })
    render(<SettingsPage pane={settingsPane} isActive />, { wrapper: createWrapper(mem) })
    fireEvent.click(screen.getByText('Terminal'))
    expect(mem.history).toContain('/settings/terminal')
  })

  it('falls back to appearance for invalid section', () => {
    const mem = memoryLocation({ path: '/settings/nonexistent', record: true })
    render(<SettingsPage pane={settingsPane} isActive />, { wrapper: createWrapper(mem) })
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
  })
})
