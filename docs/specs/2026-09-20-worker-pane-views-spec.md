# Spec — worker pane: room and chat views

- Status: v0.1 (2026-09-20) — **design draft for the user, not yet a build plan.**
  Open questions in §10 need answers before a plan is written.
- Scope: how one worker's pane presents itself. No daemon work, no Nexen work
  in the first phase; §9 collects what would need Nexen later.
- Predecessors: P-B (`2026-09-15-pb-execution-pane-spec.md`) built the pane,
  P-B2 (`2026-09-18-pb2-exec-live-stream-spec.md`) the typewriter and tool
  timing, P-B3 (`2026-09-19-pb3-tool-events-spec.md`) tool summaries / status
  / line-numbered diffs, P-B4 (`2026-09-19-pb4-cost-hover-spec.md`) cost.
  Each designed its own slice; this is the first pass over the whole surface.

## 1. Vocabulary

- **worker** — what the user calls one headless agent instance. The wire and
  the code keep saying `execution` (kickoff decision #11): renaming the wire
  is a planned contract change, not part of this.
- **room**（指揮室）and **chat**（聊天模式）— two views of one worker,
  differing "only in interface and in how much it says". They are a `mode` on
  the execution pane content, **not** members of the old
  `terminal | stream` enum: terminal ↔ worker changes what the pane is bound
  to and costs a daemon round trip (Hand to nex / Take to terminal); room ↔
  chat is free, instant and local (kickoff decision #12).

```
{ kind: 'tmux-session', hostId, sessionCode, … }              ← bound to tmux
{ kind: 'execution',    executionId, host?, from?,
  mode: 'room' | 'chat' }                                      ← bound to a worker
```

The UI may still offer all three in one menu, as long as the terminal entry
reads as an action (it interrupts a turn and moves the session) and the other
two as a view switch.

## 2. Direction (user, 2026-09-20)

Room:
> 基本上就是 terminal 的 headless 版本。全螢幕寬，無論是我的輸入或 agent 的
> 回答，資訊都統一從左側開始。每個作業指令都顯示呼叫＋顯示結果，按照長度／
> 量體折疊。思考鍊…也可以折疊顯示。diff、子行程、shell、status 等等，都忠實
> 呈現，常駐類的畫面上另外提供地方顯示常駐。輸入欄全寬無邊框，和現在 1:1。

Chat:
> 左右聊天模式，顯示模式參考 aigora 的形式。

References: the Claude Code terminal transcript (the `⏺` / `⎿` tree, folded
`… +N lines`, line-numbered diffs) for room; Collie
(github.com/AltanS/collie) for **content and input** — collapsible long
output, output search, a plain text field that works with voice dictation, a
one-tap quick-reply dock, and the agent's own prompts turned into tappable
buttons; Aigora for chat.

## 3. What is there today, and what is wrong with it

Screenshots: `docs/specs/assets/2026-09-20-pane/*` (captured on :5174 at
alpha.415 — see the file names in §3.1).

Today the pane is: a one-line header carrying eight things (state · provider ·
profile · cwd | observers · lease · turns · cost · SSE · Interrupt · Terminate
· Take to terminal), then `ConversationMessages` (bubbles: assistant left,
user right), then a bordered Reply box. Tool calls render as blocks inside the
assistant bubble; thinking as a collapsible block; diffs via `ToolDiffView`.

The problems this pass exists to fix:

1. **Two alignments fight each other.** Assistant output is left, user input is
   right, tool output is inside the left bubble — so the eye has three left
   edges and one right one. The user wants **one left edge** in room.
2. **The bubble frame wastes the width it is given.** A worker pane is often a
   full-width tab; a chat bubble column throws away half of it exactly where
   tool output, diffs and tables need it.
3. **The header is a dumping ground.** Eight facts of unequal importance on one
   line, and it is the same header at 1600px and at 900px.
4. **Folding is per-component, not per-volume.** Each block decided its own
   collapse rule as it was built; there is no single "this is N lines / N KB,
   therefore show this much" rule, and no consistent "expand" affordance.
5. **Nothing is persistent.** A background shell, a dev server, a watch — the
   things that are *still running* — have no place on screen (and, today, no
   data either: §9).
6. **No search.** Collie has output search; a long worker transcript has none.

What is good and should not be touched: the typewriter (P-B2), the tool
summary line with status and duration (P-B3), the line-numbered diff, the cost
panel (P-B4), and the input box's plain behaviour (Enter sends, Shift+Enter
newline, lease-aware disabling).

### 3.1 Screenshot index

| file | what it shows |
|---|---|
| `a-pane-full.png` | the pane after a run, scrolled to the top |
| `b1-tool-collapsed.png` / `b2-tool-expanded.png` | a Bash call, both states |
| `c-tool-error.png` | a failing call (`ls /nope`) |
| `d1-thinking-collapsed.png` / `d2-thinking-expanded.png` | the thinking block |
| `e-diff.png` | `ToolDiffView` on an Edit |
| `f-subagent.png` | a Task (subagent) call as rendered now |
| `g-header.png` | the header row |
| `h1-cost-hover.png` / `h2-cost-panel.png` | P-B4 |
| `i-input.png` | the Reply box and its relation to the content |
| `j-sidebar.png` | the Executions view row |
| `k-narrow.png` | the same pane at ~900px |

## 4. Room

### 4.1 Frame

- Full width, no bubble column, no max-width clamp beyond a comfortable
  reading measure for prose (proposal: prose paragraphs wrap at ~90ch, but
  code, output, diffs and tables use the full width).
- **One left edge.** Everything — the user's own turns, the agent's prose, tool
  calls, results, thinking, diffs — starts at the same x. Authorship is carried
  by a marker in the gutter, not by alignment:
  - user turn: a distinct gutter mark + slightly stronger text
  - agent prose: no mark
  - tool call: a status dot (running / ok / error / denied), like the `⏺`
  - everything belonging to a call: an indent rail under it (the `⎿`)
- Vertical rhythm by turn, not by message: one turn = the user's line, then
  everything the agent did, then its closing prose. A turn separator that also
  carries the per-turn facts (duration, cost) — the data is already there from
  P-B4.

### 4.2 The operation block (呼叫＋結果)

One tool call is one block:

```
● Bash  python3 tools/mkthumb.py --missing            1.2s
  ⎿ 完成 3/3
    [bb2s] bb2-feast        30 張   藍 v1 · 宴客廳
    … +166 lines                                    [expand]
```

- Header line: status dot, tool name, `primary_arg` (from N2, full value, not
  truncated by the daemon), duration. Wraps to more lines when the argument is
  long — never truncated to an ellipsis in the middle.
- Result underneath, on the indent rail.
- `denied` and `error` get their own dot colour and keep the result visible by
  default (a failure you have to expand is a failure you will miss).

**Folding by volume** — one rule for every block type, driven by the numbers
N2 already gives (`output.total_lines`, `output.total_bytes`, `truncated`):

| volume | default |
|---|---|
| ≤ 6 lines and ≤ 1 KB | shown whole, no affordance |
| ≤ 40 lines | first 6 lines + `… +N lines [expand]` |
| > 40 lines, or `truncated` | first 3 lines + `… +N lines [expand]`, and the expand state says whether the daemon itself cut it at 8 KB |
| any `error` / `denied` | one step less folded than the table says |

(Numbers are proposals — they want one session of real use to settle.)

Expansion is per block and remembered per pane while the pane lives; "expand
all" / "collapse all" on the turn separator.

### 4.3 Thinking

Collapsed by default to a single line (`Thought for 4s · 320 words`), expands
in place on the same rail. Data is already there (`ContentBlock.type ===
'thinking'`, rendered by `ThinkingBlock`). When the turn is live the thinking
line is where the typewriter shows before the first visible block.

### 4.4 Diff

As P-B3 built it (line numbers, +/-), full width, with the display budget from
#1227 applied: fold hunks beyond the first N lines, per §4.2's rule, and say
when the daemon's 2000-line cap truncated it.

### 4.5 Subagents (子行程)

A `Task` call is a block whose result is *another agent's* run. Render it as a
nested rail: the Task block folds to one line (`Task · 分析 notes.md · 8 tools
· 12s`) and expands into the child's own blocks, one indent deeper.

This needs data we do not have (§9): subagent tool calls are addressable
(`parent_tool_use_id`) but are not currently collected (#1228), and a subagent
has no result frame of its own, so its cost and end time are not separable
(measured in P-B4).

### 4.6 Persistent things (常駐)

A worker accumulates things that outlive a turn: background shells, dev
servers, watches, and — if we choose to show it — the lease and observers.
These do not belong in the transcript, because the transcript is chronological
and these are *current*. Proposal: a **dock** on the pane, collapsed to a
single row by default:

```
▸ 2 running   ● bash#3 pnpm dev (4m)   ● bash#7 tail -f log (2m)
```

It expands into a small table with a kill/inspect action per row. When empty it
is not shown at all.

**Today there is no data for this** — see §9 ask A1. Until then the dock can
only show what we already know (lease holder, observers, SSE state), which is
exactly the clutter §3.3 wants out of the header; that alone justifies the
dock.

### 4.7 Header after the pass

Leave on the header only what you steer by: state, the worker's name/brief,
and the two or three actions (Interrupt / Terminate / Take to terminal).
Everything else moves:

| fact | goes to |
|---|---|
| cost | stays as one number, panel on click (P-B4, unchanged) |
| turns | the turn separators carry it; drop from header |
| observers, lease, SSE | the dock (§4.6) |
| provider, profile, cwd | a worker-info popover on the name |

At narrow widths the header degrades to name + state + an overflow menu.

### 4.8 Input

Unchanged in behaviour, restated as design: **full width, no border**, sitting
directly under the content with a hairline separator, growing with the text.
From Collie, two additions worth having (later phase, §11):

- a **quick-reply dock** above the field (a small set of one-tap replies —
  "continue", "run the tests", "explain that" — from host config, the way
  resume templates already work per host);
- when P4 lands, the agent's permission prompt becomes **tappable buttons**
  in place of the text, which is the same idea Collie's "Ask" implements.

Output search (Collie) belongs here too: a find-in-transcript field that
scrolls and highlights, since the transcript is far longer than a scrollback.

## 5. Chat

Same data, minimum ceremony, Aigora's shape:

- left/right bubbles (agent left, you right), comfortable measure, no full
  width;
- tool calls collapse to **one quiet line per turn** — `使用了 5 個工具`
  (expands into the room-style list in place, so nothing is unreachable);
- thinking hidden entirely (a turn that is only thinking shows the typewriter,
  then nothing);
- diffs collapse to `Edited notes.md (+3 −0)`, expanding to the same diff view;
- no dock, no per-turn cost; the header keeps state + cost only;
- errors are **not** hidden: a failed tool shows a single red line with the
  message, expandable.

Chat is what the phone will use, so its rules should assume a narrow viewport
first.

## 6. Switching

`mode` on the pane content, default `room` for a worker the user started from
the Headless launcher, `chat` for … (§10 Q2). Instant, local, no daemon call,
persisted with the pane (so it survives reload and travels in a workspace
snapshot). Two panes on the same worker may differ.

## 7. Data behind each element

| element | source | status |
|---|---|---|
| prose, user turns | `assistant` / `user` raw frames | ✅ |
| typewriter | `stream_event` / `stream_snapshot` (transient, not replayed) | ✅ live only |
| tool call: name, args, status, duration | N2 `tool_use` / `tool_result` | ✅ |
| output volume for folding | `output.total_lines` / `total_bytes` / `truncated` | ✅ |
| diff hunks, `+N −M` | N2 `tool_result` | ✅ |
| thinking | `ContentBlock.type === 'thinking'` | ✅ |
| per-turn cost / tokens / model | `result` frame (P-B4) | ✅ |
| subagent grouping | `parent_tool_use_id` | ⚠️ not collected (#1228) |
| subagent cost / end time | — | ❌ ask A2 |
| running background shells | — | ❌ ask A1 |
| worker status line | — | ❌ ask A3 (or derive) |
| list-level rollup (sidebar) | — | ❌ ask A4 |

## 8. Non-goals

- Changing the terminal pane.
- P4 permission prompts (ordered after this pass by the user); §4.8 only
  reserves the place they will land.
- Renaming `execution` on the wire.
- Multi-worker layouts (two workers side by side is just two panes today).

## 9. Wire asks (to Nexen, to be sent as one batch)

- **A1 — live "what is still running"**: a durable-enough view of background
  shells (id, command, started at, still alive) so a consumer can render the
  dock. Today it is only inferable from tool calls, and never says when one
  ends.
- **A2 — subagent completion**: an event or fields closing a `Task` (its own
  cost, token usage, end time). Today the child's cost is folded into the
  parent `result` and cannot be separated.
- **A3 — worker status**: a short "what is it doing right now" string, or
  enough structure for the consumer to derive one (the headless side has no
  statusline; the tmux side gets one through hooks).
- **A4 — list-level rollup** on the execution summary (cost, turn count, last
  tool) so the sidebar and Host › Nex can show them without loading each
  worker's history. This is the one that most obviously pays for itself across
  three consumers.

All four are additive. They should go to Nexen as one contract change, not one
field at a time.

## 10. Questions for the user

- **Q1 — the turn separator.** Room groups by turn and shows duration/cost per
  turn. Wanted, or is that noise for you?
- **Q2 — chat's default.** Is chat ever the default, or is it always something
  you switch into (and the phone picks it for itself)?
- **Q3 — the dock.** Does it live in the pane (per worker) or on the pane
  header row as a strip? And should lease/observers/SSE move into it, or do
  they belong somewhere else entirely?
- **Q4 — folding numbers.** Take §4.2's table as the starting point and tune
  after a session of use, or do you already know the thresholds you want?
- **Q5 — the wire asks.** Send all four to Nexen now (so its work overlaps
  with the SPA phase), or wait until the SPA is built and we know exactly
  which we need?

## 11. Phases (proposal, after §10 is answered)

1. **R1 — the frame**: one left edge, turn grouping, the folding rule applied
   to every block type, header slimming, dock with only the facts we have.
   Pure SPA, no new data.
2. **R2 — chat**: the second view, the `mode` field, the switch.
3. **R3 — input**: search, quick replies.
4. **R4 — after the Nexen batch (§9)**: dock with real running-shell data,
   subagent nesting, sidebar rollups.
