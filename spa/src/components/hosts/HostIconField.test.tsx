import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

// jsdom has no layout engine, so useVirtualizer returns 0 items.
// Mock it to render all rows without virtualization.
vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 38,
    getVirtualItems: () =>
      Array.from({ length: count }, (_, i) => ({ index: i, key: i, start: i * 38, size: 38 })),
  }),
}))

vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0L10,10',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { HostIconField } from './HostIconField'
import { useHostStore } from '../../stores/useHostStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { hostLookOf } from '../../lib/host-look'
import en from '../../locales/en.json'

const HOST_ID = 'h1'

// H2c-2: the icon is written to the look store — read what the selector shows.
function host() {
  return hostLookOf(HOST_ID)
}

function iconButtons() {
  return screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
}

function openPicker() {
  fireEvent.click(screen.getByTestId('host-icon-preview'))
}

beforeEach(() => {
  useHostLookStore.setState({ looks: {} })
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'H', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: {},
  })
})

describe('HostIconField', () => {
  it('renders the preview button with the picker closed', () => {
    render(<HostIconField hostId={HOST_ID} />)
    const preview = screen.getByTestId('host-icon-preview')
    expect(preview).toHaveAttribute('aria-expanded', 'false')
    expect(iconButtons()).toHaveLength(0)
  })

  it('toggles the picker (a floating dialog) from the preview button', () => {
    render(<HostIconField hostId={HOST_ID} />)
    openPicker()
    expect(screen.getByTestId('host-icon-preview')).toHaveAttribute('aria-expanded', 'true')
    const dialog = screen.getByRole('dialog', { name: 'Change host icon' })
    expect(dialog.parentElement).toBe(document.body)
    expect(iconButtons().length).toBeGreaterThan(0)
    fireEvent.click(screen.getByTestId('host-icon-preview'))
    expect(iconButtons()).toHaveLength(0)
  })

  it('closes on outside mousedown; the preview button still toggles', () => {
    render(<div><HostIconField hostId={HOST_ID} /><button data-testid="outside">x</button></div>)
    openPicker()
    expect(screen.getByRole('dialog', { name: 'Change host icon' })).toBeInTheDocument()
    fireEvent.mouseDown(screen.getByTestId('outside'))
    expect(screen.queryByRole('dialog', { name: 'Change host icon' })).toBeNull()
    fireEvent.mouseDown(screen.getByTestId('host-icon-preview'))
    fireEvent.click(screen.getByTestId('host-icon-preview'))
    expect(screen.getByRole('dialog', { name: 'Change host icon' })).toBeInTheDocument()
  })

  it('a composing Escape leaves the panel open; a plain Escape closes it', () => {
    render(<HostIconField hostId={HOST_ID} />)
    openPicker()
    fireEvent.keyDown(document, { key: 'Escape', isComposing: true })
    expect(screen.getByRole('dialog', { name: 'Change host icon' })).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('dialog', { name: 'Change host icon' })).toBeNull()
  })

  it('does not render the picker dialog header (inline mode)', () => {
    render(<HostIconField hostId={HOST_ID} />)
    openPicker()
    expect(screen.queryByText(en['workspace.change_icon'])).toBeNull()
  })

  it('selecting an icon stores its name', () => {
    render(<HostIconField hostId={HOST_ID} />)
    openPicker()
    const first = iconButtons()[0]
    const name = first.getAttribute('data-icon')!
    fireEvent.click(first)
    expect(host().icon).toBe(name)
  })

  it('selecting a weight stores the weight', () => {
    render(<HostIconField hostId={HOST_ID} />)
    openPicker()
    fireEvent.click(screen.getByTestId('weight-duotone'))
    expect(host().iconWeight).toBe('duotone')
    expect(host().icon).toBeTruthy()
  })

  it('keeps the chosen weight when an icon is picked afterwards', () => {
    render(<HostIconField hostId={HOST_ID} />)
    openPicker()
    fireEvent.click(screen.getByTestId('weight-fill'))
    const first = iconButtons()[0]
    const name = first.getAttribute('data-icon')!
    fireEvent.click(first)
    expect(host().icon).toBe(name)
    expect(host().iconWeight).toBe('fill')
  })

  it("the picker's Clear button removes icon and weight", () => {
    useHostStore.getState().setHostIcon(HOST_ID, 'Rocket', 'duotone')
    render(<HostIconField hostId={HOST_ID} />)
    openPicker()
    fireEvent.click(screen.getByTestId('clear-icon'))
    expect(host().icon).toBeUndefined()
    expect(host().iconWeight).toBeUndefined()
  })

  it('the use-default button removes icon and weight', () => {
    useHostStore.getState().setHostIcon(HOST_ID, 'Rocket', 'duotone')
    render(<HostIconField hostId={HOST_ID} />)
    fireEvent.click(screen.getByTestId('host-icon-default'))
    expect(host().icon).toBeUndefined()
    expect(host().iconWeight).toBeUndefined()
  })
})
