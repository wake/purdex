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

**2.1 The delivery write has no synchronous return payload.** `ccuds.WriteFrame` dials, writes,
half-closes and waits for EOF, discarding whatever the peer sends back. It returns `nil`,
`ErrWriteIncomplete` or `ErrPostWriteTimeout` — write status only, never a payload. Even the
existing cross-host `delivered` / `delivery_uncertain` describe the write, never what the agent did
with it.

Stated precisely because the narrow claim is the one that holds: **one write operation** returns no
answer. Replies exist — `internal/module/peers/reply.go` relays a remote peer's reply back through
its helper — but they are a *separate, later* inbound delivery, not this call's return value. This
distinction matters twice below: it is why §2.2 holds, and it is why the deferred local helper
(§4.2) remains possible rather than being ruled out by this paragraph.

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
| L7 | **A local send to the origin itself is refused**, with a new `self_target` code. §4.5. |
| L8 | **Dedup does not run on the local path**, because it cannot: the id it keys on is minted per attempt by this same handler. §4.3. |

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
(`internal/module/peers/send.go:237`). The refusal becomes a branch — but *not* at step 3, because
step 3 runs before the origin is known and the local path needs it. The local branch is decided at
step 3 and taken at step 5:

| step | remote | local |
|---|---|---|
| 1–2 | admin check, decode, `ValidateText`/`ValidateMode`, `SplitAddress`, `origin_inbox` non-empty | same |
| 3 | look up the host entry; refuse `local_target` if the host is this one | set `isLocal`; **skip** the entry lookup, `host_unknown` and `host_unverified` |
| 4 | build the local envelope, `findOrigin`, `wireFromRecord` | same, unchanged |
| 5 | fetch the peer's inventory over HTTP, `normalizeRemoteRows` | reuse the step-4 envelope: `rows = local.Peers`, **no fetch, no normalisation** |
| 6 | `Resolve` over `rows`; ambiguity / not-ready / not-found / not-deliverable | same, over the local rows |
| 6b | — | **§4.5 self-target check** |
| 7 | mint `msgID`, `dreq.Validate()`, audit insert (`DirOut`) | same, with the local `ToHostID` |
| 8 | pair rate limit (on the receiver, in `/deliver`), then HTTP `post` | **pair rate limit here**, then `BuildFrame` + `writeFrame` |

**Step 5 needs no fetch because step 4 already built the local envelope** to attribute the origin
(`m.localEnvelope`, `send.go:270`). The same envelope supplies the rows and the snapshot flags:
`Partial: local.Partial`, `RegistryIncomplete: len(local.UnknownRegistryFiles) > 0`. A `Partial`
local inventory therefore yields `not_ready` for a local target exactly as a partial remote one does
— it is not a licence to guess.

**`normalizeRemoteRows` is not applied.** It exists to force an untrusted peer's self-reported host
onto the entry we authenticated. Local rows are this daemon's own; re-stamping them would be
laundering nothing through a function whose whole purpose is distrust.

#### The `entry` substitution table

`entry` (`config.PeerHost`) is zero on the local path and is read **24 times** after step 3:
`entry.Alias` ×14, `entry.HostID` ×5, `entry.Token` ×3, `entry.URL` ×2. Every one must be given a
local value or be unreachable; a missed `entry.Alias` renders as `""` in an error a human then has
to interpret. This is the largest mechanical risk in Phase A, so it is enumerated rather than left
to the implementer:

| read | local value |
|---|---|
| `entry.Alias` — in `Resolve`'s five refusal arms, three log lines, and the `toAddress` fallback | `snap.alias` |
| `entry.HostID` — audit `ToHostID`, `SendResponse.ToHostID` | `snap.hostID` |
| `entry.Token`, `entry.URL` — `m.fetch`, `m.post`, `host_unverified` | unreachable; the local branch takes neither call |

The plan must name the mechanism (a resolved `targetAlias`/`targetHostID` pair set once per branch
is the obvious one) and a test must prove no refusal on the local path renders an empty alias.

#### The frame

```go
line, err := ccuds.BuildFrame(msgID, req.OriginInbox, ccuds.Wrapper{
    From:     "uds:" + req.OriginInbox,
    FromName: origin.Address,          // the sender's own v4 address
    FromMode: effective,
    HopChain: "",                      // SendRequest carries none — see below
    Text:     req.Text,
})
err = m.writeFrame(m.stopCtx, target.Agent.Inbox, line, m.sockWriteTimeout)
```

**Two different `from`s are set here and they are not the same field.** `BuildFrame`'s second
argument becomes the NDJSON frame's top-level `Frame.From` (`ccuds/frame.go:36`), which is the
address Claude Code actually replies to. `Wrapper.From` is an attribute inside the rendered message
content — what the receiving agent *reads*. Remote sets both to the helper's socket; local sets both
to the sender's own. A test that asserts only one of them would not catch the second being wrong,
so §6.1 requires both.

**`HopChain` is `""`, not `req.HopChain`.** `SendRequest` (`wire.go:280`) has no `HopChain` field —
only `To`, `Text`, `Mode`, `OriginInbox` — so the obvious transcription does not compile. A
CLI-initiated send is by definition the first hop. (`HopChain` reaches `/deliver` only on the relay
path, `reply.go:129`, which local does not use.)

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

### 4.3 Policy on the local path, item by item

The first draft said policy "applies to local unchanged". That is not a description of the code:
audit-in, dedup, the host limit, the pair limit and mode clamping all live in `handleDeliver`, and a
local send never reaches it. Each therefore has to be decided, not inherited.

| policy | remote | local | why |
|---|---|---|---|
| **Audit** | `DirOut` on the sender, `DirIn` on the receiver | **one `DirOut` row** | Two rows exist remotely because two daemons each record what they saw. One daemon saw this one. A second row would not be corroboration, it would be the same observation written twice. |
| **Dedup** (`DedupWindow`) | `/deliver`, keyed on `req.MsgID` | **does not run** (L8) | The id is minted by this handler, per attempt (`send.go:402`); `SendRequest` carries none. Dedup guards the inter-daemon HTTP retransmit — the one hop local does not have. A check here would key on a value that is fresh by construction and could never fire: a test asserting it works would be asserting nothing. |
| **Host rate limit** | `/deliver`, per authenticated peer host | **does not run** | It rations an *external* host's access to this daemon. The local caller already holds the admin token; the limit it would impose is on the operator's own machine. |
| **Pair rate limit** | `/deliver`, `pairKey{From, To}` | **runs, after the audit insert** | This one is not about trust between daemons — it protects the *receiving session* from being flooded. That protection is as wanted locally as remotely. `OriginKey` includes `HostID`, so a local pair key cannot collide with a remote one. Placed after the audit insert so a rate-limited local send is recorded, mirroring `/deliver` (`deliver.go:265`). |
| **Mode** | clamped against the peer host's `AllowBypass` | `ValidateMode` only; declared mode taken as given | See below. |

#### What audited means here, precisely

`/send`'s existing contract is that steps 1–6 are *logged, not audited* — a caller error or a
resolution failure produces no audit row (`refuseUnaudited`), and the row is inserted at step 7,
before anything leaves the host. The local path keeps that line in the same place:

- **unaudited**: admin, decode, validation, `SplitAddress`, origin attribution, `Resolve` (ambiguous
  / not-ready / not-found / not-deliverable), and the §4.5 self-target refusal;
- **audited**: everything from the step-7 insert onward — the pair-limit refusal, a socket write
  failure, and the result (`delivered` / `delivery_uncertain`, the same mapping `/deliver` uses for
  `ErrPostWriteTimeout` at `deliver.go:350`).

This deliberately puts the local pair-limit refusal on the *audited* side while local resolution
failures stay unaudited — matching where each sits remotely, rather than matching which handler it
happens to be written in.

#### Mode: the boundary moves, and saying so is the point

`AllowBypass` is a **peer-host** trust flag with no local equivalent, so a local send takes its
declared mode as given, subject to the same `ValidateMode`. This is not "remote, unchanged" — it is
a different authorisation basis, and the spec states it rather than letting it read as an oversight:

> A local bypass message is authorised by the admin route plus live origin attribution — the caller
> holds this host's admin token and `findOrigin` has tied it to a real local session — not by
> `AllowBypass`.

The practical effect to be aware of: `pdx msg send --mode bypass` reaches any local peer, because
anything holding the admin token can already do far more to this machine than send a message. §6.1
tests that the frame carries `FromMode: bypass` and that an invalid mode is still a 400.

### 4.4 The table

Local rows already render as `<host>/<name> [<ref>]` (v4 §5.7) and need no change. What changes is
that those addresses now work in `pdx msg send`, which is what §1 asked for.

No `VIA` or local/remote column is added (L3). The `HOST` column in `--all` already says which host a
row is on; whether that host is this one is not a fact the sender needs.

### 4.5 Sending to yourself is refused

Local delivery makes a case reachable that the bridge has never had: the origin and the target are
the same session. `pdx msg whoami` prints your own address, so it is a plausible typo, and under L1
the daemon would otherwise write a message into the caller's own inbox whose reply address is that
same inbox.

**Refused**, with a new wire code `self_target` (400, unaudited, alongside the other step-6
resolution refusals). The comparison is the identity tuple the whole bridge already uses —
`AgentSessionID`, `PID`, `ProcStart` — between `origin.Agent` and `target.Agent`, not the typed
address: two different address forms can name one session, and a check on the string would miss
exactly the case that matters.

**Why refuse rather than allow.** The frame would arrive labelled as being from the receiver itself,
and its reply address would be the receiver's own socket. Nothing downstream would stop a reply from
looping: the native reply goes straight back over the socket without touching pdx, so neither
`HopChain` nor the pair limit is in the path. There is no use for the message and there is a live
way for it to misbehave, so it does not ship on the strength of "an agent probably would not".

A new error code is a wire-contract addition, so the plan must confirm the CLI renders an unknown
code from an older daemon without crashing — the same mixed-version check `ErrCodeNameMismatch`
needed in v4.

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

`omitempty` because **a candidate row need not have a ref at all** — a tier-4 match on a bare tmux
session name is the common case, but so is any row without a live cc agent, since `Ref` is empty
whenever the row has none (`record.go:39`). "Agentless" is the usual cause, not the rule; the rule
is "no ref". Same reason `AgentName` and `PID` are already optional.

**The bracket rule has two conditions, not one, and must be copied exactly.** `displayAddress`
(`cmd/pdx/peers.go:583`) omits the bracket when

```go
rec.Ref == "" || strings.HasSuffix(rec.Address, "/"+rec.Ref)
```

— the second arm because a row *addressed by its ref* already ends in it, and `mlab/_h0h3ln
[h0h3ln]` would be the ref printed twice. A ref collision is exactly a case where both candidates
carry ref-form addresses, so this arm is reachable from the very defect Phase B exists to fix.

Both call sites must therefore share one function rather than restate the rule:

```go
func addressWithRef(address, ref string) string {
	if ref == "" || strings.HasSuffix(address, "/"+ref) {
		return address
	}
	return address + " [" + strings.TrimPrefix(ref, "_") + "]"
}
```

`displayAddress` becomes a one-line caller of it, and `msgCandidateLine` the other. A test asserts
the two agree on a ref-form address — the case that a restated rule would get wrong.

## 6. Testing

### 6.1 Unit

- `handleSend` local branch delivers, and **both** `from`s are asserted: the NDJSON frame's
  top-level `Frame.From` is `uds:` + the sender's inbox, *and* the parsed wrapper's `from` attribute
  is the same — neither alone would catch the other being wrong (§4.1).
- No helper is involved: `postDeliver` is never called (assert on the existing fake), and no helper
  is acquired.
- Mode: a local send with `--mode bypass` produces `FromMode: bypass` with no host entry consulted;
  an invalid mode is still a 400.
- The pair rate limit applies to a local send and its refusal **is audited**; a local resolution
  failure is **not**. One case each — this is the boundary §4.3 fixes, so it is tested, not assumed.
- Exactly one audit row (`DirOut`) per local send, with `ToHostID` = this host.
- `Resolve` runs over this host's inventory with the same snapshot rules: a `Partial` local
  inventory yields `not_ready`, not a guess.
- **No refusal on the local path renders an empty alias.** Drive each local refusal arm and assert
  the detail contains this host's alias — this is the `entry`-substitution guard (§4.1).
- Self-target: origin and target the same session is refused `self_target`, and the check is on the
  identity tuple — a self-send addressed by ref and one addressed by name are both refused.
- `AmbiguousCandidate.Ref` is populated from `c.Ref`, and empty for a candidate with no ref.
- `addressWithRef` omits the bracket in **both** cases — empty ref, and an address already ending in
  the ref — and `displayAddress` / `msgCandidateLine` agree on the same input.

**Refusal codes are specified per address form, not per row type.** "A proxy row is refused
`not_deliverable`" is false as stated: most such rows are never selected by `Resolve` at all and
come back `peer_not_found`. The requirement is narrower and testable:

> For every address form — `tmux:<name>`, a bare tier-4 name, a name tier, a ref tier — a local
> target and an equivalent remote target produce **the same code**.

The test is a table over those four forms, run twice against the same fixture rows, once local and
once remote, asserting the codes match. That is the property L1 actually promises; enumerating which
row type yields which code would encode the resolver's current internals into the test.

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
  the wrong place. Do this **twice**, once mutating the top-level `Frame.From` and once the wrapper
  attribute: a test asserting only one of them passes the other mutation (§4.1);
- move the pair-limit check to *before* the audit insert on the local path — a test must catch that
  the refusal stopped being recorded;
- make `addressWithRef` drop its `HasSuffix` arm — a test must catch `mlab/_h0h3ln [h0h3ln]`.

**The dedup mutation from the first draft is deleted, not weakened.** It asked that removing a local
dedup check turn something red. Nothing would: the id is minted per attempt inside the handler, so
no local send can be a duplicate of another (§4.3, L8). Writing a test that appeared to cover it
would mean fabricating a duplicate the code cannot produce — a test pinned to a false statement,
which is worse than no test because it defends the falsehood against anyone who later fixes it.

### 6.4 Real-machine acceptance

1. `pdx msg send mlab/<name>` to another session on this host delivers, and the receiver's message
   shows `from: uds:/tmp/cc-socks/<sender pid>.sock`.
2. The receiver replies natively; the reply reaches the sender.
3. `pdx peers` and `pdx msg send` agree: every address the table prints is one the sender accepts.
4. Two same-named local sessions: the refusal lists both with distinct refs, and each ref delivers to
   the intended one. **This is the one that closes §5.1's defect — do not sign it off from tests
   alone.**
5. `pdx msg send <your own address>` is refused `self_target`, and the session's inbox receives
   nothing.

**Every item above is signed off by running it or recorded as not run.** v4's §9.7 was skipped and
took a real defect with it (§5.1); the lesson is not "try harder next time" but that an unrun
acceptance item must be reported as unrun, not left ambiguous in a checklist.

## 7. Out of scope

- **A local helper for symmetric audit** (§4.2) — additive, deferred with its rationale.
- **#1118's namespace alignment** — superseded. There is no second namespace to align once L1 lands;
  `ListAgents` keeps its own numbering for its own tool, and nothing has to reconcile them.
- **Native `SendMessage` deprecation** (L6). It remains the right tool for in-process subagents and
  for anything pdx does not model.

## 8. Corrections folded in from review

One cross-model review (codex, gpt-5.5) before planning. Recorded in the house style of the v4
spec's §11, because each was a claim the first draft made confidently and wrongly, and a later
reader re-deriving them would land in the same place.

| first draft | why it was wrong | now |
|---|---|---|
| "policy applies to local unchanged" (§4.3) | audit-in, dedup, host limit, pair limit and mode clamping all live in `handleDeliver`, which a local send never reaches — so nothing was inherited and every item had to be decided | §4.3 decides each one, with its reason |
| local delivery runs dedup, and a mutation removing it must turn a test red (§6.3) | the id is minted per attempt by this handler and `SendRequest` carries none, so a local send can never be a duplicate; the check could not fire and the test could only have been faked | L8: dedup does not run locally; the mutation is deleted and replaced with three that can fail |
| `HopChain: req.HopChain` in the local frame (§4.1) | `SendRequest` (`wire.go:280`) has no such field — the line does not compile | `HopChain: ""`, with the reason (a CLI send is the first hop) |
| "build the frame with the sender's own inbox as the reply address" (§4.1) | there are **two** `from`s — `BuildFrame`'s socket argument becomes `Frame.From`, the real reply address, while `Wrapper.From` is display text inside the content — and a test asserting one would miss the other | both named explicitly; §6.1 asserts both and §6.3 mutates each separately |
| the local branch replaces the step-3 refusal in place | step 3 runs before `findOrigin`, and the local frame needs the origin | decided at step 3, taken at step 5; §4.1 gives the full step table |
| the local path is "resolve → write" | `entry` is zero locally and is read 24 times after step 3; an unsubstituted `entry.Alias` renders as an empty host in the error a human then reads | §4.1's substitution table, plus a test that no local refusal renders an empty alias |
| bracket omitted "when `Ref` is empty" (§5.2) | `displayAddress` has a second arm — an address already ending in its ref — and a ref collision is precisely when candidates carry ref-form addresses, so the arm is reachable from the defect Phase B fixes | one shared `addressWithRef`, both arms, asserted against `displayAddress` |
| `omitempty` "because a tier-4 candidate has no agent" (§5.2) | agentless is the common cause, not the rule; any row without a live cc agent has no ref | the rule is "no ref"; agentless is given as the usual example |
| refusal codes stated per row type — proxy / `agent: null` / `inbox_dead` (§6.1) | most such rows are never selected by `Resolve` and come back `peer_not_found`, so the test would have contradicted the resolver | the property is per **address form**: local and remote give the same code for the same form |
| local bypass is "the same as remote, unchanged" (§4.3) | remote bypass is gated by the peer host's `AllowBypass`; locally there is no such flag, so the authorisation basis genuinely changes | stated as its own boundary: admin route plus live origin attribution |
| "the delivery channel has no return path" (§2.1) | true of one write operation, but the bridge does relay replies (`reply.go`); left broad it would have read as ruling out the deferred local helper | narrowed to "no synchronous return payload", with the reply path named |
| sending to yourself was not mentioned | L1 makes it reachable for the first time, and the frame would carry the receiver's own socket as its reply address, with nothing in the path to stop a loop | L7: refused `self_target` (§4.5) |
