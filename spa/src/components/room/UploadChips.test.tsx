// spa/src/components/room/UploadChips.test.tsx
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import UploadChips from './UploadChips'
import type { Chip } from '../../lib/nex/worker-upload'

afterEach(cleanup)

const chips: Chip[] = [
  { key: 'a', name: 'a.txt', status: 'uploading' },
  { key: 'b', name: 'b.png', status: 'done', path: '/w/b.png', previewUrl: 'blob:b' },
  { key: 'c', name: 'c.bin', status: 'failed', error: 'file_too_large' },
]

describe('UploadChips', () => {
  it('renders nothing without chips', () => {
    const { container } = render(<UploadChips chips={[]} onRemove={() => {}} />)
    expect(container.firstChild).toBeNull()
  })

  it('shows each chip with its status, a thumbnail for an image and the failure reason', () => {
    render(<UploadChips chips={chips} onRemove={() => {}} />)
    const els = screen.getAllByTestId('upload-chip')
    expect(els.map((e) => e.getAttribute('data-status'))).toEqual(['uploading', 'done', 'failed'])
    expect(els[0].textContent).toContain('a.txt')
    expect(els[1].querySelector('img')?.getAttribute('src')).toBe('blob:b')
    expect(els[2].textContent).toContain('File too large')
  })

  it('the remove button reports the chip key', () => {
    const onRemove = vi.fn()
    render(<UploadChips chips={chips} onRemove={onRemove} />)
    fireEvent.click(screen.getByRole('button', { name: 'Remove c.bin' }))
    expect(onRemove).toHaveBeenCalledWith('c')
  })
})
