// Run with `claude plugin test cmd/pdx/plugin/purdex`. The test's `on` hooks
// stand for the engine beneath the mod: a fake pdx behind $.process.run,
// $.session.*, $.fs.read, $.ui.*, and the bottom of every event a test
// raises. The P5b-1 hello tests come first, the P5b-2 relay after them.
import { test, expect, mock } from 'claude-code/testing'
import { FIXED } from './prompts.js'

// The pdx.json the extractor writes: the installing daemon's config is the
// `--config` every `pdx relay` call carries (P5b-1 review).
const PDX_JSON = '{"pdx":"/opt/pdx/bin/pdx","data_dir":"/tmp/pdx","config":"/tmp/pdx b/config.toml"}'
const REGISTER = async (_$: any, e: any) => ({ value: { command: e.name } }) // the engine's $.command.register

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
  on('command.register', REGISTER) // P5b-3: an interactive session.start registers /relay
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
  on('command.register', REGISTER)
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
  on('command.register', REGISTER)
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

// ---------- P5b-2: the relay ----------
// Every pdx call goes out from a $.clock.after timer (the P5b-1 contract: no
// hook waits on the daemon), so each step below settles or advances the
// clock before it looks at what was called — and before it asserts that
// nothing was, or a call made from a timer would never be seen.

type Fake = {
  argvs: string[][]
  timeouts: (number | undefined)[] // each pdx call's $.process.run timeoutMs, beside argvs
  registered: any[] // $.command.register specs
  submits: any[]
  commands: string[]
  toasts: string[]
  statuses: (string | undefined)[]
  files: Record<string, string>
  pdxJSON?: string
  pdx: (argv: string[]) => { exitCode: number; stdout?: string; stderr?: string } | Promise<{ exitCode: number; stdout?: string; stderr?: string }>
  sessionId: string
  usage: { tokens?: number; window: number; percent?: number }
  clock: any
  logs: string[]
  failSubmit?: (text: string) => 'reject' | 'drop' | undefined // how the engine refuses a plugin's prompt
  failCommand?: string // $.command.run rejects with this
  refuseNow?: boolean // a hand-made clock instead of mock.clock: timers fire at once, $.clock.now is refused
  // The prompt hold's local `/bin/sleep` calls (P5b-3 critic), kept apart from
  // the pdx calls in argvs, and how each answers: by default it sleeps its
  // seconds on the mocked clock and exits 0; `realSleep` sleeps in real time.
  sleeps: { argv: string[]; timeoutMs?: number }[]
  sleep?: (argv: string[]) => { exitCode: number } | Promise<{ exitCode: number }>
}

// The clock of `refuseNow`: the one way to make the prompt hold throw (its
// `$.clock.now`, where it starts its bound), for the `.catch` test.
function refusingClock(on: any) {
  on('clock.now', async () => { throw new Error('the clock refused to tell the time') })
  on('clock.after', async () => ({ value: undefined }))
  const settle = async () => { for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 1)) }
  return { settle, advance: settle, sleep: () => new Promise<void>(() => {}) }
}

// A `/bin/sleep <s>` that takes its seconds of real time (the real-time test).
const realSleep = (argv: string[]) => new Promise<{ exitCode: number }>((r) => setTimeout(() => r({ exitCode: 0 }), Number(argv[1]) * 1000))

const OP = { id: 'op-1', kind: 'self', host_id: 'h', session_id: 'sid-old', ref: '_abc123', state: 'awaiting_approval', handoff_path: '/data/relay/op-1.md', created_at: 1, updated_at: 1 }
const BEGIN_OK = JSON.stringify({ op: OP, request_id: 'req-1' })
const HELLO = (role = 'none', extra = {}) => JSON.stringify({ ok: true, role, self_relay: 'on', threshold: 70, min_growth: 20000, ...extra })
const APPROVAL = (state: string) => JSON.stringify({ id: 'req-1', kind: 'self_relay', state })
const GOOD_FILE = '# HANDOFF\n' + ['## 1. a', '## 2. b', '## 3. c', '## 4. d', '## 5. e', '## 6. f', '## 7. g', '## 8. h'].map((h) => h + '\n' + 'x'.repeat(40)).join('\n')
const AT72 = { tokens: 144000, window: 200000, percent: 72 }

function relayWorld(on: any, opts: Partial<Fake> = {}, env: Record<string, string> = {}): Fake {
  const f: Fake = {
    argvs: [], timeouts: [], registered: [], submits: [], commands: [], toasts: [], statuses: [], files: {}, logs: [], sleeps: [],
    pdx: () => ({ exitCode: 0, stdout: HELLO() }),
    sessionId: 'sid-old',
    usage: { tokens: 10000, window: 200000, percent: 5 },
    ...opts,
  }
  f.clock = f.refuseNow ? refusingClock(on) : mock.clock(on)
  mock.env(on, env)
  on('tool.check', async () => ({ decision: 'ask', reason: 'mode' }))
  on('process.run', async (_$: any, e: any) => {
    if (e.argv[0] === '/bin/sleep') {
      f.sleeps.push({ argv: [...e.argv], timeoutMs: e.init?.timeoutMs })
      const r = await (f.sleep ? f.sleep([...e.argv]) : f.clock.sleep(Number(e.argv[1]) * 1000).then(() => ({ exitCode: 0 })))
      return { value: { exitCode: r.exitCode, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
    }
    f.argvs.push([...e.argv])
    f.timeouts.push(e.init?.timeoutMs)
    const r = await f.pdx([...e.argv].slice(1))
    return { value: { exitCode: r.exitCode, stdout: r.stdout ?? '', stderr: r.stderr ?? '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('session.id', async () => ({ value: f.sessionId }))
  on('session.usage', async () => ({ value: { startedAt: 0, context: f.usage, rateLimits: [] } }))
  on('fs.read', async (_$: any, e: any) => {
    if (e.path.endsWith('/pdx.json')) return f.pdxJSON ? { value: f.pdxJSON } : { deny: 'ENOENT' }
    return e.path in f.files ? { value: f.files[e.path] } : { deny: 'ENOENT' }
  })
  on('ui.toast', async (_$: any, e: any) => { f.toasts.push(e.text); return { value: undefined } })
  on('ui.status', async (_$: any, e: any) => { f.statuses.push(e.text); return { value: undefined } })
  on('ui.log', async (_$: any, e: any) => { f.logs.push(e.text); return { value: undefined } })
  on('command.register', async (_$: any, e: any) => { f.registered.push(e); return { value: { command: e.name } } })
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('turn.start', async (_$: any, e: any) => ({ turnId: e.turnId }))
  on('turn.complete', async (_$: any, e: any) => ({ text: e.answer }))
  on('classic.SessionStart', async () => ({}))
  on('prompt.submit', async (_$: any, e: any) => {
    f.submits.push(e)
    const fail = f.failSubmit?.(e.text)
    if (fail === 'drop') return { drop: 'blocked by a settings hook' }
    if (fail === 'reject') throw new Error('the session refused the prompt')
    return { text: e.text, context: e.context }
  })
  on('command.run', async (_$: any, e: any) => {
    f.commands.push(e.command)
    if (f.failCommand) throw new Error(f.failCommand)
    return { text: 'ran ' + e.command }
  })
  on('session.compact', async (_$: any, e: any) => ({ messages: e.messages }))
  return f
}

const start = async ($: any, f: Fake, interactive = true) => {
  await $.session.start({ cwd: '/tmp', surface: interactive ? 'terminal' : null, isInteractive: interactive })
  await f.clock.settle() // the hello timer
}
const turn = ($: any, turnId: string) => $.turn.complete({ answer: 'ok', reason: 'answer', durationMs: 1, isAborted: false, turnId })
const turnAndSettle = async ($: any, f: Fake, turnId: string) => { await turn($, turnId); await f.clock.settle() }
const MSGS = [{ role: 'user' as const, text: 'hi', toolUses: [] }]
const compact = ($: any, trigger: string) => $.session.compact({ trigger, messages: MSGS })
const count = (f: Fake, cmd: string) => f.argvs.filter((a) => a[2] === cmd).length
const reports = (f: Fake) => f.argvs.map(sub).filter((c) => c.startsWith('relay report'))

// A fake pdx: hello ok; begin ok; wait answers from a queue; everything else ok.
function pdxWith(waits: Array<{ exitCode: number; stdout?: string }>, role = 'none') {
  return (argv: string[]) => {
    const [, cmd] = argv
    if (cmd === 'hello') return { exitCode: 0, stdout: HELLO(role) }
    if (cmd === 'begin') return { exitCode: 0, stdout: BEGIN_OK }
    if (cmd === 'wait') return waits.shift() ?? new Promise<never>(() => {}) // the long-poll blocks until the test ends
    if (argv[0] === 'msg') return { exitCode: 0, stdout: 'mlab/purdex-x [abc123]' }
    return { exitCode: 0, stdout: '{}' }
  }
}

// ---------- P5b-2: begin and its guards ----------

// Mutation gate: let a headless session begin → the begin shows up here.
test('headless does nothing, ever: no hello, no begin at 90 %, prompts and compaction untouched', async ($, on) => {
  const f = relayWorld(on, { pdx: pdxWith([]), usage: { tokens: 180000, window: 200000, percent: 90 } })
  await start($, f, false)
  expect(f.registered).toEqual([]) // no /relay either
  await turnAndSettle($, f, 't1')
  await $.prompt.submit({ text: 'hi', wait: false, origin: { kind: 'composer' } })
  expect(await compact($, 'auto')).toEqual({ messages: MSGS })
  await f.clock.advance(1000)
  expect(f.argvs).toEqual([])
  expect(f.submits.length).toBe(1)
})

test('begin at used ≥ 70: pdx relay begin --self with usage and window; status line set; wait loop starts from a timer', async ($, on) => {
  const f = relayWorld(on, { pdx: pdxWith([]), usage: AT72 })
  await start($, f)
  await turnAndSettle($, f, 't1')
  expect(f.argvs.map(sub).slice(1)).toEqual(['relay begin --self --session sid-old --used 72 --window 200000'])
  expect(f.statuses).toEqual(['接力等待核准中'])
  await f.clock.advance(50)
  expect(f.argvs.map(sub).at(-1)).toBe('relay wait req-1')
  expect(f.toasts).toEqual(['接力等待核准：請在 Purdex App 按核准或拒絕'])
})

test('below the threshold nothing is asked', async ($, on) => {
  const f = relayWorld(on, { pdx: pdxWith([]), usage: { tokens: 100000, window: 200000, percent: 50 } })
  await start($, f)
  await turnAndSettle($, f, 't1')
  expect(f.argvs.length).toBe(1) // hello only
})

test('PDX_RELAY_THRESHOLD lowers the threshold for an acceptance run', async ($, on) => {
  const f = relayWorld(on, { pdx: pdxWith([]), usage: { tokens: 12000, window: 200000, percent: 6 } }, { PDX_RELAY_THRESHOLD: '5' })
  await start($, f)
  await turnAndSettle($, f, 't1')
  expect(count(f, 'begin')).toBe(1)
})

test('a member does not self-relay', async ($, on) => {
  const f = relayWorld(on, { pdx: pdxWith([], 'member'), usage: { tokens: 180000, window: 200000, percent: 90 } })
  await start($, f)
  await turnAndSettle($, f, 't1')
  expect(f.argvs.length).toBe(1)
})

test('self_relay_off / self_relay_paused (exit 13) are respected: no wait, idle, asks again only at +10', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  f.pdx = (argv) => argv[1] === 'begin' ? { exitCode: 13, stderr: 'pdx relay: self_relay_paused' } : { exitCode: 0, stdout: HELLO() }
  await start($, f)
  await turnAndSettle($, f, 't1')
  expect(count(f, 'begin')).toBe(1)
  expect(count(f, 'wait')).toBe(0)
  expect(f.statuses).toEqual([])
  f.usage = { tokens: 150000, window: 200000, percent: 79 }
  await turnAndSettle($, f, 't2')
  expect(count(f, 'begin')).toBe(1)
  f.usage = { tokens: 164000, window: 200000, percent: 82 }
  await turnAndSettle($, f, 't3')
  expect(count(f, 'begin')).toBe(2)
})

test('member_relay_is_leads (exit 13) makes the mod a member: it never asks again', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  f.pdx = (argv) => argv[1] === 'begin' ? { exitCode: 13, stderr: 'pdx relay: 這個 session 是 member member_relay_is_leads' } : { exitCode: 0, stdout: HELLO() }
  await start($, f)
  await turnAndSettle($, f, 't1')
  f.usage = { tokens: 190000, window: 200000, percent: 95 }
  await turnAndSettle($, f, 't2')
  expect(count(f, 'begin')).toBe(1)
})

test('daemon unreachable (exit 20) at begin: nothing, no wait, no status', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  f.pdx = (argv) => argv[1] === 'begin' ? { exitCode: 20, stderr: 'daemon unavailable' } : { exitCode: 0, stdout: HELLO() }
  await start($, f)
  await turnAndSettle($, f, 't1')
  await f.clock.advance(1000)
  expect(count(f, 'wait')).toBe(0)
  expect(f.statuses).toEqual([])
})

// The P5b-1 contract for every daemon call: a daemon that is down answers
// only after the client's 30 s grace, and no turn end may wait for that.
// Mutation gate: await begin inside the turn.complete hook → `done` stays false.
test('a begin that hangs never holds the turn end', async ($, on) => {
  let release!: () => void
  const gate = new Promise<void>((r) => { release = r })
  const f = relayWorld(on, { usage: AT72 })
  f.pdx = (argv) => argv[1] === 'begin' ? gate.then(() => ({ exitCode: 20 })) : { exitCode: 0, stdout: HELLO() }
  await start($, f)
  let done = false
  const t = turn($, 't1').then(() => { done = true })
  await f.clock.settle()
  expect(done).toBe(true)
  expect(count(f, 'begin')).toBe(1)
  release()
  await t
  await f.clock.settle()
})

// ---------- P5b-2: wait → write → check → clear → seed → done ----------

async function approvedRelay($: any, on: any, waits = [{ exitCode: 0, stdout: APPROVAL('approved') }], more: Partial<Fake> = {}) {
  const f = relayWorld(on, { pdx: pdxWith(waits), usage: AT72, ...more })
  const clock = f.clock
  await start($, f)
  await turn($, 't1')
  await clock.advance(50) // begin (a 0 ms timer), then the wait timer
  await clock.advance(50) // the write-prompt timer after approval
  return { f, clock }
}

test('wait loops on a still-open answer and, once approved, submits the write prompt (8 sections, op nonce) and reports writing', async ($, on) => {
  const { f } = await approvedRelay($, on, [{ exitCode: 0, stdout: APPROVAL('open') }, { exitCode: 0, stdout: APPROVAL('open') }, { exitCode: 0, stdout: APPROVAL('approved') }])
  expect(count(f, 'wait')).toBe(3)
  expect(f.submits.length).toBe(1)
  const text = f.submits[0].text as string
  expect(text).toMatch(/^\[pdx-relay op=op-1 n=[0-9a-f]{12,}\] /) // P5b-2 review: an unpredictable nonce beside the op id
  expect(text).toContain('/data/relay/op-1.md')
  for (const h of ['## 1.', '## 2.', '## 3.', '## 4.', '## 5.', '## 6.', '## 7.', '## 8.']) expect(text).toContain(h)
  expect(text).toContain('接力檔')
  expect(text).toContain('mlab/purdex-x [abc123]') // pdx msg whoami, pasted into the facts
  expect(text).not.toContain('交接')
  expect(reports(f)).toEqual(['relay report op-1 writing'])
  expect(f.statuses).toEqual(['接力等待核准中', undefined])
})

// §8.7 (d): only state "approved" starts the write turn.
for (const [name, w] of [['denied (10)', { exitCode: 10 }], ['timeout (11)', { exitCode: 11 }], ['cancelled (12)', { exitCode: 12 }], ['unreachable (20)', { exitCode: 20 }], ['an exit-0 body that is not approved', { exitCode: 0, stdout: APPROVAL('weird') }]] as const) {
  test(`wait ${name}: not approved — no write prompt, no report, status cleared`, async ($, on) => {
    const { f } = await approvedRelay($, on, [w])
    await f.clock.advance(1000)
    expect(f.submits).toEqual([])
    expect(reports(f)).toEqual([])
    expect(f.statuses).toEqual(['接力等待核准中', undefined])
  })
}

// Mutation gates: recognise the write turn by state instead of by turnId, or
// by a prompt.submit hook (a plugin's own hook never sees its own submit,
// MP3) → red; run the /clear inside the hook instead of from a timer → the
// `commands` stays-empty assertion goes red.
test('the write turn is recognised by its own turn: a queued prompt that runs first does not trigger the check', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: 'the user’s queued prompt', turnId: 'tq' })
  await turnAndSettle($, f, 'tq')
  expect(reports(f)).not.toContain('relay report op-1 written')
  expect(f.commands).toEqual([])
  await $.turn.start({ text: 'The purdex plugin sent a message: ' + f.submits[0].text, turnId: 'tw' })
  await turnAndSettle($, f, 'tw')
  expect(reports(f)).toContain('relay report op-1 written')
  expect(f.commands).toEqual([])
  await clock.advance(50)
  expect(f.commands).toEqual(['clear'])
})

test('file check: missing headings get two fix rounds, then failed{handoff_incomplete} and no clear', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = '# HANDOFF\n## 1. a\n' + 'x'.repeat(300)
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.submits.length).toBe(2)
  expect(f.submits[1].text).toContain('不完整。\n缺少段落：## 2.、## 3.') // P9a deviation 3: the fixed tail is a line of its own
  await $.turn.start({ text: f.submits[1].text, turnId: 'tf1' })
  await turn($, 'tf1')
  await clock.advance(50)
  expect(f.submits.length).toBe(3)
  await $.turn.start({ text: f.submits[2].text, turnId: 'tf2' })
  await turn($, 'tf2')
  await clock.advance(50)
  expect(f.submits.length).toBe(3)
  expect(reports(f)).toContain('relay report op-1 failed --error handoff_incomplete')
  expect(f.commands).toEqual([])
  expect(f.toasts).toContain('接力檔不完整，已放棄接力；對話照常繼續')
})

test('a short file (≤ 200 chars) with all headings is incomplete too', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = ['## 1.', '## 2.', '## 3.', '## 4.', '## 5.', '## 6.', '## 7.', '## 8.'].join('\n')
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.submits[1].text).toContain('(內容過短)')
})

test('cleared: report cleared --new-session, hello again, seed prompt ↪ 接手自 <old ref>; the seed turn reports done and sets the floor', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.commands).toEqual(['clear'])
  f.sessionId = 'sid-new'
  await $.classic.SessionStart({ source: 'clear' })
  await clock.settle()
  const calls = f.argvs.map(sub)
  expect(calls).toContain('relay report op-1 cleared --new-session sid-new')
  expect(calls.filter((c) => c.startsWith('relay hello')).at(-1)).toBe('relay hello --session sid-new --version 1 --agent cc')
  await clock.advance(50)
  expect(f.submits.length).toBe(2)
  expect(f.submits[1].text.split('\n')[0]).toBe('↪ 接手自 _abc123')
  expect(f.submits[1].text).toMatch(/\n\[pdx-relay seed op=op-1 n=[0-9a-f]{12,}\] /)
  expect(f.submits[1].text).not.toContain('交接')
  f.usage = { tokens: 30000, window: 200000, percent: 15 }
  await $.turn.start({ text: f.submits[1].text, turnId: 'ts' })
  await turnAndSettle($, f, 'ts')
  expect(reports(f)).toEqual(['relay report op-1 writing', 'relay report op-1 written', 'relay report op-1 cleared --new-session sid-new', 'relay report op-1 done'])
  // the 20K loop guard: 75 % but only 10K over the floor → no new ask
  f.usage = { tokens: 40000, window: 200000, percent: 75 }
  await turnAndSettle($, f, 't9')
  expect(count(f, 'begin')).toBe(1)
  f.usage = { tokens: 50000, window: 200000, percent: 75 }
  await turnAndSettle($, f, 't10')
  expect(count(f, 'begin')).toBe(2)
})

// §8.3: a report that did not reach the daemon (20 unreachable — also a
// cleared the daemon answered 503 not_ready for through the CLI's grace —
// 21 unsupported) is re-sent at the next turn.complete and the relay goes
// on. Exit 1 is permanent since the P5b-2 review (its own test below).
// Mutation gate: drop either of the two instead → red.
for (const code of [20, 21]) {
  test(`a report that fails with ${code} is re-sent at the next turn.complete and the relay goes on`, async ($, on) => {
    const { f, clock } = await approvedRelay($, on)
    let failWritten = true
    const inner = f.pdx
    f.pdx = (argv) => (argv[1] === 'report' && argv[3] === 'written' && failWritten ? { exitCode: code } : inner(argv))
    f.files['/data/relay/op-1.md'] = GOOD_FILE
    await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
    await turn($, 'tw')
    await clock.advance(50)
    expect(f.commands).toEqual(['clear']) // relaying went on despite the failed report
    failWritten = false
    await turnAndSettle($, f, 'tx')
    expect(reports(f).filter((c) => c === 'relay report op-1 written').length).toBe(2)
    await turnAndSettle($, f, 'ty')
    expect(reports(f).filter((c) => c === 'relay report op-1 written').length).toBe(2) // landed: not sent again
  })
}

// Mutation gate: queue a 13 like the others → it is re-sent at `tx` → red.
test('a report refused with 13 bad_transition is dropped, not re-sent (the daemon is already past it)', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  const inner = f.pdx
  f.pdx = (argv) => (argv[1] === 'report' && argv[3] === 'written' ? { exitCode: 13, stderr: 'pdx relay: state cleared does not lead to written bad_transition' } : inner(argv))
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.commands).toEqual(['clear'])
  await turnAndSettle($, f, 'tx')
  await turnAndSettle($, f, 'ty')
  expect(reports(f).filter((c) => c === 'relay report op-1 written').length).toBe(1) // sent once, never queued
})

// The daemon refuses cleared → done (written → done is bad_transition), so a
// done sent while cleared is still queued would be dropped for good: the
// op's later reports wait behind the one that did not land.
test('a cleared that does not land holds done behind it until it lands', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  let clearedFails = 2
  const inner = f.pdx
  f.pdx = (argv) => (argv[1] === 'report' && argv[3] === 'cleared' && clearedFails-- > 0 ? { exitCode: 20 } : inner(argv))
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  f.sessionId = 'sid-new'
  await $.classic.SessionStart({ source: 'clear' })
  await clock.advance(50)
  expect(f.submits.length).toBe(2) // the seed went out anyway
  await $.turn.start({ text: f.submits[1].text, turnId: 'ts' })
  await turnAndSettle($, f, 'ts') // cleared re-sent and fails again: done must not go out
  expect(reports(f).slice(2)).toEqual(['relay report op-1 cleared --new-session sid-new', 'relay report op-1 cleared --new-session sid-new'])
  await turnAndSettle($, f, 'tz')
  expect(reports(f).slice(2)).toEqual(['relay report op-1 cleared --new-session sid-new', 'relay report op-1 cleared --new-session sid-new', 'relay report op-1 cleared --new-session sid-new', 'relay report op-1 done'])
})

// Mutation gate: await the cleared report inside the SessionStart hook → `cleared` stays false.
test('a report that hangs never holds a turn end or the /clear', async ($, on) => {
  let release!: () => void
  const gate = new Promise<void>((r) => { release = r })
  const { f, clock } = await approvedRelay($, on)
  const inner = f.pdx
  f.pdx = async (argv) => { if (argv[1] === 'report') await gate; return inner(argv) }
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  let done = false
  const t = turn($, 'tw').then(() => { done = true })
  await clock.settle()
  expect(done).toBe(true)
  await clock.advance(50)
  expect(f.commands).toEqual(['clear'])
  f.sessionId = 'sid-new'
  let cleared = false
  const c = $.classic.SessionStart({ source: 'clear' }).then(() => { cleared = true })
  await clock.settle()
  expect(cleared).toBe(true)
  release()
  await t
  await c
  await clock.settle()
})

// Mutation gate: allow any path under the relay dir, a prefix, or any path → red.
test('tool.check allows Write/Edit to exactly the handoff path while an op is pending, nothing else', async ($, on) => {
  await approvedRelay($, on)
  expect((await $.tool.check({ tool: 'Write', input: { file_path: '/data/relay/op-1.md', content: 'x' } })).decision).toBe('allow')
  expect((await $.tool.check({ tool: 'Edit', input: { file_path: '/data/relay/op-1.md', old_string: 'a', new_string: 'b' } })).decision).toBe('allow')
  expect((await $.tool.check({ tool: 'Write', input: { file_path: '/data/relay/op-2.md', content: 'x' } })).decision).toBe('ask')
  expect((await $.tool.check({ tool: 'Write', input: { file_path: '/data/relay/op-1.md.bak', content: 'x' } })).decision).toBe('ask')
  expect((await $.tool.check({ tool: 'Edit', input: { file_path: '/data/relay/op-2.md', old_string: 'a', new_string: 'b' } })).decision).toBe('ask')
  expect((await $.tool.check({ tool: 'Bash', input: { command: 'rm -rf /' } })).decision).toBe('ask')
})

test('tool.check allows nothing while no op is pending', async ($, on) => {
  const f = relayWorld(on, { pdx: pdxWith([]) })
  await start($, f)
  expect((await $.tool.check({ tool: 'Write', input: { file_path: '/data/relay/op-1.md', content: 'x' } })).decision).toBe('ask')
})

// The hello after /clear is P5b-1's (its own tests above); this one pins
// that the user's own /clear also resets the relay guards: the +10 re-ask
// of the old conversation no longer holds. Mutation gate: drop the reset → red.
test('the user’s own /clear while idle resets the guards and says hello again', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  f.pdx = (argv) => argv[1] === 'begin' ? { exitCode: 13, stderr: 'pdx relay: paused self_relay_paused' } : { exitCode: 0, stdout: HELLO() }
  await start($, f)
  await turnAndSettle($, f, 't1')
  await turnAndSettle($, f, 't2')
  expect(count(f, 'begin')).toBe(1) // 72 again: not 10 points over the last ask
  f.sessionId = 'sid-2'
  await $.classic.SessionStart({ source: 'clear' })
  await f.clock.settle()
  await turnAndSettle($, f, 't3')
  expect(count(f, 'begin')).toBe(2)
  expect(f.argvs.map(sub).filter((c) => c.startsWith('relay hello'))).toEqual(['relay hello --session sid-old --version 1 --agent cc', 'relay hello --session sid-2 --version 1 --agent cc'])
})

// A request the user's own /clear dropped keeps its wait loop running (the
// daemon closes it); the next request must get a loop of its own, not the
// old one's promise. Mutation gate: share one wait promise across requests → red.
test('after the user’s own /clear drops an open request, the next request waits on its own loop', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const op2 = { ...OP, id: 'op-2', session_id: 'sid-2', handoff_path: '/data/relay/op-2.md' }
  f.pdx = (argv) => {
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO() }
    if (argv[1] === 'begin') return { exitCode: 0, stdout: argv.includes('sid-2') ? JSON.stringify({ op: op2, request_id: 'req-2' }) : BEGIN_OK }
    if (argv[1] === 'wait') return argv[2] === 'req-2' ? { exitCode: 0, stdout: APPROVAL('approved') } : new Promise<never>(() => {})
    return { exitCode: 0, stdout: '{}' }
  }
  await start($, f)
  await turn($, 't1')
  await f.clock.advance(50) // begin op-1, its wait (never answers)
  f.sessionId = 'sid-2'
  await $.classic.SessionStart({ source: 'clear' })
  await f.clock.settle()
  await turn($, 't2')
  await f.clock.advance(50) // begin op-2, its wait (approved)
  await f.clock.advance(50) // the write prompt
  expect(f.argvs.map(sub).filter((c) => c.startsWith('relay wait'))).toEqual(['relay wait req-1', 'relay wait req-2'])
  expect(f.submits.length).toBe(1)
  expect(f.submits[0].text.startsWith('[pdx-relay op=op-2 n=')).toBe(true)
})

// P5b-1 review: the mod reaches the daemon that installed it. Mutation gate:
// drop --config from any call (relay or msg whoami) → red.
test('with a config in pdx.json every pdx call of a relay carries --config', async ($, on) => {
  const { f, clock } = await approvedRelay($, on, undefined, { pdxJSON: PDX_JSON })
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  f.sessionId = 'sid-new'
  await $.classic.SessionStart({ source: 'clear' })
  await clock.advance(50)
  await $.turn.start({ text: f.submits[1].text, turnId: 'ts' })
  await turnAndSettle($, f, 'ts')
  const kinds = f.argvs.map((a) => a.slice(1, 3).join(' '))
  expect(kinds).toEqual(expect.arrayContaining(['relay hello', 'relay begin', 'relay wait', 'msg whoami', 'relay report']))
  expect(reports(f).length).toBe(4)
  for (const a of f.argvs) {
    expect(a[0]).toBe('/opt/pdx/bin/pdx')
    expect(a.slice(-2)).toEqual(['--config', '/tmp/pdx b/config.toml'])
  }
})

// ---------- P5b-2 review (codex R1 + R2) ----------

const opN = (n: number) => ({ ...OP, id: 'op-' + n, handoff_path: '/data/relay/op-' + n + '.md' })
const beginOK = (n: number) => JSON.stringify({ op: opN(n), request_id: 'req-' + n })
function gated() {
  let release!: () => void
  const p = new Promise<void>((r) => { release = r })
  return { p, release }
}
const argOf = (argv: string[], flag: string) => argv[argv.indexOf(flag) + 1]
const waits = (f: Fake) => f.argvs.map(sub).filter((c) => c.startsWith('relay wait'))
const writeAllowed = async ($: any, path: string) => (await $.tool.check({ tool: 'Write', input: { file_path: path, content: 'x' } })).decision === 'allow'

// Item 1 (R1 P1 + attacker high): a begin's answer belongs to the generation
// and the session it was sent from. Mutation gate: adopt on
// `state === 'beginning'` alone → op-1 becomes the pending op, op-2 is never
// adopted and op-1's dialog is never closed.
test('a begin that answers after the user’s /clear and a newer begin is cancelled{abandoned}; the new session keeps its own op', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const older = gated()
  const newer = gated()
  f.pdx = async (argv) => {
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO() }
    if (argv[1] === 'begin') {
      const first = argOf(argv, '--session') === 'sid-old'
      await (first ? older.p : newer.p)
      return { exitCode: 0, stdout: beginOK(first ? 1 : 2) }
    }
    if (argv[1] === 'wait') return new Promise<never>(() => {})
    return { exitCode: 0, stdout: '{}' }
  }
  await start($, f)
  await turnAndSettle($, f, 't1') // begin under sid-old: hangs
  f.sessionId = 'sid-2'
  await $.classic.SessionStart({ source: 'clear' })
  await f.clock.settle() // hello under sid-2
  await turnAndSettle($, f, 't2') // begin under sid-2: hangs too
  expect(count(f, 'begin')).toBe(2)
  older.release()
  await f.clock.advance(50)
  expect(reports(f)).toEqual(['relay report op-1 cancelled --error abandoned'])
  expect(waits(f)).toEqual([])
  newer.release()
  await f.clock.advance(50)
  expect(waits(f)).toEqual(['relay wait req-2'])
  expect(await writeAllowed($, '/data/relay/op-2.md')).toBe(true)
  expect(await writeAllowed($, '/data/relay/op-1.md')).toBe(false)
  expect(reports(f)).toEqual(['relay report op-1 cancelled --error abandoned'])
})

// Mutation gate: drop the session-id comparison → op-1 is adopted under sid-x.
test('a begin whose session id changed under it (no /clear seen) is cancelled{abandoned} and the mod is idle again', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const g = gated()
  f.pdx = async (argv) => {
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO() }
    if (argv[1] === 'begin') { await g.p; return { exitCode: 0, stdout: BEGIN_OK } }
    if (argv[1] === 'wait') return new Promise<never>(() => {})
    return { exitCode: 0, stdout: '{}' }
  }
  await start($, f)
  await turnAndSettle($, f, 't1')
  f.sessionId = 'sid-x'
  g.release()
  await f.clock.advance(50)
  expect(reports(f)).toEqual(['relay report op-1 cancelled --error abandoned'])
  expect(waits(f)).toEqual([])
  expect(f.statuses).toEqual([])
  f.usage = { tokens: 164000, window: 200000, percent: 82 }
  await turnAndSettle($, f, 't2') // idle again: +10 points asks again
  expect(count(f, 'begin')).toBe(2)
})

// Item 2 (R1 P2): the daemon's threshold is the one that counts, so nothing
// is asked before hello has answered. Mutation gate: drop the helloOK check
// in maybeBegin → t1 begins at 75 % on the default 70.
test('no begin before hello answers: a 75 % turn ahead of the answer asks nothing, then the daemon’s threshold 90 holds', async ($, on) => {
  const f = relayWorld(on, { usage: { tokens: 150000, window: 200000, percent: 75 } })
  const g = gated()
  f.pdx = async (argv) => {
    if (argv[1] === 'hello') { await g.p; return { exitCode: 0, stdout: HELLO('none', { threshold: 90 }) } }
    if (argv[1] === 'begin') return { exitCode: 0, stdout: BEGIN_OK }
    if (argv[1] === 'wait') return new Promise<never>(() => {})
    return { exitCode: 0, stdout: '{}' }
  }
  await start($, f) // hello hangs
  await turnAndSettle($, f, 't1')
  expect(count(f, 'begin')).toBe(0)
  expect(count(f, 'hello')).toBe(1) // one hello in flight: the turn end does not send another
  g.release()
  await f.clock.settle()
  await turnAndSettle($, f, 't2')
  expect(count(f, 'begin')).toBe(0) // 75 < 90
  f.usage = { tokens: 182000, window: 200000, percent: 91 }
  await turnAndSettle($, f, 't3')
  expect(count(f, 'begin')).toBe(1)
})

// Mutation gate: drop the re-send at turn.complete → one hello, never a begin.
test('a failed hello is sent again from a timer at the next turn end (never awaited there); no begin until one answers', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const g = gated()
  let hellos = 0
  f.pdx = async (argv) => {
    if (argv[1] === 'hello') {
      if (++hellos === 1) return { exitCode: 20, stderr: 'daemon unavailable' }
      await g.p
      return { exitCode: 0, stdout: HELLO() }
    }
    if (argv[1] === 'begin') return { exitCode: 0, stdout: BEGIN_OK }
    if (argv[1] === 'wait') return new Promise<never>(() => {})
    return { exitCode: 0, stdout: '{}' }
  }
  await start($, f)
  expect(count(f, 'hello')).toBe(1)
  let done = false
  const t = turn($, 't1').then(() => { done = true })
  await f.clock.settle()
  expect(done).toBe(true) // the second hello hangs; the turn end did not wait for it
  expect(count(f, 'hello')).toBe(2)
  expect(count(f, 'begin')).toBe(0)
  g.release()
  await t
  await f.clock.settle()
  await turnAndSettle($, f, 't2')
  expect(count(f, 'begin')).toBe(1)
  expect(count(f, 'hello')).toBe(2) // answered: no more hellos
})

// Mutation gate: keep helloOK across /clear → t1 begins under sid-2 before its hello answered.
test('after /clear nothing is asked until the new session’s hello answers', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const g = gated()
  f.pdx = async (argv) => {
    if (argv[1] === 'hello') { if (argOf(argv, '--session') === 'sid-2') await g.p; return { exitCode: 0, stdout: HELLO() } }
    if (argv[1] === 'begin') return { exitCode: 0, stdout: BEGIN_OK }
    if (argv[1] === 'wait') return new Promise<never>(() => {})
    return { exitCode: 0, stdout: '{}' }
  }
  await start($, f)
  f.sessionId = 'sid-2'
  await $.classic.SessionStart({ source: 'clear' })
  await f.clock.settle()
  await turnAndSettle($, f, 't1')
  expect(count(f, 'begin')).toBe(0)
  g.release()
  await f.clock.settle()
  await turnAndSettle($, f, 't2')
  expect(count(f, 'begin')).toBe(1)
})

// Item 3 (attacker high): a step deferred to a timer that fails ends the
// relay — the same request still in the same state reports and goes idle —
// instead of leaving the mod in approved / clearing / seeding for good.
// Mutation gates: drop the catch of the step → the report is missing and the
// mod stays where it was (the handoff path still allowed, no new begin).
const AT82 = { tokens: 164000, window: 200000, percent: 82 }

for (const how of ['reject', 'drop'] as const) {
  test(`a write prompt the session refuses (${how}) reports failed{handoff_incomplete} and the mod is idle again`, async ($, on) => {
    const { f } = await approvedRelay($, on, undefined, { failSubmit: () => how })
    expect(f.submits.length).toBe(1)
    await f.clock.settle()
    expect(reports(f)).toEqual(['relay report op-1 failed --error handoff_incomplete'])
    expect(await writeAllowed($, '/data/relay/op-1.md')).toBe(false)
    f.usage = AT82
    await turnAndSettle($, f, 't2') // idle: +10 points asks again
    expect(count(f, 'begin')).toBe(2)
  })
}

test('a fix prompt the session refuses reports failed{handoff_incomplete} and the mod is idle again', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.failSubmit = (text) => (text.includes('不完整') ? 'reject' : undefined)
  f.files['/data/relay/op-1.md'] = '# HANDOFF\n## 1. a\n' + 'x'.repeat(300)
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.submits.length).toBe(2)
  expect(reports(f)).toEqual(['relay report op-1 writing', 'relay report op-1 failed --error handoff_incomplete'])
  expect(await writeAllowed($, '/data/relay/op-1.md')).toBe(false)
  f.usage = AT82
  await turnAndSettle($, f, 't2')
  expect(count(f, 'begin')).toBe(2)
})

test('a /clear the session refuses reports cancelled{abandoned} (written → cancelled) and the mod is idle again', async ($, on) => {
  const { f, clock } = await approvedRelay($, on, undefined, { failCommand: 'a turn is running' })
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.commands).toEqual(['clear'])
  await clock.settle()
  expect(reports(f)).toEqual(['relay report op-1 writing', 'relay report op-1 written', 'relay report op-1 cancelled --error abandoned'])
  expect(await writeAllowed($, '/data/relay/op-1.md')).toBe(false)
  f.usage = AT82
  await turnAndSettle($, f, 't2')
  expect(count(f, 'begin')).toBe(2)
})

test('a seed prompt the session refuses reports failed{handoff_incomplete}, toasts where the handoff is, and the new conversation is asked afresh', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.failSubmit = (text) => (text.startsWith('↪') ? 'reject' : undefined)
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  f.sessionId = 'sid-new'
  await $.classic.SessionStart({ source: 'clear' })
  await clock.advance(50)
  expect(f.submits.length).toBe(2)
  await clock.settle()
  expect(reports(f)).toEqual(['relay report op-1 writing', 'relay report op-1 written', 'relay report op-1 cleared --new-session sid-new', 'relay report op-1 failed --error handoff_incomplete'])
  expect(f.toasts).toContain('接力未完成：接力檔在 /data/relay/op-1.md，可手動貼給新 session')
  expect(await writeAllowed($, '/data/relay/op-1.md')).toBe(false)
  await turnAndSettle($, f, 't2') // a cleared conversation: 72 % asks again (the old +10 guard was the old conversation's)
  expect(count(f, 'begin')).toBe(2)
})

// Item 4 (attacker high): the turn a prompt of the mod's starts is told by a
// nonce minted for that one prompt, accepted only in the state it was sent
// in and only once. Mutation gates: recognise by the op id (the old tag) →
// the op-id-only turn runs the check; drop the seal → the echo `tw2`
// overwrites the write turn and `tw` checks nothing; keep one nonce for
// write and fix → the old write text starts a fix round.
const nonceOf = (text: string) => (text.match(/ n=([0-9a-f]+)\]/) || [])[1]

test('the write turn is told by a fresh nonce, once: the op id alone, an echo of the prompt and an old nonce are not the relay’s turn', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = '# HANDOFF\n## 1. a\n' + 'x'.repeat(300) // incomplete: fix rounds follow
  const n1 = nonceOf(f.submits[0].text)
  expect(n1).toMatch(/^[0-9a-f]{12,}$/)
  await $.turn.start({ text: 'look at [pdx-relay op=op-1] again', turnId: 'tu' }) // the op id is not the nonce
  await turn($, 'tu')
  await clock.advance(50)
  expect(f.submits.length).toBe(1)
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw2' }) // the same text again: tw stays the write turn
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.submits.length).toBe(2) // tw was checked: the fix prompt went out
  const n2 = nonceOf(f.submits[1].text)
  expect(n2).toMatch(/^[0-9a-f]{12,}$/)
  expect(n2).not.toBe(n1)
  await $.turn.start({ text: f.submits[0].text, turnId: 'to' }) // the write prompt's nonce is spent
  await turn($, 'to')
  await clock.advance(50)
  expect(f.submits.length).toBe(2)
  await $.turn.start({ text: f.submits[1].text, turnId: 'tf' })
  await turn($, 'tf')
  await clock.advance(50)
  expect(f.submits.length).toBe(3) // the real fix turn was checked: the second fix round
  expect(reports(f)).toEqual(['relay report op-1 writing'])
})

test('while seeding, a turn with the old seed tag or the write prompt is not the seed turn: done waits for the real one, nothing is written or cleared twice', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  f.sessionId = 'sid-new'
  await $.classic.SessionStart({ source: 'clear' })
  await clock.advance(50)
  expect(f.submits.length).toBe(2)
  await $.turn.start({ text: '[pdx-relay seed op=op-1] injected', turnId: 'tx' })
  await turnAndSettle($, f, 'tx')
  await $.turn.start({ text: f.submits[0].text, turnId: 'ty' }) // the write prompt again, in seeding
  await turnAndSettle($, f, 'ty')
  await clock.advance(50)
  expect(reports(f)).toEqual(['relay report op-1 writing', 'relay report op-1 written', 'relay report op-1 cleared --new-session sid-new'])
  expect(f.commands).toEqual(['clear'])
  await $.turn.start({ text: f.submits[1].text, turnId: 'ts' })
  await turnAndSettle($, f, 'ts')
  expect(reports(f).at(-1)).toBe('relay report op-1 done')
})

// Item 5 (attacker medium): the user's own /clear in the middle of a relay
// ends the relay at the daemon before the mod starts over, so no dialog or
// op is left waiting on a mod that moved on. Mutation gate: drop the report
// in the user's /clear branch → the awaiting / approved / seeding tests go
// red (beginning is the generation bump of item 1).
test('the user’s /clear while beginning: the late op is cancelled{abandoned}, nothing waits on it', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const g = gated()
  f.pdx = async (argv) => {
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO() }
    if (argv[1] === 'begin') { await g.p; return { exitCode: 0, stdout: BEGIN_OK } }
    if (argv[1] === 'wait') return new Promise<never>(() => {})
    return { exitCode: 0, stdout: '{}' }
  }
  await start($, f)
  await turnAndSettle($, f, 't1')
  f.sessionId = 'sid-2'
  await $.classic.SessionStart({ source: 'clear' })
  await f.clock.settle()
  g.release()
  await f.clock.advance(50)
  expect(reports(f)).toEqual(['relay report op-1 cancelled --error abandoned'])
  expect(waits(f)).toEqual([])
  expect(await writeAllowed($, '/data/relay/op-1.md')).toBe(false)
})

test('the user’s /clear while awaiting reports cancelled{abandoned} (the daemon closes the dialog), clears the status and starts over', async ($, on) => {
  const f = relayWorld(on, { pdx: pdxWith([]), usage: AT72 })
  await start($, f)
  await turn($, 't1')
  await f.clock.advance(50) // begin, then the wait (never answers)
  expect(waits(f)).toEqual(['relay wait req-1'])
  f.sessionId = 'sid-2'
  await $.classic.SessionStart({ source: 'clear' })
  await f.clock.settle()
  expect(reports(f)).toEqual(['relay report op-1 cancelled --error abandoned'])
  expect(f.statuses).toEqual(['接力等待核准中', undefined])
  expect(await writeAllowed($, '/data/relay/op-1.md')).toBe(false)
  await turnAndSettle($, f, 't2') // the new conversation is asked afresh
  expect(count(f, 'begin')).toBe(2)
})

test('the user’s /clear while approved reports cancelled{abandoned}; the write turn that follows is nobody’s', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  f.sessionId = 'sid-2'
  await $.classic.SessionStart({ source: 'clear' })
  await clock.settle()
  expect(reports(f)).toEqual(['relay report op-1 writing', 'relay report op-1 cancelled --error abandoned'])
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turnAndSettle($, f, 'tw')
  await clock.advance(50)
  expect(reports(f)).toEqual(['relay report op-1 writing', 'relay report op-1 cancelled --error abandoned'])
  expect(f.commands).toEqual([])
})

test('the user’s /clear while seeding reports failed{handoff_incomplete}; the seed turn that follows reports nothing', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  f.sessionId = 'sid-new'
  await $.classic.SessionStart({ source: 'clear' }) // the mod's own: seeding
  await clock.advance(50)
  expect(f.submits.length).toBe(2)
  f.sessionId = 'sid-3'
  await $.classic.SessionStart({ source: 'clear' }) // the user's, before the seed turn ran
  await clock.settle()
  const before = ['relay report op-1 writing', 'relay report op-1 written', 'relay report op-1 cleared --new-session sid-new']
  expect(reports(f)).toEqual([...before, 'relay report op-1 failed --error handoff_incomplete'])
  await $.turn.start({ text: f.submits[1].text, turnId: 'ts' })
  await turnAndSettle($, f, 'ts')
  expect(reports(f)).toEqual([...before, 'relay report op-1 failed --error handoff_incomplete'])
})

// Item 6 (attacker medium) as corrected by the critic: a report that fails
// with 20 / 21 / 1 is re-sent (1 covers a transient daemon 500), only so
// often, and the queue is bounded; any other code (2: usage) is dropped at
// once. Mutation gates: re-send exit 2 → it goes out again at `tx`; drop the
// re-send cap → 26 sends; drop the queue cap → op-1 is held and re-sent at
// `tz`; drop 1 from the transient set → the exit-1 test sees one send.
test('a report that fails with 2 (usage) is dropped and logged, never re-sent; the op’s later reports go on', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  const inner = f.pdx
  f.pdx = (argv) => (argv[1] === 'report' && argv[3] === 'written' ? { exitCode: 2, stderr: 'pdx relay: usage' } : inner(argv))
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.commands).toEqual(['clear'])
  await turnAndSettle($, f, 'tx')
  await turnAndSettle($, f, 'ty')
  expect(reports(f).filter((c) => c === 'relay report op-1 written').length).toBe(1)
  expect(f.logs.some((l) => l.includes('exit 2') && l.includes('relay report op-1 written'))).toBe(true)
  f.sessionId = 'sid-new'
  await $.classic.SessionStart({ source: 'clear' })
  await clock.settle()
  expect(reports(f).at(-1)).toBe('relay report op-1 cleared --new-session sid-new') // not held behind the dropped one
})

test('a report that fails once with 1 (a transient daemon 500) is re-sent and lands', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  const inner = f.pdx
  let failed = false
  f.pdx = (argv) => {
    if (argv[1] === 'report' && argv[3] === 'written' && !failed) {
      failed = true
      return { exitCode: 1, stderr: 'pdx relay: team.db failed; see the daemon log storage_error' }
    }
    return inner(argv)
  }
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  await turnAndSettle($, f, 'tx')
  expect(reports(f).filter((c) => c === 'relay report op-1 written').length).toBe(2)
})

test('a report that keeps failing with 20 is re-sent 20 times, then dropped and logged', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  const inner = f.pdx
  f.pdx = (argv) => (argv[1] === 'report' && argv[3] === 'written' ? { exitCode: 20, stderr: 'daemon unavailable' } : inner(argv))
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  for (let i = 0; i < 25; i++) await turnAndSettle($, f, 'r' + i)
  expect(reports(f).filter((c) => c === 'relay report op-1 written').length).toBe(21) // the first send and 20 re-sends
  expect(f.logs.some((l) => l.includes('20 re-sends') && l.includes('relay report op-1 written'))).toBe(true)
})

test('the report queue holds 50: a 51st report pushes out the oldest (logged), which is never re-sent', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const g = gated()
  let n = 0
  f.pdx = async (argv) => {
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO() }
    if (argv[1] === 'begin') return { exitCode: 0, stdout: beginOK(++n) }
    if (argv[1] === 'wait') return new Promise<never>(() => {})
    if (argv[1] === 'report' && argv[2] === 'op-1') { await g.p; return { exitCode: 20, stderr: 'daemon unavailable' } }
    return { exitCode: 0, stdout: '{}' }
  }
  await start($, f)
  for (let i = 1; i <= 51; i++) {
    await turn($, 't' + i)
    await f.clock.advance(50) // begin op-i, its wait (never answers)
    f.sessionId = 'sid-' + i
    await $.classic.SessionStart({ source: 'clear' }) // awaiting → cancelled{abandoned} for op-i
    await f.clock.settle()
  }
  expect(n).toBe(51)
  expect(reports(f)).toEqual(['relay report op-1 cancelled --error abandoned']) // op-1's send hangs: the rest queue behind it
  expect(f.logs.some((l) => l.includes('queue full') && l.includes('relay report op-1 cancelled'))).toBe(true)
  g.release() // op-1 answers 20: it was pushed out, so it is not held
  await f.clock.settle()
  await turnAndSettle($, f, 'tz') // a held op-1 would be re-sent here
  const sent = reports(f)
  expect(sent.filter((c) => c.startsWith('relay report op-1 ')).length).toBe(1)
  for (let i = 2; i <= 51; i++) expect(sent.filter((c) => c === 'relay report op-' + i + ' cancelled --error abandoned').length).toBe(1)
})

// ---------- P5b-3: the hold, NOTE, denial, +10, compact, /relay ----------
// The hold (spec §8.7 (b)): the prompt.submit hook waits for the request's
// answer, which the timer's wait loop settles — the loop alone asks the
// daemon and moves the state — racing it with a local `/bin/sleep 5` of the
// hook's own, started again while the answer is out: a `$` call in flight
// stops the hook's 10 s budget (HookBudget) and asks the daemon nothing
// (P5b-3 critic). A prompt that arrives while `begin` is still out waits for
// begin's answer the same way (at most 40 s), then for the request it opened;
// the request itself at most 11 min. At most 16 prompts are held at once.
// Every failure lets the prompt go on unchanged (fail-open).

const NOTE = '接力已核准，這一輪只做簡短回應；如果這是一件新工作，不要開始做，把它寫進接力檔「下一步」的第一項，由接手後的新對話處理。'
const typed = ($: any, text: string, more: any = {}) => $.prompt.submit({ text, wait: false, origin: { kind: 'composer' }, ...more })

// A fake daemon holding the one self_relay approval row. Every `pdx relay
// wait` on it (the timer's loop) answers the row's state once it closes (at
// once when it already has); an open row answers {state:"open"} when the
// call's bound runs out (the 9 min default) on the mocked clock, or after
// `realOpenMs` of real time when given. The first close wins, as in the
// daemon; a cancelled report closes an open row (P5a-2b
// closeRequestOfReportedOp: every dialog closes and the waits exit 12).
const CLOSED: Record<string, number> = { denied: 10, timeout: 11, cancelled: 12 }
function approvalRow(f: Fake, realOpenMs?: number) {
  let state = 'open'
  const waiters: Array<() => void> = []
  const closed = () => (state === 'approved' ? { exitCode: 0, stdout: APPROVAL('approved') } : { exitCode: CLOSED[state], stderr: 'pdx relay: ' + state })
  return {
    decide(next: string) {
      if (state !== 'open') return
      state = next
      for (const w of waiters.splice(0)) w()
    },
    wait() {
      if (state !== 'open') return closed()
      return new Promise<any>((resolve) => {
        waiters.push(() => resolve(closed()))
        const bound = realOpenMs !== undefined ? new Promise((r) => setTimeout(r, realOpenMs)) : f.clock.sleep(540_000)
        // a wait whose dispatch was abandoned (the test ended under it) is dropped
        bound.then(() => resolve(state === 'open' ? { exitCode: 0, stdout: APPROVAL('open') } : closed()), () => {})
      })
    },
  }
}

type RowOpts = { realOpenMs?: number; begin?: () => any; wait?: (argv: string[]) => any; report?: (argv: string[]) => any }
function rowDaemon(f: Fake, o: RowOpts = {}) {
  const row = approvalRow(f, o.realOpenMs)
  f.pdx = (argv) => {
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO() }
    if (argv[1] === 'begin') return o.begin ? o.begin() : { exitCode: 0, stdout: BEGIN_OK }
    if (argv[1] === 'wait') return o.wait?.(argv) ?? row.wait()
    if (argv[1] === 'report') {
      if (o.report) return o.report(argv)
      if (argv[3] === 'cancelled') row.decide('cancelled')
    }
    if (argv[0] === 'msg') return { exitCode: 0, stdout: 'mlab/purdex-x [abc123]' }
    return { exitCode: 0, stdout: '{}' }
  }
  return row
}
const loopWaits = (f: Fake) => waits(f).filter((c) => !c.includes('--wait')) // the timer's loop
const holdWaits = (f: Fake) => waits(f).filter((c) => c.includes('--wait')) // a hold's own long poll: there is none (P5b-3 critic)
const SLEEP = { argv: ['/bin/sleep', '5'], timeoutMs: 10_000 } // the hold's local sleep and its bound

// Mutation gates: hold body → `return next(e)` → red at the held assertion;
// NOTE before the existing context → red at the context assertion.
test('a prompt that arrives while a request is open waits; on approval it runs in the current conversation with NOTE appended after existing context, and the write prompt follows', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const row = rowDaemon(f)
  const clock = f.clock
  await start($, f)
  await turn($, 't1')
  await clock.advance(50) // begin, then the wait loop from its timer
  const p = $.prompt.submit({ text: '請幫我看一下', context: ['prior'], wait: false, origin: { kind: 'composer' } })
  await clock.settle()
  expect(f.submits.length).toBe(0) // held
  row.decide('approved')
  await p
  expect(f.submits.length).toBe(1)
  expect(f.submits[0].text).toBe('請幫我看一下')
  expect(f.submits[0].context).toEqual(['prior', NOTE])
  await clock.advance(50)
  expect(f.submits.length).toBe(2)
  expect(f.submits[1].text.startsWith('[pdx-relay op=op-1 n=')).toBe(true)
  expect(loopWaits(f)).toEqual(['relay wait req-1']) // one loop for the request
  expect(holdWaits(f)).toEqual([]) // the held prompt asked the daemon nothing
  expect(f.sleeps).toEqual([SLEEP]) // it slept locally
})

test('on denial the held prompt passes unchanged (no NOTE); the request is gone; asking again only at +10 points', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const row = rowDaemon(f)
  await start($, f)
  await turn($, 't1')
  const p = $.prompt.submit({ text: 'hi', wait: false, origin: { kind: 'peer' } })
  await f.clock.advance(50)
  expect(f.submits.length).toBe(0)
  row.decide('denied')
  await p
  await f.clock.settle()
  expect(f.submits.length).toBe(1)
  expect(f.submits[0].context).toBeUndefined()
  expect(f.statuses).toEqual(['接力等待核准中', undefined])
  f.usage = { tokens: 160000, window: 200000, percent: 80 }
  await turnAndSettle($, f, 't2')
  expect(count(f, 'begin')).toBe(1)
  f.usage = AT82
  await turnAndSettle($, f, 't3')
  expect(count(f, 'begin')).toBe(2)
})

for (const [code, state] of [[11, 'timeout'], [20, '']] as const) {
  test('on exit ' + code + ' (timeout / daemon gone) the hold releases unchanged', async ($, on) => {
    const f = relayWorld(on, { usage: AT72 })
    const row = rowDaemon(f, { wait: () => (state ? undefined : { exitCode: code, stderr: 'daemon unavailable' }) })
    await start($, f)
    await turn($, 't1')
    const p = typed($, 'hi')
    await f.clock.advance(50)
    if (state) row.decide(state)
    await p
    expect(f.submits.length).toBe(1)
    expect(f.submits[0].context).toBeUndefined()
  })
}

test('a prompt submitted while nothing is open passes straight through', async ($, on) => {
  const f = relayWorld(on)
  await start($, f)
  await typed($, 'hi')
  expect(f.submits.length).toBe(1)
  expect(count(f, 'wait')).toBe(0)
})

// Window (a), coordinator: a prompt that arrives while `pdx relay begin` is
// still out waits for begin's answer (P5b-3 critic: no 8 s release), then for
// the request it opened. Mutation gate: hold only while `awaiting` → the
// prompt goes through at once.
test('a prompt that arrives while begin is still out waits for it, then for the request it opened: approved → NOTE', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const b = gated()
  const row = rowDaemon(f, { begin: async () => { await b.p; return { exitCode: 0, stdout: BEGIN_OK } } })
  await start($, f)
  await turnAndSettle($, f, 't1') // begin is out and hangs
  const p = typed($, 'q')
  await f.clock.advance(1000)
  expect(f.submits.length).toBe(0) // held while begin is out
  b.release()
  await f.clock.settle() // begin answered: awaiting; the hold waits on the request now
  expect(f.submits.length).toBe(0)
  expect(holdWaits(f)).toEqual([])
  row.decide('approved')
  await f.clock.advance(50) // the loop's wait: approved at once
  await p
  expect(f.submits[0].context).toEqual([NOTE])
})

// ---- P5b-3 critic: the hold keeps a local `$` call of its own out — a
// `/bin/sleep 5`, never a daemon call — and never loses a prompt ----

// The budget is the engine's real-time clock, not the mocked one (`claude
// plugin test` cuts a hook that awaits a plain promise at 10 s of real time,
// as `timeout`; a mocked `$.clock` advance costs a hook nothing), so this
// one test runs in real time: each local sleep takes 5 s and each of the
// loop's waits answers "open" after 6 s, both under the test hooks' own 10 s
// budget, and the prompt is held 11.5 s. Mutation gate: await only the
// request's answer (no `$` call of the hook's) → the hook is cut at 10 s and
// the prompt goes on early, without NOTE → red.
test('the hold outlives the 10 s hook budget on its local sleeps: held 11.5 s of real time, no daemon call of its own, then approved → NOTE', { timeoutMs: 30_000 }, async ($, on) => {
  const f = relayWorld(on, { usage: AT72, sleep: realSleep })
  const row = rowDaemon(f, { realOpenMs: 6_000 })
  await start($, f)
  await turn($, 't1')
  await f.clock.advance(50) // begin; the loop's first wait
  let done = false
  const p = typed($, 'q', { context: ['prior'] }).then((r: any) => { done = true; return r })
  await new Promise((r) => setTimeout(r, 11_500))
  expect(done).toBe(false) // neither dropped nor released by a timeout
  expect(f.submits.length).toBe(0)
  expect(f.sleeps.length).toBeGreaterThanOrEqual(3) // at 0, 5 and 10 s
  expect(f.sleeps.every((x) => JSON.stringify(x) === JSON.stringify(SLEEP))).toBe(true)
  expect(holdWaits(f)).toEqual([])
  row.decide('approved')
  await p
  expect(f.submits[0].context).toEqual(['prior', NOTE])
})

// P5b-3 critic (high): a hold of its own `pdx relay wait` was one more long
// poll — and lease — at the daemon per held prompt. Mutation gates: await
// only the request's answer → no sleep → red; the old own wait → a `--wait`
// call → red.
test('the hold sleeps locally (/bin/sleep 5, 10 s bound), again and again while the clock runs far past 10 s, and asks the daemon nothing; approved → NOTE', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const row = rowDaemon(f)
  await start($, f)
  await turn($, 't1')
  await f.clock.advance(50) // begin; the loop's one 9 min wait
  const p = typed($, 'q')
  await f.clock.settle()
  expect(f.sleeps).toEqual([SLEEP])
  await f.clock.advance(130_000)
  expect(f.submits.length).toBe(0)
  expect(f.sleeps.length).toBeGreaterThanOrEqual(26) // one every 5 s
  expect(f.sleeps.every((x) => JSON.stringify(x) === JSON.stringify(SLEEP))).toBe(true)
  expect(waits(f)).toEqual(['relay wait req-1']) // the loop's one call is all the daemon was asked
  row.decide('approved')
  await p
  expect(f.submits[0].context).toEqual([NOTE])
})

// The hold waits for the request at most 11 min (its 10 min deadline and
// slack), then lets the prompt go on unchanged; the loop still drives the
// request. Mutation gate: no bound → still held → red.
test('a request still open after 11 minutes lets the held prompt go on unchanged; the request stays open', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  rowDaemon(f)
  await start($, f)
  await turn($, 't1')
  await f.clock.advance(50)
  let done = false
  const p = typed($, 'q', { context: ['c'] }).then(() => { done = true })
  await f.clock.settle()
  await f.clock.advance(659_999)
  expect(done).toBe(false)
  await f.clock.advance(1)
  expect(done).toBe(true)
  await p
  expect(f.submits[0].context).toEqual(['c'])
  expect(f.statuses).toEqual(['接力等待核准中']) // still awaiting
  expect(holdWaits(f)).toEqual([])
})

// Fail-open: a local sleep that cannot run (the call rejects, at once) or
// that fails (a non-zero exit; here after its 5 s, since one that failed at
// once would otherwise come back again and again) lets the prompt go on
// unchanged; the request stays open (the timer's loop still drives it).
// Mutation gates: keep holding on a sleep that rejects → red; read a non-zero
// exit as a full sleep → still held after it → red.
for (const how of ['awaiting', 'beginning'] as const) {
  for (const kind of ['rejects', 'exits 1'] as const) {
    test(`a /bin/sleep that ${kind} while ${how} lets the held prompt go on unchanged; the request is not touched`, async ($, on) => {
      const f = relayWorld(on, { usage: AT72 })
      f.sleep = kind === 'rejects' ? () => { throw new Error('spawn /bin/sleep ENOENT') } : () => f.clock.sleep(5_000).then(() => ({ exitCode: 1 }))
      const b = gated()
      if (how === 'beginning') rowDaemon(f, { begin: async () => { await b.p; return { exitCode: 0, stdout: BEGIN_OK } } })
      else rowDaemon(f)
      await start($, f)
      await turn($, 't1')
      await f.clock.advance(50)
      let done = false
      const p = typed($, 'q', { context: ['c'] }).then(() => { done = true })
      await f.clock.settle()
      if (kind === 'exits 1') {
        expect(done).toBe(false) // the sleep is still running
        await f.clock.advance(5_000)
      }
      expect(done).toBe(true)
      await p
      expect(f.submits[0].context).toEqual(['c'])
      expect(f.sleeps).toEqual([SLEEP])
      b.release()
      await f.clock.advance(50)
      expect(f.statuses).toEqual(['接力等待核准中']) // the request opened / stays open
      expect(await writeAllowed($, '/data/relay/op-1.md')).toBe(true)
    })
  }
}

// P5b-3 critic (medium): a prompt that arrives while begin is out waits for
// begin's own answer — begin is bounded at 35 s itself — on the same local
// sleeps, no longer released after 8 s, then for the request it opened.
// Mutation gate: release after 8 s → red.
test('a prompt held while begin is out stays held the whole 20 s begin takes, then follows the request it opened: approved → NOTE', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const row = rowDaemon(f, { begin: async () => { await f.clock.sleep(20_000); return { exitCode: 0, stdout: BEGIN_OK } } })
  await start($, f)
  await turnAndSettle($, f, 't1') // begin is out for 20 s
  let done = false
  const p = typed($, 'q', { context: ['c'] }).then(() => { done = true })
  await f.clock.advance(8_000)
  expect(done).toBe(false) // no 8 s release
  await f.clock.advance(11_999)
  expect(done).toBe(false)
  expect(f.statuses).toEqual([])
  await f.clock.advance(1) // begin answers: awaiting
  expect(f.statuses).toEqual(['接力等待核准中'])
  expect(done).toBe(false)
  expect(f.sleeps.length).toBeGreaterThanOrEqual(4)
  expect(holdWaits(f)).toEqual([])
  row.decide('approved')
  await f.clock.advance(50) // the loop's wait: approved at once
  await p
  expect(f.submits[0].context).toEqual(['c', NOTE])
})

// Begin bounds itself at 35 s; a hold waits for it at most 40 s. Mutation
// gate: no bound → still held → red.
test('a begin that has not answered in 40 s lets the held prompt go on unchanged; the request opens as usual when begin answers', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  rowDaemon(f, { begin: async () => { await f.clock.sleep(60_000); return { exitCode: 0, stdout: BEGIN_OK } } })
  await start($, f)
  await turnAndSettle($, f, 't1')
  let done = false
  const p = typed($, 'q').then(() => { done = true })
  await f.clock.advance(39_999)
  expect(done).toBe(false)
  await f.clock.advance(1)
  expect(done).toBe(true)
  await p
  expect(f.submits[0].context).toBeUndefined()
  await f.clock.advance(20_000) // begin answers: the request opens as usual
  expect(f.statuses).toEqual(['接力等待核准中'])
})

// P5b-3 critic: every held prompt keeps a child process of its own going, so
// at most 16 are held at once; the 17th starts no sleep and goes on unchanged
// at once, logged. A hold gives its place back when it ends, whatever ended
// it. Mutation gate: no limit → a 17th sleep, q17 held → red.
// U7 (spec §8.7 (b)): no new turn starts while a request is open, however
// many prompts arrive — the critic rejected a cap that let the 17th through.
// Each held prompt keeps one local sleep going, never a daemon call.
// Mutation gate: a cap of 16 that runs the 17th → q17 is submitted early → red.
test('17 prompts at once: all 17 are held on their own sleeps; none runs before the answer; every place is given back', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const row = rowDaemon(f)
  await start($, f)
  await turn($, 't1')
  await f.clock.advance(50)
  const held = Array.from({ length: 17 }, (_, i) => typed($, 'q' + (i + 1)))
  await f.clock.settle()
  expect(f.sleeps.length).toBe(17)
  expect(f.submits).toEqual([])
  await f.clock.advance(5_000)
  expect(f.sleeps.length).toBe(34) // all 17 go round again
  expect(f.submits).toEqual([])
  expect(f.argvs.filter((a) => a.includes('wait')).length).toBeLessThanOrEqual(2) // the timer's loop alone asks the daemon
  row.decide('denied')
  await Promise.all(held)
  expect(f.submits.length).toBe(17)
  expect(f.submits.every((x) => x.context === undefined)).toBe(true)
  // every hold gave its place back: the next request holds a prompt again
  const row2 = rowDaemon(f)
  f.usage = AT82
  await turnAndSettle($, f, 't2')
  await f.clock.advance(50)
  const n = f.sleeps.length
  let done = false
  const p = typed($, 'again').then(() => { done = true })
  await f.clock.settle()
  expect(done).toBe(false)
  expect(f.sleeps.length).toBe(n + 1)
  row2.decide('approved')
  await p
  expect(f.submits.at(-1).context).toEqual([NOTE])
})

// The hold's `.catch` answers next(e): a hold that fails runs the prompt,
// never drops it. A clock that refuses `$.clock.now` makes it throw.
// Mutation gate: the catch answers `{ drop }` → the prompt is lost → red.
test('a hold that throws still runs the prompt: its .catch answers next(e), never a drop', async ($, on) => {
  const f = relayWorld(on, { usage: AT72, refuseNow: true })
  const b = gated()
  rowDaemon(f, { begin: async () => { await b.p; return { exitCode: 0, stdout: BEGIN_OK } } })
  await start($, f)
  await turnAndSettle($, f, 't1') // begin is out; the hold asks a refusing clock the time
  const r = await typed($, 'q', { context: ['c'] })
  expect(r.drop).toBeUndefined()
  expect(f.submits.length).toBe(1)
  expect(f.submits[0].context).toEqual(['c'])
  await f.clock.settle()
  expect(f.logs.some((l) => l.includes('prompt hold failed (throw)'))).toBe(true) // the catch answered
})

test('a prompt held while begin is out goes on unchanged when begin opens nothing (refused 13)', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const b = gated()
  f.pdx = async (argv) => {
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO() }
    if (argv[1] === 'begin') { await b.p; return { exitCode: 13, stderr: 'pdx relay: self_relay_paused' } }
    return { exitCode: 0, stdout: '{}' }
  }
  await start($, f)
  await turnAndSettle($, f, 't1')
  const p = typed($, 'q')
  await f.clock.settle()
  expect(f.submits.length).toBe(0)
  b.release()
  await f.clock.settle()
  await p
  expect(f.submits.length).toBe(1)
  expect(f.submits[0].context).toBeUndefined()
  expect(count(f, 'wait')).toBe(0)
})

// Window (b), coordinator: begin has answered but the wait loop's timer has
// not fired. The hold only sleeps (P5b-3 critic): it asks the daemon nothing,
// starts no loop and moves no state — the loop the timer starts is the one
// that answers the request (Esc would end a loop started in the prompt's
// dispatch), and the held prompt goes on once it has. Mutation gates: start
// the loop from the hold → a loop wait goes out before the timer; let the
// hold ask the daemon itself → the prompt goes on before the loop ran.
test('a prompt that arrives after begin answered but before the wait loop started waits; only the loop the timer starts answers it and moves the state', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const row = rowDaemon(f)
  await start($, f)
  await turnAndSettle($, f, 't1') // begin answered: awaiting, the loop's timer is due in 50 ms
  expect(f.statuses).toEqual(['接力等待核准中'])
  let done = false
  const p = typed($, 'q', { context: ['c'] }).then(() => { done = true })
  await f.clock.settle()
  expect(loopWaits(f)).toEqual([])
  expect(holdWaits(f)).toEqual([])
  expect(f.sleeps).toEqual([SLEEP])
  row.decide('approved')
  await f.clock.settle()
  expect(done).toBe(false) // the daemon has approved, but no loop has asked it yet
  expect(f.statuses).toEqual(['接力等待核准中'])
  await f.clock.advance(50) // the loop: approved at once; the state moves, then the held prompt goes on
  expect(loopWaits(f)).toEqual(['relay wait req-1'])
  await p
  expect(f.submits[0].context).toEqual(['c', NOTE])
  expect(f.statuses).toEqual(['接力等待核准中', undefined])
  await f.clock.advance(50) // the write prompt
  expect(f.submits.length).toBe(2)
  expect(f.submits[1].text.startsWith('[pdx-relay op=op-1 n=')).toBe(true)
})

// A request dropped between begin's answer and its loop's timer never gets a
// loop: the timer settles its answer, so a prompt held in that gap goes on.
// Mutation gate: drop that branch of the timer → the prompt stays held.
test('a compaction before the wait loop started releases a held prompt; no loop ever starts for that request', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  rowDaemon(f, { report: () => ({ exitCode: 0, stdout: '{}' }) }) // the row stays open: only the mod lets go
  await start($, f)
  await turnAndSettle($, f, 't1') // awaiting; the loop's timer is due in 50 ms
  const p = typed($, 'q')
  await f.clock.settle()
  expect(await compact($, 'auto')).toEqual({ messages: MSGS })
  await f.clock.advance(50)
  expect(f.submits.length).toBe(1)
  await p
  expect(f.submits[0].context).toBeUndefined()
  expect(loopWaits(f)).toEqual([])
  expect(reports(f)).toEqual(['relay report op-1 cancelled --error compacted'])
})

// Mutation gates: drop the cancelled{compacted} report → no report; skip the
// return to idle → the handoff path is still allowed and a new prompt is
// held; skip compaction for an open request → red.
test('auto-compact while a request is open: compaction runs, the request is reported cancelled{compacted} and a held prompt is released unchanged; the next ask needs ≥ threshold again', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  rowDaemon(f)
  await start($, f)
  await turn($, 't1')
  await f.clock.advance(50) // begin; the wait loop (open until the daemon closes the row)
  const p = typed($, 'q')
  await f.clock.settle()
  expect(f.submits.length).toBe(0)
  expect(await compact($, 'auto')).toEqual({ messages: MSGS })
  expect(await writeAllowed($, '/data/relay/op-1.md')).toBe(false) // idle at once
  await typed($, 'after') // nothing open now: not held
  expect(f.submits.map((s) => s.text)).toEqual(['q', 'after']) // q went on at the compaction (P5b-3 review)
  await f.clock.settle() // the report goes out from a timer; the daemon closes the row; wait exits 12
  expect(reports(f)).toEqual(['relay report op-1 cancelled --error compacted'])
  expect(f.submits.length).toBe(2)
  await p
  expect(f.submits[0].context).toBeUndefined()
  expect(f.statuses.at(-1)).toBeUndefined()
  f.usage = { tokens: 142000, window: 200000, percent: 71 }
  await turnAndSettle($, f, 't2')
  expect(count(f, 'begin')).toBe(2) // 71 ≥ 70 is enough after a compaction
})

test('manual /compact while a request is open cancels it like an auto one', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  rowDaemon(f)
  await start($, f)
  await turn($, 't1')
  await f.clock.advance(50)
  expect(await compact($, 'manual')).toEqual({ messages: MSGS })
  expect(f.statuses).toEqual(['接力等待核准中', undefined]) // cleared at once, as the user's /clear does
  await f.clock.settle()
  expect(reports(f)).toEqual(['relay report op-1 cancelled --error compacted'])
})

// An approval that lands after the compaction cancelled the request is not
// acted on: nothing relays, so the held prompt gets no NOTE.
test('an approval that races a compaction is not acted on: the held prompt goes on without NOTE and no write prompt follows', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const row = rowDaemon(f)
  await start($, f)
  await turn($, 't1')
  await f.clock.advance(50)
  const p = typed($, 'q')
  await compact($, 'auto')
  row.decide('approved') // the daemon approved before the cancelled report reached it
  await f.clock.advance(50)
  await p
  await f.clock.advance(50)
  expect(f.submits.length).toBe(1)
  expect(f.submits[0].context).toBeUndefined()
  expect(reports(f)).toEqual(['relay report op-1 cancelled --error compacted'])
})

// A begin still out at a compaction would open a request for the
// conversation as it was before it shrank: the generation moves on, so the
// op is cancelled{abandoned} when begin answers, and a prompt held on it
// goes on unchanged.
test('a compaction while begin is still out: the late op is cancelled{abandoned}; a prompt held on it is released unchanged', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const b = gated()
  f.pdx = async (argv) => {
    if (argv[1] === 'hello') return { exitCode: 0, stdout: HELLO() }
    if (argv[1] === 'begin') { await b.p; return { exitCode: 0, stdout: BEGIN_OK } }
    if (argv[1] === 'wait') return new Promise<never>(() => {})
    return { exitCode: 0, stdout: '{}' }
  }
  await start($, f)
  await turnAndSettle($, f, 't1')
  let done = false
  const p = typed($, 'q').then(() => { done = true })
  await f.clock.settle()
  expect(done).toBe(false)
  expect(await compact($, 'auto')).toEqual({ messages: MSGS })
  await f.clock.settle()
  expect(done).toBe(true) // let go with the request it waited for (P5b-3 review), not when begin answers
  expect(f.submits[0].context).toBeUndefined()
  b.release()
  await f.clock.advance(50)
  await p
  expect(reports(f)).toEqual(['relay report op-1 cancelled --error abandoned'])
  expect(waits(f)).toEqual([])
})

// ---- P5b-3 review item 2 (attacker high): a request the mod lets go of
// releases its held prompts at once ----
// The mod lets a request go locally (a compaction, the user's /clear, a
// failed step): the held prompts go on at once, unchanged, before any report
// goes out — that report may never land (a daemon that is down: 20) and the
// row then stays open, so no wait would ever answer. Mutation gate: toIdle
// leaves the request's answer unsettled → the prompt stays held → red.
for (const how of ['manual /compact', 'auto-compact', 'the user’s /clear'] as const) {
  test(`${how} while a prompt is held releases it at once, unchanged, even when the cancelled report fails (20) and the row stays open`, async ($, on) => {
    const f = relayWorld(on, { usage: AT72 })
    rowDaemon(f, { report: () => ({ exitCode: 20, stderr: 'daemon unavailable' }) })
    await start($, f)
    await turn($, 't1')
    await f.clock.advance(50) // begin; the loop's wait (open)
    let done = false
    const p = typed($, 'q', { context: ['c'] }).then(() => { done = true })
    await f.clock.settle()
    expect(done).toBe(false)
    if (how === 'the user’s /clear') {
      f.sessionId = 'sid-2'
      await $.classic.SessionStart({ source: 'clear' })
    } else {
      expect(await compact($, how === 'auto-compact' ? 'auto' : 'manual')).toEqual({ messages: MSGS })
    }
    await f.clock.settle()
    expect(done).toBe(true)
    await p
    expect(f.submits.length).toBe(1)
    expect(f.submits[0].context).toEqual(['c']) // no NOTE
    expect(reports(f)).toEqual([how === 'the user’s /clear' ? 'relay report op-1 cancelled --error abandoned' : 'relay report op-1 cancelled --error compacted'])
    expect(loopWaits(f)).toEqual(['relay wait req-1']) // still out: the row never closed
  })
}

// A step deferred to a timer that fails (here the write prompt, refused)
// lets the request go the same way; a prompt the person typed meanwhile is
// held on nothing. And a wait loop that answers late for a request the mod
// let go is ignored (settle: s.pending === p).
test('a late approval for a request the mod already let go is ignored: no NOTE, no write prompt', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  const row = rowDaemon(f, { report: () => ({ exitCode: 20, stderr: 'daemon unavailable' }) })
  await start($, f)
  await turn($, 't1')
  await f.clock.advance(50)
  const p = typed($, 'q')
  await f.clock.settle()
  await compact($, 'auto')
  await f.clock.settle()
  row.decide('approved') // the report never landed; the daemon approved anyway
  await p
  await f.clock.advance(100)
  expect(f.submits.length).toBe(1)
  expect(f.submits[0].context).toBeUndefined()
  expect(f.statuses).toEqual(['接力等待核准中', undefined])
  expect(await writeAllowed($, '/data/relay/op-1.md')).toBe(false)
})

// Mutation gate: skip on every trigger (drop `e.trigger === 'auto'`) → the manual test is red.
test('auto-compact with an approved relay not yet written is skipped', async ($, on) => {
  const { f } = await approvedRelay($, on)
  expect(await compact($, 'auto')).toEqual({ skip: '接力已核准，略過壓縮，改為寫接力檔' })
  await f.clock.settle()
  expect(reports(f)).toEqual(['relay report op-1 writing'])
})

test('manual /compact with an approved relay not yet written runs (the person asked) and reports nothing', async ($, on) => {
  const { f } = await approvedRelay($, on)
  expect(await compact($, 'manual')).toEqual({ messages: MSGS })
  await f.clock.settle()
  expect(reports(f)).toEqual(['relay report op-1 writing'])
})

test('auto-compact passes through for a member', async ($, on) => {
  const f = relayWorld(on, { pdx: pdxWith([], 'member'), usage: { tokens: 180000, window: 200000, percent: 90 } })
  await start($, f)
  await turnAndSettle($, f, 't1')
  expect(await compact($, 'auto')).toEqual({ messages: MSGS })
  await f.clock.settle()
  expect(reports(f)).toEqual([])
})

test('auto-compact passes through for an idle solo session, and so does a precompute', async ($, on) => {
  const f = relayWorld(on)
  await start($, f)
  expect(await compact($, 'auto')).toEqual({ messages: MSGS })
  expect(await compact($, 'precompute')).toEqual({ messages: MSGS })
  await f.clock.settle()
  expect(reports(f)).toEqual([])
})

const PRES = { isFullscreen: false, columns: 100 }
const relayCmd = ($: any, args: string) => $.command.run({ command: 'relay', args, origin: { kind: 'composer' }, presentation: PRES })

// Mutation gate: a wrong member text → red.
test('/relay status|off|on call pdx relay self; on resets the +10 guard; a member is refused', async ($, on) => {
  const f = relayWorld(on, { usage: AT72 })
  let selfBody = { self_relay: 'on', host_switch: true, member: false }
  f.pdx = (argv) => {
    if (argv[1] === 'self') return { exitCode: 0, stdout: JSON.stringify(selfBody) }
    if (argv[1] === 'begin') return { exitCode: 13, stderr: 'pdx relay: self_relay_paused' }
    return { exitCode: 0, stdout: HELLO() }
  }
  await start($, f)
  expect(f.registered.map((r) => [r.name, r.argumentHint])).toEqual([['relay', 'off|on|status']])
  expect((await relayCmd($, 'status')).text).toBe('自我接力：開啟（主機開關 開；門檻 70%）')
  expect(f.argvs.map(sub)).toContain('relay self status --session sid-old')
  selfBody = { self_relay: 'paused', host_switch: true, member: false }
  expect((await relayCmd($, 'off')).text).toBe('自我接力：本 session 暫停（主機開關 開；門檻 70%）')
  await turnAndSettle($, f, 't1') // asks, refused 13 → lastAskPct = 72
  expect(count(f, 'begin')).toBe(1)
  selfBody = { self_relay: 'on', host_switch: true, member: false }
  await relayCmd($, 'on')
  await turnAndSettle($, f, 't2') // same 72 %, but /relay on cleared the guard
  expect(count(f, 'begin')).toBe(2)
  selfBody = { self_relay: 'off', host_switch: true, member: true }
  expect((await relayCmd($, 'status')).text).toBe('member 的接力由 lead 安排') // status answers 200 with member
  expect((await relayCmd($, 'maybe')).text).toBe('用法：/relay off|on|status')
})

// P5b-3 review item 3 (R1 P2): the daemon refuses `self on|off` for a member
// with 409 member_relay_is_leads, which the CLI prints as exit 13 with the
// code as stderr's last token (relayReportErr); `status` answers 200 with
// member. Any other 13 keeps the general text, stderr's detail included.
// Mutation gate: drop the member branch → the generic failure text → red.
test('/relay on|off in a member: exit 13 member_relay_is_leads answers member 的接力由 lead 安排; another 13 shows the detail', async ($, on) => {
  const f = relayWorld(on)
  let refusal = 'pdx relay: member 的接力由 lead 安排 member_relay_is_leads'
  f.pdx = (argv) => (argv[1] === 'self' ? { exitCode: 13, stderr: refusal + '\n' } : { exitCode: 0, stdout: HELLO() })
  await start($, f)
  expect((await relayCmd($, 'on')).text).toBe('member 的接力由 lead 安排')
  expect((await relayCmd($, 'off')).text).toBe('member 的接力由 lead 安排')
  refusal = 'pdx relay: self relay is off on this host (host config relay) self_relay_off'
  expect((await relayCmd($, 'on')).text).toBe('pdx relay self on 失敗：pdx relay: self relay is off on this host (host config relay) self_relay_off')
})

// Coordinator: /relay awaits `pdx relay self` in its hook (the person waits
// for the answer) through pdx() — so it carries --config — bounded at 8 s;
// a timeout, 20 or 21 reads as an unreachable daemon. Mutation gates: drop
// --config or the 8 s bound → red.
test('/relay runs the installed pdx with --config and an 8 s bound; a timeout, 20 or 21 reads as daemon unreachable', async ($, on) => {
  const f = relayWorld(on, { pdxJSON: PDX_JSON })
  let answer: () => any = () => ({ exitCode: 0, stdout: JSON.stringify({ self_relay: 'off', host_switch: false, member: false }) })
  f.pdx = (argv) => (argv[1] === 'self' ? answer() : { exitCode: 0, stdout: HELLO() })
  await start($, f)
  expect((await relayCmd($, 'status')).text).toBe('自我接力：關閉（主機開關 關；門檻 70%）')
  const i = f.argvs.findIndex((a) => a[2] === 'self')
  expect(f.argvs[i]).toEqual(['/opt/pdx/bin/pdx', 'relay', 'self', 'status', '--session', 'sid-old', '--config', '/tmp/pdx b/config.toml'])
  expect(f.timeouts[i]).toBe(8000)
  for (const a of [() => ({ exitCode: 20 }), () => ({ exitCode: 21 }), () => Promise.reject(new Error('timed out'))]) {
    answer = a
    expect((await relayCmd($, 'off')).text).toBe('Purdex daemon 連不上，無法變更自我接力')
  }
})

// ---------- P9a-2: the prompts are read at use (U21, spec §8.8) ----------
// Each write / fix / seed prompt is the mod's fixed head, the body `pdx relay
// prompts` answers (or the built-in one) and the mod's fixed tail.

// The pre-P9a prompts for OP, written out literally (register.js before P9a):
// the default bodies must compose to exactly these bytes. `who` is the
// pdx msg whoami answer, `n` the nonce of that one prompt.
const PRE_P9A_WRITE = (n: string, who = 'mlab/purdex-x [abc123]') => [
  '[pdx-relay op=op-1 n=' + n + '] 這個 session 的 context 已達接力門檻，使用者已核准接力（之後會 /clear）。',
  '請先停下手邊工作，用你完整的工具撰寫接力檔：/data/relay/op-1.md',
  '',
  '要求：',
  '- 自己跑 `git status`、`git diff --stat`、`git log --oneline -10` 取得檔案狀態，不要憑記憶寫。',
  '- 接力檔必須自成一體：讀它的是一個完全沒有這段對話記憶的新對話。',
  '- 寫完後只回一行「HANDOFF-WRITTEN」，不要繼續原本的工作。',
  '',
  '格式（每一段都要有，沒有內容就寫「無」）：',
  '# HANDOFF',
  '## 1. 目標與完成定義（使用者要的是什麼、怎樣算完成、範圍外）',
  '## 2. 進度（已完成且驗證 / 進行中停在哪 / 下一步第一個動作具體到指令）',
  '## 3. 檔案異動（git status 與 diff --stat 的結果，加上每個檔案的用途）',
  '## 4. 決策紀錄（選了什麼、為什麼、否決了什麼）',
  '## 5. 死路（試過失敗、不要再試的）',
  '## 6. 環境與指令（測試 / 執行方式）',
  '## 7. 未決問題與需要使用者決定的事',
  '## 8. 協作關係（下面的 pdx 身分；我的 lead 與我管理的 members，沒有就寫無）',
  '',
  '機器提供的事實（請照抄進對應段落）：',
  '- 舊 session id：sid-old',
  '- 舊 ref：_abc123',
  '- 接力時 context：144000 tokens / 200000 (72%)',
  '- pdx 身分：' + who,
].join('\n')
const PRE_P9A_SEED = (n: string) => [
  '↪ 接手自 _abc123',
  '[pdx-relay seed op=op-1 n=' + n + '] 你是接手的新對話：前一段對話 context 已滿並已清空。',
  '請先讀接力檔 /data/relay/op-1.md，然後：',
  '1. 用三行複述：目標、下一步第一個動作、目前有哪些檔案異動。',
  '2. 跑 `git status` 確認與接力檔一致，不一致就指出來。',
  '3. 接著從「下一步」繼續原本的工作。',
  '回覆的第一行請寫「↪ 接手自 _abc123」。',
].join('\n')

test('with the defaults, the write and seed prompts equal the pre-P9a text byte for byte', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  expect(f.submits[0].text).toBe(PRE_P9A_WRITE(nonceOf(f.submits[0].text)))
  f.files['/data/relay/op-1.md'] = GOOD_FILE
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  f.sessionId = 'sid-new'
  await $.classic.SessionStart({ source: 'clear' })
  await clock.advance(50)
  expect(f.submits[1].text).toBe(PRE_P9A_SEED(nonceOf(f.submits[1].text)))
})

// P9a deviation 3: one composition rule (head + body + '\n' + tail) puts the
// fixed 缺少段落 line on a line of its own, after the body's 不完整。.
test('the fix prompt is head, body, then 缺少段落 and the reply rule on the next line', async ($, on) => {
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = '# HANDOFF\n## 1. a\n## 4. d\n' + 'x'.repeat(300)
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  const text = f.submits[1].text
  expect(text).toBe('[pdx-relay op=op-1 n=' + nonceOf(text) + '] 接力檔 /data/relay/op-1.md 不完整。\n缺少段落：## 2.、## 3.、## 5.、## 6.、## 7.、## 8.。請補齊後只回「HANDOFF-WRITTEN」。')
  f.files['/data/relay/op-1.md'] = ''
  await $.turn.start({ text, turnId: 'tf' })
  await turn($, 'tf')
  await clock.advance(50)
  expect(f.submits[2].text).toBe('[pdx-relay op=op-1 n=' + nonceOf(f.submits[2].text) + '] 接力檔 /data/relay/op-1.md 不完整。\n缺少段落：## 1.、## 2.、## 3.、## 4.、## 5.、## 6.、## 7.、## 8.。請補齊後只回「HANDOFF-WRITTEN」。')
})

// A guard between the check (REQUIRED, register.js) and the generated
// prompts.js: a handoff holding only the heading lines the write tail asks
// for, each with some text, passes the check — no fix round. Mutation gate:
// a REQUIRED heading the tail does not carry → a fix prompt → red.
test('every REQUIRED heading and # HANDOFF are in FIXED.write.tail', async ($, on) => {
  const headings = FIXED.write.tail.split('\n').filter((l: string) => l.startsWith('#'))
  expect(headings[0]).toBe('# HANDOFF')
  expect(headings.slice(1).map((h: string) => h.slice(0, 5))).toEqual(['## 1.', '## 2.', '## 3.', '## 4.', '## 5.', '## 6.', '## 7.', '## 8.'])
  const { f, clock } = await approvedRelay($, on)
  f.files['/data/relay/op-1.md'] = headings.map((h: string) => h + '\nx').join('\n')
  await $.turn.start({ text: f.submits[0].text, turnId: 'tw' })
  await turn($, 'tw')
  await clock.advance(50)
  expect(f.submits.length).toBe(1)
  expect(reports(f)).toEqual(['relay report op-1 writing', 'relay report op-1 written'])
  expect(f.commands).toEqual(['clear'])
})
