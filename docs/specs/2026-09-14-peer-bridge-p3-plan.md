# Peer Bridge P3 Implementation Plan — Delivery (cc-uds only)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Plan v2 (after codex plan review `task-mu07l94z-oszksd`: 1 Blocker, 13
Majors, 3 Minors — all applied; §Review disposition at the end).

**Goal:** From a Claude Code session on host A, `pdx msg send <host>/<session>
"text"` lands in a Claude Code session on host B as a native peer message
with a reply address. That Claude's native `SendMessage` reply comes back
into the originating session on A. Every hop is authenticated per host,
mode-clamped, rate-limited, audited before the socket write, and carried by
a helper process that holds no credential. `pdx msg selftest` is the
upgrade gate for the undocumented Claude Code protocol.

**Architecture:** Spec §4.4 (delivery, origin, mode clamp, msg_id / limits,
audit), §4.5 (helper lifecycle), §4.8 (selftest), §5 P3, §6 risks. All
harness-facing code (frame, wrapper, registry files, socket write, virtual
peer) is isolated in `internal/peers/ccuds`; the helper's stdio protocol —
both the helper body and the **client that spawns and talks to it** — in
`internal/peers/proxyhelper`; the daemon side (helper manager, `/send`,
`/deliver`, reply forwarding, audit) in `internal/module/peers`; the audit
table in `internal/store` on `meta.db`; the CLI in `cmd/pdx/msg.go`.

**Tech Stack:** Go 1.26 · no SPA changes.

**Spec:** `docs/specs/2026-09-13-peer-bridge-spec.md` v3. **Depends on P2**
(`worktree-worktree-peer-bridge-p2`, PR pending): `middleware.PeerAuth` /
`Principal{Kind, Alias, HostID}` / `PrincipalFrom` / `WithPrincipal`
(`internal/middleware/peer_auth.go`), the `/api/peers` chain in
`cmd/pdx/http_chain.go`, `peersmod.HostRoutePolicy` (`policy.go`),
`Core.UpdateConfig`, `config.PeerHost{Alias, URL, HostID, Token,
InboundToken, AllowBypass}` + `MatchInboundToken` / `FindPeerHostByAlias`,
`peers.Envelope` / `Resolve` / `SplitAddress` / `HostMatches`, and the
module's `fetch` seam (`fetchFunc(ctx, client, baseURL, bearer)
(Envelope, error)`, `fetchRemote`, `newRemoteClient()` — 3 s, no redirects,
16 MiB cap — `remoteFetchTimeout`). **Implementation starts only after P2
is merged**: first `git merge origin/main` into this worktree, re-read the
P2 files named above, and adjust any signature this plan quotes that
drifted.

## Global Constraints

- **TDD, no exceptions.** Failing test first, run it, implement, run again.
  Each task is one commit.
- **Commit messages in English**; conversation replies in Traditional Chinese.
  Every commit ends with:
  ```
  Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01EgM4WuDT6evir8FL7SFFBd
  ```
- **Verification:** `go build ./... && go vet ./... && go test ./...` from the
  worktree root, green before every commit. `go test -race` for
  `./internal/module/peers/ ./internal/peers/... ./internal/store/` once per
  task in Phases C–D.
- **Worktree path:** every command runs from
  `/Users/wake/Workspace/wake/purdex/.claude/worktrees/peer-bridge-p3`
  (prefix every Bash call with `cd <that path> &&`); every Edit/Write uses
  that absolute prefix.
- **Existing tests outside this feature are not edited.** P1/P2 tests under
  `internal/peers`, `internal/module/peers`, `internal/middleware`,
  `cmd/pdx/peers_test.go`, `cmd/pdx/http_chain_test.go` are this feature's
  own and may change only where a task says so.
- **No network in unit tests** beyond `httptest` on loopback and Unix
  sockets under a short temp dir. **No test touches tmux, `claude`, the
  user's `~/.claude/sessions`, `/tmp/cc-socks`, or `~/.config/pdx`.**
- **Unix socket paths in tests** must stay under macOS's 104-byte
  `sun_path` limit: use `os.MkdirTemp("/tmp", "pdxp")` (never
  `t.TempDir()`, whose path is ~90 bytes) and `t.Cleanup(os.RemoveAll)`.
- **Secrets never reach a response, a log, the helper, or `proxies.json`.**
  The helper is spawned with an empty environment plus `PATH`/`HOME` only.
- **Package boundaries:** `internal/peers` and its subpackages stay leaves
  (stdlib + `internal/agent`); `internal/peers/ccuds` may import
  `internal/peers`; `internal/peers/proxyhelper` may import `ccuds`.
  `internal/store` gains no new imports. `internal/module/peers` imports
  all of them; `cmd/pdx/msg*.go` imports `internal/peers`, `ccuds`,
  `proxyhelper` and `internal/config` only (never the module).
- **Every constant from the spec is a named constant** in one place
  (`internal/peers/wire.go` for wire limits, `internal/module/peers/helpers.go`
  for helper lifecycle): `MaxTextBytes = 64 KiB`, `DedupWindow = 10 min`,
  `PairRateLimit = 30/min`, `InterDaemonTimeout = 10 s`, `SocketWriteTimeout
  = 5 s`, `HelperCap = 32`, `HelperIdleReap = 30 min`, `HelperReadyTimeout =
  3 s`, `HelperTermGrace = 2 s`, `VerifiedCCVersion = "2.1.270"`.
- **Clocks and processes are injectable.** Nothing in
  `internal/module/peers` calls `time.Now`, `exec.Command`, `os.Getpid`,
  `syscall.Kill` or `ps` directly; each goes through a seam set by `New()`
  and overridden in tests. Every wait in a test is bounded and driven by a
  fake clock or a channel, never by `time.Sleep` polling.
- **Every goroutine has an owner that stops it.** Anything started in
  `Start`/`Init`/`Acquire` is cancelled from a manager- or module-owned
  context and waited for in `Stop`; **an HTTP request context never owns a
  process or a goroutine that must outlive the request** (review B1).

## Decisions taken in this plan (spec-silent points; reviewers may challenge)

| # | Point | Decision |
|---|---|---|
| D1 | `/send` to the **local** host | Refused with 400 `local_target`. Same-host Claude Code sessions already reach each other natively (`ListAgents`/`SendMessage`); bridging them through a helper would only add a second, worse path. (Confirmed by the P2 session.) |
| D2 | `from-name` alias in the wrapper written on B | B's **own configured alias** for the matched host entry (`Principal.Alias`), not a string A sends — spec §4.4 `<A alias>` read as "A's alias on B". The wire carries `from.session_name` (tmux session name, or `cc:<peer_name>` outside tmux) for the part after `/`. |
| D3 | Reply `declared_mode` | The `from-mode` attribute Claude Code wrote into the reply wrapper (that harness's own report of its mode), `prompting` when absent or unparsable. A clamps it by its entry for B exactly like a first-hop message. |
| D4 | Selftest topology | `pdx msg selftest` spawns a **real `pdx peer-proxy` subprocess** through the same `proxyhelper.Spawn` client the daemon uses (spec §4.8 "through a helper"), and talks to the throwaway session's socket directly. No daemon: the gate tests the undocumented harness contract plus the helper's config/ready/forward/exit path, which is exactly the part unit tests cannot cover. The daemon pipeline (`/send` → `/deliver` → audit → clamp) is covered by Task 10's two-daemon test and by Phase F on real hosts. |
| D5 | Dedup hit | 409 `duplicate` (not silently 200): the sender never resends, and the refusal is visible. Dedup is **in-memory only**; the audit table never refuses a repeated `msg_id` (review M7). |
| D6 | `peers.deliver` toggle | `PUT /api/peers/settings {deliver}` (admin) via `Core.UpdateConfig`, `GET /api/peers/settings`; CLI `pdx msg deliver on|off|status`. `PUT /api/config` keeps its field whitelist untouched. |
| D7 | Helper `procStart` | Read with `TZ=UTC ps -p <pid> -o lstart=` (one fork per spawn), byte-identical to what the harness computes — not reformatted from `ReadProcessInfo`, whose sub-second truncation is unverified. `pidDomain` = `runtime.GOOS` (only `darwin` verified). |
| D8 | Helper config channel | The daemon writes **one JSON line** on the helper's stdin before anything else; the helper answers with the ready line. Nothing else ever travels daemon→helper; stdin EOF is the shutdown signal (§4.5). |
| D9 | Recognising **another** daemon's helpers (review M10) | A helper is recognised by its **process**, not by a registry marker: the liveness probe already reads `ProcessInfo` per pid, and an entry whose `Argv` is `pdx … peer-proxy` is a proxy row on every daemon that reads that registry, not only on the one that spawned it. `ProxyPIDs` (own helpers) stays as a fast path. Production remains one daemon per user registry; the two-daemon topology of Task 10 / Phase F is thereby safe too. |
| D10 | Audit row identity | `peer_messages` has its own `id INTEGER PRIMARY KEY`; `(msg_id, direction)` is indexed, not unique. The result update targets the row id `Insert` returned. |
| D11 | Reply `msg_id` | Minted by B (spec §4.4) with `newMsgID()`; the native frame's `msg_id` is recorded in the audit `native_msg_id` column for tracing only. |

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/peers/wire.go` *(new)* | `DeliverRequest`/`DeliverResponse`/`SendRequest`/`SendResponse`/`APIError`, `WireFrom`/`WireTo`, `OriginKey` (JSON-tagged), mode constants, error codes, `MaxTextBytes`, validators |
| `internal/peers/registry.go` *(modify)* | `Liveness.Info` probe; `Entry.IsProxy`; `IsProxyArgv` |
| `internal/peers/record.go` *(modify)* | `Build` treats `Entry.IsProxy` like `ProxyPIDs` |
| `internal/peers/ccuds/wrapper.go` *(new)* | `Wrapper`, `Format`, `Parse` |
| `internal/peers/ccuds/frame.go` *(new)* | `Frame`, `BuildFrame`, `ParseFrame`, `FromSocket`, `WriteFrame` |
| `internal/peers/ccuds/registry_write.go` *(new)* | `RegistryFiles`, `WriteRegistry`, `RemoveRegistry`, `ReadPeerFeatures`, `RegistryProcStart` |
| `internal/peers/ccuds/virtual_peer.go` *(new)* | `VirtualPeer` (bind, register, accept loop with tracked conns, cancellable `Frames`, `Close`) |
| `internal/peers/ccuds/version.go` *(new)* | `VerifiedCCVersion`, `NewerThanVerified` |
| `internal/peers/proxyhelper/helper.go` *(new)* | `Run(ctx, stdin, stdout, Options)` — the `pdx peer-proxy` body |
| `internal/peers/proxyhelper/client.go` *(new)* | `Spawn(ctx, Starter, Config) (Handle, error)` — config line, ready handshake, frame pump; `ExecStarter`; `Handle` interface |
| `cmd/pdx/peer_proxy.go` *(new)* | `peer-proxy` subcommand |
| `internal/store/peer_message.go` *(new)* | `peer_messages` table on `meta.db`; `PeerMessageStore` |
| `internal/store/meta.go` *(modify)* | migration; `(*MetaStore).PeerMessages()` |
| `internal/config/config.go` *(modify)* | `PeersConfig.Deliver bool` |
| `internal/module/peers/policy.go` *(modify)* | host may `POST /api/peers/deliver` |
| `internal/module/peers/settings.go` *(new)* | `GET`/`PUT /api/peers/settings` |
| `internal/module/peers/rows.go` *(new)* | `normalizeRemoteRows` |
| `internal/module/peers/limits.go` *(new)* | `dedupSet`, `pairLimiter` |
| `internal/module/peers/helpers.go` *(new)* | `helperManager` state machine, `proxies.json`, reap, sweep |
| `internal/module/peers/deliver.go` *(new)* | `POST /api/peers/deliver` |
| `internal/module/peers/send.go` *(new)* | `POST /api/peers/send`, `postDeliver` |
| `internal/module/peers/reply.go` *(new)* | reply forwarding, `GET /api/peers/log` |
| `internal/module/peers/module.go` *(modify)* | `New(audit)`, seams, routes, Start/Stop, version warning |
| `cmd/pdx/main.go` *(modify)* | `peer-proxy`, `msg`; `peersmod.New(meta.PeerMessages())` |
| `cmd/pdx/msg.go` *(new)* | `pdx msg send|log|deliver|selftest` grammar and the first three verbs |
| `cmd/pdx/msg_selftest.go` *(new)* | selftest body |

---

# Phase A — Leaf packages

### Task 1: Wire types and limits (`internal/peers/wire.go`)

**Files:** create `internal/peers/wire.go`, `wire_test.go`.

**Produce:**
```go
const (
    MaxTextBytes  = 64 * 1024
    ModePrompting = "prompting"
    ModeBypass    = "bypass"
)
// Error codes (the "error" field of every 4xx/5xx JSON body on /send, /deliver, /log).
const (
    ErrBadRequest = "bad_request"; ErrTextTooLarge = "text_too_large"; ErrBadMode = "bad_mode"
    ErrBadAddress = "bad_address"; ErrLocalTarget = "local_target"; ErrHostUnknown = "host_unknown"
    ErrOriginUnknown = "origin_unknown"; ErrPeerNotFound = "peer_not_found"; ErrAmbiguous = "ambiguous"
    ErrNotDeliverable = "not_deliverable"; ErrRemoteError = "remote_error"
    ErrHostUnverified = "host_unverified"; ErrDeliverDisabled = "deliver_disabled"; ErrAdminNotAllowed = "admin_not_allowed"
    ErrTargetGone = "target_gone"; ErrDuplicate = "duplicate"; ErrRateLimited = "rate_limited"
    ErrAuditUnavailable = "audit_unavailable"; ErrProxyLimit = "proxy_limit"; ErrProxySpawnFailed = "proxy_spawn_failed"
    ErrSocketWriteFailed = "socket_write_failed"; ErrNotReady = "not_ready"
    ErrReplierUnknown = "replier_unknown"; ErrProxyToProxy = "proxy_to_proxy"; ErrNoReturnRoute = "no_return_route"
)
// Results (DeliverResponse.Result / SendResponse.Result / audit result column).
const ( ResultDelivered = "delivered"; ResultDeliveryUncertain = "delivery_uncertain" )

type WireFrom struct {
    HostID         string `json:"host_id"`
    AgentSessionID string `json:"agent_session_id"`
    PID            int    `json:"pid"`
    ProcStart      string `json:"proc_start"`
    PeerName       string `json:"peer_name"`     // registry name; may be ""
    SessionName    string `json:"session_name"`  // tmux session name, or "cc:<peer_name>" outside tmux
    DeclaredMode   string `json:"declared_mode"` // prompting | bypass
}
type WireTo struct {
    AgentSessionID string `json:"agent_session_id"`
    PID            int    `json:"pid"`
    ProcStart      string `json:"proc_start"`
}
type DeliverRequest struct {
    MsgID    string   `json:"msg_id"`
    HopChain string   `json:"hop_chain,omitempty"`
    From     WireFrom `json:"from"`
    To       WireTo   `json:"to"`
    Text     string   `json:"text"`
}
type DeliverResponse struct {
    MsgID         string `json:"msg_id"`
    Result        string `json:"result"`         // delivered | delivery_uncertain
    EffectiveMode string `json:"effective_mode"`
    OneWay        bool   `json:"one_way"`        // receiver has no verified outbound route back to the sender (§4.3)
}
type SendRequest struct {
    To          string `json:"to"`             // "<host>/<session>"
    Text        string `json:"text"`
    Mode        string `json:"mode,omitempty"` // "" ⇒ prompting
    OriginInbox string `json:"origin_inbox"`
}
type SendResponse struct {
    MsgID         string `json:"msg_id"`
    ToHostID      string `json:"to_host_id"`
    ToAddress     string `json:"to_address"`     // normalised "<alias>/<session>" (Task 5)
    To            WireTo `json:"to"`
    Result        string `json:"result"`
    EffectiveMode string `json:"effective_mode"`
    OneWay        bool   `json:"one_way"`
}
type APIError struct {
    Error      string       `json:"error"`
    Detail     string       `json:"detail,omitempty"`
    Candidates []string     `json:"candidates,omitempty"` // ambiguous: addresses
    Remote     *RemoteError `json:"remote,omitempty"`     // remote_error: the other daemon's answer
}
type RemoteError struct { Status int `json:"status"`; Error string `json:"error"`; Detail string `json:"detail,omitempty"` }

// OriginKey is the helper key, the proxies.json origin and the audit
// identity of a sender. JSON tags match spec §4.5 exactly.
type OriginKey struct {
    HostID         string `json:"host_id"`
    AgentSessionID string `json:"agent_session_id"`
    PID            int    `json:"pid"`
    ProcStart      string `json:"proc_start"`
}
func (f WireFrom) Key() OriginKey
func (t WireTo) Key(hostID string) OriginKey

func ValidateText(s string) error                 // non-empty, valid UTF-8, len ≤ MaxTextBytes
func ValidateMode(s string) (string, error)       // "" | prompting | bypass ⇒ normalised
func IsUUID(s string) bool                        // 8-4-4-4-12 lowercase hex
func (r DeliverRequest) Validate() error          // msg_id UUID; tuples complete (pid > 0, proc_start parses via ParseProcStart); mode; text
```

**Tests:** JSON round-trip of every struct with golden literals (field
names are the wire contract; `OriginKey` raw keys asserted as
`host_id`/`agent_session_id`/`pid`/`proc_start`); `ValidateText` at 65536
bytes ok / 65537 rejected / invalid UTF-8 rejected / empty rejected;
`ValidateMode` table; `Validate` rejects each missing tuple field, pid 0,
bad proc_start, non-UUID msg_id; `Key()` equality.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): wire types, limits and error codes for delivery`

### Task 2: Harness-facing primitives (`internal/peers/ccuds`)

**Files:** create `internal/peers/ccuds/{wrapper,frame,registry_write,virtual_peer,version}.go`
and tests.

**Produce (wrapper.go):**
```go
type Wrapper struct{ From, FromName, FromMode, HopChain, Text string }
// Format renders exactly:
//   <cross-session-message from="…" from-name="…" from-mode="…"[ hop-chain="…"]>\n<text>\n</cross-session-message>
// Attribute values are escaped for `"` `&` `<` `>` (html.EscapeString);
// text is verbatim. hop-chain is emitted only when non-empty.
func (w Wrapper) Format() string
// Parse: ok false unless content starts with "<cross-session-message" and
// ends with "</cross-session-message>". Attributes in any order; unknown
// attributes ignored; values html-unescaped. Text is everything between
// the opening tag's trailing "\n" and the closing tag's leading "\n".
func Parse(content string) (Wrapper, bool)
```
**Produce (frame.go):**
```go
type Frame struct {
    MsgV     int    `json:"msgV"`
    MsgID    string `json:"msg_id"`
    Type     string `json:"type"`
    Priority string `json:"priority"`
    From     string `json:"from,omitempty"`
    Message  struct{ Role string `json:"role"`; Content string `json:"content"` } `json:"message"`
}
// BuildFrame returns one NDJSON line (trailing "\n"): msgV 1, type "user",
// priority "next", from "uds:"+fromSock, role "user", content w.Format().
func BuildFrame(msgID, fromSock string, w Wrapper) ([]byte, error)
// ParseFrame decodes one line. Content may be a string or an array of
// {type:"text",text} blocks (concatenated); other shapes ⇒ error.
func ParseFrame(line []byte) (Frame, error)
// FromSocket strips "uds:"; ok false when the prefix is absent.
func FromSocket(from string) (path string, ok bool)

var ErrWriteIncomplete = errors.New("frame not fully written")
var ErrPostWriteTimeout = errors.New("timed out after write")
// WriteFrame dials sockPath (unix), sets an absolute deadline of
// now+timeout on the conn, writes line, half-closes the write side and
// waits for the peer's EOF or the deadline. nil when everything completed;
// ErrWriteIncomplete (wrapped) when the write failed or timed out;
// ErrPostWriteTimeout (wrapped) when the full line was written but the
// EOF wait hit the deadline. Dial errors returned as-is. ctx cancellation
// aborts the wait.
func WriteFrame(ctx context.Context, sockPath string, line []byte, timeout time.Duration) error
```
**Produce (registry_write.go):**
```go
type RegistryEntry struct {
    PID int; SessionID, Name, Cwd, ProcStart, Version, Inbox, PidDomain string
    PeerFeatures []string
}
// DefaultPeerFeatures is the list observed on 2.1.270 (spec §3.1 / spike),
// used only when no live cc entry is available to copy from.
var DefaultPeerFeatures = []string{"notify_idle", "reply_across_default_dirs", "artifact_yield"}
func RegistryFiles(dir string, pid int, peerToken string) (jsonPath, keyPath string)
// WriteRegistry creates both files with O_CREATE|O_EXCL|O_WRONLY|O_NOFOLLOW
// (key 0600, json 0644). JSON mirrors a real cc entry: pid, sessionId, cwd,
// startedAt (ms), procStart, version, peerProtocol 1, peerFeatures, kind
// "interactive", entrypoint "cli", pidDomain, messagingSocketPath, name,
// nameSource "user", nameSince, updatedAt, status "idle", statusUpdatedAt.
// Key content {"peerToken","procStart","pidDomain"}. If the key write
// fails the json is removed. Returns the paths actually created.
func WriteRegistry(dir string, e RegistryEntry, peerToken string) (created []string, err error)
func RemoveRegistry(paths []string) error   // ENOENT ignored; first other error returned
// ReadPeerFeatures reads <dir>/<pid>.json (64 KiB cap, O_NOFOLLOW) and
// returns its peerFeatures; ok false when missing/unparsable/absent field.
func ReadPeerFeatures(dir string, pid int) (features []string, ok bool)
// RegistryProcStart reads <path>'s "procStart" (json) or the key file's
// "procStart"; "" when unreadable. Used by the sweep's ownership check.
func RegistryProcStart(path string) string
```
**Produce (virtual_peer.go):**
```go
type VirtualPeerOptions struct {
    PID         int    // own pid (injectable for tests)
    SockDir     string // default /tmp/cc-socks
    RegistryDir string // default ~/.claude/sessions
    Name        string
    SessionID   string // random UUID when ""
    Cwd, Version, PidDomain string
    PeerFeatures []string
    ProcStart   func(pid int) (string, error) // default: TZ=UTC ps -p <pid> -o lstart=
}
type VirtualPeer struct{ /* … */ }
// StartVirtualPeer: mkdir SockDir 0700 if missing; remove a stale
// <pid>.sock only if it exists AND connecting to it fails with
// ECONNREFUSED; net.Listen("unix"); chmod 0600; WriteRegistry; on any
// failure undo what was done. Then accept in a goroutine: every accepted
// conn is tracked in a set; each is read with bufio.Scanner (1 MiB max
// token) and every non-empty line is sent on Frames() — the send selects
// on the peer's done channel so a consumer that stopped reading never
// wedges Close. A conn that sends no newline is closed by Close like any
// other.
func StartVirtualPeer(o VirtualPeerOptions) (*VirtualPeer, error)
func (v *VirtualPeer) SockPath() string
func (v *VirtualPeer) Files() []string
func (v *VirtualPeer) Frames() <-chan string   // closed by Close after every reader goroutine has exited
// Close: close done; close listener; close every tracked conn; wait for
// the accept and reader goroutines; close Frames; unlink socket + files.
// Idempotent; returns the first unlink error.
func (v *VirtualPeer) Close() error
```
**Produce (version.go):** `const VerifiedCCVersion = "2.1.270"`;
`func NewerThanVerified(v string) bool` (numeric dotted compare; unparsable
⇒ false).

**Tests:** wrapper golden Format matches spec §3.2 byte layout incl.
escaping; Parse round-trips Format for every field, tolerates attribute
reordering, rejects plain text; frame field set exact; ParseFrame with
string and block-array content; `WriteFrame` against a test listener that
(a) reads all and closes ⇒ nil, (b) never reads (512 KiB line) ⇒
`ErrWriteIncomplete` within timeout+100 ms, (c) reads all but never closes
⇒ `ErrPostWriteTimeout`, (d) no listener ⇒ dial error, (e) ctx cancelled
during (c) ⇒ returns promptly; `WriteRegistry` refuses an existing json
(O_EXCL), rolls back the json when the key path is pre-occupied, key mode
0600, content parses back through `peers.ReadRegistry` with a fake
liveness as a live entry whose Name/Inbox/ProcStart match;
`ReadPeerFeatures` on a real-shaped fixture and on a file without the
field; `RegistryProcStart` for json and key; `VirtualPeer`: two peers with
different PIDs in one short dir bind distinct sockets; a frame written
with WriteFrame appears on `Frames()` verbatim; **a conn that connects and
sends no newline does not block Close** (Close returns within 1 s and the
conn is closed); **a consumer that never reads Frames does not block
Close**; Close removes socket + files, closes `Frames`, second Close nil;
stale-socket replacement only when refused, a live foreign listener at
the path ⇒ error; `NewerThanVerified` table.

- [ ] tests written and failing
- [ ] implementation, all green (`-race`)
- [ ] commit `feat(peers): ccuds — Claude Code frame, wrapper, registry and virtual peer primitives`

### Task 3: `pdx peer-proxy` helper and its client (`internal/peers/proxyhelper`)

**Files:** create `internal/peers/proxyhelper/{helper,client}.go`, tests,
`cmd/pdx/peer_proxy.go`; modify `cmd/pdx/main.go` (switch case).

**Stdio protocol (D8):**
```
daemon → helper (stdin, line 1):  {"name":"air/foo","registry_dir":"…","sock_dir":"…","version":"2.1.270","peer_features":["…"],"cwd":"…"}
helper → daemon (stdout, line 1): {"ready":true,"pid":N,"sock":"…","files":["…","…"]}
                              or: {"ready":false,"error":"…"}   then exit 1
helper → daemon (stdout, after):  {"frame":"<raw NDJSON line from the socket>"}   one per inbound line
daemon → helper: stdin EOF ⇒ helper closes the peer and exits 0
signals: SIGTERM/SIGINT ⇒ same cleanup, exit 0
```
**Produce (helper.go):**
```go
type Config struct {   // the stdin line
    Name, RegistryDir, SockDir, Version, Cwd string
    PeerFeatures []string
}
type Options struct {
    PID       int
    ProcStart func(pid int) (string, error)
    Signals   <-chan os.Signal // nil ⇒ Run installs SIGTERM/SIGINT notify
}
// Run reads the config line, starts a ccuds.VirtualPeer, writes the ready
// line, then pumps Frames() to stdout as {"frame":…} until stdin EOF, a
// signal, or ctx.Done(); then Close()s the peer. One mutex serialises
// stdout writes. A stdout write error (daemon gone) is treated like stdin
// EOF. Returns nil on clean shutdown; after a ready:false line, the error.
func Run(ctx context.Context, stdin io.Reader, stdout io.Writer, o Options) error
```
**Produce (client.go) — shared by the daemon's manager and the selftest:**
```go
// Proc is a started helper process as the client sees it.
type Proc interface {
    PID() int
    Stdin() io.WriteCloser
    Stdout() io.Reader
    Signal(os.Signal) error
    Wait() error            // returns once exited; safe to call once
}
// Starter starts a helper process. ExecStarter runs
// exec.CommandContext(ctx, exe, "peer-proxy") with Env {PATH, HOME},
// Setpgid, stderr to a caller-supplied writer with a "peer-proxy[pid]: "
// prefix. ctx is the PROCESS lifetime — the caller passes a long-lived
// context, never a request one.
type Starter func(ctx context.Context) (Proc, error)
func ExecStarter(exe string, stderr io.Writer) Starter

// Handle is a ready helper.
type Handle interface {
    PID() int
    Sock() string
    Files() []string
    Frames() <-chan string    // closed when the helper's stdout reaches EOF
    // Stop closes stdin (the helper's EOF signal), waits up to grace for
    // exit, SIGKILLs and waits again; returns the exit error. Idempotent.
    Stop(grace time.Duration) error
    Signal(os.Signal) error
}
var ErrNotReady = errors.New("helper did not become ready")
// Spawn: start(ctx); write cfg as one JSON line; read the first stdout
// line with a readyTimeout deadline (a timer, not the ctx). ready:true ⇒
// start the pump goroutine (stdout lines → Frames; malformed lines logged
// and skipped) and return the Handle. ready:false / timeout / decode
// error ⇒ SIGKILL, Wait, and — only when a ready:true line WAS parsed but
// something after it failed — RemoveRegistry(files); return wrapped
// ErrNotReady. Never leaves a process behind.
func Spawn(ctx context.Context, start Starter, cfg Config, readyTimeout time.Duration) (Handle, error)
```
`cmd/pdx/peer_proxy.go`: `case "peer-proxy": os.Exit(runPeerProxy())` with
`Options{PID: os.Getpid()}` and the default `ps` ProcStart; ignores every
argument, never reads config.toml, never opens HTTP.

**In-process fake (`proxyhelper/fake_test.go`, exported through an
`internal`-style `proxyhelpertest` package so the module tests reuse it):**
`FakeStarter(opts)` returns a `Starter` whose `Proc` runs `Run` in a
goroutine over `io.Pipe`s with `Options{PID: <counter from 900000>,
ProcStart: fake}`; `Signal` cancels its ctx; `Wait` joins the goroutine.
Variants: `Broken` (never writes ready), `Refusing` (ready:false),
`Barrier` (blocks before ready until released — for concurrency tests).

**Tests:** ready line has pid/sock/files and both files exist the instant
the ready line is observed; a frame written to `sock` is echoed as
`{"frame":…}` byte-exact; stdin EOF ⇒ Run returns nil within 1 s and
socket + files are gone; signal ⇒ same; **stdout closed by the reader
(daemon gone) ⇒ Run exits and cleans up**; unwritable `registry_dir` ⇒
`ready:false`, non-nil error, no socket left; non-JSON config ⇒
`ready:false`. `Spawn`: happy path Handle with frames pumped; Broken ⇒
`ErrNotReady` within readyTimeout+100 ms and `Wait` observed (no goroutine
leak — `goleak`-style check via `runtime.NumGoroutine` delta ≤ 0 after
1 s); Refusing ⇒ `ErrNotReady`; `Stop` returns after the helper exits and
is idempotent; **a cancelled caller context passed only to the ready wait
does not kill a ready helper** (Spawn takes the process ctx explicitly —
test that `Handle` survives cancelling a separate ctx used by the caller);
`cmd/pdx` dispatches `peer-proxy` (test the dispatch, not `main`).

- [ ] tests written and failing
- [ ] implementation, all green (`-race`)
- [ ] commit `feat(pdx): peer-proxy helper subcommand with stdio-only IPC and its spawn client`

### Task 4: Audit store (`internal/store/peer_message.go`)

**Files:** create `internal/store/peer_message.go`, `peer_message_test.go`;
modify `internal/store/meta.go` (migration + accessor).

**Produce:**
```go
// Schema (added to migrateMetaDB, CREATE TABLE IF NOT EXISTS):
//   peer_messages(id INTEGER PRIMARY KEY AUTOINCREMENT, msg_id TEXT NOT NULL,
//     native_msg_id TEXT NOT NULL DEFAULT '', direction TEXT NOT NULL, ts INTEGER NOT NULL /* unix ms */,
//     from_host_id TEXT, from_session_id TEXT, to_host_id TEXT, to_session_id TEXT,
//     declared_mode TEXT, effective_mode TEXT, bytes INTEGER, result TEXT, error TEXT)
//   CREATE INDEX IF NOT EXISTS peer_messages_ts ON peer_messages(ts)
//   CREATE INDEX IF NOT EXISTS peer_messages_msg ON peer_messages(msg_id, direction)
const ( DirOut = "out"; DirIn = "in"; DirReply = "reply" )
type PeerMessage struct {
    ID int64; MsgID, NativeMsgID, Direction string; TS time.Time
    FromHostID, FromSessionID, ToHostID, ToSessionID string
    DeclaredMode, EffectiveMode string; Bytes int; Result, Error string
}
type PeerMessageStore struct{ db *sql.DB }
func (m *MetaStore) PeerMessages() *PeerMessageStore
// Insert writes the row and returns its id. A repeated (msg_id, direction)
// is allowed (D10): dedup is the caller's in-memory window, never the DB.
func (s *PeerMessageStore) Insert(p PeerMessage) (id int64, err error)
func (s *PeerMessageStore) SetResult(id int64, result, errText string) error
// Tail returns the newest n rows, oldest first.
func (s *PeerMessageStore) Tail(n int) ([]PeerMessage, error)
```
**Tests:** `OpenMeta(":memory:")` creates the table and both indexes
(query `sqlite_master`); Insert/SetResult/Tail round-trip incl. ts ms
precision and ordering; two Inserts with the same `(msg_id, direction)`
both succeed with distinct ids; Tail(0) ⇒ empty non-nil slice; existing
`meta_test.go` untouched and green.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(store): peer_messages audit table on meta.db`

# Phase B — Config, registry, policy, limits

### Task 5: `peers.deliver`, proxy recognition, policy row, settings, row normalisation, version warning, limiters

**Files:** modify `internal/config/config.go` (+ test),
`internal/peers/registry.go`, `record.go` (+ tests),
`internal/module/peers/policy.go` (+ test), `module.go`; create
`internal/module/peers/settings.go`, `settings_test.go`, `rows.go`,
`rows_test.go`, `limits.go`, `limits_test.go`.

**Produce:**
- `PeersConfig.Deliver bool \`toml:"deliver" json:"deliver"\`` (default
  false); add one assertion to the existing Clone/Redacted tests.
- **Proxy recognition (D9)** in `internal/peers/registry.go`:
  ```go
  type Liveness struct {
      Stat      func(path string) error
      PidAlive  func(pid int) bool
      StartTime func(pid int) (time.Time, error)          // kept for P1 fakes
      Info      func(pid int) (agent.ProcessInfo, error)  // optional; when non-nil it replaces StartTime and also supplies Argv
  }
  // IsProxyArgv: filepath.Base(argv[0]) == "pdx" && argv contains "peer-proxy".
  func IsProxyArgv(argv []string) bool
  // Entry gains: IsProxy bool   (comparable — Entry stays usable as a map key)
  ```
  `DefaultLiveness` sets `Info` to `agent.ReadProcessInfo` (one call per
  entry, as today) and `StartTime` derived from it. `ReadRegistry` sets
  `IsProxy` from `Info`'s `Argv` when `Info != nil`. `Build` treats
  `e.IsProxy || in.ProxyPIDs[e.PID]` identically everywhere `ProxyPIDs`
  is consulted today (`record.go` candidates filter and outside rows).
- `HostRoutePolicy`: additionally true for `POST` with `r.URL.Path ==
  "/api/peers/deliver"`; every other row unchanged.
- `settings.go`: `GET /api/peers/settings` ⇒ `{"deliver":bool,"alias":"…"}`;
  `PUT /api/peers/settings {deliver?: bool}` ⇒ `Core.UpdateConfig`, 200
  with the same body. Both admin-only (policy refuses hosts; handler also
  checks `PrincipalFrom` ⇒ 403 in depth).
- `rows.go`:
  ```go
  // normalizeRemoteRows rewrites every row's identity from the config
  // entry, never from what the remote reported (P2 final review #5):
  // Host = alias; HostID = hostID (the verified one, or the envelope's
  // when the entry is still unverified — the caller passes whichever it
  // accepted); Address = alias + "/" + SessionName when SessionName != "",
  // else alias + "/cc:" + Agent.PeerName when Agent != nil && PeerName != "",
  // else alias + "/" + SessionCode. The remote Address is discarded.
  func normalizeRemoteRows(rows []ipeers.PeerRecord, alias, hostID string) []ipeers.PeerRecord
  ```
  `fetchHostResult` applies it to every successful row.
- `module.go`: `localEnvelope` gains a one-shot version warning — after
  `ReadRegistry`, for every distinct `Version` with
  `ccuds.NewerThanVerified`, log once per process (`sync.Map`): `peers:
  Claude Code %s is newer than the last verified %s; run pdx msg selftest`.
- `limits.go`:
  ```go
  type dedupSet struct{ /* mu, map[string]time.Time, window, now */ }
  func newDedupSet(window time.Duration, now func() time.Time) *dedupSet
  func (d *dedupSet) Seen(id string) bool    // records; true if present within window; prunes expired
  type pairKey struct{ From, To ipeers.OriginKey }
  type pairLimiter struct{ /* mu, map[pairKey][]time.Time, limit, window, now */ }
  func newPairLimiter(limit int, window time.Duration, now func() time.Time) *pairLimiter
  func (l *pairLimiter) Allow(k pairKey) bool // sliding window; empty pairs pruned
  ```

**Tests:** config round-trip `deliver = true`; `IsProxyArgv` table
(`/usr/local/bin/pdx peer-proxy` true, `pdx` alone false, `node
peer-proxy.js` false); `ReadRegistry` with a fake `Info` returning
`Argv{"pdx","peer-proxy"}` ⇒ `IsProxy`; `Build` with an `IsProxy` entry
(not in `ProxyPIDs`) ⇒ `proxy` row, `deliverable:false`, excluded from
session candidates, excluded by `Resolve("cc:<name>")`; P1 registry/record
tests untouched and green (their fakes set `StartTime`, not `Info`);
policy table (`POST /api/peers/deliver` true, `GET /api/peers/deliver`
false, `POST /api/peers/send` false, `POST /api/peers/settings` false);
settings GET/PUT persist to a temp `CfgPath`, host principal ⇒ 403;
`normalizeRemoteRows` table: a remote row claiming another host's
`host_id`/alias/`x/y/z` address is rewritten, outside-tmux row ⇒
`alias/cc:<name>`, empty everything ⇒ `alias/<code>`, output always passes
`SplitAddress`; `fetchHostResult` rows are normalised (extend the P2
scope=all test with one assertion); version warning fires once for
`2.1.271` across two inventory calls and never for `2.1.270`; dedup and
limiter with a fake clock (30 allowed, 31st refused, after 60 s allowed,
independent pairs independent, maps bounded after pruning).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): deliver toggle, proxy recognition by argv, deliver policy row, settings, row normalisation, limiters`

# Phase C — Helper manager

### Task 6: `helperManager` (`internal/module/peers/helpers.go`)

**Files:** create `internal/module/peers/helpers.go`, `helpers_test.go`.

**Produce:**
```go
const ( HelperCap = 32; HelperIdleReap = 30 * time.Minute; HelperReadyTimeout = 3 * time.Second; HelperTermGrace = 2 * time.Second )

type helperState int
const ( helperStarting helperState = iota; helperReady; helperStopping; helperExited )

type helper struct {
    key       ipeers.OriginKey
    name      string
    gen       uint64            // manager-wide monotonic; identifies THIS instance
    state     helperState       // guarded by manager mu
    ready     chan struct{}     // closed when state leaves helperStarting (ready or failed)
    err       error             // set when starting failed
    handle    proxyhelper.Handle // nil until ready
    pid       int; procStart string; sock string; files []string
    lastUsed  time.Time         // guarded by mu
    stopOnce  sync.Once
}
type proxyRecord struct {
    PID int `json:"pid"`; ProcStart string `json:"proc_start"`; Sock string `json:"sock"`
    Files []string `json:"files"`; Origin ipeers.OriginKey `json:"origin"`
}
type helperManager struct {
    mu          sync.Mutex
    helpers     map[ipeers.OriginKey]*helper
    nextGen     uint64
    procCtx     context.Context     // lifetime of every helper process; cancelled by Stop
    procCancel  context.CancelFunc
    start       proxyhelper.Starter
    now         func() time.Time
    procStart   func(pid int) (string, error)
    pidAlive    func(pid int) bool
    liveEntries func() []ipeers.Entry    // for peerFeatures (newest live non-proxy cc entry)
    proxiesPath string
    registryDir, sockDir, version string
    readyTimeout, termGrace time.Duration
    onFrame     func(h *helper, line string)
    log         func(format string, args ...any)
    swept       bool                     // set by Sweep; Acquire refuses until true
}
func newHelperManager(/* fields */) *helperManager

var ( ErrProxyLimit = errors.New("proxy_limit"); ErrProxySpawnFailed = errors.New("proxy_spawn_failed"); ErrNotSwept = errors.New("not_ready") )

// Acquire returns the ready helper for key, spawning one when absent.
// waitCtx bounds only THIS caller's wait; the process runs under procCtx.
//   lock; !swept ⇒ ErrNotSwept
//   h present:
//     helperReady    ⇒ touch lastUsed; unlock; return h
//     helperStarting ⇒ unlock; wait on h.ready or waitCtx; re-lock; if h.err != nil ⇒ return h.err; else return h (state must be ready — a helper cannot go starting→stopping)
//     stopping/exited ⇒ treated as absent for spawning purposes ONLY after it has left the map (Release removes it under mu before it returns); so: unlock, wait on h.exited (a second channel closed by Release), retry the whole Acquire (bounded by waitCtx)
//   h absent: len(helpers) >= HelperCap ⇒ ErrProxyLimit; insert &helper{starting, gen: nextGen++}; unlock
//   cfg := proxyhelper.Config{Name: name, RegistryDir, SockDir, Version, PeerFeatures: peerFeatures(), Cwd: registryDir}
//   handle, err := proxyhelper.Spawn(procCtx, start, cfg, readyTimeout)
//   err ⇒ lock; h.err = wrapped ErrProxySpawnFailed; delete(helpers, key); close(h.ready); close(h.exited); unlock; return
//   ps, err := procStart(handle.PID()); err ⇒ handle.Stop(termGrace); RemoveRegistry(handle.Files()); same failure path
//   lock; fill pid/procStart/sock/files/handle; if err := writeProxiesLocked(); err != nil ⇒ unlock; handle.Stop; RemoveRegistry; failure path (ownership must be durable before ready — M3)
//   h.state = ready; lastUsed = now(); close(h.ready); unlock
//   go pump(h): for line := range handle.Frames() { touch(h); onFrame(h, line) }; then release(h, "exited")
func (m *helperManager) Acquire(waitCtx context.Context, key ipeers.OriginKey, name string) (*helper, error)
// peerFeatures: newest live non-proxy cc entry by ParseProcStart (ties by
// pid desc) ⇒ ccuds.ReadPeerFeatures(registryDir, pid); none/unreadable ⇒
// ccuds.DefaultPeerFeatures.
func (m *helperManager) peerFeatures() []string
func (m *helperManager) Touch(key ipeers.OriginKey)
// Release is instance-bound: it takes the *helper, not the key.
//   lock; if helpers[h.key] != h || h.state != ready ⇒ unlock; return   (stale callback or already stopping — M2)
//   h.state = stopping; delete(helpers, h.key); writeProxiesLocked(); unlock
//   h.stopOnce.Do: handle.Stop(termGrace) (closes stdin ⇒ helper removes its files; grace; SIGKILL); RemoveRegistry(h.files) (idempotent, after Wait); unlink h.sock (ENOENT ok); lock; h.state = exited; close(h.exited); unlock
func (m *helperManager) Release(h *helper, reason string)
func (m *helperManager) ReapIdle()                       // Release every ready helper with lastUsed older than HelperIdleReap
func (m *helperManager) ProxyPIDs() map[int]bool          // pids of starting+ready helpers
func (m *helperManager) FindBySock(sock string) (*helper, bool)
// Sweep (§4.5 Startup sweep), called once from Start BEFORE the HTTP server accepts:
//   read proxiesPath; missing ⇒ []; unparsable ⇒ log, treat as [] (files of unknown helpers are left alone — nothing to prove ownership)
//   for each record:
//     alive := pidAlive(pid) && procStart(pid) == record.ProcStart
//     alive ⇒ signal SIGTERM; wait ≤ termGrace for !pidAlive; SIGKILL; wait ≤ termGrace again; still alive ⇒ log and SKIP its files (never delete under a live process we could not stop)
//     files: unlink each recorded path ONLY if ccuds.RegistryProcStart(path) == record.ProcStart (a reused pid's new files have a different procStart — M4); other files are logged and left
//     sock: unlink ONLY if connecting to it fails with ECONNREFUSED or ENOENT (a live listener is someone else's — M4)
//   write "[]" atomically; error ⇒ return it (Start fails: an unwritable DataDir must not run a daemon that cannot record ownership — M3)
//   swept = true
func (m *helperManager) Sweep() error
// Stop: procCancel is NOT used for graceful stop — Release every ready
// helper (parallel, each bounded by termGrace), wait for all pumps to
// finish (WaitGroup), then procCancel() as the backstop.
func (m *helperManager) Stop()
func (m *helperManager) writeProxiesLocked() error   // temp file in the same dir + fsync + rename
```

**Tests (FakeStarter from Task 3, short dirs, fake clock, fake
`procStart`/`pidAlive` maps):** two concurrent Acquire for one key with a
Barrier starter ⇒ one spawn, both callers get the same helper; Acquire
after ready returns the same instance; cap: 32 helpers ⇒ 33rd
`ErrProxyLimit`, a starting helper counts; Broken starter with
`readyTimeout` 100 ms ⇒ `ErrProxySpawnFailed`, map empty, no socket, every
waiter woken; Refusing ⇒ same; `procStart` failure after ready ⇒ helper
stopped, files removed, `ErrProxySpawnFailed`; `writeProxies` failure
(proxiesPath in a read-only dir) ⇒ same rollback; `proxies.json` after two
spawns: raw JSON keys `pid/proc_start/sock/files/origin{host_id,…}`, no
`.tmp` left; **Release is instance-bound**: capture `h1`, Release it,
Acquire again ⇒ `h2` with a new gen; a late `Release(h1)` is a no-op and
`h2` survives; concurrent `Release(h1)` ×3 ⇒ handle.Stop called once,
record removed once (assert via the fake's counters); Acquire while `h1`
is stopping waits for `exited` and spawns fresh; ReapIdle with the clock
advanced 31 min releases only the idle one; pump delivers frames to
`onFrame` and touches lastUsed; a helper whose stdout closes on its own
(fake exits) is removed from the map and `proxies.json`; **an Acquire
whose waitCtx is cancelled mid-start leaves the helper starting and a
later Acquire gets it ready** (B1); **`Acquire` before `Sweep` ⇒
`ErrNotSwept`**; Sweep: (a) live record (pidAlive true, procStart equal)
⇒ SIGTERM observed, then files unlinked; (b) live record that ignores
SIGTERM and SIGKILL (fake stays alive) ⇒ files kept, logged; (c) dead pid
with files whose `procStart` equals the record ⇒ unlinked; (d) reused pid
— files rewritten by the test with a different procStart and a live
listener at the sock ⇒ files and sock kept; (e) missing file ⇒ nil, `[]`
written; (f) unwritable proxiesPath ⇒ error; Stop releases all and every
pump goroutine has exited (goroutine count check).

- [ ] tests written and failing
- [ ] implementation, all green (`-race`)
- [ ] commit `feat(peers): helper manager — instance-bound lifecycle, durable ownership, sweep`

# Phase D — Daemon endpoints

### Task 7: `POST /api/peers/deliver`

**Files:** create `internal/module/peers/deliver.go`, `deliver_test.go`;
modify `module.go` (constructor, seams, routes, `ProxyPIDs`, Start/Stop),
`fakes_test.go` / `module_test.go` (fixture), `cmd/pdx/main.go`
(`peersmod.New(meta.PeerMessages())`).

**Module changes:**
```go
type AuditStore interface {
    Insert(store.PeerMessage) (int64, error)
    SetResult(id int64, result, errText string) error
    Tail(n int) ([]store.PeerMessage, error)
}
func New(audit AuditStore) *Module   // audit nil ⇒ every send/deliver is audit_unavailable
// new fields: audit; helpers *helperManager; dedup *dedupSet; pairs *pairLimiter;
// writeFrame func(ctx, sock string, line []byte, timeout time.Duration) error (default ccuds.WriteFrame);
// sockWriteTimeout; newMsgID func() string (uuid v4 from crypto/rand);
// deliverClient *http.Client; post postDeliverFunc (declared HERE with a nil default; Task 8 fills it);
// stopCtx/stopCancel; workers sync.WaitGroup; replySem chan struct{} (cap 8).
```
`Init` builds the manager (`filepath.Join(c.Cfg.DataDir, "proxies.json")`,
registry dir, `/tmp/cc-socks`, `proxyhelper.ExecStarter(os.Executable(),
logWriter)`, `liveEntries` = a closure over `ReadRegistry`) and wires
`onFrame` to `m.handleReplyFrame` (Task 9; until then a logging stub).
`Start`: `helpers.Sweep()` (error ⇒ `Start` returns it), then the 1-minute
reap ticker goroutine under `stopCtx`. `Stop`: `stopCancel()`, `workers.Wait()`
(reply workers), `helpers.Stop()`. `localEnvelope` passes
`helpers.ProxyPIDs()`.

**Fixture update (M13):** `module_test.go`'s `&Module{…}` literal becomes a
`newTestModule(t, opts)` helper that sets every seam: `helpers` built with
`FakeStarter`, `dedup`/`pairs` with the test clock, `audit` an in-memory
`store.OpenMeta(":memory:").PeerMessages()`, `writeFrame` real, `post` a
fake that fails loudly if called. Existing P1/P2 assertions unchanged.

**Handler steps (the order is the test contract):**
1. Principal must be `PrincipalHost` (admin ⇒ 403 `admin_not_allowed`);
   `Principal.HostID == ""` ⇒ 403 `host_unverified`.
2. Config snapshot under `RLock`: `Deliver`, local `HostID`, local alias,
   and the entry at `FindPeerHostByAlias(Principal.Alias)`. **Identity
   binding (M1):** the entry must exist and `entry.HostID ==
   Principal.HostID` (both non-empty); otherwise 403 `host_unverified` —
   an alias deleted and recreated for a different host between auth and
   handling can never inherit this request. `entry.AllowBypass` and
   `entry.Token` (return route) are read from this same entry, nothing
   else. `!Deliver` ⇒ 403 `deliver_disabled`.
3. Decode body (1 MiB `MaxBytesReader`), `req.Validate()` ⇒ 400 with the
   specific code; `req.From.HostID != Principal.HostID` ⇒ 403
   `host_unverified`.
4. `dedup.Seen(msg_id)` ⇒ 409 `duplicate` (no new audit row).
5. **Audit first (M8):** `id, err := audit.Insert(PeerMessage{MsgID, DirIn,
   TS, From…, To…, DeclaredMode, EffectiveMode: "", Bytes})`; err ⇒ 503
   `audit_unavailable`. Every later refusal calls `SetResult(id, <code>,
   detail)`.
6. Target: `localEnvelope` ⇒ the record with `Agent != nil && Agent.Type ==
   "cc" && Agent.SessionID == to.AgentSessionID && Agent.PID == to.PID &&
   Agent.ProcStart == to.ProcStart && Deliverable` — else 409
   `target_gone` (detail names which field mismatched, never another
   session's data).
7. `effective := clamp(req.From.DeclaredMode, entry.AllowBypass)`;
   `SetResult` is deferred — the row's `effective_mode` is written by a
   dedicated `SetModes(id, declared, effective)` call here (add it to the
   store interface in this task; a one-line UPDATE).
8. `pairs.Allow(pairKey{From: req.From.Key(), To: req.To.Key(localHostID)})`
   false ⇒ 429 `rate_limited`.
9. `oneWay := entry.Token == ""` (§4.3: delivered anyway, marked, audited
   with error `no_return_route` on a `delivered` result).
10. `helpers.Acquire(r.Context(), req.From.Key(), Principal.Alias + "/" +
    req.From.SessionName)`: `ErrNotSwept` ⇒ 503 `not_ready`;
    `ErrProxyLimit` ⇒ 503 `proxy_limit`; `ErrProxySpawnFailed` ⇒ 502
    `proxy_spawn_failed`; waitCtx cancelled ⇒ 499-style early return with
    `SetResult("", "client_gone")` — the helper keeps starting (B1).
11. `ccuds.BuildFrame(msg_id, h.sock, Wrapper{From: "uds:"+h.sock,
    FromName: h.name, FromMode: effective, HopChain: req.HopChain, Text})`;
    `writeFrame(stopCtx-derived ctx, target.Agent.Inbox, line,
    sockWriteTimeout)`: nil ⇒ `SetResult(delivered, oneWay ?
    "no_return_route" : "")`, `helpers.Touch`, 200 `{msg_id, delivered,
    effective_mode, one_way}`; `ErrPostWriteTimeout` ⇒ `delivery_uncertain`
    (200); other ⇒ `SetResult("", err)`, 502 `socket_write_failed`.

**Tests (httptest module via `newTestModule`, `WithPrincipal` contexts,
fake sessions/owners/liveness, a real Unix listener as the target inbox in
a short dir, FakeStarter helpers, in-memory audit):** happy path — 200
delivered; the listener received exactly one line whose `ParseFrame` gives
`from == "uds:"+helper sock`, `msg_id` equal, wrapper `from-name
"air/foo"`, `from-mode prompting`, text verbatim; audit row `in` has both
modes and result; **the helper survives the request context**: cancel the
request's ctx after the response, write a frame into the helper's socket,
`onFrame` fires (B1); declared bypass with `AllowBypass=false` ⇒ prompting,
true ⇒ bypass; `hop_chain` carried; `one_way` true + audit error
`no_return_route` when the entry has no `Token`; admin ⇒ 403; unverified
principal ⇒ 403; **alias recreated for another host_id after auth** (build
the principal with the old HostID, config entry with a new one) ⇒ 403 and
no delivery (M1); `Deliver=false` ⇒ 403; `from.host_id` mismatch ⇒ 403;
65537-byte text ⇒ 400; duplicate ⇒ 409 and one line on the listener;
wrong pid ⇒ 409 `target_gone` **with an audit row** (M8); inbox_dead row ⇒
409; a `to` that is a proxy row (own helper AND another daemon's helper
recognised via `IsProxy`) ⇒ 409; 31st message in a minute ⇒ 429 audited;
audit Insert failure ⇒ 503 and nothing on the socket; `proxy_limit` /
`proxy_spawn_failed` / `not_ready` (before Sweep) mappings with audit
results; listener that reads but never closes (`sockWriteTimeout` 100 ms)
⇒ 200 `delivery_uncertain`; listener absent ⇒ 502 with audit error;
`ProxyPIDs` makes the helper's own registry entry a `proxy` row in `GET
/api/peers`.

- [ ] tests written and failing
- [ ] implementation, all green (`-race`)
- [ ] commit `feat(peers): POST /api/peers/deliver — bind identity, audit first, clamp, helper, socket write`

### Task 8: `POST /api/peers/send` and the outbound client

**Files:** create `internal/module/peers/send.go`, `send_test.go`; modify
`module.go` (route; `post` default = `postDeliver`; `deliverClient` default).

**Produce:**
```go
type postDeliverFunc func(ctx context.Context, client *http.Client, baseURL, bearer string, req ipeers.DeliverRequest) (ipeers.DeliverResponse, *ipeers.RemoteError, error)
// postDeliver POSTs <baseURL>/api/peers/deliver, 64 KiB response cap (a
// deliver answer is tiny), no redirects. 200 ⇒ (resp, nil, nil); other
// status ⇒ (zero, &RemoteError{Status, Error, Detail} from an APIError
// body — else Error "http_<code>", nil); transport error ⇒ (zero, nil, err).
func postDeliver(...)
func newDeliverClient() *http.Client   // Timeout InterDaemonTimeout, no redirects
```
**Handler steps:**
1. Principal must be admin (403 otherwise).
2. Decode (1 MiB cap); `ValidateText` ⇒ 400 `text_too_large` / `bad_request`;
   `ValidateMode` ⇒ 400 `bad_mode`; `SplitAddress(to)` ⇒ 400 `bad_address`;
   `origin_inbox == ""` ⇒ 400 `origin_unknown`.
3. Config snapshot: local host id/alias, cloned hosts. Host part matches the
   local alias or host id ⇒ 400 `local_target` (D1). Otherwise the entry by
   `HostMatches(host, h.Alias, h.HostID)`; none ⇒ 404 `host_unknown`;
   `Token == ""` or `HostID == ""` ⇒ 409 `host_unverified` (detail: run
   `pdx peers host set-token`).
4. Origin: `localEnvelope` ⇒ the record with `Agent != nil && Agent.Type ==
   "cc" && Agent.Inbox == origin_inbox && Deliverable` (proxy rows — own or
   foreign — never qualify) ⇒ else 400 `origin_unknown`. `from :=
   WireFrom{HostID: local, …, SessionName: rec.SessionName or
   "cc:"+PeerName, DeclaredMode: mode}`.
5. Remote snapshot: `m.fetch(ctx 3 s, m.client, h.URL, h.Token)` error ⇒
   502 `remote_error{Status 0}`; `env.HostID != h.HostID` ⇒ 502 (`host_id
   mismatch`); `!env.OK` ⇒ 502. `rows := normalizeRemoteRows(env.Peers,
   h.Alias, h.HostID)`.
6. `ipeers.Resolve(rows, session)`: `ErrNotFound` ⇒ 404 `peer_not_found`;
   `*AmbiguousError` ⇒ 409 `ambiguous` with normalised candidate addresses;
   not `Deliverable` ⇒ 409 `not_deliverable` (detail = `Reason`).
7. `msg_id := newMsgID()`; `id, err := audit.Insert(DirOut, …)` ⇒ 503
   `audit_unavailable`.
8. `post(ctx 10 s under stopCtx, deliverClient, h.URL, h.Token,
   DeliverRequest{MsgID, From, To: {session, pid, proc_start}, Text})`:
   transport error ⇒ `SetResult("", err)`, 502 `remote_error`; RemoteError ⇒
   `SetResult(remote.Error, remote.Detail)`, 502 with `Remote`; success ⇒
   `SetModes`, `SetResult(resp.Result, resp.OneWay ? "no_return_route" :
   "")`, 200 `SendResponse{…, OneWay}`.

**Tests:** `postDeliver` against httptest: 200 decode; 409 APIError ⇒
RemoteError with code/detail; 500 HTML ⇒ `http_500`; 65 KiB body ⇒ error;
timeout with a 50 ms client; bearer/path asserted. Handler: happy path
with fake `fetch` + fake `post` capturing the request ⇒ `from` equals the
origin row, `to` equals the resolved tuple, `msg_id` UUID, response
echoes result/mode/one_way, audit row `out`; **the remote snapshot's rows
claim a different host_id and alias ⇒ the request still targets the
config entry and `to_address` uses the entry's alias** (P2 #5); each error
step with status/code (table-driven); `local_target` for alias and host
id; origin pointing at a proxy row (own helper, and a foreign helper via
`IsProxy`) ⇒ `origin_unknown`; `ambiguous` carries both candidate
addresses; remote `target_gone` ⇒ 502 `remote.error == "target_gone"`,
audit result `target_gone`; the fake `post` asserts the bearer is the
entry's `Token` and nothing else.

- [ ] tests written and failing
- [ ] implementation, all green (`-race`)
- [ ] commit `feat(peers): POST /api/peers/send — origin attribution, remote resolve, outbound deliver`

### Task 9: Reply path and `GET /api/peers/log`

**Files:** create `internal/module/peers/reply.go`, `reply_test.go`; modify
`module.go` (route; `onFrame` wiring).

**`handleReplyFrame(h *helper, line string)`** runs on the helper's pump
goroutine. It acquires `replySem` **synchronously** (back-pressure onto
that helper's stdout pipe is acceptable; it blocks only that helper), then
`workers.Add(1)` and processes in a goroutine under `stopCtx`; `Stop`
waits for `workers`. Steps:
1. `ccuds.ParseFrame(line)`; error or `Type != "user"` ⇒ log and drop.
2. `FromSocket(frame.From)` false ⇒ audit `reply` row result
   `replier_unknown` (from_session_id ""), drop.
3. `helpers.FindBySock(sock)` hit, **or** the inventory row for `sock` is a
   proxy row (foreign helper, D9) ⇒ audit `proxy_to_proxy`, drop.
4. `localEnvelope` ⇒ the record with `Agent.Type == "cc" && Agent.Inbox ==
   sock && Deliverable`; none ⇒ audit `replier_unknown`, drop.
5. `w, ok := ccuds.Parse(content)`; text = `w.Text` when ok else whole
   content; `declared := ValidateMode(w.FromMode)` or prompting (D3); `hop
   := w.HopChain`. `ValidateText` failure ⇒ audit `text_too_large`, drop.
6. Return route: config snapshot; the entry with `HostID == h.key.HostID &&
   Token != ""`; none ⇒ audit `no_return_route`, drop.
7. `msg_id := newMsgID()` (D11); `id, err := audit.Insert(DirReply,
   NativeMsgID: frame.MsgID, from replier, to h.key)`; error ⇒ log, drop.
8. `post(ctx 10 s, deliverClient, entry.URL, entry.Token, DeliverRequest{
   MsgID, HopChain: hop, From: replier tuple, To: WireTo(h.key), Text})`:
   success ⇒ `SetModes`, `SetResult`, `helpers.Touch(h.key)`; RemoteError
   `target_gone` ⇒ `SetResult`, `helpers.Release(h, "origin gone")`;
   other ⇒ `SetResult` only.

**`GET /api/peers/log?tail=N`** (admin; hosts refused by policy and in
depth): default 50, max 1000, 400 on a non-integer; `{"messages":[…]}`
with `ts` RFC 3339 ms and every column incl. `native_msg_id`.

**Tests:** drive `handleReplyFrame` with a real helper from the manager
(FakeStarter): happy path ⇒ fake `post` receives `to == h.key`, `from ==
replier tuple`, `hop_chain`, `declared_mode` from the wrapper, text
unwrapped, `msg_id` is a fresh UUID ≠ the native one and the audit row
has `native_msg_id`; plain content ⇒ whole content, prompting; non-user
frame dropped without audit; `from` without `uds:` ⇒ `replier_unknown`;
`from` = own helper's sock ⇒ `proxy_to_proxy`; `from` = a foreign proxy
row ⇒ `proxy_to_proxy`; unknown sock ⇒ `replier_unknown`; no return route
⇒ `no_return_route`, `post` never called; remote `target_gone` ⇒ helper
released (socket gone, map empty); audit insert failure ⇒ no post;
**eight replies blocked inside a slow fake `post` then `Stop`** ⇒ Stop
returns within 2× the fake's delay and no goroutine leaks; the ninth frame
blocks the pump until a slot frees (assert with a channel); log endpoint
tail/ordering/400/403.

- [ ] tests written and failing
- [ ] implementation, all green (`-race`)
- [ ] commit `feat(peers): forward native replies through the return route; GET /api/peers/log`

### Task 10: Two-daemon end-to-end test

**Files:** create `internal/module/peers/e2e_test.go`.

Two real `Module`s (A and B) via `newTestModule`, each wrapped in
`middleware.PeerAuth(tokenFn, peersFn, HostRoutePolicy)` so principals are
real, paired both ways (tokens, host ids, `Deliver: true`), real
`fetchRemote` and `postDeliver` over httptest, real `ccuds.WriteFrame`,
FakeStarter helpers, **one shared short socket dir and one shared registry
dir** (two daemons on one machine share both — Phase F reproduces this).
Fake "cc sessions": test-owned Unix listeners `origin.sock` (A, tmux
`mt1`) and `target.sock` (B, `foo`), each with a registry file and a fake
liveness whose `Info` returns `Argv{"claude"}` for them and
`Argv{"pdx","peer-proxy"}` for every helper pid (so each daemon sees the
other's helpers as proxies, D9).

Flow asserted end-to-end:
1. `POST A /api/peers/send {to:"b/foo", text:"ping", origin_inbox: origin.sock}`
   ⇒ 200 `one_way:false`; `target.sock` receives one frame: wrapper
   `from-name "a/mt1"`, `from-mode prompting`, text `ping`, `from ==
   "uds:"+B-helper sock`.
2. The test, as the target Claude, writes a native reply into the
   B-helper's socket (`Wrapper{From: "uds:"+target.sock, FromName: "foo",
   FromMode: "bypass", HopChain: "abc", Text: "pong"}`).
3. `origin.sock` receives one frame within 2 s: `from-name "b/foo"`,
   `from-mode prompting` (A's entry for B has `AllowBypass false`),
   `hop-chain "abc"`, text `pong`, `from == "uds:"+A-helper sock`.
4. Audit: A `out delivered`, `in delivered`; B `in delivered`, `reply
   delivered` with `native_msg_id`.
5. A's entry `AllowBypass true`, repeat 2 ⇒ `from-mode bypass`.
6. **Shared-registry boundary (M10):** `GET A /api/peers` lists B's helper
   as `proxy`/not deliverable; `POST A /send` with `origin_inbox` = B's
   helper sock ⇒ 400 `origin_unknown`; `POST B /deliver` (as A) with `to`
   = A's helper tuple ⇒ 409 `target_gone`; a frame written into B's helper
   with `from` = A's helper sock ⇒ B audits `proxy_to_proxy`, nothing
   forwarded.
7. Kill the origin listener + registry file (A's liveness says dead);
   repeat 2 ⇒ B's `reply` row `target_gone`, B's helper for A/mt1 released.
8. `POST A /send` again ⇒ 400 `origin_unknown`.
9. `Stop` both modules ⇒ every helper socket gone, both `proxies.json` are
   `[]`, no goroutine leak.

- [ ] test written and failing (compiles against Tasks 7–9)
- [ ] all green (`-race -count=20`)
- [ ] commit `test(peers): two-daemon end-to-end delivery, reply, clamp, proxy boundary, origin-gone`

# Phase E — CLI

### Task 11: `pdx msg send|log|deliver`

**Files:** create `cmd/pdx/msg.go`, `msg_test.go`; modify `cmd/pdx/main.go`
(switch case `msg`).

**Grammar (hand parser in the style of `parsePeersInvocation`; every
rejection ⇒ usage on stderr, exit 2, no config load, no request):**
```
pdx msg send <host>/<session> <text> [--mode prompting|bypass] [--json] [--config <path>]
pdx msg log [--tail N] [--json] [--config <path>]
pdx msg deliver <on|off|status> [--json] [--config <path>]
pdx msg selftest [--timeout <dur>] [--config <path>]        (Task 12)
```
- `send`: `origin_inbox = getenv("CLAUDE_CODE_MESSAGING_SOCKET")` (injectable);
  empty ⇒ stderr `pdx msg: origin_unknown: CLAUDE_CODE_MESSAGING_SOCKET is
  unset — run inside a Claude Code session`, exit 1, no request (m3: the
  same code the daemon would return). POST `/api/peers/send`, 15 s client.
  200 ⇒ `sent <msg_id> → <to_address> (<result>, mode <effective_mode>[,
  one-way])`; `--json` passthrough. Error ⇒ `pdx msg: <error>[: <detail>]`;
  `remote_error` ⇒ `pdx msg: <host>: <remote.error>[: <detail>]`;
  `ambiguous` ⇒ one candidate per line; exit 1.
- `log`: GET `/api/peers/log?tail=N` (default 50); table `TIME DIR MSG_ID
  FROM TO MODE BYTES RESULT ERROR` (MSG_ID first 8 chars, FROM/TO
  `<host_id>/<session_id[:8]>`, MODE `decl→eff` as `p→p`/`b→p`/`b→b`, TIME
  local `15:04:05`).
- `deliver on|off` ⇒ PUT `/api/peers/settings {deliver}`, print `deliver:
  on|off`; `status` ⇒ GET, same output.

**Tests:** grammar table (exit 2, zero requests); `send` without the env
var ⇒ exit 1, stderr contains `origin_unknown`, zero requests; `send`
posts the expected body incl. `origin_inbox` and prints the success line
(with and without `one-way`); error rendering for `remote_error`,
`ambiguous`, `origin_unknown`; `log` golden table; `deliver on` PUT body;
`--json` passthrough for all three.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(cli): pdx msg send|log|deliver`

### Task 12: `pdx msg selftest`

**Files:** create `cmd/pdx/msg_selftest.go`, `msg_selftest_test.go`; modify
`cmd/pdx/msg.go` (verb).

**Dependencies struct (`selftestDeps`, every field injectable, production
values in `newSelftestDeps()`):** `tmux func(ctx, args ...string) ([]byte,
error)`, `registryDir`, `sockDir`, `readRegistry func(dir) ([]ipeers.Entry,
error)` (real: `ReadRegistry` with `DefaultLiveness`), `spawn func(ctx,
proxyhelper.Config) (proxyhelper.Handle, error)` (real:
`proxyhelper.Spawn(ctx, ExecStarter(os.Executable(), stderr), cfg, 3 s)`),
`writeFrame`, `pidAlive`, `procStart func(pid) (string, error)`, `signal
func(pid int, sig os.Signal) error`, `readPeerFeatures`, `sleep func(ctx,
d)` (real: timer/ctx select), `now`.

**Body (`runMsgSelftest(ctx, deps, timeout, stdout, stderr) int`):**
1. `name := "pdx-selftest-" + 6 hex`. `tmux new-session -d -s <name> --
   claude -p --verbose --input-format stream-json --output-format
   stream-json --name <name> --settings {"crossSessionInbound":"accept"}`
   (argv passed as separate arguments to tmux — tmux joins them; no shell
   quoting). Cleanup is registered before this call and runs on every
   exit path incl. `signal.NotifyContext` cancellation.
2. Poll ≤ 15 s (250 ms `sleep`): `readRegistry` for the non-proxy entry
   whose `TmuxSessionName() == name`; record its **pid, procStart, Inbox
   and registry files** (`RegistryFiles` derived from the json path +
   glob `<pid>.*.key`). None ⇒ `FAIL: session did not register (Claude
   Code ≥ 2.1.224 with peer messaging required)`, exit 1.
3. `features := readPeerFeatures(registryDir, target.PID)` or
   `DefaultPeerFeatures`; `h := spawn(ctx, Config{Name: "pdx-selftest-probe",
   RegistryDir, SockDir, Version: VerifiedCCVersion, PeerFeatures:
   features})` — a **real `pdx peer-proxy` subprocess** (D4/M11); failure ⇒
   `FAIL: helper did not start: …`, exit 1.
4. `nonce := 8 hex`; `writeFrame(target.Inbox, BuildFrame(uuid, h.Sock(),
   Wrapper{From: "uds:"+h.Sock(), FromName: "pdx-selftest-probe", FromMode:
   prompting, Text: "PDX_SELFTEST " + nonce + ": reply with exactly: PONG "
   + nonce}), 5 s)`; error ⇒ `FAIL: write to <inbox>: …`.
5. Wait ≤ `timeout` (default 60 s) on `h.Frames()` for a frame with `Type
   == "user"`, `FromSocket(From) == target.Inbox` and content containing
   `nonce` ⇒ `PASS: reply from <name> via helper pid <p> in <elapsed>`,
   exit 0; timeout ⇒ `FAIL: no reply within <timeout>`, exit 1; a frame
   from the target without the nonce ⇒ `note:` and keep waiting;
   `Frames()` closed (helper died) ⇒ `FAIL: helper exited`.
6. **Cleanup (M12), always, in this order, each step reported on stdout
   and a failure turning the exit code to 1 with `cleanup incomplete`:**
   `h.Stop(2 s)`; `tmux kill-session -t <name>`; wait ≤ 5 s for
   `!pidAlive(target.PID) || procStart(target.PID) != target.ProcStart`,
   else SIGTERM, wait 2 s, SIGKILL, wait 2 s; then, for each of the
   target's registry files still present whose `RegistryProcStart` equals
   `target.ProcStart`, unlink it, and unlink `target.Inbox` if connecting
   is refused; finally verify the probe's files (`h.Files()`) and socket
   are gone (remove if not). Print `cleanup: ok` or `cleanup incomplete:
   <what remains>`.

**Tests (all deps faked; no tmux/claude/real dirs):** `new-session` and
`kill-session` argv golden; entry appears on the 3rd poll ⇒ proceeds;
never appears ⇒ FAIL and kill-session still called; the frame written to
the target inbox parses with the nonce and `from` = the fake handle's
sock; reply with the nonce ⇒ PASS; frame from another socket ignored;
`Frames` closed ⇒ FAIL helper exited; timeout ⇒ FAIL and cleanup called;
spawn failure ⇒ FAIL, kill-session called; **cleanup: target pid stays
alive after kill-session ⇒ SIGTERM then SIGKILL observed; leftover target
registry files with matching procStart are removed and non-matching ones
kept; a leftover probe socket is removed; a cleanup step failing ⇒ exit 1
with `cleanup incomplete`**; ctx cancelled mid-wait ⇒ cleanup runs.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(cli): pdx msg selftest — Claude Code peer protocol upgrade gate through a real helper`

# Phase F — Acceptance (main session)

### Task 13: Two daemons on mlab (one machine, real Claude Code sessions)

- [ ] `git merge origin/main` (post-P2) already done; `make build`.
- [ ] P2 acceptance layout (`/tmp/pdx-p3/{a,b}`, ports 7861/7862, distinct
      `data_dir`, non-empty admin tokens, both `data_dir`s holding a copy of
      the production `agent_events.db` so both resolve owners); pair both
      ways; `pdx msg deliver on --config …/{a,b}/config.toml`.
- [ ] Two real Claude Code sessions in tmux: `mt-origin` and `mt-target`
      (`--settings '{"crossSessionInbound":"accept"}'`). Inside `mt-origin`:
      `pdx msg send b/mt-target "ping from a" --config /tmp/pdx-p3/a/config.toml`
      ⇒ `sent <id> → b/mt-target (delivered, mode prompting)`.
- [ ] `mt-target` sees `<cross-session-message from="uds:/tmp/cc-socks/<helper
      pid>.sock" from-name="a/mt-origin" from-mode="prompting">`; its
      `SendMessage` reply appears in `mt-origin` as `from-name="b/mt-target"`.
- [ ] `pdx msg log` on a: `out delivered`, `in delivered`; on b: `in`,
      `reply` with `native_msg_id`. `pdx peers` on **both** daemons shows
      both helpers as `proxy` rows (D9). `cat /tmp/pdx-p3/b/data/proxies.json`
      has one record with the full origin tuple.
- [ ] `--mode bypass` ⇒ target sees `prompting`; `pdx peers host set-token a
      <T> --allow-bypass=true --config …/b/…` ⇒ `bypass`.
- [ ] Negative: `env -u CLAUDE_CODE_MESSAGING_SOCKET pdx msg send …` ⇒
      `origin_unknown`; `pdx msg send a/mt-target …` ⇒ `local_target`;
      `pdx msg deliver off` on b ⇒ `b: deliver_disabled`; 31 sends in a loop
      ⇒ 31st `b: rate_limited`; `curl -X POST …7862/api/peers/deliver -H
      'Authorization: Bearer btoken'` ⇒ 403 `admin_not_allowed`; remove b's
      entry for a (`host remove`) then send from a ⇒ `one-way` in the
      output and `no_return_route` in a's log.
- [ ] Restart b with a helper alive: log shows the sweep terminating the
      old pid, `proxies.json` is `[]`, no stale `<pid>.json`; a helper whose
      pid was reused by an unrelated process (simulate by editing the
      record's pid to a live shell's pid) keeps that process's files.
- [ ] `pdx msg selftest` ⇒ `PASS … via helper pid …`, `cleanup: ok`; `tmux
      ls` has no `pdx-selftest-*`; no `pdx-selftest*` registry files; no
      `pdx peer-proxy` process left (`pgrep -f peer-proxy`).
- [ ] Tear down (`pdx stop` both, `rm -rf /tmp/pdx-p3`); paste transcripts
      into the PR.

### Task 14: mlab ↔ air-2026 (after deploy)

- [ ] Deploy alpha.N on both hosts; pair both ways; `pdx msg deliver on` on
      both. From a cc session on mlab: `pdx msg send air/<s> "ping"` arrives
      on air as `mini-lab/<s>`; the native reply returns; `pdx msg log` on
      both hosts shows the four rows; `pdx msg selftest` passes on both.
      Record in the PR and in memory `kickoff_peer_bridge`.

---

## Review disposition (codex plan review `task-mu07l94z-oszksd`)

| # | Sev | Finding | Disposition |
|---|---|---|---|
| B1 | Blocker | helper process bound to the HTTP request ctx | manager-owned `procCtx`; `Acquire(waitCtx)` bounds only the wait; `Spawn(ctx=process)`; tests for helper surviving request cancellation and a caller cancelled mid-start (Task 3, 6, 7) |
| M1 | Major | identity and `allow_bypass` from different config snapshots | handler re-reads the entry by alias in one snapshot and requires `entry.HostID == Principal.HostID`; alias-recreated test (Task 7) |
| M2 | Major | Release keyed by origin, no instance identity / once semantics | state machine starting→ready→stopping→exited, `gen`, `Release(*helper)`, `stopOnce`, `exited` channel; concurrency tests (Task 6) |
| M3 | Major | ownership persistence failure undefined; sweep errors ignored | ready only after `procStart` + atomic `proxies.json` succeed, else stop + remove; `Sweep` error fails `Start`; `Acquire` refuses before sweep (`not_ready`) (Task 6, 7) |
| M4 | Major | sweep deletes by basename ⇒ pid-reuse deletes a new Claude's files | unlink files only when their `procStart` equals the record's; unlink sock only when refused; `RegistryProcStart` (Task 2, 6) |
| M5 | Major | Close/shutdown ignores accepted conns, blocked sends, reply workers | `VirtualPeer` tracks conns, cancellable `Frames` send, closes channel after readers exit; helper treats stdout error as EOF; reply workers under `stopCtx` + WaitGroup, semaphore acquired before spawning (Task 2, 3, 9) |
| M6 | Major | `peerFeatures` never sourced | `ccuds.ReadPeerFeatures`, `DefaultPeerFeatures`, manager `peerFeatures()` newest live non-proxy entry per spawn; `Entry` stays comparable (Task 2, 6) |
| M7 | Major | audit PK turns 10-min dedup into permanent refusal | autoincrement `id`, `(msg_id, direction)` indexed not unique, `SetResult(id)` (D10, Task 4) |
| M8 | Major | no one-way marking; `target_gone` unaudited | `one_way` in responses + audit error `no_return_route`; audit row inserted before target verification, every refusal `SetResult`s (Task 1, 7, 8) |
| M9 | Major | reply `msg_id` taken from the harness | B mints; native id kept in `native_msg_id` (D11, Task 4, 9) |
| M10 | Major | another daemon's helpers pass as cc rows on a shared registry | proxy recognition by process argv via `Liveness.Info` / `Entry.IsProxy` (D9, Task 5); boundary asserted in Task 10 and Phase F |
| M11 | Major | selftest bypasses the helper spec §4.8 requires | selftest spawns a real `pdx peer-proxy` through `proxyhelper.Spawn` (D4, Task 3, 12) |
| M12 | Major | selftest cleanup does not guarantee the target's teardown | pid/procStart-tracked kill escalation, procStart-checked file removal, verified probe cleanup, `cleanup incomplete` exit 1 (Task 12) |
| M13 | Major | fixtures/seams insufficient for TDD | `newTestModule` fixture rewrite; `postDeliverFunc` declared in Task 7; `Handle` interface + `selftestDeps` with injectable sleep/tmux/signals (Task 3, 7, 12) |
| m1 | Minor | `normalizeRemoteRows` inputs / unparsable Address | `(rows, alias, hostID)`; Address rebuilt from session name / `cc:` / code (Task 5) |
| m2 | Minor | `proxies.json.origin` keys | JSON tags on `OriginKey`, raw-key test (Task 1, 6) |
| m3 | Minor | CLI env-unset error lacks `origin_unknown` | `pdx msg: origin_unknown: …` (Task 11) |
