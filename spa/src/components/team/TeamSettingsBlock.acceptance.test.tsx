// spa/src/components/team/TeamSettingsBlock.acceptance.test.tsx — the 761 acceptance on the team settings block: label tone, the read-only folders line
// (chips, a pencil that opens the editor), the other hosts' applied grants as chips, and the divider between the editor's two sections.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { act } from 'react'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { TeamSettingsBlock } from './TeamSettingsBlock'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useRelayQuotaStore } from '../../lib/team/relay-quota'
import { configureRoots, resetRoots } from '../../lib/team/roots'
import { configureHostRoots, resetHostRoots, type PairedHostRoots, type HostRootChange } from '../../lib/team/host-roots'
import type { RosterSession, TeamRoster } from '../../lib/team/roster'

const A = 'host-a'
const sess = (id: string): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true })
const team = (): TeamRoster => ({
  id: 't-l1', host_id: 'd', created_at: 1, team_name: '', team_label: '', lead: sess('l1'), members: [], roots: ['/Users/wake/a', '/b'], roots_rev: 3, max_members: 4, in_use: 1,
})
const host = (over: Partial<PairedHostRoots> = {}): PairedHostRoots => ({ hostId: 'h-air', alias: 'air26', status: 'ok', roots: [], changes: [], ...over })
const ch = (over: Partial<HostRootChange> = {}): HostRootChange => ({ id: 'c1', op: 'add', path: '/x', state: 'pending', code: '', detail: '', ...over })
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve() })

let hosts: PairedHostRoots[]
const show = async () => { render(<TeamSettingsBlock hostId={A} teamId="t-l1" name="Team" />); await flush() }

beforeEach(() => {
  cleanup()
  resetRoots()
  resetHostRoots()
  hosts = [host({ roots: ['/Users/wake/Workspace/ios'], changes: [ch({ id: 'p', path: '/pending-path' }), ch({ id: 'r', path: '/refused-path', state: 'refused' })] })]
  configureRoots({ identity: () => 'id-1' })
  configureHostRoots({ identity: () => 'id-1', fetch: async () => hosts, post: async () => {}, dismiss: async () => {} })
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({ hosts: { [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [A], activeHostId: A, runtime: {} })
  useTeamRosterStore.getState().reset()
  useTeamRosterStore.getState().apply(A, [team()])
  useRelayQuotaStore.getState().reset()
  useUnattendedStore.getState().reset()
  useUnattendedStore.getState().setMaxMembersSupport(A, 'yes')
})
afterEach(() => { cleanup() })

describe('label tone', () => {
  it('the settings labels are text-secondary (the values keep their own colour)', async () => {
    await show()
    const line = screen.getByTestId('team-settings-folders')
    expect(within(line).getByText('資料夾').className).toContain('text-text-secondary')
    expect(within(line).getByText('資料夾').className).not.toContain('text-text-muted')
    expect(within(screen.getByTestId('cap-stepper')).getByText('成員上限').className).toContain('text-text-secondary')
    expect(screen.getByTestId('team-settings-in-use').className).toContain('text-text-muted')
  })
  it('the editor\'s two section labels are text-secondary too', async () => {
    await show()
    fireEvent.click(screen.getByTestId('team-folder-edit'))
    await flush()
    const editor = screen.getByTestId('roots-editor')
    expect(within(editor).getByText('成員可啟動的目錄').className).toContain('text-text-secondary')
    expect(within(screen.getByTestId('host-roots')).getByText('其他主機的資料夾').className).toContain('text-text-secondary')
  })
})

describe('the read-only folders line', () => {
  it('chips carry no remove button, and there is no dashed +; a pencil with an i18n label opens the editor', async () => {
    await show()
    const line = screen.getByTestId('team-settings-folders')
    expect(within(line).queryByTestId('team-folder-add')).toBeNull()
    expect(within(line).queryByTestId('roots-remove')).toBeNull()
    const pencil = within(line).getByTestId('team-folder-edit')
    expect(pencil).toHaveAttribute('aria-label', '編輯資料夾')
    expect(pencil).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByTestId('roots-editor')).toBeNull()
    fireEvent.click(pencil)
    expect(pencil).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByTestId('roots-editor')).toBeInTheDocument()
    // chips stay read-only with the editor open: removal is in the editor's rows
    expect(within(line).getAllByTestId('team-folder-chip').every((c) => c.querySelector('button') === null)).toBe(true)
  })
  it('a divider (border-subtle) sits between 成員可啟動的目錄 and 其他主機的資料夾', async () => {
    await show()
    fireEvent.click(screen.getByTestId('team-folder-edit'))
    await flush()
    const divider = screen.getByTestId('roots-section-divider')
    expect(divider.className).toContain('border-border-subtle')
    const kids = Array.from(screen.getByTestId('roots-editor').children)
    expect(kids.indexOf(divider)).toBeGreaterThan(kids.indexOf(screen.getAllByTestId('roots-row')[0].closest('ul') as Element))
    expect(kids.indexOf(divider)).toBeLessThan(kids.indexOf(screen.getByTestId('host-roots')))
  })
})

describe('opening the editor', () => {
  it('reads the other hosts\' grants again (the folded line already holds the poll, so the editor mounting does not)', async () => {
    let reads = 0
    configureHostRoots({ identity: () => 'id-1', fetch: async () => { reads++; return hosts }, post: async () => {}, dismiss: async () => {} })
    await show()
    const before = reads
    expect(before).toBeGreaterThan(0)
    hosts = [host({ roots: ['/Users/wake/Workspace/ios', '/changed-elsewhere'] })]
    fireEvent.click(screen.getByTestId('team-folder-edit'))
    await flush()
    expect(reads).toBe(before + 1)
    expect(screen.getAllByTestId('team-folder-chip').map((c) => c.textContent)).toContain('/changed-elsewhere')
  })
})

describe('opening the editor while the mount read is still out', () => {
  it('reads once more after it, so the older snapshot does not stay on screen', async () => {
    const waiting: Array<(h: PairedHostRoots[]) => void> = []
    configureHostRoots({ identity: () => 'id-1', fetch: () => new Promise<PairedHostRoots[]>((res) => { waiting.push(res) }), post: async () => {}, dismiss: async () => {} })
    render(<TeamSettingsBlock hostId={A} teamId="t-l1" name="Team" />)
    await flush()
    expect(waiting).toHaveLength(1)
    fireEvent.click(screen.getByTestId('team-folder-edit'))
    await act(async () => { waiting[0]([host({ roots: ['/old'] })]); await Promise.resolve(); await Promise.resolve() })
    expect(waiting).toHaveLength(2) // the open asked for a read after the one in flight
    await act(async () => { waiting[1]([host({ roots: ['/new'] })]); await Promise.resolve(); await Promise.resolve() })
    expect(screen.getAllByTestId('team-folder-chip').map((c) => c.textContent)).toContain('/new')
    expect(screen.getAllByTestId('team-folder-chip').map((c) => c.textContent)).not.toContain('/old')
  })
})

describe('other hosts\' grants as chips', () => {
  it('lists the applied grant after the team\'s own, with a host badge and the shared-by-all tooltip', async () => {
    await show()
    const chips = screen.getAllByTestId('team-folder-chip')
    expect(chips.map((c) => c.textContent)).toEqual(['/Users/wake/a', '/b', '/Users/wake/Workspace/ios'])
    const remote = chips[2]
    expect(remote).toHaveAttribute('data-remote', 'true')
    expect(remote).toHaveAttribute('title', 'air26：/Users/wake/Workspace/ios（整台 mlab 共用，所有 team）')
    expect(chips[0]).toHaveAttribute('data-remote', 'false')
    expect(chips[0]).toHaveAttribute('title', '/Users/wake/a')
  })
  it('every remote chip starts with a HostBadge, a neutral one for a host without a look or without an id', async () => {
    hosts = [host({ roots: ['/known-id'] }), host({ hostId: '', alias: 'noid', roots: ['/no-id'] })]
    await show()
    for (const chip of screen.getAllByTestId('team-folder-chip').filter((c) => c.getAttribute('data-remote') === 'true')) {
      const badge = within(chip).getByTestId('team-folder-host-badge')
      expect(badge.getAttribute('data-has-color')).toBe('false')
      expect(chip.firstElementChild).toBe(badge)
    }
    expect(within(screen.getAllByTestId('team-folder-chip')[0]).queryByTestId('team-folder-host-badge')).toBeNull() // the team's own chips have none
  })
  it('lists neither pending nor refused changes', async () => {
    await show()
    const text = screen.getAllByTestId('team-folder-chip').map((c) => c.textContent).join('|')
    expect(text).not.toContain('pending-path')
    expect(text).not.toContain('refused-path')
  })
  it('the English tooltip says the same', async () => {
    useI18nStore.getState().setLocale('en')
    await show()
    expect(screen.getAllByTestId('team-folder-chip')[2]).toHaveAttribute('title', 'air26: /Users/wake/Workspace/ios (shared by all of mlab, every team)')
  })
})
