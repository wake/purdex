import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { HostBadgeSetting } from './HostBadgeSetting'

const PREFIX = 'host-badge-test'

function renderSetting(overrides: { enabled?: boolean } = {}) {
  const handlers = {
    onEnabledChange: vi.fn(),
    onLineColorChange: vi.fn(),
    onBoxChange: vi.fn(),
    onInsetChange: vi.fn(),
    onRadiusChange: vi.fn(),
  }
  const utils = render(
    <HostBadgeSetting
      label="Sidebar host badge"
      description="desc"
      enabled={overrides.enabled ?? true}
      lineColor="host"
      box={16}
      inset={2}
      radius={4}
      testIdPrefix={PREFIX}
      {...handlers}
    />,
  )
  return { ...utils, ...handlers }
}

const NUMERIC_IDS = ['box', 'inset', 'radius'] as const

function numeric(id: (typeof NUMERIC_IDS)[number]) {
  return screen.getByTestId(`${PREFIX}-${id}`) as HTMLInputElement
}

function lineColorButtons() {
  return within(screen.getByTestId(`${PREFIX}-line-color`)).getAllByRole('button')
}

const base = {
  label: 'Sidebar host badge',
  description: 'desc',
  enabled: true,
  lineColor: 'host' as const,
  box: 16,
  inset: 2,
  radius: 4,
  onEnabledChange: vi.fn(),
  onLineColorChange: vi.fn(),
  onBoxChange: vi.fn(),
  onInsetChange: vi.fn(),
  onRadiusChange: vi.fn(),
  testIdPrefix: PREFIX,
}

describe('HostBadgeSetting', () => {
  it('renders the group label and one toggle, one segment control, three number inputs', () => {
    renderSetting()
    expect(screen.getByText('Sidebar host badge')).toBeTruthy()
    expect(screen.getByRole('switch', { name: /Sidebar host badge/ })).toBeTruthy()
    expect(lineColorButtons()).toHaveLength(2)
    for (const id of NUMERIC_IDS) {
      const input = numeric(id)
      expect(input.getAttribute('type')).toBe('number')
      expect(input.getAttribute('aria-label')).toMatch(/^Sidebar host badge: /)
    }
  })

  it('the toggle calls onEnabledChange', () => {
    const { onEnabledChange } = renderSetting()
    fireEvent.click(screen.getByRole('switch', { name: /Sidebar host badge/ }))
    expect(onEnabledChange).toHaveBeenCalledWith(false)
  })

  it('the line color segments call onLineColorChange', () => {
    const { onLineColorChange } = renderSetting()
    fireEvent.click(lineColorButtons()[1])
    expect(onLineColorChange).toHaveBeenLastCalledWith('neutral')
  })

  it('each number input calls its own handler', () => {
    const { onBoxChange, onInsetChange, onRadiusChange } = renderSetting()
    fireEvent.change(numeric('box'), { target: { value: '20' } })
    expect(onBoxChange).toHaveBeenLastCalledWith(20)
    fireEvent.change(numeric('inset'), { target: { value: '3' } })
    expect(onInsetChange).toHaveBeenLastCalledWith(3)
    fireEvent.change(numeric('radius'), { target: { value: '6' } })
    expect(onRadiusChange).toHaveBeenLastCalledWith(6)
  })

  it('clamps out-of-range numbers before calling the handlers', () => {
    const { onBoxChange, onInsetChange, onRadiusChange } = renderSetting()
    fireEvent.change(numeric('box'), { target: { value: '99' } })
    expect(onBoxChange).toHaveBeenLastCalledWith(24)
    fireEvent.change(numeric('box'), { target: { value: '1' } })
    expect(onBoxChange).toHaveBeenLastCalledWith(12)
    fireEvent.change(numeric('inset'), { target: { value: '9' } })
    expect(onInsetChange).toHaveBeenLastCalledWith(5)
    fireEvent.change(numeric('radius'), { target: { value: '99' } })
    expect(onRadiusChange).toHaveBeenLastCalledWith(8)
  })

  it('disables everything except the toggle when enabled is false', () => {
    const { onLineColorChange, onBoxChange } = renderSetting({ enabled: false })
    expect(screen.getByRole('switch', { name: /Sidebar host badge/ })).not.toBeDisabled()
    for (const id of NUMERIC_IDS) {
      expect(numeric(id)).toBeDisabled()
    }
    const group = screen.getByTestId(`${PREFIX}-line-color`)
    expect(group).toHaveAttribute('inert')
    expect(group).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(lineColorButtons()[1])
    expect(onLineColorChange).not.toHaveBeenCalled()
    fireEvent.change(numeric('box'), { target: { value: '20' } })
    expect(onBoxChange).not.toHaveBeenCalled()
  })

  it('the toggle still works while disabled', () => {
    const { onEnabledChange } = renderSetting({ enabled: false })
    fireEvent.click(screen.getByRole('switch', { name: /Sidebar host badge/ }))
    expect(onEnabledChange).toHaveBeenCalledWith(true)
  })

  it('shows a caption above each numeric input', () => {
    render(<HostBadgeSetting {...base} />)
    for (const [id, caption] of [['box', 'Size'], ['inset', 'Inset'], ['radius', 'Radius']] as const) {
      const input = screen.getByTestId(`${base.testIdPrefix}-${id}`)
      const label = input.closest('label')
      expect(label).not.toBeNull()
      expect(label!.textContent).toContain(caption)
    }
    expect(screen.queryByTestId(`${base.testIdPrefix}-line-opacity`)).toBeNull()
    expect(screen.queryByTestId(`${base.testIdPrefix}-bg-opacity`)).toBeNull()
  })
})
