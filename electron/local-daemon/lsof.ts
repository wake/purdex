// Pure helpers for the `lsof -F0pfn` machine format: every field is a
// single-letter tag followed by its value and a NUL; a process set is
// terminated by a newline. We never fall back to parsing the human table.

export interface LsofFile { fd: string; name: string }
export interface LsofProcess { pid: number; files: LsofFile[] }

export function parseLsofF0(output: string): LsofProcess[] {
  const procs: LsofProcess[] = []
  let cur: LsofProcess | null = null
  let file: LsofFile | null = null
  for (const set of output.split('\n')) {
    if (set === '') continue
    for (const field of set.split('\0')) {
      if (field === '') continue
      const tag = field[0]
      const value = field.slice(1)
      if (tag === 'p') {
        cur = { pid: Number(value), files: [] }
        procs.push(cur)
        file = null
      } else if (tag === 'f' && cur) {
        file = { fd: value, name: '' }
        cur.files.push(file)
      } else if (tag === 'n' && file) {
        file.name = value
      }
    }
  }
  return procs
}

export function txtPaths(procs: LsofProcess[], pid: number): string[] {
  return procs.filter((p) => p.pid === pid).flatMap((p) => p.files.filter((f) => f.fd === 'txt').map((f) => f.name))
}

export function listenersOn(procs: LsofProcess[], bind: string, port: number): number[] {
  const exact = `${bind}:${port}`
  const any = `*:${port}`
  return procs.filter((p) => p.files.some((f) => f.name === exact || f.name === any)).map((p) => p.pid)
}

export type Ownership =
  | { managed: 'managed'; alive: { pid: number } | null }
  | { managed: 'external'; reason: string; alive: { pid: number } | null }
  | { managed: 'none'; alive: null }

export interface OwnershipInput {
  candidatePid: number | null
  candidateIsOurs: boolean
  listenerPids: number[]
  listenerBinaries: Record<number, string | undefined>
  binExists: boolean
}

// Spec §3.1 "Decision". The pid-file number is only a candidate: it is
// ours iff its executable resolves to binPath. A foreign listener on our
// endpoint always wins — we must never stop or replace someone else's daemon.
export function decideOwnership(i: OwnershipInput): Ownership {
  const alive = i.candidatePid !== null && i.candidateIsOurs ? { pid: i.candidatePid } : null
  const foreign = i.listenerPids.find((p) => alive === null || p !== alive.pid)
  if (foreign !== undefined) {
    const bin = i.listenerBinaries[foreign]
    return { managed: 'external', reason: bin ? `running daemon is ${bin}` : `port is served by pid ${foreign}`, alive }
  }
  if (alive) return { managed: 'managed', alive }
  return i.binExists ? { managed: 'managed', alive: null } : { managed: 'none', alive: null }
}
