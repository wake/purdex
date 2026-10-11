# Deck / chat presentation rules shared by Mac and iOS — v1 (2026-10-11)

Rules, not layout. The two apps follow the same rules for **what is shown and what each state looks like**; layout, sizes and
gestures follow each platform ("一致不代表介面要一樣", user 2026-10-11). Strings are the zh-TW keys the Mac uses (`spa/src/locales/zh-TW.json`);
iOS uses the same wording.

## 0. Principles (user decisions, do not reopen)
- P1 The deck is the terminal, drawn: everything stays open (no auto-collapse of the conversation), steps are always shown; the
  chat view is the same conversation with less detail (one row per turn's work).
- P2 **What the terminal shows, the deck shows** (2026-10-11): queued messages, step results, turn end, background work. Where the
  data is missing, show nothing rather than guess, and report the gap.
- P3 Everything left-aligned (user, agent, peer, system rows alike). One typeface family for prose; agent text has no bold
  (same weight throughout): Markdown headings and strong/b are drawn at body size and body weight (only the block spacing
  remains); CJK-only extra letter spacing (Latin is never spread).
- P4 Font size and line height are a per-device user setting on the Mac (deck and chat); iOS may offer the same later. Do not copy the
  Mac's pixel values.

## 1. Voices (who is speaking)
| Voice | How it is set apart | Header text |
|---|---|---|
| User (you) | purple family; block with border + background at the same strength as the selected sidebar item | `你 · {time}` |
| User, absorbed mid-turn (queued in the terminal, read at a tool boundary) | as user | `你 · {time} · 回合中送達` (new key; replaces the misleading `你 · 排隊中` for delivered messages) |
| Agent | no block; header `◆ {model} · {time}` one step brighter than system grey | — |
| Peer message | cyan family; keep the original line breaks | `來自 {name} · {time}` |
| Task report / background report | grey, dashed rule | `背景任務回報` |
| Scheduled wake-up | grey | `排程喚醒` |
| System rows (interrupted, compacted, …) | `--text-system` grey (#949494 dark / #6e6e6e light), one size under the text | `已中斷`, `Context 已壓縮 · {time}` |

## 2. Steps (tool calls)
- Every step starts with a **status dot** (like the terminal's coloured ●): running = pulsing accent; ok = green (status done, and for
  commands exit code 0 or absent); failed = red (status failed, **or done with a non-zero exit code**); denied / interrupted / any
  unknown status = muted grey (never green). Only one running indicator per step.
- A text chip stays at the end of the header for the non-ok states: `失敗`, `exit {n}`, `已拒絕`, `已中斷`.
- Output is folded: `▸ 輸出 · {n} 行`. On failure both the fold label and the output text are red.
- Command (execute) card: the command block is dimmed one step and brightens on hover (iOS: on press / when expanded).
- File change (edit / create) card: collapsed to the **first 3 diff lines**, dimmed, `+N −M` visible; an expand control at the end of
  the header shows the longer view (16 lines) and `顯示全部 {n} 行` when there is more. No expand control when the diff is ≤3 lines.
  Expansion is remembered per step for the tab.
- Tool-row text (paths, commands) is one step dimmer than prose.
- Subagent step: `子 agent · {type}`, description, time (ticking while running), state dot; unfolds its own steps in place, one level
  indented.
- Thinking: one collapsed row `▸ 思考 · {s} 秒`.

## 3. Live line and turn end (bottom of the stream)
- While the agent works, one grey line at the end of the stream (above the input / dock), ticking once a second:
  - a tool running: `✻ 執行中 · {tool} · {elapsed}`; with subagents: `✻ 執行中 · {tool} · {n} 個 subagent · {elapsed}`;
  - only subagents: `✻ 執行中 · {n} 個 subagent · {elapsed}`;
  - nothing running (the model is thinking or writing): `✻ 思考中… · {elapsed}` (thinking vs writing cannot be told apart live);
  - waiting for you: `✻ 等你回覆` in the waiting colour.
  Idle: no line. Its appearing / disappearing must not jump a reader pinned to the bottom.
- A finished turn ends with `✻ 完成 · {duration} · {HH:mm}` (grey, left). Interrupted / failed turns already say so; no extra line.

## 4. Queued messages
- Messages the app is holding because the agent is busy (`你 · 等 agent 空閒` today) are drawn **grey at the end of the stream** (inside the scrolled content, after the live line — it moves with the stream; never pinned above the input),
  with `撤銷`. With an empty input, **Up recalls all of them into the input** (Mac); iOS: an equivalent action (e.g. tap the grey
  message → back into the input). Only messages not yet handed to the session can be recalled.
- Messages queued **inside Claude Code** (typed in the terminal while busy, peer messages) are shown the same way at the end of the
  stream once the daemon carries them (header `queued: [{text, at, source user|peer, from?}]`, #2619; omitted when the session is not live or ended; draw only while running / waiting); when absorbed they disappear from the
  tail and appear as a normal user / peer message where they were read (`回合中送達`). Background-task notifications are not shown
  as queued.

## 5. Session status row (under the input)
- Terminal-style: model · effort, cwd + worktree / branch; cost on the right. Background = the host colour at its middle tone.
- Data: the session's Claude Code statusLine snapshot (the Mac reads `useAgentStore.ccStatus[host+code].raw`, the same snapshot
  that feeds the usage display), falling back to the pane's recorded cwd. No new daemon field.

## 6. Input
- Draft (text + attachments) is kept per tab across tab switches. Attachments show as thumbnails with `[Image #N]` placeholders in
  the text; tapping opens the original.
- Mac: Esc clears the draft with `已清除草稿 · ⌘Z 復原` for 5 s; focus goes to the input on tab / view switch. iOS: pick its own
  gestures; the rule is "clearing a draft is undoable".

## 7. Scrolling
- Pinned to the bottom → follow new content. Left pinned when switching away → come back at the bottom; scrolled up → come back at
  the same place.
- Scrolling up loads earlier turns down to the start of the session (`載入較早的內容…`, then `已到對話開頭`), keeping the reader's
  anchor.

## Not settled yet (follow the Mac later)
Exact colours, sizes and spacing; background-work display (needs daemon data, #2592).
