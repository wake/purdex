// K-2b review: the buttons inside a clickable (take-back) block must not also fire the block's own click.
import { describe, it, expect, vi, afterEach } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { QueuedMessages } from './QueuedMessages'
import type { QueueEntry } from '../../lib/conversations/send-queue'

const base = { ids: ['x'], createdAt: 0, undoUntil: 0, startedAt: 0 }
const sent: QueueEntry = { ...base, id: 'sent', text: 'handed', state: 'sent', mode: 'fill' }
const failed: QueueEntry = { ...base, id: 'failed', text: 'bad', state: 'failed', outcome: { kind: 'refused', reason: 'x' } as never }
const maybe: QueueEntry = { ...base, id: 'maybe', text: 'unsure', state: 'maybe', outcome: { kind: 'unknown', reason: 'x' } as never }
afterEach(cleanup)

const mount = (entries: QueueEntry[]) => {
  const fns = { onUndo: vi.fn(), onResend: vi.fn(), onDismiss: vi.fn(), onTakeBack: vi.fn() }
  render(<QueuedMessages entries={entries} {...fns} />)
  return fns
}

describe('inner buttons of a clickable block', () => {
  it('Dismiss on a handed entry dismisses only that entry and never takes back', () => {
    const f = mount([sent])
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(f.onDismiss).toHaveBeenCalledWith('sent')
    expect(f.onTakeBack).not.toHaveBeenCalled()
  })
  it('Retry and Resend never take back', () => {
    const f = mount([failed, maybe])
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    fireEvent.click(screen.getByRole('button', { name: 'Send again (may duplicate)' }))
    expect(f.onResend).toHaveBeenCalledWith('failed')
    expect(f.onTakeBack).not.toHaveBeenCalled()
  })
})
