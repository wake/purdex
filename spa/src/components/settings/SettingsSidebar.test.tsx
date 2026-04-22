import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SettingsSidebar } from './SettingsSidebar'
import { registerSettingsSection, clearSettingsSectionRegistry } from '../../lib/settings-section-registry'
import {
  clearContributions,
  registerSettingsContribution,
} from '../../lib/settings-contribution-registry'
import { dispatchSettingsContributions } from '../../lib/dispatch-settings-contributions'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'

const FakeComponent = () => null

describe('SettingsSidebar', () => {
  beforeEach(() => {
    clearSettingsSectionRegistry()
    clearContributions()
    registerSettingsSection({ id: 'appearance', label: 'Appearance', order: 0, component: FakeComponent })
    registerSettingsSection({ id: 'terminal', label: 'Terminal', order: 1, component: FakeComponent })
    registerSettingsSection({ id: 'workspace', label: 'Workspace', order: 10 })
    registerSettingsSection({ id: 'sync', label: 'Sync', order: 11 })
    dispatchSettingsContributions([])
  })

  it('renders all section items', () => {
    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    expect(screen.getByText('Appearance')).toBeTruthy()
    expect(screen.getByText('Terminal')).toBeTruthy()
    expect(screen.getByText('Workspace')).toBeTruthy()
    expect(screen.getByText('Sync')).toBeTruthy()
  })

  it('highlights active section', () => {
    render(<SettingsSidebar activeSection="terminal" onSelectSection={vi.fn()} />)
    const terminalItem = screen.getByText('Terminal').closest('[data-section]')
    expect(terminalItem?.getAttribute('data-active')).toBe('true')
  })

  it('calls onSelectSection for enabled items', () => {
    const onSelect = vi.fn()
    render(<SettingsSidebar activeSection="appearance" onSelectSection={onSelect} />)
    fireEvent.click(screen.getByText('Terminal'))
    expect(onSelect).toHaveBeenCalledWith('terminal')
  })

  it('does not call onSelectSection for reserved items', () => {
    const onSelect = vi.fn()
    render(<SettingsSidebar activeSection="appearance" onSelectSection={onSelect} />)
    fireEvent.click(screen.getByText('Workspace'))
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('shows coming soon badge on reserved items', () => {
    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    const badges = screen.getAllByText('coming soon')
    expect(badges.length).toBe(2)
  })
})

// ---------------------------------------------------------------------------
// F7 — honor `disabled(ctx)` + `disabledReasonKey` on active contributions.
// Reserved (legacy `registerSettingsSection` without a component) is still
// its own separate bucket below the divider; disabled-by-ctx rows render in
// their natural order, styled disabled, with the reason key shown as title
// tooltip (falls back to the key if i18n has no entry).
// ---------------------------------------------------------------------------

describe('SettingsSidebar (F7: disabled-by-ctx)', () => {
  const FakeComp = () => null

  beforeEach(() => {
    clearSettingsSectionRegistry()
    clearContributions()
    // A plain enabled row so we can verify ordering + interaction.
    registerSettingsSection({ id: 'appearance', label: 'Appearance', order: 0, component: FakeComp })
    dispatchSettingsContributions([])
  })

  it('renders a disabled-by-ctx row but does not invoke onSelectSection on click', () => {
    registerSettingsContribution({
      moduleId: 'mod',
      id: 'mod.gated',
      localId: 'gated',
      scope: 'purdex',
      order: 5,
      labelKey: 'GatedLabel',
      component: FakeComp,
      disabled: (_ctx: SettingsContextFor<'purdex'>) => true,
    })

    const onSelect = vi.fn()
    render(<SettingsSidebar activeSection="appearance" onSelectSection={onSelect} />)
    // The row is rendered (visible in the sidebar).
    const row = screen.getByText('GatedLabel').closest('[data-section]')
    expect(row).toBeTruthy()
    // Click is a no-op.
    fireEvent.click(screen.getByText('GatedLabel'))
    expect(onSelect).not.toHaveBeenCalled()
    // `data-disabled-ctx="true"` so downstream code / tests can distinguish
    // it from a reserved row.
    expect(row!.getAttribute('data-disabled-ctx')).toBe('true')
  })

  it('surfaces disabledReasonKey via title tooltip when provided', () => {
    registerSettingsContribution({
      moduleId: 'mod',
      id: 'mod.gated',
      localId: 'gated',
      scope: 'purdex',
      order: 5,
      labelKey: 'GatedLabel',
      component: FakeComp,
      disabled: () => true,
      disabledReasonKey: 'mod.gated.reason',
    })

    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    const row = screen.getByText('GatedLabel').closest('[data-section]') as HTMLElement
    // The i18n key itself is used as the fallback when the bundle has no
    // translation — `t(key)` returns `key` in that case.
    expect(row.getAttribute('title')).toBe('mod.gated.reason')
  })

  it('does not mark a contribution disabled when disabled(ctx) returns false', () => {
    registerSettingsContribution({
      moduleId: 'mod',
      id: 'mod.alive',
      localId: 'alive',
      scope: 'purdex',
      order: 5,
      labelKey: 'AliveLabel',
      component: FakeComp,
      disabled: () => false,
    })

    const onSelect = vi.fn()
    render(<SettingsSidebar activeSection="appearance" onSelectSection={onSelect} />)
    const row = screen.getByText('AliveLabel').closest('[data-section]') as HTMLElement
    expect(row.getAttribute('data-disabled-ctx')).toBeNull()
    fireEvent.click(screen.getByText('AliveLabel'))
    expect(onSelect).toHaveBeenCalledWith('alive')
  })

  it('omitted `disabled` defaults to enabled', () => {
    registerSettingsContribution({
      moduleId: 'mod',
      id: 'mod.plain',
      localId: 'plain',
      scope: 'purdex',
      order: 5,
      labelKey: 'PlainLabel',
      component: FakeComp,
    })

    const onSelect = vi.fn()
    render(<SettingsSidebar activeSection="appearance" onSelectSection={onSelect} />)
    fireEvent.click(screen.getByText('PlainLabel'))
    expect(onSelect).toHaveBeenCalledWith('plain')
  })

  it('disabled-by-ctx rows appear in natural order (not grouped with reserved)', () => {
    // reserved at order=10
    registerSettingsSection({ id: 'reserved', label: 'Reserved', order: 10 })
    // disabled-by-ctx active contribution at order=5 — should sort BEFORE reserved,
    // since ordering is by `order`, not by enabled/disabled bucket.
    registerSettingsContribution({
      moduleId: 'mod',
      id: 'mod.gated',
      localId: 'gated',
      scope: 'purdex',
      order: 5,
      labelKey: 'GatedLabel',
      component: FakeComp,
      disabled: () => true,
    })

    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    const labels = Array.from(document.querySelectorAll('[data-section]')).map((el) =>
      el.querySelector('span')?.textContent?.trim(),
    )
    // order: appearance(0) → gated(5) → reserved(10)
    expect(labels).toEqual(['Appearance', 'GatedLabel', 'Reserved'])
  })
})
