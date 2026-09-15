import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent, cleanup } from '@testing-library/react'
import { ElectronSection } from './ElectronSection'
import { useI18nStore } from '../../stores/useI18nStore'

const mockGetVisible = vi.fn()
const mockSetVisible = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
  useI18nStore.getState().setLocale('en')
  window.electronAPI = {
    ...window.electronAPI!,
    tray: { getVisible: mockGetVisible, setVisible: mockSetVisible },
  } as typeof window.electronAPI
})

afterEach(() => {
  cleanup()
})

const renderIt = () => act(async () => { render(<ElectronSection />) })
const getSwitch = () => screen.getByRole('switch', { name: 'Show in menu bar' })

describe('ElectronSection — tray toggle', () => {
  it('reads the current tray visibility on mount and reflects it', async () => {
    mockGetVisible.mockResolvedValue(false)
    await renderIt()
    expect(mockGetVisible).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('false'))
    expect(screen.getByText('Show in Menu Bar')).toBeTruthy()
  })

  it('reflects true from getVisible', async () => {
    mockGetVisible.mockResolvedValue(true)
    await renderIt()
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('true'))
  })

  it('clicking the switch calls setVisible(false) and flips aria-checked', async () => {
    mockGetVisible.mockResolvedValue(true)
    mockSetVisible.mockResolvedValue(false)
    await renderIt()
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('true'))
    await act(async () => { fireEvent.click(getSwitch()) })
    expect(mockSetVisible).toHaveBeenCalledWith(false)
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('false'))
  })

  it('rolls back when setVisible rejects', async () => {
    mockGetVisible.mockResolvedValue(true)
    mockSetVisible.mockRejectedValue(new Error('ipc down'))
    await renderIt()
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('true'))
    await act(async () => { fireEvent.click(getSwitch()) })
    expect(mockSetVisible).toHaveBeenCalledWith(false)
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('true'))
  })

  it('still renders (defaulting to on) when the tray API is unavailable', async () => {
    window.electronAPI = { ...window.electronAPI!, tray: undefined } as unknown as typeof window.electronAPI
    await renderIt()
    expect(getSwitch().getAttribute('aria-checked')).toBe('true')
  })
})
