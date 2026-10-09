// Records /ws/conversations/claude/<session_id> frames to an NDJSON file until it is killed (SIGTERM) or the optional
// max-seconds elapse. One line per frame: {t, type, seq, value}; {t, closed:1, code} when the socket ends.
//
//   node rec-conv.mjs <out.ndjson> <session_id> [max-seconds] [after-cursor]
//
// The token is read from ~/.config/pdx/config.toml into a variable and used only for the one-time ticket request
// (POST /api/ws-ticket); it is never printed or put on a command line. PDX_BASE overrides http://100.64.0.2:7860.
import fs from 'node:fs'

const [outPath, sessionId, maxSec, after] = process.argv.slice(2)
if (!outPath || !sessionId) {
  console.error('usage: node rec-conv.mjs <out.ndjson> <session_id> [max-seconds] [after-cursor]')
  process.exit(2)
}
const cfg = fs.readFileSync(process.env.HOME + '/.config/pdx/config.toml', 'utf8')
const tok = cfg.split('\n').find(l => /^token\s*=/.test(l)).match(/"([^"]+)"/)[1]
const base = process.env.PDX_BASE || 'http://100.64.0.2:7860'
const out = fs.createWriteStream(outPath, { flags: 'a' })
const w = o => out.write(JSON.stringify({ t: Date.now(), ...o }) + '\n')

const r = await fetch(base + '/api/ws-ticket', { method: 'POST', headers: { Authorization: 'Bearer ' + tok } })
const { ticket } = await r.json()
let url = base.replace(/^http/, 'ws') + '/ws/conversations/claude/' + sessionId + '?ticket=' + ticket
if (after) url += '&after=' + encodeURIComponent(after)
const ws = new WebSocket(url)
const finish = () => { out.end(() => process.exit(0)) }
ws.onmessage = e => {
  try { w(JSON.parse(e.data)) } catch { /* not JSON */ }
}
ws.onclose = e => { w({ closed: 1, code: e.code }); finish() }
ws.onerror = () => { w({ error: 1 }) }
process.on('SIGTERM', () => { try { ws.close() } catch {} ; setTimeout(finish, 200) })
if (Number(maxSec) > 0) setTimeout(() => { try { ws.close() } catch {} }, Number(maxSec) * 1000)
