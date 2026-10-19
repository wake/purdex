// spa/src/hooks/useAgentNotifications.seat.test.ts — a session notification is titled by the persona behind the session when the App knows it (name pool v2 spec §9.3), else by the tmux name as before.
import { describe, it, expect, beforeEach } from 'vitest'
import { notificationName } from './useAgentNotifications'
import { emptyPeerHostEntry, usePeerStore, type PeerRow } from '../stores/usePeerStore'
import { useSessionStore } from '../stores/useSessionStore'
import { usePersonaStore } from '../stores/usePersonaStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { makeCollection, makePersona } from '../lib/names/__fixtures__/personas'

const H = 'host-a'
const GEN = '4242:1700000000'
const peerRow = (address: string, tmuxInstance = GEN): PeerRow => ({ address, ref: '_abc123', title: '', titleSource: '', deliverable: true, reason: '', tmuxInstance, agent: null })
const setPeers = (rows: Record<string, PeerRow>) => usePeerStore.setState({ byHost: { [H]: { ...emptyPeerHostEntry(), rows, fetchedAt: 1 } } })

beforeEach(() => {
  useTeamRosterStore.getState().reset()
  setPeers({ S1: peerRow('mlab/proj-1-kenji') })
  useSessionStore.setState({ sessions: { [H]: [{ code: 'S1', name: 'proj-1', tmux_instance: GEN }, { code: 'S2', name: 'proj-2', tmux_instance: GEN }] as never } })
  const c = makeCollection(['kenji'])
  c.items[0] = makePersona('kenji', { name: 'Kenji' })
  usePersonaStore.setState({ byHost: { [H]: { status: 'ready', collection: c } } } as never)
})

describe('notificationName', () => {
  it('is the persona\'s name for a session the App knows is one', () => {
    expect(notificationName(H, 'S1')).toBe('Kenji')
  })

  it('is the tmux name for a session no store places (no peers row yet), and the code for an unknown one', () => {
    expect(notificationName(H, 'S2')).toBe('proj-2')
    expect(notificationName(H, 'S9')).toBe('S9')
  })

  it('is the tmux name when the address ends in no persona\'s code', () => {
    setPeers({ S1: peerRow('mlab/proj-1-ghost') })
    expect(notificationName(H, 'S1')).toBe('proj-1')
  })

  it('does not trust a peers row of another tmux generation', () => {
    setPeers({ S1: peerRow('mlab/proj-1-kenji', '9999:1') })
    expect(notificationName(H, 'S1')).toBe('proj-1')
  })
})
