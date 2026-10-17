import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { PauseReleaseButton } from './PauseReleaseButton'
import { UnattendedPausedSection } from './UnattendedPausedSection'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
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
  useTeamRosterStore.getState().reset()
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

describe('PauseReleaseButton: relay_unanswered confirm', () => {
  const click = () => { fireEvent.click(screen.getByTestId('pause-release')) }
  it('asks first when the causes hold relay_unanswered; nothing is sent until confirmed', async () => {
    post.mockResolvedValue({ kind: 'released', n: 1 })
    render(<PauseReleaseButton hostId="a" sessionId="S" causes={['quota', 'relay_unanswered']} />)
    click()
    await flush()
    expect(post).not.toHaveBeenCalled()
    expect(screen.getByTestId('pause-release-confirm-panel').textContent).toContain('解除後不接力，這個 session 繼續作業，之後不再申請接力')
    fireEvent.click(screen.getByTestId('pause-release-confirm-confirm'))
    await flush()
    expect(post).toHaveBeenCalledTimes(1)
    expect(post).toHaveBeenCalledWith('a', 'S')
    expect(screen.queryByTestId('pause-release-confirm-panel')).toBeNull()
  })
  it('cancel sends nothing and closes the dialog', async () => {
    render(<PauseReleaseButton hostId="a" sessionId="S" causes={['relay_unanswered']} />)
    click()
    fireEvent.click(screen.getByTestId('pause-release-confirm-cancel'))
    await flush()
    expect(post).not.toHaveBeenCalled()
    expect(screen.queryByTestId('pause-release-confirm-panel')).toBeNull()
  })
  it.each([[['quota']], [['lead']], [['quota', 'lead']], [undefined]])('causes %j: no confirm, the POST goes out at once', async (causes) => {
    post.mockResolvedValue({ kind: 'released', n: 1 })
    render(<PauseReleaseButton hostId="a" sessionId="S" causes={causes as never} />)
    click()
    await flush()
    expect(screen.queryByTestId('pause-release-confirm-panel')).toBeNull()
    expect(post).toHaveBeenCalledTimes(1)
  })
})

describe('UnattendedPausedSection', () => {
  it('relay_unanswered rows: the light text by role, and 解除 asks before it sends', async () => {
    usePausedStore.getState().set('a', [
      sess('1', { role: 'solo', causes: ['relay_unanswered'] }),
      sess('2', { role: 'member', causes: ['quota', 'relay_unanswered'], lead_ref: '_l' }),
    ], 1)
    render(<UnattendedPausedSection hostIds={['a']} />)
    const lights = screen.getAllByTestId('paused-light')
    expect(lights.map((l) => l.textContent).sort()).toEqual(expect.arrayContaining([
      expect.stringContaining('被擋下：接力申請沒有回應'),
      expect.stringContaining('被擋下：等待 lead 確認接力'),
    ]))
    fireEvent.click(screen.getAllByTestId('pause-release')[0])
    await flush()
    expect(post).not.toHaveBeenCalled()
    expect(screen.getByTestId('pause-release-confirm-panel')).toBeTruthy()
  })

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

describe('UnattendedPausedSection team names (U15)', () => {
  const roster = (lead: string, name: string) => ({ id: `t-${lead}`, host_id: 'd', created_at: 1, team_name: name, team_label: '', lead: { session_id: lead, ref: `_${lead}`, address: `m/${lead}`, live: true }, members: [] })

  it('a paused lead shows its team name (found on another host\'s roster), the title muted after it', () => {
    useTeamRosterStore.getState().apply('b', [roster('1', 'Payments')])
    usePausedStore.getState().set('a', [sess('1', { title: 'iface-lead', role: 'lead', causes: ['lead'] })], 1)
    render(<UnattendedPausedSection hostIds={['a']} />)
    const row = screen.getByTestId('paused-row')
    expect(row.textContent).toContain('alpha：Payments')
    expect(screen.getByTestId('paused-lead-title')).toHaveTextContent('iface-lead')
  })

  it('an empty team name shows the lead title; a solo row keeps title else ref', () => {
    useTeamRosterStore.getState().apply('a', [roster('1', '')])
    usePausedStore.getState().set('a', [sess('1', { title: 'iface-lead', role: 'lead' }), sess('2', { title: '' })], 1)
    render(<UnattendedPausedSection hostIds={['a']} />)
    const text = screen.getAllByTestId('paused-row').map((r) => r.textContent).join('|')
    expect(text).toContain('alpha：iface-lead')
    expect(text).toContain('alpha：_2')
    expect(screen.queryByTestId('paused-lead-title')).toBeNull()
  })
})
