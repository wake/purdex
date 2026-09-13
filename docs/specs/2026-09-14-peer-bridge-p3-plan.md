# Peer Bridge P3 Implementation Plan — Delivery (cc-uds only)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Plan v1 (pre codex review).

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
peer) is isolated in `internal/peers/ccuds`; the helper's stdio protocol in
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
module's `fetch` seam (`fetchFunc`, `fetchRemote`, `remoteFetchTimeout`).
**Implementation starts only after P2 is merged**: first `git merge
origin/main` into this worktree, re-read the P2 files named above, and
adjust any signature this plan quotes that drifted.

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
  sockets under a short temp dir.
- **Unix socket paths in tests** must stay under macOS's 104-byte
  `sun_path` limit: use `os.MkdirTemp("/tmp", "pdxp")` (never
  `t.TempDir()`, whose path is ~90 bytes) and `t.Cleanup(os.RemoveAll)`.
- **Secrets never reach a response, a log, the helper, or `proxies.json`.**
  The helper is spawned with an empty environment plus `PATH`/`HOME` only.
- **Package boundaries:** `internal/peers` and its subpackages stay leaves
  (stdlib + `internal/agent`); `internal/peers/ccuds` may import
  `internal/peers`; `internal/peers/proxyhelper` may import `ccuds`.
  `internal/store` gains no new imports. `internal/module/peers` imports
  all of them; `cmd/pdx/msg.go` imports `internal/peers`, `ccuds`, and
  `internal/config` only (never the module).
- **Every constant from the spec is a named constant** in one place
  (`internal/peers/wire.go` for wire limits, `internal/module/peers/helpers.go`
  for helper lifecycle): `MaxTextBytes = 64 KiB`, `DedupWindow = 10 min`,
  `PairRateLimit = 30/min`, `InterDaemonTimeout = 10 s`, `SocketWriteTimeout
  = 5 s`, `HelperCap = 32`, `HelperIdleReap = 30 min`, `HelperReadyTimeout =
  3 s`, `HelperTermGrace = 2 s`, `VerifiedCCVersion = "2.1.270"`.
- **Clocks and processes are injectable.** Nothing in
  `internal/module/peers` calls `time.Now`, `exec.Command`, `os.Getpid`,
  `syscall.Kill` or `ps` directly; each goes through a seam set by `New()`
  and overridden in tests.

## Decisions taken in this plan (spec-silent points; reviewers may challenge)

| # | Point | Decision |
|---|---|---|
| D1 | `/send` to the **local** host | Refused with 400 `local_target`. Same-host Claude Code sessions already reach each other natively (`ListAgents`/`SendMessage`); bridging them through a helper would only add a second, worse path. |
| D2 | `from-name` alias in the wrapper written on B | B's **own configured alias** for the matched host entry (`Principal.Alias`), not a string A sends. It is the name B's user would type in `pdx msg send <alias>/…`, and it is not attacker-controlled. The wire carries `from.session_name` (tmux session name, or `cc:<peer_name>` outside tmux) for the part after `/`. |
| D3 | Reply `declared_mode` | The `from-mode` attribute Claude Code wrote into the reply wrapper (it is that harness's own report of its mode), `prompting` when absent or unparsable. A clamps it by its entry for B exactly like a first-hop message. |
| D4 | Selftest topology | The `pdx msg selftest` process **is** the virtual peer (one process = one peer, spec §3.3), talking directly to the throwaway session's socket. No daemon, no helper subprocess, no audit: the gate tests the undocumented harness contract (§4.8), which is exactly the part unit tests cannot cover. The daemon pipeline is covered by Task 10's two-daemon test and by Phase F. |
| D5 | Dedup hit | 409 `duplicate` (not silently 200): the sender never resends, and the audit row shows what happened. |
| D6 | `peers.deliver` toggle | `PUT /api/peers/settings {deliver}` (admin) via `Core.UpdateConfig`, `GET /api/peers/settings`; CLI `pdx msg deliver on|off|status`. `PUT /api/config` keeps its field whitelist untouched. |
| D7 | Helper `procStart` | Read with `TZ=UTC ps -p <pid> -o lstart=` (one fork per spawn), byte-identical to what the harness computes — not reformatted from `ReadProcessInfo`, whose sub-second truncation is unverified. `pidDomain` = `runtime.GOOS` (only `darwin` verified). |
| D8 | Helper config channel | The daemon writes **one JSON line** on the helper's stdin (`name`, `registry_dir`, `sock_dir`, `version`, `peer_features`) before anything else; the helper answers with the ready line. Nothing else ever travels daemon→helper; stdin EOF is the shutdown signal (§4.5). |

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/peers/wire.go` *(new)* | `DeliverRequest`/`DeliverResponse`/`SendRequest`/`SendResponse`/`APIError`, `WireFrom`/`WireTo` tuples, mode constants, error codes, `MaxTextBytes`, `ValidateText`, `ValidateMode` |
| `internal/peers/ccuds/wrapper.go` *(new)* | `Wrapper{From, FromName, FromMode, HopChain, Text}`, `Format`, `Parse` |
| `internal/peers/ccuds/frame.go` *(new)* | `Frame` wire struct, `BuildFrame`, `ParseFrame`, `WriteFrame` (dial + deadline + write + close; `ErrWriteIncomplete` vs `ErrPostWriteTimeout`) |
| `internal/peers/ccuds/registry_write.go` *(new)* | `RegistryFiles`, `WriteRegistry` (O_EXCL, 0600 key, rollback), `RemoveRegistry` |
| `internal/peers/ccuds/virtual_peer.go` *(new)* | `VirtualPeer`: bind `<sockdir>/<pid>.sock`, register, accept loop → `Frames() <-chan string`, `Close` |
| `internal/peers/ccuds/version.go` *(new)* | `VerifiedCCVersion`, `NewerThanVerified(v) bool` |
| `internal/peers/proxyhelper/proxyhelper.go` *(new)* | `Run(ctx, stdin, stdout, Options)` — the `pdx peer-proxy` body |
| `cmd/pdx/peer_proxy.go` *(new)* | `peer-proxy` subcommand → `proxyhelper.Run` with production options |
| `internal/store/peer_message.go` *(new)* | `peer_messages` table on `meta.db`; `PeerMessageStore` |
| `internal/store/meta.go` *(modify)* | migration adds `peer_messages`; `(*MetaStore).PeerMessages()` |
| `internal/config/config.go` *(modify)* | `PeersConfig.Deliver bool` |
| `internal/module/peers/policy.go` *(modify)* | host may `POST /api/peers/deliver` |
| `internal/module/peers/settings.go` *(new)* | `GET`/`PUT /api/peers/settings` |
| `internal/module/peers/limits.go` *(new)* | `dedupSet`, `pairLimiter` (injectable clock) |
| `internal/module/peers/helpers.go` *(new)* | `helperManager`: spawn seam, ready handshake, cap, per-key serialisation, `proxies.json`, idle reap, origin-gone reap, Stop, startup sweep |
| `internal/module/peers/deliver.go` *(new)* | `POST /api/peers/deliver` |
| `internal/module/peers/send.go` *(new)* | `POST /api/peers/send`, `postDeliver` client |
| `internal/module/peers/reply.go` *(new)* | helper frame → replier → return route → `postDeliver`; `GET /api/peers/log` |
| `internal/module/peers/module.go` *(modify)* | `New(audit)`, seams, routes, `ProxyPIDs` from the manager, version warning, Start/Stop |
| `cmd/pdx/main.go` *(modify)* | `peer-proxy`, `msg` subcommands; `peersmod.New(meta.PeerMessages())` |
| `cmd/pdx/msg.go` *(new)* | `pdx msg send|log|deliver|selftest` |
| `cmd/pdx/msg_selftest.go` *(new)* | selftest body |

---

# Phase A — Leaf packages

### Task 1: Wire types and limits (`internal/peers/wire.go`)

**Files:** create `internal/peers/wire.go`, `wire_test.go`.

**Produce:**
```go
const (
    MaxTextBytes = 64 * 1024
    ModePrompting = "prompting"
    ModeBypass    = "bypass"
)
// Error codes (the "error" field of every 4xx/5xx JSON body on /send, /deliver).
const (
    ErrBadRequest = "bad_request"; ErrTextTooLarge = "text_too_large"; ErrBadMode = "bad_mode"
    ErrBadAddress = "bad_address"; ErrLocalTarget = "local_target"; ErrHostUnknown = "host_unknown"
    ErrOriginUnknown = "origin_unknown"; ErrPeerNotFound = "peer_not_found"; ErrAmbiguous = "ambiguous"
    ErrNotDeliverable = "not_deliverable"; ErrRemoteError = "remote_error"
    ErrHostUnverified = "host_unverified"; ErrDeliverDisabled = "deliver_disabled"; ErrAdminNotAllowed = "admin_not_allowed"
    ErrTargetGone = "target_gone"; ErrDuplicate = "duplicate"; ErrRateLimited = "rate_limited"
    ErrAuditUnavailable = "audit_unavailable"; ErrProxyLimit = "proxy_limit"; ErrProxySpawnFailed = "proxy_spawn_failed"
    ErrSocketWriteFailed = "socket_write_failed"
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
}
type SendRequest struct {
    To          string `json:"to"`           // "<host>/<session>"
    Text        string `json:"text"`
    Mode        string `json:"mode,omitempty"` // "" ⇒ prompting
    OriginInbox string `json:"origin_inbox"`
}
type SendResponse struct {
    MsgID         string `json:"msg_id"`
    ToHostID      string `json:"to_host_id"`
    ToAddress     string `json:"to_address"`     // resolved "<alias>/<session>" on the target host
    To            WireTo `json:"to"`
    Result        string `json:"result"`
    EffectiveMode string `json:"effective_mode"`
}
type APIError struct {
    Error      string       `json:"error"`
    Detail     string       `json:"detail,omitempty"`
    Candidates []string     `json:"candidates,omitempty"` // ambiguous: addresses
    Remote     *RemoteError `json:"remote,omitempty"`     // remote_error: the other daemon's answer
}
type RemoteError struct { Status int `json:"status"`; Error string `json:"error"`; Detail string `json:"detail,omitempty"` }

// OriginKey is the helper key and the audit/session identity of a sender.
type OriginKey struct{ HostID, AgentSessionID string; PID int; ProcStart string }
func (f WireFrom) Key() OriginKey
func (t WireTo) Key(hostID string) OriginKey

// ValidateText: non-empty, valid UTF-8, len ≤ MaxTextBytes. ValidateMode:
// "" | prompting | bypass (returns the normalised mode). ValidateDeliver:
// msg_id is a UUID (36 chars, RFC 4122 shape), from/to tuples complete
// (host_id, session ids, pid > 0, proc_start parseable via ParseProcStart),
// declared_mode valid, text valid.
func ValidateText(s string) error
func ValidateMode(s string) (string, error)
func (r DeliverRequest) Validate() error
```

**Tests:** JSON round-trip of every struct with golden literals (field names
are the wire contract); `ValidateText` at 65536 bytes ok / 65537 rejected /
invalid UTF-8 rejected / empty rejected; `ValidateMode` table; `Validate`
rejects each missing tuple field, pid 0, bad proc_start, non-UUID msg_id;
`Key()` equality for equal tuples.

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
// Parse accepts a content string; ok is false unless it starts with
// "<cross-session-message" and ends with "</cross-session-message>".
// Attributes are read in any order; unknown attributes are ignored; values
// are html-unescaped. Text is everything between the opening tag's
// trailing "\n" and the closing tag's leading "\n".
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
// FromSocket strips the "uds:" prefix; ok false when the prefix is absent.
func FromSocket(from string) (path string, ok bool)

var ErrWriteIncomplete = errors.New("frame not fully written")
var ErrPostWriteTimeout = errors.New("timed out after write")
// WriteFrame dials sockPath (unix), sets an absolute deadline of now+timeout
// on the conn, writes line, then closes the write side and waits for the
// peer's EOF or the deadline. Returns nil when everything completed;
// ErrWriteIncomplete (wrapped) when the write itself failed or timed out;
// ErrPostWriteTimeout (wrapped) when the full line was written but the
// close/EOF wait hit the deadline. Dial errors are returned as-is.
func WriteFrame(ctx context.Context, sockPath string, line []byte, timeout time.Duration) error
```
**Produce (registry_write.go):**
```go
type RegistryEntry struct {
    PID int; SessionID, Name, Cwd, ProcStart, Version, Inbox, PidDomain string
    PeerFeatures []string
}
// RegistryFiles returns the two paths for pid: <dir>/<pid>.json and
// <dir>/<pid>.<sha256hex(peerToken)>.key.
func RegistryFiles(dir string, pid int, peerToken string) (jsonPath, keyPath string)
// WriteRegistry creates both files with O_CREATE|O_EXCL|O_WRONLY (key mode
// 0600, json 0644), never following symlinks (O_NOFOLLOW). JSON content
// mirrors a real cc entry: pid, sessionId, cwd, startedAt (ms), procStart,
// version, peerProtocol 1, peerFeatures, kind "interactive", entrypoint
// "cli", pidDomain, messagingSocketPath, name, nameSource "user",
// nameSince, updatedAt, status "idle", statusUpdatedAt. Key content
// {"peerToken","procStart","pidDomain"}. If the key write fails the json is
// removed; returns the paths actually created (for the ready line).
func WriteRegistry(dir string, e RegistryEntry, peerToken string) (created []string, err error)
// RemoveRegistry unlinks every path (ignoring ENOENT); first error returned.
func RemoveRegistry(paths []string) error
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
// Start: mkdir SockDir 0700 if missing; remove a stale <pid>.sock only if
// it exists AND connecting to it fails with ECONNREFUSED (never a live one);
// net.Listen("unix"); chmod 0600; WriteRegistry; on any failure undo what
// was done and return err. Then accept in a goroutine: each conn is read
// with bufio.Scanner (1 MiB max token), every non-empty line is sent on
// Frames() verbatim; conn read errors close that conn only.
func StartVirtualPeer(o VirtualPeerOptions) (*VirtualPeer, error)
func (v *VirtualPeer) SockPath() string
func (v *VirtualPeer) Files() []string   // registry paths created
func (v *VirtualPeer) Frames() <-chan string
// Close stops accepting, closes the listener, unlinks the socket and the
// registry files; idempotent.
func (v *VirtualPeer) Close() error
```
**Produce (version.go):** `const VerifiedCCVersion = "2.1.270"`;
`func NewerThanVerified(v string) bool` (numeric dotted compare; unparsable
⇒ false).

**Tests:** wrapper golden Format matches the spike's byte layout (spec
§3.2) incl. escaping; Parse round-trips Format for every field, tolerates
attribute reordering, rejects plain text; frame golden JSON key order
irrelevant but field set exact; ParseFrame with string and block-array
content; `WriteFrame` against a test listener that (a) reads all and
closes ⇒ nil, (b) never reads (SO_RCVBUF-sized line of 512 KiB) ⇒
`ErrWriteIncomplete` within timeout+100 ms, (c) reads all but never
closes ⇒ `ErrPostWriteTimeout`, (d) no listener ⇒ dial error;
`WriteRegistry` refuses an existing json (O_EXCL, error mentions the
path), rolls back the json when the key path is pre-occupied, key mode
0600, json content parses back through `peers.ReadRegistry` with a fake
liveness as a live entry whose Name/Inbox/ProcStart match; `RemoveRegistry`
tolerates missing; `VirtualPeer`: two peers in one test with different PIDs
in a short temp dir bind distinct sockets, a frame written with WriteFrame
appears on `Frames()` verbatim, Close removes socket + files, second Close
is nil; stale-socket replacement only when refused; `NewerThanVerified`
table (`2.1.270` false, `2.1.271` true, `2.2.0` true, `2.1.9` false, `x`
false).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): ccuds — Claude Code frame, wrapper, registry and virtual peer primitives`

### Task 3: `pdx peer-proxy` helper (`internal/peers/proxyhelper` + subcommand)

**Files:** create `internal/peers/proxyhelper/proxyhelper.go`, `proxyhelper_test.go`,
`cmd/pdx/peer_proxy.go`; modify `cmd/pdx/main.go` (switch case).

**Stdio protocol (D8):**
```
daemon → helper (stdin, line 1):  {"name":"air/foo","registry_dir":"…","sock_dir":"…","version":"2.1.270","peer_features":["…"],"cwd":"…"}
helper → daemon (stdout, line 1): {"ready":true,"pid":N,"sock":"…","files":["…","…"]}
                              or: {"ready":false,"error":"…"}   then exit 1
helper → daemon (stdout, after):  {"frame":"<raw NDJSON line from the socket>"}   one per inbound line
daemon → helper: stdin EOF ⇒ helper Close()s the peer and exits 0
signals: SIGTERM/SIGINT ⇒ same cleanup, exit 0
```
**Produce:**
```go
type Options struct {
    PID         int
    ProcStart   func(pid int) (string, error)
    Signals     <-chan os.Signal // nil ⇒ Run installs its own SIGTERM/SIGINT notify
}
// Run reads the config line, starts a ccuds.VirtualPeer, writes the ready
// line, then pumps Frames() to stdout as {"frame":…} until stdin EOF, a
// signal, or ctx.Done(). Every stdout write is one line, flushed. Returns
// nil on clean shutdown; a non-nil error (after a ready:false line) when
// the peer could not start. Stdout writes are serialised by one mutex
// (frames arrive concurrently with the ready line only in tests).
func Run(ctx context.Context, stdin io.Reader, stdout io.Writer, o Options) error
```
`cmd/pdx/peer_proxy.go`: `case "peer-proxy": os.Exit(runPeerProxy())` with
`Options{PID: os.Getpid()}` and the default `ps` ProcStart. The helper never
reads config.toml, never opens HTTP, and ignores every CLI argument.

**Tests (in-process, pipes, short socket dir, PID from a counter starting at
900000 so two helpers never collide):** ready line has pid/sock/files and
both files exist before the line is observed (assert by reading the files
in the test the instant the ready line arrives); a frame written to `sock`
is echoed as `{"frame":…}` byte-exact; closing stdin ⇒ Run returns nil
within 1 s and socket + files are gone; a signal on `Options.Signals` ⇒
same; config line with an unwritable `registry_dir` ⇒ `ready:false` line,
non-nil error, no socket left; a config line that is not JSON ⇒
`ready:false`; the `cmd/pdx` switch dispatches `peer-proxy` (test the
dispatch function, not `main`, as `hook_test.go` does for `hook`).

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(pdx): peer-proxy helper subcommand with stdio-only IPC`

### Task 4: Audit store (`internal/store/peer_message.go`)

**Files:** create `internal/store/peer_message.go`, `peer_message_test.go`;
modify `internal/store/meta.go` (migration + accessor).

**Produce:**
```go
// Schema (added to migrateMetaDB, CREATE TABLE IF NOT EXISTS):
//   peer_messages(msg_id TEXT NOT NULL, direction TEXT NOT NULL, ts INTEGER NOT NULL /* unix ms */,
//     from_host_id TEXT, from_session_id TEXT, to_host_id TEXT, to_session_id TEXT,
//     declared_mode TEXT, effective_mode TEXT, bytes INTEGER, result TEXT, error TEXT,
//     PRIMARY KEY (msg_id, direction))
//   CREATE INDEX IF NOT EXISTS peer_messages_ts ON peer_messages(ts)
const ( DirOut = "out"; DirIn = "in"; DirReply = "reply" )
type PeerMessage struct {
    MsgID, Direction string; TS time.Time
    FromHostID, FromSessionID, ToHostID, ToSessionID string
    DeclaredMode, EffectiveMode string; Bytes int; Result, Error string
}
type PeerMessageStore struct{ db *sql.DB }
func (m *MetaStore) PeerMessages() *PeerMessageStore
// Insert writes the row (result "" until SetResult). A duplicate
// (msg_id, direction) is an error (the dedup set is checked first by the
// caller; the PK is the backstop).
func (s *PeerMessageStore) Insert(p PeerMessage) error
func (s *PeerMessageStore) SetResult(msgID, direction, result, errText string) error
// Tail returns the newest n rows, oldest first.
func (s *PeerMessageStore) Tail(n int) ([]PeerMessage, error)
```
**Tests:** `OpenMeta(":memory:")` migration creates the table (query
`sqlite_master`); Insert/SetResult/Tail round-trip incl. ts ms precision
and ordering; duplicate PK error; Tail(0) ⇒ empty non-nil slice; existing
`meta_test.go` untouched and green.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(store): peer_messages audit table on meta.db`

# Phase B — Config, policy, limits

### Task 5: `peers.deliver`, policy row, settings endpoint, version warning, limiters

**Files:** modify `internal/config/config.go` (+ test), `internal/module/peers/policy.go`
(+ test), `module.go`; create `internal/module/peers/settings.go`, `settings_test.go`,
`limits.go`, `limits_test.go`.

**Produce:**
- `PeersConfig.Deliver bool \`toml:"deliver" json:"deliver"\`` (default
  false); `Clone`/`Redacted` carry it (value type — assert in the existing
  clone test by adding one assertion).
- `HostRoutePolicy`: additionally true for `POST` with `r.URL.Path ==
  "/api/peers/deliver"`; every other row unchanged.
- `settings.go`: `GET /api/peers/settings` ⇒ `{"deliver":bool,"alias":"…"}`;
  `PUT /api/peers/settings {deliver?: bool}` ⇒ `Core.UpdateConfig`, 200
  with the same body. Both admin-only (policy refuses hosts; handler also
  checks `PrincipalFrom` ⇒ 403 in depth).
- `module.go`: `normalizeRemoteRows(rows []ipeers.PeerRecord, h config.PeerHost)
  []ipeers.PeerRecord` — for every row set `Host = h.Alias`, `HostID =
  h.HostID` (the verified one; when `h.HostID == ""` the envelope's
  `HostID` that `fetchHostResult` already accepted), and rebuild `Address`
  as `h.Alias + "/" + <text after the first "/" of the remote Address>` (a
  remote Address without "/" becomes `alias/<session_name or cc:peer_name>`).
  `fetchHostResult` applies it to every successful row so `scope=all`
  never echoes a remote's self-reported identity, and `/send` (Task 8)
  applies it before `Resolve`. Pure function, table-tested (a remote that
  claims another host's `host_id` and alias in its rows is overwritten).
- `module.go`: `localEnvelope` gains a one-shot version warning — after
  `ReadRegistry`, for every distinct `Version` with
  `ccuds.NewerThanVerified`, log once per process
  (`sync.Map` of warned versions): `peers: Claude Code %s is newer than the
  last verified %s; run pdx msg selftest`.
- `limits.go`:
  ```go
  type dedupSet struct{ /* mu, map[string]time.Time, window, now */ }
  func newDedupSet(window time.Duration, now func() time.Time) *dedupSet
  // Seen records id and reports whether it was already present within the
  // window; entries older than the window are pruned on every call.
  func (d *dedupSet) Seen(id string) bool
  type pairKey struct{ From, To ipeers.OriginKey }
  type pairLimiter struct{ /* mu, map[pairKey][]time.Time, limit, window, now */ }
  func newPairLimiter(limit int, window time.Duration, now func() time.Time) *pairLimiter
  // Allow reports whether one more event for k fits in the sliding window
  // and records it if so. Empty pairs are pruned.
  func (l *pairLimiter) Allow(k pairKey) bool
  ```

**Tests:** config round-trip `deliver = true`; policy table (`POST
/api/peers/deliver` true, `GET /api/peers/deliver` false, `POST
/api/peers/send` false, `POST /api/peers/settings` false); settings GET/PUT
persist to a temp `CfgPath` and a host principal gets 403; version warning
fires once for `2.1.271` across two inventory calls and never for
`2.1.270`; dedup: second `Seen` true, after window+1 s false again, prune
keeps the map bounded (assert len); limiter: 30 allowed, 31st refused,
after 60 s allowed, independent pairs independent — all with a fake clock.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(peers): deliver toggle, deliver policy row, settings, row normalisation, dedup and pair limiter`

# Phase C — Helper manager

### Task 6: `helperManager` (`internal/module/peers/helpers.go`)

**Files:** create `internal/module/peers/helpers.go`, `helpers_test.go`,
`helpers_fake_test.go` (in-process fake helper).

**Produce:**
```go
const ( HelperCap = 32; HelperIdleReap = 30 * time.Minute; HelperReadyTimeout = 3 * time.Second; HelperTermGrace = 2 * time.Second )

// helperProc is one spawned helper process, abstracted so tests can run
// proxyhelper.Run in-process over pipes with a fake pid.
type helperProc interface {
    PID() int
    Stdin() io.WriteCloser
    Stdout() io.Reader
    Signal(os.Signal) error
    Wait() error          // returns once the process has exited
}
type spawnFunc func(ctx context.Context) (helperProc, error)

type helper struct {
    key       ipeers.OriginKey
    name      string          // "<alias>/<session_name>"
    pid       int; procStart string
    sock      string; files []string
    proc      helperProc
    lastUsed  time.Time       // guarded by manager mu
    ready     chan struct{}   // closed on ready or failure
    err       error           // set when ready failed
}
type proxyRecord struct {
    PID int `json:"pid"`; ProcStart string `json:"proc_start"`; Sock string `json:"sock"`
    Files []string `json:"files"`; Origin ipeers.OriginKey `json:"origin"`
}
type helperManager struct {
    mu        sync.Mutex
    helpers   map[ipeers.OriginKey]*helper
    spawn     spawnFunc
    now       func() time.Time
    procStart func(pid int) (string, error)   // for proxies.json + sweep
    pidAlive  func(pid int) bool
    proxiesPath string                          // <DataDir>/proxies.json
    registryDir, sockDir, version string; peerFeatures []string
    onFrame   func(h *helper, line string)     // set by the module (reply path)
    log       func(format string, args ...any)
}
func newHelperManager(/* the fields above */) *helperManager

// Acquire returns the ready helper for key, spawning one when absent.
// Under mu: if present and ready ⇒ touch lastUsed, return. If present and
// starting ⇒ release mu, wait on ready (≤ HelperReadyTimeout), re-check.
// If absent: len(helpers) >= HelperCap ⇒ ErrProxyLimit; else insert a
// starting helper and release mu, then spawn(), write the config line,
// read the ready line with a HelperReadyTimeout deadline. On timeout or
// ready:false: SIGKILL, Wait, RemoveRegistry(files reported so far ⇒ none
// unless ready:true was parsed), delete from map, ErrProxySpawnFailed.
// On ready:true: procStart(pid), record, writeProxies(), close(ready),
// start the stdout pump goroutine (each {"frame"} line ⇒ touch lastUsed,
// onFrame(h, frame); EOF ⇒ Release(key, "exited")).
func (m *helperManager) Acquire(ctx context.Context, key ipeers.OriginKey, name string) (*helper, error)
// Touch bumps lastUsed (called after a successful outbound socket write).
func (m *helperManager) Touch(key ipeers.OriginKey)
// Release SIGTERMs the helper, waits ≤ HelperTermGrace then SIGKILLs,
// waits for exit, removes the record and rewrites proxies.json. The
// helper's own signal handler removes its files; Release removes them again
// (idempotent) only after Wait returned.
func (m *helperManager) Release(key ipeers.OriginKey, reason string)
// ReapIdle releases every helper whose lastUsed is older than
// HelperIdleReap; called from a 1-minute ticker started in Start.
func (m *helperManager) ReapIdle()
// ProxyPIDs returns the set of live helper pids (for BuildInput.ProxyPIDs).
func (m *helperManager) ProxyPIDs() map[int]bool
// FindBySock returns the helper owning sock (reply path proxy_to_proxy check).
func (m *helperManager) FindBySock(sock string) (*helper, bool)
// Sweep implements the startup sweep row of §4.5 over proxies.json: for
// each record with pidAlive(pid) && procStart(pid) == record.ProcStart:
// SIGTERM, wait ≤ HelperTermGrace, SIGKILL, wait until !pidAlive; then
// unlink sock + files. For a dead/reused pid: unlink only files whose
// basename starts with "<pid>." and the sock only if it is
// "<sockDir>/<pid>.sock". Finally write an empty list. A missing or
// unparsable file ⇒ treated as empty (logged).
func (m *helperManager) Sweep() error
// Stop releases every helper (daemon shutdown).
func (m *helperManager) Stop()
// writeProxies serialises the current records to proxiesPath atomically
// (temp file in the same dir + rename), under mu.
func (m *helperManager) writeProxies() error
```
Production `spawn`: `exec.CommandContext(ctx, os.Executable(), "peer-proxy")`
with `Env: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}`, stdin
pipe, stdout pipe, stderr to the daemon log with a `peer-proxy[pid]:`
prefix, `SysProcAttr{Setpgid: true}` so the helper is not in the daemon's
foreground group. `Wait()` reaps.

**Fake helper (test file):** `fakeSpawn` runs `proxyhelper.Run` in a
goroutine with `Options{PID: <counter>, ProcStart: fake}` over
`io.Pipe`s; `Signal` cancels its ctx (models SIGTERM); `Wait` blocks on the
goroutine. A `brokenSpawn` variant never writes a ready line; a
`refusingSpawn` writes `ready:false`.

**Tests:** Acquire spawns once for two concurrent calls with the same key
(barrier inside fakeSpawn; assert one spawn); second Acquire after ready
returns the same helper; cap: 32 ready helpers ⇒ 33rd `ErrProxyLimit`; a
starting helper counts toward the cap; ready timeout (brokenSpawn, fake
clock is NOT used here — use a 100 ms `readyTimeout` override field) ⇒
`ErrProxySpawnFailed`, map empty, no socket; `ready:false` ⇒ same;
`proxies.json` after two spawns has two records with the full origin key
and is valid JSON written via rename (assert no `.tmp` left); Release
removes the socket and files and the record; ReapIdle with a fake clock
advanced 31 min releases only the idle one; stdout pump delivers frames to
`onFrame` and touches lastUsed; Sweep: (a) record for a live fake pid
(pidAlive true, procStart equal) ⇒ Signal called then files unlinked; (b)
dead pid ⇒ only `<pid>.*` files under the registry dir unlinked, a
foreign file in `files` (different basename prefix) survives; (c) reused
pid (procStart differs) ⇒ same as dead; (d) missing file ⇒ nil; final
file content `[]`; Stop releases all.

- [ ] tests written and failing
- [ ] implementation, all green (`-race`)
- [ ] commit `feat(peers): helper manager — spawn, ready handshake, cap, proxies.json, reap, sweep`

# Phase D — Daemon endpoints

### Task 7: `POST /api/peers/deliver`

**Files:** create `internal/module/peers/deliver.go`, `deliver_test.go`;
modify `module.go` (constructor, seams, routes, `ProxyPIDs` from the
manager, Start/Stop), `fakes_test.go` / `module_test.go` (constructor
change only), `cmd/pdx/main.go` (`peersmod.New(meta.PeerMessages())`).

**Module changes:**
```go
// AuditStore is what the module needs from store.PeerMessageStore.
type AuditStore interface {
    Insert(store.PeerMessage) error
    SetResult(msgID, direction, result, errText string) error
    Tail(n int) ([]store.PeerMessage, error)
}
func New(audit AuditStore) *Module     // audit nil ⇒ every send/deliver is audit_unavailable
// new fields: audit AuditStore; helpers *helperManager; dedup *dedupSet;
// pairs *pairLimiter; writeFrame func(ctx, sock string, line []byte, timeout time.Duration) error
// (default ccuds.WriteFrame); post postDeliverFunc (Task 8); newMsgID func() string
// (default uuid v4 via crypto/rand); sockWriteTimeout (default SocketWriteTimeout).
```
`Init` builds the manager with `filepath.Join(c.Cfg.DataDir, "proxies.json")`,
the registry dir, `/tmp/cc-socks`, and wires `onFrame` to `m.handleReplyFrame`
(Task 9; a no-op stub until then). `Start` runs `helpers.Sweep()` (an error
is logged, not fatal) and starts the idle-reap ticker; `Stop` stops the
ticker and calls `helpers.Stop()`. `localEnvelope` passes
`helpers.ProxyPIDs()` as `BuildInput.ProxyPIDs`.

**Handler steps (each failure writes `APIError` and the audit row when one
exists; the order matters and is the test contract):**
1. Principal must be `PrincipalHost` (admin ⇒ 403 `admin_not_allowed`);
   `HostID == ""` ⇒ 403 `host_unverified`.
2. Config snapshot under `RLock`: `Deliver`, local `HostID`, alias, the
   matched host entry (by `Principal.Alias` via `FindPeerHostByAlias`; gone
   ⇒ 403 `host_unverified`). `!Deliver` ⇒ 403 `deliver_disabled`.
3. Decode body (1 MiB `MaxBytesReader`), `req.Validate()` ⇒ 400 with the
   specific code (`text_too_large` / `bad_mode` / `bad_request`);
   `req.From.HostID != Principal.HostID` ⇒ 403 `host_unverified`.
4. `dedup.Seen(msg_id)` ⇒ 409 `duplicate` (no audit row: the first one has it).
5. Target: `localEnvelope` ⇒ the record with `Agent != nil && Agent.Type ==
   "cc" && Agent.SessionID == to.AgentSessionID && Agent.PID == to.PID &&
   Agent.ProcStart == to.ProcStart && Deliverable` — else 409 `target_gone`
   (detail names which of session/pid/proc_start mismatched, without
   echoing another session's data).
6. `effective := clamp(req.From.DeclaredMode, entry.AllowBypass)`.
7. `pairs.Allow(pairKey{From: req.From.Key(), To: req.To.Key(localHostID)})`
   false ⇒ 429 `rate_limited` (audited: Insert with result `rate_limited`).
8. `audit.Insert(PeerMessage{MsgID, DirIn, TS: now, From…, To…,
   DeclaredMode, EffectiveMode, Bytes: len(text)})` error ⇒ 503
   `audit_unavailable` (nothing delivered).
9. `helpers.Acquire(ctx, req.From.Key(), Principal.Alias + "/" +
   req.From.SessionName)`: `ErrProxyLimit` ⇒ 503 `proxy_limit`;
   `ErrProxySpawnFailed` ⇒ 502 `proxy_spawn_failed`; both `SetResult`.
10. `ccuds.BuildFrame(msg_id, helper.sock, Wrapper{From: "uds:"+helper.sock,
    FromName: helper.name, FromMode: effective, HopChain: req.HopChain,
    Text: req.Text})`; `writeFrame(ctx, target.Agent.Inbox, line,
    sockWriteTimeout)`: nil ⇒ `SetResult(delivered)`, `helpers.Touch`, 200
    `{msg_id, result: delivered, effective_mode}`; `ErrPostWriteTimeout` ⇒
    `SetResult(delivery_uncertain)`, 200 with that result; any other error
    ⇒ `SetResult("", err)`, 502 `socket_write_failed`.

**Tests (httptest module, `WithPrincipal` contexts, fake sessions/owners/
liveness as P1's fakes, a real Unix listener as the target inbox in a short
temp dir, fakeSpawn from Task 6, in-memory `store.OpenMeta(":memory:")`
audit):** happy path — 200 delivered, the listener received exactly one
line whose `ParseFrame` gives `from == "uds:"+helper sock`, `msg_id`
equal, and whose wrapper is `from-name "air/foo"`, `from-mode prompting`,
text verbatim; declared bypass with `AllowBypass=false` ⇒ prompting, with
true ⇒ bypass (both audited with both columns); `hop_chain` carried;
admin ⇒ 403; unverified host ⇒ 403; `Deliver=false` ⇒ 403; `from.host_id`
mismatch ⇒ 403; 65537-byte text ⇒ 400; duplicate msg_id ⇒ 409 and the
listener saw one line; wrong pid ⇒ 409 `target_gone`; not deliverable
(inbox_dead) ⇒ 409; 31st message in a minute for one pair ⇒ 429 and audit
row `rate_limited`; audit Insert failure (fake) ⇒ 503 and nothing written
to the socket; `proxy_limit` and `proxy_spawn_failed` mapping with audit
results; listener that reads but never closes (with `sockWriteTimeout` 100
ms) ⇒ 200 `delivery_uncertain`; listener absent ⇒ 502 and audit error set;
`ProxyPIDs` from the manager makes the helper's own registry entry a
`proxy` row in `GET /api/peers`; a `/deliver` whose `to` is a helper
(proxy row) ⇒ 409 `target_gone` (proxies are never deliverable).

- [ ] tests written and failing
- [ ] implementation, all green (`-race`)
- [ ] commit `feat(peers): POST /api/peers/deliver — verify, clamp, audit, helper, socket write`

### Task 8: `POST /api/peers/send` and the outbound client

**Files:** create `internal/module/peers/send.go`, `send_test.go`; modify
`module.go` (route, `post` seam).

**Produce:**
```go
type postDeliverFunc func(ctx context.Context, client *http.Client, baseURL, bearer string, req ipeers.DeliverRequest) (ipeers.DeliverResponse, *ipeers.RemoteError, error)
// postDeliver POSTs <baseURL>/api/peers/deliver with a 10 s context, no
// redirects, 1 MiB body cap. 200 ⇒ (resp, nil, nil); any other status ⇒
// (zero, &RemoteError{Status, Error, Detail} decoded from the body when
// it is an APIError — else Error "http_<code>", nil); transport error ⇒
// (zero, nil, err).
func postDeliver(...)
func newDeliverClient() *http.Client   // Timeout InterDaemonTimeout, no redirects
```
**Handler steps:**
1. Principal must be admin (403 otherwise — policy refuses hosts already).
2. Decode (1 MiB cap); `ValidateText` ⇒ 400 `text_too_large` /
   `bad_request`; `ValidateMode` ⇒ 400 `bad_mode`; `SplitAddress(to)` ⇒
   400 `bad_address`; `origin_inbox == ""` ⇒ 400 `origin_unknown`.
3. Config snapshot: local host id/alias, cloned hosts. Host part matches the
   local alias or local host id ⇒ 400 `local_target` (D1). Otherwise find
   the entry by `HostMatches(host, h.Alias, h.HostID)`; none ⇒ 404
   `host_unknown`; entry with `Token == ""` or `HostID == ""` ⇒ 409
   `host_unverified` (detail: run `pdx peers host set-token`).
4. Origin: `localEnvelope` ⇒ the record with `Agent != nil && Agent.Type ==
   "cc" && Agent.Inbox == origin_inbox && Deliverable` (a proxy row or a
   non-deliverable row does not qualify) ⇒ else 400 `origin_unknown`.
   `from := WireFrom{HostID: local, AgentSessionID, PID, ProcStart, PeerName,
   SessionName: rec.SessionName or "cc:"+PeerName, DeclaredMode: mode}`.
5. Remote snapshot: `m.fetch(ctx 3 s, m.client, h.URL, h.Token)` error ⇒
   502 `remote_error` with `Remote{Status: 0, Error: err}`; `env.HostID !=
   h.HostID` ⇒ 502 `remote_error` (`host_id mismatch`); `!env.OK` ⇒ 502.
   Then `normalizeRemoteRows(env.Peers, h)` (Task 5): the remote's
   self-reported `Host` / `HostID` / `Address` are **never** used for
   addressing — every row is rewritten to the entry's alias and verified
   `HostID` before resolution (P2 final review #5).
6. `ipeers.Resolve(env.Peers, session)`: `ErrNotFound` ⇒ 404
   `peer_not_found`; `*AmbiguousError` ⇒ 409 `ambiguous` with
   `Candidates` = addresses; record not `Deliverable` ⇒ 409
   `not_deliverable` (detail = record.Reason).
7. `msg_id := newMsgID()`; audit `Insert(DirOut, from local, to
   env.HostID/target session)` error ⇒ 503 `audit_unavailable`.
8. `post(ctx, deliverClient, h.URL, h.Token, DeliverRequest{MsgID, From,
   To: {session, pid, proc_start}, Text})`: transport error ⇒
   `SetResult("", err)`, 502 `remote_error`; RemoteError ⇒
   `SetResult(remote.Error, remote.Detail)`, 502 `remote_error` with
   `Remote` (the CLI prints `air: target_gone`); success ⇒
   `SetResult(resp.Result)`, 200 `SendResponse{MsgID, ToHostID,
   ToAddress: target.Address, To, Result, EffectiveMode}`.

**Tests:** `postDeliver` against httptest: 200 decode; 409 APIError body ⇒
RemoteError with code and detail; 500 HTML ⇒ `http_500`; timeout with a
50 ms client ⇒ transport error; bearer and path asserted. Handler: happy
path with a fake `fetch` returning a snapshot and a fake `post` capturing
the request ⇒ request `from` tuple equals the origin row, `to` equals the
resolved record's agent tuple, `msg_id` UUID-shaped, response echoes the
remote's result and mode, audit row `out` with result; each error step
above with its status/code (table-driven); `local_target` for both the
alias and the full host id; origin pointing at a proxy row ⇒
`origin_unknown`; `ambiguous` carries both candidate addresses; remote
`target_gone` ⇒ 502 with `remote.error == "target_gone"` and the audit
result `target_gone`; the fake `post` asserts no `Authorization` other than
the entry's `Token`.

- [ ] tests written and failing
- [ ] implementation, all green (`-race`)
- [ ] commit `feat(peers): POST /api/peers/send — origin attribution, remote resolve, outbound deliver`

### Task 9: Reply path and `GET /api/peers/log`

**Files:** create `internal/module/peers/reply.go`, `reply_test.go`; modify
`module.go` (route `GET /api/peers/log`; `onFrame` wiring).

**`handleReplyFrame(h *helper, line string)`** (runs on the helper's pump
goroutine; must never block on HTTP for long — it spawns a goroutine per
frame, bounded by a semaphore of 8):
1. `ccuds.ParseFrame(line)`; error or `Type != "user"` ⇒ log and drop.
2. `ccuds.FromSocket(frame.From)` false ⇒ audit `reply` row with result
   `replier_unknown` (from_session_id "") and drop.
3. `helpers.FindBySock(sock)` hit ⇒ audit `proxy_to_proxy`, drop.
4. `localEnvelope` ⇒ record with `Agent.Type == "cc" && Agent.Inbox == sock
   && Deliverable`; none ⇒ audit `replier_unknown`, drop.
5. `w, ok := ccuds.Parse(frame.Message.Content)`; text = `w.Text` when ok
   else the whole content; `declared := ValidateMode(w.FromMode)` or
   prompting (D3); `hop := w.HopChain`. `ValidateText(text)` failure ⇒
   audit `text_too_large`, drop.
6. Return route: config snapshot; the entry with `HostID == h.key.HostID`
   and `Token != ""` — none ⇒ audit `no_return_route`, drop (nothing is
   written back into the target session).
7. `msg_id := frame.MsgID` if UUID-shaped else `newMsgID()`; audit
   `Insert(DirReply, from replier, to h.key)`; error ⇒ log, drop
   (fail closed).
8. `post(ctx 10 s, deliverClient, entry.URL, entry.Token, DeliverRequest{
   MsgID, HopChain: hop, From: replier tuple with SessionName / PeerName /
   DeclaredMode, To: WireTo(h.key), Text})`; success ⇒ `SetResult`,
   `helpers.Touch(h.key)`; RemoteError `target_gone` ⇒ `SetResult`,
   `helpers.Release(h.key, "origin gone")` (§4.5 origin-gone row); other
   errors ⇒ `SetResult` only.

**`GET /api/peers/log?tail=N`** (admin; policy refuses hosts): default 50,
max 1000, 400 on a non-integer; body `{"messages":[…]}` with `ts` as RFC
3339 ms.

**Tests:** drive `handleReplyFrame` directly with a fake helper record:
happy path ⇒ fake `post` receives `to == helper key`, `from == replier
tuple`, `hop_chain` carried, `declared_mode` from the wrapper, text
unwrapped; plain (unwrapped) content ⇒ whole content as text, prompting;
non-user frame dropped without audit; `from` without `uds:` ⇒
`replier_unknown` audited; `from` = another helper's sock ⇒
`proxy_to_proxy`; unknown sock ⇒ `replier_unknown`; no return route ⇒
`no_return_route` audited and `post` never called; remote `target_gone` ⇒
helper released (socket gone); audit insert failure ⇒ no post; log
endpoint tail/ordering/400/403.

- [ ] tests written and failing
- [ ] implementation, all green (`-race`)
- [ ] commit `feat(peers): forward native replies through the return route; GET /api/peers/log`

### Task 10: Two-daemon end-to-end test

**Files:** create `internal/module/peers/e2e_test.go`.

Two real `Module`s (A and B) as in P2's pairing test, each behind
`newOuterHandler`-equivalent auth (use `middleware.PeerAuth` +
`HostRoutePolicy` around the module mux so the principal is real), paired
both ways in config (tokens + host ids + `Deliver: true`), real
`fetchRemote` and real `postDeliver` over httptest, real `ccuds.WriteFrame`,
fakeSpawn helpers, one shared short socket dir and one shared registry dir
(both daemons read the same registry, as two daemons on one machine
would). Fake "cc sessions": test-owned Unix listeners `origin.sock` (A's
side, in tmux session `mt1`) and `target.sock` (B's side, `foo`), each
with a registry file + fake liveness that says alive, and A's/B's fake
session providers listing their own session only.

Flow asserted end-to-end:
1. `POST A /api/peers/send {to:"b/foo", text:"ping", origin_inbox: origin.sock}`
   ⇒ 200; `target.sock` receives one frame: wrapper `from-name "a/mt1"`,
   `from-mode prompting`, text `ping`, `from == "uds:"+B-helper sock`.
2. The test, acting as the target Claude, writes a native reply into the
   B-helper's socket: `BuildFrame(newUUID, target.sock, Wrapper{From:
   "uds:"+target.sock, FromName: "foo", FromMode: "bypass", HopChain:
   "abc", Text: "pong"})`.
3. `origin.sock` receives one frame within 2 s: wrapper `from-name
   "b/foo"`, `from-mode prompting` (clamped: A's entry for B has
   `AllowBypass false`), `hop-chain "abc"`, text `pong`, `from ==
   "uds:"+A-helper sock`; A now has one helper keyed by B/foo's tuple.
4. Audit: A has rows `out` (delivered) and `in` (delivered); B has `in`
   and `reply` (delivered).
5. Set A's entry `AllowBypass true`, repeat 2 ⇒ `from-mode bypass`.
6. Kill the origin listener + registry file (A's fake liveness says dead);
   repeat 2 ⇒ B's audit `reply` row `target_gone` and B's helper for A/mt1
   is released (its socket gone).
7. `POST A /send` again ⇒ 400 `origin_unknown`.

- [ ] test written and failing (compiles against Tasks 7–9)
- [ ] all green (`-race`), no flakiness in 20 runs (`-count=20`)
- [ ] commit `test(peers): two-daemon end-to-end delivery, reply, clamp and origin-gone`

# Phase E — CLI

### Task 11: `pdx msg send|log|deliver`

**Files:** create `cmd/pdx/msg.go`, `msg_test.go`; modify `cmd/pdx/main.go`
(switch case `msg`).

**Grammar (hand parser, same style as `parsePeersInvocation`; every
rejection ⇒ usage on stderr, exit 2, no config load, no request):**
```
pdx msg send <host>/<session> <text> [--mode prompting|bypass] [--json] [--config <path>]
pdx msg log [--tail N] [--json] [--config <path>]
pdx msg deliver <on|off|status> [--json] [--config <path>]
pdx msg selftest [--timeout <dur>] [--config <path>]        (Task 12)
```
- `send`: `origin_inbox = os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET")` (via an
  injectable `getenv` for tests); empty ⇒ stderr `pdx msg: not inside a
  Claude Code session (CLAUDE_CODE_MESSAGING_SOCKET unset)`, exit 1, no
  request. POST `/api/peers/send` with `cfg.Token`, 15 s client (the daemon
  needs up to 3 s + 10 s). 200 ⇒ stdout `sent <msg_id> → <to_address>
  (<result>, mode <effective_mode>)`; `--json` ⇒ body passthrough. Error ⇒
  stderr `pdx msg: <error>[: <detail>]`, `remote_error` ⇒ `pdx msg: <host>:
  <remote.error>[: <detail>]`, `ambiguous` ⇒ one candidate per line; exit 1.
- `log`: GET `/api/peers/log?tail=N` (default 50); table
  `TIME  DIR  MSG_ID  FROM  TO  MODE  BYTES  RESULT  ERROR` where MSG_ID is
  the first 8 chars, FROM/TO are `<host_id>/<session_id[:8]>`, MODE is
  `decl→eff` (`p→p`, `b→p`, `b→b`), TIME is local `15:04:05`.
- `deliver on|off` ⇒ PUT `/api/peers/settings {deliver}` and print
  `deliver: on|off`; `status` ⇒ GET and print the same.

**Tests:** grammar table (exit 2, zero requests — count on a test server);
`send` without the env var ⇒ exit 1, zero requests; `send` posts the
expected body incl. `origin_inbox` and prints the success line; error
rendering for `remote_error`, `ambiguous`, `origin_unknown`; `log` golden
table; `deliver on` PUT body and output; `--json` passthrough for all
three.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(cli): pdx msg send|log|deliver`

### Task 12: `pdx msg selftest`

**Files:** create `cmd/pdx/msg_selftest.go`, `msg_selftest_test.go`; modify
`cmd/pdx/msg.go` (verb).

**Body (`runMsgSelftest(deps, timeout, stdout, stderr) int`, deps injectable:
`tmux func(args ...string) ([]byte, error)`, `registryDir`, `sockDir`,
`readRegistry`, `startPeer func(ccuds.VirtualPeerOptions) (*ccuds.VirtualPeer, error)`,
`writeFrame`, `now`):**
1. `name := "pdx-selftest-" + 6 hex`; `tmux new-session -d -s <name>
   'claude -p --verbose --input-format stream-json --output-format
   stream-json --name <name> --settings {"crossSessionInbound":"accept"}'`
   (the command string is built with `strconv.Quote`-free shell quoting
   via a small helper; test the exact argv). Register a cleanup that
   always runs (defer + `signal.NotifyContext`): `tmux kill-session -t
   <name>`, `peer.Close()`.
2. Poll every 250 ms ≤ 15 s: `peers.ReadRegistry(registryDir,
   DefaultLiveness())` for the entry whose `TmuxSessionName() == name`;
   none in time ⇒ `FAIL: session did not register (Claude Code ≥ 2.1.224
   with peer messaging required)`, exit 1.
3. `peer := startPeer(VirtualPeerOptions{PID: os.Getpid(), Name:
   "pdx-selftest-probe", Version: ccuds.VerifiedCCVersion, PeerFeatures:
   copied from the target entry's registry file (read raw JSON), …})`.
4. `nonce := 8 hex`; `writeFrame(target.Inbox, BuildFrame(uuid, peer.SockPath(),
   Wrapper{From: "uds:"+peer.SockPath(), FromName: "pdx-selftest-probe",
   FromMode: prompting, Text: "PDX_SELFTEST " + nonce + ": reply with
   exactly: PONG " + nonce}), 5 s)`.
5. Wait ≤ `timeout` (default 60 s) on `peer.Frames()` for a frame whose
   `ParseFrame` has `Type == "user"`, `FromSocket(From) == target.Inbox`,
   and whose content contains `nonce` ⇒ `PASS: reply from <name> in
   <elapsed>` exit 0; timeout ⇒ `FAIL: no reply within <timeout>` exit 1;
   a frame from the target without the nonce is printed as `note:` and
   waiting continues.
6. Cleanup always; print `cleanup: tmux session and registry files
   removed`.

**Tests (all deps faked, no tmux/claude):** argv of `new-session` and
`kill-session` golden; registry entry appears on the 3rd poll ⇒ proceeds;
never appears ⇒ FAIL text and kill-session still called; the frame written
to the target inbox parses with the nonce; a reply frame with the nonce ⇒
PASS; a frame from another socket is ignored; timeout ⇒ FAIL and cleanup
called; peer start failure ⇒ FAIL, kill-session called.

- [ ] tests written and failing
- [ ] implementation, all green
- [ ] commit `feat(cli): pdx msg selftest — Claude Code peer protocol upgrade gate`

# Phase F — Acceptance (main session)

### Task 13: Two daemons on mlab (one machine, real Claude Code sessions)

- [ ] `git merge origin/main` (post-P2) already done; `make build`.
- [ ] Reuse the P2 acceptance layout (`/tmp/pdx-p3/{a,b}`, ports 7861/7862,
      distinct `data_dir`, both admin tokens non-empty, a's `data_dir`
      holding a copy of the production `agent_events.db` so a resolves
      owners; b's too, since b must resolve the target's owner) — pair both
      ways, `pdx msg deliver on --config …/b/config.toml` and for a.
- [ ] Open two real Claude Code sessions in tmux: `mt-origin` (any cwd) and
      `mt-target` with `--settings '{"crossSessionInbound":"accept"}'`.
      Inside `mt-origin`, run `pdx msg send b/mt-target "ping from a"
      --config /tmp/pdx-p3/a/config.toml` (the Bash tool inherits
      `CLAUDE_CODE_MESSAGING_SOCKET`). Expect `sent <id> → b/mt-target
      (delivered, mode prompting)`.
- [ ] In `mt-target`, the message arrives as `<cross-session-message
      from="uds:/tmp/cc-socks/<helper pid>.sock" from-name="a/mt-origin"
      from-mode="prompting">`; ask it to reply with `SendMessage`. The
      reply appears in `mt-origin` with `from-name="b/mt-target"`.
- [ ] `pdx msg log --config …/a/config.toml` shows `out delivered` and
      `in delivered`; b shows `in` and `reply`. `pdx peers --config …/b/…`
      shows the helper as a `proxy` row, not deliverable. `cat
      /tmp/pdx-p3/b/data/proxies.json` has one record with the full origin
      tuple.
- [ ] `--mode bypass` from `mt-origin` ⇒ target sees `from-mode="prompting"`;
      `pdx peers host set-token a <T> --allow-bypass=true --config …/b/…`
      then again ⇒ `from-mode="bypass"`.
- [ ] Negative: `env -u CLAUDE_CODE_MESSAGING_SOCKET pdx msg send …` ⇒
      `origin_unknown`; `pdx msg send a/mt-target …` ⇒ `local_target`;
      `pdx msg deliver off` on b then send ⇒ `b: deliver_disabled`; 31 sends
      in a loop ⇒ the 31st `b: rate_limited`; `curl -X POST
      …7862/api/peers/deliver -H 'Authorization: Bearer btoken'` ⇒ 403
      `admin_not_allowed`.
- [ ] Restart b with the helper alive: on start the log shows the sweep
      terminating the old helper pid, `proxies.json` is `[]`, no stale
      `<pid>.json` in `~/.claude/sessions`.
- [ ] `pdx msg selftest` ⇒ `PASS`; `tmux ls` shows no `pdx-selftest-*`
      session afterwards; no `pdx-selftest-probe` registry file remains.
- [ ] Tear down (`pdx stop` both, `rm -rf /tmp/pdx-p3`), paste transcripts
      into the PR.

### Task 14: mlab ↔ air-2026 (after deploy)

- [ ] Deploy alpha.N on both hosts; pair both ways; `pdx msg deliver on`
      on both. From a cc session on mlab: `pdx msg send air/<s> "ping"`
      arrives on air as `mini-lab/<s>`; the native reply returns; `pdx msg
      log` on both hosts shows the four rows; `pdx msg selftest` passes on
      both. Record in the PR and in memory `kickoff_peer_bridge`.
