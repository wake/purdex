# Peer Local Delivery — one door, one ref namespace

Date: 2026-09-17 · Scope: daemon (`internal/module/peers`, `internal/peers`) + `pdx` CLI · Two phases

Follows `2026-09-17-peer-address-v4-spec.md` (#1107, alpha.373). v4 made the address readable;
this makes it the *only* address an agent needs, by removing the second door.

Supersedes #1118, which proposed aligning two namespaces. §2 explains why that was the wrong
question.

## 1. Goal

**`pdx msg send` reaches every peer, local or remote.** One entry point, one ref namespace, one
table that is always sufficient to address any row it prints.

Today `pdx msg send mlab/purdex-dd` is refused with `local_target`, and the operator is told to use
Claude Code's native `SendMessage` instead. So an agent must decide which of two tools to use based
on where the target happens to live — and the two tools name the same session differently:

```
pdx peers --all   →  mini-lab/purdex-dd [h0h3ln]
ListAgents        →  purdex-dd [ba68ab]
```

Both are correct. They are different namespaces.

## 2. What the mechanism actually is

Every design option here is decided by one fact, so it is stated before the decisions.

**Native messaging and pdx are parallel implementations, not layers:**

```
native SendMessage:   resolve (in the sender's own CC process)  ──→ write frame to target socket
pdx msg send:         resolve (in the sender's daemon)          ──→ write frame to target socket
```

pdx never calls a native function. It writes the same NDJSON frame, into the same
`/tmp/cc-socks/<pid>.sock`, that native would. The receiver cannot tell them apart.

Three consequences, each verified:

**2.1 The delivery channel has no return path.** `ccuds.WriteFrame` dials, writes, half-closes and
waits for EOF. It returns `nil`, `ErrWriteIncomplete` or `ErrPostWriteTimeout` — write status only,
never a payload. Even the existing cross-host `delivered` / `delivery_uncertain` describe the write,
never what the agent did with it.

**2.2 Disambiguation is a tool return value, and a tool result has exactly one addressee.** When a
name is ambiguous, native's `SendMessage` fails *before any socket is opened* and returns the
candidate list to whoever called it:

```
'purdex' matches 3 agents by prefix. Re-send with the ref of the one you mean:
  purdex-4a [a41f6a] — Claude session, on this machine, active 4h ago
  purdex-dd [ba68ab] — Claude session, on this machine, active 11m ago
  purdex-03 [4d9211] — Claude session, on this machine, active 4h ago
```

Nothing was sent; that answer never left the calling process. pdx is not inside a CC process, so
there is no call for it to be the answer to. This is not a missing API — a return value cannot be
routed to someone who did not make the call.

**2.3 pdx cannot obtain native's ref.** Measured: 11 input variants (sessionId, pid, socket path,
name, and concatenations, with and without dashes) × 9 hash algorithms × 20 truncation offsets, over
two live sessions — no match. Nor is it stored: nothing under `~/.claude/` holds it outside
conversation transcripts. And it is **per-observer**, not per-conversation: one session reads as
`d8dc4a` on `mini-lab` and `ad1737` on `air`.

**Therefore: whoever resolves owns the ref, and the two cannot be mixed.** Choosing the entry point
chooses the ref namespace. That is the whole design, and everything below follows from it.

## 3. Decisions

| # | Decision |
|---|---|
| L1 | **One door.** `pdx msg send` accepts a local target. `local_target` is deleted. |
| L2 | **pdx resolves local targets itself**, from the registry it already reads. The ref is therefore pdx's, everywhere, for every row. |
| L3 | **No local/remote column.** Which side a row is on is the daemon's business, not the reader's — L1 makes it irrelevant to the caller. |
| L4 | **Local delivery uses no helper.** The frame's `from` is the sender's own socket, so the receiver replies straight back. |
| L5 | An ambiguity refusal **carries each candidate's ref**. Without it the refusal is unusable under v4 — see §5. |
| L6 | Native `SendMessage` keeps working and is not deprecated. It is simply no longer required to reach a local peer. |

### 3.1 Why not the other door

Refusing local targets and pointing at native has one real advantage: native's disambiguation loop
closes by itself, because the session holding the tool is the one that gets the candidate list.

It was rejected because it makes the cost structural rather than occasional:

- an agent must know where a target lives before it can choose a tool, and "where it lives" is what
  the address's host segment was supposed to tell it;
- `pdx peers --all` prints local rows, so it advertises addresses that `pdx msg send` refuses. That
  is the same defect v4 fixed for `cc` and `tmux` (#1107, `795340db`): *an address this daemon mints
  and prints must be one it can also resolve*;
- two ref namespaces persist forever, and no amount of alignment merges them, because one of the two
  is per-observer.

L2 costs one thing in exchange: pdx's ref for a local session differs from what `ListAgents` shows
for that same session. Under L1 nobody sends with `ListAgents`, so this is two tools each keeping its
own internal numbering — not a contradiction the operator has to resolve.

## 4. Phase A — local delivery

### 4.1 The path

`handleSend` currently refuses at step 3 when the target host is this host
(`internal/module/peers/send.go:237`). Replace the refusal with a local branch:

1. resolve the address against **this host's** own inventory, with the same `Resolve` and the same
   `ResolveSnapshot` rules a remote target gets;
2. build the frame with the **sender's own inbox** as the reply address;
3. write it to the target's inbox.

```go
line, err := ccuds.BuildFrame(req.MsgID, req.OriginInbox, ccuds.Wrapper{
    From:     "uds:" + req.OriginInbox,
    FromName: origin.Address,          // the sender's own v4 address
    FromMode: effective,
    HopChain: req.HopChain,
    Text:     req.Text,
})
err = m.writeFrame(m.stopCtx, target.Agent.Inbox, line, m.sockWriteTimeout)
```

`req.OriginInbox` is already required and validated by `handleSend`, and `findOrigin` has already
attributed it to a live, deliverable, non-proxy cc row. Nothing new is needed to know who is sending.

### 4.2 No helper, and why that is not an oversight

A helper exists so that a receiver — which only ever replies to a `uds:` address — has something
local to reply to when the sender is on another machine. A local sender has a working socket in the
same filesystem, so the helper would be a process spawned to impersonate a session sitting next to
it.

The result is that local delivery is strictly **shorter** than remote:

```
remote:  resolve → HTTP → peer daemon → acquire helper → write
local:   resolve → write
```

**Deferred, not rejected: a local helper would buy symmetric observation.** With L4 the reply is a
native message from the receiver straight to the sender, so pdx sees the outbound leg and not the
return. Routing the reply through a local helper would put both legs in pdx's audit, dedup and rate
limits.

It is deferred because the symmetry it buys is partial anyway: two same-host sessions talking
natively bypass pdx entirely, and always will. So a local helper would not give "pdx sees everything"
— only "pdx sees both legs of conversations pdx started". If that is later wanted as an audit
property, it is an additive change to this design, not a revision of it.

### 4.3 Policy applies to local, unchanged

Audit, dedup (`DedupWindow`), the pair rate limit and mode clamping are message semantics, not
transport details. They apply to a local send exactly as they do to a remote one. The only steps
that do not run are the ones that exist for the network hop: `postDeliver`, the remote `/deliver`
round trip, and helper acquisition.

`AllowBypass` is a **peer-host** trust flag and has no local equivalent: a local sender is this
host's own user, already authenticated by the admin token. The declared mode is taken as given, with
the same `ValidateMode` check.

### 4.4 The table

Local rows already render as `<host>/<name> [<ref>]` (v4 §5.7) and need no change. What changes is
that those addresses now work in `pdx msg send`, which is what §1 asked for.

No `VIA` or local/remote column is added (L3). The `HOST` column in `--all` already says which host a
row is on; whether that host is this one is not a fact the sender needs.

## 5. Phase B — the ambiguity refusal must carry refs

### 5.1 The defect

`AmbiguousCandidate` (`internal/peers/wire.go:357`) carries `Address`, `AgentName`, `PID`, `Cwd` —
and no ref.

Under v4 that is not a gap in detail, it is a refusal that cannot be acted on. Two live conversations
whose registry names collide produce **identical** `Address` and **identical** `AgentName`:

```
pdx msg: ambiguous: peer address "mlab/purdex-dd" is ambiguous (2 candidates)
  mlab/purdex-dd  agent purdex-dd  pid 39396  cwd ~
  mlab/purdex-dd  agent purdex-dd  pid 12345  cwd ~
```

The operator can see there are two and can address neither: `pid` and `cwd` are not address forms.

v4 spec §3.1's P2 residual states the opposite outcome as a design promise — that colliding rows
"are told so with both candidates **and their refs**" — and §9.7 exists to check it. The
implementation never carried them, seven review passes did not catch it, and §9.7 was the acceptance
item that was skipped.

**Why it survived:** the one e2e test covering ambiguity (`internal/module/peers/e2e_test.go:1180`)
uses two live *processes of one conversation*, named `twin-1` and `twin-2`. Under v4 those have
different names, hence different addresses, hence are self-distinguishing — the case the test covers
is the case that no longer needs refs. The case that needs them, two conversations sharing a name,
has no test at all.

### 5.2 The fix

```go
type AmbiguousCandidate struct {
	Address   string `json:"address"`
	Ref       string `json:"ref,omitempty"`   // NEW
	AgentName string `json:"agent_name,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
}
```

Populated from `c.Ref` where the candidate is built (`send.go:335`), and rendered by
`msgCandidateLine` (`cmd/pdx/msg.go:428`) in the bracket form the address grammar already accepts, so
the line can be copied whole:

```
pdx msg: ambiguous: peer address "mlab/purdex-dd" is ambiguous (2 candidates)
  mlab/purdex-dd [h0h3ln]  agent purdex-dd  pid 39396  cwd ~
  mlab/purdex-dd [40zqk2]  agent purdex-dd  pid 12345  cwd ~
```

`omitempty` because a tier-4 candidate (a bare tmux session name) can match a row with no agent and
therefore no ref — the same reason `AgentName` and `PID` are already optional.

**Do not print a bracket when `Ref` is empty.** A row addressed by its ref already ends in it, and
`displayAddress` (`cmd/pdx/peers.go`) has the same rule; the two renderings must agree or a copied
line will not parse.

## 6. Testing

### 6.1 Unit

- `handleSend` local branch: delivers; the frame's `From` is `uds:` + the sender's inbox, **not** a
  helper socket; `postDeliver` is never called (use the existing fake and assert it was not invoked).
- Local send honours dedup, the pair rate limit and mode validation — one case each, mirroring the
  remote equivalents.
- Local send to a row that is not deliverable (`inbox_dead`, proxy, `agent: null`) is refused with
  the same code a remote target would get.
- `Resolve` is called with this host's inventory and the same snapshot rules: a `Partial` local
  inventory yields `not_ready`, not a guess.
- `AmbiguousCandidate.Ref` is populated, and empty for an agentless tier-4 candidate.
- `msgCandidateLine` renders `<address> [<ref>]` when a ref is present and the bare address when not.

### 6.2 The test that should have existed

Two live conversations with the **same** registry name, on the local host. The bare name is refused;
the refusal names both candidates with **different** refs; each ref then delivers to its own row.
This is v4 §9.7 written as an automated test rather than an acceptance step, because the acceptance
step is the one that got skipped.

### 6.3 Mutation tests (deliverables)

Each must turn something red; if it does not, the missing test is the work:

- drop `Ref` from the candidate — the §6.2 test must fail on the refusal being unusable, not merely
  on a missing field;
- give local delivery a helper socket as `From` — a test must catch that the receiver would reply to
  the wrong place;
- skip the dedup check on the local path — a test must catch the duplicate being delivered twice.

### 6.4 Real-machine acceptance

1. `pdx msg send mlab/<name>` to another session on this host delivers, and the receiver's message
   shows `from: uds:/tmp/cc-socks/<sender pid>.sock`.
2. The receiver replies natively; the reply reaches the sender.
3. `pdx peers` and `pdx msg send` agree: every address the table prints is one the sender accepts.
4. Two same-named local sessions: the refusal lists both with distinct refs, and each ref delivers to
   the intended one. **This is the one that closes §5.1's defect — do not sign it off from tests
   alone.**

## 7. Out of scope

- **A local helper for symmetric audit** (§4.2) — additive, deferred with its rationale.
- **#1118's namespace alignment** — superseded. There is no second namespace to align once L1 lands;
  `ListAgents` keeps its own numbering for its own tool, and nothing has to reconcile them.
- **Native `SendMessage` deprecation** (L6). It remains the right tool for in-process subagents and
  for anything pdx does not model.
