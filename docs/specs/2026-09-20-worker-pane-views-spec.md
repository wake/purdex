# Spec — worker pane: room and chat views

- Status: v1.0 (2026-09-20) — **design settled; every question answered. A
  plan can be written from this.**
  v0.4 added the real screenshots and what they changed (§3.1, §4.2, §4.3, §4.5);
  v0.3 answered Q1–Q3 (§10); v0.2 folded in the source audit (§3.2).
  Q4 (folding numbers) and Q5 (when to send the wire asks) still open.
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
alpha.415 — file names in §3.1). §3.2 is a source-level audit of the same
build: every item there is verifiable in the code, which is why the problems
below are stated as facts and not impressions.

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
3. **The header is a dumping ground.** Eight facts at one visual weight, with
   the destructive actions styled like the facts, and no responsive behaviour
   whatsoever (§3.2).
4. **Folding is per-component, not per-volume.** Three components, three
   different truncation mechanisms, three height caps, and no collapsed
   header tells you how much is hidden — even though the data to do it has
   been on the wire since N2 (§3.2).
5. **Nothing is persistent.** A background shell, a dev server, a watch — the
   things that are *still running* — have no place on screen (and, today, no
   data either: §9).
6. **No search.** Collie has output search; a long worker transcript has none.

What is good and should not be touched: the typewriter (P-B2), the tool
summary line with status and duration (P-B3), `ToolDiffView` (old/new gutters,
`@@` headers, truncation note — already close to the reference), `CostPanel`
(the only responsive logic in the pane today: `panelWidth()` + a resize
listener; use it as the model for everything else), the shared formatters
(`formatUsd` / `formatTokens` / `formatDuration`, so the header, the tooltip
and the panel cannot disagree), and the input box's plain behaviour (Enter
sends, Shift+Enter newline, lease-aware disabling).

### 3.2 Source audit (alpha.415)

**Header** (`execution/ExecutionHeader.tsx`)
- Eight facts at one visual weight: two rows, both `text-xs text-text-muted`,
  only `state` promoted. Nothing anchors the eye.
- Destructive actions styled as facts: Interrupt / Terminate / Take to
  terminal are three text buttons sharing one class; Terminate only turns red
  *after* the first click, and the binding change (Take to terminal) looks
  exactly like an interrupt.
- No responsive logic at all: two `flex-1` spacers, no `flex-wrap`, no
  `min-w-0`, no `truncate` (cwd has a `title` but cannot shrink). ~900px has
  nowhere to put row 2.

**Conversation** (`ConversationMessages.tsx`)
- Already chat-shaped, which is the opposite of room: user is a right-aligned
  bubble at `max-w-[75%]`, assistant is left at `max-w-[90%]` — so **the pane
  never uses its full width at any size**. Room is a rewrite of this
  component, not a tweak.
- Flat rhythm: `p-4 space-y-4`, every block (thinking / text / tool_use /
  tool_result) a sibling at the same depth. **A turn has no container**, so
  there is nothing to label, collapse or hang per-turn facts on — §4.1's turn
  grouping has to create that unit.
- Interrupted and slash-command messages are inline special cases with
  hard-coded colours carrying `TODO: theme token`.

**Folding — three components, three rules**

| | collapsed header shows | expanded cap |
|---|---|---|
| `ToolCallBlock` | name + summary cut at `SUMMARY_LIMIT = 80` | `max-h-60` |
| `ToolResultBlock` | `content.slice(0, 80) + '...'` (different mechanism) | `max-h-60` |
| `ThinkingBlock` | a caret, **no size hint at all** | none |

- **No collapsed header says how much is hidden**, although N2 supplies
  `total_lines` / `total_bytes` / `truncated` and `toolResultFacts` already
  surfaces some of it. This is the largest single gap against the reference's
  `… +166 lines (ctrl+o to expand)`.
- "按照長度／量體折疊" is simply not implemented: all three default to
  `useState(false)` regardless of payload size; no auto-expand policy, no
  expand-all, and fold state is component-local so it dies on remount.
- Three different height caps (`max-h-60`, `max-h-60`, `max-h-64`).

**Pairing and nesting**
- `tool_use` and its `tool_result` render as two independent cards with no
  indent, rule or connector: the pairing exists in the data
  (`tools[block.tool_use_id]`) but is never expressed spatially. The `⎿` rail
  in §4.2 is exactly this relationship.
- `parent_tool_use_id` appears nowhere in the render tree, so subagent calls
  (#1228) would land flat, indistinguishable from top-level ones.

**Input** (`StreamInput.tsx`)
- `mx-2 mb-2 border rounded-xl` — inset, bordered, rounded; the target is
  full-width and borderless, so this is opposite on both counts.
- Auto-grow has **no max height**: a long paste can squeeze the conversation
  to nothing.
- Carries affordances unused here (`showAttach={false}`, an `onHandoffToTerm`
  that is never passed).

**常駐** — confirmed absent: no component, no state, no data path. It is a
wire ask (§9 A1), not a rendering gap.

**Theme debt to clear in the same pass**: `TODO: theme token` in
`ToolCallBlock` ×2, `ToolResultBlock` ×3, `ConversationMessages` ×2,
`MessageBubble` ×1, `ToolDiffView` ×1.

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
| `live-typing.png` | mid-stream: a half-assembled tool input |

Not captured: `d1/d2-thinking-*` — see §4.3, the thinking block never renders
on this machine.

### 3.1.1 What the screenshots changed in this design

1. **The card frame is the problem, more than the alignment.** Every
   operation is a bordered, rounded, full-width card, and one call is *two*
   of them (invocation card + result card) with a faint green fill on the
   successful one. So a five-tool turn is ten cards of equal weight, and the
   agent's own prose is the only thing without a frame — it sits at a
   *different* left edge (x≈107) from the card contents (x≈166).
   Room drops the card: a status dot, the tool name, the argument, and the
   result on an indent rail under it. No border, no fill; only special
   blocks (diff) keep a container.
2. **Success is louder than failure.** The successful result card has a green
   tint; the failing one has red text on the normal dark fill, so a failure
   is *less* visible when you scan (`c-tool-error.png`). Room inverts this:
   errors and denials carry the fill, success carries nothing.
3. **The right column is three different things stacked in one place**:
   duration (`0.0s`), size (`30 lines`), diff stat (`+5 −0`)
   (`a-pane-full.png`). Room gives size to the fold affordance
   (`… +29 lines`), keeps the diff stat with the diff, and shows duration
   only when it is worth reading (≥ 1 s until #1229 lands, then ms).
4. **A subagent is indistinguishable from the user.** The prompt the agent
   writes for its Task renders in the same right-aligned blue bubble as the
   human's brief, and the subagent's own Bash call and result are top-level
   cards — the transcript reads as if the user said it and the main agent did
   it (`a-pane-full.png`, `f-subagent.png`). Filed as **#1263**; the nesting
   design is §4.5.
5. **The collapsed result preview flattens newlines**, gluing a file's line
   numbers to its text (`1 # Pane Design Notes 2 3 - …`). Filed as **#1265**;
   §4.2 specifies first-line-plus-count instead.
6. **The content is cut off by the input box** with no separator, so the last
   line always looks truncated (`i-input.png`, and at 900px a tool line is
   sliced in half in `k-narrow.png`).
7. **Mid-stream is noisy**: a tool call shows its half-assembled raw JSON
   input (`{"file_path": "/Users/wake/…/pane-des`) until the block closes
   (`live-typing.png`). Room shows the tool name and a stable placeholder
   until the argument is complete.
8. **The cost panel prints the account email** (`h2-cost-panel.png`) — filed
   as **#1264**, and the tooltip stays open behind the panel repeating the
   same line.

## 4. Room

### 4.1 Frame

- Full width, no bubble column, no max-width clamp beyond a comfortable
  reading measure for prose (proposal: prose paragraphs wrap at ~90ch, but
  code, output, diffs and tables use the full width). Today nothing in the
  pane exceeds `max-w-[90%]`, so the width is never actually used (§3.2).
- **One left edge.** Everything — the user's own turns, the agent's prose, tool
  calls, results, thinking, diffs — starts at the same x. Authorship is carried
  by a marker in the gutter, not by alignment:
  - user turn: a distinct gutter mark + slightly stronger text
  - agent prose: no mark
  - tool call: a status dot (running / ok / error / denied), like the `⏺`
  - everything belonging to a call: an indent rail under it (the `⎿`)
- Vertical rhythm by turn, not by message: one turn = the user's line, then
  everything the agent did, then its closing prose.
  **A turn is a container, not a decoration** (user, Q1): the grouping exists
  in the DOM so a turn can be collapsed, scrolled to, searched and — later —
  labelled, but **it draws no separator and shows no per-turn duration or
  cost** in the first pass. The user's own line already marks where a turn
  begins; a rule under it would be a second answer to a question that is
  already answered. Per-turn facts stay available (P-B4 has them) and can be
  switched on later without re-plumbing.
  Turn boundaries are explicit in the data, not inferred: a turn opens on
  `execution.message_accepted` (or the delegate brief for the first one),
  every N2 tool event carries `turn_id`, and a `result` frame closes it — so
  the container is exact even when a turn ends by interrupt.

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
| — | *a preview never joins lines together (#1265): the lines it shows keep their newlines. Where a preview is **one** line — chat's collapsed tool line (§5), a subagent's folded summary (§4.5) — that line is the output's **first** line, not its lines flattened into one.* |
| ≤ 40 lines | first 6 lines + `… +N lines [expand]` |
| > 40 lines, or `truncated` | first 3 lines + `… +N lines [expand]`, and the expand state says whether the daemon itself cut it at 8 KB |
| any `error` / `denied` | one step less folded than the table says |

(Numbers are proposals — they want one session of real use to settle.)

Expansion is per block and remembered per pane while the pane lives; "expand
all" / "collapse all" on the turn separator.

### 4.3 Thinking — measured: the text is usually absent

**Correction to the earlier answer in this conversation.** The *block* is in
the data (`ContentBlock.type === 'thinking'`), but its text very often is not:

- headless `claude -p` runs on mlab returned thinking blocks with
  `"thinking": ""` (signature only) in every sample the capture agent took,
  across two turns including an explicit "think hard" prompt;
- in interactive transcripts on the same machine, 17 of 179 blocks carried
  text in one session and 0 of 30 in another.

`ConversationMessages.tsx:76` renders only when `block.thinking` is truthy,
so today **`ThinkingBlock` never appears in a worker** — it is dead code
there, which is why the screenshots have no `d1`/`d2`.

Design consequence: thinking is an *optional* element, not a pillar. Room
shows a fold only when there is text (`Thought for 4s · 320 words`,
expanding in place on the rail). When a thinking block arrives empty, room
shows **nothing** — not an empty fold — and the fact that the agent thought
is carried by the live typewriter, which already covers that moment. Chat
hides thinking entirely either way (§5).

Open (upstream, not blocking): whether the empty text is a provider setting,
a model behaviour, or something Nexen strips. Worth one question to the
Nexen side when the §9 batch goes out.

### 4.4 Diff

As P-B3 built it (line numbers, +/-), full width, with the display budget from
#1227 applied: fold hunks beyond the first N lines, per §4.2's rule, and say
when the daemon's 2000-line cap truncated it.

### 4.5 Subagents (子行程)

A `Task` call is a block whose result is *another agent's* run. Render it as a
nested rail: the Task block folds to one line (`Task · 分析 notes.md · 8 tools
· 12s`) and expands into the child's own blocks, one indent deeper.

Two things must be true before the nesting reads correctly, and neither is
today (`f-subagent.png`):

- **attribution** — a frame with `parent_tool_use_id != null` belongs to the
  child, not to the user and not to the main agent. Today the child's prompt
  renders as the human's bubble and its tool calls render as top level
  (#1263, #1228). The rail is what fixes it visually; the gate on
  `parent_tool_use_id` is what fixes it semantically.
- **order** — the hand-back result currently appears *after* the child's own
  summary, so the transcript reads backwards; and the hand-back prints raw
  JSON instead of its text (#1263).

Still missing from the wire (§9 A2): a subagent has no result frame of its
own, so its cost and end time cannot be separated from the parent's
(measured in P-B4).

### 4.6 Persistent things (常駐)

A worker accumulates things that outlive a turn: background shells, dev
servers, watches, and — if we choose to show it — the lease and observers.
These do not belong in the transcript, because the transcript is chronological
and these are *current*. **It lives inside the pane** (user, Q3), not on the
tab bar or a global strip: it is this worker's state, so it travels with the
worker's pane and two panes on different workers each show their own. Collapsed
to a single row by default:

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
directly under the content with a hairline separator (today there is none, so
the transcript always looks truncated — §3.1.1 #6), growing with the text **up
to a ceiling** (today it has none, so a long paste squeezes the conversation to
nothing — §3.2).
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

`mode` on the pane content, **always defaulting to `room`** (user, Q2: chat is
something you switch into; whether any context should pick it by itself — the
phone, a narrow viewport — is deferred until chat exists and has been used).
Instant, local, no daemon call,
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

**Answered 2026-09-20:**

- **Q1 — the turn separator.** No separator, no per-turn duration or cost for
  now ("我看看情況再調整"). The turn stays as a container (§4.1).
- **Q2 — chat's default.** Deferred; room is always the default (§6).
- **Q3 — the dock.** Inside the pane (§4.6).

- **Q4 — folding numbers.** §4.2's table is the starting point; tune after a
  session of real use.
- **Q5 — the wire asks.** **Not in parallel.** §9 goes to Nexen *after* the
  SPA phases, when we know which asks are still needed. R4 therefore sits at
  the end of §11 and nothing in R1–R3 may depend on new wire data.

## 11. Phases (proposal, after §10 is answered)

1. **R1 — the frame**: one left edge, turn grouping, the folding rule applied
   to every block type, header slimming, dock with only the facts we have.
   Pure SPA, no new data.
2. **R2 — chat**: the second view, the `mode` field, the switch.
3. **R3 — input**: search, quick replies.
4. **R4 — last, after the §9 batch has been sent and shipped** (Q5: not in
   parallel): dock with real running-shell data, subagent cost/end time,
   sidebar rollups. R1–R3 must stand on their own without it.

Bugs found while capturing §3.1 that can be fixed independently of the phases,
and should be: **#1263** (subagent attribution), **#1265** (collapsed preview
flattens newlines — R1 needs the corrected behaviour anyway), **#1264**
(account email in the cost panel). Existing: #1226, #1227, #1228, #1229,
#1234, #1235.
