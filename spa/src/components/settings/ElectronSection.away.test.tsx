import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent, cleanup } from '@testing-library/react'
import { ElectronSection } from './ElectronSection'
import { useI18nStore } from '../../stores/useI18nStore'

// PA-2b: the "away after N minutes" row (plan D11), gated on presence.getAwayMinutes.
const getAwayMinutes = vi.fn()
const setAwayMinutes = vi.fn()
const original = window.electronAPI

const withBridge = (presence: unknown) => {
  window.electronAPI = { ...original!, tray: { getVisible: vi.fn().mockResolvedValue(true), setVisible: vi.fn() }, presence } as typeof window.electronAPI
}
const renderIt = () => act(async () => { render(<ElectronSection />) })
const select = () => screen.getByRole('combobox', { name: 'Minutes away before pushing' }) as HTMLSelectElement

beforeEach(() => {
  vi.clearAllMocks()
  useI18nStore.getState().setLocale('en')
})
afterEach(() => { cleanup(); window.electronAPI = original })

describe('ElectronSection — away minutes', () => {
  it('is hidden without the presence group', async () => {
    withBridge(undefined)
    await renderIt()
    expect(screen.queryByText('Minutes away before pushing to your phone')).toBeNull()
  })

  it('is hidden when the shell has the group but not getAwayMinutes', async () => {
    withBridge({ setHosts: vi.fn() })
    await renderIt()
    expect(screen.queryByText('Minutes away before pushing to your phone')).toBeNull()
  })

  it('shows the stored value with the offered choices', async () => {
    getAwayMinutes.mockResolvedValue(10)
    withBridge({ getAwayMinutes, setAwayMinutes })
    await renderIt()
    await waitFor(() => expect(select().value).toBe('10'))
    expect(Array.from(select().options).map((o) => o.value)).toEqual(['1', '2', '3', '5', '10', '15', '30', '60'])
  })

  it('a change calls setAwayMinutes and shows the value it returns', async () => {
    getAwayMinutes.mockResolvedValue(5)
    setAwayMinutes.mockResolvedValue(15)
    withBridge({ getAwayMinutes, setAwayMinutes })
    await renderIt()
    await waitFor(() => expect(select().value).toBe('5'))
    fireEvent.change(select(), { target: { value: '30' } })
    await waitFor(() => expect(setAwayMinutes).toHaveBeenCalledWith(30))
    await waitFor(() => expect(select().value).toBe('15'))
  })

  it('goes back to the previous value when saving fails', async () => {
    getAwayMinutes.mockResolvedValue(5)
    setAwayMinutes.mockRejectedValue(new Error('no'))
    withBridge({ getAwayMinutes, setAwayMinutes })
    await renderIt()
    await waitFor(() => expect(select().value).toBe('5'))
    fireEvent.change(select(), { target: { value: '60' } })
    await waitFor(() => expect(select().value).toBe('5'))
  })

  it('has zh-TW and en strings', async () => {
    getAwayMinutes.mockResolvedValue(3)
    withBridge({ getAwayMinutes, setAwayMinutes })
    useI18nStore.getState().setLocale('zh-TW')
    await renderIt()
    expect(screen.getByText('離開電腦幾分鐘後推到手機')).toBeTruthy()
    expect(screen.getByText('在電腦前（這台 Mac 有鍵盤或滑鼠動作、沒鎖螢幕）時不推到手機；鎖螢幕 1 分鐘後也會推。')).toBeTruthy()
    await waitFor(() => expect(screen.getByRole('option', { name: '3 分鐘' })).toBeTruthy())
    cleanup()
    useI18nStore.getState().setLocale('en')
    await renderIt()
    expect(screen.getByText('Minutes away before pushing to your phone')).toBeTruthy()
    expect(screen.getByRole('option', { name: '3 min' })).toBeTruthy()
  })
})
