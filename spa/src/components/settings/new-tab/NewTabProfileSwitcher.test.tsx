import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { NewTabProfileSwitcher } from './NewTabProfileSwitcher'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'

beforeEach(() => {
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
})

describe('NewTabProfileSwitcher', () => {
  it('highlights active profile', () => {
    const onSelect = vi.fn()
    render(
      <NewTabProfileSwitcher
        active="1col"
        onSelect={onSelect}
        onToggleEnabled={() => {}}
        renderMain={() => <div>main</div>}
        renderThumb={(k) => <div>{`thumb-${k}`}</div>}
      />
    )
    expect(screen.getByTestId('profile-tab-1col')).toHaveAttribute('data-active', 'true')
    expect(screen.getByTestId('profile-tab-3col')).not.toHaveAttribute('data-active')
  })

  it('calls onSelect for each tab', () => {
    const onSelect = vi.fn()
    render(
      <NewTabProfileSwitcher
        active="1col"
        onSelect={onSelect}
        onToggleEnabled={() => {}}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    fireEvent.click(screen.getByTestId('profile-tab-3col'))
    expect(onSelect).toHaveBeenCalledWith('3col')
  })

  it('calls onToggleEnabled for 3col/2col but not 1col (locked)', () => {
    const onToggle = vi.fn()
    render(
      <NewTabProfileSwitcher
        active="1col"
        onSelect={() => {}}
        onToggleEnabled={onToggle}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    fireEvent.click(screen.getByTestId('profile-toggle-3col'))
    expect(onToggle).toHaveBeenCalledWith('3col', true)

    fireEvent.click(screen.getByTestId('profile-toggle-1col'))
    expect(onToggle).not.toHaveBeenCalledWith('1col', expect.anything())
  })

  it('shows prefilled hint when profile has content but is disabled', () => {
    useNewTabLayoutStore.getState().placeModule('3col', 'a', 0, 0)
    render(
      <NewTabProfileSwitcher
        active="1col"
        onSelect={() => {}}
        onToggleEnabled={() => {}}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    expect(screen.getByTestId('profile-hint-3col')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-hint-1col')).not.toBeInTheDocument()
  })

  it('shows empty badge when profile has no content', () => {
    render(
      <NewTabProfileSwitcher
        active="1col"
        onSelect={() => {}}
        onToggleEnabled={() => {}}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    expect(screen.getByTestId('profile-empty-1col')).toBeInTheDocument()
  })
})
