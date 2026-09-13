# Spec — Cross-host agent peer bridge ("Peer Bridge")

Status: draft v1
Date: 2026-09-13
Branch: `worktree-peer-bridge`

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
| tmux session list | `GET /api/sessions` (`internal/module/session/provider.go:21`) | session code, tmux name, cwd, running command |
| Agent provenance | `GET /api/sessions/{code}/provenance` (Tab Rebuild) | `agent_type`, agent `session_id`, `cwd`, `tmux_pane_id` |
| Agent hooks | `pdx hook` (`cmd/pdx/hook.go`) | runs inside every cc/codex/opencode session at SessionStart |
| Host identity | `host_id` in `~/.config/pdx/config.toml` | stable per-host name |
| Bearer auth + tailnet bind | `internal/middleware/middleware.go:59` | daemons are reachable from each other |

What is missing: a peer-shaped view of that state, knowledge of the other
hosts at daemon level, and a delivery adapter into each agent kind.

## 2. Goals / non-goals

### Goals

1. `pdx peers` lists every agent session on the local host with a stable
   address `<host>/<session>`, its agent kind, its Claude Code peer name (when
   it has one), whether it is busy, and which delivery transport it supports.
2. `pdx peers --all` does the same across every configured host, in one table,
   with per-host failures reported inline rather than failing the whole call.
3. `pdx msg send <host>/<session> "<text>"` delivers a message into a session
   on another host. For Claude Code targets the message arrives as a native
   peer message carrying a reply address the receiving Claude can answer with
   its own `SendMessage`, without knowing Purdex is in the path. For other
   agents it arrives as typed input.
4. Every cross-host delivery is authenticated with a credential that grants
   nothing beyond peer listing and delivery, and is written to an audit log.

### Non-goals

- Replacing or wrapping Remote Control. Both coexist; Purdex never touches the
  bridge path.
- Injecting *user-authority* input into Claude Code through the peer socket.
  The socket path is peer-trust by construction (§3.4); user-authority input
  keeps using the existing tmux `send-keys` / stream-mode stdin lanes.
- Codex `app-server` integration. Codex and OpenCode targets get tmux
  `send-keys` in this spec; a richer adapter is a later spec.
- SPA UI. This spec is daemon + CLI. A "Peers" panel can consume `/api/peers`
  later without changes here.
- Broadcast, groups, message history browsing, read receipts.
- Windows / named pipes. macOS and Linux only (the two hosts in scope).

## 3. Evidence (measured on mlab, Claude Code 2.1.270, 2026-09-13)

Full protocol notes are in the user's memory
`reference_cc_peer_socket_protocol`; the spike script is
`~/.claude/notes/cc-peer-proxy-spike.mjs`. The facts this spec depends on:

### 3.1 Discovery and registry

- Each session binds `/tmp/cc-socks/<pid>.sock` (env
  `CLAUDE_CODE_MESSAGING_SOCKET`) and writes `~/.claude/sessions/<pid>.json`
  (`sessionId`, `name`, `cwd`, `tmux: "<session>:@<win>.%<pane>"`,
  `messagingSocketPath`, `procStart`, `status: idle|busy`) plus
  `<pid>.<sha256>.key` (mode 0600, `{peerToken, procStart, pidDomain}`).
- `procStart` equals `TZ=UTC ps -p <pid> -o lstart=`.
- `-p --input-format stream-json` sessions bind a socket too; bare mode does
  not.
- `sessionId` in that file equals the `session_id` Purdex records from the
  SessionStart hook, so the join needs no heuristics.

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
loop detection; a relay must preserve it when re-wrapping.

### 3.3 Virtual peers

Any process that binds `/tmp/cc-socks/<own pid>.sock` and writes the two
registry files appears in `ListAgents`, receives native `SendMessage`
frames, and is a valid reply target (T3b). The registry is keyed by pid, so
**one process can impersonate exactly one peer**. The harness verifies that
the registered pid is alive and its `procStart` matches; it does not verify
`from-mode`.

### 3.4 Trust class of socket-delivered messages

Whatever the frame says, the receiving harness presents it to the model as a
message from another session ("not typed by your user"), forbids it from
answering permission prompts or changing configuration, and treats slash
commands as text. This is the correct trust level for agent-to-agent traffic
and is why the bridge must not be used as a substitute for user input.

## 4. Design

### 4.1 Address scheme

```
<host>/<session>
```

- `<host>` is the daemon's `host_id` with the random suffix stripped for
  display (`mini-lab:278cbm` → `mini-lab`), and a configured alias may
  override it (§4.5). Matching is case-insensitive on the alias or full id.
- `<session>` is the tmux session name (Purdex's session unit), e.g. `mt1`.
  Session *codes* (`02ybs5`) are also accepted as an escape hatch.
- Claude Code peer names (`purdex-47`) are shown in listings but are not
  addresses: they are machine-generated, collide across hosts, and change on
  `/rename`.

### 4.2 Peer record

`GET /api/peers` returns one record per tmux session on the host, plus one
per Claude Code registry entry that is not inside any tmux session:

```jsonc
{
  "host": "mini-lab",            // display name
  "host_id": "mini-lab:278cbm",
  "address": "mini-lab/mt1",
  "session_code": "02ybs5",      // "" when the agent runs outside tmux
  "session_name": "mt1",
  "cwd": "/Users/wake/Workspace/wake/purdex",
  "agent": {                     // null when no agent is detected
    "type": "cc",                // cc | codex | opencode
    "session_id": "fa5d4c07-…",
    "peer_name": "purdex-47",    // cc only
    "pid": 76973,                // cc only
    "inbox": "/tmp/cc-socks/76973.sock", // cc only
    "status": "busy",            // cc: from registry; others: from Purdex agent status
    "version": "2.1.270"
  },
  "transport": "cc-uds",         // cc-uds | tmux-keys | none
  "seen_at": 1789314873394
}
```

Join order for a tmux session: provenance (`agent_type`, `session_id`) →
Claude Code registry by `sessionId` → fall back to registry by tmux pane
(`tmux` field) for sessions whose provenance is missing. A registry entry is
live only if its socket exists and `kill -0 pid` succeeds and `procStart`
matches the current process; stale entries are skipped, never deleted.

`transport` is derived: cc with a live inbox → `cc-uds`; any agent in a tmux
pane → `tmux-keys`; no agent → `none`. A cc session outside tmux with a live
inbox is `cc-uds` (deliverable, not rebuildable).

### 4.3 Host registry and fan-out (P2)

`~/.config/pdx/config.toml`:

```toml
[peers]
peer_token = "pdxp_…"                # accepted ONLY on /api/peers* routes

[[peers.hosts]]
name  = "air"                        # alias used in addresses
url   = "http://100.64.0.4:7860"
token = "pdxp_…"                     # that host's peer_token
```

- `GET /api/peers?scope=all` fans out to every configured host's
  `GET /api/peers` in parallel with a 3 s timeout each and returns
  `{ hosts: [{name, ok, error?, peers: […]}] }`. The local host is always
  first and always `ok`.
- `peer_token` is a second bearer credential checked by a route-scoped guard:
  it authenticates `/api/peers` and (in P3) `/api/peers/deliver`, nothing
  else. The existing admin token also works on those routes. Inter-daemon
  calls send `peer_token` only.
- `pdx token generate --peer` mints a `peer_token`; `pdx peers host add
  <name> <url> <token>` writes a `[[peers.hosts]]` entry.

### 4.4 Delivery (P3)

```
pdx msg send air/foo "text"
   │  POST /api/peers/send {to, text, mode?}          (local daemon, admin or peer token)
   ▼
local daemon ── POST /api/peers/deliver ──▶ remote daemon
                {to_session, text, from:{host, session, peer_name, mode}, hop_chain?}
                                              │
                        transport cc-uds:     ▼  virtual peer for <from.host>/<from.session>
                                              └─▶ /tmp/cc-socks/<target pid>.sock
                        transport tmux-keys:  └─▶ tmux send-keys (prefixed line)
```

**Virtual peers.** Because one process can impersonate one peer (§3.3), the
daemon spawns a helper `pdx peer-proxy --name <host>/<session>` per remote
origin it has to speak for, on first use. The helper:

1. binds `/tmp/cc-socks/<own pid>.sock`, writes the registry pair with
   `name = "<host>/<session>"`, `nameSource: "user"`, `kind: "interactive"`;
2. forwards every inbound frame to the daemon over `POST /api/peers/inbound`
   (loopback, admin token from its environment), preserving `msg_id` and the
   `hop-chain` attribute;
3. exits on SIGTERM, removing its socket and registry files;
4. is reaped by the daemon after 30 min without traffic in either direction.

Daemon → target Claude Code: write one frame (§3.2) to the target's inbox with
`from: "uds:<virtual peer sock>"` and a wrapper whose `from-name` is the
origin address and whose `from-mode` is `from.mode`.

Remote Claude Code → virtual peer → daemon `/api/peers/inbound` → daemon looks
up which origin the virtual peer represents → `POST /api/peers/deliver` on the
origin host, which delivers into the origin session through *its* virtual peer
for the replier. The chain is symmetric; neither Claude ever sees a Purdex
address, only a peer named `air/foo` or `mini-lab/mt1`.

**Mode propagation.** `from.mode` is never upgraded by any daemon. When the
origin is a native `SendMessage` to a virtual peer, the wrapper already carries
the sender's `from-mode` and it is forwarded as-is. When the origin is
`pdx msg send`, the CLI cannot observe the calling session's mode, so it sends
`prompting` unless the caller passes `--mode bypass`, which is the same
self-declaration Claude Code itself makes.

**tmux-keys transport.** The text is sent as a single line
`[peer message from <host>/<session>] <text>` followed by Enter, using the
existing `POST /api/sessions/{code}/send-keys`. Newlines in `text` are
collapsed to spaces. This lane has no reply address; the prefix tells the
receiving agent how to answer (`pdx msg send <origin> …`). The daemon refuses
tmux-keys delivery when the pane's current command is a shell (no agent), to
avoid executing the text.

**Audit.** Every `/api/peers/deliver` and `/api/peers/inbound` writes a row to
`peer_messages` in `meta.db`: `ts, direction, from_addr, to_addr, mode,
transport, bytes, result, error`. `pdx msg log [--tail N]` prints it.

### 4.5 Configuration summary

| Key | Phase | Default | Meaning |
|---|---|---|---|
| `peers.alias` | P1 | derived from `host_id` | display / address name of this host |
| `peers.peer_token` | P2 | unset (fan-out disabled) | route-scoped credential |
| `peers.hosts[]` | P2 | empty | remote daemons |
| `peers.deliver` | P3 | `false` | accept `/api/peers/deliver` at all |
| `peers.proxy_idle_minutes` | P3 | `30` | virtual peer reap timeout |

### 4.6 Compatibility contract

Everything in §3.2–§3.3 is undocumented Claude Code behaviour except the
socket path, the auth line and the `type:"user"` frame, which the official
docs describe for scripts. The spec therefore requires:

- `pdx peers` shows each cc session's `version`; the daemon logs a warning
  when a version newer than the last verified one (`2.1.270`) appears.
- P3 ships `pdx msg selftest`: starts a headless `claude -p --input-format
  stream-json --name pdx-selftest --settings '{"crossSessionInbound":"accept"}'`
  in a throwaway tmux session, runs the T3b loop through a virtual peer, and
  reports pass/fail. This is the upgrade gate.

## 5. Phases

Each phase is one PR, independently reviewable and shippable.

### P1 — Local inventory (read-only)

- `internal/peers`: registry reader (`~/.claude/sessions`), liveness check,
  join with provenance, `PeerRecord`.
- `GET /api/peers` (local scope only) in a new `peers` module.
- `pdx peers [--json]` CLI, table output sorted by session name.
- No config changes except optional `peers.alias`.

Acceptance: on mlab, `pdx peers` lists every tmux session, marks the cc ones
with their peer name and `cc-uds`, and shows `q5uc6s`-style shell-only
sessions as `none`.

### P2 — Host registry and fan-out

- `peers.peer_token`, `peers.hosts[]`, route-scoped token guard.
- `GET /api/peers?scope=all` with parallel fan-out and partial results.
- `pdx peers --all`, `pdx peers host add|remove|list`,
  `pdx token generate --peer`.

Acceptance: with air-2026 configured, `pdx peers --all` on either host shows
both hosts; stopping one daemon yields an inline error row for it, not a
failure.

### P3 — Delivery

- `pdx peer-proxy` helper, virtual peer lifecycle, `POST /api/peers/inbound`.
- `POST /api/peers/send`, `POST /api/peers/deliver`, cc-uds and tmux-keys
  adapters, mode propagation, audit table, `pdx msg send|log|selftest`.

Acceptance: from a cc session on mlab, `pdx msg send air/<s> "ping"` reaches
the cc session on air as a peer message from `mini-lab/<s>`; that Claude's
native `SendMessage` reply arrives back in the mlab session; `pdx msg log`
shows both rows; `pdx msg selftest` passes on both hosts.

## 6. Risks

| Risk | Mitigation |
|---|---|
| Claude Code changes the registry/frame format | §4.6 selftest gate; virtual peer logic isolated in `internal/peers/ccuds` |
| `peer_token` leak lets a third party inject peer messages into every session on a host | token is route-scoped, `peers.deliver` is opt-in, audit log, and messages still land at peer trust (§3.4) |
| Sender lies about `from-mode` to skip the receiver's hold | identical to what any local process can do today; daemons never upgrade the mode, and the CLI defaults to `prompting` |
| Virtual peer pids accumulate | idle reaping; `pdx peers` shows them under `agent.type: "proxy"` so leaks are visible |
| tmux-keys corrupts a half-typed prompt | refuse when no agent is running; documented as best-effort lane |
| Message loops A→B→A | Claude Code's own per-sender rate limit and repeat suppression apply on both ends; `hop-chain` is preserved |

## 7. Open questions

1. Should P2's fan-out live in the daemon (chosen: yes, so the SPA can reuse
   it) or only in the CLI? Daemon-side costs a route-scoped token; CLI-side
   would leave the SPA without a peers view.
2. Should the daemon pre-spawn virtual peers for every remote cc session
   after `pdx peers --all`, so `ListAgents` on one host shows the other host's
   sessions without a first message? Deferred: it multiplies helper processes
   and is a discoverability nicety, not a delivery requirement.
