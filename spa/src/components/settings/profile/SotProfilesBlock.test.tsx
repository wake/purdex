import { useLayoutEffect } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import en from '../../../locales/en.json'
import { SotProfilesBlock } from './SotProfilesBlock'
import { useSotProfiles } from './useSotProfiles'
import { useHostStore } from '../../../stores/useHostStore'
import { deleteProfile, listProfiles, renameProfile } from '../../../lib/profile/api'
import type { Attachment, DeleteProfileOutcome, Failure, ProfileIndexEntry } from '../../../lib/profile/api'

vi.mock('../../../lib/profile/api', () => ({ listProfiles: vi.fn(), renameProfile: vi.fn(), deleteProfile: vi.fn() }))

const attachment = (clientId: string, deviceName: string): Attachment => ({ clientId, profileId: 'p', deviceName, attachedAt: 1, lastSeen: 2 })
const profile = (id: string, name: string, attachments: Attachment[] = []): ProfileIndexEntry => ({ id, name, createdAt: 1, updatedAt: 2, sections: [], attachments })
const failure = (message: string, reason: Failure['reason'] = 'network'): Failure => ({ kind: 'failed', reason, status: 0, message })
const rows = (...list: ProfileIndexEntry[]) => vi.mocked(listProfiles).mockResolvedValue({ kind: 'ok', value: list })

/**
 * THE A→B→A RACE (PR #1340 re-review): the list is rendered at A, the host is moved to B before the fetch leaves (a
 * layout effect stands in for "another window, between the render and the effect"), and back to A before React
 * renders again — so no render ever sees B. `listProfiles` answers as the machine at the address it would really
 * reach: the one the host is at WHEN IT IS CALLED, and — like api.ts — refuses when `expectEndpoint` is not that.
 */
function raceAtoBtoA(machines: Record<string, ProfileIndexEntry[]>, a: string, b: string) {
  const at = (ip: string) => useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: { ...s.hosts.h1, ip } } }))
  const where = () => `${useHostStore.getState().hosts.h1.ip}:${useHostStore.getState().hosts.h1.port}`
  let moved = false
  vi.mocked(listProfiles).mockImplementation(async (hostId: string, opts?: { expectEndpoint?: string }) => {
    const reached = where()
    if (moved && reached === `${b}:7860`) at(a) // …and back, before anything renders again
    if (opts?.expectEndpoint !== undefined && opts.expectEndpoint !== reached) return { kind: 'failed', reason: 'endpoint-changed', status: 0, message: 'moved' }
    return { kind: 'ok', value: hostId === 'h1' ? (machines[reached] ?? []) : [] }
  })
  /** Rendered after the component under test: its layout effect runs after that component's render, before its effects. */
  function MoveToB() {
    useLayoutEffect(() => {
      if (moved) return
      moved = true
      at(b)
    }, [])
    return null
  }
  return MoveToB
}

/** The section's wiring: one fetch, handed to the block. */
function Harness({ hostId = 'h1', attached = 'p1' }: { hostId?: string; attached?: string }) {
  const { view, reload } = useSotProfiles(hostId)
  return view === null ? null : <SotProfilesBlock hostId={hostId} attachedProfileId={attached} view={view} reload={reload} />
}

const ready = () => waitFor(() => expect(screen.getByTestId('profile-sot-block')).not.toHaveAttribute('data-state', 'loading'))

/** Where h1 is: every request to it is pinned to this (useSotProfiles / useSotDelete never send unpinned). */
const H1 = { expectEndpoint: '10.0.0.1:7860' }

beforeEach(() => {
  for (const fn of [listProfiles, renameProfile, deleteProfile]) vi.mocked(fn).mockReset()
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '10.0.0.1', port: 7860, order: 0 }, h2: { id: 'h2', name: 'air', ip: '10.0.0.2', port: 7860, order: 1 } }, hostOrder: ['h1', 'h2'] })
})

afterEach(cleanup)

describe('the list: loading, failed, empty, rows', () => {
  it('loading until the host answers', async () => {
    let answer: (v: { kind: 'ok'; value: ProfileIndexEntry[] }) => void = () => {}
    vi.mocked(listProfiles).mockReturnValue(new Promise((resolve) => { answer = resolve }))
    render(<Harness />)
    expect(listProfiles).toHaveBeenCalledWith('h1', H1)
    expect(screen.getByTestId('profile-sot-block')).toHaveAttribute('data-state', 'loading')
    expect(screen.getByTestId('profile-sot-loading')).toBeInTheDocument()
    await act(async () => { answer({ kind: 'ok', value: [profile('p1', 'default')] }) })
    expect(screen.getByTestId('profile-sot-block')).toHaveAttribute('data-state', 'rows')
    expect(screen.getByTestId('profile-sot-name-p1')).toHaveTextContent('default')
  })

  it('a failure is said, with a retry that asks again', async () => {
    vi.mocked(listProfiles).mockResolvedValueOnce(failure('connection refused'))
    render(<Harness />)
    await ready()
    expect(screen.getByTestId('profile-sot-block')).toHaveAttribute('data-state', 'error')
    expect(screen.getByTestId('profile-sot-error')).toHaveTextContent('connection refused')
    rows(profile('p1', 'default'))
    fireEvent.click(screen.getByTestId('profile-sot-retry'))
    await waitFor(() => expect(screen.getByTestId('profile-sot-name-p1')).toBeInTheDocument())
    expect(listProfiles).toHaveBeenCalledTimes(2)
  })

  it('a rejected request is a failure too', async () => {
    vi.mocked(listProfiles).mockRejectedValue(new Error('boom'))
    render(<Harness />)
    await ready()
    expect(screen.getByTestId('profile-sot-error')).toHaveTextContent('boom')
  })

  it('no profile on the host', async () => {
    rows()
    render(<Harness />)
    await ready()
    expect(screen.getByTestId('profile-sot-block')).toHaveAttribute('data-state', 'empty')
    expect(screen.getByTestId('profile-sot-empty')).toHaveTextContent(en['settings.profile.sot.empty'])
  })

  it('the one this device syncs with is marked; attached devices are named', async () => {
    rows(profile('p1', 'default', [attachment('c1', 'Mini'), attachment('c2', 'Air')]), profile('p2', 'experiment'))
    render(<Harness />)
    await ready()
    expect(screen.getByTestId('profile-sot-current-p1')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-sot-current-p2')).toBeNull()
    expect(screen.getByTestId('profile-sot-devices-p1')).toHaveTextContent('Mini')
    expect(screen.getByTestId('profile-sot-devices-p1')).toHaveTextContent('Air')
    expect(screen.queryByTestId('profile-sot-devices-p2')).toBeNull()
  })
})

describe('delete — only what the FETCHED index shows nobody attached to', () => {
  it('attachments → disabled, and the row says who', async () => {
    rows(profile('p2', 'experiment', [attachment('c2', 'Air')]))
    render(<Harness />)
    await ready()
    expect(screen.getByTestId('profile-sot-delete-p2')).toBeDisabled()
    expect(screen.getByTestId('profile-sot-delete-blocked-p2')).toHaveTextContent(en['settings.profile.sot.delete_blocked_attached'])
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    expect(screen.queryByTestId('profile-sot-delete-dialog')).toBeNull()
  })

  it('the one this device syncs with → disabled even if the index lists nobody: stop sync first', async () => {
    rows(profile('p1', 'default'))
    render(<Harness />)
    await ready()
    expect(screen.getByTestId('profile-sot-delete-p1')).toBeDisabled()
    expect(screen.getByTestId('profile-sot-delete-blocked-p1')).toHaveTextContent(en['settings.profile.sot.delete_blocked_current'])
  })

  it('nobody attached → asks first; Cancel deletes nothing', async () => {
    rows(profile('p2', 'experiment'))
    render(<Harness />)
    await ready()
    expect(screen.getByTestId('profile-sot-delete-p2')).toBeEnabled()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    expect(screen.getByTestId('profile-sot-delete-dialog')).toHaveTextContent('experiment')
    fireEvent.click(screen.getByTestId('profile-sot-delete-cancel'))
    expect(screen.queryByTestId('profile-sot-delete-dialog')).toBeNull()
    expect(deleteProfile).not.toHaveBeenCalled()
  })

  it('Confirm deletes it on the host, busy meanwhile, and the list is fetched again', async () => {
    rows(profile('p2', 'experiment'))
    let answer: (v: DeleteProfileOutcome) => void = () => {}
    vi.mocked(deleteProfile).mockReturnValue(new Promise((resolve) => { answer = resolve }))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    fireEvent.click(screen.getByTestId('profile-sot-delete-confirm'))
    expect(deleteProfile).toHaveBeenCalledWith('h1', 'p2', H1)
    expect(screen.getByTestId('profile-sot-delete-confirm')).toBeDisabled()
    rows()
    await act(async () => { answer({ kind: 'deleted' }) })
    expect(screen.queryByTestId('profile-sot-delete-dialog')).toBeNull()
    await waitFor(() => expect(screen.getByTestId('profile-sot-empty')).toBeInTheDocument())
  })

  it('409 attached: the devices still attached are listed by name', async () => {
    rows(profile('p2', 'experiment'))
    vi.mocked(deleteProfile).mockResolvedValue({ kind: 'attached', attachments: [attachment('c2', 'Air'), attachment('c3', 'Studio')] })
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    fireEvent.click(screen.getByTestId('profile-sot-delete-confirm'))
    const refused = await screen.findByTestId('profile-sot-attached-p2')
    expect(refused).toHaveTextContent('Air')
    expect(refused).toHaveTextContent('Studio')
    expect(screen.queryByTestId('profile-sot-delete-dialog')).toBeNull()
    expect(listProfiles).toHaveBeenCalledTimes(2) // and the index is asked again: it was out of date
  })

  it('409 attached with nobody listed (they have just left): said as that', async () => {
    rows(profile('p2', 'experiment'))
    vi.mocked(deleteProfile).mockResolvedValue({ kind: 'attached', attachments: [] })
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    fireEvent.click(screen.getByTestId('profile-sot-delete-confirm'))
    expect(await screen.findByTestId('profile-sot-attached-p2')).toHaveTextContent(en['settings.profile.sot.attached_none'])
  })

  it('a failed delete is said with the host\'s own words', async () => {
    rows(profile('p2', 'experiment'))
    vi.mocked(deleteProfile).mockResolvedValue(failure('disk full', 'server'))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    fireEvent.click(screen.getByTestId('profile-sot-delete-confirm'))
    expect(await screen.findByTestId('profile-sot-status')).toHaveTextContent('disk full')
  })
})

describe('rename', () => {
  it('renames on the host and fetches the list again; the attached one can be renamed too', async () => {
    rows(profile('p1', 'default'))
    vi.mocked(renameProfile).mockResolvedValue({ kind: 'ok', value: { id: 'p1', name: 'work' } })
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-rename-p1'))
    const input = screen.getByTestId('profile-sot-rename-input') as HTMLInputElement
    expect(input.value).toBe('default')
    fireEvent.change(input, { target: { value: '  work  ' } })
    rows(profile('p1', 'work'))
    fireEvent.click(screen.getByTestId('profile-sot-rename-save'))
    expect(renameProfile).toHaveBeenCalledWith('h1', 'p1', 'work', H1)
    await waitFor(() => expect(screen.getByTestId('profile-sot-name-p1')).toHaveTextContent('work'))
    expect(screen.queryByTestId('profile-sot-rename-input')).toBeNull()
  })

  it('a blank or unchanged name cannot be sent; Cancel sends nothing', async () => {
    rows(profile('p1', 'default'))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-rename-p1'))
    expect(screen.getByTestId('profile-sot-rename-save')).toBeDisabled()
    fireEvent.change(screen.getByTestId('profile-sot-rename-input'), { target: { value: '   ' } })
    expect(screen.getByTestId('profile-sot-rename-save')).toBeDisabled()
    fireEvent.click(screen.getByTestId('profile-sot-rename-cancel'))
    expect(screen.queryByTestId('profile-sot-rename-input')).toBeNull()
    expect(renameProfile).not.toHaveBeenCalled()
  })

  it('a failed rename is said, and the input stays', async () => {
    rows(profile('p1', 'default'))
    vi.mocked(renameProfile).mockResolvedValue(failure('name taken', 'rejected'))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-rename-p1'))
    fireEvent.change(screen.getByTestId('profile-sot-rename-input'), { target: { value: 'work' } })
    fireEvent.keyDown(screen.getByTestId('profile-sot-rename-input'), { key: 'Enter' })
    expect(await screen.findByTestId('profile-sot-status')).toHaveTextContent('name taken')
    expect(screen.getByTestId('profile-sot-rename-input')).toBeInTheDocument()
  })
})

describe('Refresh', () => {
  it('asks the host again', async () => {
    rows(profile('p1', 'default'))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-refresh'))
    await waitFor(() => expect(listProfiles).toHaveBeenCalledTimes(2))
  })

  it('no host, no request', () => {
    function NoHost() {
      const { view } = useSotProfiles(null)
      return <span data-testid="view">{String(view)}</span>
    }
    render(<NoHost />)
    expect(screen.getByTestId('view')).toHaveTextContent('null')
    expect(listProfiles).not.toHaveBeenCalled()
  })

  it('a host that is in no store has no address to pin to: nothing is asked, nothing is offered — until it appears, then it is asked PINNED (PR #1340 re-review)', async () => {
    rows(profile('p2', 'experiment'))
    useHostStore.setState({ hosts: {}, hostOrder: [] })
    render(<Harness />)
    await act(async () => {})
    expect(listProfiles).not.toHaveBeenCalled()
    expect(screen.queryByTestId('profile-sot-block')).toBeNull()
    expect(screen.queryByTestId('profile-sot-delete-p2')).toBeNull()
    expect(screen.queryByTestId('profile-sot-rename-p2')).toBeNull()
    act(() => useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '10.0.0.1', port: 7860, order: 0 } }, hostOrder: ['h1'] }))
    await ready()
    expect(listProfiles).toHaveBeenCalledTimes(1)
    expect(listProfiles).toHaveBeenCalledWith('h1', H1)
    expect(screen.getByTestId('profile-sot-delete-p2')).toBeEnabled()
  })

  it('… and when it leaves the store again, what was listed goes with it: nothing left to act on', async () => {
    rows(profile('p2', 'experiment'))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    act(() => useHostStore.setState({ hosts: {}, hostOrder: [] }))
    expect(screen.queryByTestId('profile-sot-delete-dialog')).toBeNull()
    expect(screen.queryByTestId('profile-sot-block')).toBeNull()
    expect(deleteProfile).not.toHaveBeenCalled()
  })
})

describe('an action belongs to the host it was started on (the master can move under an open page)', () => {
  const byHost = (map: Record<string, ProfileIndexEntry[]>) =>
    vi.mocked(listProfiles).mockImplementation((hostId: string) => Promise.resolve({ kind: 'ok', value: map[hostId] ?? [] }))

  it('the delete confirmation closes the moment the host changes — and nothing is deleted on the new host', async () => {
    byHost({ h1: [profile('p1', 'default'), profile('p2', 'experiment')], h2: [profile('p9', 'other'), profile('p2', 'same id, other host')] })
    const { rerender } = render(<Harness hostId="h1" attached="p1" />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    expect(screen.getByTestId('profile-sot-delete-dialog')).toBeInTheDocument()
    rerender(<Harness hostId="h2" attached="p9" />)
    expect(screen.queryByTestId('profile-sot-delete-dialog')).toBeNull()
    await waitFor(() => expect(screen.getByTestId('profile-sot-name-p9')).toBeInTheDocument())
    expect(screen.queryByTestId('profile-sot-delete-dialog')).toBeNull()
    expect(deleteProfile).not.toHaveBeenCalled()
  })

  it('… so does the rename editor, and its draft is gone', async () => {
    byHost({ h1: [profile('p1', 'default'), profile('p2', 'experiment')], h2: [profile('p9', 'other'), profile('p2', 'same id, other host')] })
    const { rerender } = render(<Harness hostId="h1" attached="p1" />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-rename-p2'))
    fireEvent.change(screen.getByTestId('profile-sot-rename-input'), { target: { value: 'typed on h1' } })
    rerender(<Harness hostId="h2" attached="p9" />)
    expect(screen.queryByTestId('profile-sot-rename-input')).toBeNull()
    await waitFor(() => expect(screen.getByTestId('profile-sot-name-p9')).toBeInTheDocument())
    expect(screen.queryByTestId('profile-sot-rename-input')).toBeNull()
    expect(renameProfile).not.toHaveBeenCalled()
  })

  it('the same host, another profile attached: open actions close too', async () => {
    byHost({ h1: [profile('p1', 'default'), profile('p2', 'experiment')] })
    const { rerender } = render(<Harness hostId="h1" attached="p1" />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    rerender(<Harness hostId="h1" attached="p2" />)
    expect(screen.queryByTestId('profile-sot-delete-dialog')).toBeNull()
    expect(screen.getByTestId('profile-sot-delete-p2')).toBeDisabled()
  })

  it('checked again when it is sent: a profile that has left the list meanwhile is not deleted, and that is said', async () => {
    rows(profile('p1', 'default'), profile('p2', 'experiment'))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    rows(profile('p1', 'default'))
    fireEvent.click(screen.getByTestId('profile-sot-refresh'))
    await waitFor(() => expect(screen.queryByTestId('profile-sot-row-p2')).toBeNull())
    fireEvent.click(screen.getByTestId('profile-sot-delete-confirm'))
    expect(deleteProfile).not.toHaveBeenCalled()
    expect(screen.queryByTestId('profile-sot-delete-dialog')).toBeNull()
    expect(screen.getByTestId('profile-sot-status')).toHaveTextContent(en['settings.profile.sot.stale_action'])
  })

  it('… and so is a rename', async () => {
    rows(profile('p1', 'default'), profile('p2', 'experiment'))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-rename-p2'))
    fireEvent.change(screen.getByTestId('profile-sot-rename-input'), { target: { value: 'late' } })
    const save = screen.getByTestId('profile-sot-rename-save')
    rows(profile('p1', 'default'))
    fireEvent.click(screen.getByTestId('profile-sot-refresh'))
    await waitFor(() => expect(screen.queryByTestId('profile-sot-row-p2')).toBeNull())
    // The row went, and its editor with it: there is nothing left to send from.
    expect(save).not.toBeInTheDocument()
    expect(renameProfile).not.toHaveBeenCalled()
  })

  it('an answer that arrives after the host changed says nothing on the new host\'s page', async () => {
    byHost({ h1: [profile('p1', 'default'), profile('p2', 'experiment')], h2: [profile('p9', 'other')] })
    let answer: (v: DeleteProfileOutcome) => void = () => {}
    vi.mocked(deleteProfile).mockReturnValue(new Promise((resolve) => { answer = resolve }))
    const { rerender } = render(<Harness hostId="h1" attached="p1" />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    fireEvent.click(screen.getByTestId('profile-sot-delete-confirm'))
    rerender(<Harness hostId="h2" attached="p9" />)
    await waitFor(() => expect(screen.getByTestId('profile-sot-name-p9')).toBeInTheDocument())
    await act(async () => { answer({ kind: 'failed', reason: 'server', status: 500, message: 'h1 said no' }) })
    expect(screen.queryByTestId('profile-sot-status')).toBeNull()
    expect(screen.getByTestId('profile-sot-delete-p9')).toBeDisabled() // the attached one; and not stuck busy:
    expect(screen.getByTestId('profile-sot-rename-p9')).toBeEnabled()
  })
})

describe('useSotProfiles — one host\'s answer never paints another host\'s list', () => {
  it('a slow answer of the previous host is dropped', async () => {
    let answerH1: (v: { kind: 'ok'; value: ProfileIndexEntry[] }) => void = () => {}
    vi.mocked(listProfiles).mockImplementation((hostId: string) =>
      hostId === 'h1' ? new Promise((resolve) => { answerH1 = resolve }) : Promise.resolve({ kind: 'ok', value: [profile('p9', 'other')] }))
    const { rerender } = render(<Harness hostId="h1" attached="p1" />)
    rerender(<Harness hostId="h2" attached="p9" />)
    await waitFor(() => expect(screen.getByTestId('profile-sot-name-p9')).toBeInTheDocument())
    await act(async () => { answerH1({ kind: 'ok', value: [profile('p1', 'default')] }) })
    expect(screen.getByTestId('profile-sot-name-p9')).toBeInTheDocument()
    expect(screen.queryByTestId('profile-sot-name-p1')).toBeNull()
    expect(screen.getByTestId('profile-sot-block')).toHaveAttribute('data-state', 'rows')
  })

  it('until the new host answers, the previous host\'s list is NOT shown as its list: loading', async () => {
    vi.mocked(listProfiles).mockImplementation((hostId: string) =>
      hostId === 'h1' ? Promise.resolve({ kind: 'ok', value: [profile('p1', 'default'), profile('p2', 'experiment')] }) : new Promise(() => {}))
    const { rerender } = render(<Harness hostId="h1" attached="p1" />)
    await ready()
    rerender(<Harness hostId="h2" attached="p9" />)
    expect(screen.getByTestId('profile-sot-block')).toHaveAttribute('data-state', 'loading')
    expect(screen.queryByTestId('profile-sot-row-p2')).toBeNull()
  })

  it('an answer after the page closed sets nothing', async () => {
    let answer: (v: { kind: 'ok'; value: ProfileIndexEntry[] }) => void = () => {}
    vi.mocked(listProfiles).mockReturnValue(new Promise((resolve) => { answer = resolve }))
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    const { unmount } = render(<Harness />)
    unmount()
    await act(async () => { answer({ kind: 'ok', value: [] }) })
    expect(errors).not.toHaveBeenCalled()
    errors.mockRestore()
  })
})

describe('an action belongs to the ADDRESS the list was fetched from, too — the same host id may move (PR #1340 review)', () => {
  const A = '10.0.0.1:7860'
  const hostAt = (ip: string) => act(() => useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip, port: 7860, order: 0 } } }))

  beforeEach(() => {
    useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '10.0.0.1', port: 7860, order: 0 } }, hostOrder: ['h1'] })
  })
  afterEach(() => {
    useHostStore.setState({ hosts: {}, hostOrder: [] })
  })

  it('h1\'s address changes while the delete confirmation is open: it closes in that render, nothing is sent, h1 is listed again', async () => {
    rows(profile('p1', 'default'), profile('p2', 'experiment'))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    hostAt('10.0.0.99')
    expect(screen.queryByTestId('profile-sot-delete-dialog')).toBeNull()
    await waitFor(() => expect(listProfiles).toHaveBeenCalledTimes(2))
    await ready()
    expect(screen.queryByTestId('profile-sot-delete-dialog')).toBeNull()
    expect(deleteProfile).not.toHaveBeenCalled()
  })

  it('… and so does the rename editor', async () => {
    rows(profile('p1', 'default'), profile('p2', 'experiment'))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-rename-p2'))
    hostAt('10.0.0.99')
    expect(screen.queryByTestId('profile-sot-rename-input')).toBeNull()
    expect(renameProfile).not.toHaveBeenCalled()
  })

  it('the delete is pinned to the address; a move after the check → refused by the api, nothing deleted, said in a sentence', async () => {
    rows(profile('p1', 'default'), profile('p2', 'experiment'))
    vi.mocked(deleteProfile).mockResolvedValue(failure('host h1 is not at 10.0.0.1:7860 any more', 'endpoint-changed'))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-delete-p2'))
    fireEvent.click(screen.getByTestId('profile-sot-delete-confirm'))
    expect(deleteProfile).toHaveBeenCalledWith('h1', 'p2', { expectEndpoint: A })
    expect(await screen.findByTestId('profile-sot-status')).toHaveTextContent(en['settings.profile.sot.endpoint_changed'])
    await waitFor(() => expect(listProfiles).toHaveBeenCalledTimes(2))
  })

  it('A→B→A between the render and the fetch: B\'s list is NEVER shown as A\'s (the list call is pinned too)', async () => {
    const MoveToB = raceAtoBtoA({ '10.0.0.1:7860': [profile('p2', 'on A')], '10.0.0.66:7860': [profile('p2', 'on B')] }, '10.0.0.1', '10.0.0.66')
    render(<><Harness /><MoveToB /></>)
    await waitFor(() => expect(screen.getByTestId('profile-sot-block')).toHaveTextContent('on A'))
    expect(screen.getByTestId('profile-sot-block')).not.toHaveTextContent('on B')
    expect(listProfiles).toHaveBeenCalledWith('h1', { expectEndpoint: A })
  })

  it('the rename is pinned to the address too; refused likewise', async () => {
    rows(profile('p1', 'default'))
    vi.mocked(renameProfile).mockResolvedValue(failure('host h1 is not at 10.0.0.1:7860 any more', 'endpoint-changed'))
    render(<Harness />)
    await ready()
    fireEvent.click(screen.getByTestId('profile-sot-rename-p1'))
    fireEvent.change(screen.getByTestId('profile-sot-rename-input'), { target: { value: 'work' } })
    fireEvent.click(screen.getByTestId('profile-sot-rename-save'))
    expect(renameProfile).toHaveBeenCalledWith('h1', 'p1', 'work', { expectEndpoint: A })
    expect(await screen.findByTestId('profile-sot-status')).toHaveTextContent(en['settings.profile.sot.endpoint_changed'])
  })
})
