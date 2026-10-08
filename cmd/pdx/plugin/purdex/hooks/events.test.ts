// hooks/events.test.ts — the mod event reporter (interface U1 spec §6.2–§6.5), run by
// `claude plugin test cmd/pdx/plugin/purdex`. The test's `on` hooks stand beneath the whole
// mod (register.js, which registers ask.js and events.js) as the engine: `http.fetch` is the
// daemon's Unix socket, `fs.read` serves pdx.json and VERSION, `session.*` / `agent.list` are
// the session, `process.run` is `pdx` (the relay's hello, ask.js's begin / wait).
//
// Every POST goes out from a $.clock.after timer, so each step settles or advances the mocked
// clock before it looks at what was posted.
import { test, expect, mock } from 'claude-code/testing'

const SID1 = 'aaaaaaaa-1111-4111-8111-111111111111'
const SID2 = 'bbbbbbbb-2222-4222-8222-222222222222'
const SOCK = '/tmp/pdxm-test/mod.sock'
const PDX_JSON = JSON.stringify({ pdx: '/opt/pdx/bin/pdx', data_dir: '/tmp/pdx', config: '/tmp/pdx/config.toml', mod_socket: SOCK })
const PDX_JSON_OLD = JSON.stringify({ pdx: '/opt/pdx/bin/pdx', data_dir: '/tmp/pdx', config: '/tmp/pdx/config.toml' }) // before U1-1a
const VERSION = '1.0.0-alpha.600\n'
const HELLO = JSON.stringify({ ok: true, role: 'none', self_relay: 'on', threshold: 70, min_growth: 20000 })
const BASH_OK = { ref: 1, result: { stdout: 'ok', stderr: '', interrupted: false }, text: 'ok' }

type Answer = { status: number; text?: string } | { deny: string }
type Post = { url: string; init: any; body: any; at: number }
type W = {
  posts: Post[]
  sid: string
  agents: any[] | 'fail'
  pdxJSON: string | null
  decision: string // what tool.check answers beneath the mod
  daemon: (body: any, n: number) => Answer | Promise<Answer>
  bash: (e: any, $: any) => any // the Bash tool beneath the mod
  compact: (e: any) => any // the compaction beneath the mod
  pdx: (argv: string[]) => { exitCode: number; stdout?: string; stderr?: string } | Promise<{ exitCode: number; stdout?: string; stderr?: string }>
  clock: any
  logs: string[]
}

// The daemon's answer to a batch it applied whole: the highest seq it holds.
const ackAll = (body: any): Answer => ({ status: 200, text: JSON.stringify({ ack: body.events[body.events.length - 1].seq }) })
const never = () => new Promise<never>(() => {})

function evWorld(on: any, opts: Partial<W> = {}): W {
  const w: W = {
    posts: [], sid: SID1, agents: [], pdxJSON: PDX_JSON, decision: 'allow', logs: [],
    daemon: ackAll,
    bash: () => BASH_OK,
    compact: (e: any) => ({ messages: e.messages }),
    pdx: (argv) => (argv[0] === 'relay' && argv[1] === 'hello' ? { exitCode: 0, stdout: HELLO } : { exitCode: 0, stdout: '{}' }),
    ...opts,
  } as W
  if (!w.clock) w.clock = mock.clock(on)
  on('http.fetch', async (_$: any, e: any) => {
    const body = JSON.parse(e.init.body)
    w.posts.push({ url: e.url, init: e.init, body, at: w.clock.now ? w.clock.now() : 0 })
    const a = await w.daemon(body, w.posts.length)
    if ('deny' in a) return { deny: a.deny }
    return { value: { status: a.status, ok: a.status >= 200 && a.status < 300, headers: { 'content-type': 'application/json' }, text: a.text ?? '' } }
  })
  on('session.id', async () => ({ value: w.sid }))
  on('session.version', async () => ({ value: { version: '2.1.293', base: '2.1.293' } }))
  on('session.surfaces', async () => ({ value: ['terminal'] }))
  on('session.usage', async () => ({ value: { startedAt: 0, context: { tokens: 1000, window: 200000, percent: 1 }, rateLimits: [] } }))
  on('agent.list', async () => {
    if (w.agents === 'fail') throw new Error('agent list refused')
    return { value: w.agents }
  })
  on('fs.read', async (_$: any, e: any) => {
    if (e.path.endsWith('/pdx.json')) return w.pdxJSON === null ? { deny: 'ENOENT' } : { value: w.pdxJSON }
    if (e.path.endsWith('/VERSION')) return { value: VERSION }
    return { deny: 'ENOENT' }
  })
  on('process.run', async (_$: any, e: any) => {
    const r = await w.pdx([...e.argv].slice(1))
    return { value: { exitCode: r.exitCode, stdout: r.stdout ?? '', stderr: r.stderr ?? '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('command.register', async (_$: any, e: any) => ({ value: { command: e.name } }))
  on('ui.log', async (_$: any, e: any) => { w.logs.push(e.text); return { value: undefined } })
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('turn.start', async (_$: any, e: any) => ({ turnId: e.turnId }))
  on('turn.complete', async (_$: any, e: any) => ({ text: e.answer }))
  on('classic.SessionStart', async () => ({}))
  on('classic.Stop', async () => ({}))
  on('session.end', async (_$: any, e: any) => ({ sessionId: e.sessionId }))
  on('session.measure', async (_$: any, e: any) => ({ changed: e.changed }))
  on('session.compact', async (_$: any, e: any) => w.compact(e))
  on('tool.check', async () => ({ decision: w.decision }))
  on('tool.call', { tool: 'Bash' }, async ($: any, e: any) => w.bash(e, $))
  on('agent.spawn', async () => ({ agentId: 'ag-9', model: 'claude-haiku-4-5' }))
  return w
}

const start = async ($: any, w: W, interactive = true) => {
  await $.session.start({ cwd: '/work', surface: interactive ? 'terminal' : null, isInteractive: interactive })
  await w.clock.settle()
}
const turnStart = ($: any, id: string) => $.turn.start({ text: 'hi', turnId: id })
const turnDone = ($: any, id: string, more: any = {}) => $.turn.complete({ answer: 'ok', reason: 'answer', durationMs: 12, isAborted: false, turnId: id, ...more })
const measure = ($: any, percent = 5) => $.session.measure({ context: { tokens: percent * 2000, window: 200000, percent }, rateLimits: [], changed: ['context'] })
const end = ($: any, reason: string, sid = SID1) => $.session.end({ reason, sessionId: sid, resume: { id: sid } } as any)
// Every event the daemon has seen, each seq once, in seq order.
const evs = (w: W) => {
  const seen = new Map<number, any>()
  for (const p of w.posts) for (const e of p.body.events) if (!seen.has(e.seq)) seen.set(e.seq, e)
  return [...seen.values()].sort((a, b) => a.seq - b.seq)
}
const types = (w: W) => evs(w).map((e) => e.type)
const seqs = (p: Post) => p.body.events.map((e: any) => e.seq)
const ofType = (w: W, type: string) => evs(w).filter((e) => e.type === type)

test('posts session.start then turn events in seq order to the socket from pdx.json', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  expect(w.posts).toEqual([]) // queued; the flush is a 150 ms timer, never inside a hook
  await w.clock.advance(150)
  expect(w.posts.length).toBe(1)
  const p = w.posts[0]
  expect(p.url).toBe('http://pdx/mod/v1/events')
  expect(p.init.method).toBe('POST')
  expect(p.init.socketPath).toBe(SOCK)
  expect(p.init.headers).toEqual({ 'content-type': 'application/json' })
  expect(p.body).toEqual({
    v: 1,
    stream: expect.stringMatching(/^[A-Za-z0-9_-]{22}$/),
    agent: 'cc',
    cc_version: '2.1.293',
    mod_version: '1.0.0-alpha.600',
    dropped_total: 0,
    events: [
      { seq: 1, at: expect.any(Number), sid: SID1, type: 'session.start', data: { cwd: '/work', surface: 'terminal' } },
      { seq: 2, at: expect.any(Number), sid: SID1, type: 'turn.start', data: { turn_id: 't1' } },
      { seq: 3, at: expect.any(Number), sid: SID1, type: 'turn.complete', data: { turn_id: 't1', reason: 'answer', duration_ms: 12, aborted: false } },
    ],
  })
  // the relay is untouched: its hello went out as before
  await turnStart($, 't2')
  await w.clock.advance(150)
  expect(w.posts.length).toBe(2)
  expect(w.posts[1].body.stream).toBe(p.body.stream) // one stream per mod load
  expect(seqs(w.posts[1])).toEqual([4])
})

test('a headless session reports nothing', async ($, on) => {
  const w = evWorld(on)
  await start($, w, false)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await $.tool.call({ tool: 'Bash', command: 'ls' } as any)
  await measure($)
  await w.clock.advance(30_000)
  await end($, 'other')
  expect(w.posts).toEqual([])
})

for (const [name, json] of [['no mod_socket in pdx.json', PDX_JSON_OLD], ['no pdx.json at all', null]] as const) {
  test(`${name} → no fetch at all`, async ($, on) => {
    const w = evWorld(on, { pdxJSON: json })
    await start($, w)
    await turnStart($, 't1')
    await $.tool.call({ tool: 'Bash', command: 'ls' } as any)
    await turnDone($, 't1')
    await w.clock.advance(30_000) // no heartbeat either
    await end($, 'prompt_input_exit')
    expect(w.posts).toEqual([])
  })
}

// Mutation gate: ack ignored (drop every sent event on 200) → the resend of seq 3 is missing.
test('a 200 ack drops acked events and the next batch starts after them', async ($, on) => {
  const w = evWorld(on, { daemon: (body, n) => (n === 1 ? { status: 200, text: '{"ack":2}' } : ackAll(body)) })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.advance(150) // [1,2,3], the daemon applied up to 2
  await w.clock.advance(150) // seq 3 again
  await turnStart($, 't2')
  await w.clock.advance(150)
  expect(w.posts.map(seqs)).toEqual([[1, 2, 3], [3], [4]])
  await w.clock.advance(1000)
  expect(w.posts.length).toBe(3) // nothing left: no further POST
})

test('a failed POST is retried with backoff and nothing is lost', async ($, on) => {
  const answers: Answer[] = [{ status: 500, text: 'oops' }, { status: 503, text: '{"error":"registry_full"}' }, { deny: 'ECONNREFUSED' }]
  const w = evWorld(on, { daemon: (body) => answers.shift() ?? ackAll(body) })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.advance(7150)
  expect(w.posts.map((p) => p.at)).toEqual([150, 1150, 3150, 7150]) // backoff 1 s, 2 s, 4 s
  expect(w.posts.map(seqs)).toEqual([[1, 2, 3], [1, 2, 3], [1, 2, 3], [1, 2, 3]])
  await turnStart($, 't2') // a success resets the backoff: the next batch goes 150 ms later
  await w.clock.advance(150)
  expect(w.posts.map((p) => p.at).slice(4)).toEqual([7300])
  expect(seqs(w.posts[4])).toEqual([4])
  expect(types(w)).toEqual(['session.start', 'turn.start', 'turn.complete', 'turn.start'])
})

test('the backoff doubles to a 30 s cap', async ($, on) => {
  const w = evWorld(on, { daemon: () => ({ status: 500 }) })
  await start($, w)
  await w.clock.advance(150 + 1000 + 2000 + 4000 + 8000 + 16000 + 30000 + 30000)
  const at = w.posts.map((p) => p.at)
  expect(at.map((t, i) => (i === 0 ? t : t - at[i - 1]))).toEqual([150, 1000, 2000, 4000, 8000, 16000, 30000, 30000])
})

test('a POST that never answers misses its 5 s deadline and is retried', async ($, on) => {
  let late!: (a: Answer) => void
  const w = evWorld(on, { daemon: (body, n) => (n === 1 ? new Promise<Answer>((r) => { late = r }) : ackAll(body)) })
  await start($, w)
  await turnStart($, 't1')
  await w.clock.advance(150)
  expect(w.posts.length).toBe(1)
  await w.clock.advance(4999)
  expect(w.posts.length).toBe(1) // still waiting for the first answer: one request in flight at a time
  await w.clock.advance(1) // the deadline: a failure, backoff 1 s
  await w.clock.advance(1000)
  expect(w.posts.map((p) => p.at)).toEqual([150, 6150])
  expect(w.posts.map(seqs)).toEqual([[1, 2], [1, 2]])
  late({ status: 400, text: '{"error":"bad_event"}' }) // the late answer of the first is ignored
  await w.clock.settle()
  await turnDone($, 't1')
  await w.clock.advance(150)
  expect(seqs(w.posts[2])).toEqual([3])
  expect(w.posts[2].body.dropped_total).toBe(0)
})

test('a 400 drops the batch and adds its size to dropped_total', async ($, on) => {
  const w = evWorld(on, { daemon: (body, n) => (n === 1 ? { status: 400, text: '{"error":"bad_event"}' } : ackAll(body)) })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.advance(150) // [1,2,3] rejected: dropped, never sent again
  await w.clock.advance(5000)
  expect(w.posts.length).toBe(1)
  await turnStart($, 't2')
  await w.clock.advance(150)
  expect(w.posts.map(seqs)).toEqual([[1, 2, 3], [4]])
  expect(w.posts.map((p) => p.body.dropped_total)).toEqual([0, 3])
})

// Mutation gate: `droppedTotal = 0` after a 200 → the batch after the in-flight overflow says 5, not 8.
test('queue overflow drops the oldest and dropped_total is carried, cumulative, in every later batch', { timeoutMs: 60_000 }, async ($, on) => {
  let fail = true
  let hold: Promise<void> | null = null
  let release!: () => void
  const w = evWorld(on, {
    daemon: async (body) => {
      if (fail) return { status: 500 }
      if (hold) { const h = hold; hold = null; await h }
      return ackAll(body)
    },
  })
  await start($, w) // seq 1
  await w.clock.advance(150) // fails: backoff 1 s
  for (let i = 0; i < 1002; i++) await measure($, i % 100) // seq 2..1003: 1003 queued, cap 1000
  fail = false
  await w.clock.advance(1000) // the retry: the oldest three are gone
  expect(seqs(w.posts[1])[0]).toBe(4)
  expect(w.posts[1].body.events.length).toBe(200)
  expect(w.posts[1].body.dropped_total).toBe(3)
  hold = new Promise<void>((r) => { release = r })
  await w.clock.advance(150) // [204..403] goes out and stays in flight
  expect(seqs(w.posts[2])[0]).toBe(204)
  for (let i = 0; i < 205; i++) await measure($, i % 100) // seq 1004..1208: 1005 queued, five more dropped
  release()
  await w.clock.settle() // the in-flight batch is acked to 403
  await w.clock.advance(150)
  expect(seqs(w.posts[3])[0]).toBe(404)
  expect(w.posts.map((p) => p.body.dropped_total)).toEqual([0, 3, 3, 8])
  await w.clock.advance(150 * 6)
  expect(w.posts.slice(3).every((p) => p.body.dropped_total === 8)).toBe(true)
  // Every event reached the daemon but 2 and 3, dropped before they were ever sent (1 went out
  // in the failed first POST, 204–208 in the in-flight batch: dropped_total is an upper bound).
  const seen = new Set(evs(w).map((e) => e.seq))
  expect(Array.from({ length: 1208 }, (_, i) => i + 1).filter((s) => !seen.has(s))).toEqual([2, 3])
})

// Mutation gate: heartbeat not started → no heartbeat events.
test('heartbeat every 10 s carries turn_id, asks and agents', async ($, on) => {
  const w = evWorld(on, { decision: 'ask' })
  w.agents = [{ id: 'ag-1', description: 'look around', type: 'Explore', status: 'running' }]
  await start($, w)
  await turnStart($, 't1')
  await $.tool.check({ tool: 'Bash', input: { command: 'rm -rf x' }, tool_use_id: 'tu-1' } as any)
  await w.clock.advance(10_150)
  expect(ofType(w, 'heartbeat').map((e) => e.data)).toEqual([
    { turn_id: 't1', asks: ['tu-1'], compacting: false, agents: [{ id: 'ag-1', status: 'running' }] },
  ])
  await turnDone($, 't1') // the main turn ended: no turn, no open asks
  w.agents = 'fail'
  await w.clock.advance(10_000)
  expect(ofType(w, 'heartbeat').map((e) => e.data)[1]).toEqual({ asks: [], compacting: false, agents: [] })
  await w.clock.advance(10_000)
  expect(ofType(w, 'heartbeat').length).toBe(3)
})

test('tool.check ask then tool.end clears the ask', async ($, on) => {
  let release!: () => void
  on('tool.call', { tool: 'Write' }, async () => {
    await new Promise<void>((r) => { release = r }) // the permission dialog is up
    return BASH_OK
  })
  const w = evWorld(on, { decision: 'ask' })
  await start($, w)
  await turnStart($, 't1')
  const call = $.tool.call({ tool: 'Write', file_path: '/x', content: 'x' } as any)
  await w.clock.advance(150)
  const id = ofType(w, 'tool.start')[0].data.tool_use_id
  expect(id).toMatch(/.+/)
  // The engine decides a real call inside its tool.call chain (M-U1-3), for that call's own
  // tool_use_id (a test hook cannot raise it beneath, so the test raises it here, in its place).
  await $.tool.check({ tool: 'Write', input: { file_path: '/x' }, tool_use_id: id } as any)
  await w.clock.advance(10_000)
  expect(ofType(w, 'heartbeat')[0].data.asks).toEqual([id])
  release()
  expect(await call).toEqual(BASH_OK)
  await w.clock.advance(10_000)
  expect(ofType(w, 'heartbeat')[1].data.asks).toEqual([])
  expect(types(w).filter((t) => t !== 'heartbeat')).toEqual(['session.start', 'turn.start', 'tool.start', 'tool.check', 'tool.end'])
  expect(ofType(w, 'tool.start')[0].data).toEqual({ tool: 'Write', tool_use_id: id })
  expect(ofType(w, 'tool.check')[0].data).toEqual({ tool: 'Write', tool_use_id: id, decision: 'ask' })
  expect(ofType(w, 'tool.end')[0].data).toEqual({ tool_use_id: id, ms: 10_150, error: false }) // timed on the engine's clock
})

test('a subagent turn.complete carries agent_id and keeps the main turn_id', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 'st-1', { agentId: 'ag-1', durationMs: 40 })
  await w.clock.advance(10_150)
  expect(ofType(w, 'turn.complete').map((e) => e.data)).toEqual([{ turn_id: 'st-1', reason: 'answer', agent_id: 'ag-1', duration_ms: 40, aborted: false }])
  expect(ofType(w, 'heartbeat')[0].data.turn_id).toBe('t1')
  await turnDone($, 't1', { reason: 'aborted', isAborted: true })
  await w.clock.advance(10_000)
  expect(ofType(w, 'turn.complete')[1].data).toEqual({ turn_id: 't1', reason: 'aborted', duration_ms: 12, aborted: true })
  expect(ofType(w, 'heartbeat')[1].data.turn_id).toBeUndefined()
})

// Mutation gate: sid not refreshed on clear → the events after /clear carry the old sid.
test('after /clear events carry the new sid and session.switch names the old one', async ($, on) => {
  const w = evWorld(on, { decision: 'ask' })
  await start($, w)
  await turnStart($, 't1')
  await $.tool.check({ tool: 'Bash', input: {}, tool_use_id: 'tu-1' } as any)
  await turnDone($, 't1')
  await turnStart($, 't2') // /clear while a turn is out: its state does not survive the switch
  await end($, 'clear', SID1)
  w.sid = SID2
  await $.classic.SessionStart({ source: 'clear' })
  await w.clock.advance(10_000) // the heartbeat goes on across /clear (same process, same stream)
  await turnStart($, 't3')
  await w.clock.advance(150)
  const e = evs(w)
  expect(e.map((x) => [x.type, x.sid])).toEqual([
    ['session.start', SID1], ['turn.start', SID1], ['tool.check', SID1], ['turn.complete', SID1], ['turn.start', SID1],
    ['session.end', SID1], ['session.switch', SID2], ['heartbeat', SID2], ['turn.start', SID2],
  ])
  expect(ofType(w, 'session.end')[0].data).toEqual({ reason: 'clear' })
  expect(ofType(w, 'session.switch')[0].data).toEqual({ prev_sid: SID1, source: 'clear' })
  expect(ofType(w, 'heartbeat')[0].data).toEqual({ asks: [], compacting: false, agents: [] })
  expect(new Set(w.posts.map((p) => p.body.stream)).size).toBe(1)
})

test('a startup SessionStart is not a switch; a resume is', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await $.classic.SessionStart({ source: 'startup' })
  await end($, 'resume', SID1)
  w.sid = SID2
  await $.classic.SessionStart({ source: 'resume' })
  await w.clock.advance(150)
  expect(types(w)).toEqual(['session.start', 'session.end', 'session.switch'])
  expect(ofType(w, 'session.switch')[0].data).toEqual({ prev_sid: SID1, source: 'resume' })
})

test('session.end flushes inside the hook', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await end($, 'prompt_input_exit')
  expect(w.posts.length).toBe(1) // no clock moved: the POST went out inside the hook
  expect(types(w)).toEqual(['session.start', 'turn.start', 'turn.complete', 'session.end'])
  expect(ofType(w, 'session.end')[0]).toEqual(expect.objectContaining({ sid: SID1, data: { reason: 'prompt_input_exit' } }))
  await w.clock.advance(30_000) // the heartbeat is cancelled and nothing is left to send
  expect(w.posts.length).toBe(1)
})

// Mutation gate: the final flush waits for the request in flight → no second POST inside the hook.
test('session.end flushes even while a POST is in flight', async ($, on) => {
  const w = evWorld(on, { daemon: (body, n) => (n === 1 ? never() : ackAll(body)) })
  await start($, w)
  await turnStart($, 't1')
  await w.clock.advance(150) // [1,2] in flight, never answered
  await turnDone($, 't1')
  await end($, 'other')
  expect(w.posts.length).toBe(2)
  expect(w.posts.map(seqs)).toEqual([[1, 2], [1, 2, 3, 4]]) // every queued event, the in-flight ones too
})

test('session.end flushes even during backoff', async ($, on) => {
  const w = evWorld(on, { daemon: (body, n) => (n === 1 ? { status: 500 } : ackAll(body)) })
  await start($, w)
  await w.clock.advance(150) // fails: the next try is 1 s away
  await w.clock.advance(300)
  await end($, 'logout')
  expect(w.posts.map((p) => p.at)).toEqual([150, 450])
  expect(w.posts.map(seqs)).toEqual([[1], [1, 2]])
})

test('the final flush sends the newest 500 and counts the older ones as dropped', { timeoutMs: 60_000 }, async ($, on) => {
  const w = evWorld(on, { daemon: (body, n) => (n === 1 ? { status: 500 } : ackAll(body)) })
  await start($, w)
  await w.clock.advance(150) // fails
  for (let i = 0; i < 600; i++) await measure($, i % 100) // seq 2..601
  await end($, 'other') // seq 602
  expect(w.posts.length).toBe(2)
  expect(w.posts[1].body.events.length).toBe(500)
  expect(seqs(w.posts[1])[0]).toBe(103)
  expect(w.posts[1].body.dropped_total).toBe(102)
})

// The `.catch` on every registration is replay-safe: a hook that throws after next(e) does not
// run what is beneath again (d.ts CatchHandler / Caught). The reporter times a tool with
// $.clock.now() around next(e); here the clock refuses after the tool ran, so the hook throws.
// This pins the outcome, not the `.catch` alone: dropping the `.catch` on tool.call stays green
// (measured on 2.1.293 — the engine keeps a failed hook's settled next(e) by itself); the
// `.catch` is the spec's defence in depth.
test('a reporter that throws after next never re-runs the tool', async ($, on) => {
  let ran = 0
  let nows = 0
  on('clock.now', async () => {
    nows++
    if (ran > 0) throw new Error('the clock refused to tell the time')
    return { value: 0 }
  })
  on('clock.after', () => never()) // no timer ever fires: nothing but the hook runs
  on('clock.every', () => never())
  on('clock.sleep', () => never())
  const w = evWorld(on, { clock: { settle: async () => {} }, bash: () => { ran++; return BASH_OK } })
  await start($, w)
  const r = await $.tool.call({ tool: 'Bash', command: 'ls' } as any)
  expect(nows).toBe(2) // the reporter was on and asked the clock again after the tool ran
  expect(ran).toBe(1)
  expect(r).toEqual(BASH_OK)
})

test('a fetch that throws never changes the engine\'s result', async ($, on) => {
  let ran = 0
  const w = evWorld(on, { daemon: () => ({ deny: 'ECONNREFUSED' }), bash: () => { ran++; return BASH_OK } })
  await start($, w)
  await turnStart($, 't1')
  await w.clock.advance(150) // the first POST throws
  const r = await $.tool.call({ tool: 'Bash', command: 'ls' } as any)
  expect(r).toEqual(BASH_OK)
  expect(ran).toBe(1)
  await w.clock.advance(1000)
  expect(w.posts.length).toBe(2) // retried with backoff, the tool's events with it
  expect(w.posts[1].body.events.map((e: any) => e.type)).toEqual(['session.start', 'turn.start', 'tool.start', 'tool.end'])
})

test('agent.spawn, session.measure and classic.Stop report agent.spawn, usage and background', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await $.agent.spawn({ prompt: 'p', description: 'd', subagentType: 'Explore', tool_use_id: 'tu-a', background: true } as any)
  await $.agent.spawn({ prompt: 'p', description: 'd', subagentType: 'general-purpose', tool_use_id: 'tu-w', background: false, workflow: { runId: 'run-1', agentIndex: 0 } } as any)
  await $.session.measure({ context: { tokens: 50_000, window: 200_000, percent: 25 }, rateLimits: [{ kind: 'five_hour', percentUsed: 23.5, resetsAt: '2026-10-08T12:00:00Z' }, { kind: 'seven_day', percentUsed: 7 }], cost: { usd: 1.25 }, changed: ['context', 'cost'] })
  await $.session.measure({ context: { window: 200_000 }, rateLimits: [], changed: ['rateLimits'] })
  await $.classic.Stop({ stop_hook_active: false, background_tasks: [{ id: 'b1', type: 'monitor', status: 'running', description: 'tail' }, { id: 'b2', type: 'shell', status: 'running', description: 'sleep', command: 'sleep 9' }], session_crons: [{ id: 'c1', schedule: '0 9 * * *', recurring: true }] } as any)
  await $.classic.Stop({ stop_hook_active: false } as any)
  await w.clock.advance(150)
  expect(ofType(w, 'agent.spawn').map((e) => e.data)).toEqual([
    { agent_id: 'ag-9', tool_use_id: 'tu-a', background: true, subagent_type: 'Explore' },
    { agent_id: 'ag-9', tool_use_id: 'tu-w', background: false, subagent_type: 'general-purpose', workflow_run_id: 'run-1' },
  ])
  expect(ofType(w, 'usage').map((e) => e.data)).toEqual([
    { context: { tokens: 50_000, window: 200_000, percent: 25 }, rate_limits: [{ kind: 'five_hour', percent_used: 23.5, resets_at: '2026-10-08T12:00:00Z' }, { kind: 'seven_day', percent_used: 7 }], cost_usd: 1.25, changed: ['context', 'cost'] },
    { context: { window: 200_000 }, rate_limits: [], changed: ['rateLimits'] },
  ])
  expect(ofType(w, 'background').map((e) => e.data)).toEqual([
    { tasks: [{ id: 'b1', type: 'monitor', status: 'running' }, { id: 'b2', type: 'shell', status: 'running' }], crons: 1 },
    { tasks: [], crons: 0 },
  ])
})

test('a compaction reports compact.start and compact.end, and the heartbeat says compacting meanwhile', async ($, on) => {
  let release!: () => void
  const MSGS = [{ role: 'user', text: 'hi', toolUses: [] }]
  const w = evWorld(on, { compact: (e) => (e.trigger === 'manual' ? new Promise((r) => { release = () => r({ messages: e.messages }) }) : { messages: e.messages }) })
  await start($, w)
  const c = $.session.compact({ trigger: 'manual', messages: MSGS } as any)
  await w.clock.advance(10_150)
  expect(ofType(w, 'heartbeat')[0].data.compacting).toBe(true)
  release()
  await c
  await $.session.compact({ trigger: 'auto', agentId: 'ag-1', messages: MSGS } as any) // a subagent's: reported, not the main mirror
  await w.clock.advance(10_000)
  expect(ofType(w, 'heartbeat')[1].data.compacting).toBe(false)
  expect(evs(w).filter((e) => e.type.startsWith('compact.')).map((e) => [e.type, e.data])).toEqual([
    ['compact.start', { trigger: 'manual' }], ['compact.end', { trigger: 'manual', ok: true }],
    ['compact.start', { trigger: 'auto', agent_id: 'ag-1' }], ['compact.end', { trigger: 'auto', agent_id: 'ag-1', ok: true }],
  ])
})

// ---- AskUserQuestion with the reporter on: ask.js's hook outside, the reporter's inside ----

const Q = [{ question: '紅還是藍？', header: '顏色', options: [{ label: '紅', description: 'r' }, { label: '藍', description: 'b' }], multiSelect: false }]
const NATIVE_RED = { ref: 1, result: { questions: Q, answers: { '紅還是藍？': '紅' }, annotations: {} }, text: 'Your questions have been answered', isReadOnly: true }
const DISMISSED = { ref: 2, isError: true, result: 'The user dismissed the question', text: 'The user dismissed the question' }
const askPdx = (begin: { exitCode: number; stdout?: string; stderr?: string }, wait?: string) => (argv: string[]) => {
  if (argv[0] === 'relay' && argv[1] === 'hello') return { exitCode: 0, stdout: HELLO }
  if (argv[0] === 'ask' && argv[1] === 'begin') return begin
  if (argv[0] === 'ask' && argv[1] === 'wait' && wait) return { exitCode: 0, stdout: wait }
  return { exitCode: 0, stdout: '{}' }
}
const NO_RESPONDERS = { exitCode: 13, stderr: 'pdx ask: 沒有連線中的客戶端可以回答 no_responders' }
const toolEvents = (w: W) => evs(w).filter((e) => e.type === 'tool.start' || e.type === 'tool.end')

test('AskUserQuestion, native answer: tool.start once, tool.end once, ask flow unchanged', async ($, on) => {
  let calls = 0
  on('tool.call', { tool: 'AskUserQuestion' }, () => { calls++; return NATIVE_RED })
  const w = evWorld(on, { pdx: askPdx(NO_RESPONDERS) })
  await start($, w)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q } as any)
  expect(r).toEqual(expect.objectContaining({ result: NATIVE_RED.result, isReadOnly: true, ref: 1 }))
  expect(calls).toBe(1)
  await w.clock.advance(150)
  const t = toolEvents(w)
  expect(t.map((e) => e.type)).toEqual(['tool.start', 'tool.end'])
  expect(t[0].data).toEqual({ tool: 'AskUserQuestion', tool_use_id: expect.stringMatching(/.+/) })
  expect(t[1].data).toEqual(expect.objectContaining({ tool_use_id: t[0].data.tool_use_id, error: false }))
})

test('AskUserQuestion, remote answer (ask.js returns {result} with next pending): tool.end {error:true}, result is the remote one', async ($, on) => {
  let calls = 0
  let closeNative!: (err: Error) => void
  // the native dialog: closed by the engine once the remote answer returned (it aborts what runs beneath)
  on('tool.call', { tool: 'AskUserQuestion' }, () => { calls++; return new Promise((_, reject) => { closeNative = reject }) })
  const w = evWorld(on, { pdx: askPdx({ exitCode: 0, stdout: '{"id":"r1"}' }, '{"state":"answered_remote","hook":{"answers":{"紅還是藍？":"藍"}}}') })
  await start($, w)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q } as any)
  expect(r.result).toEqual({ questions: Q, answers: { '紅還是藍？': '藍' } })
  closeNative(new Error('the dialog was closed'))
  await w.clock.advance(150)
  await w.clock.advance(10_000)
  expect(calls).toBe(1)
  const t = toolEvents(w)
  expect(t.map((e) => e.type)).toEqual(['tool.start', 'tool.end'])
  expect(t[1].data).toEqual(expect.objectContaining({ tool_use_id: t[0].data.tool_use_id, error: true }))
  expect(ofType(w, 'heartbeat')[0].data.asks).toEqual([]) // the open question is closed in the mirror
})

test('AskUserQuestion, dismissed: tool.end once, result unchanged', async ($, on) => {
  let calls = 0
  on('tool.call', { tool: 'AskUserQuestion' }, () => { calls++; return DISMISSED })
  const w = evWorld(on, { pdx: askPdx(NO_RESPONDERS) })
  await start($, w)
  const r = await $.tool.call({ tool: 'AskUserQuestion', questions: Q } as any)
  expect(r).toEqual(expect.objectContaining({ isError: true, result: DISMISSED.result }))
  expect(calls).toBe(1)
  await w.clock.advance(150)
  const t = toolEvents(w)
  expect(t.map((e) => e.type)).toEqual(['tool.start', 'tool.end'])
  expect(t[1].data.error).toBe(true)
})
