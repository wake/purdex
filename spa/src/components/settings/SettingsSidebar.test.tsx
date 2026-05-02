import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import type { ReactNode } from 'react'

// Mock <PuzzlePiece> so we can assert on the `weight` and `className`
// props SettingsSidebar passes to it. The real Phosphor SVG render
// is expensive and opaque to weight / className assertions;
// intercepting the component lets us hold the rotate-bold contract
// on the prop boundary instead of the rendered output. Spec §4.6.
vi.mock('@phosphor-icons/react', () => {
  type StubProps = {
    weight?: string
    className?: string
    'aria-hidden'?: boolean | 'true' | 'false'
    children?: ReactNode
  }
  const PuzzlePiece = ({ weight, className, ...rest }: StubProps) => (
    <span
      data-testid="puzzle"
      data-weight={weight}
      className={className}
      {...rest}
    />
  )
  return { PuzzlePiece }
})

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
    registerSettingsSection({ id: 'sync', label: 'Sync', order: 11, component: FakeComponent })
    dispatchSettingsContributions([])
  })

  it('renders all section items', () => {
    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    expect(screen.getByText('Appearance')).toBeTruthy()
    expect(screen.getByText('Terminal')).toBeTruthy()
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

  // PR-3 removed the reserved (coming-soon) rows entirely. These used to be
  // registered via `registerSettingsSection({ id, label, order })` without a
  // component — the function now throws on that shape. The
  // "reserved items render coming-soon badge" and "reserved row click is a
  // no-op" assertions are preserved as a disabled-by-ctx pattern test
  // below (see F7 describe block).
})

// ---------------------------------------------------------------------------
// F7 — honor `disabled(ctx)` + `disabledReasonKey` on active contributions.
// After PR-3 there is no reserved / coming-soon bucket — disabled-by-ctx
// rows are the only non-clickable kind, rendered in their natural order.
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
    // it from an enabled row.
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

  it('module-owned puzzle icon is bold + not rotated', () => {
    // Spec §4.6 — Settings sidebar puzzle icon must be `weight="bold"`
    // and have no `rotate-[30deg]` class. Asserted at the Phosphor prop
    // boundary via the module-level mock above.
    registerSettingsContribution({
      moduleId: 'mod',
      id: 'mod.alive',
      localId: 'alive',
      scope: 'purdex',
      order: 5,
      labelKey: 'AliveLabel',
      component: FakeComp,
    })

    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    const puzzles = screen.getAllByTestId('puzzle')
    // Exactly one module-owned row exists in this fixture (`alive`).
    expect(puzzles).toHaveLength(1)
    expect(puzzles[0].getAttribute('data-weight')).toBe('bold')
    expect(puzzles[0].className).not.toContain('rotate-')
  })

  it('built-in row does not render a puzzle icon', () => {
    // Only the dispatched legacy `appearance` row exists here — no
    // module-owned contribution, so no puzzle icon should render.
    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    expect(screen.queryByTestId('puzzle')).toBeNull()
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

  it('disabled-by-ctx rows appear in natural order (sort by `order` ascending)', () => {
    // Two active rows with a disabled-by-ctx row in between. Ordering is by
    // `order`, not by enabled/disabled — so gated sits between the two
    // enabled rows even though it is not clickable. The existing
    // `appearance` row was registered + flushed by the describe-level
    // beforeEach; we only push the two additional rows here via the direct
    // new-registry path (avoids a second dispatch wiping the flushed
    // `appearance` entry).
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
    registerSettingsContribution({
      moduleId: 'mod',
      id: 'mod.later',
      localId: 'later',
      scope: 'purdex',
      order: 10,
      labelKey: 'Later',
      component: FakeComp,
    })

    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    const labels = Array.from(document.querySelectorAll('[data-section]')).map((el) =>
      el.querySelector('span')?.textContent?.trim(),
    )
    // order: appearance(0) → gated(5) → later(10)
    expect(labels).toEqual(['Appearance', 'GatedLabel', 'Later'])
  })
})
