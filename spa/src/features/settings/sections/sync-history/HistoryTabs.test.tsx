import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HistoryTabs } from './HistoryTabs'

describe('HistoryTabs', () => {
  it('renders Local selected', () => {
    render(<HistoryTabs active="local" remoteAvailable={false} onChange={() => {}} />)
    expect(screen.getByRole('tab', { name: /local/i })).toHaveAttribute('aria-selected', 'true')
  })

  it('Remote tab disabled when remoteAvailable=false', () => {
    render(<HistoryTabs active="local" remoteAvailable={false} onChange={() => {}} />)
    expect(screen.getByRole('tab', { name: /remote/i })).toBeDisabled()
  })

  it('dispatches onChange', () => {
    const fn = vi.fn()
    render(<HistoryTabs active="local" remoteAvailable={true} onChange={fn} />)
    fireEvent.click(screen.getByRole('tab', { name: /remote/i }))
    expect(fn).toHaveBeenCalledWith('remote')
  })
})
