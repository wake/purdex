import { describe, expect, it } from 'vitest'
import { readCliState } from './cli'
import type { ExecFn } from './launch-env'

// Copied, not moved: index.test.ts still needs both for its own harness.
const HOME = '/Users/t'
const BIN = `${HOME}/.config/pdx/bin/pdx`

describe('readCliState (spec §4.1)', () => {
  const env: NodeJS.ProcessEnv = { PATH: '/x' }
  const report = {
    self: BIN, resolved: `${HOME}/.local/bin/pdx`, isSelf: true,
    localBin: `${HOME}/.local/bin`, localBinExists: true, localBinOnPath: true,
    link: 'ok', fixes: [], ok: true,
  }
  const cli = (exec: ExecFn, present = true) => readCliState({ exec, exists: async () => present }, BIN, env)

  it('runs the managed binary with `path --json` and the launch env', async () => {
    const calls: Array<{ file: string; args: string[]; env?: NodeJS.ProcessEnv }> = []
    const exec: ExecFn = async (file, args, opts) => {
      calls.push({ file, args, env: opts.env })
      return { code: 0, stdout: JSON.stringify(report), stderr: '', timedOut: false }
    }
    await cli(exec)
    expect(calls).toHaveLength(1)
    expect(calls[0].file).toBe(BIN)
    expect(calls[0].args).toEqual(['path', '--json'])
    expect(calls[0].env).toBe(env)
  })

  it('exit 0 is a parsed report', async () => {
    const exec: ExecFn = async () => ({ code: 0, stdout: JSON.stringify(report) + '\n', stderr: '', timedOut: false })
    expect(await cli(exec)).toEqual({ kind: 'report', code: 0, report })
  })

  it('exit 1 is the normal not-reachable answer, not an error', async () => {
    const off = { ...report, resolved: null, isSelf: false, link: 'missing', fixes: ['link'], ok: false }
    const exec: ExecFn = async () => ({ code: 1, stdout: JSON.stringify(off), stderr: '', timedOut: false })
    expect(await cli(exec)).toEqual({ kind: 'report', code: 1, report: off })
  })

  it('malformed output is its own outcome, never a report', async () => {
    const exec: ExecFn = async () => ({ code: 0, stdout: 'not json', stderr: 'oops', timedOut: false })
    expect(await cli(exec)).toEqual({ kind: 'unparseable', code: 0, stdout: 'not json', stderr: 'oops' })
  })

  it('JSON that is not a pdx path report is unparseable, not a report', async () => {
    const exec: ExecFn = async () => ({ code: 0, stdout: '{"resolved":42}', stderr: '', timedOut: false })
    expect((await cli(exec)).kind).toBe('unparseable')
  })

  it('a thrown exec is its own outcome', async () => {
    const exec: ExecFn = async () => { throw new Error('EACCES') }
    expect(await cli(exec)).toEqual({ kind: 'exec-failed', error: 'EACCES' })
  })

  it('a timeout is an exec failure, not "not reachable"', async () => {
    const exec: ExecFn = async () => ({ code: null, stdout: '', stderr: '', timedOut: true })
    expect((await cli(exec)).kind).toBe('exec-failed')
  })

  it('a spawn failure (code null, no timeout) is an exec failure', async () => {
    const exec: ExecFn = async () => ({ code: null, stdout: '', stderr: 'ENOENT', timedOut: false })
    expect((await cli(exec)).kind).toBe('exec-failed')
  })

  it('an absent binary is its own outcome and nothing is executed', async () => {
    let ran = false
    const exec: ExecFn = async () => { ran = true; return { code: 0, stdout: '', stderr: '', timedOut: false } }
    expect(await cli(exec, false)).toEqual({ kind: 'not-installed' })
    expect(ran).toBe(false)
  })
})
