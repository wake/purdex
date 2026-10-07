// Purdex mod (P5b-1): says hello to the daemon at an interactive session.start
// and again after every /clear (the new conversation has a new session id,
// M1, and the daemon keys mod presence by it — spec §8.3, P8a-1d).
// The relay itself lands in P5b-2/P5b-3 (spec §8.7).

const VERSION = '1' // the mod ↔ daemon protocol version `pdx relay hello --version` reports
const CALL_TIMEOUT_MS = 35_000 // one daemonclient grace (30 s) plus slack

const s = { interactive: false, pdx: 'pdx' }

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

async function hello($) {
  const sid = await $.session.id()
  await run($, ['relay', 'hello', '--session', sid, '--version', VERSION, '--agent', 'cc'], CALL_TIMEOUT_MS)
}

export function register(on) {
  on('session.start', async ($, e, next) => {
    s.interactive = !!e.isInteractive
    if (!s.interactive) return next(e) // a Nexen worker's `claude -p`: the mod does nothing (spec §5)
    const cfg = parseJSON(await $.fs.read($.plugin.root + '/pdx.json').catch(() => ''))
    if (cfg && cfg.pdx) s.pdx = cfg.pdx // written beside VERSION by the extractor; absent in `claude plugin test`
    await hello($)
    return next(e)
  })

  // /clear gives the conversation a new session id (M1): say hello again
  // under it, or the daemon's presence record (and P8a-1d's terminal-only
  // backstop) would still name the old one. Not for startup / resume (that
  // is session.start's hello) and never when headless.
  on('classic.SessionStart', async ($, e, next) => {
    const r = await next(e)
    if (s.interactive && e.source === 'clear') await hello($)
    return r
  })
}
