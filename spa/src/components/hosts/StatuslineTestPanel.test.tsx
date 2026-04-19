import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { StatuslineTestPanel } from './StatuslineTestPanel'
import * as hookMod from '../../hooks/useStatuslineTest'
import type { StatuslineTestState } from '../../hooks/useStatuslineTest'

function makeState(overrides: Partial<StatuslineTestState> = {}): StatuslineTestState {
  return {
    stages: {
      1: { status: 'untested' }, 2: { status: 'untested' },
      3: { status: 'untested' }, 4: { status: 'untested' }, 5: { status: 'untested' },
    },
    running: false,
    lastRunAt: null,
    nonce: null,
    ...overrides,
  }
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('StatuslineTestPanel', () => {
  it('renders 5 stage rows with the expected names', () => {
    vi.spyOn(hookMod, 'useStatuslineTest').mockReturnValue({ state: makeState(), run: vi.fn() })
    render(<StatuslineTestPanel hostId="h1" autoRun={false} />)
    expect(screen.getByText(/proxy spawned/i)).toBeInTheDocument()
    expect(screen.getByText(/daemon → ws broadcast/i)).toBeInTheDocument()
    expect(screen.getByText(/spa store updated/i)).toBeInTheDocument()
  })

  it('Run again button calls run()', () => {
    const run = vi.fn()
    vi.spyOn(hookMod, 'useStatuslineTest').mockReturnValue({ state: makeState(), run })
    render(<StatuslineTestPanel hostId="h1" autoRun={false} />)
    fireEvent.click(screen.getByRole('button', { name: /run test again/i }))
    expect(run).toHaveBeenCalledOnce()
  })

  it('auto-runs once on mount when autoRun=true', async () => {
    const run = vi.fn().mockResolvedValue(undefined)
    vi.spyOn(hookMod, 'useStatuslineTest').mockReturnValue({ state: makeState(), run })
    render(<StatuslineTestPanel hostId="h1" autoRun={true} />)
    await waitFor(() => expect(run).toHaveBeenCalledOnce())
  })

  it('shows failure error when a stage failed', () => {
    const state = makeState({
      stages: {
        1: { status: 'passed', elapsedMs: 10 },
        2: { status: 'failed', error: 'timeout at stage 2' },
        3: { status: 'skipped' }, 4: { status: 'skipped' }, 5: { status: 'skipped' },
      },
      lastRunAt: Date.now(),
    })
    vi.spyOn(hookMod, 'useStatuslineTest').mockReturnValue({ state, run: vi.fn() })
    render(<StatuslineTestPanel hostId="h1" autoRun={false} />)
    fireEvent.click(screen.getByRole('button', { name: /show log/i }))
    expect(screen.getByText(/timeout at stage 2/i)).toBeInTheDocument()
  })
})
