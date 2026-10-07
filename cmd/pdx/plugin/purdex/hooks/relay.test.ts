// Run with `claude plugin test cmd/pdx/plugin/purdex`. The test's `on` hooks
// stand for the engine beneath the mod: a fake pdx behind $.process.run,
// $.session.*, $.fs.read, $.ui.*, and the bottom of every event a test
// raises. The P5b-1 hello tests come first, the P5b-2 relay after them.
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

// ---------- P5b-2: the relay ----------
// Every pdx call goes out from a $.clock.after timer (the P5b-1 contract: no
// hook waits on the daemon), so each step below settles or advances the
// clock before it looks at what was called — and before it asserts that
// nothing was, or a call made from a timer would never be seen.

type Fake = {
  argvs: string[][]
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
}

const OP = { id: 'op-1', kind: 'self', host_id: 'h', session_id: 'sid-old', ref: '_abc123', state: 'awaiting_approval', handoff_path: '/data/relay/op-1.md', created_at: 1, updated_at: 1 }
const BEGIN_OK = JSON.stringify({ op: OP, request_id: 'req-1' })
const HELLO = (role = 'none', extra = {}) => JSON.stringify({ ok: true, role, self_relay: 'on', threshold: 70, min_growth: 20000, ...extra })
const APPROVAL = (state: string) => JSON.stringify({ id: 'req-1', kind: 'self_relay', state })
const GOOD_FILE = '# HANDOFF\n' + ['## 1. a', '## 2. b', '## 3. c', '## 4. d', '## 5. e', '## 6. f', '## 7. g', '## 8. h'].map((h) => h + '\n' + 'x'.repeat(40)).join('\n')
const AT72 = { tokens: 144000, window: 200000, percent: 72 }

function relayWorld(on: any, opts: Partial<Fake> = {}, env: Record<string, string> = {}): Fake {
  const f: Fake = {
    argvs: [], submits: [], commands: [], toasts: [], statuses: [], files: {}, logs: [],
    pdx: () => ({ exitCode: 0, stdout: HELLO() }),
    sessionId: 'sid-old',
    usage: { tokens: 10000, window: 200000, percent: 5 },
    ...opts,
  }
  f.clock = mock.clock(on)
  mock.env(on, env)
  on('tool.check', async () => ({ decision: 'ask', reason: 'mode' }))
  on('process.run', async (_$: any, e: any) => {
    f.argvs.push([...e.argv])
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
  expect(f.submits[1].text).toContain('不完整，缺少段落：## 2.、## 3.')
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
// 21 unsupported, 1 runtime) is re-sent at the next turn.complete and the
// relay goes on. Mutation gate: drop any of the three instead → red.
for (const code of [20, 21, 1]) {
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
