import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent, cleanup } from '@testing-library/react'
import { ElectronSection } from './ElectronSection'
import { useI18nStore } from '../../stores/useI18nStore'

const mockGetVisible = vi.fn()
const mockSetVisible = vi.fn()
const mockUnsubscribe = vi.fn()
// Captures the callback the component subscribes with, so tests can fire a
// "visibility changed" broadcast as if it came from the main process.
let visibilityListener: ((visible: boolean) => void) | null = null
const mockOnVisibilityChanged = vi.fn((cb: (visible: boolean) => void) => {
  visibilityListener = cb
  return mockUnsubscribe
})

beforeEach(() => {
  vi.clearAllMocks()
  visibilityListener = null
  useI18nStore.getState().setLocale('en')
  window.electronAPI = {
    ...window.electronAPI!,
    tray: { getVisible: mockGetVisible, setVisible: mockSetVisible, onVisibilityChanged: mockOnVisibilityChanged },
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

  it('follows a tray:visibility-changed broadcast from another window', async () => {
    mockGetVisible.mockResolvedValue(true)
    await renderIt()
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('true'))
    expect(mockOnVisibilityChanged).toHaveBeenCalledTimes(1)
    act(() => { visibilityListener!(false) })
    expect(getSwitch().getAttribute('aria-checked')).toBe('false')
    act(() => { visibilityListener!(true) })
    expect(getSwitch().getAttribute('aria-checked')).toBe('true')
  })

  it('unsubscribes from visibility changes on unmount', async () => {
    mockGetVisible.mockResolvedValue(true)
    const { unmount } = render(<ElectronSection />)
    await waitFor(() => expect(mockOnVisibilityChanged).toHaveBeenCalledTimes(1))
    expect(mockUnsubscribe).not.toHaveBeenCalled()
    unmount()
    expect(mockUnsubscribe).toHaveBeenCalledTimes(1)
  })

  it('still works when the shell predates onVisibilityChanged', async () => {
    window.electronAPI = {
      ...window.electronAPI!,
      tray: { getVisible: mockGetVisible, setVisible: mockSetVisible },
    } as typeof window.electronAPI
    mockGetVisible.mockResolvedValue(false)
    await renderIt()
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('false'))
    expect(getSwitch().closest('[inert]')).toBeNull()
  })

  it('ignores a stale setVisible response when a newer click has already resolved', async () => {
    mockGetVisible.mockResolvedValue(true)
    let resolveFirst!: (v: boolean) => void
    const first = new Promise<boolean>((r) => { resolveFirst = r })
    mockSetVisible
      .mockReturnValueOnce(first)                 // click 1: on -> off, slow
      .mockResolvedValueOnce(true)                // click 2: off -> on, fast
    await renderIt()
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('true'))

    await act(async () => { fireEvent.click(getSwitch()) })   // -> false (pending)
    expect(getSwitch().getAttribute('aria-checked')).toBe('false')
    await act(async () => { fireEvent.click(getSwitch()) })   // -> true (resolves true)
    expect(mockSetVisible).toHaveBeenNthCalledWith(1, false)
    expect(mockSetVisible).toHaveBeenNthCalledWith(2, true)
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('true'))

    // Now the first (stale) request finishes with "false" — it must not win.
    await act(async () => { resolveFirst(false) })
    expect(getSwitch().getAttribute('aria-checked')).toBe('true')
  })

  it('does not roll back from a stale rejection once newer clicks have resolved', async () => {
    mockGetVisible.mockResolvedValue(true)
    let rejectFirst!: (e: Error) => void
    const first = new Promise<boolean>((_, rej) => { rejectFirst = rej })
    mockSetVisible
      .mockReturnValueOnce(first)                 // click 1: true -> false, rejects late
      .mockResolvedValueOnce(true)                // click 2: false -> true
      .mockResolvedValueOnce(false)               // click 3: true -> false
    await renderIt()
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('true'))
    await act(async () => { fireEvent.click(getSwitch()) })
    await act(async () => { fireEvent.click(getSwitch()) })
    await act(async () => { fireEvent.click(getSwitch()) })
    expect(mockSetVisible).toHaveBeenCalledTimes(3)
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('false'))

    // Click 1's `prev` was true; a naive rollback would flip the switch back
    // on even though the user's latest intent (click 3) is off.
    await act(async () => { rejectFirst(new Error('late failure')) })
    expect(getSwitch().getAttribute('aria-checked')).toBe('false')
  })

  it('keeps the switch enabled while a toggle is in flight', async () => {
    mockGetVisible.mockResolvedValue(true)
    mockSetVisible.mockReturnValue(new Promise<boolean>(() => {}))   // never resolves
    await renderIt()
    await waitFor(() => expect(getSwitch().getAttribute('aria-checked')).toBe('true'))
    await act(async () => { fireEvent.click(getSwitch()) })
    expect(getSwitch().closest('[inert]')).toBeNull()
    expect((getSwitch() as HTMLButtonElement).disabled).toBe(false)
    await act(async () => { fireEvent.click(getSwitch()) })
    expect(mockSetVisible).toHaveBeenCalledTimes(2)
  })

  it('disables the row when the tray IPC is missing (older shell serving a newer SPA)', async () => {
    window.electronAPI = { ...window.electronAPI!, tray: undefined } as unknown as typeof window.electronAPI
    await renderIt()
    const sw = getSwitch()
    expect(sw.getAttribute('aria-checked')).toBe('true')
    expect(sw.closest('[inert]')).not.toBeNull()
    fireEvent.click(sw)
    expect(mockSetVisible).not.toHaveBeenCalled()
  })
})
