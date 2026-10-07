---
name: pdx-team
description: Purdex lead / member / team and context relay. Use when the work is large enough to split across sessions, when a `[pdx team]` notice arrives, when you are a lead or a member, or when context is running out. Says how to ask for lead mode, how to wait for the answer, what a lead and a member do, and that self relay is the Purdex mod's job, never the agent's.
---

# pdx-team — lead / member / team and context relay

Vocabulary (spec §4): a **lead** runs a **team** of **members**. A member is never called a worker (that word is Nexen's headless execution). **接力** (relay) is a new conversation taking over when context is full; **切換** (handoff) is terminal ↔ worker.

## When to ask for lead mode, and how to wait

- Ask only when the work is **large and parallel**: several independent pieces that would each take a session a long time. One sequential task is not a reason.
- Run `pdx lead request --reason "<why>" [--max-members N] [--root <dir>]` **in the foreground**, with Bash `timeout: 600000`. **Never in the background**: the approval is a hard lock on this session and a background run defeats it.
- The answer is a person's click in Purdex.app. **Never approve yourself**: there is no `pdx` command that approves, and you must not look for another way.
- Exit 0 means approved. **Treat a timeout (exit 11) as no**, like a denial (exit 10). Exit 13 means the rules refused (you are already a lead, or a member cannot lead).

## As a lead

- `pdx spawn --root <dir> [--repo <name>]` opens a member and prints its address and ref; `pdx kill <ref>` closes one; `pdx team` lists them. Address members by **ref** (`<host>/_xxxxxx`): names change, refs are redirected across relays.
- **Recommend a worktree** to each member — have it `EnterWorktree`, or prepare one for it. Where the member works is your call (U10).
- When a `[pdx team] <ref> context 已用 NN%` notice arrives, **you decide** whether and when to relay that member: `pdx relay <ref>`. The daemon only detects and reports (U9).
- Write the team roster (each member's ref, address, task and worktree) into §8 「協作關係」 of your own handoff, so the conversation that takes over from you still knows its team.

## As a member

- **Never relay yourself.** Your relay is the lead's to start.
- Report to the lead's address (`pdx msg send <lead address> "..."`), not to the user.

## Self relay

- The **Purdex mod asks the user on its own** when this session's context passes the threshold. **You never ask for a relay and never approve one.** When the mod's prompt arrives, write the handoff file it names and answer `HANDOFF-WRITTEN`, nothing else.
- `/relay off` / `/relay on` / `/relay status` is **the user's switch, not yours**. Do not run it.
