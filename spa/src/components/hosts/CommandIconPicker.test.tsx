import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
  getIconPath: () => 'M0,0L1,1',
}))

vi.mock('../../features/workspace/generated/icon-meta.json', () => ({
  default: [
    { n: 'Terminal', t: ['console', 'cli'], c: ['development'] },
    { n: 'Rocket', t: ['launch', 'spaceship'], c: ['objects'] },
    { n: 'House', t: ['home'], c: ['general'] },
    ...Array.from({ length: 150 }, (_, i) => ({ n: `Filler${i}`, t: ['filler'], c: [] })),
  ],
}))

import { CommandIconPicker } from './CommandIconPicker'

describe('CommandIconPicker', () => {
  it('offers the five agent icons first and selects one', () => {
    const onChange = vi.fn()
    render(<CommandIconPicker value={{ kind: 'phosphor', value: 'Terminal' }} onChange={onChange} />)
    for (const v of ['cc-bot', 'cc-star', 'openai', 'codex', 'opencode']) {
      expect(screen.getByTestId(`command-icon-agent-${v}`)).toBeInTheDocument()
    }
    fireEvent.click(screen.getByTestId('command-icon-agent-codex'))
    expect(onChange).toHaveBeenCalledWith({ kind: 'agent', value: 'codex' })
  })

  it('marks the current value as pressed', async () => {
    render(<CommandIconPicker value={{ kind: 'phosphor', value: 'Rocket' }} onChange={vi.fn()} />)
    expect(await screen.findByTestId('command-icon-phosphor-Rocket')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('command-icon-agent-cc-bot')).toHaveAttribute('aria-pressed', 'false')
  })

  it('paginates and searches names and tags', async () => {
    const onChange = vi.fn()
    render(<CommandIconPicker value={{ kind: 'agent', value: 'cc-bot' }} onChange={onChange} />)
    await screen.findByTestId('command-icon-phosphor-Terminal')
    expect(screen.queryByTestId('command-icon-phosphor-Filler140')).toBeNull()
    fireEvent.click(screen.getByTestId('command-icon-more'))
    expect(screen.getByTestId('command-icon-phosphor-Filler140')).toBeInTheDocument()

    fireEvent.change(screen.getByTestId('command-icon-search'), { target: { value: 'launch' } })
    await waitFor(() => expect(screen.queryByTestId('command-icon-phosphor-Terminal')).toBeNull())
    fireEvent.click(screen.getByTestId('command-icon-phosphor-Rocket'))
    expect(onChange).toHaveBeenCalledWith({ kind: 'phosphor', value: 'Rocket' })

    fireEvent.change(screen.getByTestId('command-icon-search'), { target: { value: 'zzz' } })
    expect(await screen.findByTestId('command-icon-empty')).toBeInTheDocument()
  })

  it('disabled blocks selection', () => {
    const onChange = vi.fn()
    render(<CommandIconPicker value={{ kind: 'phosphor', value: 'Terminal' }} onChange={onChange} disabled />)
    const btn = screen.getByTestId('command-icon-agent-codex')
    expect(btn).toBeDisabled()
    fireEvent.click(btn)
    fireEvent.click(screen.getByTestId('command-icon-phosphor-Rocket'))
    expect(onChange).not.toHaveBeenCalled()
    expect(screen.getByTestId('command-icon-search')).toBeDisabled()
  })
})
