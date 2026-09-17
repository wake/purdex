# D1 Peer Pairing — Mutation-Test Record

- Branch: `worktree-phase-d-pairing-ui`
- HEAD sha: `fef5937c3fafc49b621a56b931780d0abce85239`
- Date: 2026-09-18

Task 6 proves the tests written in Tasks 1–5 guard what they claim (spec
§8.1: mutation tests are a deliverable). Each row below was applied as a
temporary one-edit mutation, run against the named test(s) with
`go test -count=1` (no `-race`, no cache), confirmed FAIL, then reverted with
`git checkout -- <file>` before the next row. After all eleven rows,
`git status --short` was empty (Step 2).

| # | Mutation (exact edit) | File | Command | Result | Failing assertion (as printed) |
|---|---|---|---|---|---|
| M1 | Deleted the `else if !env.OK { rowErr = "peer reported ok=false" }` branch in `fetchHostResult` | `internal/module/peers/module.go` | `go test -count=1 ./internal/module/peers/ -run 'RemoteNotOKWithoutText'` | FAIL (both) | `hosts_verify_test.go:125: body = {Alias:air HostID:air:1 OK:false Error: SelfAlias: DaemonVersion:}, want ok=false error="peer reported ok=false"` and `module_test.go:1141: row.Error = "", want "peer reported ok=false"` |
| M2 | Changed `if h.Token == ""` to `if false` in `fetchHostResult` | `internal/module/peers/module.go` | `go test -count=1 ./internal/module/peers/ -run 'NoOutboundToken'` | FAIL (both) | `module.go:668: fetch should not have been called` (in `TestHandleVerifyHost_NoOutboundToken_NoDial`); `module_test.go:1179: row.Error = "Get \"http://127.0.0.1:1/api/peers\": dial tcp 127.0.0.1:1: connect: connection refused", want "no outbound token"` (in `TestHandlePeers_ScopeAll_NoOutboundToken`) |
| M3 | Changed `if h.HostID != "" && env.HostID != h.HostID` to `if false` in `fetchHostResult` | `internal/module/peers/module.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandleVerifyHost_HostIDMismatch'` | FAIL | `hosts_verify_test.go:108: body = {Alias:air HostID:air:1 OK:true Error: SelfAlias: DaemonVersion:}, want ok=false with a host_id mismatch error` |
| M4 | Replaced `SelfAlias: boundRemoteText(env.Alias)` with `SelfAlias: env.Alias` in `fetchHostResult`'s return | `internal/module/peers/module.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandleVerifyHost_SelfAliasBounded'` | FAIL | `hosts_verify_test.go:144: self_alias length = 5000, want bounded with an ellipsis` |
| M5 | After `res := m.fetchHostResult(...)` in `handleVerifyHost`, inserted a write-on-verify block: `if res.OK && h.HostID == "" { _ = m.core.UpdateConfig(func(cfg *config.Config) error { i := cfg.Peers.FindPeerHostByAlias(alias); if i != -1 { cfg.Peers.Hosts[i].HostID = res.HostID }; return nil }) }` | `internal/module/peers/hosts_verify.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandleVerifyHost_NeverWrites'` | FAIL | `hosts_verify_test.go:179: in-memory host_id = "air:1", want still empty` |
| M6 | Deleted the fast-path `if other != -1 && other != idx { …409… }` block AND the in-closure `if other := …; other != -1 && other != i { … }` block in `handlePutHost` | `internal/module/peers/hosts.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandlePutHost_Rename_(CollisionCaseInsensitive_409Unchanged\|ConcurrentCollision_409)$'` | FAIL (both) | `hosts_test.go:1618: status = 200, want 409; body={"alias":"mini",...}` (`TestHandlePutHost_Rename_CollisionCaseInsensitive_409Unchanged`); `hosts_test.go:1726: PUT status = 200, want 409; body={"alias":"air26",...}` (`TestHandlePutHost_Rename_ConcurrentCollision_409`) |
| M7 | Deleted only the in-closure uniqueness re-check `if other := cfg.Peers.FindPeerHostByAlias(req.Alias); other != -1 && other != i { …409… }` (fast-path check kept) | `internal/module/peers/hosts.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandlePutHost_Rename_ConcurrentCollision_409'` | FAIL | `hosts_test.go:1726: PUT status = 200, want 409; body={"alias":"air26","url":"https://a.example","host_id":"air:1","verified":true,"has_token":true,"has_inbound_token":true,"allow_bypass":false}` |
| M8 | Changed both `other != idx` (fast-path) and `other != i` (in-closure) to `true` in `handlePutHost`'s uniqueness checks | `internal/module/peers/hosts.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandlePutHost_Rename_CaseChangeOfOwnAliasOK'` | FAIL | `hosts_test.go:1639: status = 409, want 200; body={"error":"alias \"Air\" is already used by another host"}` |
| M9 | Deleted the fast-path `config.ValidateAlias(req.Alias, localAlias)` call in `handlePutHost` (kept the in-closure one; added `_ = localAlias` to keep the now-unused local compiling — the mutation itself is the deleted validation call) | `internal/module/peers/hosts.go` | `go test -count=1 ./internal/module/peers/ -run 'TestHandlePutHost_Rename(_Invalid_400\|InvalidWithToken_400NoDial)$'` | `_Invalid_400` PASS (closure still catches the value); `InvalidWithToken_400NoDial` FAIL — this split is exactly why the no-dial test exists (Task 3) | `hosts.go:221: fetch should not have been called` (in `TestHandlePutHost_RenameInvalidWithToken_400NoDial`) |
| M10 | Replaced `sanitizeCell(resp.SelfAlias)` with `resp.SelfAlias` in the drift line of `runPeersHostVerify` | `cmd/pdx/peers.go` | `go test -count=1 ./cmd/pdx/ -run 'SelfAliasSanitized'` | FAIL | `peers_test.go:1831: stdout contains a raw control byte: "air  ok  host_id a:1  daemon v\\a\n  self alias: evil\\x1b[31mred\n  alias drift: peer calls itself evil\x1b[31mred\n"` |
| M11 | Changed `!strings.EqualFold(resp.SelfAlias, resp.Alias)` to `resp.SelfAlias != resp.Alias` in `runPeersHostVerify` | `cmd/pdx/peers.go` | `go test -count=1 ./cmd/pdx/ -run 'NoDriftWhenSameCaseInsensitive'` | FAIL | `peers_test.go:1800: stdout = "air  ok  host_id a:1  daemon x\n  self alias: AIR\n  alias drift: peer calls itself AIR\n", want no drift line for a case-only difference` |

Every mutation was reverted with `git checkout -- <file>` immediately after
its test run; `git status --short` was empty after all eleven (Step 2).

**M9 note:** the brief anticipates exactly this split — `_Invalid_400` alone
stays green because the in-closure `config.ValidateAlias` re-check still
catches the invalid alias (just after paying for a network round trip when a
token is also supplied), which is why Task 3 added the no-dial variant as a
second, independent guard. Both facts are recorded above, not "fixed."
