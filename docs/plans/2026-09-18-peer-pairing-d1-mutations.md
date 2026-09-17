# D1 Peer Pairing — Mutation-Test Record

- Branch: `worktree-phase-d-pairing-ui`
- HEAD sha: `460ef62e39b1e96934d06a43f1a1db237a367ca9`
- Date: 2026-09-18
- Re-run in full after the PR review fix wave: HEAD now includes `3b04f98c`
  (host rename refuses to report success when the daemon returned the old
  alias), `d8198760` (a rename-only PUT re-checks the entry's identity under
  the lock too), `c61b523a` (verify --json omits error on success, matching
  the daemon body), `3037d8f2` (host rename compares the daemon's alias
  exactly), and this branch's own fix for the stale-`idx` fast-path bug
  (§A of the round-2 follow-ups; `TestHandlePutHost_Rename_CaseOnly_AfterConcurrentDeleteShiftsIndex`).
  Rows M1–M11 were re-applied and re-run exactly as originally recorded, to
  confirm every one still guards what it claims at the new HEAD; rows
  M12–M16 are new, covering the five fixes/tests added since the original
  record (Task 6 of the D1 plan).

Task 6 proves the tests written in Tasks 1–5 guard what they claim (spec
§8.1: mutation tests are a deliverable). Each row below was applied as a
temporary one-edit mutation, run against the named test(s) with
`go test -count=1` (no `-race`, no cache), confirmed FAIL, then reverted with
`git checkout -- <file>` before the next row. After all sixteen rows,
`git status --short` was empty (Step 2).

| # | Mutation (exact edit) | File | Command | Result | Failing assertion (as printed) |
|---|---|---|---|---|---|
| M1 | Deleted the `else if !env.OK { rowErr = "peer reported ok=false" }` branch in `fetchHostResult` | `internal/module/peers/module.go` | `go test -count=1 ./internal/module/peers/ -run 'RemoteNotOKWithoutText'` | FAIL (both) | `hosts_verify_test.go:125: body = {Alias:air HostID:air:1 OK:false Error: SelfAlias: DaemonVersion:}, want ok=false error="peer reported ok=false"` and `module_test.go:1141: row.Error = "", want "peer reported ok=false"` |
| M2 | Changed `if h.Token == ""` to `if false` in `fetchHostResult` | `internal/module/peers/module.go` | `go test -count=1 ./internal/module/peers/ -run 'NoOutboundToken'` | FAIL (both) | `module.go:674: fetch should not have been called` (in `TestHandleVerifyHost_NoOutboundToken_NoDial`; line shifted from 668 to 674 since the original record — unrelated code growth, same assertion); `module_test.go:1179: row.Error = "Get \"http://127.0.0.1:1/api/peers\": dial tcp 127.0.0.1:1: connect: connection refused", want "no outbound token"` (in `TestHandlePeers_ScopeAll_NoOutboundToken`) |
| M3 | Changed `if h.HostID != "" && env.HostID != h.HostID` to `if false` in `fetchHostResult` | `internal/module/peers/module.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandleVerifyHost_HostIDMismatch'` | FAIL | `hosts_verify_test.go:108: body = {Alias:air HostID:air:1 OK:true Error: SelfAlias: DaemonVersion:}, want ok=false with a host_id mismatch error` |
| M4 | Replaced `SelfAlias: boundRemoteText(env.Alias)` with `SelfAlias: env.Alias` in `fetchHostResult`'s return | `internal/module/peers/module.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandleVerifyHost_SelfAliasBounded'` | FAIL | `hosts_verify_test.go:144: self_alias length = 5000, want bounded with an ellipsis` |
| M5 | After `res := m.fetchHostResult(...)` in `handleVerifyHost`, inserted a write-on-verify block: `if res.OK && h.HostID == "" { _ = m.core.UpdateConfig(func(cfg *config.Config) error { i := cfg.Peers.FindPeerHostByAlias(alias); if i != -1 { cfg.Peers.Hosts[i].HostID = res.HostID }; return nil }) }` | `internal/module/peers/hosts_verify.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandleVerifyHost_NeverWrites'` | FAIL (both assertions) | `hosts_verify_test.go:174: config file changed by a verify:\nbefore=...\nafter=...` (a full TOML dump showing `host_id = "air:1"` newly written) **and** `hosts_verify_test.go:179: in-memory host_id = "air:1", want still empty`. Both are `t.Errorf` (non-fatal), so both fire in the same run — the original record only transcribed the second. Re-run at this HEAD confirms both print; there is no Fatalf/early-return split between them. |
| M6 | Deleted the fast-path `if other != -1 && other != cur { …409… }` block AND the in-closure `if other := …; other != -1 && other != i { … }` block in `handlePutHost` (post-fix, the fast-path variable is `cur`, not the pre-fix `idx`) | `internal/module/peers/hosts.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandlePutHost_Rename_(CollisionCaseInsensitive_409Unchanged\|ConcurrentCollision_409)$'` | FAIL (both) | `hosts_test.go:1652: status = 200, want 409; body={"alias":"mini",...}` (`TestHandlePutHost_Rename_CollisionCaseInsensitive_409Unchanged`); `hosts_test.go:1794: PUT status = 200, want 409; body={"alias":"air26",...}` (`TestHandlePutHost_Rename_ConcurrentCollision_409`) |
| M7 | Deleted only the in-closure uniqueness re-check `if other := cfg.Peers.FindPeerHostByAlias(req.Alias); other != -1 && other != i { …409… }` (fast-path check kept) | `internal/module/peers/hosts.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandlePutHost_Rename_ConcurrentCollision_409'` | FAIL | `hosts_test.go:1794: PUT status = 200, want 409; body={"alias":"air26","url":"https://a.example","host_id":"air:1","verified":true,"has_token":true,"has_inbound_token":true,"allow_bypass":false}` |
| M8 | Changed both `other != cur` (fast-path) and `other != i` (in-closure) to `true` in `handlePutHost`'s uniqueness checks (kept `cur` referenced via `_ = cur` so the now-orphaned local still compiles — the mutation itself is the two comparisons) | `internal/module/peers/hosts.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandlePutHost_Rename_CaseChangeOfOwnAliasOK'` | FAIL | `hosts_test.go:1673: status = 409, want 200; body={"error":"alias \"Air\" is already used by another host"}` |
| M9 | Deleted the fast-path `config.ValidateAlias(req.Alias, localAlias)` call in `handlePutHost` (kept the in-closure one; added `_ = localAlias` to keep the now-unused local compiling — the mutation itself is the deleted validation call) | `internal/module/peers/hosts.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandlePutHost_Rename(_Invalid_400\|InvalidWithToken_400NoDial)$'` | `_Invalid_400` PASS (closure still catches the value); `InvalidWithToken_400NoDial` FAIL — this split is exactly why the no-dial test exists (Task 3) | `hosts.go:221: fetch should not have been called` (in `TestHandlePutHost_RenameInvalidWithToken_400NoDial`) |
| M10 | Replaced `sanitizeCell(resp.SelfAlias)` with `resp.SelfAlias` in the drift line of `runPeersHostVerify` | `cmd/pdx/peers.go` | `go test -count=1 ./cmd/pdx/ -run 'SelfAliasSanitized'` | FAIL | `peers_test.go:1831: stdout contains a raw control byte: "air  ok  host_id a:1  daemon v\\a\n  self alias: evil\\x1b[31mred\n  alias drift: peer calls itself evil\x1b[31mred\n"` |
| M11 | Changed `!strings.EqualFold(resp.SelfAlias, resp.Alias)` to `resp.SelfAlias != resp.Alias` in `runPeersHostVerify` | `cmd/pdx/peers.go` | `go test -count=1 ./cmd/pdx/ -run 'NoDriftWhenSameCaseInsensitive'` | FAIL | `peers_test.go:1800: stdout = "air  ok  host_id a:1  daemon x\n  self alias: AIR\n  alias drift: peer calls itself AIR\n", want no drift line for a case-only difference` |
| M12 | Changed `if row.Alias != newAlias` to `if false` in `runPeersHostRename` | `cmd/pdx/peers.go` | `go test -count=1 ./cmd/pdx/ -run 'TestRunPeersCmd_HostRename_OldDaemonIgnoredAlias\|TestRunPeersCmd_HostRename_OldDaemonIgnoredCaseOnlyRename'` | FAIL (both) | `peers_test.go:1956: exit code = 0, want 1`, `peers_test.go:1959: stdout = "renamed air -> air\n", want no success line`, `peers_test.go:1962: stderr = "", want the not-applied message` (`TestRunPeersCmd_HostRename_OldDaemonIgnoredAlias`); `peers_test.go:1977: exit code = 0, want 1`, `peers_test.go:1980: stdout = "renamed air -> air\n", want no success line` (`TestRunPeersCmd_HostRename_OldDaemonIgnoredCaseOnlyRename`) |
| M13 | Changed `row.Alias != newAlias` to `!strings.EqualFold(row.Alias, newAlias)` in `runPeersHostRename` | `cmd/pdx/peers.go` | `go test -count=1 ./cmd/pdx/ -run 'TestRunPeersCmd_HostRename_OldDaemonIgnoredAlias\|TestRunPeersCmd_HostRename_OldDaemonIgnoredCaseOnlyRename'` | `…OldDaemonIgnoredCaseOnlyRename` FAIL; `…OldDaemonIgnoredAlias` stays green (a fully-ignored, non-case rename is still caught by `EqualFold`, since "air" and "air26" differ regardless of case — only the case-only mutation escapes it) | `peers_test.go:1977: exit code = 0, want 1`, `peers_test.go:1980: stdout = "renamed air -> air\n", want no success line` |
| M14 | Removed `omitempty` from `Error` in `cliVerifyHostResponse` | `cmd/pdx/peers.go` | `go test -count=1 ./cmd/pdx/ -run 'TestRunPeersCmd_HostVerify_JSONPassthrough'` | FAIL | `peers_test.go:1853: json has error key = , want omitted on success (matching the daemon body)` |
| M15 | Changed `if verifying || renaming` back to `if verifying` in `handlePutHost`'s commit closure | `internal/module/peers/hosts.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandlePutHost_RenameOnly_ConcurrentRecreate_409NotRenamed'` | FAIL | `hosts_test.go:1325: PUT status = 200, want 409; body={"alias":"air26","url":"https://a.example","host_id":"","verified":false,"has_token":false,"has_inbound_token":true,"allow_bypass":false}` |
| M16 | Changed the fast-path `other != cur` back to `other != idx` in `handlePutHost` (kept `cur` referenced via `_ = cur` so it still compiles) | `internal/module/peers/hosts.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandlePutHost_Rename_CaseOnly_AfterConcurrentDeleteShiftsIndex'` | FAIL | `hosts_test.go:1707: PUT status = 409, want 200; body={"error":"alias \"Air\" is already used by another host"}` |

Every mutation was reverted with `git checkout -- <file>` immediately after
its test run; `git status --short` was empty after all sixteen.

**M9 note:** the brief anticipates exactly this split — `_Invalid_400` alone
stays green because the in-closure `config.ValidateAlias` re-check still
catches the invalid alias (just after paying for a network round trip when a
token is also supplied), which is why Task 3 added the no-dial variant as a
second, independent guard. Both facts are recorded above, not "fixed."

**M5 note (correction from the original record):** the original record
listed only the in-memory assertion (`hosts_verify_test.go:179`) as the
failing one. Re-running the identical mutation at this HEAD shows the test
actually has two independent `t.Errorf` calls — one comparing the config
file's bytes before/after (`hosts_verify_test.go:174`), one comparing the
in-memory `HostID` (`hosts_verify_test.go:179`) — and neither is `t.Fatalf`
or gated behind an early return, so both fire in a single run once the
write-on-verify mutation is introduced. The original record's omission was
an incomplete transcription, not a real behavior difference; both are
recorded above.

**M13 note:** `TestRunPeersCmd_HostRename_OldDaemonIgnoredAlias` renames
"air" to "air26" against a fake daemon that always echoes back "air". Under
`EqualFold`, "air" vs "air26" are still unequal regardless of case, so the
mutation does not weaken that test — it only weakens the narrower
case-only-rename test, exactly as M12's sibling in the D1 review follow-up
intends to demonstrate: a naive `EqualFold` relaxation would silently accept
an old daemon's case-only no-op as a successful rename.
