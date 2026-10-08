A real peer message that starts a turn in an idle session (origin `peer`, `isMeta`), and the Bash reply the model sends back, recorded from Claude Code 2.1.294.

- Recorded with `claude --model sonnet` (2.1.294, default permission mode) in a throwaway tmux session and git repo. Once the session was idle and routable, one message was sent from another session with `pdx msg send <address> "fixture ping: reply with the single word pong"`. The model answered by running `pdx msg send` through Bash (one permission dialog, approved). Side effect of the recording: one real "pong" message reached the recording session. Scrubbed with `scrubfixture`; paths are `/work/…`.
- Turn 1: an `isMeta` user row with `origin.kind: peer`, `promptSource: system`, `turnOrigin: peer` and a `<cross-session-message from=… from-name=…>` wrapper → source `peer`, text = the message body (`fixture ping: reply with the single word pong`), `from.kind: peer`, `from.name` = the sender's pdx address. The "This came from another Claude session…" paragraph after the wrapper is part of the same row's text but outside the wrapper. One empty `thinking` row (dropped), one Bash `tool_use` (`execute`, done, 1-line result, 89 bytes), one reply, `turn_duration`.
- Turn 2: `/exit` (slash, with its stdout row). The `isMeta` caveat row is skipped.
- This build has no MultiEdit / Grep / Glob tools.
- Closed session (`live: false`).

Notes for the reader
- The peer row is `isMeta` yet opens the turn (the spec's exception). It has no `turnPosition`/`promptIndex` in this file; the opening decision rests on the spec's `origin.kind: "peer"` rule alone.
- The `from-name` in the input is the recorder's real pdx address; the facts do not repeat it.
