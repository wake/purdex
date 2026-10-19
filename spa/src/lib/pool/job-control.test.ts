// spa/src/lib/pool/job-control.test.ts — which daemon takes a change to a job, and which items the 「⋯」 menu offers for it.
import { describe, it, expect } from 'vitest'
import { controlVia, menuActions } from './job-control'

const caps = (...ids: string[]) => (id: string) => ids.includes(id)

describe('controlVia', () => {
  it('no daemon announces jobs.control.v1: nothing can take a change', () => {
    expect(controlVia('mlab', { ownDaemon: { mlab: 'd-mlab' }, forwarders: ['d-mlab'] }, caps())).toEqual({ cancelVia: null, positionVia: null })
  })

  it('the origin\'s own daemon takes both, and is the one a cancel goes to even when another forwarder is first', () => {
    expect(controlVia('air26', { ownDaemon: { air26: 'd-air26' }, forwarders: ['d-mlab', 'd-air26'] }, caps('d-mlab', 'd-air26'))).toEqual({ cancelVia: 'd-air26', positionVia: 'd-air26' })
  })

  it('an origin the App has no daemon for: a cancel goes through the first forwarder that announces it; no place can be set', () => {
    expect(controlVia('mserv', { ownDaemon: { mlab: 'd-mlab' }, forwarders: ['d-air26', 'd-mlab'] }, caps('d-mlab'))).toEqual({ cancelVia: 'd-mlab', positionVia: null })
  })

  it('an own daemon that does not announce it cannot take a place, and a cancel falls to a forwarder that does', () => {
    expect(controlVia('mlab', { ownDaemon: { mlab: 'd-mlab' }, forwarders: ['d-mlab', 'd-air26'] }, caps('d-air26'))).toEqual({ cancelVia: 'd-air26', positionVia: null })
  })

  it('an origin named like an Object.prototype member has no own daemon', () => {
    expect(controlVia('toString', { ownDaemon: {}, forwarders: ['d-1'] }, caps('d-1'))).toEqual({ cancelVia: 'd-1', positionVia: null })
  })
})

describe('menuActions', () => {
  const both = { cancelVia: 'd', positionVia: 'd' }
  const kinds = (a: ReturnType<typeof menuActions>) => a.map((x) => (x.kind === 'cancel' ? (x.confirm ? 'cancel+confirm' : 'cancel') : x.position))

  it('a queued job with no place of its own: front, back, hold, then cancel at once; no 恢復', () => {
    expect(kinds(menuActions({ state: 'queued', manual: '' }, both))).toEqual(['front', 'back', 'hold', 'cancel'])
  })

  it('a place the job already has is not offered again, and 恢復 comes once it has one', () => {
    expect(kinds(menuActions({ state: 'queued', manual: 'hold' }, both))).toEqual(['front', 'back', 'normal', 'cancel'])
    expect(kinds(menuActions({ state: 'queued', manual: 'front' }, both))).toEqual(['back', 'hold', 'normal', 'cancel'])
    expect(kinds(menuActions({ state: 'queued', manual: 'back' }, both))).toEqual(['front', 'hold', 'normal', 'cancel'])
  })

  it('without a daemon for places, a queued job offers cancel only; without one for cancel, only the places', () => {
    expect(kinds(menuActions({ state: 'queued', manual: '' }, { cancelVia: 'd', positionVia: null }))).toEqual(['cancel'])
    expect(kinds(menuActions({ state: 'queued', manual: '' }, { cancelVia: null, positionVia: 'd' }))).toEqual(['front', 'back', 'hold'])
  })

  it('a job that is going offers cancel, and it asks first (claimed as well as running)', () => {
    expect(kinds(menuActions({ state: 'running', manual: '' }, both))).toEqual(['cancel+confirm'])
    expect(kinds(menuActions({ state: 'claimed', manual: '' }, both))).toEqual(['cancel+confirm'])
  })

  it('an ended job offers nothing, and nothing is offered when nothing can take it', () => {
    for (const state of ['succeeded', 'failed', 'cancelled', 'superseded', 'expired']) expect(menuActions({ state, manual: '' }, both)).toEqual([])
    expect(menuActions({ state: 'queued', manual: '' }, { cancelVia: null, positionVia: null })).toEqual([])
  })
})
