// A failure nobody was mounted to see is kept for the next mount - and freed with the pane (#2457), like the other memories.
import { describe, it, expect, afterEach, vi } from 'vitest'
import { draftKey } from './draft-memory'
import { clearAllAttachments } from './attachment-memory'
import { clearAllUploads, startUploads, takeUnseenFailures, uploadsOf } from './attachment-upload'
import { releasePane, retireStaleSessions } from './pane-release'

const mocks = vi.hoisted(() => ({ upload: vi.fn() }))
vi.mock('../host-api', async (orig) => ({ ...(await orig<typeof import('../host-api')>()), agentUploadToPath: mocks.upload }))

const KEY = draftKey('pane-x', 'h', 's1')
const file = new File(['x'], 'a.png', { type: 'image/png' })
const flush = async () => { await Promise.resolve(); await Promise.resolve() }
const failOffline = async () => {
  mocks.upload.mockRejectedValueOnce(Object.assign(new Error('net'), { kind: 'network' }))
  startUploads(KEY, 'h', 'dev001', [file])
  await flush()
}

afterEach(() => { clearAllUploads(); clearAllAttachments(); mocks.upload.mockReset() })

describe('unseen upload failures', () => {
  it('are kept for the next mount', async () => {
    await failOffline()
    expect(takeUnseenFailures(KEY)).toHaveLength(1)
    expect(takeUnseenFailures(KEY)).toEqual([])
  })
  it('are freed by releasePane', async () => {
    await failOffline()
    releasePane('pane-x')
    expect(takeUnseenFailures(KEY)).toEqual([])
  })
  it('are freed by retireStaleSessions for the sessions the pane left, not the one it shows', async () => {
    await failOffline()
    retireStaleSessions('pane-x', 'h', 's1')
    expect(takeUnseenFailures(KEY)).toHaveLength(1)
    await failOffline()
    retireStaleSessions('pane-x', 'h', 's2')
    expect(takeUnseenFailures(KEY)).toEqual([])
  })
  it('a release also drops the running entry', () => {
    mocks.upload.mockReturnValueOnce(new Promise(() => {}))
    startUploads(KEY, 'h', 'dev001', [file])
    expect(uploadsOf(KEY)).toHaveLength(1)
    releasePane('pane-x')
    expect(uploadsOf(KEY)).toEqual([])
  })
})
