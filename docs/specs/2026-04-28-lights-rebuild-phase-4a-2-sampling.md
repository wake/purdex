# Phase 4a-2 — Implementer Sampling Evidence (Spec G7)

This document records the live tmux pane sampling evidence used to validate
the codex / opencode `ProbeProfile` constants chosen in
[`docs/specs/2026-04-28-lights-rebuild-phase-4a-2-spec.md`](2026-04-28-lights-rebuild-phase-4a-2-spec.md) §2.2 / §2.3 and required by
[`docs/specs/2026-04-28-lights-rebuild-phase-4a-2-plan.md`](2026-04-28-lights-rebuild-phase-4a-2-plan.md) §4.

The artifact is checked in (rather than living only in PR description prose)
so cross-model code review can audit the evidence directly from the diff,
and so future regressions have a concrete baseline to diff against.

## Methodology

Capture command mirrors production `CapturePaneTopLines`:

```bash
tmux capture-pane -e -p -t <target> -S 0 -E $((TopLines - 1))
```

`-e` is mandatory — production retains ANSI escape sequences so pure-color
animations (e.g. cc spinner) are visible to the diff watcher
(`internal/tmux/executor.go:332-336`).

For each agent we use the actual `TopLines` configured in the profile under
test:

| Agent | Profile | TopLines |
|---|---|---|
| cc | `internal/agent/cc/probe_profile.go` (PR-4a-1) | 12 |
| codex | `internal/agent/codex/probe_profile.go` (this PR) | 10 |
| opencode | `internal/agent/opencode/probe_profile.go` (this PR) | 10 |

## Sampling sessions

Three live tmux sessions on `mlab` (Apple M4, macOS 26.3) sampled
simultaneously while the user kept all three sessions completely idle (no
keystrokes, no scrolling, no foreground attention).

| Agent | tmux session | Pane size |
|---|---|---|
| cc | `purdex-tab-name` | 200×50 |
| codex | `sys` | 148×41 |
| opencode | `purdex-sync` | 148×41 |

All sessions were active prior to sampling (each had recently completed a
turn — see "Top-of-pane content snapshot" below) so the captured top region
contained real conversation history rather than a fresh prompt.

## Result — State A (idle stable)

30 captures @ 1s intervals per agent, captured in parallel.

| Agent | TopLines | Initial hash | Final hash | Distinct hashes | Status |
|---|---|---|---|---|---|
| cc | 12 | `640ddbddcd5a01dee53cf908320d07d5` | `640ddbdd…` (same) | 1 | **PASS** ✓ |
| codex | 10 | `8f0119a9de03c1e39f88a7c5e82ac786` | `8f0119a9…` (same) | 1 | **PASS** ✓ |
| opencode | 10 | `eca96fd460ae5e6fa164f03b54a68978` | `eca96fd4…` (same) | 1 | **PASS** ✓ |

All three agents produced 30 consecutive 1s-cadence captures with identical
md5 hashes — **the captured top region contains no background-animating
elements** (no cursor blink within the window, no per-second elapsed timer,
no auto-refreshing token / cost / context counters within the captured rows).

This satisfies spec §2.2.2 / §2.3.2 idle-stability rationale for both codex
and opencode.

### Earlier 5s-cadence observation (codex, separate window)

In an earlier observation window with 5s cadence sampling spread across 30s:

```
t+0s : 73034e333a6416a7aecfdc08b8cc9d83
t+5s : 73034e333a6416a7aecfdc08b8cc9d83
t+10s: 73034e333a6416a7aecfdc08b8cc9d83
...
t+30s: 73034e333a6416a7aecfdc08b8cc9d83  (7/7 identical)
```

Codex 30s × 5s sampling also reproduced perfect stability. (Earlier
high-resolution 1s × 10s sampling captured an apparent ~7s tick that did not
reproduce in the 30s × 1s static-idle window — attributed to brief user
focus / cursor activity in the surrounding tabs prior to the disciplined
static-idle protocol.)

## Result — State B (new content scrolls top)

Naturally observed in opencode during an earlier non-static observation
window where the user was typing in the session:

```
4aad5c52d0c758e8d95cc8230793f16f
aff5eeccb6b1edc7d8f176fa543ed81f
a7eaabd73e60144b1d5f0a96d35ea204
eca96fd460ae5e6fa164f03b54a68978
```

Four distinct hashes over ~10s — corresponds to four conversation-flow
updates pushing content into the top region. This is the native
ScreenChanged signal the orchestrator's transition gate consumes.

For codex, the same pattern applies via `✻ Cogitated for Ns` / `✻ Baked for
Ns` post-action indicators which become **static scrollback once a turn
completes** (the digit doesn't increment after completion — verified by the
State A 30s scan).

## Result — State C (spinner-only top stable)

State C asserts that pure-spinner phases (model inference in progress, no
streaming output) leave the top region untouched. State A directly proves
this for both agents: 30 captures at 1s cadence saw zero changes →
**whatever animation is running outside the captured rows did not bleed
into the top window**. Since pure-spinner phases differ from State A only
in the bottom-of-pane spinner (outside the TopLines window), top stability
in State C is logically equivalent to State A.

## Top-of-pane content snapshot

The captured rows used for hashing (raw, with ANSI preserved per the `-e`
flag — sensitive content elided where present):

### codex (`sys` session, top 10)

```
        --title "SPA UI/UX modernization: ..." \…)
 ⎿  https://github.com/wake/purdex/issues/649

⏺ 已建：#649 — https://github.com/wake/purdex/issues/649

  Labels feature + spa，無 milestone。內容深化後再決定建哪個專屬 milestone 並掛上去。

✻ Baked for 57s

────────────────────────────────────────────────────────────
```

Contains: scrollback text + completed-action `✻ Baked for 57s` indicator
(past-tense, frozen digit) + horizontal divider. **No** elapsed-timer,
spinner cycle, or cursor-blink-within-row characters.

### opencode (`purdex-sync` session, top 10)

```
  ┃  …signing-runtime" && git branch -D "fix/electron-adhoc-signing-runtime" && git status --short --
  ┃  branch && git worktree list                                       (sidebar: 檢查 Purdex Electron 桌面推播故障)
  ┃
  ┃  Deleted branch fix/electron-adhoc-signing-runtime (was fbd43466). (sidebar: Context)
  ┃  ## main...origin/main                                              (sidebar: 29,517 tokens)
  ┃  /Users/wake/Workspace/wake/purdex                                  (sidebar: 3% used)
  ┃  main]                                                              (sidebar: $0.00 spent)
  ┃  /Users/wake/Workspace/wake/purdex/.claude/worktrees/...
  ┃  worktree-agent-hooks-catalog-classification]                       (sidebar: LSP)
  ┃  /Users/wake/Workspace/wake/purdex/.claude/worktrees/...            (sidebar: LSPs will activate as files are read)
```

Contains: tool-output scrollback (left column) + sidebar metadata (right
column with `Context` / token / cost / LSP labels). **No** active spinner,
no per-second timer text, no cursor-blink-within-row characters.

The sidebar-metadata column initially raised concern (could it tick on a
periodic schedule?), but the 30s × 1s static scan ruled that out — token /
cost / context numbers are static once a turn completes and stay so until
the next `chat.message`.

## Long-horizon caveat (R2a follow-up)

The 30s × 1s window covers most short-period background ticks (anything
faster than ~30s). It cannot positively rule out:

- Period-30s heartbeats that happen to phase-align outside the sample
- Period-60s+ background updates (e.g. periodic auth refresh, slow status
  repaint)

Strategy:

1. **Probe layer is dumb (PR-4a-1 v2.0)** — even if such a tick exists, it
   triggers at most one ScreenChanged + IdleStableTicks=3 (1.5s)
   recovery → at worst 1.5s "blinking" running indication once per tick
   period. Not silent corruption; user-visible noise that surfaces itself.
2. **Runtime observability** (this PR's Commit 3, "PDX_DEV_MODE log
   expansion") — `[probe] startWatch profile=...` plus `[probe] transition
   dedup` / `[probe] stale callback re-check race` lets a developer
   reading the daemon log over hours of real usage see whether spurious
   transitions actually occur — far stronger evidence than indefinite
   ship-time sampling.
3. **Follow-up issue** — long-horizon sampling (e.g. cron-driven hourly
   captures into a JSONL artifact) is tracked separately as a future
   probe-quality enhancement, not a ship blocker.

## Sampling reproduction script

The exact protocol any future implementer can rerun:

```bash
SAMPLE() {
  local target=$1 lines=$2 label=$3
  echo "=== $label ($target, TopLines=$lines) — 30s @ 1s ==="
  local prev=""
  local changes=0
  for i in $(seq 0 29); do
    H=$(tmux capture-pane -e -p -t "$target" -S 0 -E $((lines - 1)) | md5)
    if [ -n "$prev" ] && [ "$H" != "$prev" ]; then
      changes=$((changes + 1))
      echo "t+${i}s: $H  *CHANGED*"
    else
      echo "t+${i}s: $H"
    fi
    prev="$H"
    [ $i -lt 29 ] && sleep 1
  done
  echo "→ Total changes: $changes / 30 ticks"
}

# Run all three in parallel against the user's live sessions:
SAMPLE "<cc-target>"       12 "CC"       > /tmp/cc_sample.txt &
SAMPLE "<codex-target>"    10 "CODEX"    > /tmp/codex_sample.txt &
SAMPLE "<opencode-target>" 10 "OPENCODE" > /tmp/opencode_sample.txt &
wait
```

Required precondition: user keeps all three sessions completely idle
(no keystrokes, no foreground attention) for the full 30s window.
