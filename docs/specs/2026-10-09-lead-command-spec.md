# `/lead` and `/relay now` — two mod commands (spec + plan)

Date: 2026-10-09. Coordinator: purdex-1f (`mlab/_9vyvs0`). Line: lead/member (A). Order (user): after T-2.

## 1. User decisions

| # | Decision (2026-10-09) |
|---|---|
| D0 | 「目前 relay / team 化(lead) 這些有註冊成 claude code 指令嗎」→ only `/relay` is; lead is asked for in words. 「做 /lead 指令，排在 T-2 之後」 |
| D1 | 「現在的 relay 指令只是切換，不能觸發嗎？」→「要，/relay now 一起加進去，如果只打 /relay 不加參數，等同於 now」 |

## 2. Behaviour

- **`/lead [補充]`** is registered by the Purdex mod, the same way as `/relay` (`$.command.register` at `session.start` in `cmd/pdx/plugin/purdex/hooks/register.js:642`; handler `on('command.run', { command: 'lead' }, …)` like `:807`). Interactive sessions only (a Nexen worker's `claude -p` gets nothing, as for `/relay`).
- **Already a lead** (this session leads a live team): the command answers at once, without a model turn: 「已經是 lead：<team_name> ［<team_label>］（上限 N）」.
- **A member**: answers 「member 不能成為 lead」 (the daemon refuses a member's lead request; do not spend a turn).
- **Otherwise**: the command answers 「已請這個 session 申請 lead，請到 Purdex App 核准」 and submits **one prompt** to the session (`$.prompt.submit`), in Chinese, that tells the agent: the user asked it to become a lead now with `/lead`; follow the `pdx-team` skill and run `pdx lead request --reason … --name … --label …` **in the foreground** (Bash `timeout: 600000`) **at once, without judging whether the work is large enough**; pick the reason, the name and the label from the current work; use what the user wrote after `/lead` (`<補充>`, quoted verbatim, may be empty) for the name, label or member limit when it gives them.
- **Daemon unreachable** (exit 20／21 from the status check): answer 「daemon 連不上，無法申請 lead」 and submit nothing.
- No new daemon route and no new CLI command: the lead check uses what exists (`pdx team --json` → exit 0 = lead; the role the relay `hello` already caches = member). If a narrower read is needed, ask the coordinator first.

## 2b. `/relay now` (and bare `/relay`)

- **`/relay` with no argument means `now`** (today it means `status`; `status` stays available as `/relay status`). `RELAY_USAGE` becomes 「用法：/relay [now|off|on|status]」 and the command's argumentHint 「now|off|on|status（不帶＝now）」.
- **`now`** starts the self relay **at once, whatever the context use**, through **the same path the threshold ask takes** (`pdx relay begin --self --session … --used <current %> --window <n>` → approval in the App, or unattended mode's rule → handoff written → `/clear` → seed). Do not duplicate that state machine: call into it.
- In a **member**: no relay; answer 「member 的接力由 lead 進行（lead 用 pdx relay <ref>）」 (the user's rule on #2062: a member's relay is run by its lead).
- This session paused with `/relay off`: **`now` still relays** — an explicit command overrides the session's own pause (it does not turn the pause off).
- The **host switch** is off: no relay; answer 「主機的自我接力開關是關的」.
- A relay **already in progress** for this session (any state between asked and seeded): no second one; answer 「接力進行中」.
- Daemon unreachable: answer the existing `RELAY_UNREACHABLE`.
- Not in scope: a per-session relay quota (#2062) — when it lands, `now` under unattended mode follows it.

## 3. Skill

`cmd/pdx/plugin/purdex/skills/pdx-team/SKILL.md`, section `## When to ask for lead mode, and how to wait` (heading unchanged — U23 tests find sections by it): after the first bullet add 「When the user runs `/lead` or plainly asks you to become a lead, request it at once: the "large and parallel" test above does not apply.」 Pin it in `cmd/pdx/plugin/embed_test.go`.

## 4. Plan (one PR, ≈ 400 lines; split `/lead` and `/relay now` into two PRs if it grows past 800)

1. `register.js`: register `lead` (description 「Purdex：請這個 session 申請成為 lead」, argumentHint 「[名稱／短名／上限等補充]」) beside `relay`; `command.run{command:'lead'}` handler per §2. The prompt text is a constant in the file; the user's `<補充>` is inserted as data (quoted, length-bounded, control characters stripped), never as instructions to the mod.
2. Tests (`relay.test.ts` style, `claude plugin test`): registered only when interactive; lead → answers, submits nothing; member → answers, submits nothing; unreachable → answers, submits nothing; otherwise → exactly one `prompt.submit`, whose text contains the foreground／`timeout: 600000` instruction and the user's `<補充>` verbatim; a `<補充>` with a newline or a control character is sanitised. Mutation: submit for a lead; drop the foreground instruction.
3. `/relay now`: the bare-`/relay` default change, `now` per §2b by reusing the threshold path; tests — bare `/relay` starts a relay (not status); `now` in a member, with the host switch off, or with a relay in progress answers and starts nothing; `now` after `/relay off` starts one and the session stays paused afterwards; `status`／`on`／`off` unchanged (existing tests stay green). Mutation: bare `/relay` back to status; `now` honouring the session pause.
4. Skill line + embed pin; the skill's `## Self relay` section (heading unchanged) gains one line: 「`/relay` (or `/relay now`) is the user's way to relay early; you never run it.」
5. Guard: `TestHooks_NoEventRegisteredTwiceWithoutMatcher` — the new `command.run` hook carries a matcher (`{ command: 'lead' }`).
6. Coordination: `register.js` is shared with the interface line (88, `mlab/_tn9uwa`): tell it the lines before editing, and before merging.
7. Deploy: daemon (unchanged) + **`pdx setup`** (mod and skill); resources mode stays as it is. Acceptance (coordinator): in a throwaway Haiku session `/lead` → the App shows a lead request with a name and a label; in a lead `/lead` → the "already a lead" line and no request; in a member → the refusal line; in a throwaway session at low context `/relay` → a relay request in the App, approve → handoff → new conversation; `/relay status` still prints the status.
