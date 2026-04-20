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

  it('delegates to rotateSessionPristine (single-tx rotation)', async () => {
    const rotateCalls: Array<{ trigger: string }> = []
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [],
      getLocal: async () => null,
      createSnapshot: async () => { throw new Error('createSnapshot should not be called') },
      deleteLocal: async () => {},
      demoteSessionPristine: async () => { throw new Error('demoteSessionPristine should not be called') },
      rotateSessionPristine: async (_b, trigger) => {
        rotateCalls.push({ trigger })
        return { id: 'p', timestamp: 0, device: 'd', trigger, bundleSize: 0, contributorIds: [], isSessionPristine: true }
      },
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })

    await ensureSessionPristine()
    expect(rotateCalls).toEqual([{ trigger: 'pre-restore' }])
  })

  it('propagates rotation failures so App.tsx bootstrap can surface them', async () => {
    setSnapshotStore({
      init: async () => {},
      listLocal: async () => [],
      getLocal: async () => null,
      createSnapshot: async () => { throw new Error('unexpected') },
      deleteLocal: async () => {},
      demoteSessionPristine: async () => {},
      rotateSessionPristine: async () => { throw new Error('quota exceeded') },
      compact: async () => ({ kept: [], evicted: [] }),
      clear: async () => {},
    })
    await expect(ensureSessionPristine()).rejects.toThrow('quota exceeded')
  })
})
