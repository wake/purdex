// Purdex mod (P5b-1): says hello to the daemon at an interactive session.start
// and again after every /clear (the new conversation has a new session id,
// M1, and the daemon keys mod presence by it — spec §8.3, P8a-1d).
// The relay itself lands in P5b-2/P5b-3 (spec §8.7).

const VERSION = '1' // the mod ↔ daemon protocol version `pdx relay hello --version` reports
const CALL_TIMEOUT_MS = 35_000 // one daemonclient grace (30 s) plus slack

// config: the installing daemon's config file (pdx.json "config"); '' lets
// pdx fall back to its default one.
const s = { interactive: false, pdx: 'pdx', config: '' }

function parseJSON(text) {
  try { return JSON.parse(text) } catch { return undefined }
}

async function run($, argv, timeoutMs) {
  try {
    return await $.process.run([s.pdx, ...argv], { timeoutMs })
  } catch (err) {
    return { exitCode: 20, stdout: '', stderr: String(err) }
  }
}

// relay runs `pdx relay <args>` against the daemon that installed the mod:
// with a config in pdx.json every call carries `--config <path>`, so a
// second daemon on this machine (another data dir) is never the one asked.
function relay($, args, timeoutMs) {
  return run($, ['relay', ...args, ...(s.config ? ['--config', s.config] : [])], timeoutMs)
}

async function hello($) {
  const sid = await $.session.id()
  await relay($, ['hello', '--session', sid, '--version', VERSION, '--agent', 'cc'], CALL_TIMEOUT_MS)
}

// helloLater sends hello from a timer, never inside the hook: a daemon that
// is down or restarting answers only after the client's 30 s grace, and a
// session start or a /clear must not wait for that.
function helloLater($) {
  $.clock.after(0, () => { void hello($).catch(() => {}) })
}

export function register(on) {
  on('session.start', async ($, e, next) => {
    s.interactive = !!e.isInteractive
    if (!s.interactive) return next(e) // a Nexen worker's `claude -p`: the mod does nothing (spec §5)
    const cfg = parseJSON(await $.fs.read($.plugin.root + '/pdx.json').catch(() => ''))
    if (cfg && cfg.pdx) s.pdx = cfg.pdx // written beside VERSION by the extractor; absent in `claude plugin test`
    s.config = cfg && typeof cfg.config === 'string' ? cfg.config : ''
    helloLater($)
    return next(e)
  })

  // /clear gives the conversation a new session id (M1): say hello again
  // under it, or the daemon's presence record (and P8a-1d's terminal-only
  // backstop) would still name the old one. Not for startup / resume (that
  // is session.start's hello) and never when headless.
  on('classic.SessionStart', async ($, e, next) => {
    const r = await next(e)
    if (s.interactive && e.source === 'clear') helloLater($)
    return r
  })
}
