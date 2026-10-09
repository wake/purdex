---
name: pdx-team
description: Purdex lead / member / team and context relay. Use when the work is large enough to split across sessions, when a `[pdx team]` notice arrives, when you are a lead or a member, or when context is running out. Says how to ask for lead mode, how to wait for the answer, what a lead and a member do, and that self relay is the Purdex mod's job, never the agent's.
---

# pdx-team — lead / member / team and context relay

Vocabulary (spec §4): a **lead** runs a **team** of **members**. A member is never called a worker (that word is Nexen's headless execution). **接力** (relay) is a new conversation taking over when context is full; **切換** (handoff) is terminal ↔ worker.

## When to ask for lead mode, and how to wait

- Ask only when the work is **large and parallel**: several independent pieces that would each take a session a long time. One sequential task is not a reason.
- Run `pdx lead request --reason "<why>" --name "<team name>" --label "<短名>" [--max-members N] [--root <dir>]` **in the foreground**, with Bash `timeout: 600000`. **Never in the background**: the approval is a hard lock on this session and a background run defeats it.
- Always give `--name`: a name for the team's work (at most 64 bytes); it is shown in the team panel, and the user may change it when approving.
- Always give `--label`: a short, meaningful name of your own for the tab group label, about five Chinese characters (10 display columns), e.g. `A 線`, `資源線`, `資源派工` — a summary, never the name cut short.
- The answer is a person's click in Purdex.app, or an automatic approval when the user has turned on unattended mode (which you never turn on and never ask for). **Never approve yourself**: there is no `pdx` command that approves, and you must not look for another way.
- **Never turn on 無人值守模式 (unattended mode).** It is the user's switch in Purdex.app: there is no `pdx` command for it, and you must not call the daemon's route or edit host config to get around that.
- Exit 0 means approved. **Treat a timeout (exit 11) as no**, like a denial (exit 10). Exit 13 means the rules refused (you are already a lead, or a member cannot lead).

## As a lead

- `pdx spawn [--cwd <dir>] [--title <t>] [--model <m>] [--effort <e>] [--brief-file <f> | --brief <text>] [--task-subject <s> [--done-when <line>]…]` opens a member on this host in `--cwd` (default: your working directory; it must be under the roots you were granted) and prints one JSON line with its address and ref. The brief reaches the member from you, after a first line saying it is your member, so its replies come back to you. `pdx kill <ref>` closes one of your members; `pdx team` lists them. Address members by **ref** (`<host>/_xxxxxx`): names change, refs are redirected across relays.
- **Choose each member's model and effort for its task**: `--model sonnet` for mechanical work, `--model opus` for design; `--effort low|medium|high|xhigh|max`. The host's default model is not fixed, so a spawn without `--model` runs whatever it happens to be today. Check `pdx team`: its MODEL and EFFORT columns show what each member actually runs, from its first turn on.
- Exit 13 means the team rules refused (`not_lead`, `team_full`, `cwd_outside_grant`, `not_your_member`, `relay_open`; the code is the last word on stderr). Exit 14 (`member_start_timeout`) means the daemon gave up on the member after 20 s and freed its place (this host needs the Purdex hooks). Exit 1 with the member's JSON on stdout means only the brief was not sent: send it yourself with `pdx msg send <address>`.
- If `pdx spawn` ends with `spawn_wait_timeout` (exit 1, after 9 minutes of waiting), the member may still be starting: check `pdx team` first and do not spawn again.
- Hand each piece of work to a member as a task: `pdx task add --to <ref> --subject … --brief-file …` (or `pdx spawn … --task-subject …`, whose brief becomes the member's first task); `pdx task ls` and `pdx team` show what each member is on.
- **Recommend a worktree** to each member — have it `EnterWorktree`, or prepare one for it. Where the member works is your call (U10).
- When a `[pdx team] <ref> context 已用 NN%` notice arrives, **you decide** whether and when to relay that member: `pdx relay <ref>`. The daemon only detects and reports (U9).
- Write the team roster (each member's ref, address, task and worktree) into §8 「協作關係」 of your own handoff, so the conversation that takes over from you still knows its team.

## As a member

- Report with `pdx report <kind>` (`ack` when you start, `ready` before any merge and wait for the lead's go-ahead, `merged`, `done`; `question` / `blocked` with `--needs`), not free text; `pdx task mine` lists your tasks, and after a relay the notice lists them too.
- **Never relay yourself.** Your relay is the lead's to start.
- Report to the lead's address (`pdx msg send <lead address> "..."`), not to the user.

## Self relay

- The **Purdex mod asks the user on its own** when this session's context passes the threshold. **You never ask for a relay and never approve one.** When the mod's prompt arrives, write the handoff file it names and answer `HANDOFF-WRITTEN`, nothing else.
- `/relay off` / `/relay on` / `/relay status` is **the user's switch, not yours**. Do not run it.
- `/relay` (or `/relay now`) is the user's way to relay early: **you never run it**. A session paused with `/relay off` is not relayed by it; the user runs `/relay on` first.
- **Never turn on 無人值守模式 (unattended mode).** It is the user's switch in Purdex.app: there is no `pdx` command for it, and you must not call the daemon's route or edit host config to get around that.
