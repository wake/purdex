import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { WebNotificationPrompt } from './WebNotificationPrompt'
import { STORAGE_KEYS } from '../lib/storage/keys'

function stubNotification(permission: NotificationPermission, requestPermission = vi.fn().mockResolvedValue('granted')) {
  Object.defineProperty(window, 'Notification', {
    configurable: true, writable: true,
    value: Object.assign(function () {}, { permission, requestPermission }),
  })
}

describe('WebNotificationPrompt', () => {
  beforeEach(() => {
    delete (window as unknown as { electronAPI?: unknown }).electronAPI
    localStorage.clear()
    stubNotification('default')
  })

  it('web + default → 顯示提示', () => {
    render(<WebNotificationPrompt />)
    expect(screen.getByText(/notification|通知/i)).toBeInTheDocument()
  })
  it('點啟用 → requestPermission 被呼叫且提示消失', async () => {
    const req = vi.fn().mockResolvedValue('granted')
    stubNotification('default', req)
    render(<WebNotificationPrompt />)
    fireEvent.click(screen.getByRole('button', { name: /enable|啟用/i }))
    expect(req).toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument())
  })
  it('點關閉 → 寫入 dismissal 且消失', () => {
    render(<WebNotificationPrompt />)
    fireEvent.click(screen.getByRole('button', { name: /dismiss|close|關閉|稍後/i }))
    expect(localStorage.getItem(STORAGE_KEYS.WEB_NOTIFICATION_DISMISSED)).toBe('1')
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })
  it('granted → 不顯示', () => {
    stubNotification('granted')
    render(<WebNotificationPrompt />)
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })
  it('denied → 不顯示', () => {
    stubNotification('denied')
    render(<WebNotificationPrompt />)
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })
  it('pre-dismissed → 不顯示', () => {
    localStorage.setItem(STORAGE_KEYS.WEB_NOTIFICATION_DISMISSED, '1')
    render(<WebNotificationPrompt />)
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })
  it('Electron → 不顯示', () => {
    ;(window as unknown as { electronAPI?: unknown }).electronAPI = {}
    render(<WebNotificationPrompt />)
    expect(screen.queryByText(/notification|通知/i)).not.toBeInTheDocument()
  })
})
