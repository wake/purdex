// The live team's roots in the unattended panel (c9d9c7-55, #2561): the real section over the real roster / roots stores; only the PUT is faked.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { UnattendedQuotaSection } from '../UnattendedQuotaSection'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useRelayQuotaStore } from '../../lib/team/relay-quota'
import { useMaxMembersStore, teamKey } from '../../lib/team/max-members'
import { ApprovalApiError } from '../../lib/team/approval-api'
import { configureRoots, resetRoots, useRootsStore } from '../../lib/team/roots'
import type { TeamRoster } from '../../lib/team/roster'
import type { SessionQuota } from '../../lib/team/types'

const H = 'host-a'
const T = 't-L'
const lead = { session_id: 'L', ref: '_lead00', address: 'mlab/lead-xx', live: true }
const member = { session_id: 'M', ref: '_abc123', address: 'mlab/m-xx', live: true, name: 'worker-1', state: 'active', origin: 'spawned', joined_at: 1 }
const team = (over: Partial<TeamRoster> = {}): TeamRoster => ({ id: T, host_id: 'd', created_at: 1, team_name: '', team_label: '', lead, members: [member], roots: ['/Users/wake/a', '/b'], roots_rev: 3, ...over })
const row: SessionQuota = { session_id: 'L', root_session_id: 'root-L', title: 'T-L', address: 'mlab/lead-xx', is_lead: true, self_left: 0, member_pool_left: 0, rev: 1 }
const show = () => render(<UnattendedQuotaSection hosts={[{ hostId: H, rows: [row] }]} headings={false} />)
const roster = () => useTeamRosterStore.getState().byHost[H][0]
const err = (status: number, code: string, body: Record<string, unknown> = {}) => new ApprovalApiError(status, code, typeof body.detail === 'string' ? body.detail : '', null, { error: code, ...body })
const type = (v: string) => fireEvent.change(screen.getByTestId('roots-input'), { target: { value: v } })

beforeEach(() => {
  cleanup()
  resetRoots()
  configureRoots({ identity: () => 'id-1' })
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({ hosts: { [H]: { id: H, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [H], activeHostId: H, runtime: {} })
  useTeamRosterStore.getState().reset()
  useTeamRosterStore.getState().apply(H, [team()])
  useRelayQuotaStore.getState().reset()
  useUnattendedStore.getState().reset()
  useMaxMembersStore.getState().reset()
})

describe('the roots editor', () => {
  it('lists the roster\'s roots under the lead\'s row, the full path in the tooltip', () => {
    show()
    const paths = screen.getAllByTestId('roots-path')
    expect(paths.map((p) => p.textContent)).toEqual(['/Users/wake/a', '/b'])
    expect(paths[0]).toHaveAttribute('title', '/Users/wake/a')
  })

  it('is not drawn for a daemon without roots, nor under a member', () => {
    useTeamRosterStore.getState().apply(H, [team({ roots: undefined, roots_rev: undefined })])
    show()
    expect(screen.queryByTestId('roots-editor')).toBeNull()
  })

  it('add: sends the whole list (existing + new) with the roster\'s revision, then clears the box', async () => {
    const put = vi.fn(async () => ({ team_id: T, roots: ['/Users/wake/a', '/b', '/c'], roots_rev: 4 }))
    configureRoots({ put })
    show()
    type('  /c ')
    await act(async () => { fireEvent.click(screen.getByTestId('roots-add')) })
    expect(put).toHaveBeenCalledWith(H, T, ['/Users/wake/a', '/b', '/c'], 3)
    expect((screen.getByTestId('roots-input') as HTMLInputElement).value).toBe('')
    expect(screen.getAllByTestId('roots-path').map((p) => p.textContent)).toEqual(['/Users/wake/a', '/b', '/c']) // from the roster
  })

  it('remove: sends the list without that row', async () => {
    const put = vi.fn(async () => ({ team_id: T, roots: ['/Users/wake/a'], roots_rev: 4 }))
    configureRoots({ put })
    show()
    await act(async () => { fireEvent.click(screen.getAllByTestId('roots-remove')[1]) })
    expect(put).toHaveBeenCalledWith(H, T, ['/Users/wake/a'], 3)
    expect(screen.getAllByTestId('roots-row')).toHaveLength(1)
  })

  it('refuses a relative / empty / duplicate path before asking the daemon', async () => {
    const put = vi.fn()
    configureRoots({ put })
    show()
    type('rel/x')
    await act(async () => { fireEvent.click(screen.getByTestId('roots-add')) })
    expect(screen.getByTestId('roots-input-error')).toHaveTextContent('絕對路徑')
    type('/b')
    await act(async () => { fireEvent.click(screen.getByTestId('roots-add')) })
    expect(screen.getByTestId('roots-input-error')).toHaveTextContent('已在清單')
    expect(put).not.toHaveBeenCalled()
  })

  it('400 with a row field: the detail under that row; without one: under the input; the box keeps what was typed', async () => {
    configureRoots({ put: async () => { throw err(400, 'bad_request', { detail: 'not a directory', field: 'roots[2]' }) } })
    show()
    type('/nope')
    await act(async () => { fireEvent.click(screen.getByTestId('roots-add')) })
    expect(screen.getByTestId('roots-input-error')).toHaveTextContent('not a directory') // roots[2] is the row being added
    expect((screen.getByTestId('roots-input') as HTMLInputElement).value).toBe('/nope')
    configureRoots({ put: async () => { throw err(400, 'bad_request', { detail: 'gone', field: 'roots[1]' }) } })
    await act(async () => { fireEvent.click(screen.getAllByTestId('roots-remove')[0]) })
    expect(screen.getByTestId('roots-row-error')).toHaveTextContent('gone')
    expect(screen.getAllByTestId('roots-row')[1]).toContainElement(screen.getByTestId('roots-row-error'))
  })

  it('409 rev_mismatch: the list is refreshed from the answer and the person is told to do it again', async () => {
    configureRoots({ put: async () => { throw err(409, 'rev_mismatch', { detail: 'x', roots: ['/elsewhere'], roots_rev: 9 }) } })
    show()
    type('/c')
    await act(async () => { fireEvent.click(screen.getByTestId('roots-add')) })
    expect(screen.getAllByTestId('roots-path').map((p) => p.textContent)).toEqual(['/elsewhere'])
    expect(roster().roots_rev).toBe(9)
    expect(screen.getByTestId('roots-notice')).toHaveTextContent('已被別處更新')
  })

  it('409 roots_in_use: names the member (by its roster name), the spawn and the adoption by cwd; the list does not change', async () => {
    configureRoots({
      put: async () => {
        throw err(409, 'roots_in_use', { detail: 'x', in_use: [
          { root: '/b', kind: 'member', ref: '_abc123', cwd: '/b/x' }, { root: '/b', kind: 'spawn', cwd: '/b/y' }, { root: '/b', kind: 'adoption', cwd: '/b/z' },
        ] })
      },
    })
    show()
    await act(async () => { fireEvent.click(screen.getAllByTestId('roots-remove')[1]) })
    const box = screen.getByTestId('roots-in-use')
    expect(box).toHaveTextContent('/b')
    const lines = screen.getAllByTestId('roots-in-use-item').map((l) => l.textContent)
    expect(lines[0]).toContain('worker-1')
    expect(lines[0]).toContain('/b/x')
    expect(lines[1]).toContain('/b/y')
    expect(lines[2]).toContain('/b/z')
    expect(screen.getAllByTestId('roots-row')).toHaveLength(2)
  })

  it('404: says the team ended', async () => {
    configureRoots({ put: async () => { throw err(404, 'not_found') } })
    show()
    await act(async () => { fireEvent.click(screen.getAllByTestId('roots-remove')[0]) })
    expect(screen.getByTestId('roots-notice')).toHaveTextContent('已結束')
  })

  it('the controls are disabled while the request is out, and enabled again after', async () => {
    let release: (v: { team_id: string; roots: string[]; roots_rev: number }) => void = () => {}
    configureRoots({ put: () => new Promise((r) => { release = r }) })
    show()
    await act(async () => { fireEvent.click(screen.getAllByTestId('roots-remove')[0]) })
    expect(screen.getByTestId('roots-add')).toBeDisabled()
    expect(screen.getByTestId('roots-input')).toBeDisabled()
    screen.getAllByTestId('roots-remove').forEach((b) => expect(b).toBeDisabled())
    await act(async () => { release({ team_id: T, roots: ['/b'], roots_rev: 4 }) })
    expect(screen.getByTestId('roots-add')).not.toBeDisabled()
    expect(useRootsStore.getState().inflight[teamKey(H, T)]).toBeUndefined()
  })

  it('the path being typed survives the panel unmounting and coming back (tab switch)', () => {
    const first = show()
    type('/half/typed')
    first.unmount()
    show()
    expect((screen.getByTestId('roots-input') as HTMLInputElement).value).toBe('/half/typed')
  })
})
