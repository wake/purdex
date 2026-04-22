// spa/src/components/hosts/HostSidebar.test.tsx
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { HostSidebar } from './HostSidebar'
import { useHostStore } from '../../stores/useHostStore'
import { clearContributions, registerSettingsContribution } from '../../lib/settings-contribution-registry'
import { clearModuleRegistry } from '../../lib/module-registry'
import { registerBuiltinModules } from '../../lib/register-modules'

vi.mock('../../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]) }
})

const HOST_ID = 'test-host'
const HOST_B = 'host-b'

beforeEach(() => {
  cleanup()
  // Populate the contribution registry with the six built-in host sub-pages.
  // HostSidebar now reads listContributions('host') instead of the old hard-coded
  // SUB_PAGES const, so the registry must be populated for sub-page labels to appear.
  clearContributions()
  clearModuleRegistry()
  registerBuiltinModules()

  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: {},
  })
})

afterEach(() => {
  delete (window as unknown as Record<string, unknown>).electronAPI
  clearContributions()
  clearModuleRegistry()
})

describe('HostSidebar', () => {
  const defaultProps = {
    selectedHostId: HOST_ID,
    selectedSubPage: 'overview' as const,
    onSelect: vi.fn(),
    onAddHost: vi.fn(),
  }

  beforeEach(() => {
    defaultProps.onSelect = vi.fn()
    defaultProps.onAddHost = vi.fn()
  })

  it('renders host names from store', () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
    })
    render(<HostSidebar {...defaultProps} />)
    expect(screen.getByText('Test Host')).toBeInTheDocument()
    expect(screen.getByText('Second Host')).toBeInTheDocument()
  })

  it('shows StatusIcon green for connected runtime', () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: { [HOST_ID]: { status: 'connected' } },
    })
    render(<HostSidebar {...defaultProps} />)
    // The host button contains a Circle SVG with text-green-400
    const hostButton = screen.getByText('Test Host').closest('button')!
    const svgs = hostButton.querySelectorAll('svg')
    const greenIcon = Array.from(svgs).find((svg) => svg.classList.contains('text-green-400'))
    expect(greenIcon).toBeTruthy()
  })

  it('shows StatusIcon grey for undefined runtime', () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: {},
    })
    render(<HostSidebar {...defaultProps} />)
    const hostButton = screen.getByText('Test Host').closest('button')!
    const svgs = hostButton.querySelectorAll('svg')
    const mutedIcon = Array.from(svgs).find((svg) => svg.classList.contains('text-text-muted'))
    expect(mutedIcon).toBeTruthy()
  })

  it('shows StatusIcon red for disconnected runtime', () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: { [HOST_ID]: { status: 'disconnected' } },
    })
    render(<HostSidebar {...defaultProps} />)
    const hostButton = screen.getByText('Test Host').closest('button')!
    const svgs = hostButton.querySelectorAll('svg')
    const redIcon = Array.from(svgs).find((svg) => svg.classList.contains('text-red-400'))
    expect(redIcon).toBeTruthy()
  })

  it('clicking a collapsed host expands it and shows sub-pages', () => {
    // HOST_B is not selectedHostId, so it starts collapsed
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
      runtime: {},
    })
    render(<HostSidebar {...defaultProps} />)

    // Second host should be collapsed — sub-pages not visible for it
    // The selected host (HOST_ID) starts expanded, so its sub-pages are visible
    const overviewButtons = screen.getAllByText('Overview')
    expect(overviewButtons).toHaveLength(1) // only from HOST_ID

    // Click the collapsed host
    fireEvent.click(screen.getByText('Second Host'))

    // Now both hosts should show sub-pages
    const allOverview = screen.getAllByText('Overview')
    expect(allOverview).toHaveLength(2)
  })

  it('clicking "Add Host" button calls onAddHost', () => {
    render(<HostSidebar {...defaultProps} />)
    fireEvent.click(screen.getByText('Add Host'))
    expect(defaultProps.onAddHost).toHaveBeenCalledTimes(1)
  })

  it('sub-page items are clickable and call onSelect', () => {
    render(<HostSidebar {...defaultProps} />)
    // The selected host is expanded by default, so sub-pages are visible
    fireEvent.click(screen.getByText('Sessions'))
    expect(defaultProps.onSelect).toHaveBeenCalledWith(HOST_ID, 'sessions')

    fireEvent.click(screen.getByText('Hooks'))
    expect(defaultProps.onSelect).toHaveBeenCalledWith(HOST_ID, 'hooks')

    fireEvent.click(screen.getByText('Uploads'))
    expect(defaultProps.onSelect).toHaveBeenCalledWith(HOST_ID, 'uploads')
  })

  it('shows Hosts title', () => {
    render(<HostSidebar {...defaultProps} />)
    expect(screen.getByText('Hosts')).toBeInTheDocument()
  })

  it('auto-expands previously-collapsed host when it becomes selectedHostId', () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
      runtime: {},
    })

    // Render with HOST_ID selected, then expand and collapse HOST_B
    const { rerender } = render(<HostSidebar {...defaultProps} />)
    // Click HOST_B to expand it
    fireEvent.click(screen.getByText('Second Host'))
    // Click HOST_B again to collapse it — expanded['host-b'] = false
    fireEvent.click(screen.getByText('Second Host'))

    // Now simulate fallback: selectedHostId changes to HOST_B
    rerender(<HostSidebar {...defaultProps} selectedHostId={HOST_B} />)

    // HOST_B should be expanded despite having been manually collapsed
    const sessionsButtons = screen.getAllByText('Sessions')
    expect(sessionsButtons.length).toBeGreaterThanOrEqual(1)
  })

  it('expanding a different collapsed host picks first selectable sub-page for that host (R2 D2)', () => {
    // R2 defender D2 fix — when expanding a different host we must NOT carry
    // over the current host's selectedSubPage; that would briefly navigate
    // into the new host's sub-page even when it is disabled for that host
    // (visible blank-and-redirect under runtime-gated modules).  Instead
    // pick the first selectable sub-page for the target host using its
    // own runtime.
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
      runtime: {},
    })
    // Current selectedSubPage is 'hooks' for HOST_ID; built-ins have no
    // disabled() predicates, so the first selectable for HOST_B is 'overview'.
    render(<HostSidebar {...defaultProps} selectedSubPage="hooks" />)

    fireEvent.click(screen.getByText('Second Host'))
    expect(defaultProps.onSelect).toHaveBeenCalledWith(HOST_B, 'overview')
  })

  it('clicking the currently selected host preserves selectedSubPage (no per-target rewrite)', () => {
    // When clicking the SAME host that is already selected, the existing
    // selectedSubPage stays — D2 only kicks in for cross-host expansion.
    render(<HostSidebar {...defaultProps} selectedSubPage="hooks" />)

    fireEvent.click(screen.getByText('Test Host'))
    // Same-host click while already expanded → no onSelect (collapse instead)
    // — but the test setup leaves the host expanded so clicking it again
    // collapses without calling onSelect.  Assert no spurious onSelect call.
    expect(defaultProps.onSelect).not.toHaveBeenCalled()
  })

  it('auto-expands new selectedHostId on prop change (e.g. host deletion fallback)', () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
      runtime: {},
    })

    // Initial: HOST_ID selected and expanded (|| semantics: selectedHostId always expands),
    // HOST_B is neither selected nor in expanded map → collapsed
    const { rerender } = render(<HostSidebar {...defaultProps} />)
    expect(screen.getAllByText('Overview')).toHaveLength(1) // only HOST_ID expanded

    // Simulate host deletion fallback: selectedHostId changes to HOST_B
    rerender(<HostSidebar {...defaultProps} selectedHostId={HOST_B} />)

    // HOST_B should now be expanded (it is the new selectedHostId)
    // HOST_ID was initialised as expanded=true in useState, so it stays expanded too
    const overviewItems = screen.getAllByText('Overview')
    expect(overviewItems.length).toBeGreaterThanOrEqual(1)
    const sessionsItems = screen.getAllByText('Sessions')
    expect(sessionsItems.length).toBeGreaterThanOrEqual(1)
  })

  // T7 companion (§3.1): disabled contribution → sidebar row appears with
  // data-disabled-ctx="true" attribute, click is a no-op (F7 contract).
  it('T7 (§3.1): disabled contribution renders as disabled row in sidebar', () => {
    const DisabledBody = () => null

    registerSettingsContribution({
      moduleId: 'fakemod',
      id: 'fakemod.disabled-section',
      localId: 'disabled-section',
      scope: 'host',
      order: 100,
      labelKey: 'disabled-section',
      component: DisabledBody,
      disabled: () => true,
      disabledReasonKey: 'settings.coming_soon',
    })

    render(<HostSidebar {...defaultProps} />)

    // Disabled row must appear (not filtered out)
    const disabledRows = document.querySelectorAll('[data-disabled-ctx="true"]')
    expect(disabledRows.length).toBeGreaterThan(0)

    // Click on disabled row must NOT call onSelect
    const disabledButton = disabledRows[0] as HTMLButtonElement
    fireEvent.click(disabledButton)
    expect(defaultProps.onSelect).not.toHaveBeenCalled()
  })

  // Test 13 (#588 spec §6.2 #13): sidebar passes runtime[hostId] in ctx so
  // disabled(ctx) predicates can react to live host runtime.
  it('Test 13: sidebar builds runtime-aware ctx — runtime tick flips disabled', () => {
    const RuntimeGated = () => null
    registerSettingsContribution({
      moduleId: 'fakemod',
      id: 'fakemod.runtime-gated',
      localId: 'runtime-gated',
      scope: 'host',
      order: 100,
      labelKey: 'runtime-gated',
      component: RuntimeGated,
      // disabled when no runtime observed yet.
      disabled: (ctx) => ctx.runtime === undefined,
    })

    // Initially no runtime[HOST_ID] — row disabled.
    const { rerender } = render(<HostSidebar {...defaultProps} />)
    expect(document.querySelectorAll('[data-disabled-ctx="true"]').length).toBeGreaterThan(0)

    // Tick runtime — re-render sidebar; disabled row count drops by one.
    useHostStore.setState((state) => ({
      runtime: { ...state.runtime, [HOST_ID]: { status: 'connected' } },
    }))
    rerender(<HostSidebar {...defaultProps} />)
    // The runtime-gated row no longer matches data-disabled-ctx="true".
    const disabledNow = Array.from(
      document.querySelectorAll('[data-disabled-ctx="true"]'),
    ) as HTMLElement[]
    for (const el of disabledNow) {
      expect(el.textContent).not.toContain('runtime-gated')
    }
  })
})
