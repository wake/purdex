import { describe, it, expect, beforeEach } from 'vitest'
import type { HostEvent } from '../host-events'
import { useSyncStore } from '../sync/use-sync-store'
import { useBackupStore } from '../../stores/useBackupStore'
import { dispatchBackupWsEvent } from './backup-ws-dispatch'

function backupDoneEvent(value: object): HostEvent {
  return { type: 'backup:done', session: '', value: JSON.stringify(value) }
}

beforeEach(() => {
  useBackupStore.setState({ byHost: {} })
})

describe('dispatchBackupWsEvent — cross-device backup:done refresh', () => {
  it("'backup:done' is a valid HostEvent type", () => {
    const ev: HostEvent = { type: 'backup:done', session: '', value: '{}' }
    expect(ev.type).toBe('backup:done')
  })

  it('refreshes status for an event from a DIFFERENT device', () => {
    const ev = backupDoneEvent({
      storeId: 'inapp:buffer', snapshotId: 9, currentHeadId: 9,
      device: 'some-other-device', trigger: 'auto', createdAt: 1_751_230_000,
    })
    dispatchBackupWsEvent('h1', ev)
    expect(useBackupStore.getState().byHost['h1']?.lastBackupAt).toBe(1_751_230_000 * 1000)
  })

  it('ignores its OWN device event (own posts refresh locally, R1-P1a)', () => {
    const ownId = useSyncStore.getState().getClientId()
    const ev = backupDoneEvent({
      storeId: 'inapp:buffer', snapshotId: 9, currentHeadId: 9,
      device: ownId, trigger: 'auto', createdAt: 1_751_230_000,
    })
    dispatchBackupWsEvent('h1', ev)
    expect(useBackupStore.getState().byHost['h1']).toBeUndefined()
  })

  it('ignores a malformed payload without throwing', () => {
    expect(() => dispatchBackupWsEvent('h1', { type: 'backup:done', session: '', value: 'not json' }))
      .not.toThrow()
    expect(useBackupStore.getState().byHost['h1']).toBeUndefined()
  })
})
