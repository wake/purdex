# Phone push: workers (PW-2) and relay through the dev host — spec (draft 2026-10-11)

Extends `docs/specs/2026-10-09-push-spec.md` (push v1) and the #2142 design note. Two parts that ship independently:
**Part A** worker (Nexen execution) push on the host the worker runs on; **Part B** hosts without the APNs key relay their
pushes through the dev host.

## 1. User decisions (2026-10-11, do not reopen)
- D1 Worker push follows **exactly the terminal rules**: push "needs you", done and failed; only for worker tabs open on that
  phone (or with "notify without a tab" on); the body is what the terminal's would be (done → the last reply, else 「任務完成」;
  failed → the reason); the app-icon badge counts workers with the same per-session rule; no push while a Mac shows the tab (R6).
  This replaces #2142 R-6 (fixed body 「任務完成」) and R-7's "badge excludes workers".
- D2 The APNs key is **not** copied to other hosts ("每多一顆 daemon 就要多佈署一次金鑰，不安全"). Other hosts' pushes are sent
  **through the dev host**, and the App's dev-host setting decides which host that is.

## 2. Facts this relies on (measured on main, 2026-10-11)
- PW-1 (#2521): `internal/module/nex/worker_notify.go` publishes `WorkerNotifyEvent{ExecID, Status waiting|idle|error, DedupKey,
  RequestID, ToolName, Title, Brief, Provider, Reason, TurnCount, Stamp}` on transitions only, registry key
  `nex.worker-notify-feed`, non-blocking per-subscriber channel (64), strings clipped to 256 runes, not masked.
- Push gate `internal/push/gate.go`: `EventStage` (fresh by BroadcastTs, status, silent, Notification+idle, R6 `ShowsCode`),
  `DeviceWants` (prefs per agent type, `Tabs` membership or `NotifyWithoutTab`), `SendStage` (error debounce). Content
  `internal/push/agent_content.go` (Stop → `last_assistant_message` else 「任務完成」; StopFailure → `error_details` / `error`
  else 「任務異常中斷」). Payload `content.go` (`purdex{host_id, kind, session_code, …, open_approval_keys}`).
- A worker tab's session code is `exec-<executionId>` on the Mac (`spa/src/lib/nex/worker-agent-status.ts`); it cannot collide
  with a tmux code.
- The nex row/digest carries no assistant text; the Mac's done body comes from the SPA live stream (`lastAssistantText`).
- Phones register with **each** host (`POST /api/push/devices`, token + prefs + tabs per host). The push module mounts only when
  `[push] apns_dir` is set (`cmd/pdx/main.go`), so a host without the key has no `/api/push/devices` today.
- Host-to-host: `peers.HostCaller.Call` (Bearer = the entry's Token, `to_host_id` in the body); inbound `/api/peers/*` is
  `PeerAuth` + `HostRoutePolicy` allow-list; per-route authorisation in the handler (e.g. `AllowTeam`). Precedent for a host
  pushing to another: team facts (`POST /api/peers/team/facts`, outbox, idempotent ids, per-host rate limit, 64 KiB body).
- The daemon has no notion of a dev host; only the SPA (`useHostStore.devHostId`).

## 3. Part A — worker push (PW-2)
A1 **Source**: the push module subscribes to `nex.worker-notify-feed` when present (soft dependency: not in `Dependencies()`;
   absent key → log once, skip).
A2 **Mapping to the terminal's events** (so `DeviceWants`, content and prefs are reused as is):
   | worker status | event name | content |
   |---|---|---|
   | waiting with ToolName | `PermissionRequest` | 「需要授權：{tool}」 |
   | waiting without ToolName (a question / handoff ask) | `Notification` (notification_type permission_prompt) | 「需要授權核准」 / title |
   | idle (after running/waiting) | `Stop` | last assistant reply (A4) else 「任務完成」 |
   | error | `StopFailure` | `Reason`, normalised and masked, else 「任務異常中斷」 |
   AgentType = the provider's agent type (`claude` → the Claude Code type key the prefs use); SessionCode = `exec-<ExecID>`;
   the session name in the title = the worker's `Title`, else `exec-<first 6>`.
A3 **Gate**: rule 0 "fresh" uses `DedupKey` (in-memory LRU, 1000 keys) instead of `BroadcastTs`; rules 1–3 as today; R6 uses
   `ShowsCode("exec-<id>")` (needs A6); rules 5–7 unchanged (error debounce keyed by session code + event + reason).
A4 **Done body**: read the last top-level assistant text of the worker's last turn from Nexen, asynchronously, outside the
   projector's read slot, ≤2 s, cut to 300 runes; on any failure fall back to 「任務完成」. The plan measures which Nexen read gives
   it cheapest (events tail vs transcript) and must not block the feed.
A5 **Payload**: `kind:"agent"` (unchanged, so older iOS builds still show it), `session_code:"exec-<id>"`, new
   `purdex.execution_id`, `collapse-id: agent-exec-<id>`. **Badge**: `open_approval_keys` gains `s:exec-<id>` for every worker
   currently in `waiting` on this host (the push module keeps the last status per exec from the feed); the phone dedupes by session
   key as today (one session = 1).
A6 **Mac presence (SPA)**: the presence reporter includes `exec-<id>` codes of worker tabs being shown, so R6 covers workers.
A7 **iOS (contract)**: include worker tabs' `exec-<id>` codes in `prefs.tabs`; tap → open/switch to that worker tab on that host
   (`host_id` + `execution_id`); per-agent prefs apply to workers by their agent type.
A8 Known gap kept (#2142/#2143): a very fast turn or the first turn after a restart may miss its done push (no `turn_count`
   relaxation).

## 4. Part B — relay through the dev host
Design: **the dev host is a signing proxy.** Each host keeps doing everything it does today for its own sessions — device
registrations (tokens, prefs, tabs), the gate, R6 presence, content, badge keys — and only the final APNs request goes to the
dev host, which signs it with the only key and sends it. The dev host keeps no state about other hosts' devices.

B1 **Which host relays**: the App's dev-host setting. When it is set or changed, the App sends to every host it manages
   `PUT /api/push/relay {relay_host_id}` (admin / App principal; `""` = none), persisted in hostconfig. A host with its own key
   sends directly and ignores the relay setting; a host is never its own relay.
B2 **Keyless push module**: the push module mounts when `[push] apns_dir` is set **or** a relay is configured. Keyless, it serves
   `/api/push/devices` and presence as today and swaps the APNs sender for a relay sender. `/api/info` `push` gains
   `{mode: "direct"|"relay"|"off", relay_host_id?, relay_ok}`; `push.v1` is advertised only when mode ≠ off and (direct ready or the
   relay is reachable and allowed), so the phone registers only where a push can actually go out.
B3 **Relay endpoint on the dev host**: `POST /api/peers/push/send` (added to `HostRoutePolicy`), host principal only, the
   caller's `PeerHost` entry must have **`allow_push_relay = true`** (new, default false). Body ≤ 64 KiB:
   `{to_host_id, id (UUIDv4, idempotent for 10 min), jobs: [{token, env: sandbox|production, payload (JSON ≤ 4 KiB), collapse_id?,
   push_type: alert|background, priority: 10|5, expiration?}]}` (≤ 32 jobs). The dev host checks: topic = its own bundle id (never
   from the caller), env is one it can sign for, payload size, token shape (hex 64–200), per-caller rate limit (e.g. 60 jobs/min,
   burst 20) → 429; sends through its normal sender; returns per job `{status, apns_id?, reason?}` mirroring the APNs answer
   (410 Unregistered → the caller deletes that device, as the local sender does).
B4 **Relay sender (caller side)**: same queue and retry rules as the local sender (spec v1 §7), transport = HostCaller; a relay
   that is unreachable / refuses (403 not allowed, 404 no endpoint) → jobs dropped and counted, `relay_ok=false` in `/api/info`,
   retried per §7 only for transient errors. No outbox persistence (v1, same as local: a push is not worth replaying late).
B5 **Permission on the dev host**: `allow_push_relay` is set per paired host on the dev host (Hosts page, next to "Allow team"),
   default off; when the user sets a dev host, the App offers to switch it on for the paired hosts it lists (one confirmation).
   *(Open question Q1 for the user.)*
B6 **Payload origin**: the caller builds the payload, so `purdex.host_id`, `session_code`, badge keys and the title are the source
   host's — the phone attributes the push to the right host and sums badges per host as today.
B7 **Security notes**: the relay never learns device prefs or tabs; it can only be asked to send a ≤4 KiB payload to a token for
   its own topic, by a host the user allowed, rate-limited. A compromised allowed peer could spam that phone's notifications —
   same trust level as AllowTeam; revoking = switching `allow_push_relay` off or unpairing.

## 5. Delivery (PRs, each ≤ 800 lines incl. tests)
- PW-2a daemon: feed subscription, mapping, DedupKey freshness, worker waiting set → badge keys, payload `execution_id`; done body
  = 「任務完成」 placeholder behind A4's interface.
- PW-2b daemon: A4 last-reply reader (measure first).
- PW-2c SPA: presence includes `exec-` codes (A6).
- PW-2d iOS: tabs include `exec-` codes, tap-to-open workers, badge keys (contract A7).
- PR-1 daemon: `allow_push_relay` in PeerHost + config/API + Hosts page field; relay endpoint B3 with checks and rate limit.
- PR-2 daemon: keyless module mount, relay sender B4, `/api/info` push mode, `PUT /api/push/relay` (B1/B2).
- PR-3 SPA: dev-host setting → `PUT /api/push/relay` to every host; Hosts page switch; the confirmation of B5.
- Order: PW-2a → PW-2c → PW-2b; PR-1 → PR-2 → PR-3; iOS PW-2d after PW-2a merges. Deploy gate: PR-2 needs PR-1 on the dev
  host first (a relay to a host without the endpoint just fails closed).

## 6. Acceptance
- A worker on mlab: needs-you, done (with the last reply) and failed reach the phone only when its tab is open on the phone; not
  while the Mac shows it; the badge counts a waiting worker once.
- A Claude Code session on air26 (no key): its done push arrives via mlab with air26's host label; with `allow_push_relay` off on
  mlab nothing is sent and air26's `/api/info` shows `relay_ok=false`; switching the dev host in the App moves the relay.

## 7. Open questions
- Q1 (user): the relay permission on the dev host — explicit per-host switch, default off, offered once when the dev host is set
  (recommended, same safety level as "Allow team"), or automatic for every paired host?
- Q2 (plan): which Nexen read gives the last assistant text cheapest (A4).
