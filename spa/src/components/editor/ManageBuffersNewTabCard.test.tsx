import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ManageBuffersNewTabCard } from './ManageBuffersNewTabCard'

vi.mock('../../stores/useI18nStore', () => ({
  useI18nStore: (selector: (s: { t: (k: string) => string }) => unknown) =>
    selector({ t: (k: string) => k }),
}))

describe('ManageBuffersNewTabCard', () => {
  it('N2-3: click invokes onSelect with {kind:"editor-buffers"}', () => {
    const onSelect = vi.fn()
    render(<ManageBuffersNewTabCard onSelect={onSelect} />)
    fireEvent.click(screen.getByTestId('manage-buffers-card'))
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect).toHaveBeenCalledWith({ kind: 'editor-buffers' })
  })
})
