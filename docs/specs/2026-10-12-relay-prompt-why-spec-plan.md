# Relay prompts say why the relay happened (spec + plan)

Follow-up of the idle relay (`docs/specs/2026-10-11-idle-relay-spec-plan.md`). One PR, mod + daemon defaults; no schema,
no wire change.

## 1. Problem
The write and seed prompts were written for the 70 % threshold relay and are shared by every relay:
- write opens with 「這個 session 的 context 已達接力門檻……請先停下手邊工作」 — wrong for an idle relay (context ≥ 40 %,
  nothing in hand), and already wrong for a manual `/relay` at any percent;
- seed opens with 「前一段對話 context 已滿並已清空」 — same;
- seed step 3 「接著從『下一步』繼續原本的工作」 is the harmful one: an idle session was idle because it was **waiting**
  (a user decision, a peer's merge notice). Told to "continue", the new conversation may act on what was waiting for the
  user.

## 2. User decisions (2026-10-12, do not reopen)
- The idle relay's prompts change as 1f proposed: the opening names the real cause; the new conversation, when the
  handoff's next step is a wait, stops and waits instead of starting work; the handoff must say what it waits for.
- One set of bodies with a variable the mod fills, not a second set of bodies per trigger (keeps the settings page and
  stored custom bodies as they are).

## 3. Design
### 3.1 New public variable `{{why}}`
`RelayPromptVariables` gains `why` (7 names). Every constructor of the relay request `p` stamps an explicit
`p.cause` (review finding: inferring the row from which fields `p` happens to hold sends a lead-less claim and a recovery
to the threshold row), and the mod fills `why` from it with a clause (no final 。):

| constructor (`register.js`) | `p.cause` | `why` |
|---|---|---|
| `begin`, idle ask (`idle` argument) | `idle` | `已閒置 N 分鐘（context P%），趁快取還沒過期先接力` — N = the idle minutes the ask was opened with (stored on `p` as `idleMinutes`; today it is a local in `begin` and `s.idleMinutes` is overwritten by hello), P = `p.idlePct` |
| `begin`, `/relay` or `/relay now` (`manual` argument; a member's own `/relay` too) | `manual` | `使用者手動要求接力` |
| `begin`, otherwise (threshold ask) | `threshold` | `context 已達接力門檻` |
| member claim (~:1052), with or without `lead` | `lead` | `lead 安排接力` |
| reload recovery (~:1262) | `recovered` | `mod 重新載入過，原因沒有保留` |

- One pure function `whyOf(p)` in the mod (exported for tests) switches on `p.cause`. A missing / unknown cause gives
  the `recovered` text; `cause: 'idle'` with `idleMinutes` not an integer 1–59 or `idlePct` not a finite number gives
  `已閒置一段時間，趁快取還沒過期先接力`. It never throws.
- `why` is public (bodies may use it), filled in `compose` for write, fix and seed alike.

### 3.2 Default bodies (`internal/team/relay_prompts.go`, mirrored to `prompts.js` by the generator test)
write:
```
這個 session 要接力了：{{why}}。接力已核准（之後會 /clear）。
請用你完整的工具撰寫接力檔：{{path}}（手邊有工作就先停下）

要求：
- 接力檔必須自成一體：讀它的是一個完全沒有這段對話記憶的新對話。
- 如果現在是在等使用者、lead 或其他 session（例如等決定、等 merge 通知），§2 的「下一步」要寫清楚在等什麼、等誰、等到之後的第一個動作（具體到指令）。

機器提供的 git 狀態（照抄進 §3）：
{{git}}
```
seed:
```
你是接手的新對話：前一段對話已接力並清空（{{why}}）。
請先讀接力檔 {{path}}，然後：
1. 用三行複述：目標、下一步第一個動作、目前有哪些檔案異動。
2. 跑 `git status` 確認與接力檔一致，不一致就指出來。
3. 如果「下一步」是在等使用者、lead 或其他 session，就停在這裡等，不要自己開始新的工作；否則接著從「下一步」繼續原本的工作。
回覆的第一行請寫「↪ 接手自 {{old_ref}}」。
```
fix: unchanged. Fixed heads / tails: unchanged (the eight `# HANDOFF` headings, tags, facts stay byte for byte).

Step 3 and the new write requirement are trigger-neutral on purpose: a threshold relay that lands while waiting gets the
same protection, and the member-claim / recovery paths (which do not know the cause) are covered without a wire change.

### 3.3 Settings page
No component change: the editor lists `variables` from `GET /api/relay/prompts`. Add help text
`hosts.relay.prompts.var.why` — zh-TW 「接力原因（mod 依觸發方式填入：閒置、達門檻、手動、lead 安排）」, en
"Why this relay happened (the mod fills it: idle, threshold, manual, arranged by the lead)".

### 3.4 Stored custom bodies
Not migrated. A stored body without `{{why}}` works exactly as today. (mlab stores a custom write body; after deploy the
lead edits its first two lines by hand — §5 step 4 — not part of this PR.)

## 4. Compatibility
- The mod takes the **effective** body from the daemon (`bodyFor` → `pdx relay prompts`). New daemon + old mod: the old
  mod's `fill` leaves `{{why}}` as typed, so a relay in the minutes between the daemon restart and `pdx setup` shows the
  literal `{{why}}` (cosmetic; the relay still works). The same holds for a stored custom body that uses `{{why}}` on a
  host whose mod is older. Deploy runs `pdx setup` right after the restart on both hosts.
- Old daemon + new mod: the mod falls back to its own `DEFAULT_BODIES` only on failure; with an old daemon it gets the old
  text (no `{{why}}`), which still works.

## 5. Plan (one PR, branch `feat/relay-prompt-why`)
1. Commit this document as `docs/specs/2026-10-12-relay-prompt-why-spec-plan.md`.
2. TDD, tests first:
   - Go `internal/team/relay_prompts_test.go`: the pinned variable list gains `why`; per-kind variable sets (write: path,
     git, why; seed: path, old_ref, why; fix: path); the default bodies hold the §3.2 text.
   - `go test ./cmd/pdx/plugin/ -run TestPromptsJS -update` regenerates `prompts.js`; the test then passes unflagged.
   - `relay.test.ts`: `whyOf` for every `cause` of §3.1 incl. a missing cause and malformed numbers; each constructor
     stamps its cause (a claim without `lead` → `lead`, a recovery → `recovered`); an idle begin → the write prompt and, after
     `/clear`, the seed prompt contain the idle clause with N and P; manual and threshold begins → their clauses;
     member claim → `lead 安排接力`; recovery → its clause; the byte-for-byte default tests (`PRE_P9A_*`, the seed
     pre-T-2 test) move to the new text; a stored custom body without `{{why}}` composes exactly as before.
   - mutations: drop `idleMinutes` from `p` → the idle test goes red; swap the idle / threshold rows → red; leave `why`
     out of `pub` → the "body may use it" test goes red.
3. Locale keys (§3.3) in `spa/src/locales/zh-TW.json` and `en.json`; `spa/src/components/hosts/RelayPromptEditor.test.tsx`
   `VARS` / `DEF` fixtures follow the new list and text.
4. Verify before PR: `go test ./cmd/pdx/...` (incl. the plugin sub-packages), `go test ./internal/team/...
   ./internal/module/team/... ./internal/module/hostconfig/...`, `make lint`; the relay/member vitest files under
   `cmd/pdx/plugin`; and, because locales change, `cd spa && npx vitest run --maxWorkers=3` + `tsc -b` + lint on a freshly
   fetched origin/main merge before merge.
5. Release (lead): daemon restart then `pdx setup` on mlab and air26 (mod changed); then edit mlab's stored write body:
   first two lines → the §3.2 first two lines, and add the §3.2 「如果現在是在等……」 requirement line; everything else in
   that body stays as stored.
