import { describe, it, expect, vi, afterEach } from 'vitest'
import { copyText } from './copy-text'

const originalClipboard = navigator.clipboard
const originalExecCommand = document.execCommand

afterEach(() => {
  Object.defineProperty(navigator, 'clipboard', { value: originalClipboard, configurable: true })
  document.execCommand = originalExecCommand
})

describe('copyText', () => {
  it('uses navigator.clipboard.writeText when available', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    await copyText('hello')
    expect(writeText).toHaveBeenCalledWith('hello')
  })

  it('falls back to a hidden textarea + execCommand when clipboard is unavailable', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    document.execCommand = vi.fn(() => true)
    await expect(copyText('hello')).resolves.toBeUndefined()
    expect(document.execCommand).toHaveBeenCalledWith('copy')
  })

  it('rejects when neither clipboard nor execCommand is available', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    document.execCommand = undefined as unknown as typeof document.execCommand
    await expect(copyText('hello')).rejects.toThrow('copy unsupported')
  })
})
