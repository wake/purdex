import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { NewTabPresetSwitcher } from './NewTabPresetSwitcher'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'

beforeEach(() => {
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
})

describe('NewTabPresetSwitcher', () => {
  it('highlights active preset', () => {
    const onSelect = vi.fn()
    render(
      <NewTabPresetSwitcher
        active="1col"
        onSelect={onSelect}
        onToggleEnabled={() => {}}
        renderMain={() => <div>main</div>}
        renderThumb={(k) => <div>{`thumb-${k}`}</div>}
      />
    )
    expect(screen.getByTestId('preset-tab-1col')).toHaveAttribute('data-active', 'true')
    expect(screen.getByTestId('preset-tab-3col')).not.toHaveAttribute('data-active')
  })

  it('calls onSelect for each tab', () => {
    const onSelect = vi.fn()
    render(
      <NewTabPresetSwitcher
        active="1col"
        onSelect={onSelect}
        onToggleEnabled={() => {}}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    fireEvent.click(screen.getByTestId('preset-tab-3col'))
    expect(onSelect).toHaveBeenCalledWith('3col')
  })

  it('calls onToggleEnabled for 3col/2col but not 1col (locked)', () => {
    const onToggle = vi.fn()
    render(
      <NewTabPresetSwitcher
        active="1col"
        onSelect={() => {}}
        onToggleEnabled={onToggle}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    fireEvent.click(screen.getByTestId('preset-toggle-3col'))
    expect(onToggle).toHaveBeenCalledWith('3col', true)

    fireEvent.click(screen.getByTestId('preset-toggle-1col'))
    expect(onToggle).not.toHaveBeenCalledWith('1col', expect.anything())
  })

  it('shows prefilled hint when preset has content but is disabled', () => {
    useNewTabLayoutStore.getState().placeModule('3col', 'a', 0, 0)
    render(
      <NewTabPresetSwitcher
        active="1col"
        onSelect={() => {}}
        onToggleEnabled={() => {}}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    expect(screen.getByTestId('preset-hint-3col')).toBeInTheDocument()
    expect(screen.queryByTestId('preset-hint-1col')).not.toBeInTheDocument()
  })

  it('shows empty badge when preset has no content', () => {
    render(
      <NewTabPresetSwitcher
        active="1col"
        onSelect={() => {}}
        onToggleEnabled={() => {}}
        renderMain={() => null}
        renderThumb={() => null}
      />
    )
    expect(screen.getByTestId('preset-empty-1col')).toBeInTheDocument()
  })
})
