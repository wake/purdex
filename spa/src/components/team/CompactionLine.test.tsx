import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, createEvent, waitFor, act, cleanup } from '@testing-library/react'
import type { CompactionView, CompactionOverride } from '../../lib/team/compaction-api'

const getCompaction = vi.fn()
const putCompaction = vi.fn()
vi.mock('../../lib/team/compaction-api', () => ({
  getCompaction: (...a: unknown[]) => getCompaction(...a),
  putCompaction: (...a: unknown[]) => putCompaction(...a),
}))
import { CompactionLine } from './CompactionLine'

const NONE: CompactionOverride = { auto: null, threshold: null, idle: null }
const view = (eff: Partial<CompactionView['effective']> = {}, override: CompactionOverride = NONE): CompactionView => ({
  role: 'none', override, effective: { managed: true, auto: true, threshold: 70, idle: true, ...eff },
})
const pressed = (id: string) => screen.getByTestId(id).getAttribute('aria-pressed')
const lastPut = () => putCompaction.mock.calls.at(-1)![2]

beforeEach(() => { cleanup(); getCompaction.mockReset(); putCompaction.mockReset() })

async function mount(v: CompactionView) {
  getCompaction.mockResolvedValue(v)
  const r = render(<CompactionLine hostId="h1" sessionId="S1" />)
  await screen.findByTestId('compaction-dev')
  return r
}

describe('CompactionLine', () => {
  it('lights dev for auto/70/idle, discuss for auto off + idle off, custom otherwise', async () => {
    await mount(view())
    expect(pressed('compaction-dev')).toBe('true'); expect(pressed('compaction-discuss')).toBe('false')
    expect(screen.queryByTestId('compaction-custom')).toBeNull()
    cleanup()
    await mount(view({ auto: false, idle: false }))
    expect(pressed('compaction-discuss')).toBe('true'); expect(pressed('compaction-dev')).toBe('false')
    cleanup()
    await mount(view({ threshold: 50 }))
    expect(pressed('compaction-dev')).toBe('false'); expect(pressed('compaction-discuss')).toBe('false')
    expect(screen.getByTestId('compaction-custom')).toBeTruthy()
  })

  it('shows one sentence when not managed', async () => {
    getCompaction.mockResolvedValue(view({ managed: false, reason: 'self_relay' }))
    render(<CompactionLine hostId="h1" sessionId="S1" />)
    const el = await screen.findByTestId('compaction-unmanaged')
    expect(el.textContent).toContain('Self relay')
    expect(screen.queryByTestId('compaction-dev')).toBeNull()
    cleanup()
    getCompaction.mockResolvedValue(view({ managed: false, reason: 'member_auto' }))
    render(<CompactionLine hostId="h1" sessionId="S1" />)
    expect((await screen.findByTestId('compaction-unmanaged')).textContent).toContain('lead')
  })

  it('writes the four payloads', async () => {
    const ov = { auto: true, threshold: 40, idle: true }
    await mount(view({ threshold: 40 }, ov))
    putCompaction.mockResolvedValue(view({}, ov))
    fireEvent.click(screen.getByTestId('compaction-discuss'))
    await waitFor(() => expect(putCompaction).toHaveBeenCalledTimes(1))
    expect(lastPut()).toEqual({ auto: false, threshold: 40, idle: false })
    fireEvent.click(screen.getByTestId('compaction-dev'))
    await waitFor(() => expect(putCompaction).toHaveBeenCalledTimes(2))
    expect(lastPut()).toEqual({ auto: true, threshold: 70, idle: true })
    fireEvent.click(screen.getByTestId('compaction-idle'))
    await waitFor(() => expect(putCompaction).toHaveBeenCalledTimes(3))
    expect(lastPut()).toEqual({ auto: true, threshold: 40, idle: false })
    fireEvent.click(screen.getByTestId('compaction-threshold-plus'))
    await waitFor(() => expect(putCompaction).toHaveBeenCalledTimes(4))
    expect(lastPut()).toEqual({ auto: true, threshold: 45, idle: true })
    fireEvent.click(screen.getByTestId('compaction-follow-host'))
    await waitFor(() => expect(putCompaction).toHaveBeenCalledTimes(5))
    expect(lastPut()).toEqual(NONE)
  })

  it('discuss with no threshold override sends threshold null', async () => {
    await mount(view())
    putCompaction.mockResolvedValue(view())
    fireEvent.click(screen.getByTestId('compaction-discuss'))
    await waitFor(() => expect(putCompaction).toHaveBeenCalled())
    expect(lastPut()).toEqual({ auto: false, threshold: null, idle: false })
  })

  it('shows the GET after a PUT, not the PUT answer', async () => {
    await mount(view())
    putCompaction.mockResolvedValue(view({ auto: false, idle: false }))
    getCompaction.mockResolvedValue(view({ threshold: 50 })) // the lead changed it right after
    fireEvent.click(screen.getByTestId('compaction-discuss'))
    await waitFor(() => expect(getCompaction).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByTestId('compaction-threshold-value').textContent).toBe('50%'))
  })

  it('falls back to the PUT answer when the re-read fails', async () => {
    await mount(view())
    putCompaction.mockResolvedValue(view({ auto: false, idle: false }))
    getCompaction.mockRejectedValue(new Error('x'))
    fireEvent.click(screen.getByTestId('compaction-discuss'))
    await waitFor(() => expect(pressed('compaction-discuss')).toBe('true'))
  })

  it('shows follow-host only with an override', async () => {
    await mount(view())
    expect(screen.queryByTestId('compaction-follow-host')).toBeNull()
    cleanup()
    await mount(view({}, { auto: null, threshold: null, idle: false }))
    expect(screen.getByTestId('compaction-follow-host')).toBeTruthy()
  })

  it('drops a read that answers after the session changed', async () => {
    let resolveOld!: (v: CompactionView) => void
    getCompaction.mockImplementationOnce(() => new Promise((r) => { resolveOld = r }))
    getCompaction.mockResolvedValueOnce(view({ threshold: 30 }))
    const { rerender } = render(<CompactionLine hostId="h1" sessionId="S1" />)
    rerender(<CompactionLine hostId="h1" sessionId="S2" />)
    await waitFor(() => expect(screen.getByTestId('compaction-threshold-value').textContent).toBe('30%'))
    await act(async () => { resolveOld(view({ threshold: 90 })) })
    expect(screen.getByTestId('compaction-threshold-value').textContent).toBe('30%')
  })

  it('shows an error and keeps the value when the PUT fails', async () => {
    await mount(view())
    putCompaction.mockRejectedValue(new Error('boom'))
    fireEvent.click(screen.getByTestId('compaction-discuss'))
    await screen.findByTestId('compaction-error')
    expect(pressed('compaction-dev')).toBe('true')
  })

  it('the threshold stepper and the idle switch keep the terminal focus (mousedown is default-prevented)', async () => {
    await mount(view())
    for (const id of ['compaction-threshold-plus', 'compaction-threshold-minus', 'compaction-idle']) {
      const ev = createEvent.mouseDown(screen.getByTestId(id))
      fireEvent(screen.getByTestId(id), ev)
      expect(ev.defaultPrevented, id).toBe(true)
    }
  })

  it('a hung save of the last session does not block the next one', async () => {
    getCompaction.mockResolvedValueOnce(view())
    const { rerender } = render(<CompactionLine hostId="h1" sessionId="S1" />)
    await screen.findByTestId('compaction-dev')
    putCompaction.mockImplementationOnce(() => new Promise(() => {}))
    fireEvent.click(screen.getByTestId('compaction-discuss'))
    await waitFor(() => expect(putCompaction).toHaveBeenCalledTimes(1))
    getCompaction.mockResolvedValueOnce(view({ threshold: 30 }))
    rerender(<CompactionLine hostId="h1" sessionId="S2" />)
    await waitFor(() => expect(screen.getByTestId('compaction-threshold-value').textContent).toBe('30%'))
    putCompaction.mockResolvedValueOnce(view({ threshold: 35 }))
    getCompaction.mockResolvedValueOnce(view({ threshold: 35 }))
    fireEvent.click(screen.getByTestId('compaction-threshold-plus'))
    await waitFor(() => expect(putCompaction).toHaveBeenCalledTimes(2))
    expect(putCompaction.mock.calls[1][1]).toBe('S2')
    await waitFor(() => expect(screen.getByTestId('compaction-threshold-value').textContent).toBe('35%'))
  })

  it('two quick idle clicks flip twice, ending where they began', async () => {
    await mount(view())
    let release!: (v: CompactionView) => void
    putCompaction.mockImplementationOnce(() => new Promise((r) => { release = r }))
    fireEvent.click(screen.getByTestId('compaction-idle'))
    fireEvent.click(screen.getByTestId('compaction-idle'))
    await waitFor(() => expect(putCompaction).toHaveBeenCalledTimes(1))
    putCompaction.mockResolvedValueOnce(view())
    getCompaction.mockResolvedValue(view({ idle: false }, { auto: null, threshold: null, idle: false }))
    await act(async () => { release(view({ idle: false }, { auto: null, threshold: null, idle: false })) })
    await waitFor(() => expect(putCompaction).toHaveBeenCalledTimes(2))
    expect(lastPut().idle).toBe(true)
  })

  it('an idle click then dev ends at the dev set', async () => {
    await mount(view())
    let release!: (v: CompactionView) => void
    putCompaction.mockImplementationOnce(() => new Promise((r) => { release = r }))
    fireEvent.click(screen.getByTestId('compaction-idle'))
    fireEvent.click(screen.getByTestId('compaction-dev'))
    await waitFor(() => expect(putCompaction).toHaveBeenCalledTimes(1))
    putCompaction.mockResolvedValueOnce(view())
    getCompaction.mockResolvedValue(view())
    await act(async () => { release(view({ idle: false })) })
    await waitFor(() => expect(putCompaction).toHaveBeenCalledTimes(2))
    expect(lastPut()).toEqual({ auto: true, threshold: 70, idle: true })
  })
})
