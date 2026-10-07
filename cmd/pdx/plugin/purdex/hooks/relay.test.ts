// Run with `claude plugin test cmd/pdx/plugin/purdex`. The test's `on` hooks
// stand for the engine beneath the mod (a fake pdx behind $.process.run).
import { test, expect } from 'claude-code/testing'

function world(on: any, ids: { sid: string } = { sid: 'sid-1' }) {
  const argvs: string[][] = []
  on('process.run', async (_$: any, e: any) => {
    argvs.push([...e.argv])
    return { value: { exitCode: 0, stdout: '{"ok":true,"role":"none","self_relay":"on","threshold":70,"min_growth":20000}', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('session.id', async () => ({ value: ids.sid }))
  on('fs.read', async (_$: any, e: any) => (e.path.endsWith('/pdx.json') ? { value: '{"pdx":"/opt/pdx/bin/pdx","data_dir":"/tmp/pdx"}' } : { deny: 'ENOENT' }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('classic.SessionStart', async () => ({}))
  return argvs
}

const sub = (a: string[]) => a.slice(1).join(' ')

test('an interactive session.start says hello through the pdx named in pdx.json', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  expect(argvs).toEqual([['/opt/pdx/bin/pdx', 'relay', 'hello', '--session', 'sid-1', '--version', '1', '--agent', 'cc']])
})

test('a headless session.start (claude -p) calls nothing', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: null, isInteractive: false })
  expect(argvs).toEqual([])
})

// Spec §8.3 / P8a-1d: presence is keyed by session id and /clear mints a new
// one. Mutation gate: drop the classic.SessionStart hook → one hello only.
test('after /clear the mod says hello again with the new session id', async ($, on) => {
  const ids = { sid: 'sid-1' }
  const argvs = world(on, ids)
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  ids.sid = 'sid-2'
  await $.classic.SessionStart({ source: 'clear' })
  expect(argvs.map(sub)).toEqual(['relay hello --session sid-1 --version 1 --agent cc', 'relay hello --session sid-2 --version 1 --agent cc'])
})

test('a SessionStart that is not a clear adds no hello (startup / resume are session.start’s)', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  await $.classic.SessionStart({ source: 'startup' })
  await $.classic.SessionStart({ source: 'resume' })
  expect(argvs.length).toBe(1)
})

test('a /clear while headless says nothing', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: null, isInteractive: false })
  await $.classic.SessionStart({ source: 'clear' })
  expect(argvs).toEqual([])
})

test('without pdx.json the mod falls back to pdx on PATH', async ($, on) => {
  const argvs: string[][] = []
  on('process.run', async (_$: any, e: any) => { argvs.push([...e.argv]); return { value: { exitCode: 1, stdout: '', stderr: 'unknown command', isStdoutTruncated: false, isStderrTruncated: false } } })
  on('session.id', async () => ({ value: 'sid-1' }))
  on('fs.read', async () => ({ deny: 'ENOENT' }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  const r = await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  expect(argvs[0][0]).toBe('pdx')
  expect(r).toEqual({ cwd: '/tmp' }) // a failed hello never fails the session
})
