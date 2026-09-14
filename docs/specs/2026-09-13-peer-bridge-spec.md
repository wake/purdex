# Spec — Cross-host agent peer bridge ("Peer Bridge")

Status: draft v3.2 (R1 `task-mtzzzf2x-kd6vvt`: 2 Blockers, 10 Majors; R2
`task-mu00ba1c-7564mi`: 1 Blocker, 6 Majors, 4 Minors — all accepted; §8/§9
hold both disposition tables). v3.2: §4.3/§4.4 note that a delivery from a
host with no verified entry is refused at authentication, so "one-way" means
verified inbound identity without an outbound `token` (P3 plan D12).
Date: 2026-09-14
Branch: `worktree-peer-bridge`

> **v2 narrowed delivery to Claude Code only and moved authorization to the
> daemon's single auth entry. v3 fixes what v2 left open:** credentials are
> per inbound host, never shared (§4.3); the peer routes are locked down in
> P1 rather than P2 (§4.6, §5); helpers are keyed by the full process
> generation (§4.5); remote name resolution has an actual protocol step
> (§4.4); loop protection claims only what a daemon can enforce (§4.4).

## 1. Problem

Claude Code 2.1.224+ lets one session message another (`ListAgents` /
`SendMessage`). On one machine this works over a per-session Unix socket. The
only cross-machine path is Remote Control: it goes through Anthropic servers,
lists only sessions that are themselves connected to Remote Control, hides
machine-generated session names, and excludes every non-Claude agent. Its
discovery logic is compiled into the binary and has no extension point.

The user now runs agents on two machines (`mlab`, a new `air-2026`) in the same
tailnet, mixes Claude Code with Codex and OpenCode, and already drives all of
them through Purdex. Today, asking "what peers are running on my Purdex hosts"
has no answer, and a session on one host cannot hand a finding to a session on
the other without the user copy-pasting.

Purdex already holds most of the required state per host:

| Asset | Where | What it gives |
|---|---|---|
| tmux session list | `GET /api/sessions` (`internal/module/session/provider.go:21`) | session code, tmux name, cwd, running command, `tmux_instance` generation |
| Agent owner per session | `resolveSessionOwner` (`internal/module/agent/provenance_handler.go:93`), served as `GET /api/sessions/{code}/provenance` | `agent_type`, agent `session_id`, `cwd`, `tmux_pane_id` |
| Agent hooks | `pdx hook` (`cmd/pdx/hook.go`) | runs inside every cc/codex/opencode session; already resolves tmux pane + sender pid |
| Host identity | `host_id` in `~/.config/pdx/config.toml` | stable per-host id |
| Auth entry | `cmd/pdx/main.go:195-205`: CORS → IPWhitelist → PairingGuard → `TokenAuth` → module mux | one admin bearer token for every route |
| Config persistence | `internal/core/config_handler.go` (`config.WriteFile` under `CfgMu`, `NotifyConfigChange`) | the only supported way to change a running daemon's config |

What is missing: a peer-shaped view of that state, knowledge of the other
hosts at daemon level, a credential narrower than the admin token, and a
delivery adapter into Claude Code.

## 2. Goals / non-goals

### Goals

1. `pdx peers` lists every agent session on the local host with a stable
   address, its agent kind, its Claude Code peer name when it has one, whether
   it is busy, and whether it can receive a peer message.
2. `pdx peers --all` does the same across every configured host in one table,
   with per-host failures reported inline.
3. `pdx msg send <host>/<session> "<text>"`, run inside a Claude Code session
   on host A, delivers a message into a Claude Code session on host B. It
   arrives as a native peer message with a reply address; the receiving
   Claude answers with its own `SendMessage` and never learns Purdex is in the
   path.
4. Every cross-host call is authenticated with a credential that identifies
   the calling host and grants nothing beyond peer listing and delivery. Every
   delivery attempt is written to an audit log before the socket write.

### Non-goals

- Replacing or wrapping Remote Control. Both coexist.
- **Any delivery into a non-Claude agent, or into a Claude Code session whose
  inbox is unavailable.** Typed input (tmux `send-keys`, stream-mode stdin)
  carries user authority and has none of the harness protections of §3.4; a
  bridge that silently downgrades to it would turn a peer-trust credential
  into a user-trust one. A typed-input lane, if ever wanted, is a separate
  spec with its own opt-in and its own trust statement. Codex / OpenCode
  sessions appear in the inventory as `deliverable: false`.
- Injecting *user-authority* input into Claude Code through the peer socket.
- Codex `app-server` integration.
- SPA UI. A "Peers" panel can consume `/api/peers` later.
- Broadcast, groups, message history browsing, read receipts, persistent
  queues, retries.
- Pre-spawning virtual peers so `ListAgents` on one host shows the other
  host's sessions before any message is exchanged.
- Windows / named pipes.

## 3. Evidence (measured on mlab, Claude Code 2.1.270, 2026-09-13)

Full protocol notes are in the user's memory
`reference_cc_peer_socket_protocol`; the spike script is
`~/.claude/notes/cc-peer-proxy-spike.mjs`. The facts this spec depends on:

### 3.1 Discovery and registry

- Each session binds `/tmp/cc-socks/<pid>.sock` (env
  `CLAUDE_CODE_MESSAGING_SOCKET`) and writes `~/.claude/sessions/<pid>.json`
  with `sessionId`, `name`, `nameSource`, `cwd`, `tmux:
  "<session>:@<win>.%<pane>"`, `messagingSocketPath`, `procStart`, `version`,
  `status: idle|busy`, plus `<pid>.<sha256>.key` (mode 0600,
  `{peerToken, procStart, pidDomain}`).
- `procStart` equals `TZ=UTC ps -p <pid> -o lstart=`.
- `-p --input-format stream-json` sessions bind a socket too; bare mode does
  not.
- `sessionId` in that file equals the `session_id` Purdex records from the
  SessionStart hook, so the join needs no heuristics. A `--resume` gives a
  new pid the same `sessionId`; the dead pid's file may linger until Claude
  Code sweeps it.

### 3.2 Wire format (one NDJSON line per message)

```
{"type":"auth","token":"<CLAUDE_CODE_MESSAGING_TOKEN>"}        # optional on macOS/Linux
{"msgV":1,"msg_id":"<uuid>","type":"user","priority":"next",
 "from":"uds:/tmp/cc-socks/<pid>.sock",
 "message":{"role":"user","content":
   "<cross-session-message from=\"uds:/tmp/cc-socks/<pid>.sock\" from-name=\"<name>\" from-mode=\"bypass|prompting\">\n<text>\n</cross-session-message>"}}
```

Observed rules:

| Test | Result |
|---|---|
| T1 plain text line | dropped silently; the frame must be `type:"user"` |
| T1 no `from` | delivered; receiving Claude reports "from=NONE", no reply address |
| T3 `from` without `uds:` prefix | delivered, but the harness attaches no reply address |
| T3b `from:"uds:<registered sock>"` | delivered; receiver replied natively with `SendMessage to: "uds:<that sock>"` |
| T4 sender self-reports `from-mode="bypass"` to a bypass receiver | delivered with no approval dialog |
| T0 prompting sender → bypass receiver | held for user approval (5 min), as documented |

Replies carry an extra `hop-chain="<hex>"` attribute the harness uses for
loop detection; a relay must carry it through unchanged.

### 3.3 Virtual peers

Any process that binds `/tmp/cc-socks/<own pid>.sock` and writes the two
registry files appears in `ListAgents`, receives native `SendMessage`
frames, and is a valid reply target (T3b). The registry is keyed by pid, so
**one process can impersonate exactly one peer**. The harness verifies that
the registered pid is alive and its `procStart` matches; it does not verify
`from-mode`. Writing these files is an ordinary same-user file write; no OS
privilege is gained, but the write boundaries in §4.5 still apply.

### 3.4 Trust class of socket-delivered messages

Whatever the frame says, the receiving harness presents it to the model as a
message from another session ("not typed by your user"), forbids it from
answering permission prompts or changing configuration, and treats slash
commands as text. This is the correct trust level for agent-to-agent traffic
and is the premise every other decision here rests on.

## 4. Design

### 4.1 Identity and addresses

Two layers, kept apart:

**Wire identity** (what daemons and audit rows carry, never abbreviated):

| Field | Meaning |
|---|---|
| `host_id` | the full configured `host_id`, e.g. `mini-lab:278cbm` |
| `agent_session_id` | the Claude Code `sessionId` |
| `pid`, `proc_start` | the process the inbox socket belongs to |

A delivery or reply is bound to this tuple. If the tuple no longer resolves
to a live socket, the daemon refuses with `target_gone`; it never re-resolves
a name to whatever now carries it.

**Human address** (what people type and listings show):

```
<host>/<session>
```

- `<host>` resolves, case-insensitively, against the local host's own alias,
  then each configured host's `alias`, then any host's full `host_id`. The
  local alias defaults to `host_id` up to the first `:`. Two hosts resolving
  to the same alias is a configuration error rejected at `host add`.
- `<session>` resolves, in order, against: tmux session name; tmux session
  code; `cc:<peer_name>` for a Claude Code session with that registry name
  (the only form that reaches a session outside tmux). A `<session>` that
  matches more than one row is rejected with `ambiguous` and the candidate
  rows; the caller retries with a code or `cc:` form.
- Resolution happens on the host that owns the session, at send time, and
  yields the wire identity above. Names are never sent as the target on the
  wire.

### 4.2 Peer record

`GET /api/peers` returns one record per tmux session, plus one per live
Claude Code registry entry that no tmux session's row consumed (its
`sessionId` matched no session's owner) and whose `tmux` field does not
point into any listed session. An entry is represented exactly once, so a
`cc:<peer_name>` address (§4.1) never has two candidates from one entry:

```jsonc
{
  "host": "mini-lab",            // alias
  "host_id": "mini-lab:278cbm",
  "address": "mini-lab/mt1",     // or "mini-lab/cc:<peer_name>" outside tmux
  "session_code": "02ybs5",      // "" outside tmux
  "session_name": "mt1",         // "" outside tmux
  "tmux_instance": "6901:1789205013",
  "cwd": "/Users/wake/Workspace/wake/purdex",
  "agent": {                     // null when no agent owns the session
    "type": "cc",                // cc | codex | opencode | proxy
    "session_id": "fa5d4c07-…",
    "peer_name": "purdex-47",    // cc/proxy only
    "pid": 76973,                // cc/proxy only
    "proc_start": "Sun Sep 13 15:22:36 2026",
    "inbox": "/tmp/cc-socks/76973.sock",
    "status": "busy",            // cc: registry status; codex/opencode: Purdex agent status; proxy: "proxy"
    "version": "2.1.270"         // cc registry `version`; "" when absent
  },
  "deliverable": true,           // cc with live inbox, not a proxy
  "reason": ""                   // why not deliverable: no_agent | not_cc | inbox_dead | proxy | ambiguous
}
```

Response envelope: `{ "host_id", "ok", "error", "partial", "peers": [...] }`.
`partial: true` means the inventory deadline expired and some rows lack an
`agent`; `ok: false` means the tmux server or registry could not be read at
all. The local host is not exempt from either.

**Which agent a row represents.** The agent module's existing owner
resolution (`resolveSessionOwner`: the most recently active root agent frame
among the session's panes, generation-checked) decides the row's `agent`. The
peers module consumes it through a small interface the agent module
registers in the service registry (`agent.owner-resolver`), not by HTTP. The
Claude Code registry is joined **only** by `sessionId`; there is no
pane-based fallback. When more than one **live** entry has that `sessionId`,
the entry whose `tmux` pane id equals the owner's `TmuxPaneID` is used if it
is the only such entry; otherwise the row is `deliverable: false, reason:
ambiguous`. (A resumed session's dead predecessor never reaches this step:
liveness has already dropped it.)

**Liveness.** A registry entry is live iff its `messagingSocketPath` exists,
`kill(pid, 0)` succeeds, and the process's start time matches `procStart`.
Stale entries are skipped, never deleted. Entries whose `.json` is unreadable
or fails schema are skipped.

**Deadline.** One inventory call has a 2 s **soft** budget: owner resolution
(the slow part — several `ps` forks per distinct pid; the existing resolver
cannot interrupt a read in flight) is started for sessions in list order,
and no new resolution starts once the budget is spent. Sessions not started
are reported with `agent: null` and `partial: true`. A single slow resolution
can therefore push a response past 2 s; P2's per-host timeout is 3 s and
treats a late response as that host's error row, not as data loss. A session
whose owner resolution failed (a tmux read error, a resolver timeout, or a
cancelled context) is reported the same way as one not started — `agent:
null`, `partial: true` — never as `no_agent`, since that would claim the
session has no agent rather than that its lookup could not be completed.

**Proxy rows.** Virtual peers (§4.5) are Claude Code registry entries too.
The daemon recognises its own helpers by pid from `proxies.json` (§4.5),
reports them as `agent.type: "proxy"`, `deliverable: false, reason: proxy`,
and excludes them from `cc:<peer_name>` resolution.

### 4.3 Host registry and fan-out (P2)

```toml
[peers]
alias = "mini-lab"            # optional; default host_id up to ':'

[[peers.hosts]]
alias         = "air"
url           = "http://100.64.0.4:7860"
host_id       = "air-2026:9k2x1p"   # learned when `token` is first verified; "" until then
token         = "pdxp_…"            # OUTBOUND: what we present to air (= air's inbound_token for us)
inbound_token = "pdxp_…"            # INBOUND: what air must present to us; minted here, unique per host
allow_bypass  = false               # §4.4 mode clamp
```

There is **no shared credential**. Each configured host has its own
`inbound_token`; when a remote call arrives, the bearer is compared
(constant-time) against every entry's `inbound_token`, and the entry that
matches *is* the authenticated host. The payload's `from.host_id` must equal
that entry's `host_id`, which must already be known (see pairing below); a
mismatch or an empty `host_id` refuses the call with `host_unverified`.

- **Pairing is two `host add`s.** On B: `POST /api/peers/hosts {alias, url}`
  mints `inbound_token`, appends the entry (`host_id: ""`), persists, and
  returns the token for the operator to carry to A. On A: `POST
  /api/peers/hosts {alias, url, token: <B's inbound_token for A>}` does the
  same for A's side and, because a `token` was supplied, immediately calls
  B's `GET /api/peers` with it; the envelope's `host_id` is stored as the
  entry's `host_id`. B's entry for A stays `host_id: ""` until B is given
  A's `inbound_token` via `PUT /api/peers/hosts/{alias} {token}`, which
  performs the same verification. Only when both entries carry a `host_id`
  can messages flow both ways; `pdx peers host list` shows the state of
  each side. `DELETE /api/peers/hosts/{alias}` removes an entry.
- **Fan-out.** `GET /api/peers?scope=all` (admin only) calls every host that
  has a `token` in parallel, 3 s per host, and returns `{ hosts: [{alias,
  host_id, ok, error?, partial?, peers}] }`, local host first. A host's
  failure is a row, not an error.
- **All writes go through the daemon** (`POST`/`PUT`/`DELETE
  /api/peers/hosts…`, admin only): mutate under `CfgMu`, persist with
  `config.WriteFile`, `NotifyConfigChange`; the CLI never edits the file, and
  the daemon reads `[peers]` from memory on every request.
- **Redaction.** A single helper redacts `peers.hosts[].token` and
  `inbound_token` (deep-copying the slice first) and is used by **every**
  config response — `GET /api/config` and the `PUT` handler's echo
  (`config_handler.go:115`) — and by the daemon log.
- **Bidirectional is the caller's job.** A delivery whose return route is
  missing is still delivered, marked one-way (§4.4) and audited as
  `no_return_route`. Note that "missing" is narrower than it looks: a
  delivery from a host B has *no verified entry* for is refused at
  authentication (`host_unverified`, above) — it never reaches the one-way
  case. One-way means B's entry for A has a verified `host_id` (inbound
  identity) but no outbound `token`, a state pairing never produces on its
  own (both `host add`s verify with a token); it exists for hand-edited
  configs (P3 plan D12).

### 4.4 Delivery (P3, Claude Code targets only)

```
pdx msg send air/foo "text"          (inside a cc session on host A)
   │ reads CLAUDE_CODE_MESSAGING_SOCKET (§ origin)
   │ POST /api/peers/send  {to, text, origin_inbox, mode?}        admin token
   ▼
A daemon: verify origin → GET B /api/peers (bearer: A's outbound token for B)
   │       → peers.Resolve(snapshot, "foo") → target tuple (§4.1)
   │ POST /api/peers/deliver  {msg_id, hop_chain?, from:{host_id, agent_session_id, pid, proc_start, peer_name, declared_mode}, to:{agent_session_id, pid, proc_start}, text}
   │        bearer: A's outbound token for B
   ▼
B daemon: authenticate (§4.3) → re-verify `to` tuple against live inventory → clamp mode → audit → helper for `from` → write frame
```

**Remote resolution.** Names are resolved by the *sender* over a fresh
inventory snapshot fetched from the target host (the same `peers.Resolve`
P1 ships), and only the resulting tuple travels. The receiver re-verifies
the tuple against its own live inventory at delivery time and refuses with
`target_gone` when the pid/proc_start no longer match. No remote "resolve"
endpoint exists.

**Origin.** `pdx msg send` never accepts a `--from`. It reads
`CLAUDE_CODE_MESSAGING_SOCKET` from its own environment — Claude Code exports
each session's own socket to Bash commands and hooks — and sends that path
as `origin_inbox`. The daemon accepts the call only if that path is a live,
non-proxy inventory row (§4.2) and fills the wire identity from the row; a
missing or unknown path is rejected with `origin_unknown`. This is
**endpoint attribution for a trusted local admin caller**, not proof of
which process ran the command: any same-UID process can read the registry
and present another session's socket path, and that is outside the boundary
this spec draws (§3.4 already limits what such a message can do). Running
the command with the variable unset or pointing at nothing live is an
error, not a degraded send.

**Authentication of `/deliver`** is §4.3: the matching `inbound_token`
names the host; `from.host_id` must equal that entry's verified `host_id`;
`allow_bypass` is read from the same entry. The empty-token bypass in the
existing `TokenAuth` never applies to `/api/peers*` (§4.6). A host with no
verified entry is therefore refused here (`host_unverified`), before any
one-way consideration: `one_way` is computed only for an authenticated
sender, from the same entry, as "no outbound `token`" (§4.3, D12).

**Mode.** B computes `effective_mode`:

| declared | `allow_bypass` on B's entry for A | effective |
|---|---|---|
| prompting | any | prompting |
| bypass | false (default) | prompting |
| bypass | true | bypass |

The wrapper's `from-mode` is `effective_mode`. Audit records both. On A,
`pdx msg send` sends `declared_mode = prompting` unless the caller passes
`--mode bypass`; A's daemon never raises it. This preserves the receiver's
approval hold by default and lets the user opt a trusted host in.

**Virtual peer selection.** B keeps, per origin wire identity
`(host_id, agent_session_id, pid, proc_start)`, at most one helper (§4.5). A
resumed origin (same `agent_session_id`, new pid) gets a new helper; the old
one is reaped when its origin is found gone, so a late reply to the old
process can never reach the new one. The frame written
to the target's inbox has `from: "uds:<helper sock>"` and a wrapper with
`from-name = "<A alias>/<origin session name or cc:peer_name>"`,
`from-mode = effective_mode`, and any `hop-chain` attribute carried in from
the origin wrapper.

**Reply path.** The target Claude replies natively to the helper's socket.
The helper hands the raw frame to B's daemon over its IPC pipe (§4.5). B
resolves the *replier* from the frame's `from` socket against its own live
inventory — an unknown socket or a socket belonging to another helper is
refused (`replier_unknown` / `proxy_to_proxy`). B then calls A's
`/api/peers/deliver` with the replier as `from` and the helper's bound origin
as `to`. If A is not configured on B, the reply is refused with
`no_return_route`, and the refusal is audited; nothing is sent back into the
target session. A, on receipt, spawns its own helper for the replier and
delivers into the origin session exactly as above. The chain is symmetric.

**Message identity, limits and loops.**

| Rule | Value |
|---|---|
| `msg_id` | UUID minted by A for a send, by B for a reply; carried end-to-end and into the inbox frame |
| Dedup | a daemon drops a `/deliver` whose `msg_id` it has seen in the last 10 min (in-memory); this catches retransmits, not conversations |
| Single hop | a daemon delivers only into its own sessions and only from a session on the authenticated host; it never forwards a `/deliver` to a third host. There is no `hop` counter because there is nothing to count |
| `hop_chain` | when the frame that reached a helper carries a `hop-chain` attribute, its value is sent as `hop_chain` and written back verbatim into the outgoing wrapper; it is Claude Code's loop marker and the bridge only carries it |
| Pair rate limit | a daemon refuses more than 30 deliveries per minute for one `(from tuple, to tuple)` pair with `rate_limited`, audited. Beyond that, conversation loops are bounded only by Claude Code's own per-sender throttles and repeat suppression — the bridge makes no stronger claim |
| Text size | UTF-8 `text` ≤ 64 KiB; refused at `/send` and `/deliver` |
| HTTP | 10 s per inter-daemon call, no retry |
| Socket write | 5 s; success means the full frame was written and the connection closed cleanly |
| Timeout after write | reported to the caller as `delivery_uncertain`; the caller does not resend |

**Audit.** Table `peer_messages` in `meta.db`: `msg_id, ts, direction
(out|in|reply), from_host_id, from_session_id, to_host_id, to_session_id,
declared_mode, effective_mode, bytes, result, error`. The row is
written **before** the socket write; if the write fails the row is updated
with the error. If the audit insert itself fails, the delivery is refused
(`audit_unavailable`). `pdx msg log [--tail N]` reads it.

### 4.5 Virtual peer helper lifecycle

`pdx peer-proxy` is a subcommand of the same binary, spawned by the daemon
with **stdin/stdout as its only channel**. It receives no token and opens no
HTTP connection.

| Concern | Rule |
|---|---|
| Key | the full origin tuple `(host_id, agent_session_id, pid, proc_start)` — helper map, `proxies.json.origin` and the bound reply target all use it |
| Spawn | on first `/deliver` for a key; a per-key mutex prevents concurrent spawns; a helper that is still starting counts toward the cap |
| Ready | helper writes `{"ready":true,"pid":N,"sock":"…","files":[…]}` on stdout only after the socket is bound **and both registry files exist**; the daemon waits ≤ 3 s, else SIGKILLs it, waits for exit, removes only the files that helper reported creating, and refuses the delivery (`proxy_spawn_failed`) |
| Registry writes | helper creates `<own pid>.json` / `<own pid>.<sha>.key` with `O_EXCL` under `~/.claude/sessions/`, key mode 0600, never follows or replaces an existing path; if the second file fails, it removes the first and exits non-zero; socket path is always `/tmp/cc-socks/<own pid>.sock` |
| Registry content | `name = "<alias>/<session>"`, `nameSource: "user"`, `kind: "interactive"`, `peerFeatures` copied from the newest live cc entry (so a future feature flag is not silently missing); a `purdex: {host_id, agent_session_id}` object is **not** written — unknown fields are not proven safe with the harness's parser |
| Ownership record | daemon writes `proxies.json` in `DataDir`: `[{pid, proc_start, sock, files:[…], origin:{host_id, agent_session_id, pid, proc_start}}]`; updates are serialised under one mutex and written atomically (temp file + rename) on every spawn/exit |
| Inbound | helper forwards every inbound line verbatim on stdout as `{"frame": <line>}`; parsing and routing are the daemon's |
| Cap | at most 32 helpers per daemon (constant); beyond it `/deliver` is refused with `proxy_limit` |
| Idle reap | no traffic in either direction for 30 min (constant) ⇒ SIGTERM |
| Origin gone | when the daemon learns the origin session is gone (`target_gone` on a reply), the helper is reaped |
| Parent death | helper exits when stdin reaches EOF (daemon crash or restart) and removes its files |
| Startup sweep | daemon reads `proxies.json`; for each record whose pid is alive **and** whose current start time equals the recorded `proc_start`, it sends SIGTERM, waits ≤ 2 s, SIGKILLs, and waits for exit; only then unlinks the recorded `sock` and `files`. A record whose pid is dead or reused (start time differs) has its files unlinked only if they still name that pid; the file is then rewritten empty |
| Signals | SIGTERM/SIGINT ⇒ remove socket + both registry files, exit 0 |

### 4.6 Authorization matrix

Implemented at the daemon's single auth entry (`cmd/pdx/main.go`), replacing
the plain `TokenAuth` wrap for `/api/peers*` with a scoped check. Every other
route keeps its current behaviour.

| Route | admin token | a configured host's `inbound_token` | ticket |
|---|---|---|---|
| `GET /api/peers` (local scope) | ✓ | ✓ | – |
| `GET /api/peers?scope=all` | ✓ | ✗ | – |
| `POST /api/peers/send` | ✓ | ✗ | – |
| `POST /api/peers/deliver` | ✗ | ✓ (and `from.host_id` must equal the matched entry's verified `host_id`) | – |
| `POST /api/peers/hosts`, `DELETE …/{host_id}`, `POST /api/peers/token` | ✓ | ✗ | – |
| `GET /api/peers/log` | ✓ | ✗ | – |

No configured hosts means the `inbound_token` column is ✗ everywhere. An
unset admin token, which today disables auth for every route, does **not**
open the peer routes: on `/api/peers*` an empty admin token means the admin
column is ✗, and one-time tickets (`?ticket=`) are never accepted. This
lockdown lands in **P1** as a `PeerRouteAuth` wrapper placed in front of
`TokenAuth` for the `/api/peers` prefix; P2 adds the `inbound_token` column
to the same wrapper. `IPWhitelist` and `PairingGuard` stay in front as
today; every other route keeps the existing `TokenAuth` unchanged.

`/deliver` is admin-✗ on purpose: the admin token is for the user's own
clients, and a delivery must always be attributable to a configured host.
Local sends go through `/send`, which verifies the origin session.

### 4.7 Configuration summary

| Key | Phase | Default | Meaning |
|---|---|---|---|
| `peers.alias` | P1 | `host_id` up to `:` | display / address name |
| `peers.hosts[]` | P2 | empty | remote daemons: `alias`, `url`, `host_id`, `token`, `inbound_token`, `allow_bypass` |
| `peers.deliver` | P3 | `false` | accept `/api/peers/deliver` at all |

Helper cap (32) and idle reap (30 min) are constants; they become settings
only if a need appears.

### 4.8 Compatibility contract

Everything in §3.2–§3.3 except the socket path, the auth line and the
`type:"user"` frame is undocumented Claude Code behaviour. Therefore:

- The inventory reports each cc session's `version`; the daemon logs one
  warning per version newer than the last verified (`2.1.270`).
- P3 ships `pdx msg selftest`: starts `claude -p --input-format stream-json
  --name pdx-selftest --settings '{"crossSessionInbound":"accept"}'` in a
  throwaway tmux session, delivers a message to it through a helper, waits
  ≤ 60 s for its native reply to reach the helper, prints pass/fail, and
  always tears down the tmux session, the helper and their registry files.
  It is the upgrade gate: run it after every Claude Code update.

## 5. Phases

Each phase is one PR, independently reviewable and shippable.

### P1 — Local inventory (read-only)

- `internal/peers`: registry reader for `~/.claude/sessions`, liveness check,
  `PeerRecord`, address resolution (§4.1) as a pure function over records.
- Agent module registers an `agent.owner-resolver` service exposing
  `ResolveSessionOwner(ctx, code)`.
- `PeerRouteAuth` in `internal/middleware`, wired in `cmd/pdx/main.go` in
  front of `TokenAuth` for the `/api/peers` prefix: non-empty admin bearer
  required, tickets refused, empty admin token ⇒ 401 (§4.6).
- New `peers` module: `GET /api/peers` (local scope only), 2 s soft budget,
  `partial` / `ok` envelope.
- `pdx peers [--json]` CLI, table sorted by session name.
- Config: `peers.alias` only.

Acceptance: on mlab, `pdx peers` lists every tmux session; cc sessions show
peer name, pid, inbox, `deliverable: true`; shell-only sessions show
`agent: null`; a cc session outside tmux appears as `cc:<name>`;
`curl /api/peers` without a bearer, or with `?ticket=`, is 401.

### P2 — Host registry, scoped auth, fan-out

- `peers.hosts[]` with per-host `inbound_token`; the `inbound_token` column
  of §4.6 added to `PeerRouteAuth`.
- `POST`/`PUT`/`DELETE /api/peers/hosts…` with the two-step pairing of §4.3,
  config persistence, shared redaction for GET and PUT config responses.
- `GET /api/peers?scope=all` with parallel fan-out and per-host rows.
- `pdx peers --all`, `pdx peers host add|set-token|remove|list`.

Acceptance: with air-2026 paired both ways, `pdx peers --all` on either host
shows both hosts and `host list` shows both entries verified; stopping one
daemon yields an error row; a request to `/api/peers` with a wrong
`inbound_token` is 401 even when the admin token is unset.

### P3 — Delivery (cc-uds only)

- `pdx peer-proxy` helper and lifecycle (§4.5), `proxies.json`, startup sweep.
- `POST /api/peers/send` (remote snapshot + `Resolve`), `POST
  /api/peers/deliver` (tuple re-verification), origin attribution, mode
  clamp, dedup, pair rate limit, limits, audit table, `pdx msg
  send|log|selftest`.

Acceptance: from a cc session on mlab, `pdx msg send air/<s> "ping"` reaches
the cc session on air as a peer message from `mini-lab/<s>`; that Claude's
native reply arrives back in the mlab session; `pdx msg log` shows the send,
the reply and their `msg_id`s; `pdx msg selftest` passes on both hosts;
`pdx msg send` with `CLAUDE_CODE_MESSAGING_SOCKET` unset or stale is refused
with `origin_unknown`.

## 6. Risks

| Risk | Mitigation |
|---|---|
| Claude Code changes the registry/frame format | §4.8 selftest gate; harness-facing code isolated in `internal/peers/ccuds` |
| One host's `inbound_token` leaks | it identifies exactly that host; the holder can list and deliver *as that host* only, with that host's `allow_bypass`; `peers.deliver` opt-in; audit; messages land at peer trust (§3.4) |
| Remote host lies about `declared_mode` | clamped to `prompting` unless the receiver's own entry for that host allows it |
| Remote host lies about `from.host_id` | must equal the `host_id` verified for the matched credential, else `host_unverified` |
| Same-UID process forges `origin_inbox` on `/send` | out of scope by design (§4.4 origin); the forged message is still peer-trust and audited |
| Helper pids accumulate or outlive the daemon | cap (starting helpers included), idle reap, EOF-on-stdin exit, ownership-checked startup sweep |
| Reply re-binds to a different session or process generation | wire identity and helper key are the full tuple; `target_gone` instead of re-resolution |
| Message loops A→B→A | pair rate limit, `msg_id` dedup for retransmits, `hop_chain` carried through; conversational loops beyond that rely on Claude Code's own throttles (documented) |
| Inventory slow on hosts with many sessions | 2 s budget with `partial`, memoised process reads |

## 7. Decisions recorded

- **Fan-out lives in the daemon**, not the CLI, so a future SPA panel can use
  the same endpoint. The cost — a second credential and a scoped auth
  entry — is paid in P2 and is required by P3 anyway.
- **No pre-spawned virtual peers.** Helpers exist only for origins that have
  actually sent something; discoverability of remote sessions is `pdx peers
  --all`'s job.
- **No typed-input lane in this spec** (§2). Codex/OpenCode targets are
  listed but not deliverable.

## 8. Review disposition (R1 `task-mtzzzf2x-kd6vvt`)

| # | Severity | Finding | Disposition |
|---|---|---|---|
| 1 | Blocker | module-local guard can't get past global `TokenAuth`; peer token allowed on `/send` | §4.6 matrix at the auth entry; `/send` admin-only; `/deliver` accept_token-only |
| 2 | Blocker | tmux-keys breaks the peer-trust premise; auto-downgrade | tmux-keys removed from the spec; `deliverable:false` instead of downgrade |
| 3 | Major | send-keys pane ≠ provenance pane | moot (#2) |
| 4 | Major | `--mode bypass` skips receiver hold | mode clamp with per-host `allow_bypass`; both modes audited |
| 5 | Major | `/send` has no verifiable origin | origin from `CLAUDE_CODE_MESSAGING_SOCKET`, verified against inventory; no `--from` |
| 6 | Major | reply routing lacks bidirectional config and host binding | full `host_id` on the wire; identity from matched config entry; `no_return_route` |
| 7 | Major | addresses unstable, non-tmux uncovered | §4.1 two-layer identity; `cc:<name>` form; `ambiguous` / `target_gone` |
| 8 | Major | inventory cross-module interface, multi-agent rule, deadline | `agent.owner-resolver` service; sessionId-only join; 2 s budget with `partial` |
| 9 | Major | helper holds admin token; replier identity; loopback may not exist | stdio IPC; replier resolved from live inventory; proxy-to-proxy refused |
| 10 | Major | helper lifecycle gaps | §4.5 table: dedup spawn, ready handshake, cap, EOF exit, startup sweep, O_EXCL writes |
| 11 | Major | CLI config writes bypass the running daemon; no redaction | all changes via admin API + `config.WriteFile`; redaction extended |
| 12 | Major | no end-to-end message id, dedup, limits | `msg_id`, 10 min dedup, hop ≤ 4, 64 KiB, timeouts, audit-before-write, fail closed |
| 13 | Minor | version source, local `ok`, proxy enum, selftest teardown | §4.2 `version`/`ok`/`partial`; `proxy` type; §4.8 timeout + teardown |
| 14 | Minor | P3 too wide; drop knobs | P3 = cc-uds only; cap/reap are constants; §7 records fan-out decision |

## 9. Review disposition (R2 `task-mu00ba1c-7564mi`)

| # | Severity | Finding | Disposition |
|---|---|---|---|
| 1 | Blocker | shared `accept_token` lets any peer impersonate an `allow_bypass` host | per-host `inbound_token`; matched credential names the host; `host_unverified` (§4.3, §4.4) |
| 2 | Major | P1 left peer routes on the empty-token / ticket-accepting `TokenAuth` | `PeerRouteAuth` lands in P1 (§4.6, §5) |
| 3 | Major | `host add` could not call `/api/info` with a peer credential | `host_id` learned from the `/api/peers` envelope (§4.3) |
| 4 | Major | helper keyed by `(host_id, session_id)` loses process generation | full tuple everywhere (§4.4, §4.5) |
| 5 | Major | cleanup ownership: partial registry writes, ready timeout, pid reuse in sweep | §4.5 rows Ready / Registry writes / Startup sweep / Ownership record |
| 6 | Major | `hop` / `msg_id` did not bound conversational loops; `hop_chain` not on the wire | single-hop rule, `hop_chain` field, pair rate limit, claim narrowed (§4.4) |
| 7 | Major | no protocol step for remote name resolution | sender resolves over a fetched snapshot; receiver re-verifies tuple (§4.4) |
| 8 | Minor | 2 s budget not enforceable with the existing resolver | soft budget, no cross-session memo claim, P2 timeout semantics (§4.2) |
| 9 | Minor | pane tiebreak not unique | exact `TmuxPaneID` match, unique or `ambiguous` (§4.2) |
| 10 | Minor | origin verification overstated | reworded as endpoint attribution; same-UID forgery out of scope (§4.4, §6) |
| 11 | Minor | PUT config echo not redacted | shared deep-copying redaction helper for every config response (§4.3) |
