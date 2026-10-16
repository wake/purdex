// F-2d: the connection check of a probe host — one line per fleet daemon host, two marks each.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'

const runProbeCheck = vi.fn()
vi.mock('../../lib/fleet/probe-check', async (orig) => ({
  ...(await orig<typeof import('../../lib/fleet/probe-check')>()),
  runProbeCheck: (...a: unknown[]) => runProbeCheck(...a),
}))

import { ProbeCheck } from './ProbeCheck'
import { useFleetStore } from '../../lib/fleet/store'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'

const A = 'ha'
const B = 'hb'
const aliases = { [A]: 'nas-a', [B]: 'nas-b' }
const ans = (a: Record<string, unknown>) => ({ kind: 'answer', answer: { ssh: 'unknown', measure: 'unknown', error: null, ...a } })
const line = (id: string) => screen.getByTestId(`probe-check-${id}`)
const mark = (id: string, which: 'ssh' | 'measure') => screen.getByTestId(`probe-check-${id}-${which}`).textContent!

beforeEach(() => {
  runProbeCheck.mockReset()
  const host = (id: string) => ({ id, name: id, ip: '127.0.0.1', port: 7860, token: 't' })
  useHostStore.setState({
    hosts: { [A]: host(A), [B]: host(B) } as never,
    hostOrder: [A, B],
    runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } } as never,
  })
  useFleetStore.setState({ byHost: { [A]: { gen: 1, support: 'yes', term: 0 }, [B]: { gen: 1, support: 'yes', term: 0 } } as never })
})
afterEach(cleanup)

describe('ProbeCheck', () => {
  it('on open asks each daemon with ITS OWN alias and shows both marks as passed', async () => {
    runProbeCheck.mockResolvedValue(ans({ ssh: 'ok', measure: 'ok' }))
    render(<ProbeCheck aliases={aliases} />)
    await waitFor(() => expect(mark(A, 'ssh')).toContain('✓'))
    expect(runProbeCheck.mock.calls.map((c) => [c[0], c[1]]).sort()).toEqual([[A, 'nas-a'], [B, 'nas-b']])
    expect(mark(A, 'measure')).toContain('✓')
    expect(line(B).textContent).toContain('SSH key')
    expect(line(B).textContent).toContain('Measurement')
  })

  it('auth: the key was refused — SSH ✗ with the reason, the reading stays —', async () => {
    runProbeCheck.mockResolvedValue(ans({ ssh: 'failed', error: 'auth' }))
    render(<ProbeCheck aliases={aliases} />)
    await waitFor(() => expect(mark(A, 'ssh')).toContain('✗'))
    expect(mark(A, 'ssh')).toContain('key was refused')
    expect(mark(A, 'measure')).toContain('—')
    expect(mark(A, 'measure')).not.toContain('✓')
  })

  it('unreachable: both marks are — with one "cannot reach the host"', async () => {
    runProbeCheck.mockResolvedValue(ans({ error: 'unreachable' }))
    render(<ProbeCheck aliases={aliases} />)
    await waitFor(() => expect(line(A).textContent).toContain('cannot reach'))
    expect(mark(A, 'ssh')).toContain('—')
    expect(mark(A, 'measure')).toContain('—')
    expect(line(A).textContent).not.toContain('✗')
  })

  it('remote_exit: SSH ✓ and the reading ✗', async () => {
    runProbeCheck.mockResolvedValue(ans({ ssh: 'ok', measure: 'failed', error: 'remote_exit' }))
    render(<ProbeCheck aliases={aliases} />)
    await waitFor(() => expect(mark(A, 'measure')).toContain('✗'))
    expect(mark(A, 'ssh')).toContain('✓')
    expect(mark(A, 'measure')).toContain('no reading')
  })

  it('unknown is shown as —, never as ✓', async () => {
    runProbeCheck.mockResolvedValue(ans({}))
    render(<ProbeCheck aliases={aliases} />)
    await waitFor(() => expect(mark(A, 'ssh')).toContain('—'))
    expect(mark(A, 'ssh')).not.toContain('✓')
    expect(mark(A, 'measure')).not.toContain('✓')
  })

  it('an offline daemon host is not asked: "{host} is offline, cannot check"', async () => {
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'disconnected' } } as never })
    runProbeCheck.mockResolvedValue(ans({ ssh: 'ok', measure: 'ok' }))
    render(<ProbeCheck aliases={aliases} />)
    await waitFor(() => expect(mark(A, 'ssh')).toContain('✓'))
    expect(line(B).textContent).toContain('hb is offline')
    expect(runProbeCheck.mock.calls.map((c) => c[0])).toEqual([A])
  })

  it('a 404 says that daemon does not support the check yet', async () => {
    runProbeCheck.mockResolvedValue({ kind: 'unsupported' })
    render(<ProbeCheck aliases={aliases} />)
    await waitFor(() => expect(line(A).textContent).toContain('does not support the check yet'))
  })

  it('Recheck runs again and is disabled while a check is running', async () => {
    let release: (v: unknown) => void = () => {}
    runProbeCheck.mockImplementation(() => new Promise((res) => { release = res }))
    render(<ProbeCheck aliases={{ [A]: 'nas-a' }} />)
    const btn = screen.getByTestId('probe-check-rerun') as HTMLButtonElement
    await waitFor(() => expect(btn.disabled).toBe(true))
    release(ans({ ssh: 'ok', measure: 'ok' }))
    await waitFor(() => expect(btn.disabled).toBe(false))
    runProbeCheck.mockClear()
    fireEvent.click(btn)
    await waitFor(() => expect(runProbeCheck).toHaveBeenCalledTimes(1)) // only A has an alias here
  })

  it('runs again when the saved aliases change', async () => {
    runProbeCheck.mockResolvedValue(ans({ ssh: 'ok', measure: 'ok' }))
    const { rerender } = render(<ProbeCheck aliases={aliases} />)
    await waitFor(() => expect(runProbeCheck).toHaveBeenCalledTimes(2))
    rerender(<ProbeCheck aliases={{ ...aliases, [A]: 'other-a' }} />)
    await waitFor(() => expect(runProbeCheck.mock.calls.some((c) => c[1] === 'other-a')).toBe(true))
  })

  it('comes out in zh-TW too', async () => {
    useI18nStore.getState().setLocale('zh-TW')
    try {
      runProbeCheck.mockResolvedValue(ans({ ssh: 'failed', error: 'host_key' }))
      render(<ProbeCheck aliases={aliases} />)
      await waitFor(() => expect(mark(A, 'ssh')).toContain('主機金鑰不符'))
      expect(line(A).textContent).toContain('SSH 金鑰')
      expect(line(A).textContent).toContain('量測讀數')
    } finally {
      useI18nStore.getState().setLocale('en')
    }
  })
})
