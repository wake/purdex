import { describe, it, expect, beforeEach, vi } from 'vitest'
import { usePeerStore } from './usePeerStore'
import * as api from '../lib/host-api'
import type { PeerRecordWire, PeersEnvelope } from '../lib/host-api'

vi.mock('../lib/host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/host-api')>()),
  fetchPeers: vi.fn(),
}))

const H = 'h1'

function row(over: Partial<PeerRecordWire> = {}): PeerRecordWire {
  return {
    host: 'mini-lab',
    host_id: 'mini-lab:278cbm',
    address: 'mini-lab/_3k9f2mq4:ai-chat4-ai-chat-story-3a',
    row_kind: 'session',
    canonical: '_3k9f2mq4',
    label: 'ai-chat4',
    label_source: 'user',
    label_rev: 0,
    suffix: 'ai-chat4-ai-chat-story-3a',
    session_code: 'z141yl',
    session_name: 'ai-chat4',
    tmux_instance: '6901:1789205013',
    cwd: '/Users/wake/Workspace/wake/ai-chat-story',
    agent: {
      type: 'cc',
      session_id: 'f5ddb1d6-be90-4b36-9927-a260075c3b0f',
      peer_name: 'ai-chat-story-3a',
      pid: 42603,
      proc_start: 'Sun Sep 13 18:57:56 2026',
      inbox: '/tmp/cc-socks/42603.sock',
      status: 'idle',
      version: '2.1.270',
    },
    deliverable: true,
    reason: '',
    ...over,
  }
}

function envelope(peers: PeerRecordWire[], over: Partial<PeersEnvelope> = {}): PeersEnvelope {
  return {
    host_id: 'mini-lab:278cbm',
    ok: true,
    partial: false,
    peers,
    daemon_version: '1.0.0-alpha.364',
    unknown_registry_files: [],
    labels_unavailable: false,
    ...over,
  }
}

/** A fetch nobody has answered yet, plus the switches that answer it. */
function pendingFetch() {
  let settle!: (e: PeersEnvelope) => void
  let fail!: (err: Error) => void
  vi.mocked(api.fetchPeers).mockReturnValueOnce(new Promise<PeersEnvelope>((resolve, reject) => {
    settle = resolve
    fail = reject
  }))
  return { settle, fail }
}

beforeEach(() => {
  vi.mocked(api.fetchPeers).mockReset()
  usePeerStore.getState().forgetHost(H)
  usePeerStore.getState().forgetHost('h2')
  usePeerStore.setState({ byHost: {} })
})

describe('indexing', () => {
  it('indexes session rows by session_code, keeping only the displayed fields', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([row()]))
    await usePeerStore.getState().refresh(H)
    expect(usePeerStore.getState().byHost[H].rows).toEqual({
      z141yl: {
        address: 'mini-lab/_3k9f2mq4:ai-chat4-ai-chat-story-3a',
        canonical: '_3k9f2mq4',
        label: 'ai-chat4',
        labelSource: 'user',
        deliverable: true,
        reason: '',
        // Kept because a session code alone does not identify a session: tmux
        // hands `$N` out from zero again after a restart, so the reader has to
        // check that the row and the pane describe the same tmux server.
        tmuxInstance: '6901:1789205013',
        agent: { type: 'cc', peerName: 'ai-chat-story-3a', status: 'idle' },
      },
    })
  })

  it('keeps an absent tmux_instance as the empty string — unknown, never a match', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([row({ tmux_instance: '' })]))
    await usePeerStore.getState().refresh(H)
    expect(usePeerStore.getState().byHost[H].rows.z141yl.tmuxInstance).toBe('')
  })

  it('indexes inbox_dead and ambiguous session rows — they are session rows and keep their codes', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([
      row({ session_code: 'dead01', deliverable: false, reason: 'inbox_dead' }),
      row({ session_code: 'ambi01', deliverable: false, reason: 'ambiguous' }),
    ]))
    await usePeerStore.getState().refresh(H)
    const rows = usePeerStore.getState().byHost[H].rows
    expect(rows.dead01.reason).toBe('inbox_dead')
    expect(rows.ambi01.reason).toBe('ambiguous')
  })

  it('drops entry rows — by row_kind, not by an empty session_code', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([
      row({ row_kind: 'entry', session_code: '' }),
      // A hypothetical entry row that *does* carry a code is dropped too: the
      // rule is row_kind, never "entry rows happen to be empty".
      row({ row_kind: 'entry', session_code: 'notmine' }),
      row({ session_code: 'keepme' }),
    ]))
    await usePeerStore.getState().refresh(H)
    expect(Object.keys(usePeerStore.getState().byHost[H].rows)).toEqual(['keepme'])
  })

  it('drops a session row with an empty session_code — both conditions are required', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([row({ session_code: '' })]))
    await usePeerStore.getState().refresh(H)
    expect(usePeerStore.getState().byHost[H].rows).toEqual({})
  })

  // `label_source: ''` is not what makes this the no-agent case — a live
  // conversation that has not named itself reports `''` too (spec §8.1). What
  // says "no agent" is an empty `canonical`, so this row carries one and the
  // test asserts it: without that line it would no longer pin what it was
  // written to pin.
  it('keeps a row with no agent (agent: null, canonical: \'\')', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([
      row({ session_code: 'bare01', canonical: '', label: '', label_source: '', suffix: '', agent: null, deliverable: false, reason: 'no_agent' }),
    ]))
    await usePeerStore.getState().refresh(H)
    expect(usePeerStore.getState().byHost[H].rows.bare01.agent).toBeNull()
    expect(usePeerStore.getState().byHost[H].rows.bare01.canonical).toBe('')
  })

  // The other reading of `label_source: ''`: a live conversation with no label
  // of its own. It keeps its canonical and its address, so nothing may treat an
  // empty label source as "no agent".
  it("keeps the canonical of a live row that has not named itself", async () => {
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([
      row({ session_code: 'live01', label: '', label_source: '' }),
    ]))
    await usePeerStore.getState().refresh(H)
    const r = usePeerStore.getState().byHost[H].rows.live01
    expect(r.canonical).toBe('_3k9f2mq4')
    expect(r.agent).not.toBeNull()
  })
})

describe('refresh', () => {
  it('dedupes ten concurrent refreshes into one request', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([row()]))
    await Promise.all(Array.from({ length: 10 }, () => usePeerStore.getState().refresh(H)))
    expect(api.fetchPeers).toHaveBeenCalledTimes(1)
  })

  it('a refresh after the first settles issues a second request', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([row()]))
    await usePeerStore.getState().refresh(H)
    await usePeerStore.getState().refresh(H)
    expect(api.fetchPeers).toHaveBeenCalledTimes(2)
  })

  it('keeps hosts independent', async () => {
    vi.mocked(api.fetchPeers).mockImplementation(async (hostId: string) =>
      envelope([row({ session_code: hostId === H ? 'aaa' : 'bbb' })]))
    await Promise.all([usePeerStore.getState().refresh(H), usePeerStore.getState().refresh('h2')])
    expect(api.fetchPeers).toHaveBeenCalledTimes(2)
    expect(Object.keys(usePeerStore.getState().byHost[H].rows)).toEqual(['aaa'])
    expect(Object.keys(usePeerStore.getState().byHost.h2.rows)).toEqual(['bbb'])
  })

  it('marks the host loading while in flight and clears it afterwards', async () => {
    const { settle } = pendingFetch()
    const p = usePeerStore.getState().refresh(H)
    expect(usePeerStore.getState().byHost[H].loading).toBe(true)
    settle(envelope([row()]))
    await p
    expect(usePeerStore.getState().byHost[H].loading).toBe(false)
  })

  it('records fetchedAt on success', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(1_700_000_000_000)
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([row()]))
    await usePeerStore.getState().refresh(H)
    expect(usePeerStore.getState().byHost[H].fetchedAt).toBe(1_700_000_000_000)
    expect(usePeerStore.getState().byHost[H].error).toBeNull()
    vi.useRealTimers()
  })

  it('a failed refresh records the error and keeps the previous rows and fetchedAt', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValueOnce(envelope([row()]))
    await usePeerStore.getState().refresh(H)
    const before = usePeerStore.getState().byHost[H]
    vi.mocked(api.fetchPeers).mockRejectedValueOnce(new Error('boom'))
    await expect(usePeerStore.getState().refresh(H)).resolves.toBeUndefined()
    const after = usePeerStore.getState().byHost[H]
    expect(after.error).toBe('boom')
    expect(after.loading).toBe(false)
    expect(after.rows).toEqual(before.rows)
    expect(after.fetchedAt).toBe(before.fetchedAt)
  })

  // Codex R1. A 200 carrying ok:false is the daemon describing a failure it
  // could not build an inventory for, not an empty fleet. Indexing its empty
  // `peers` would turn "we could not look" into a confident "no peer" and drop
  // the rows we still had.
  it('an ok:false envelope is a failed refresh, not an empty one', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValueOnce(envelope([row()]))
    await usePeerStore.getState().refresh(H)
    const before = usePeerStore.getState().byHost[H]

    vi.mocked(api.fetchPeers).mockResolvedValueOnce(
      envelope([], { ok: false, error: 'tmux unavailable' }),
    )
    await usePeerStore.getState().refresh(H)
    const after = usePeerStore.getState().byHost[H]

    expect(after.error).toBe('tmux unavailable')
    expect(after.rows).toEqual(before.rows)
    expect(after.fetchedAt).toBe(before.fetchedAt)
    expect(after.loading).toBe(false)
  })

  it('an ok:false envelope with no error message still records one', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValueOnce(envelope([], { ok: false }))
    await usePeerStore.getState().refresh(H)
    expect(usePeerStore.getState().byHost[H].error).toBeTruthy()
  })

  it('a successful refresh clears a previous error', async () => {
    vi.mocked(api.fetchPeers).mockRejectedValueOnce(new Error('boom'))
    await usePeerStore.getState().refresh(H)
    expect(usePeerStore.getState().byHost[H].error).toBe('boom')
    vi.mocked(api.fetchPeers).mockResolvedValueOnce(envelope([row()]))
    await usePeerStore.getState().refresh(H)
    expect(usePeerStore.getState().byHost[H].error).toBeNull()
  })

  it('stores the envelope flags', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([row()], {
      partial: true,
      labels_unavailable: true,
      unknown_registry_files: ['/tmp/cc-registry/9.json'],
    }))
    await usePeerStore.getState().refresh(H)
    expect(usePeerStore.getState().byHost[H].envelope).toEqual({
      partial: true,
      labelsUnavailable: true,
      unknownRegistryFiles: ['/tmp/cc-registry/9.json'],
    })
  })
})

describe('forgetHost', () => {
  it('clears one host and leaves the others intact', async () => {
    vi.mocked(api.fetchPeers).mockResolvedValue(envelope([row()]))
    await usePeerStore.getState().refresh(H)
    await usePeerStore.getState().refresh('h2')
    usePeerStore.getState().forgetHost(H)
    expect(usePeerStore.getState().byHost[H]).toBeUndefined()
    expect(usePeerStore.getState().byHost.h2).toBeDefined()
  })

  it('an answer already in flight when the host is forgotten never lands', async () => {
    const { settle } = pendingFetch()
    const p = usePeerStore.getState().refresh(H)
    usePeerStore.getState().forgetHost(H)
    settle(envelope([row()]))
    await p
    expect(usePeerStore.getState().byHost[H]).toBeUndefined()
  })

  it('a failure already in flight when the host is forgotten never lands either', async () => {
    const { fail } = pendingFetch()
    const p = usePeerStore.getState().refresh(H)
    usePeerStore.getState().forgetHost(H)
    fail(new Error('boom'))
    await p
    expect(usePeerStore.getState().byHost[H]).toBeUndefined()
  })

  it('is a no-op for a host that was never fetched', () => {
    expect(() => usePeerStore.getState().forgetHost('nobody')).not.toThrow()
  })
})
