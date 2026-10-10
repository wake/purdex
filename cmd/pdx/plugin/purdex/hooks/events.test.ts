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
  agents: any[] | 'fail' | (() => Promise<any[]>) // a function: agent.list waits on what it returns
  pdxJSON: string | null
  decision: string // what tool.check answers beneath the mod
  daemon: (body: any, n: number) => Answer | Promise<Answer>
  bash: (e: any, $: any) => any // the Bash tool beneath the mod
  compact: (e: any) => any // the compaction beneath the mod
  sessionStart?: (e: any) => any // classic.SessionStart beneath the mod (the relay's /clear work runs above it)
  pdx: (argv: string[]) => { exitCode: number; stdout?: string; stderr?: string } | Promise<{ exitCode: number; stdout?: string; stderr?: string }>
  clock: any
  logs: string[]
  gets: { url: string; init: any }[] // the GETs of /mod/v1/team (TI-5b)
  team: (sid: string, n: number) => Answer | Promise<Answer> // the daemon's answer to that read
  invalidated: number // ui.invalidate calls (a redraw requested)
  wbReqs: { url: string; body: any }[] // the POSTs to the workbook routes (WB-1c), never mixed with the event batches
  wbNext: (body: any, n: number) => Answer | Promise<Answer> // the daemon's answer to `next` (default 204)
  wbResult: (body: any, n: number) => Answer | Promise<Answer> // ... to `result` (default 200 {more:false})
  model: (e: any, n: number) => any // $.model.complete beneath the mod: a ModelCompleteResult, or { deny } for a refused call
  modelCalls: any[]
  fork: (e: any, n: number) => any // $.model.fork beneath the mod (WB-2b-ii)
  forkCalls: any[]
  registered: string[] // the slash commands the mod registered
  pqReqs: { url: string; body: any }[] // the POSTs to the prompt routes (U3-0b), never mixed with the event batches
  promptNext: (body: any, n: number) => Answer | Promise<Answer> // the daemon's answer to prompt/next (default: a long poll that never answers)
  promptResult: (body: any, n: number) => Answer | Promise<Answer> // ... to prompt/result (default 200)
  submit: (e: any, n: number) => any // prompt.submit beneath the mod: { text } or { drop }, or { deny } for a refused call
  submitCalls: any[]
  abort: (e: any) => any // turn.abort beneath the mod: undefined, or { deny } when no turn is running
  abortCalls: any[]
  wbRefresh: (body: any, n: number) => Answer | Promise<Answer> // the daemon's answer to POST /workbook/refresh
}

const teamAnswer = (role: string, members = 0, label = ''): Answer => ({ status: 200, text: JSON.stringify({ role, members, team_label: label }) })

// The daemon's answer to a batch it applied whole: the highest seq it holds.
const ackAll = (body: any): Answer => ({ status: 200, text: JSON.stringify({ ack: body.events[body.events.length - 1].seq }) })
const never = () => new Promise<never>(() => {})

function evWorld(on: any, opts: Partial<W> = {}): W {
  const w: W = {
    posts: [], sid: SID1, agents: [], pdxJSON: PDX_JSON, decision: 'allow', logs: [],
    gets: [], invalidated: 0, team: () => teamAnswer('none'),
    wbReqs: [], wbNext: () => ({ status: 204 }), wbResult: () => ({ status: 200, text: '{"more":false}' }),
    model: () => ({ isAnswered: true, text: '{}', usage: { input_tokens: 1, output_tokens: 1, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } }), modelCalls: [],
    fork: () => ({ isAnswered: true, text: '{"status":"s","todos":{"done":[],"dropped":[],"add":[]}}', usage: { input_tokens: 10, output_tokens: 5, cache_read_input_tokens: 900, cache_creation_input_tokens: 0 } }), forkCalls: [], registered: [], pqReqs: [], promptNext: () => never(), promptResult: () => ({ status: 200, text: '{"ok":true}' }),
    submit: (e: any) => ({ text: e.text }), submitCalls: [], abort: () => undefined, abortCalls: [],
    wbRefresh: () => ({ status: 202, text: '{"entry_id":7}' }),
    daemon: ackAll,
    bash: () => BASH_OK,
    compact: (e: any) => ({ messages: e.messages }),
    pdx: (argv) => (argv[0] === 'relay' && argv[1] === 'hello' ? { exitCode: 0, stdout: HELLO } : { exitCode: 0, stdout: '{}' }),
    ...opts,
  } as W
  if (!w.clock) w.clock = mock.clock(on)
  on('http.fetch', async (_$: any, e: any) => {
    if (e.init?.method === 'GET') { // the team read (TI-5b): never mixed with the event POSTs
      w.gets.push({ url: e.url, init: e.init })
      const sid = new URL(e.url).searchParams.get('session_id') ?? ''
      const a = await w.team(sid, w.gets.length)
      if ('deny' in a) return { deny: a.deny }
      return { value: { status: a.status, ok: a.status >= 200 && a.status < 300, headers: { 'content-type': 'application/json' }, text: a.text ?? '' } }
    }
    if (String(e.url).includes('/mod/v1/prompt/')) { // the prompt routes (U3-0b)
      const pb = JSON.parse(e.init.body)
      w.pqReqs.push({ url: e.url, body: pb })
      const isNext = String(e.url).endsWith('/next')
      const pa = await (isNext ? w.promptNext(pb, w.pqReqs.filter((r) => r.url.endsWith('/next')).length) : w.promptResult(pb, w.pqReqs.filter((r) => r.url.endsWith('/result')).length))
      if ('deny' in pa) return { deny: pa.deny }
      return { value: { status: pa.status, ok: pa.status >= 200 && pa.status < 300, headers: { 'content-type': 'application/json' }, text: pa.text ?? '' } }
    }
    if (String(e.url).includes('/mod/v1/workbook/')) { // the job routes (WB-1c)
      const wbBody = JSON.parse(e.init.body)
      w.wbReqs.push({ url: e.url, body: wbBody })
      const isNext = String(e.url).endsWith('/next')
      if (String(e.url).endsWith('/refresh')) {
        const ra = await w.wbRefresh(wbBody, w.wbReqs.filter((r) => r.url.endsWith('/refresh')).length)
        if ('deny' in ra) return { deny: ra.deny }
        return { value: { status: ra.status, ok: ra.status >= 200 && ra.status < 300, headers: { 'content-type': 'application/json' }, text: ra.text ?? '' } }
      }
      const a = await (isNext ? w.wbNext(wbBody, w.wbReqs.filter((r) => r.url.endsWith('/next')).length) : w.wbResult(wbBody, w.wbReqs.filter((r) => r.url.endsWith('/result')).length))
      if ('deny' in a) return { deny: a.deny }
      return { value: { status: a.status, ok: a.status >= 200 && a.status < 300, headers: { 'content-type': 'application/json' }, text: a.text ?? '' } }
    }
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
    return { value: typeof w.agents === 'function' ? await w.agents() : w.agents }
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
  on('model.complete', async (_$: any, e: any) => {
    w.modelCalls.push(e)
    const r = await w.model(e, w.modelCalls.length)
    return r && 'deny' in r ? { deny: r.deny } : { value: r }
  })
  on('prompt.submit', async (_$: any, e: any) => {
    w.submitCalls.push(e)
    const r = await w.submit(e, w.submitCalls.length)
    return r
  })
  on('turn.abort', async (_$: any, e: any) => {
    w.abortCalls.push(e)
    const r = await w.abort(e)
    return r && 'deny' in r ? { deny: r.deny } : { value: undefined }
  })
  on('model.fork', async (_$: any, e: any) => {
    w.forkCalls.push(e)
    const r = await w.fork(e, w.forkCalls.length)
    return r && 'deny' in r ? { deny: r.deny } : { value: r }
  })
  on('command.register', async (_$: any, e: any) => { w.registered.push(e.name); return { value: { command: e.name } } })
  on('ui.log', async (_$: any, e: any) => { w.logs.push(e.text); return { value: undefined } })
  on('ui.invalidate', async () => { w.invalidated += 1; return { value: undefined } })
  on('session.start', async (_$: any, e: any) => ({ cwd: e.cwd }))
  on('turn.start', async (_$: any, e: any) => ({ turnId: e.turnId }))
  on('turn.complete', async (_$: any, e: any) => ({ text: e.answer }))
  on('classic.SessionStart', async (_$: any, e: any) => (w.sessionStart ? w.sessionStart(e) : {}))
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
    cwd: '/work',
    interactive: true,
    caps: ['workbook.v2', 'workbook.refresh', 'prompt.v1'],
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
    { turn_id: 't1', asks: ['tu-1'], compacting: false, agents: [{ id: 'ag-1', status: 'running' }], error: false },
  ])
  await turnDone($, 't1') // the main turn ended: no turn, no open asks
  w.agents = 'fail'
  await w.clock.advance(10_000)
  expect(ofType(w, 'heartbeat').map((e) => e.data)[1]).toEqual({ asks: [], compacting: false, error: false })
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
  expect(ofType(w, 'heartbeat')[0].data).toEqual({ asks: [], compacting: false, agents: [], error: false })
  expect(new Set(w.posts.map((p) => p.body.stream)).size).toBe(1)
})

// /clear takes a while between session.end{clear} (old sid) and session.switch (new sid; about
// 660 ms in M-U1-4, the hooks beneath doing the relay's /clear work). No heartbeat goes out in
// between: neither one due then nor one that was waiting on agent.list when session.end came.
// Mutation gates: no switching state → the beat due at 20 s goes out under SID1; session.end
// {clear} leaves the beat in flight valid → the one released at 10 s goes out under SID1.
test('between session.end{clear} and session.switch no heartbeat goes out; after it they go on under the new sid', async ($, on) => {
  let releaseList: (() => void) | undefined
  let releaseSwitch: (() => void) | undefined
  const w = evWorld(on)
  await start($, w)
  await turnStart($, 't1')
  w.agents = () => new Promise<any[]>((r) => { releaseList = () => r([]) })
  await w.clock.advance(10_000) // the 10 s beat has begun: it waits on agent.list
  expect(typeof releaseList).toBe('function')
  await end($, 'clear', SID1)
  w.sid = SID2
  w.sessionStart = () => new Promise((r) => { releaseSwitch = () => r({}) })
  const sw = $.classic.SessionStart({ source: 'clear' })
  await w.clock.settle()
  expect(typeof releaseSwitch).toBe('function') // the switch is held beneath the reporter
  w.agents = []
  releaseList!() // the beat begun before session.end goes on now
  await w.clock.advance(10_000) // and the 20 s one comes due, the switch still held
  expect(ofType(w, 'heartbeat')).toEqual([])
  releaseSwitch!()
  await sw
  await w.clock.advance(10_150) // the 30 s beat
  expect(evs(w).map((x) => [x.type, x.sid])).toEqual([
    ['session.start', SID1], ['turn.start', SID1], ['session.end', SID1], ['session.switch', SID2], ['heartbeat', SID2],
  ])
  expect(ofType(w, 'heartbeat')[0].data).toEqual({ asks: [], compacting: false, agents: [], error: false })
})

// Mutation gate: the switch reported only after a next(e) that resolved → no session.switch
// and the heartbeat stays silent for good.
test('a SessionStart that fails beneath still ends the switch: the heartbeat goes on under the new sid', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await end($, 'clear', SID1)
  w.sid = SID2
  w.sessionStart = () => { throw new Error('a hook beneath failed') }
  await $.classic.SessionStart({ source: 'clear' }).catch(() => {})
  await w.clock.advance(10_150)
  expect(evs(w).map((x) => [x.type, x.sid])).toEqual([
    ['session.start', SID1], ['session.end', SID1], ['session.switch', SID2], ['heartbeat', SID2],
  ])
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

// Mutation gate: a beat checks only on its way in (or stopBeat leaves the beat in flight
// valid) → the heartbeat waiting on agent.list lands after session.end.
test('a heartbeat in flight when the session ends never lands after session.end', async ($, on) => {
  let release: (() => void) | undefined
  const w = evWorld(on)
  await start($, w)
  await w.clock.advance(150) // session.start acked
  w.agents = () => new Promise<any[]>((r) => { release = () => r([]) })
  await w.clock.advance(9_850) // the 10 s beat has begun: it waits on agent.list
  expect(typeof release).toBe('function')
  await end($, 'prompt_input_exit') // the final flush goes out inside the hook
  expect(w.posts.length).toBe(2)
  release!() // the beat in flight goes on now
  await w.clock.settle()
  await w.clock.advance(30_000)
  expect(types(w)).toEqual(['session.start', 'session.end'])
  expect(w.posts.length).toBe(2)
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

// ---- the Monitor tool's task is named "monitor" (U1-2 follow-up, spec §7 "Background symbol") ----

// Claude Code lists a background task the Monitor tool started as type "shell" (its "monitor" type is an MCP
// watch). The Monitor tool's tool.call result carries the task id (measured on 2.1.295: {ref, result: {taskId,
// timeoutMs, persistent}, text}); the background event this reporter sends names that id "monitor", on a copy.
// Mutation gates: the rewrite left out → red; every shell task renamed → red; the set not cleared on session.end →
// red; not cleared on session.switch → red.
const MONITOR_OK = { ref: 1, result: { taskId: 'mon1', timeoutMs: 1_800_000, persistent: false }, text: 'Monitor started (task mon1)' }
const monitorWorld = (on: any, result: any = MONITOR_OK) => {
  const w = evWorld(on)
  on('tool.call', { tool: 'Monitor' }, async () => result)
  return w
}
const TASKS = [{ id: 'mon1', type: 'shell', status: 'running', description: 'until stop-me', command: 'until [ -e stop-me ]; do sleep 2; done' }, { id: 'sh2', type: 'shell', status: 'running', description: 'sleep', command: 'sleep 9' }]

test('a task the Monitor tool started is reported as monitor; every other task is untouched', async ($, on) => {
  const w = monitorWorld(on)
  await start($, w)
  await turnStart($, 't1')
  await $.tool.call({ tool: 'Monitor', description: 'd', command: 'until [ -e stop-me ]; do sleep 2; done', timeout_ms: 1_800_000, tool_use_id: 'tu-m' } as any)
  const stop = { stop_hook_active: false, background_tasks: TASKS, session_crons: [] } as any
  await $.classic.Stop(stop)
  await w.clock.advance(150)
  expect(ofType(w, 'background').map((e) => e.data)).toEqual([
    { tasks: [{ id: 'mon1', type: 'monitor', status: 'running' }, { id: 'sh2', type: 'shell', status: 'running' }], crons: 0 },
  ])
  expect(stop.background_tasks[0].type).toBe('shell') // the engine's own object is not rewritten
  await w.clock.advance(10_000) // the heartbeat mirrors the rewritten copy
  expect(ofType(w, 'heartbeat').at(-1).data.background).toEqual({ tasks: [{ id: 'mon1', type: 'monitor', status: 'running' }, { id: 'sh2', type: 'shell', status: 'running' }], crons: 0 })
})

test('a Monitor call that failed, or whose id is not listed, changes nothing', async ($, on) => {
  const w = monitorWorld(on, { ref: 1, result: { taskId: 'mon1' }, isError: true, text: 'could not start' }) // an error answer carries no usable id
  await start($, w)
  await $.tool.call({ tool: 'Monitor', description: 'd', command: 'x', timeout_ms: 1000, tool_use_id: 'tu-m' } as any)
  await $.classic.Stop({ stop_hook_active: false, background_tasks: TASKS } as any)
  await w.clock.advance(150)
  expect(ofType(w, 'background').map((e) => e.data.tasks.map((t: any) => t.type))).toEqual([['shell', 'shell']])
})

test('an id leaves when a Stop stops listing it, so a reused id is an ordinary shell again', async ($, on) => {
  const w = monitorWorld(on)
  await start($, w)
  await $.tool.call({ tool: 'Monitor', description: 'd', command: 'x', timeout_ms: 1000, tool_use_id: 'tu-m' } as any)
  await $.classic.Stop({ stop_hook_active: false, background_tasks: TASKS } as any) // listed: monitor
  await $.classic.Stop({ stop_hook_active: false, background_tasks: [TASKS[1]] } as any) // gone: the task ended
  await $.classic.Stop({ stop_hook_active: false, background_tasks: TASKS } as any) // the id again, a different task
  await w.clock.advance(150)
  expect(ofType(w, 'background').map((e) => e.data.tasks.map((t: any) => t.type))).toEqual([['monitor', 'shell'], ['shell'], ['shell', 'shell']])
})

test('a monitor not listed yet survives a Stop that does not list it (a subagent or an earlier Stop)', async ($, on) => {
  const w = monitorWorld(on)
  await start($, w)
  await $.tool.call({ tool: 'Monitor', description: 'd', command: 'x', timeout_ms: 1000, tool_use_id: 'tu-m' } as any)
  await $.classic.Stop({ stop_hook_active: false, background_tasks: [] } as any)
  await $.classic.Stop({ stop_hook_active: false, background_tasks: TASKS } as any)
  await w.clock.advance(150)
  expect(ofType(w, 'background').map((e) => e.data.tasks.map((t: any) => t.type))).toEqual([[], ['monitor', 'shell']])
})

for (const [name, result] of [
  ['a top-level taskId', { ref: 1, result: {}, taskId: 'mon1', text: 'x' }],
  ['text only', { ref: 1, result: {}, text: 'Monitor started (task mon1, expires in 30m unless the source ends first)' }],
] as const) test('the task id is also found from ' + name, async ($, on) => {
  const w = monitorWorld(on, result)
  await start($, w)
  await $.tool.call({ tool: 'Monitor', description: 'd', command: 'x', timeout_ms: 1000, tool_use_id: 'tu-m' } as any)
  await $.classic.Stop({ stop_hook_active: false, background_tasks: TASKS } as any)
  await w.clock.advance(150)
  expect(ofType(w, 'background').map((e) => e.data.tasks.map((t: any) => t.type))).toEqual([['monitor', 'shell']])
})

test('at most 64 monitor ids are kept: the oldest goes first', async ($, on) => {
  const results = Array.from({ length: 65 }, (_, i) => ({ ref: 1, result: { taskId: 'm' + i }, text: 't' }))
  let n = 0
  const w = evWorld(on)
  on('tool.call', { tool: 'Monitor' }, async () => results[n++])
  await start($, w)
  for (let i = 0; i < 65; i++) await $.tool.call({ tool: 'Monitor', description: 'd', command: 'x', timeout_ms: 1000, tool_use_id: 'tu-' + i } as any)
  await $.classic.Stop({ stop_hook_active: false, background_tasks: [{ id: 'm0', type: 'shell', status: 'running' }, { id: 'm64', type: 'shell', status: 'running' }] } as any)
  await w.clock.advance(150)
  expect(ofType(w, 'background').map((e) => e.data.tasks.map((t: any) => t.type))).toEqual([['shell', 'monitor']])
})

test('a reused monitor id counts as the newest for the cap', async ($, on) => {
  let n = 0
  const ids = [...Array.from({ length: 64 }, (_, i) => 'm' + i), 'm0', 'extra']
  const w = evWorld(on)
  on('tool.call', { tool: 'Monitor' }, async () => ({ ref: 1, result: { taskId: ids[n++] }, text: 't' }))
  await start($, w)
  for (let i = 0; i < ids.length; i++) await $.tool.call({ tool: 'Monitor', description: 'd', command: 'x', timeout_ms: 1000, tool_use_id: 'tu-' + i } as any)
  await $.classic.Stop({ stop_hook_active: false, background_tasks: [{ id: 'm0', type: 'shell', status: 'running' }, { id: 'm1', type: 'shell', status: 'running' }, { id: 'extra', type: 'shell', status: 'running' }] } as any)
  await w.clock.advance(150)
  // m0 was reused (newest), so the cap evicted m1, the oldest left
  expect(ofType(w, 'background').map((e) => e.data.tasks.map((t: any) => t.type))).toEqual([['monitor', 'shell', 'monitor']])
})

test('the monitor ids are forgotten at session.end', async ($, on) => {
  const w = monitorWorld(on)
  await start($, w)
  await $.tool.call({ tool: 'Monitor', description: 'd', command: 'x', timeout_ms: 1000, tool_use_id: 'tu-m' } as any)
  await end($, 'prompt_input_exit', SID1) // no new session.start, no session.switch after it
  await $.classic.Stop({ stop_hook_active: false, background_tasks: TASKS } as any)
  await w.clock.advance(150)
  expect(ofType(w, 'background').map((e) => e.data.tasks.map((t: any) => t.type))).toEqual([['shell', 'shell']])
})

test('the monitor ids are forgotten at session.switch', async ($, on) => {
  const w = monitorWorld(on)
  await start($, w)
  await $.tool.call({ tool: 'Monitor', description: 'd', command: 'x', timeout_ms: 1000, tool_use_id: 'tu-m' } as any)
  w.sid = SID2
  await $.classic.SessionStart({ source: 'clear' }) // session.switch without a session.end before it
  await $.classic.Stop({ stop_hook_active: false, background_tasks: TASKS } as any)
  await w.clock.advance(150)
  expect(ofType(w, 'background').map((e) => e.data.tasks.map((t: any) => t.type))).toEqual([['shell', 'shell']])
})

// ---- what a restarted daemon learns from any batch (U1-2a-1) ----

// A daemon that restarts under a running mod never sees that stream's session.start again: every
// batch says where the session runs and that it is interactive, the heartbeats alone included.
// Mutation gate: cwd left out of the envelope → red.
test('every batch carries cwd and interactive', async ($, on) => {
  const w = evWorld(on, { daemon: (body, n) => (n === 2 ? { status: 500 } : ackAll(body)) })
  await start($, w)
  await turnStart($, 't1')
  await w.clock.advance(150) // session.start, turn.start
  await w.clock.advance(10_000) // the heartbeat's batch fails …
  await w.clock.advance(1000) // … and goes again
  await end($, 'prompt_input_exit') // the final flush, inside the hook
  expect(w.posts.length).toBe(4)
  expect(w.posts.map((p) => [p.body.cwd, p.body.interactive])).toEqual([['/work', true], ['/work', true], ['/work', true], ['/work', true]])
  expect(w.posts.map((p) => p.body.events.map((e: any) => e.type))).toEqual([['session.start', 'turn.start'], ['heartbeat'], ['heartbeat'], ['session.end']])
})

test('cwd survives /clear', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await w.clock.advance(150)
  await end($, 'clear', SID1)
  w.sid = SID2
  await $.classic.SessionStart({ source: 'clear' })
  await w.clock.advance(10_000) // the beat after the switch
  const after = w.posts.slice(2) // [session.start], [session.end] (inside its hook), then the switch's
  expect(after.length).toBeGreaterThan(0)
  expect(after.flatMap((p) => p.body.events.map((e: any) => e.type))).toEqual(['session.switch', 'heartbeat'])
  expect(after.every((p) => p.body.cwd === '/work' && p.body.interactive === true)).toBe(true)
})

test('heartbeat carries error after a main turn ends in error and clears it at the next main turn', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await w.clock.advance(10_000) // beat 1: nothing has failed
  await turnStart($, 't1')
  await turnDone($, 't1', { reason: 'error' })
  await w.clock.advance(10_000) // beat 2: the main turn ended in error
  await turnStart($, 't2')
  await w.clock.advance(10_000) // beat 3: a new main turn clears it
  await turnDone($, 't2', { reason: 'error' })
  await end($, 'clear', SID1)
  w.sid = SID2
  await $.classic.SessionStart({ source: 'clear' })
  await w.clock.advance(10_150) // beat 4: a new conversation has no error outcome yet (posted)
  expect(ofType(w, 'heartbeat').map((e) => e.data.error)).toEqual([false, true, false, false])
})

// Mutation gate: lastError set by a subagent turn → red.
test('a subagent turn ending in error does not set the heartbeat error', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 'st-1', { agentId: 'ag-1', reason: 'error' })
  await w.clock.advance(10_000)
  await turnDone($, 't1')
  await w.clock.advance(10_150)
  expect(ofType(w, 'heartbeat').map((e) => e.data.error)).toEqual([false, false])
})

test('heartbeat mirrors the last background and a new session.start forgets it', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await w.clock.advance(10_000) // beat 1: no classic.Stop yet, no background member
  await $.classic.Stop({ stop_hook_active: false, background_tasks: [{ id: 'b1', type: 'monitor', status: 'running', description: 'tail' }], session_crons: [{ id: 'c1', schedule: '0 9 * * *', recurring: true }] } as any)
  await w.clock.advance(10_000) // beat 2: the tasks the last Stop listed
  await end($, 'clear', SID1)
  w.sid = SID2
  await $.classic.SessionStart({ source: 'clear' })
  await w.clock.advance(10_000) // beat 3: kept across /clear (same process)
  await $.classic.Stop({ stop_hook_active: false } as any)
  await w.clock.advance(10_000) // beat 4: the last Stop listed none
  await $.classic.Stop({ stop_hook_active: false, background_tasks: [{ id: 'b2', type: 'shell', status: 'running', description: 'sleep' }] } as any)
  await start($, w) // a new session.start in the same load
  await w.clock.advance(10_150) // beat 5: forgotten (posted)
  const beats = ofType(w, 'heartbeat').map((e) => e.data)
  expect(beats.length).toBe(5)
  const BG = { tasks: [{ id: 'b1', type: 'monitor', status: 'running' }], crons: 1 }
  expect(beats.map((d) => d.background)).toEqual([undefined, BG, BG, { tasks: [], crons: 0 }, undefined])
  expect('background' in beats[0]).toBe(false)
  expect('background' in beats[4]).toBe(false)
  expect(ofType(w, 'background').map((e) => e.data)).toEqual([BG, { tasks: [], crons: 0 }, { tasks: [{ id: 'b2', type: 'shell', status: 'running' }], crons: 0 }])
})

test('heartbeat omits agents when agent.list throws', async ($, on) => {
  const w = evWorld(on)
  w.agents = [{ id: 'ag-1', description: 'look around', type: 'Explore', status: 'running' }]
  await start($, w)
  await w.clock.advance(10_150) // beat 1: the list answers
  w.agents = 'fail'
  await w.clock.advance(10_000) // beat 2: it throws — no agents member, not [] (that would clear every dot)
  const beats = ofType(w, 'heartbeat').map((e) => e.data)
  expect(beats[0].agents).toEqual([{ id: 'ag-1', status: 'running' }])
  expect(beats[1]).toEqual({ asks: [], compacting: false, error: false })
  expect('agents' in beats[1]).toBe(false)
})

// ---- tool.approved: a permission ask leaves waiting when its row starts running (M-U1-6) ----

// The ToolUse row's isRunning is false while the permission dialog is open and turns true about
// 16 ms after the person approves (M-U1-6, CC 2.1.294). The render hook only looks: whatever is
// beneath answers is what the engine draws.
const DRAWN = { type: 'engine', ref: 7 } as const
const toolRow = (id: string, tool: string, isRunning: boolean) => ({
  surface: 'terminal', component: 'ToolUse', requestId: id,
  props: { tool_use_id: id, tool, input: {}, isRunning, isErrored: false, isInterrupted: false },
}) as any
function renderWorld(on: any, opts: Partial<W> = {}) {
  const seen: any[] = []
  on('ui.render', { component: 'ToolUse' }, async (_$: any, e: any) => { seen.push(e); return DRAWN })
  return { w: evWorld(on, { decision: 'ask', ...opts }), seen }
}

// Mutation gate: report on every render (no delete) → red.
test('a permission ask leaves on the ToolUse render with isRunning true and reports tool.approved once', async ($, on) => {
  const { w } = renderWorld(on)
  await start($, w)
  await turnStart($, 't1')
  await $.tool.check({ tool: 'Bash', input: { command: 'rm -rf x' }, tool_use_id: 'tu-1' } as any)
  await $.ui.render(toolRow('tu-1', 'Bash', false)) // the dialog is open
  await w.clock.advance(10_150) // beat 1, posted at 10 150
  await $.ui.render(toolRow('tu-1', 'Bash', true)) // approved: the row runs
  await $.ui.render(toolRow('tu-1', 'Bash', true)) // redrawn while it runs
  await $.ui.render(toolRow('tu-1', 'Bash', false)) // and once it ended
  await w.clock.advance(10_000) // beat 2
  expect(ofType(w, 'heartbeat').map((e) => e.data.asks)).toEqual([['tu-1'], []])
  expect(ofType(w, 'tool.approved').map((e) => [e.sid, e.data])).toEqual([[SID1, { tool_use_id: 'tu-1' }]])
  expect(types(w).filter((t) => t !== 'heartbeat')).toEqual(['session.start', 'turn.start', 'tool.check', 'tool.approved'])
})

// Mutation gate: approve question asks too → red.
test('a question ask is not approved by a render', async ($, on) => {
  let release!: () => void
  on('tool.call', { tool: 'ExitPlanMode' }, async () => {
    await new Promise<void>((r) => { release = r }) // the plan dialog is up
    return BASH_OK
  })
  const { w } = renderWorld(on)
  await start($, w)
  await turnStart($, 't1')
  await $.tool.check({ tool: 'AskUserQuestion', input: {}, tool_use_id: 'tu-q' } as any) // a check naming a question tool
  const call = $.tool.call({ tool: 'ExitPlanMode', plan: 'p' } as any) // its tool.start opens a question
  await w.clock.advance(150)
  const plan = ofType(w, 'tool.start')[0].data.tool_use_id
  expect(plan).toMatch(/.+/)
  await $.ui.render(toolRow('tu-q', 'AskUserQuestion', true))
  await $.ui.render(toolRow(plan, 'ExitPlanMode', true))
  await w.clock.advance(10_000) // beat 1, posted at 10 150
  expect(ofType(w, 'heartbeat')[0].data.asks).toEqual(['tu-q', plan]) // a question leaves at its tool.end only
  release()
  await call
  await w.clock.advance(10_000)
  expect(ofType(w, 'heartbeat')[1].data.asks).toEqual(['tu-q'])
  expect(ofType(w, 'tool.approved')).toEqual([])
})

test('a render of a tool with no open ask reports nothing', async ($, on) => {
  const { w } = renderWorld(on, { decision: 'allow' })
  await start($, w)
  await w.clock.advance(150) // session.start
  await $.tool.check({ tool: 'Bash', input: { command: 'ls' }, tool_use_id: 'tu-ok' } as any) // allowed: no ask
  await w.clock.advance(150)
  await $.ui.render(toolRow('tu-ok', 'Bash', true))
  await $.ui.render(toolRow('tu-none', 'Bash', true)) // a row no check ever named
  await w.clock.advance(1000)
  expect(types(w)).toEqual(['session.start', 'tool.check'])
  expect(w.posts.length).toBe(2)
})

test('the render hook returns next(e) unchanged', async ($, on) => {
  const { w, seen } = renderWorld(on)
  await start($, w)
  await $.tool.check({ tool: 'Bash', input: {}, tool_use_id: 'tu-1' } as any)
  const rows = [toolRow('tu-1', 'Bash', false), toolRow('tu-1', 'Bash', true), toolRow('tu-x', 'Read', true)]
  for (const row of rows) expect(await $.ui.render(row)).toEqual(DRAWN) // what beneath drew, as drawn
  expect(seen.map((e) => e.props)).toEqual(rows.map((r) => r.props)) // and beneath saw the props as given
  expect(seen.map((e) => e.requestId)).toEqual(['tu-1', 'tu-1', 'tu-x'])
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

// ---- TI-5b: `lead mode · N members` in the SessionMode footer (team-interface spec §4.10) ----
//
// The mod reads GET /mod/v1/team?session_id= on the mod socket at session.start and every 15 s, never from the render
// hook; the render hook only reads the cached answer. A SessionMode instance has props {modes: string[]}.

const modeRow = (modes: string[] = []) => ({ surface: 'terminal', component: 'SessionMode', requestId: 'sm', props: { modes } }) as any
let engineModes: string[] = []
function modeWorld(on: any, opts: Partial<W> = {}) {
  // beneath the mod: the engine's own footer; it records the modes the mod handed down
  engineModes = []
  on('ui.render', { component: 'SessionMode' }, async (_$: any, e: any) => { engineModes = [...e.props.modes]; return DRAWN })
  return { w: evWorld(on, opts) }
}
// drawn renders the footer once and returns the modes the engine drew
const drawn = async ($: any, modes: string[] = []) => { await $.ui.render(modeRow(modes)); return engineModes }

test('a lead gets the label appended to the footer modes', async ($, on) => {
  const { w } = modeWorld(on, { team: () => teamAnswer('lead', 3, '資源線') })
  await start($, w)
  expect(w.gets.length).toBe(1)
  expect(w.gets[0].url).toBe('http://pdx/mod/v1/team?session_id=' + SID1)
  expect(w.gets[0].init).toMatchObject({ method: 'GET', socketPath: SOCK })
  expect(await drawn($, ['focus'])).toEqual(['focus', 'lead mode · 3 members'])
})

test('1 member is singular and 0 members still shows', async ($, on) => {
  let n = 1
  const { w } = modeWorld(on, { team: () => teamAnswer('lead', n) })
  await start($, w)
  expect(await drawn($)).toEqual(['lead mode · 1 member'])
  n = 0
  await w.clock.advance(15_000)
  expect(await drawn($)).toEqual(['lead mode · 0 members'])
})

// Mutation gate: append for every role → red.
for (const role of ['member', 'none']) {
  test(`a ${role} session leaves the modes unchanged`, async ($, on) => {
    const { w } = modeWorld(on, { team: () => teamAnswer(role, 2) })
    await start($, w)
    expect(await drawn($, ['focus'])).toEqual(['focus'])
  })
}

test('no label before the first good read, and a failed read keeps the last good value', async ($, on) => {
  let mode: 'fail' | 'ok' | 'down' = 'fail'
  const { w } = modeWorld(on, { team: () => (mode === 'fail' ? { status: 503, text: '{"error":"unavailable"}' } : mode === 'down' ? { deny: 'ECONNREFUSED' } : teamAnswer('lead', 2)) })
  await start($, w)
  expect(await drawn($)).toEqual([]) // the first read failed: nothing to show yet
  mode = 'ok'
  await w.clock.advance(15_000)
  expect(await drawn($)).toEqual(['lead mode · 2 members'])
  for (const m of ['fail', 'down'] as const) {
    mode = m
    await w.clock.advance(15_000)
    expect(await drawn($)).toEqual(['lead mode · 2 members']) // the last good value stands
  }
})

test('a good read of another role takes the label away, a changed count redraws', async ($, on) => {
  let ans = teamAnswer('lead', 1)
  const { w } = modeWorld(on, { team: () => ans })
  await start($, w)
  const base = w.invalidated
  expect(base).toBeGreaterThan(0) // the first good read asked for a redraw
  await w.clock.advance(15_000)
  expect(w.invalidated).toBe(base) // nothing changed, nothing redrawn
  ans = teamAnswer('lead', 2)
  await w.clock.advance(15_000)
  expect(w.invalidated).toBe(base + 1)
  expect(await drawn($)).toEqual(['lead mode · 2 members'])
  ans = teamAnswer('none')
  await w.clock.advance(15_000)
  expect(w.invalidated).toBe(base + 2)
  expect(await drawn($)).toEqual([])
})

// The 15 s timer is the reader; the render hook never goes to the socket.
// Mutation gate: read from the render hook → red.
test('the render hook never calls fetch', async ($, on) => {
  const { w } = modeWorld(on, { team: () => teamAnswer('lead', 3) })
  await start($, w)
  const before = w.gets.length
  for (let i = 0; i < 5; i++) await drawn($)
  expect(w.gets.length).toBe(before)
  await w.clock.advance(15_000)
  expect(w.gets.length).toBe(before + 1)
  await w.clock.advance(45_000)
  expect(w.gets.length).toBe(before + 4)
})

test('session.end clears the timer', async ($, on) => {
  const { w } = modeWorld(on, { team: () => teamAnswer('lead', 3) })
  await start($, w)
  await end($, 'prompt_input_exit')
  const n = w.gets.length
  await w.clock.advance(60_000)
  expect(w.gets.length).toBe(n)
})

// A /clear or a relay changes the session id: the cached answer is the old session's, so it goes and the new id is read.
// Mutation gate: keep asking with the old id / keep the old label → red.
test('session.switch drops the cache and reads again with the new id', async ($, on) => {
  const { w } = modeWorld(on, { team: (sid) => (sid === SID1 ? teamAnswer('lead', 3) : teamAnswer('none')) })
  await start($, w)
  expect(await drawn($)).toEqual(['lead mode · 3 members'])
  await end($, 'clear')
  w.sid = SID2
  await $.classic.SessionStart({ source: 'clear' } as any)
  await w.clock.settle()
  expect(w.gets[w.gets.length - 1].url).toBe('http://pdx/mod/v1/team?session_id=' + SID2)
  expect(await drawn($)).toEqual([]) // the new conversation is no lead
  await w.clock.advance(15_000)
  expect(w.gets[w.gets.length - 1].url).toBe('http://pdx/mod/v1/team?session_id=' + SID2)
})

test('an answer for the old session that lands after a switch is dropped', async ($, on) => {
  let release: (a: Answer) => void = () => {}
  const slow = new Promise<Answer>((r) => { release = r })
  const { w } = modeWorld(on, { team: (_sid, n) => (n === 1 ? slow : teamAnswer('none')) })
  await $.session.start({ cwd: '/work', surface: 'terminal', isInteractive: true })
  await w.clock.settle()
  await end($, 'clear')
  w.sid = SID2
  await $.classic.SessionStart({ source: 'clear' } as any)
  await w.clock.settle()
  release(teamAnswer('lead', 9)) // the first read, for SID1, answers late
  await w.clock.settle()
  expect(await drawn($)).toEqual([])
})

// $.http.fetch cannot be cancelled: a daemon that takes the request and never answers must not collect one more per tick.
// Mutation gate: drop the one-in-flight guard → red.
test('a read that never answers is the only one in flight, and a switch may read again', async ($, on) => {
  const { w } = modeWorld(on, { team: () => never() })
  await start($, w)
  expect(w.gets.length).toBe(1)
  await w.clock.advance(120_000) // eight ticks, the 5 s deadline long gone
  expect(w.gets.length).toBe(1)
  expect(await drawn($, ['focus'])).toEqual(['focus'])
  await end($, 'clear')
  w.sid = SID2
  await $.classic.SessionStart({ source: 'clear' } as any)
  await w.clock.settle()
  expect(w.gets.length).toBe(2) // the new session is a new generation
  expect(w.gets[1].url).toBe('http://pdx/mod/v1/team?session_id=' + SID2)
})

test('a headless session asks nothing and leaves the footer alone', async ($, on) => {
  const { w } = modeWorld(on, { team: () => teamAnswer('lead', 3) })
  await start($, w, false)
  await w.clock.advance(60_000)
  expect(w.gets).toEqual([])
  expect(await drawn($, ['focus'])).toEqual(['focus'])
})

test('without a mod_socket the footer is untouched', async ($, on) => {
  const { w } = modeWorld(on, { pdxJSON: PDX_JSON_OLD, team: () => teamAnswer('lead', 3) })
  await start($, w)
  await w.clock.advance(60_000)
  expect(w.gets).toEqual([])
  expect(await drawn($, ['focus'])).toEqual(['focus'])
})

// ---- WB-1c: the workbook's job executor (session workbook spec §5.1) ----

const JOB = (extra: any = {}) => ({ id: 'wbj-1', kind: 'turn', complete: { model: 'haiku', system: [{ text: 'SYS', cache: true }, { text: 'TAIL' }], prompt: '{"turn":1}', max_tokens: 4096, effort: 'low', timeout_ms: 30000 }, ...extra })
const jobAnswer = (job: any): Answer => ({ status: 200, text: JSON.stringify({ job }) })
const nextReqs = (w: W) => w.wbReqs.filter((r) => r.url.endsWith('/next'))
const resultReqs = (w: W) => w.wbReqs.filter((r) => r.url.endsWith('/result'))
const USAGE = { input_tokens: 100, output_tokens: 20, cache_read_input_tokens: 50, cache_creation_input_tokens: 7 }

test('every batch announces workbook.v2, workbook.refresh and prompt.v1 and nothing else', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await turnStart($, 't1')
  await w.clock.advance(150)
  await w.clock.advance(10_150) // a heartbeat batch
  expect(w.posts.length).toBeGreaterThanOrEqual(2)
  for (const p of w.posts) expect(p.body.caps).toEqual(['workbook.v2', 'workbook.refresh', 'prompt.v1'])
})

// Mutation gate: ask inside the hook, or for a subagent / interrupted turn → red.
test('a main turn that answered or failed asks next, from a timer; a subagent or an interrupted turn does not', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  expect(w.wbReqs).toEqual([]) // never inside the hook
  await w.clock.settle()
  expect(nextReqs(w).length).toBe(1)
  expect(nextReqs(w)[0].body).toEqual({ stream: expect.stringMatching(/^[A-Za-z0-9_-]{22}$/), session_id: SID1, wait_ms: 15000 })
  await turnStart($, 't2')
  await turnDone($, 't2', { reason: 'error' })
  await w.clock.settle()
  expect(nextReqs(w).length).toBe(2)
  await turnStart($, 't3')
  await turnDone($, 'st-1', { agentId: 'ag-1' })
  await turnDone($, 't3', { reason: 'aborted', isAborted: true })
  await w.clock.settle()
  expect(nextReqs(w).length).toBe(2)
})

// Mutation gate: pass max_tokens / timeout_ms through unmapped, or drop the cache marks → red.
test('a turn job runs one $.model.complete with the keys mapped, then reports usage and latency', async ($, on) => {
  const w = evWorld(on, { wbNext: (_b, n) => (n === 1 ? jobAnswer(JOB()) : { status: 204 }), model: () => ({ isAnswered: true, text: '{"skip":false}', usage: USAGE }) })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  expect(w.modelCalls.length).toBe(1)
  const c = w.modelCalls[0]
  expect(c.model).toBe('haiku')
  expect(c.prompt).toBe('{"turn":1}')
  expect(c.system).toBe('SYSTAIL')
  expect(c.systemBlocks).toEqual([{ text: 'SYS', cache: true }, { text: 'TAIL' }])
  expect(c.maxTokens).toBe(4096)
  expect(c.effort).toBe('low')
  expect(c.timeoutMs).toBe(30000)
  expect(resultReqs(w).length).toBe(1)
  expect(resultReqs(w)[0].body).toEqual({
    stream: expect.any(String), job_id: 'wbj-1', answered: true, text: '{"skip":false}',
    usage: { input: 100, output: 20, cache_read: 50 }, latency_ms: expect.any(Number),
  })
})

// Mutation gate: map every non-answer to one reason, or lose status / error → red.
const outcomes: [string, any, any][] = [
  ['api error', { isAnswered: false, reason: 'api-error', status: 529, error: 'overloaded', usage: USAGE }, { reason: 'api-error', status: 529, error: 'overloaded' }],
  ['empty reply', { isAnswered: false, reason: 'empty-reply', usage: USAGE }, { reason: 'empty-reply' }],
  ['aborted', { isAnswered: false, reason: 'aborted', usage: USAGE }, { reason: 'aborted' }],
  ['a reason it does not know', { isAnswered: false, reason: 'brand-new', usage: USAGE }, { reason: 'api-error' }],
]
for (const [name, result, want] of outcomes) {
  test(`${name} is reported with its reason`, async ($, on) => {
    const w = evWorld(on, { wbNext: (_b, n) => (n === 1 ? jobAnswer(JOB()) : { status: 204 }), model: () => result })
    await start($, w)
    await turnStart($, 't1')
    await turnDone($, 't1')
    await w.clock.settle()
    const b = resultReqs(w)[0].body
    expect(b.answered).toBe(false)
    expect(b).toMatchObject(want)
    expect(b.text).toBeUndefined()
  })
}

test('a call the engine refuses (it rejects) is reported as refused', async ($, on) => {
  const w = evWorld(on, { wbNext: (_b, n) => (n === 1 ? jobAnswer(JOB()) : { status: 204 }), model: () => ({ deny: 'model blocked' }) })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  expect(resultReqs(w)[0].body).toMatchObject({ answered: false, reason: 'refused', job_id: 'wbj-1', usage: { input: 0, output: 0, cache_read: 0 } })
})

// Mutation gate: run an unknown kind, or leave it unanswered → red.
for (const kind of ['mystery']) {
  test(`a ${kind} job is answered refused without a model call`, async ($, on) => {
    const w = evWorld(on, { wbNext: (_b, n) => (n === 1 ? jobAnswer({ id: 'wbj-r', kind, fork: { prompt: 'x' } }) : { status: 204 }) })
    await start($, w)
    await turnStart($, 't1')
    await turnDone($, 't1')
    await w.clock.settle()
    expect(w.modelCalls.length).toBe(0)
    expect(resultReqs(w)[0].body).toMatchObject({ job_id: 'wbj-r', answered: false, reason: 'refused' })
  })
}

// Mutation gate: ignore `more` → the second job waits for the next turn → red.
test('a result answered more asks again at once; no more stops', async ($, on) => {
  const w = evWorld(on, {
    wbNext: (_b, n) => (n <= 2 ? jobAnswer(JOB({ id: 'wbj-' + n })) : { status: 204 }),
    wbResult: (_b, n) => ({ status: 200, text: JSON.stringify({ more: n === 1 }) }),
  })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  expect(resultReqs(w).map((r) => r.body.job_id)).toEqual(['wbj-1', 'wbj-2'])
  expect(nextReqs(w).length).toBe(2) // the second job came on the same loop; nothing asks a third time
  await w.clock.advance(5000)
  expect(nextReqs(w).length).toBe(2)
})

// Mutation gate: start a second poll while one runs → red.
test('one job at a time: a trigger during a call only marks one more ask afterwards', async ($, on) => {
  let release: (v: any) => void = () => {}
  const w = evWorld(on, {
    wbNext: (_b, n) => (n === 1 ? jobAnswer(JOB()) : { status: 204 }),
    model: () => new Promise((resolve) => { release = resolve }),
  })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  expect(w.modelCalls.length).toBe(1)
  await turnStart($, 't2')
  await turnDone($, 't2')
  await w.clock.settle()
  expect(nextReqs(w).length).toBe(1) // busy: no second poll while the call runs
  release({ isAnswered: true, text: '{}', usage: USAGE })
  await w.clock.settle()
  expect(resultReqs(w).length).toBe(1)
  expect(nextReqs(w).length).toBe(2) // the trigger that came meanwhile is asked once, afterwards
})

// Mutation gate: ignore the `workbook` field of the events answer → red.
test('an events answer with workbook:true asks next at once (wait 0)', async ($, on) => {
  const w = evWorld(on, {
    daemon: (body) => ({ status: 200, text: JSON.stringify({ ack: body.events[body.events.length - 1].seq, workbook: true }) }),
    wbNext: (_b, n) => (n === 1 ? jobAnswer(JOB()) : { status: 204 }),
  })
  await start($, w)
  await w.clock.advance(150)
  await w.clock.settle()
  expect(nextReqs(w)[0].body.wait_ms).toBe(0)
  expect(w.modelCalls.length).toBe(1)
})

test('a result that cannot be reported drops the job: no further asking', async ($, on) => {
  const w = evWorld(on, { wbNext: (_b, n) => (n === 1 ? jobAnswer(JOB()) : { status: 204 }), wbResult: () => ({ deny: 'socket gone' }) })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  expect(resultReqs(w).length).toBe(1)
  expect(nextReqs(w).length).toBe(1)
})

// Mutation gate: ask on whatever hint arrives → red (codex R1).
test('a workbook hint that arrives after the session ended asks nothing', async ($, on) => {
  let answer: (a: Answer) => void = () => {}
  let held = false
  const w = evWorld(on, {
    daemon: (body, n) => {
      if (n === 1) { held = true; return new Promise<Answer>((resolve) => { answer = resolve }) } // the first batch stays in flight
      return ackAll(body)
    },
  })
  await start($, w)
  await w.clock.advance(150) // the batch goes out and waits
  expect(held).toBe(true)
  await end($, 'exit')
  answer({ status: 200, text: JSON.stringify({ ack: 1, workbook: true }) })
  await w.clock.settle()
  expect(w.wbReqs).toEqual([])
})

test('after session.end nothing asks', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await turnDone($, 't1') // an ask is scheduled ...
  await end($, 'exit') // ... and the session ends before its timer runs
  await w.clock.settle()
  expect(w.wbReqs).toEqual([])
})

// Mutation gate: no mod-owned deadline → a call that never settles blocks the executor for good (codex attack).
test('a model call that never settles is cut at timeout_ms + 5 s (before the lease runs out), reported aborted, and the executor works again', async ($, on) => {
  const w = evWorld(on, {
    wbNext: (_b, n) => (n === 1 ? jobAnswer(JOB()) : n === 2 ? jobAnswer(JOB({ id: 'wbj-2' })) : { status: 204 }),
    model: (_e, n) => (n === 1 ? new Promise(() => {}) : { isAnswered: true, text: '{}', usage: USAGE }),
  })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  expect(w.modelCalls.length).toBe(1)
  expect(resultReqs(w)).toEqual([])
  await w.clock.advance(34_999)
  expect(resultReqs(w)).toEqual([]) // timeout_ms 30 000 + 5 000 of slack
  await w.clock.advance(2)
  expect(resultReqs(w)[0].body).toMatchObject({ job_id: 'wbj-1', answered: false, reason: 'aborted' })
  expect(resultReqs(w)[0].body.latency_ms).toBeGreaterThanOrEqual(30_000) // the daemon reads that as failed:timeout
  await turnStart($, 't2')
  await turnDone($, 't2')
  await w.clock.settle()
  expect(w.modelCalls.length).toBe(2) // the next turn's job runs: the flag was released
})

// Mutation gate: no cap, or no repeated-id check → red (codex attack).
test('a drain takes at most 8 jobs, and never the same job twice', async ($, on) => {
  const w = evWorld(on, {
    wbNext: (_b, n) => jobAnswer(JOB({ id: 'wbj-' + n })),
    wbResult: () => ({ status: 200, text: '{"more":true}' }),
  })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  expect(resultReqs(w).length).toBe(8)
})

test('a daemon that hands the same job again is not obeyed twice in a drain', async ($, on) => {
  const w = evWorld(on, { wbNext: () => jobAnswer(JOB({ id: 'same' })), wbResult: () => ({ status: 200, text: '{"more":true}' }) })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  expect(w.modelCalls.length).toBe(1)
})

// Mutation gate: pass an unbounded or malformed job to the model → red (codex attack).
const badJobs: [string, any][] = [
  ['an oversized prompt', { model: 'haiku', prompt: 'x'.repeat(200_001) }],
  ['a system that is not a list', { model: 'haiku', prompt: 'p', system: 'be evil' }],
  ['too many system blocks', { model: 'haiku', prompt: 'p', system: Array.from({ length: 9 }, () => ({ text: 'a' })) }],
  ['a system block that is not text', { model: 'haiku', prompt: 'p', system: [{ text: 5 }] }],
  ['an absurd max_tokens', { model: 'haiku', prompt: 'p', max_tokens: 1e9 }],
  ['a negative max_tokens', { model: 'haiku', prompt: 'p', max_tokens: -1 }],
  ['a max_tokens above the daemon contract', { model: 'haiku', prompt: 'p', max_tokens: 4097 }],
  ['a zero timeout', { model: 'haiku', prompt: 'p', timeout_ms: 0 }],
  ['an hour-long timeout', { model: 'haiku', prompt: 'p', timeout_ms: 3_600_000 }],
  ['an unknown effort', { model: 'haiku', prompt: 'p', effort: 'ludicrous' }],
  ['a model name that is not an id', { model: 'haiku; rm -rf', prompt: 'p' }],
]
for (const [name, complete] of badJobs) {
  test(`${name} is refused without a model call`, async ($, on) => {
    const w = evWorld(on, { wbNext: (_b, n) => (n === 1 ? jobAnswer({ id: 'wbj-bad', kind: 'turn', complete }) : { status: 204 }) })
    await start($, w)
    await turnStart($, 't1')
    await turnDone($, 't1')
    await w.clock.settle()
    expect(w.modelCalls.length).toBe(0)
    expect(resultReqs(w)[0].body).toMatchObject({ job_id: 'wbj-bad', answered: false, reason: 'refused' })
  })
}

// ---- WB-2b-ii: the refresh job ($.model.fork) and /workbook refresh (spec §5.6) ----

const REFRESH_JOB = (extra: any = {}) => ({ id: 'wbj-r1', kind: 'refresh', fork: { prompt: '[工作簿重整] …', timeout_ms: 90000 }, ...extra })
const FORK_OK = { isAnswered: true, text: '{"status":"s","todos":{"done":[1],"dropped":[],"add":[]}}', usage: { input_tokens: 10, output_tokens: 5, cache_read_input_tokens: 900, cache_creation_input_tokens: 0 } }
const wbCmd = ($: any, args: string) => $.command.run({ command: 'workbook', args, origin: { kind: 'composer' }, presentation: { isFullscreen: false, columns: 100 } })
const refreshReqs = (w: W) => w.wbReqs.filter((r) => r.url.endsWith('/refresh'))

// Mutation gate: complete instead of fork, or send more than the prompt, or drop the usage → red.
test('a refresh job runs one $.model.fork with the prompt alone, and reports the usage and latency', async ($, on) => {
  const w = evWorld(on, { wbNext: (_b, n) => (n === 1 ? jobAnswer(REFRESH_JOB()) : { status: 204 }), fork: () => FORK_OK })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  expect(w.modelCalls.length).toBe(0)
  expect(w.forkCalls.length).toBe(1)
  expect(w.forkCalls[0]).toEqual({ prompt: '[工作簿重整] …' })
  expect(resultReqs(w)[0].body).toMatchObject({
    job_id: 'wbj-r1', answered: true, text: FORK_OK.text, usage: { input: 10, output: 5, cache_read: 900 },
  })
})

// Mutation gate: map nothing-to-fork to api-error → red.
test('a fork with nothing to fork is reported as nothing-to-fork', async ($, on) => {
  const w = evWorld(on, { wbNext: (_b, n) => (n === 1 ? jobAnswer(REFRESH_JOB()) : { status: 204 }), fork: () => ({ isAnswered: false, reason: 'nothing-to-fork', usage: {} }) })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  expect(resultReqs(w)[0].body).toMatchObject({ job_id: 'wbj-r1', answered: false, reason: 'nothing-to-fork' })
})

// Mutation gate: pass a malformed fork to the model → red.
for (const [name, fork] of [
  ['no fork', undefined],
  ['an empty prompt', { prompt: '' }],
  ['a prompt that is not text', { prompt: 5 }],
  ['an oversized prompt', { prompt: 'x'.repeat(200_001) }],
  ['a zero timeout', { prompt: 'p', timeout_ms: 0 }],
  ['an hour-long timeout', { prompt: 'p', timeout_ms: 3_600_000 }],
] as [string, any][]) {
  test(`a refresh job with ${name} is refused without a model call`, async ($, on) => {
    const w = evWorld(on, { wbNext: (_b, n) => (n === 1 ? jobAnswer({ id: 'wbj-r2', kind: 'refresh', fork }) : { status: 204 }) })
    await start($, w)
    await turnStart($, 't1')
    await turnDone($, 't1')
    await w.clock.settle()
    expect(w.forkCalls.length).toBe(0)
    expect(resultReqs(w)[0].body).toMatchObject({ job_id: 'wbj-r2', answered: false, reason: 'refused' })
  })
}

// Mutation gate: no deadline on the fork → the executor holds for good → red.
test('a fork that never settles is cut at timeout_ms + 5 s and reported aborted', async ($, on) => {
  const w = evWorld(on, {
    wbNext: (_b, n) => (n === 1 ? jobAnswer(REFRESH_JOB({ fork: { prompt: 'p', timeout_ms: 90000 } })) : { status: 204 }),
    fork: () => new Promise(() => {}),
  })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  expect(w.forkCalls.length).toBe(1)
  await w.clock.advance(94_000)
  expect(resultReqs(w).length).toBe(0)
  await w.clock.advance(1100)
  expect(resultReqs(w)[0].body).toMatchObject({ job_id: 'wbj-r1', answered: false, reason: 'aborted' })
})

// Mutation gate: ask next inside the hook, or not at all after a 202 → red.
test('/workbook refresh asks the daemon, answers one line, then asks next at once from a timer', async ($, on) => {
  const w = evWorld(on, { wbNext: (_b, n) => (n === 1 ? jobAnswer(REFRESH_JOB()) : { status: 204 }), fork: () => FORK_OK })
  await start($, w)
  const r = await wbCmd($, 'refresh')
  expect(r.text).toContain('已排入')
  expect(refreshReqs(w)).toHaveLength(1)
  expect(refreshReqs(w)[0].body).toEqual({ stream: expect.stringMatching(/^[A-Za-z0-9_-]{22}$/), session_id: SID1 })
  expect(nextReqs(w)).toHaveLength(0) // not inside the hook
  await w.clock.settle()
  expect(nextReqs(w)).toHaveLength(1)
  expect(nextReqs(w)[0].body.wait_ms).toBe(0)
  expect(w.forkCalls.length).toBe(1)
})

test('/workbook refresh: not_live, refresh_pending, an unreachable daemon and wrong arguments each say so and ask nothing', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  w.wbRefresh = () => ({ status: 409, text: '{"error":"not_live"}' })
  expect((await wbCmd($, 'refresh')).text).toContain('沒有可執行重整')
  w.wbRefresh = () => ({ status: 409, text: '{"error":"refresh_pending"}' })
  expect((await wbCmd($, 'refresh')).text).toContain('還在進行')
  w.wbRefresh = () => ({ status: 500, text: '{"error":"internal"}' })
  expect((await wbCmd($, 'refresh')).text).toContain('失敗')
  w.wbRefresh = () => ({ deny: 'socket gone' })
  expect((await wbCmd($, 'refresh')).text).toContain('沒有連上')
  for (const args of ['', 'rebuild', 'refresh now']) expect((await wbCmd($, args)).text).toContain('用法')
  await w.clock.settle()
  expect(nextReqs(w)).toHaveLength(0)
  expect(refreshReqs(w)).toHaveLength(4)
})

test('the /workbook command is registered once the reporter is on', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  expect(w.registered).toContain('workbook')
})

// A fork cannot be cut ($.model.fork takes no signal): after its deadline it keeps spending the whole conversation's tokens,
// so no other model call starts until it settles; then the executor asks again. Its late answer is not reported.
// Mutation gate: let the loop go on while the fork runs → red (codex attack).
test('a fork that outlived its deadline holds the executor until it settles; its late answer is dropped', async ($, on) => {
  let release: (v: any) => void = () => {}
  const w = evWorld(on, {
    wbNext: (_b, n) => (n === 1 ? jobAnswer(REFRESH_JOB({ fork: { prompt: 'p', timeout_ms: 90000 } })) : n === 2 ? jobAnswer(JOB({ id: 'wbj-t2' })) : { status: 204 }),
    wbResult: () => ({ status: 200, text: '{"more":true}' }), // the daemon says another job is ready
    fork: () => new Promise((resolve) => { release = resolve }),
  })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  await w.clock.advance(95_100) // the deadline: reported aborted
  expect(resultReqs(w).map((r) => r.body.job_id)).toEqual(['wbj-r1'])
  await w.clock.settle()
  expect(nextReqs(w)).toHaveLength(1) // not asked again while the fork runs
  expect(w.modelCalls.length).toBe(0)
  release(FORK_OK) // it finally settles
  await w.clock.settle()
  expect(resultReqs(w).filter((r) => r.body.job_id === 'wbj-r1')).toHaveLength(1) // the late answer is not reported
  expect(nextReqs(w).length).toBeGreaterThanOrEqual(2) // asked again now
  expect(w.modelCalls.length).toBe(1)
  expect(resultReqs(w).map((r) => r.body.job_id)).toContain('wbj-t2')
})

// While a fork runs past its deadline /workbook refresh queues nothing and says why; a fork that never settles is given up
// after 10 minutes and the executor works again. Mutation gate: no cap → red; queue anyway → red (codex critic).
test('an orphaned fork: /workbook refresh is refused with a reason; after 10 minutes the hold is given up', async ($, on) => {
  const w = evWorld(on, {
    wbNext: (_b, n) => (n === 1 ? jobAnswer(REFRESH_JOB({ fork: { prompt: 'p', timeout_ms: 90000 } })) : n === 2 ? jobAnswer(JOB({ id: 'wbj-t2' })) : { status: 204 }),
    wbResult: () => ({ status: 200, text: '{"more":true}' }),
    fork: () => new Promise(() => {}), // never settles
  })
  await start($, w)
  await turnStart($, 't1')
  await turnDone($, 't1')
  await w.clock.settle()
  await w.clock.advance(95_100)
  expect((await wbCmd($, 'refresh')).text).toContain('還在背景執行')
  expect(refreshReqs(w)).toHaveLength(0)
  await w.clock.advance(600_000)
  await w.clock.settle()
  expect(w.modelCalls.length).toBe(1) // the executor works again: the second job ran
  expect((await wbCmd($, 'refresh')).text).toContain('已排入')
})

// ---- U3-0b: the Apps' send and interrupt (interface U3 plan D7) ----

const PJ = '0'.repeat(32)
const PJOB = (extra: any = {}) => ({ id: 'pj-' + PJ, kind: 'submit', session_id: SID1, text: 'hello from the app', ...extra })
const nextOnce = (job: any) => (_b: any, n: number): Answer | Promise<Answer> => (n === 1 ? { status: 200, text: JSON.stringify({ job }) } : never())
// gated: the job is held back until open() is called (so a test can start a turn first), then handed once.
const gated = (job: any) => {
  let open: () => void = () => {}
  const door = new Promise<void>((r) => { open = r })
  return { open, fn: (_b: any, n: number): Answer | Promise<Answer> => (n === 1 ? door.then(() => ({ status: 200, text: JSON.stringify({ job }) } as Answer)) : never()) }
}
const pqNext = (w: W) => w.pqReqs.filter((r) => r.url.endsWith('/next'))
const pqResult = (w: W) => w.pqReqs.filter((r) => r.url.endsWith('/result'))

// Mutation gate: poll before the reporter is on, or poll for another session id → red.
test('the standing poll asks prompt/next for this session, once, and waits there', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await w.clock.advance(1000)
  expect(pqNext(w)).toHaveLength(1)
  expect(pqNext(w)[0].body).toEqual({ stream: expect.stringMatching(/^[A-Za-z0-9_-]{22}$/), session_id: SID1, wait_ms: 15000 })
})

// Mutation gate: send it framed (no asUser), or report accepted without calling → red.
test('a submit job is run as the person\'s own words and reported accepted; then the poll goes on', async ($, on) => {
  const w = evWorld(on, { promptNext: nextOnce(PJOB()) })
  await start($, w)
  await w.clock.settle()
  expect(w.submitCalls).toHaveLength(1)
  expect(w.submitCalls[0]).toMatchObject({ text: 'hello from the app', origin: { kind: 'plugin', asUser: true } })
  expect(pqResult(w)[0].body).toEqual({ stream: pqNext(w)[0].body.stream, job_id: 'pj-' + PJ, status: 'accepted' })
  expect(pqNext(w).length).toBeGreaterThanOrEqual(2) // asked again after the result
})

// Mutation gate: call $.prompt.submit mid-turn (it would block until idle) → red.
test('while a turn runs a submit is reported busy at once and nothing is submitted', async ($, on) => {
  const g = gated(PJOB())
  const w = evWorld(on, { promptNext: g.fn })
  await start($, w)
  await turnStart($, 't1') // a turn is running
  g.open()
  await w.clock.settle()
  expect(w.submitCalls).toHaveLength(0)
  expect(pqResult(w)[0].body).toMatchObject({ job_id: 'pj-' + PJ, status: 'busy' })
})

// Mutation gate: skip the session comparison → red.
test('a submit made for another session id (a /clear since) is dropped session_changed', async ($, on) => {
  const w = evWorld(on, { promptNext: nextOnce(PJOB({ session_id: SID2 })) })
  await start($, w)
  await w.clock.settle()
  expect(w.submitCalls).toHaveLength(0)
  expect(pqResult(w)[0].body).toMatchObject({ status: 'dropped', reason: 'session_changed' })
})

test('a prompt Claude Code drops is reported dropped with its reason', async ($, on) => {
  const w = evWorld(on, { promptNext: nextOnce(PJOB()), submit: () => ({ drop: 'blocked by a hook' }) })
  await start($, w)
  await w.clock.settle()
  expect(pqResult(w)[0].body).toMatchObject({ status: 'dropped', reason: 'blocked by a hook' })
})

test('a submit the engine refuses is reported dropped refused', async ($, on) => {
  const w = evWorld(on, { promptNext: nextOnce(PJOB()), submit: () => { throw new Error('engine says no') } })
  await start($, w)
  await w.clock.settle()
  expect(pqResult(w)[0].body).toMatchObject({ status: 'dropped', reason: 'refused' })
})

test('an interrupt aborts the running main turn', async ($, on) => {
  const g = gated(PJOB({ kind: 'interrupt', text: undefined }))
  const w = evWorld(on, { promptNext: g.fn })
  await start($, w)
  await turnStart($, 't-run')
  g.open()
  await w.clock.settle()
  expect(w.abortCalls).toHaveLength(1)
  expect(w.abortCalls[0]).toMatchObject({ turnId: 't-run' })
  expect(pqResult(w)[0].body).toMatchObject({ status: 'accepted' })
})

test('an interrupt with no turn running is dropped not_running and aborts nothing', async ($, on) => {
  const w = evWorld(on, { promptNext: nextOnce(PJOB({ kind: 'interrupt', text: undefined })) })
  await start($, w)
  await w.clock.settle()
  expect(w.abortCalls).toHaveLength(0)
  expect(pqResult(w)[0].body).toMatchObject({ status: 'dropped', reason: 'not_running' })
})

test('an abort the engine refuses (the turn ended meanwhile) is dropped not_running', async ($, on) => {
  const g = gated(PJOB({ kind: 'interrupt', text: undefined }))
  const w = evWorld(on, { promptNext: g.fn, abort: () => ({ deny: 'no turn is running' }) })
  await start($, w)
  await turnStart($, 't-run')
  g.open()
  await w.clock.settle()
  expect(pqResult(w)[0].body).toMatchObject({ status: 'dropped', reason: 'not_running' })
})

// Mutation gate: run a job outside the bounds → red (fail closed).
for (const [name, job] of [
  ['a bad job id', PJOB({ id: 'x' })],
  ['a bad session id', PJOB({ session_id: 'nope' })],
  ['an unknown kind', PJOB({ kind: 'steer' })],
  ['blank text', PJOB({ text: '   ' })],
  ['over-long text', PJOB({ text: 'x'.repeat(8001) })],
  ['text that is not a string', PJOB({ text: 5 })],
] as [string, any][]) {
  test(`a job with ${name} is not run and not reported`, async ($, on) => {
    const w = evWorld(on, { promptNext: nextOnce(job) })
    await start($, w)
    await w.clock.advance(1100)
    await w.clock.settle()
    expect(w.submitCalls).toHaveLength(0)
    expect(w.abortCalls).toHaveLength(0)
    expect(pqResult(w)).toHaveLength(0)
  })
}

// Mutation gate: no pause after a failed or instant-empty poll → a busy loop → red.
test('a failed poll backs off, and an instant empty answer waits before the next poll', async ($, on) => {
  const w = evWorld(on, { promptNext: (_b, n) => (n === 1 ? { status: 500, text: '{}' } : n === 2 ? { status: 204 } : never()) })
  await start($, w)
  await w.clock.settle()
  expect(pqNext(w)).toHaveLength(1) // the failure: backing off
  await w.clock.advance(2100)
  expect(pqNext(w)).toHaveLength(2)
  await w.clock.advance(500)
  expect(pqNext(w)).toHaveLength(2) // the instant 204: waiting a second
  await w.clock.advance(700)
  expect(pqNext(w)).toHaveLength(3)
})

// Mutation gate: keep polling for the old session id after a /clear → red.
test('after a /clear the poll is for the new session id', async ($, on) => {
  const w = evWorld(on)
  await start($, w)
  await w.clock.advance(1000)
  expect(pqNext(w)[0].body.session_id).toBe(SID1)
  await end($, 'clear', SID1)
  w.sid = SID2
  await $.classic.SessionStart({ source: 'clear' })
  await w.clock.advance(1000)
  const last = pqNext(w)[pqNext(w).length - 1]
  expect(last.body.session_id).toBe(SID2)
})

// session.end{clear} has been seen but the switch not yet: $.session.id() still says the old id, yet the conversation is
// ending - a job that arrives then is dropped, never run (codex R1). Mutation gate: drop the ev.switching test → red.
test('a job that arrives between session.end{clear} and the switch is dropped session_changed', async ($, on) => {
  const g = gated(PJOB())
  const w = evWorld(on, { promptNext: g.fn })
  await start($, w)
  await end($, 'clear', SID1) // the switch is pending: ev.switching
  g.open()
  await w.clock.settle()
  expect(w.submitCalls).toHaveLength(0)
  expect(pqResult(w)[0].body).toMatchObject({ status: 'dropped', reason: 'session_changed' })
})

// Every report POST hangs: the tries are bounded in time (2 s each, none started after 6.5 s from the job's arrival) so the
// last one still reaches the daemon inside its 10 s lease and the poll loop is back soon (codex critic). Mutation gate: the
// old 5 s per try and no budget → the third try starts after the lease / the loop is held ~17 s → red.
test('a hanging prompt result is tried within the lease and the poll loop resumes', async ($, on) => {
  const w = evWorld(on, { promptNext: (_b, n) => (n === 1 ? { status: 200, text: JSON.stringify({ job: PJOB() }) } : never()), promptResult: () => never() })
  await start($, w)
  await w.clock.settle()
  await w.clock.advance(6600) // 2 s + 0.7 s + 2 s + 1.4 s: the third try starts at ~6.1 s
  expect(pqResult(w)).toHaveLength(3)
  await w.clock.advance(2200) // its 2 s deadline passed
  expect(pqNext(w).length).toBeGreaterThanOrEqual(2) // back to polling, well inside 9 s
})

// A report that gets no 200 (a lost answer, a busy daemon) is sent again inside the daemon's hand timeout; a 409 (settled
// already) is final and not repeated; after three tries it gives up (codex attack). Mutation gate: a single try → red.
test('a prompt result that fails is sent again, bounded; a 409 is not retried', async ($, on) => {
  const w = evWorld(on, { promptNext: nextOnce(PJOB()), promptResult: (_b, n) => (n === 1 ? { status: 503, text: '{}' } : n === 2 ? { deny: 'socket gone' } : { status: 200, text: '{}' }) })
  await start($, w)
  await w.clock.advance(5000)
  expect(pqResult(w)).toHaveLength(3)
  expect(new Set(pqResult(w).map((r) => JSON.stringify(r.body))).size).toBe(1) // the same report each time
})

test('a 409 on the report ends the retries', async ($, on) => {
  const w = evWorld(on, { promptNext: nextOnce(PJOB()), promptResult: () => ({ status: 409, text: '{"error":"not_leased"}' }) })
  await start($, w)
  await w.clock.advance(5000)
  expect(pqResult(w)).toHaveLength(1)
})

test('a report that never succeeds is tried three times and no more', async ($, on) => {
  const w = evWorld(on, { promptNext: nextOnce(PJOB()), promptResult: () => ({ status: 500, text: '{}' }) })
  await start($, w)
  await w.clock.advance(10_000)
  expect(pqResult(w)).toHaveLength(3)
})
