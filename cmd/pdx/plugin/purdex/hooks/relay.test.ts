// Run with `claude plugin test cmd/pdx/plugin/purdex`. The test's `on` hooks
// stand for the engine beneath the mod (a fake pdx behind $.process.run).
import { test, expect, mock } from 'claude-code/testing'

// The pdx.json the extractor writes: the installing daemon's config is the
// `--config` every `pdx relay` call carries (P5b-1 review).
const PDX_JSON = '{"pdx":"/opt/pdx/bin/pdx","data_dir":"/tmp/pdx","config":"/tmp/pdx b/config.toml"}'

function world(on: any, ids: { sid: string } = { sid: 'sid-1' }, pdxJSON: string = PDX_JSON) {
  const argvs: string[][] = []
  const clock = mock.clock(on) // hello goes out from $.clock.after(0): tests settle it
  on('process.run', async (_$: any, e: any) => {
    argvs.push([...e.argv])
    return { value: { exitCode: 0, stdout: '{"ok":true,"role":"none","self_relay":"on","threshold":70,"min_growth":20000}', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('session.id', async () => ({ value: ids.sid }))
  on('fs.read', async (_$: any, e: any) => (e.path.endsWith('/pdx.json') ? { value: pdxJSON } : { deny: 'ENOENT' }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('classic.SessionStart', async () => ({}))
  return Object.assign(argvs, { settle: () => clock.settle() })
}

const sub = (a: string[]) => a.slice(1).join(' ')

test('an interactive session.start says hello through the pdx and to the daemon named in pdx.json', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  await argvs.settle()
  expect(argvs).toEqual([['/opt/pdx/bin/pdx', 'relay', 'hello', '--session', 'sid-1', '--version', '1', '--agent', 'cc', '--config', '/tmp/pdx b/config.toml']])
})

// Mutation gate: always append --config → this test fails.
test('a pdx.json without config adds no --config (pdx falls back to its default)', async ($, on) => {
  const argvs = world(on, { sid: 'sid-1' }, '{"pdx":"/opt/pdx/bin/pdx","data_dir":"/tmp/pdx"}')
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  await argvs.settle()
  expect(argvs).toEqual([['/opt/pdx/bin/pdx', 'relay', 'hello', '--session', 'sid-1', '--version', '1', '--agent', 'cc']])
})

test('a headless session.start (claude -p) calls nothing', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: null, isInteractive: false })
  await argvs.settle()
  expect(argvs).toEqual([])
})

// Spec §8.3 / P8a-1d: presence is keyed by session id and /clear mints a new
// one. Mutation gate: drop the classic.SessionStart hook → one hello only.
test('after /clear the mod says hello again with the new session id', async ($, on) => {
  const ids = { sid: 'sid-1' }
  const argvs = world(on, ids)
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  await argvs.settle()
  ids.sid = 'sid-2'
  await $.classic.SessionStart({ source: 'clear' })
  await argvs.settle()
  expect(argvs.map(sub)).toEqual(['relay hello --session sid-1 --version 1 --agent cc --config /tmp/pdx b/config.toml', 'relay hello --session sid-2 --version 1 --agent cc --config /tmp/pdx b/config.toml'])
})

test('a SessionStart that is not a clear adds no hello (startup / resume are session.start’s)', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  await $.classic.SessionStart({ source: 'startup' })
  await $.classic.SessionStart({ source: 'resume' })
  await argvs.settle()
  expect(argvs.length).toBe(1)
})

test('a /clear while headless says nothing', async ($, on) => {
  const argvs = world(on)
  await $.session.start({ cwd: '/tmp', surface: null, isInteractive: false })
  await $.classic.SessionStart({ source: 'clear' })
  await argvs.settle()
  expect(argvs).toEqual([])
})

test('without pdx.json the mod falls back to pdx on PATH', async ($, on) => {
  const argvs: string[][] = []
  const clock = mock.clock(on)
  on('process.run', async (_$: any, e: any) => { argvs.push([...e.argv]); return { value: { exitCode: 1, stdout: '', stderr: 'unknown command', isStdoutTruncated: false, isStderrTruncated: false } } })
  on('session.id', async () => ({ value: 'sid-1' }))
  on('fs.read', async () => ({ deny: 'ENOENT' }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  const r = await $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(argvs[0][0]).toBe('pdx')
  expect(r).toEqual({ cwd: '/tmp' }) // a failed hello never fails the session
})

// A daemon that is down answers hello only after the client's 30 s grace:
// the session start (and a /clear) must not wait for it. The fake pdx below
// never answers until the test releases it; session.start has resolved by
// then. Mutation gate: await hello inside the hook → `started` stays false.
test('a hello that hangs never holds the session start or a /clear', async ($, on) => {
  const argvs: string[][] = []
  const clock = mock.clock(on)
  let release!: () => void
  const gate = new Promise<void>((r) => { release = r })
  on('process.run', async (_$: any, e: any) => {
    argvs.push([...e.argv])
    await gate
    return { value: { exitCode: 20, stdout: '', stderr: 'daemon_unavailable', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('session.id', async () => ({ value: 'sid-1' }))
  on('fs.read', async () => ({ deny: 'ENOENT' }))
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('classic.SessionStart', async () => ({}))
  let started = false
  const p = $.session.start({ cwd: '/tmp', surface: 'terminal', isInteractive: true }).then(() => { started = true })
  await clock.settle()
  expect(started).toBe(true)
  expect(argvs.length).toBe(1)
  let cleared = false
  const c = $.classic.SessionStart({ source: 'clear' }).then(() => { cleared = true })
  await clock.settle()
  expect(cleared).toBe(true)
  release()
  await p
  await c
  await clock.settle()
})
