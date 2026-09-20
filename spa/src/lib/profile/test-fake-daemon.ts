// spa/src/lib/profile/test-fake-daemon.ts — TEST SUPPORT, imported by the
// integration tests only. An in-memory daemon that keeps P1's compare-and-set
// semantics: revs only go up, a wrong baseRev is a 409 carrying the live
// section, a delete leaves a tombstone that takes baseRev 0 only, an
// already-gone delete is a 200.
import type { DeleteOutcome, DeleteSectionParams, ProfileIndexEntry, PutOutcome, PutSectionBody, Result, Section, SectionMeta } from './api'

export interface FakeRow {
  rev: number
  /** null = tombstone */
  hash: string | null
  payload: Record<string, unknown> | null
  fingerprint: string
  ordinal: number
  writer: string
}

export class FakeDaemon {
  private readonly profileId: string

  constructor(profileId: string) {
    this.profileId = profileId
  }

  rows = new Map<string, FakeRow>()
  writes: Array<{ op: 'put' | 'delete'; key: string; clientId: string; outcome: string }> = []

  private metaOf(key: string, r: FakeRow): SectionMeta {
    return { section: key, rev: r.rev, hash: r.hash!, fingerprint: r.fingerprint, ordinal: r.ordinal, writer: r.writer, updatedAt: 0 }
  }

  live(): string[] {
    return [...this.rows].filter(([, r]) => r.hash !== null).map(([key]) => key).sort()
  }

  revs(): Record<string, number> {
    return Object.fromEntries([...this.rows].map(([key, r]) => [key, r.rev]))
  }

  list(): Result<ProfileIndexEntry[]> {
    const sections = [...this.rows].filter(([, r]) => r.hash !== null).map(([key, r]) => this.metaOf(key, r))
    return { kind: 'ok', value: [{ id: this.profileId, name: 'p', createdAt: 0, updatedAt: 0, sections, attachments: [] }] }
  }

  get(key: string): Result<Section | null> {
    const r = this.rows.get(key)
    if (r === undefined || r.hash === null) return { kind: 'ok', value: null }
    return { kind: 'ok', value: { ...this.metaOf(key, r), payload: JSON.parse(JSON.stringify(r.payload)) as Record<string, unknown> } }
  }

  put(key: string, body: PutSectionBody): PutOutcome {
    const cur = this.rows.get(key)
    const isLive = cur !== undefined && cur.hash !== null
    const log = (outcome: string): void => void this.writes.push({ op: 'put', key, clientId: body.clientId, outcome })
    if (body.baseRev !== (isLive ? cur.rev : 0)) {
      log('conflict')
      return isLive ? { kind: 'conflict', rev: cur.rev, hash: cur.hash, payload: cur.payload } : { kind: 'conflict', rev: 0, hash: null, payload: null }
    }
    if (isLive && cur.hash === body.hash) {
      log('converged')
      return { kind: 'converged', rev: cur.rev }
    }
    const rev = (cur?.rev ?? 0) + 1
    this.rows.set(key, { rev, hash: body.hash, payload: JSON.parse(JSON.stringify(body.payload)) as Record<string, unknown>, fingerprint: body.fingerprint, ordinal: body.ordinal, writer: body.clientId })
    log('applied')
    return { kind: 'applied', rev }
  }

  delete(key: string, params: DeleteSectionParams): DeleteOutcome {
    const cur = this.rows.get(key)
    const log = (outcome: string): void => void this.writes.push({ op: 'delete', key, clientId: params.clientId, outcome })
    if (cur === undefined || cur.hash === null) {
      log('already-gone')
      return { kind: 'applied', rev: cur?.rev ?? 0 }
    }
    if (params.baseRev !== cur.rev) {
      log('conflict')
      return { kind: 'conflict', rev: cur.rev, hash: cur.hash, payload: cur.payload }
    }
    this.rows.set(key, { ...cur, rev: cur.rev + 1, hash: null, payload: null, writer: params.clientId })
    log('applied')
    return { kind: 'applied', rev: cur.rev + 1 }
  }
}
