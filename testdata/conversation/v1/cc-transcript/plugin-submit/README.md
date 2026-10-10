Recorded Claude Code session driven by a mod's `$.prompt.submit`, both framed and `asUser: true` (U3-0b: how the Apps send).

- Recorded with Claude Code 2.1.296 (`claude --model haiku`) in a throwaway tmux session; a throwaway mod called `$.prompt.submit` on command with throwaway prompts ("Reply with exactly: PROBE-…"). Scrubbed with `scrubfixture`.
- Rows of interest: a `user` row with `origin {kind: "plugin", name}` and `promptSource` / `turnOrigin` `system` whose text is the framed "The <name> plugin sent a message: …" (three of them) — **a message from the plugin, not the person's: a `user` item with source `peer`, `from {kind: "plugin", name}`, the frame and the footer taken off, its own turn** (#2396; before that these rows were skipped and their answers attached to the turn before); and two rows with `origin {kind: "plugin", name, asUser: true}` and the bare text — **the person's words sent on their behalf: a `user` item, source `user`** (what the Apps' send produces, so the App's local echo finds its message, U3-0b).
- One `task-notification` turn (the background `sleep` finishing), three `Bash` steps (the first blocked by Claude Code: `failed`).
- Live: `false`.
