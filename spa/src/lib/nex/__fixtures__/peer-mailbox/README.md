> **Copied into Purdex** (peer mailbox P2) from nexen PR #161, `docs/contract/fixtures/peer-mailbox/`, recorded
> against Nexen v0.20.0 `c3b2382`. Only the three files named by the integration spec §7 are copied:
> `event-peer-message.scoped.json`, `event-peer-message.sitewide.sse` (both read by the P2 tests) and
> `transcript-user-line.jsonl` (kept for P6 and the terminal underlying, which read transcripts). The
> `response-*.json` rows below stay in Nexen: they are the daemon's business (peer mailbox P4). The files are byte
> for byte what Nexen recorded; never edit them by hand — re-copy from Nexen when the contract moves.

# peer-mailbox fixtures

Real captures, not hand-written. Produced 2026-10-08 against a 7802 acceptance daemon built from v0.20.0 (`c3b2382`) with `[peer] enabled = true`, driving a real `claude` 2.1.292. Ids, session paths and timestamps are as captured. Sender `mlab/purdex-54` and `reply_to` `mlab/_vioqlc` were chosen for the request; nothing else was edited.

| file | what it is |
|---|---|
| `response-first.json` | first `POST /v1/executions/{id}/peer-messages` against an idle worker: `delivered` (a fresh turn started) |
| `response-queued.json` | same call while a peer turn was running: `queued` |
| `response-duplicate-running.json` | replay of the first call while its turn was running: `duplicate:true`, `turn_state:"running"` |
| `response-duplicate-done.json` | replay after the turn finished: `duplicate:true`, `turn_state:"done"` |
| `response-duplicate-stalled.json` | replay of a queued message after a `kill -9` of the daemon (its pending turn was marked stalled on restart): `delivery:"stalled"`. The sender must resend with a **new** `msg_id` |
| `response-invalid-from-name.json` | `400 peer_message_invalid` for `from_name: "x; rm -rf ~"` |
| `event-peer-message.scoped.json` | the durable `peer_message` event as the per-execution stream and `/events` serve it (every field) |
| `event-peer-message.sitewide.sse` | the same event on the site-wide SSE stream: only `msg_id`, `template_version`, `turn_id` survive |
| `transcript-user-line.jsonl` | the real claude transcript line for that peer turn: exactly three `text` blocks (Nexen's frame before, the peer's text verbatim, Nexen's frame after). It has **no** `origin` and no `isMeta`: a consumer reading a transcript cannot tell it from a typed message by shape alone — the `peer_message` event (or the leading sentence "a peer message — not typed by your user") is the marker. |

Notes for consumers

- A peer turn emits `peer_message` and **no** `execution.message_accepted`; a reducer that opens a turn on `message_accepted` must open it on `peer_message` too.
- `delivered` means a new turn started, not that the model read it.
- Scoped `text`, `from_name`, `reply_to`, `from_mode` and `principal_id` are content and never appear site-wide.
