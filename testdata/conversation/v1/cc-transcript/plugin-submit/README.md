Recorded Claude Code session driven by a mod's `$.prompt.submit`, both framed and `asUser: true` (U3-0b: how the Apps send).

- Recorded with Claude Code 2.1.296 (`claude --model haiku`) in a throwaway tmux session; a throwaway mod called `$.prompt.submit` on command with throwaway prompts ("Reply with exactly: PROBE-…"). Scrubbed with `scrubfixture`.
- Rows of interest: a `user` row with `origin {kind: "plugin", name}` and `promptSource` / `turnOrigin` `system` whose text is the framed "The <name> plugin sent a message: …" (three of them) — **not the person's, skipped** (counted as `origin:plugin`); and two rows with `origin {kind: "plugin", name, asUser: true}` and the bare text — **the person's words sent on their behalf: a `user` item, source `user`** (this is what the Apps' send produces, so the App's local echo finds its message).
- Consequence worth knowing: a skipped framed prompt opens no turn, so the assistant rows it caused attach to the open turn before it (turn 1 holds the answers to the second framed prompt and the background command), or open a turn with no user item (turn 0). That is the existing rule for plugin prompts, not changed here.
- One `task-notification` turn (the background `sleep` finishing), three `Bash` steps (the first blocked by Claude Code: `failed`).
- Live: `false`.
