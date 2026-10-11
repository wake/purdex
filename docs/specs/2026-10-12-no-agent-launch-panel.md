# No-agent launch panel — resume the last conversation or start a new one from the deck / chat (spec + plan, rev 1)

## 1. User decisions (2026-10-12, do not reopen)
- A session with **no agent running**, opened in the deck or the chat (Mac and iOS alike), shows a launch panel instead of
  「這個 session 沒有 Claude Code 對話」. It is the rebuild screen's idea applied to a live session: tmux, the project
  directory, then a quick button to **resume the last conversation** or **start a new one**.
- The rows: 「在 tmux 裡」 (disabled — already in tmux), 「切到專案目錄」 (disabled when already there), and two quick buttons
  「接續上次對話」／「開新對話」.
- 「開新對話」 starts **the agent this session last ran** (no agent picker); with no record, Claude Code.
- Mac and iOS change together. 「切到終端機」 is always reachable from the panel (iOS today hides the mode switch in this
  state, so the terminal cannot be reached — fixed by this).

## 2. When the panel shows
- The deck / chat would show the "no conversation" state: Mac `DeckUnreadable` reason `no_session` (`DeckPane.tsx`) and the
  chat's equivalent; iOS `DeckScreen`'s 「這個 session 沒有 Claude Code 對話」 and the chat's equivalent.
- Not for the other unreadable reasons (`not_found`, `unreachable`, `provider_unsupported`): they keep their message,
  retry and 「切到終端機」 as today.
- A worker (Nexen execution) has no terminal and never shows it.

## 3. Data (no daemon change)
- **Last agent**: `GET /api/sessions/{code}/provenance` → `{found, agent_type, session_id, cwd, last_seen_at, …,
  tmux_instance}`. Read when the panel mounts and on 「重新整理」; a provenance whose `tmux_instance` differs from the
  session's is ignored (the code was reused).
- **Where the pane is**: the sessions list item's `cwd` (tmux pane current path) and `current_command` (the pane's
  foreground program); `branch` (#2707) may be shown next to the directory.
- **Commands**: the host's resume templates (`hostconfig` `resumeTemplates`, defaults in `spa/src/lib/resume-templates.ts`:
  cc `claude --resume {id}` / `claude -c`, codex `codex resume {id}` / `codex resume --last`, opencode `opencode -s {id}` /
  `opencode -c`). New: a start table, same keys — cc `claude`, codex `codex`, opencode `opencode` (not configurable yet).

## 4. The panel
```
這個 session 目前沒有在跑 agent
  ☑ 在 tmux 裡                                   (always disabled: a live session is a tmux session)
  ☐ 切到專案目錄  ~/Workspace/tangency/at-inwin-plugin  (main)
  [ 接續上次對話 ]  [ 開新對話 ]
  切到終端機 →
```
- **Project directory** = provenance `cwd` when found, else (Mac) the pane's rebuild-record cwd; none → the row is hidden.
  The checkbox defaults **on** when the pane's current `cwd` differs from it, and is **disabled (shown as 「已在這個目錄」)**
  when equal (compare after trimming one trailing `/`).
- **接續上次對話**: enabled when provenance is found and its agent type has a template; the command is the template's
  `exact` with `{id}` = `session_id` when the id is usable, else its `fallback`. Disabled (with a short reason in its
  tooltip / accessibility hint, 「沒有上一次的對話紀錄」) otherwise.
- **開新對話**: the start command of provenance's agent type; unknown type or no provenance → `claude`.
- Each button's tooltip (Mac) / accessibility hint and long-press preview (iOS) shows the **exact line that will be typed**.
- **Busy pane guard**: when `current_command` is present and is not a shell (`zsh`, `bash`, `fish`, `sh`, `dash`, `ksh`,
  `tcsh`, `nu`, `pwsh`, a leading `-` stripped, case-insensitive), both buttons are disabled with
  「這個 pane 正在執行 {cmd}，請先到終端機結束它」 and 「切到終端機」 is emphasised. Absent `current_command` → allowed.
- No animation.

## 5. Running it
- The typed line: `cd <dir> && <command>` when the directory row is checked and enabled, else `<command>`. `<dir>` is
  shell-quoted (single quotes, `'` → `'\''`; a leading `~/` stays unquoted as `~/` + the quoted rest).
- Sent with the **generation-guarded send-keys** the rebuild uses: `POST /api/sessions/{code}/send-keys` with the session's
  `tmux_instance` (Mac `session-launch.ts` / the rebuild transport; iOS `DaemonClient.sendKeys(code, keys,
  expectedTmuxInstance:)`), body `{keys: line + "\n", expected_tmux_instance}` — exactly as the rebuild transport
  (`spa/src/lib/rebuild/transport.ts` `sendKeys`) sends its resume command. No `tmux_instance` on the session → the buttons
  are disabled (「無法確認這個 session，請重新整理」), never an unguarded send.
- While the send is in flight both buttons are disabled; an error shows inline under the buttons (409, the generation
  mismatch → 「這個 session 已經換了一個，請重新整理」; other errors → the HTTP status text). Success needs no message: once the agent's hooks arrive the deck / chat
  switches to the conversation by itself (existing behaviour). The panel never switches to the terminal on its own.

## 6. Phases (one PR each, ≤ 800 lines or ≤ 20 files)
- **NA-1 Mac (SPA)**: a `NoAgentLaunchPanel` component used by `DeckPane` for `no_session` and by the chat's equivalent;
  a pure `launchPlan({provenance, session, templates, cdChecked})` → `{dir, cdState, resume: {command|null, reason},
  fresh: {command}, busy: string|null, line(kind)}`; i18n zh-TW + en. Tests: §2 when / when not; §4 every row state
  (provenance found / not / reused instance; cwd equal / differs / none; id usable / not; unknown agent; busy shell vs
  program; missing current_command); §5 the typed line (quoting incl. `'` and `~/`, with and without cd), send path called
  with the tmux instance, in-flight disable, error line; chat uses the same panel; deck mode unchanged for readable
  conversations. Mutation: drop the busy guard ⇒ red; drop the cd-equal check ⇒ red.
- **NA-2 iOS**: same panel and rules in `DeckScreen` / the chat view; the mode switch is visible in this state; same tests
  in Swift; OTA as usual.
- NA-1 and NA-2 are independent (no daemon change) and can merge in either order.
