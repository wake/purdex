// spa/src/components/journal/RefChips.test.tsx — the refs of a row (J-2c): links only where a remote URL makes them one.
import { describe, it, expect, vi, afterEach } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { RefChips } from './RefChips'

const REMOTE = 'https://github.com/wake/purdex'
afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('RefChips', () => {
  it('with a remote URL #N and PR #N are links; a task number stays plain (M: link everything)', () => {
    render(<RefChips refs={['#2829', 'PR #2830', '7bcf08-12']} remoteUrl={REMOTE} />)
    const links = screen.getAllByRole('link')
    expect(links.map((a) => a.getAttribute('href'))).toEqual([`${REMOTE}/issues/2829`, `${REMOTE}/issues/2830`])
    const plain = screen.getByText('7bcf08-12')
    expect(plain.closest('a')).toBeNull()
  })
  it('without a remote URL nothing is a link, the refs still show', () => {
    render(<RefChips refs={['#2829', '7bcf08-12']} remoteUrl="" />)
    expect(screen.queryAllByRole('link')).toEqual([])
    expect(screen.getByText('#2829')).toBeTruthy()
    expect(screen.getByText('7bcf08-12')).toBeTruthy()
  })
  it('a click opens it outside the app, and does not reach the row behind it', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const onRow = vi.fn()
    render(<div onClick={onRow}><RefChips refs={['#1']} remoteUrl={REMOTE} /></div>)
    fireEvent.click(screen.getByRole('link'))
    expect(open).toHaveBeenCalledWith(`${REMOTE}/issues/1`, '_blank', 'noopener')
    expect(onRow).not.toHaveBeenCalled()
  })
  it('no refs, nothing drawn', () => {
    const { container } = render(<RefChips refs={[]} remoteUrl={REMOTE} />)
    expect(container.firstChild).toBeNull()
  })
})
