import { describe, expect, it, beforeEach } from 'vitest'
import { ensureSessionPristine } from '../register-sync'
import { setSnapshotStore } from '../snapshot-store-instance'
import { __setEngineForTests } from '../register-sync'

describe('ensureSessionPristine', () => {
  beforeEach(() => {
    __setEngineForTests({
      register: () => {},
      getContributors: () => [],
      serialize: () => ({
        version: 1, timestamp: 0, device: 'd',
        collections: { x: { version: 1, data: {} } },
      }),
      push: async () => ({ version: 1, timestamp: 0, device: 'd', collections: {} }),
      pull: async () => ({ appliedBundle: null, conflicts: [] }),
    } as never)
  })

  it('creates a session-pristine snapshot if none exists', async () => {
    const created: Array<{ trigger: string; isPristine: boolean }> = []
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [],
      getLocal: async () => null,
      createSnapshot: async (_b, trigger, opts) => {
        created.push({ trigger, isPristine: opts?.isSessionPristine ?? false })
        return { id: 'p', timestamp: 0, device: 'd', trigger, bundleSize: 0, contributorIds: [], isSessionPristine: opts?.isSessionPristine ?? false }
      },
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })

    await ensureSessionPristine()
    expect(created).toHaveLength(1)
    expect(created[0].isPristine).toBe(true)
    expect(created[0].trigger).toBe('pre-restore')
  })

  it('does not create duplicate pristine if one already exists', async () => {
    const created: Array<unknown> = []
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [
        { id: 'existing', timestamp: 0, device: 'd', trigger: 'pre-restore', bundleSize: 0, contributorIds: [], isSessionPristine: true },
      ],
      getLocal: async () => null,
      createSnapshot: async (...args) => { created.push(args); return { id: 'x', timestamp: 0, device: 'd', trigger: 'manual', bundleSize: 0, contributorIds: [], isSessionPristine: false } },
      deleteLocal: async () => {},
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })
    await ensureSessionPristine()
    expect(created).toHaveLength(0)
  })
})
