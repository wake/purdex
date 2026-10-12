import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { PauseReleaseButton } from './PauseReleaseButton'
import { UnattendedPausedSection } from './UnattendedPausedSection'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { usePausedStore } from '../stores/usePausedStore'
import { postPauseRelease, type PausedSession, type ReleaseResult } from '../lib/team/paused-api'
import { refreshPaused } from '../lib/team/paused-refresh'

vi.mock('../lib/team/paused-api', async (orig) => ({ ...(await orig<typeof import('../lib/team/paused-api')>()), postPauseRelease: vi.fn() }))
vi.mock('../lib/team/paused-refresh', () => ({ refreshPaused: vi.fn(async () => {}) }))
const post = vi.mocked(postPauseRelease)
const refresh = vi.mocked(refreshPaused)

const flush = () => act(async () => { await new Promise<void>((r) => setTimeout(r, 0)) })
const sess = (id: string, over: Partial<PausedSession> = {}): PausedSession => ({ session_id: id, ref: `_${id}`, title: `t-${id}`, role: 'solo', causes: ['quota'], since: 1, lead_ref: '', bounced: 0, ...over })

beforeEach(() => {
  post.mockReset()
  refresh.mockClear()
  useI18nStore.getState().setLocale('zh-TW')
  usePausedStore.getState().reset()
  useHostStore.setState({
    hosts: { a: { id: 'a', name: 'alpha', ip: '1', port: 1, token: 't', order: 0 }, b: { id: 'b', name: 'beta', ip: '2', port: 1, token: 't', order: 1 } } as never,
    hostOrder: ['a', 'b'], runtime: {},
  })
})

describe('PauseReleaseButton', () => {
  const press = async () => { fireEvent.click(screen.getByTestId('pause-release')); await flush() }
  it('200 -> re-reads the list', async () => {
    post.mockResolvedValue({ kind: 'released', n: 1 })
    render(<PauseReleaseButton hostId="a" sessionId="S" />)
    await press()
    expect(post).toHaveBeenCalledWith('a', 'S')
    expect(refresh).toHaveBeenCalledWith('a')
    expect(screen.queryByTestId('pause-release-error')).toBeNull()
  })
  it('404 not_paused -> quiet re-read, no error', async () => {
    post.mockResolvedValue({ kind: 'not_paused' })
    render(<PauseReleaseButton hostId="a" sessionId="S" />)
    await press()
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('pause-release-error')).toBeNull()
  })
  it('400 -> error beside the button, button usable again, no re-read', async () => {
    post.mockResolvedValue({ kind: 'bad_claim', detail: 'bad label' })
    render(<PauseReleaseButton hostId="a" sessionId="S" />)
    await press()
    expect(screen.getByTestId('pause-release-error').textContent).toContain('bad label')
    expect(screen.getByTestId('pause-release')).not.toBeDisabled()
    expect(refresh).not.toHaveBeenCalled()
  })
  it('network error -> error shown, button enabled again', async () => {
    post.mockResolvedValue({ kind: 'error', message: 'down' })
    render(<PauseReleaseButton hostId="a" sessionId="S" />)
    await press()
    expect(screen.getByTestId('pause-release-error').textContent).toContain('down')
    expect(screen.getByTestId('pause-release')).not.toBeDisabled()
  })
  it('a double click sends one POST, and the button is disabled while sending', async () => {
    let done!: (r: ReleaseResult) => void
    post.mockImplementation(() => new Promise((r) => { done = r }))
    render(<PauseReleaseButton hostId="a" sessionId="S" />)
    const b = screen.getByTestId('pause-release')
    fireEvent.click(b)
    fireEvent.click(b)
    expect(post).toHaveBeenCalledTimes(1)
    expect(b).toBeDisabled()
    await act(async () => { done({ kind: 'released', n: 1 }) })
    expect(b).not.toBeDisabled()
  })
})

describe('UnattendedPausedSection', () => {
  it('renders rows from two hosts, the light text, and a lead hint only for role=lead', () => {
    usePausedStore.getState().set('a', [sess('1', { title: 'one', bounced: 2 })], 1)
    usePausedStore.getState().set('b', [sess('2', { title: '', role: 'lead', causes: ['lead'] })], 1)
    render(<UnattendedPausedSection hostIds={['a', 'b']} />)
    const rows = screen.getAllByTestId('paused-row')
    expect(rows).toHaveLength(2)
    expect(rows[0].textContent).toContain('alpha：one')
    expect(rows[0].textContent).toContain('被擋下：接力額度用完・退回 2 則')
    expect(rows[1].textContent).toContain('beta：_2')
    expect(rows[1].textContent).toContain('被擋下：lead 已暫停')
    expect(screen.getAllByTestId('paused-lead-hint')).toHaveLength(1)
    expect(screen.getAllByTestId('pause-release')).toHaveLength(2)
  })
  it('shows nothing when no listed host has a paused session', () => {
    usePausedStore.getState().set('b', [sess('2')], 1)
    const { container } = render(<UnattendedPausedSection hostIds={['a']} />)
    expect(container).toBeEmptyDOMElement()
  })
})
